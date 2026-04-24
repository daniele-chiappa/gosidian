// Package audit provides an append-only JSONL log for mutating operations
// on the vault. Each mutating handler in HTTP and MCP writes one record.
//
// Format: one JSON object per line, fields:
//   - ts:        RFC3339 timestamp
//   - source:    "http" or "mcp"
//   - token:     token id (8 hex), empty for HTTP requests without auth
//   - actor:     e.g. http remote IP or token name (best-effort)
//   - action:    create / update / append / delete / rename / create_project /
//                delete_project / rename_project
//   - path:      vault-relative path (or project name for project ops)
//   - to:        target path/name for rename (omitted otherwise)
//   - size:      content size in bytes after the operation (omitted for delete)
//
// File: <vault>/.gosidian/audit.jsonl. Always opened with O_APPEND so multiple
// writers (HTTP + MCP) interleave safely; an additional Mutex serializes our
// own writes for atomicity at the line level.
package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Source string

const (
	SourceHTTP Source = "http"
	SourceMCP  Source = "mcp"
)

type Action string

const (
	ActionCreate        Action = "create"
	ActionUpdate        Action = "update"
	ActionAppend        Action = "append"
	ActionDelete        Action = "delete"
	ActionRename        Action = "rename"
	ActionCreateProject Action = "create_project"
	ActionDeleteProject    Action = "delete_project"
	ActionRenameProject    Action = "rename_project"
	ActionUploadAttachment Action = "upload_attachment"
	ActionDeleteAttachment Action = "delete_attachment"
	ActionTokenCreate      Action = "token_create"
	ActionTokenRevoke      Action = "token_revoke"
)

// Entry is the on-disk shape. Keep field names short; this file may grow.
type Entry struct {
	TS     time.Time `json:"ts"`
	Source Source    `json:"source"`
	Token  string    `json:"token,omitempty"`
	Actor  string    `json:"actor,omitempty"`
	UserID string    `json:"user_id,omitempty"` // webauth user id (v1.4+); empty for MCP
	Action Action    `json:"action"`
	Path   string    `json:"path"`
	To     string    `json:"to,omitempty"`
	Size   int64     `json:"size,omitempty"`
}

// Log is a thread-safe append-only writer.
type Log struct {
	path string
	mu   sync.Mutex
}

// Open prepares the audit log file. The directory is created if missing. A
// nil *Log is allowed and behaves as a no-op (used when audit is disabled).
func Open(path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &Log{path: path}, nil
}

// Write appends an entry. Best-effort: errors are returned but callers are
// free to ignore them — failing to audit must never block the user request.
func (l *Log) Write(e Entry) error {
	if l == nil {
		return nil
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	data, err := json.Marshal(&e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// TailOpts defines the filters for TailFiltered. All fields are optional. An
// empty TailOpts equals "give me the newest Limit entries".
type TailOpts struct {
	Since      time.Time // lower bound on entry.TS (exclusive). Zero = no lower bound.
	Until      time.Time // upper bound on entry.TS (exclusive). Zero = no upper bound.
	Actor      string    // exact match on entry.Actor (token name, possibly suffixed with @<cid>).
	UserID     string    // exact match on entry.UserID (webauth user id).
	Action     Action    // exact match on entry.Action.
	PathPrefix string    // entry.Path must start with this string (vault-relative).
	Source     Source    // exact match on entry.Source ("http" or "mcp").
	Limit      int       // max entries to return. <=0 or >500 is clamped to 50.
}

// TailFiltered reads the log and returns entries matching the filters. The
// scan walks forward but only keeps the tail (most recent Limit matches), so
// result order is oldest→newest (same as Tail). Designed to be called from
// the memory_audit_tail MCP tool so agents can introspect their own activity.
func (l *Log) TailFiltered(opts TailOpts) ([]Entry, error) {
	if l == nil {
		return nil, nil
	}
	limit := opts.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := os.ReadFile(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	// Linear scan forward, decode each JSON line, apply filters, keep the
	// newest-limit in a ring-style slice. Audit volume is modest for a single
	// user (seconds of scan even at 100k entries) and this keeps the code
	// straightforward; reverse-scan optimizations are unnecessary at this scale.
	var matches []Entry
	start := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			if i > start {
				line := data[start:i]
				var e Entry
				if err := json.Unmarshal(line, &e); err == nil && matchesOpts(e, opts) {
					matches = append(matches, e)
					if len(matches) > limit {
						matches = matches[1:]
					}
				}
			}
			start = i + 1
		}
	}
	return matches, nil
}

// matchesOpts returns true if the entry passes all non-zero filters.
func matchesOpts(e Entry, opts TailOpts) bool {
	if !opts.Since.IsZero() && !e.TS.After(opts.Since) {
		return false
	}
	if !opts.Until.IsZero() && !e.TS.Before(opts.Until) {
		return false
	}
	if opts.Actor != "" && e.Actor != opts.Actor {
		return false
	}
	if opts.UserID != "" && e.UserID != opts.UserID {
		return false
	}
	if opts.Action != "" && e.Action != opts.Action {
		return false
	}
	if opts.Source != "" && e.Source != opts.Source {
		return false
	}
	if opts.PathPrefix != "" {
		if e.Path == "" || (len(e.Path) < len(opts.PathPrefix) || e.Path[:len(opts.PathPrefix)] != opts.PathPrefix) {
			return false
		}
	}
	return true
}

// Tail reads the last n entries from the log, newest last (i.e. file order).
// Returns fewer than n if the log is shorter. Suitable for the /audit page.
func (l *Log) Tail(n int) ([]Entry, error) {
	if l == nil {
		return nil, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := os.ReadFile(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	// Split lines from the end backwards. Cheap because audit logs are small
	// for personal use; if it ever grows, switch to a reverse line scanner.
	var lines [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			line := data[start:i]
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]Entry, 0, len(lines))
	for _, line := range lines {
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return out, fmt.Errorf("parse: %w", err)
		}
		out = append(out, e)
	}
	return out, nil
}

// Path exposes the file location for templates / health output.
func (l *Log) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}
