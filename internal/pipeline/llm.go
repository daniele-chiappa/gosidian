package pipeline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
)

// The LLM nodes speak the OpenAI chat completions API, which Ollama, vLLM,
// llama.cpp and OpenRouter all serve: one client for every provider.

const defaultDescribePrompt = "Describe this image for someone who cannot see it: what it shows, any text in it, and what it means in the document it comes from. Answer in the language of the document."

func (e *Engine) provider(name string) (Provider, error) {
	if len(e.cfg.Providers) == 0 {
		return Provider{}, errors.New("no LLM provider is configured on this server ([[pipelines.llm]])")
	}
	if name == "" {
		return e.cfg.Providers[0], nil
	}
	for _, p := range e.cfg.Providers {
		if p.Name == name {
			return p, nil
		}
	}
	return Provider{}, fmt.Errorf("there is no LLM provider %q on this server", name)
}

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type chatResponse struct {
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatChoice struct {
	Message      chatAnswer `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

// chatAnswer is a model's answer, or a piece of it (a stream's delta).
type chatAnswer struct {
	Content string `json:"content"`
	// The reasoning of a model that thinks first, as the servers name it
	// (Ollama, vLLM, others).
	Reasoning        string `json:"reasoning"`
	ReasoningContent string `json:"reasoning_content"`
}

// chatChunk is a piece of a provider's answer as a stream.
type chatChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Index        int        `json:"index"`
		Delta        chatAnswer `json:"delta"`
		FinishReason *string    `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// readStream reads a provider's answer sent as server-sent events, each
// piece of its text to w as it comes: the answer it makes is the one the
// provider gives whole. estimated says that the provider gave no usage:
// the completion counts a token a piece.
func readStream(body io.Reader, w *streamWriter) (cr chatResponse, estimated bool, err error) {
	var text, reasoning strings.Builder
	finish, pieces, usage, done := "", 0, false, false
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64<<10), maxResponseBytes)
	for !done && sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			done = true
			break
		}
		var ch chatChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return cr, false, fmt.Errorf("a piece of the stream is not JSON: %w", err)
		}
		if ch.Error != nil {
			return cr, false, errors.New(ch.Error.Message)
		}
		if ch.Model != "" {
			cr.Model = ch.Model
		}
		if ch.Usage != nil {
			cr.Usage = *ch.Usage
			usage = true
		}
		for _, c := range ch.Choices {
			if c.Index != 0 {
				continue
			}
			if c.Delta.Content != "" {
				if text.Len()+len(c.Delta.Content) > maxResponseBytes {
					return cr, false, fmt.Errorf("the answer is longer than %d bytes", maxResponseBytes)
				}
				text.WriteString(c.Delta.Content)
				w.write(c.Delta.Content)
				pieces++
			}
			reasoning.WriteString(c.Delta.Reasoning + c.Delta.ReasoningContent)
			if c.FinishReason != nil && *c.FinishReason != "" {
				finish = *c.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return cr, false, fmt.Errorf("reading the stream: %w", err)
	}
	if !done && finish == "" {
		return cr, false, errors.New("the stream ended before the answer did")
	}
	if !usage {
		cr.Usage.CompletionTokens, cr.Usage.TotalTokens = pieces, pieces
	}
	cr.Choices = []chatChoice{{Message: chatAnswer{Content: text.String(), Reasoning: reasoning.String()}, FinishReason: finish}}
	return cr, !usage, nil
}

