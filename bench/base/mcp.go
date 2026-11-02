package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// mcpClient speaks the Streamable HTTP transport of MCP the way an agent's
// client does: one session per client, JSON-RPC over POST /mcp. It knows no
// gosidian internals, so the same client measures any version that serves
// /mcp (every image since before the first benchmark of 2026-09-25).
type mcpClient struct {
	url   string
	token string
	sid   string
	next  atomic.Int64
	http  *http.Client
}

func newMCPClient(baseURL, token string) (*mcpClient, error) {
	c := &mcpClient{url: strings.TrimRight(baseURL, "/") + "/mcp", token: token, http: &http.Client{Timeout: 2 * time.Minute}}
	if _, _, err := c.rpc("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "gosidian-bench-base", "version": "1"},
	}); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if _, _, err := c.send("notifications/initialized", map[string]any{}, false); err != nil {
		return nil, fmt.Errorf("initialized: %w", err)
	}
	return c, nil
}

// rpcError is a JSON-RPC error, or a tool result flagged isError.
type rpcError struct{ msg string }

func (e *rpcError) Error() string { return e.msg }

// rpc sends a request and returns its result with the size of the whole
// response body: the bytes an agent's client receives.
func (c *mcpClient) rpc(method string, params any) (json.RawMessage, int, error) {
	return c.send(method, params, true)
}

func (c *mcpClient) send(method string, params any, wantReply bool) (json.RawMessage, int, error) {
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	var id int64
	if wantReply {
		id = c.next.Add(1)
		msg["id"] = id
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.sid != "" {
		req.Header.Set("Mcp-Session-Id", c.sid)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		c.sid = s
	}
	if !wantReply {
		return nil, len(raw), nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, len(raw), fmt.Errorf("%s: HTTP %d: %.200s", method, resp.StatusCode, raw)
	}
	reply, err := findReply(raw, resp.Header.Get("Content-Type"), id)
	if err != nil {
		return nil, len(raw), fmt.Errorf("%s: %w", method, err)
	}
	if reply.Error != nil {
		return nil, len(raw), &rpcError{fmt.Sprintf("%s: %s", method, reply.Error.Message)}
	}
	return reply.Result, len(raw), nil
}

type rpcReply struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// findReply picks the reply to id out of a plain JSON body or, when the
// server answers as an event stream, out of its data lines.
func findReply(raw []byte, contentType string, id int64) (rpcReply, error) {
	if !strings.HasPrefix(contentType, "text/event-stream") {
		var r rpcReply
		if err := json.Unmarshal(raw, &r); err != nil {
			return r, fmt.Errorf("decode reply: %w", err)
		}
		return r, nil
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		var r rpcReply
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &r) == nil && r.ID == id {
			return r, nil
		}
	}
	return rpcReply{}, errors.New("no reply in the event stream")
}

// toolResult is what tools/call returns. An agent like Claude Code reads
// structuredContent when it is there, the text otherwise.
type toolResult struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Structured json.RawMessage `json:"structuredContent"`
	IsError    bool            `json:"isError"`
}

// agentBytes is the size of what the agent reads of a result.
func (r toolResult) agentBytes() int {
	if len(r.Structured) > 0 && string(r.Structured) != "null" {
		return len(r.Structured)
	}
	n := 0
	for _, c := range r.Content {
		n += len(c.Text)
	}
	return n
}

// payload is the result's JSON object: structuredContent, or the text of
// the first content block.
func (r toolResult) payload() json.RawMessage {
	if len(r.Structured) > 0 && string(r.Structured) != "null" {
		return r.Structured
	}
	if len(r.Content) > 0 {
		return json.RawMessage(r.Content[0].Text)
	}
	return nil
}

// call runs a tool and times it from the request to the decoded reply.
func (c *mcpClient) call(name string, args map[string]any) (toolResult, time.Duration, error) {
	start := time.Now()
	raw, _, err := c.rpc("tools/call", map[string]any{"name": name, "arguments": args})
	took := time.Since(start)
	var r toolResult
	if err != nil {
		return r, took, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, took, fmt.Errorf("%s: decode result: %w", name, err)
	}
	if r.IsError {
		text := ""
		if len(r.Content) > 0 {
			text = r.Content[0].Text
		}
		return r, took, &rpcError{fmt.Sprintf("%s: %.200s", name, text)}
	}
	return r, took, nil
}

// toolInfo is one entry of tools/list.
type toolInfo struct {
	Name        string `json:"name"`
	InputSchema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	} `json:"inputSchema"`
}

// listTools returns the tools and the size of the tools/list reply: the
// schemas an agent loads at the start of every session.
func (c *mcpClient) listTools() (map[string]toolInfo, int, error) {
	raw, size, err := c.rpc("tools/list", map[string]any{})
	if err != nil {
		return nil, 0, err
	}
	var res struct {
		Tools []toolInfo `json:"tools"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, 0, err
	}
	out := make(map[string]toolInfo, len(res.Tools))
	for _, t := range res.Tools {
		out[t.Name] = t
	}
	return out, size, nil
}
