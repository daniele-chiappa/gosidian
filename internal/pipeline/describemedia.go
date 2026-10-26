package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/clock"
	"github.com/gosidian/gosidian/internal/frontmatter"
)

// llm.describe_media (IMP-179): the image notes of a note (those in.import
// keeps in <note>_img/) get a description from a vision model, with the
// text around the image in the note as context: the label as the note's
// title and label, the description as the body, the searchable text of an
// image note (ADR-013). Each note is written as soon as it is described, so
// a step cut short, or a run started again, goes on from the images still
// without a description.

const (
	defaultMediaPrompt = `An image of the document "{title}", section: {section}.
The text around it (` + imageMark + ` is where it stands):
{context}

Describe the image for someone who searches the document without seeing it: what it shows, the text, fields, buttons and values you can read in it, and what it illustrates in the text. Answer in the language of the document, as JSON: {"label": "a title of 5 to 10 words", "description": "at most 120 words"}.`

	defaultInlineMediaPrompt = `A small image inside a sentence of the document "{title}" (` + imageMark + ` is where it stands):
{context}

Say what it is (an icon, a button, a symbol) and what it stands for in the sentence. Answer in the language of the document, as JSON: {"label": "at most 6 words", "description": "one sentence"}.`

	// mediaMaxConcurrency caps the calls a step makes at once.
	mediaMaxConcurrency = 8
	// mediaAttempts are the tries for one image.
	mediaAttempts = 3
	// defaultMediaMaxPixels is the largest image the model gets, about
	// 1920×1040: some 2000 tokens for Qwen-VL, inside Ollama's default
	// context of 4096 with the prompt and the answer.
	defaultMediaMaxPixels = 2_000_000
	// defaultInlineMaxSide is the longest side, in pixels, of an image in
	// a sentence that gets the inline prompt: a larger one is a screenshot
	// put in the line, and gets the full prompt (BUG-124).
	defaultInlineMaxSide = 128
)

// mediaRetryWait is the wait after a failed try, times the try's number.
var mediaRetryWait = 2 * time.Second

// mediaJob is an image note to describe.
type mediaJob struct {
	path, image, prompt string
	raw                 []byte
}

