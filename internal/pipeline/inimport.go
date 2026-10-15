package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // the sizes of the images in.import stores
	_ "image/jpeg"
	_ "image/png"
	"path"
	"sort"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/frontmatter"
	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/pkgimport"
	"github.com/gosidian/gosidian/internal/vault"
)

// in.import (IMP-179): a markdown file of the IN folder becomes a note of
// the project, its tree under with.base kept under with.dest. The files it
// links are stored as attachments (named by their content: a file linked
// twice is stored once) and its links rewritten to them, as a package
// import does; the rest of the text and the frontmatter stay as written.
// Each image gets an image note (ADR-013) in <note>_img/, which points at
// the image (media) and at the note (article): llm.describe_media writes
// its description there, so the document itself is not changed.

// importedImage is an image in.import stored: where it came from and what
// its image note says about it.
type importedImage struct {
	src, inFile   string // its path under base, and in the IN folder
	sha           string // the original's, in the IN folder
	width, height int
}

func inImport(ctx context.Context, env *Env, with map[string]any) (any, error) {
	f, err := required(with, "file")
	if err != nil {
		return nil, err
	}
	file, err := env.inPath(f)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(path.Ext(file), ".md") {
		return nil, fmt.Errorf("%s is not a markdown file", file)
	}
	root := vault.INRoot(env.Project)
	src := strings.TrimPrefix(file, root+"/")
	base := inRelative(str(with, "base"))
	if base != "" {
		if !strings.HasPrefix(src, base+"/") {
			return nil, fmt.Errorf("%s is not under with.base %q", file, base)
		}
		src = strings.TrimPrefix(src, base+"/")
	}
	d, err := required(with, "dest")
	if err != nil {
		return nil, err
	}
	dest, err := env.projectPath(d)
	if err != nil {
		return nil, err
	}
	if dest == root || strings.HasPrefix(dest, root+"/") {
		return nil, fmt.Errorf("with.dest %q is inside the IN folder", d)
	}
	mediaNotes, listImages := true, true
	if b, ok := with["media_notes"].(bool); ok {
		mediaNotes = b
	}
	if b, ok := with["list_images"].(bool); ok {
		listImages = b
	}
	var conv Type
	if use := strings.TrimSpace(str(with, "convert")); use != "" {
		t, ok := env.Engine.types.get(use)
		if !ok {
			return nil, fmt.Errorf("with.convert %q is no node type of this server", use)
		}
		conv = t
	}
	convWith, _ := with["convert_with"].(map[string]any)
	// The images of one call of the convert node: at most what a plugin
	// takes in one call ([pipelines] max_plugin_files), which refuses more.
	convBatch := int(number(with, "convert_batch", defaultConvertBatch))
	if convBatch < 1 {
		return nil, errors.New("with.convert_batch: 1 or more")
	}
	host := env.Engine.host
	data, err := host.ReadAttachment(env.Project, file)
	if err != nil {
		return nil, err
	}
	text := string(data)

	inFileOf := func(p string) string {
		if base == "" {
			return root + "/" + p
		}
		return root + "/" + base + "/" + p
	}
	notes := map[string]string{}    // a markdown file it links → its note
	attached := map[string]string{} // a file it links → its attachment
	images := map[string]importedImage{}
	skipped := []any{}
	read := []string{file}
	// store keeps a file as it is, as an attachment.
	store := func(img importedImage, b []byte) {
		vp, err := env.StoreAttachment(path.Base(img.src), b)
		if err != nil {
			skipped = append(skipped, map[string]any{"src": img.src, "reason": err.Error()})
			return
		}
		attached[img.src] = vp
		if info, ok := attach.AllowedExt[strings.ToLower(path.Ext(vp))]; ok && info.IsImage {
			images[vp] = img
		}
	}
	var toConvert []importedImage
	for _, l := range pkgimport.Links(text, src) {
		if _, ok := attached[l.Target]; ok {
			continue
		}
		if _, ok := notes[l.Target]; ok {
			continue
		}
		if !l.Image && strings.EqualFold(path.Ext(l.Target), ".md") {
			notes[l.Target] = dest + "/" + l.Target
			continue
		}
		in := inFileOf(l.Target)
		b, err := host.ReadAttachment(env.Project, in)
		if err != nil {
			skipped = append(skipped, map[string]any{"src": l.Target, "reason": err.Error()})
			continue
		}
		read = append(read, in)
		sum := sha256.Sum256(b)
		img := importedImage{src: l.Target, inFile: in, sha: hex.EncodeToString(sum[:])}
		if c, _, err := image.DecodeConfig(bytes.NewReader(b)); err == nil {
			img.width, img.height = c.Width, c.Height
		}
		if conv != nil && convertible[strings.ToLower(path.Ext(l.Target))] {
			attached[l.Target] = "" // taken: the link may come again
			toConvert = append(toConvert, img)
			continue
		}
		store(img, b)
	}
	converted, convertErrors := 0, []any{}
	for start := 0; start < len(toConvert); start += convBatch {
		batch := toConvert[start:min(start+convBatch, len(toConvert))]
		done, err := env.convertImages(ctx, conv, convWith, batch)
		for i, img := range batch {
			if err == nil && done[i].path != "" {
				if done[i].width > 0 {
					img.width, img.height = done[i].width, done[i].height
				}
				attached[img.src] = done[i].path
				images[done[i].path] = img
				converted++
				continue
			}
			// A batch that failed whole (the plugin down, a tool error)
			// gives no item: its images are stored as they are.
			var why string
			if err != nil {
				why = err.Error()
			} else {
				why = done[i].why
			}
			if why != "" {
				convertErrors = append(convertErrors, map[string]any{"src": img.src, "reason": why})
			}
			delete(attached, img.src)
			b, rerr := host.ReadAttachment(env.Project, img.inFile)
			if rerr != nil {
				skipped = append(skipped, map[string]any{"src": img.src, "reason": rerr.Error()})
				continue
			}
			store(img, b)
		}
	}
	text, unresolved := pkgimport.RewriteMarkdown(text, src, notes, attached)
	if unresolved == nil {
		unresolved = []string{}
	}

	// The note keeps the document's frontmatter, with where it comes from:
	// in_file is its path in the IN folder, as text, since a file of the IN
	// folder is no note a wikilink could reach.
	notePath := dest + "/" + src
	fm := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw([]byte(text)))
	set := []frontmatter.Field{
		{Key: "pipeline", Value: "[[" + strings.TrimSuffix(env.Run.Ref, ".md") + "]]"},
		{Key: "pipeline_run", Value: env.Run.ID},
		{Key: "in_file", Value: file},
	}
	if _, has := fm["tags"]; !has {
		set = append(set, frontmatter.Field{Key: "tags", Value: []string{env.Project}})
	}
	content, err := frontmatter.SetKeys([]byte(text), set, nil)
	if err != nil {
		return nil, fmt.Errorf("the frontmatter of %s: %w", file, err)
	}
	res, err := host.WriteNote(env.Project, notePath, content, WriteUpsert, Actor(env.Run.Ref))
	if err != nil {
		return nil, err
	}

	out := map[string]any{"path": res.Path, "created": res.Created, "attachments": len(attached),
		"image_count": 0, "removed": []string{}, "unresolved": unresolved, "skipped": skipped}
	if conv != nil {
		out["converted"], out["convert_errors"] = converted, convertErrors
	}
	if listImages {
		out["images"] = []any{}
	}
	if mediaNotes {
		made, removed, err := env.syncMediaNotes(notePath, string(content), images)
		if err != nil {
			return nil, err
		}
		out["image_count"], out["removed"] = len(made), removed
		if listImages {
			out["images"] = made
		}
	}

	// The files it read are done, with the run; the document's status is
	// the run's, as for any file a run takes.
	if env.Engine.in != nil && len(read) > 1 {
		if err := env.Engine.in.SetStatus(env.Project, read[1:], infolder.StatusDone, env.Run.ID); err != nil {
			env.Log.Warn("in.import: marking the files it read", "err", err)
		}
	}
	return out, nil
}