// chat calls a provider and returns the node output: text, model, usage.
// With w, the provider sends its answer as a stream, each piece to w; one
// that answers whole is read as such.
func (e *Engine) chat(ctx context.Context, env *Env, with map[string]any, msgs []chatMessage, w *streamWriter) (map[string]any, error) {
	p, err := e.provider(str(with, "provider"))
	if err != nil {
		return nil, err
	}
	model := str(with, "model")
	if model == "" {
		model = p.Model
	}
	if model == "" {
		return nil, fmt.Errorf("with.model is required: provider %q has no default model", p.Name)
	}
	maxTokens := int(number(with, "max_tokens", 1024))
	if cap := e.cfg.MaxLLMTokens; cap > 0 && env.tokens+maxTokens > cap {
		return nil, fmt.Errorf("the run used %d LLM tokens, and this call could pass the cap of %d ([pipelines] max_llm_tokens)", env.tokens, cap)
	}
	body := map[string]any{"model": model, "messages": msgs, "max_tokens": maxTokens, "stream": w != nil}
	if w != nil {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if t, ok := with["temperature"].(float64); ok {
		body["temperature"] = t
	}
	if truthy(with["json"]) {
		body["response_format"] = map[string]any{"type": "json_object"}
	}
	// How much a model that reasons first may think: none (Ollama turns it
	// off), low, medium, high. A thinking model can spend all max_tokens
	// before it answers.
	if r := str(with, "reasoning_effort"); r != "" {
		body["reasoning_effort"] = r
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BaseURL, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKeyEnv != "" {
		// An empty variable is a provider without a key (Ollama): a
		// compose file names the variable whether the install sets it.
		if key := strings.TrimSpace(os.Getenv(p.APIKeyEnv)); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	} else if p.APIKeySecret != "" {
		if e.secrets == nil {
			return nil, errors.New("secrets are not configured on this server (GOSIDIAN_SECRETS_KEY)")
		}
		key, err := e.secrets.Get(p.APIKeySecret)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
	}
	// The providers are configured by the owner: no allowlist here.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var cr chatResponse
	streamed, estimated := false, false
	if w != nil && resp.StatusCode < 400 && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		if cr, estimated, err = readStream(resp.Body, w); err != nil {
			return nil, fmt.Errorf("provider %q: %w", p.Name, err)
		}
		streamed = true
	} else {
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &cr); err != nil {
			return nil, retryable(resp, fmt.Errorf("provider %q answered %d, not JSON", p.Name, resp.StatusCode))
		}
	}
	if resp.StatusCode >= 400 || cr.Error != nil {
		msg := fmt.Sprintf("%d", resp.StatusCode)
		if cr.Error != nil {
			msg += ": " + cr.Error.Message
		}
		return nil, retryable(resp, fmt.Errorf("provider %q answered %s", p.Name, msg))
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("provider %q answered no choice", p.Name)
	}
	choice := cr.Choices[0]
	text := strings.TrimSpace(choice.Message.Content)
	if !streamed {
		// A provider that answers whole: the stream has it at once.
		w.write(text)
	}
	out := map[string]any{
		"text": text, "model": cr.Model, "provider": p.Name, "finish_reason": choice.FinishReason,
		"usage": map[string]any{"prompt_tokens": float64(cr.Usage.PromptTokens), "completion_tokens": float64(cr.Usage.CompletionTokens), "total_tokens": float64(cr.Usage.TotalTokens)},
	}
	if estimated {
		out["usage"].(map[string]any)["estimated"] = true
	}
	if r := strings.TrimSpace(choice.Message.Reasoning + choice.Message.ReasoningContent); r != "" {
		out["reasoning"] = r
	}
	if truthy(with["json"]) {
		var j any
		if err := json.Unmarshal([]byte(text), &j); err == nil {
			out["json"] = j
		}
	}
	return out, nil
}

func llmChat(ctx context.Context, env *Env, with map[string]any) (any, error) {
	prompt, err := required(with, "prompt")
	if err != nil {
		return nil, err
	}
	var msgs []chatMessage
	if s := str(with, "system"); s != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: s})
	}
	msgs = append(msgs, chatMessage{Role: "user", Content: prompt})
	var w *streamWriter
	if truthy(with["stream"]) {
		w = env.streamWriter()
	}
	return env.Engine.chat(ctx, env, with, msgs, w)
}

var imageMIME = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}

// maxImageBytes caps an image sent to a model.
const maxImageBytes = 10 << 20

// imageMessage is a user message with a prompt and an image.
func imageMessage(prompt, mime string, data []byte) chatMessage {
	return chatMessage{Role: "user", Content: []map[string]any{
		{"type": "text", "text": prompt},
		{"type": "image_url", "image_url": map[string]any{"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)}},
	}}
}

func llmDescribe(ctx context.Context, env *Env, with map[string]any) (any, error) {
	img, err := required(with, "image")
	if err != nil {
		return nil, err
	}
	rel, err := env.projectPath(img)
	if err != nil {
		return nil, err
	}
	mime, ok := imageMIME[strings.ToLower(path.Ext(rel))]
	if !ok {
		return nil, fmt.Errorf("%s is not an image the models read (png, jpg, gif, webp)", rel)
	}
	data, err := env.Engine.host.ReadAttachment(env.Project, rel)
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("%s is %d bytes, at most %d", rel, len(data), maxImageBytes)
	}
	prompt := str(with, "prompt")
	if prompt == "" {
		prompt = defaultDescribePrompt
	}
	var msgs []chatMessage
	if s := str(with, "system"); s != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: s})
	}
	msgs = append(msgs, imageMessage(prompt, mime, data))
	out, err := env.Engine.chat(ctx, env, with, msgs, nil)
	if err != nil {
		return nil, err
	}
	out["image"] = rel
	return out, nil
}