func llmDescribeMedia(ctx context.Context, env *Env, with map[string]any) (any, error) {
	started := time.Now()
	n, err := required(with, "note")
	if err != nil {
		return nil, err
	}
	article, err := env.projectPath(n)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(article, ".md") {
		article += ".md"
	}
	host := env.Engine.host
	lister, ok := host.(Folders)
	if !ok {
		return nil, errors.New("this server does not list the image notes of a note")
	}
	p, err := env.Engine.provider(str(with, "provider"))
	if err != nil {
		return nil, err
	}
	model := str(with, "model")
	if model == "" {
		model = p.Model
	}
	version := int(number(with, "prompt_version", 1))
	chars := int(number(with, "context_chars", 1200))
	workers := min(max(int(number(with, "concurrency", 1)), 1), mediaMaxConcurrency)
	inlineMax := int(number(with, "inline_max_side", defaultInlineMaxSide))
	// Which images this pass takes: an icon (an image in a sentence up to
	// inline_max_side) may want another model than a screenshot, and on
	// one GPU that is another pass (IMP-179).
	only := strings.TrimSpace(str(with, "images"))
	switch only {
	case "", "all", "icons", "others":
	default:
		return nil, fmt.Errorf("with.images %q: all, icons or others", only)
	}
	prompt, inlinePrompt := str(with, "prompt"), str(with, "inline_prompt")
	if prompt == "" {
		prompt = defaultMediaPrompt
	}
	if inlinePrompt == "" {
		inlinePrompt = defaultInlineMediaPrompt
	}

	art, err := host.ReadNote(env.Project, article)
	if err != nil {
		return nil, err
	}
	title := art.Title
	if t, _ := art.Frontmatter["title"].(string); t != "" {
		title = t
	}
	lines := strings.Split(art.Body, "\n")
	first := map[string]imageRef{}
	for _, r := range scanImages(lines) {
		if _, ok := first[r.URL]; !ok {
			first[r.URL] = r
		}
	}
	paths, err := lister.NotesIn(env.Project, mediaFolder(article))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	var jobs []mediaJob
	skipped, unreadable, excluded := 0, 0, 0
	for _, mp := range paths {
		note, err := host.ReadNote(env.Project, mp)
		if err != nil || !isMediaOf(note.Frontmatter, article) {
			continue
		}
		if strings.TrimSpace(note.Body) != "" && !truthy(with["redo"]) {
			skipped++
			continue
		}
		img, _ := note.Frontmatter["media"].(string)
		ref, ok := first[attach.FileURL(img)]
		if !ok {
			skipped++ // no longer in the note
			continue
		}
		if _, ok := imageMIME[strings.ToLower(path.Ext(img))]; !ok {
			unreadable++ // svg: no model reads it
			continue
		}
		icon := ref.Inline && (inlineMax <= 0 || longestSide(note.Frontmatter) <= inlineMax)
		if (only == "icons" && !icon) || (only == "others" && icon) {
			excluded++ // left to another pass
			continue
		}
		tmpl := prompt
		if icon {
			tmpl = inlinePrompt
		}
		text := strings.NewReplacer("{title}", title, "{section}", sectionAt(lines, ref.Line),
			"{context}", imageContext(lines, ref, chars)).Replace(tmpl)
		jobs = append(jobs, mediaJob{path: mp, image: img, prompt: text, raw: note.Raw})
	}

	var (
		mu                sync.Mutex
		used              = map[string]float64{}
		described, failed int
		downscaled        int
		errs              []string
		stop              error
		wg                sync.WaitGroup
	)
	queue := make(chan mediaJob)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range queue {
				mu.Lock()
				if stop == nil && ctx.Err() != nil {
					stop = ctx.Err()
				}
				if stop != nil {
					mu.Unlock()
					continue
				}
				call := *env
				call.tokens = env.tokens + int(used["total_tokens"])
				mu.Unlock()
				out, err := env.describeMediaNote(ctx, &call, with, model, version, j)
				mu.Lock()
				if u, ok := out["usage"].(map[string]any); ok {
					for k, v := range u {
						f, _ := v.(float64)
						used[k] += f
					}
				}
				var capped tokenCapError
				switch {
				case errors.As(err, &capped), err != nil && ctx.Err() != nil:
					if stop == nil {
						stop = err
					}
				case err != nil:
					failed++
					if len(errs) < 10 {
						errs = append(errs, j.path+": "+err.Error())
					}
				default:
					described++
				}
				if out["downscaled"] == true {
					downscaled++
				}
				mu.Unlock()
			}
		}()
	}
	for _, j := range jobs {
		queue <- j
	}
	close(queue)
	wg.Wait()

	if stop != nil {
		return nil, fmt.Errorf("stopped after describing %d images (the ones described are kept): %w", described, stop)
	}
	if failed > 0 && failed*2 > described+failed {
		return nil, fmt.Errorf("%d of %d images failed: %s", failed, described+failed, strings.Join(errs, "; "))
	}
	if errs == nil {
		errs = []string{}
	}
	return map[string]any{
		"described": described, "skipped": skipped, "unreadable": unreadable, "excluded": excluded, "failed": failed, "errors": errs, "downscaled": downscaled,
		"usage":   map[string]any{"prompt_tokens": used["prompt_tokens"], "completion_tokens": used["completion_tokens"], "total_tokens": used["total_tokens"]},
		"seconds": time.Since(started).Round(time.Second).Seconds(),
	}, nil
}

// tokenCapError is a call that the run's cap of LLM tokens forbids.
type tokenCapError struct{ used, cap int }

func (e tokenCapError) Error() string {
	return fmt.Sprintf("the run used %d LLM tokens, and the next call could pass the cap of %d ([pipelines] max_llm_tokens)", e.used, e.cap)
}

