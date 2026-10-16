package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// maxResponseBytes caps what http.request and the LLM calls read.
const maxResponseBytes = 4 << 20

// allowed says whether the server may call u: http or https, and under
// one of the prefixes of [pipelines] http_allowlist.
func (e *Engine) allowed(u string) bool {
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" || pu.User != nil {
		return false
	}
	for _, p := range e.cfg.HTTPAllowlist {
		if p != "" && strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

// guardedClient follows a redirect only to an allowed address.
func guardedClient(e *Engine) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if !e.allowed(req.URL.String()) {
				return fmt.Errorf("a redirect to %s, outside the allowlist", req.URL.Redacted())
			}
			return nil
		},
	}
}

// retryable turns an answer that says "later" into a RetryAfter.
func retryable(resp *http.Response, err error) error {
	if resp == nil || (resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable) {
		return err
	}
	after := 30 * time.Second
	if s, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && s > 0 {
		after = time.Duration(s) * time.Second
	}
	return RetryAfter{After: after, Err: err}
}

func httpRequest(ctx context.Context, env *Env, with map[string]any) (any, error) {
	u, err := required(with, "url")
	if err != nil {
		return nil, err
	}
	if !env.Engine.allowed(u) {
		return nil, fmt.Errorf("url %q is not among the addresses the server may call ([pipelines] http_allowlist)", u)
	}
	method := strings.ToUpper(str(with, "method"))
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	ctype := ""
	if j, ok := with["json"]; ok {
		b, err := json.Marshal(j)
		if err != nil {
			return nil, fmt.Errorf("with.json: %w", err)
		}
		body, ctype = bytes.NewReader(b), "application/json"
	} else if s := str(with, "body"); s != "" {
		body, ctype = strings.NewReader(s), "text/plain; charset=utf-8"
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if h, ok := with["headers"].(map[string]any); ok {
		for k, v := range h {
			req.Header.Set(k, render(v))
		}
	}
	if s := str(with, "secret_header"); s != "" {
		// A header whose value is a secret: "Authorization=api_key" sends
		// the secret api_key, never written in the note.
		name, secret, ok := strings.Cut(s, "=")
		if !ok {
			return nil, errors.New("with.secret_header is Header=secret_name")
		}
		if env.Engine.secrets == nil {
			return nil, errors.New("secrets are not configured on this server (GOSIDIAN_SECRETS_KEY)")
		}
		v, err := env.Engine.secrets.Get(strings.TrimSpace(secret))
		if err != nil {
			return nil, err
		}
		req.Header.Set(strings.TrimSpace(name), v)
	}
	resp, err := env.Engine.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("the answer is larger than %d bytes", maxResponseBytes)
	}
	out := map[string]any{"status": float64(resp.StatusCode), "headers": flatHeaders(resp.Header)}
	var j any
	if strings.Contains(resp.Header.Get("Content-Type"), "json") && json.Unmarshal(data, &j) == nil {
		out["json"] = j
	} else {
		out["body"] = string(data)
	}
	if resp.StatusCode >= 400 && !truthy(with["accept_errors"]) {
		return nil, retryable(resp, fmt.Errorf("%s %s answered %d", method, req.URL.Redacted(), resp.StatusCode))
	}
	return out, nil
}

func flatHeaders(h http.Header) map[string]any {
	out := map[string]any{}
	for k, v := range h {
		out[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	return out
}

// RunSummary is what a callback receives when a run ends.
type RunSummary struct {
	Run      string            `json:"run"`
	Pipeline string            `json:"pipeline"`
	Project  string            `json:"project"`
	Status   jobs.Status       `json:"status"`
	Error    string            `json:"error,omitempty"`
	Started  time.Time         `json:"started,omitzero"`
	Finished time.Time         `json:"finished,omitzero"`
	Steps    map[string]string `json:"steps"`
	// Answer is the output of the node the pipeline names as its answer,
	// when it ended done (IMP-191).
	Answer any `json:"answer,omitempty"`
}

// Summary is a run's summary.
func Summary(run jobs.Run, steps []jobs.Step) RunSummary {
	s := RunSummary{Run: run.ID, Pipeline: run.Ref, Project: run.Project, Status: run.Status, Error: run.Error,
		Started: run.Started, Finished: run.Finished, Steps: map[string]string{}}
	for _, st := range steps {
		s.Steps[st.Name] = string(st.Status)
	}
	if a, ok := AnswerOf(run, steps); ok {
		s.Answer = a
	}
	return s
}

func (e *Engine) callback(run jobs.Run, steps []jobs.Step) error {
	if !e.allowed(run.CallbackURL) {
		return errors.New("the address is no longer in the allowlist")
	}
	b, _ := json.Marshal(Summary(run, steps))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, run.CallbackURL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("it answered %d", resp.StatusCode)
	}
	return nil
}