// convertible are the images in.import hands its convert node; an svg, a
// document or another file is stored as it is.
var convertible = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".tif": true, ".tiff": true}

// defaultConvertBatch is the images of one call of the convert node when
// its convert_batch says nothing.
const defaultConvertBatch = 25

// convertedImage is an image the convert node gave back: the attachment it
// is stored as, and its size; or why it gave none (kept as it is, or an
// error), path empty.
type convertedImage struct {
	path          string
	width, height int
	why           string
}

// convertImages hands a batch of images of the IN folder to the convert
// node, as its files setting (by reference, for a plugin that takes them
// so), with its other settings from with. The node answers with results, in
// the order of its files, each naming the file it gave back (output), as
// images.convert does (IMP-184); the files it gave back are attachments
// already. An image it kept or failed on has no path.
func (env *Env) convertImages(ctx context.Context, t Type, with map[string]any, batch []importedImage) ([]convertedImage, error) {
	args := make(map[string]any, len(with)+1)
	for k, v := range with {
		args[k] = v
	}
	files := make([]any, len(batch))
	for i, img := range batch {
		files[i] = img.inFile
	}
	args["files"] = files
	out, err := t.Run(ctx, env, args)
	if err != nil {
		return nil, fmt.Errorf("converting: %w", err)
	}
	m, _ := out.(map[string]any)
	results, _ := m["results"].([]any)
	if len(results) != len(batch) {
		return nil, fmt.Errorf("converting: %d results for %d images", len(results), len(batch))
	}
	byName := map[string]string{}
	stored, _ := m["files"].([]any)
	for _, f := range stored {
		fm, _ := f.(map[string]any)
		name, _ := fm["name"].(string)
		p, _ := fm["path"].(string)
		byName[name] = p
	}
	done := make([]convertedImage, len(batch))
	for i, r := range results {
		rm, _ := r.(map[string]any)
		if name, _ := rm["output"].(string); name != "" && byName[name] != "" {
			w, _ := rm["width"].(float64)
			h, _ := rm["height"].(float64)
			done[i] = convertedImage{path: byName[name], width: int(w), height: int(h)}
			continue
		}
		if e, _ := rm["error"].(string); e != "" {
			done[i].why = e
		} else if name, _ := rm["output"].(string); name != "" {
			done[i].why = "the converted file was not stored: " + name
		}
	}
	return done, nil
}