// describeMediaNote asks the model about one image, a few times, and writes
// the answer in its note; a note it gave up on gets describe_error. The
// output holds the usage of every call, failed or not.
func (env *Env) describeMediaNote(ctx context.Context, call *Env, with map[string]any, model string, version int, j mediaJob) (map[string]any, error) {
	usage := map[string]any{}
	add := func(out map[string]any) {
		if u, ok := out["usage"].(map[string]any); ok {
			for k, v := range u {
				f, _ := v.(float64)
				prev, _ := usage[k].(float64)
				usage[k] = prev + f
			}
		}
	}
	maxTokens := int(number(with, "max_tokens", 600))
	callWith := map[string]any{"provider": with["provider"], "model": model, "max_tokens": float64(maxTokens), "json": true}
	if t, ok := with["temperature"].(float64); ok {
		callWith["temperature"] = t
	}
	res := map[string]any{"usage": usage}
	mime := imageMIME[strings.ToLower(path.Ext(j.image))]
	data, err := env.Engine.host.ReadAttachment(env.Project, j.image)
	if err == nil {
		var same bool
		if data, same, err = downscale(data, int(number(with, "max_pixels", defaultMediaMaxPixels))); err == nil && !same {
			mime, res["downscaled"] = "image/png", true
		}
	}
	if err == nil && len(data) > maxImageBytes {
		err = fmt.Errorf("%s is %d bytes, at most %d", j.image, len(data), maxImageBytes)
	}
	var msgs []chatMessage
	if s := str(with, "system"); s != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: s})
	}
	msgs = append(msgs, imageMessage(j.prompt, mime, data))
	var label, description string
	var broken bool
	for attempt := 1; err == nil; attempt++ {
		if cap := env.Engine.cfg.MaxLLMTokens; cap > 0 {
			spent, _ := usage["total_tokens"].(float64)
			if n := call.tokens + int(spent); n+maxTokens > cap {
				return res, tokenCapError{used: n, cap: cap}
			}
		}
		var out map[string]any
		out, err = env.Engine.chat(ctx, call, callWith, msgs, nil)
		add(out)
		if err == nil {
			// A JSON that does not read is tried again; the last try keeps
			// what can be read of it (BUG-125).
			label, description, broken = mediaAnswer(out)
			if description != "" && (!broken || attempt >= mediaAttempts) {
				break
			}
			err = errors.New("the model gave no description")
			if broken {
				err = errors.New("the model's JSON does not read")
			}
		}
		if ctx.Err() != nil || attempt >= mediaAttempts {
			break
		}
		wait := time.Duration(attempt) * mediaRetryWait
		var ra RetryAfter
		if errors.As(err, &ra) && ra.After > wait {
			wait = min(ra.After, 30*time.Second)
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		err = nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return res, err
		}
		c, ferr := frontmatter.SetKeys(j.raw, []frontmatter.Field{{Key: "describe_error", Value: oneLine(err.Error(), 300)}}, nil)
		if ferr == nil {
			_, _ = env.Engine.host.WriteNote(env.Project, j.path, c, WriteUpsert, Actor(env.Run.Ref))
		}
		return res, err
	}
	set := []frontmatter.Field{
		{Key: "model", Value: model},
		{Key: "prompt_version", Value: version},
		{Key: "described", Value: clock.NoteDate(env.Engine.now())},
	}
	unset := []string{"describe_error"}
	if label != "" {
		set = append([]frontmatter.Field{{Key: "title", Value: label}, {Key: "label", Value: label}}, set...)
	} else {
		unset = append(unset, "title", "label")
	}
	c, err := frontmatter.SetKeys(j.raw, set, unset)
	if err != nil {
		return res, err
	}
	if _, err := env.Engine.host.WriteNote(env.Project, j.path, withBody(c, description), WriteUpsert, Actor(env.Run.Ref)); err != nil {
		return res, err
	}
	return res, nil
}

// mediaAnswer reads a model's answer: label and description from its JSON,
// else its text as the description. An answer that looks like JSON (it
// starts with { or a fence) but does not read is broken: what can be read
// of its label and description, which the caller takes only at the last
// try (BUG-125). Prose stays the description, as written.
func mediaAnswer(out map[string]any) (label, description string, broken bool) {
	if j, ok := out["json"].(map[string]any); ok {
		label, _ = j["label"].(string)
		description, _ = j["description"].(string)
	}
	if strings.TrimSpace(description) == "" {
		text, _ := out["text"].(string)
		description = text
		if t := strings.TrimSpace(text); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "```") {
			broken = true
			label, description = salvageAnswer(t)
		}
	}
	return oneLine(label, 120), strings.TrimSpace(description), broken
}

var (
	answerLabelRe = regexp.MustCompile(`"label"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	answerDescRe  = regexp.MustCompile(`(?s)"description"\s*:\s*"(.*)`)
	// answerBreakRe is a string closed and another opened in the middle of
	// the description: «…\"  ,  "contiene…».
	answerBreakRe = regexp.MustCompile(`"\s*,\s*"`)
)

// salvageAnswer reads what it can of a JSON answer that does not parse:
// the label's string, and the description's text to the end, its stray
// quotes and the JSON's closing taken out.
func salvageAnswer(t string) (label, description string) {
	if m := answerLabelRe.FindStringSubmatch(t); m != nil {
		label = unescapeJSON(m[1])
	}
	if m := answerDescRe.FindStringSubmatch(t); m != nil {
		d := strings.TrimRight(strings.TrimSpace(m[1]), "`} \n")
		d = strings.TrimSuffix(strings.TrimSpace(d), "\"")
		description = unescapeJSON(answerBreakRe.ReplaceAllString(d, " "))
	}
	return label, description
}

// unescapeJSON is the text of a JSON string's content; what does not read
// keeps its characters, its common escapes taken out.
func unescapeJSON(s string) string {
	var out string
	if json.Unmarshal([]byte(`"`+s+`"`), &out) == nil {
		return out
	}
	return strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, " ", `\t`, " ").Replace(s)
}

func longestSide(fm map[string]any) int {
	side := 0
	for _, k := range []string{"width", "height"} {
		n := 0
		switch v := fm[k].(type) {
		case int:
			n = v
		case int64:
			n = int(v)
		case float64:
			n = int(v)
		case string:
			n, _ = strconv.Atoi(strings.TrimSpace(v))
		}
		side = max(side, n)
	}
	return side
}
