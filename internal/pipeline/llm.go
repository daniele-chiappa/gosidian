package pipeline

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// chat calls a provider and returns the node output: text, model, usage.
func (e *Engine) chat(ctx context.Context, env *Env, with map[string]any, msgs []chatMessage) (map[string]any, error) {
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
	body := map[string]any{"model": model, "messages": msgs, "max_tokens": maxTokens, "stream": false}
	if t, ok := with["temperature"].(float64); ok {
		body["temperature"] = t
	}
	if truthy(with["json"]) {
		body["response_format"] = map[string]any{"type": "json_object"}
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BaseURL, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKeySecret != "" {
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return nil, retryable(resp, fmt.Errorf("provider %q answered %d, not JSON", p.Name, resp.StatusCode))
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
	text := strings.TrimSpace(cr.Choices[0].Message.Content)
	out := map[string]any{
		"text": text, "model": cr.Model, "provider": p.Name,
		"usage": map[string]any{"prompt_tokens": float64(cr.Usage.PromptTokens), "completion_tokens": float64(cr.Usage.CompletionTokens), "total_tokens": float64(cr.Usage.TotalTokens)},
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
	return env.Engine.chat(ctx, env, with, msgs)
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
	out, err := env.Engine.chat(ctx, env, with, msgs)
	if err != nil {
		return nil, err
	}
	out["image"] = rel
	return out, nil
}