// describedKeys are what llm.describe_media writes in an image note: an
// image that changed loses them. Its title is its label: until it is
// described an image note has none, and goes by its file's name, so that the
// thousands of image notes of a document do not outrank the document in a
// search with its title.
var describedKeys = []string{"title", "label", "model", "prompt_version", "described", "describe_error"}

// syncMediaNotes writes the image notes of the article from its content:
// one per image it stored, in the order the images first appear, named
// after the image's file. A note whose image did not change keeps its
// description; the image notes of the article that no image needs any more
// go (into the trash).
func (env *Env) syncMediaNotes(article, content string, images map[string]importedImage) ([]any, []string, error) {
	host := env.Engine.host
	lines := strings.Split(parser.BodyAfterFrontmatter([]byte(content)), "\n")
	type entry struct {
		ref   imageRef
		att   string
		count int
	}
	var order []*entry
	byAtt := map[string]*entry{}
	for _, r := range scanImages(lines) {
		att := strings.TrimPrefix(r.URL, attach.FileURL(""))
		if _, ok := images[att]; !ok {
			continue
		}
		if e := byAtt[att]; e != nil {
			e.count++
			continue
		}
		e := &entry{ref: r, att: att, count: 1}
		byAtt[att] = e
		order = append(order, e)
	}

	folder := mediaFolder(article)
	there := map[string]bool{}    // the notes in the folder
	mine := map[string]NoteData{} // those that are image notes of the article
	lister, _ := host.(Folders)
	if lister != nil {
		paths, err := lister.NotesIn(env.Project, folder)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range paths {
			there[p] = true
			if n, err := host.ReadNote(env.Project, p); err == nil && isMediaOf(n.Frontmatter, article) {
				mine[p] = n
			}
		}
	}

	actor := Actor(env.Run.Ref)
	used := map[string]bool{}
	made := []any{}
	for i, e := range order {
		img := images[e.att]
		stem := strings.TrimSuffix(path.Base(img.src), path.Ext(img.src))
		p := folder + "/" + stem + ".md"
		for n := 2; used[p] || there[p] && mine[p].Path == ""; n++ {
			p = fmt.Sprintf("%s/%s-%d.md", folder, stem, n)
		}
		used[p] = true
		set := []frontmatter.Field{
			{Key: "type", Value: "image"},
			{Key: "media", Value: e.att},
			{Key: "article", Value: "[[" + strings.TrimSuffix(article, ".md") + "]]"},
			{Key: "position", Value: i + 1},
			{Key: "occurrences", Value: e.count},
			{Key: "inline", Value: e.ref.Inline},
			{Key: "section", Value: sectionAt(lines, e.ref.Line)},
			{Key: "in_sha256", Value: img.sha},
			{Key: "in_file", Value: img.inFile},
			{Key: "pipeline", Value: "[[" + strings.TrimSuffix(env.Run.Ref, ".md") + "]]"},
			{Key: "pipeline_run", Value: env.Run.ID},
			{Key: "tags", Value: []string{env.Project, "type:image"}},
		}
		unset := []string{"sha256"} // before in_sha256, the same value
		if alt := strings.TrimSpace(e.ref.Alt); alt != "" {
			set = append(set, frontmatter.Field{Key: "alt_original", Value: alt})
		} else {
			unset = append(unset, "alt_original")
		}
		if img.width > 0 {
			set = append(set, frontmatter.Field{Key: "width", Value: img.width}, frontmatter.Field{Key: "height", Value: img.height})
		}
		var raw []byte
		body := ""
		if n, ok := mine[p]; ok {
			raw, body = n.Raw, n.Body
			// The description is of the original: it goes when the original
			// changed, not when the attachment did (a conversion, IMP-184).
			was, _ := n.Frontmatter["in_sha256"].(string)
			if was == "" {
				was, _ = n.Frontmatter["sha256"].(string)
			}
			if was != img.sha {
				body = ""
				unset = append(unset, describedKeys...)
			} else if _, described := n.Frontmatter["label"]; !described {
				unset = append(unset, "title")
			}
		}
		c, err := frontmatter.SetKeys(raw, set, unset)
		if err != nil {
			return nil, nil, fmt.Errorf("the image note %s: %w", p, err)
		}
		if _, err := host.WriteNote(env.Project, p, withBody(c, body), WriteUpsert, actor); err != nil {
			return nil, nil, err
		}
		made = append(made, map[string]any{"note": p, "attachment": e.att, "src": img.src, "position": i + 1, "inline": e.ref.Inline})
	}

	removed := []string{}
	var gone []string
	for p := range mine {
		if !used[p] {
			gone = append(gone, p)
		}
	}
	sort.Strings(gone)
	for _, p := range gone {
		if err := lister.DeleteNote(env.Project, p, actor); err != nil {
			return nil, nil, err
		}
		removed = append(removed, p)
	}
	return made, removed, nil
}
