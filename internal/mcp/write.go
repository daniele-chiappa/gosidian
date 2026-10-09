package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/mark3labs/mcp-go/mcp"
)

// The one write pipeline of a note (IMP-086). Every tool that writes a note
// goes through writeNote, which runs the whole sequence in one place:
// per-path lock, load, the mode and if_match preconditions, the new body,
// the size and rate limits on that body, write + index, audit, the schema
// check and the note/tree events. Eight bugs of the 2026-09 review came from
// handlers that rebuilt this sequence by hand and skipped a step; the write
// conformance test (write_conformance_test.go) checks every tool against it.
// Authorization (scope and path) stays the caller's job, before writeNote.

// writeMode says what the note must be before the write.
type writeMode int

const (
	// writeUpsert writes the note whether it exists or not.
	writeUpsert writeMode = iota
	// writeCreate refuses a note that exists (writeExists).
	writeCreate
	// writeReplace refuses a note that does not exist (writeMissing).
	writeReplace
)

// writeKind classifies a refused or failed write, so a caller can word the
// refusals its tool documents (an existing note, above all).
type writeKind int

const (
	writeExists writeKind = iota + 1
	writeMissing
	writePrecondition
	writeLimit
	writeContent
	writeFailed
	// writeShrink: the content would empty the note (shrinkGuard).
	writeShrink
)

// writeError is a write refused or failed: the message for the tool, the
// HTTP status the byte endpoints answer and, for a rate refusal, how long
// until a place frees up.
type writeError struct {
	kind   writeKind
	status int
	msg    string
	wait   time.Duration
}

func (e *writeError) Error() string { return e.msg }

// result is the error as a tool result.
func (e *writeError) result() *mcp.CallToolResult { return mcp.NewToolResultError(e.msg) }

// contentError is a refusal of noteWrite.content, with the tool's message.
func contentError(format string, a ...any) *writeError {
	return &writeError{kind: writeContent, status: http.StatusBadRequest, msg: fmt.Sprintf(format, a...)}
}

// errSkipWrite, returned by noteWrite.content, ends the write without
// writing and without an error: a dry run, or nothing to change.
var errSkipWrite = errors.New("nothing to write")

// noteWrite is one write of one note.
type noteWrite struct {
	rel     string
	mode    writeMode
	ifMatch string
	// content builds the new body from the note as it is, nil when it does
	// not exist. A *writeError it returns is passed on as it is;
	// errSkipWrite ends the write without one.
	content func(existing *vault.Note) ([]byte, error)
	// action is the audit action; empty means create or update, after
	// whether the note existed.
	action audit.Action
	// schemaCheck adds the database-schema problems of the written note to
	// the result (noteSchemaProblems).
	schemaCheck bool
	// locked says the caller already holds the per-path lock, because it
	// probed the path before doing other work (storing an attachment).
	locked bool
	// automation is the server writing as itself: no token and no limiter,
	// audited as the automation.
	automation bool
	// shrinkGuard refuses a content that would empty an existing note
	// (shrinkRefusal, IMP-147): an agent's whole-note rewrite that did not
	// say allow_shrink.
	shrinkGuard bool
}

// noteWritten is a write's outcome.
type noteWritten struct {
	Path    string
	ETag    string
	Created bool
	Size    int
	// Skipped: content returned errSkipWrite; ETag is the note's as it is.
	Skipped bool
}

// writeNote runs w through the pipeline. tok is the caller's token, for the
// limiter (nil with w.automation).
func (s *Server) writeNote(ctx context.Context, tok *auth.Token, w noteWrite) (noteWritten, *writeError) {
	if !w.locked {
		unlock := s.vault.LockPath(w.rel)
		defer unlock()
	}
	existing, err := s.vault.Load(w.rel)
	if err != nil {
		existing = nil
		switch {
		case w.mode == writeReplace:
			return noteWritten{}, &writeError{kind: writeMissing, status: http.StatusNotFound, msg: callToolResultText(writeNoteError(w.rel, err))}
		case errors.Is(err, vault.ErrNotNote):
			// Not a note path: write + index refuses it below, with the
			// message that points at the file tools.
		case !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "no such file"):
			return noteWritten{}, &writeError{kind: writeFailed, status: http.StatusInternalServerError, msg: "load failed: " + err.Error()}
		}
	}
	if existing != nil && w.mode == writeCreate {
		return noteWritten{}, &writeError{kind: writeExists, status: http.StatusConflict, msg: fmt.Sprintf("note %q already exists", w.rel)}
	}
	if w.ifMatch != "" {
		if existing == nil {
			// The caller thought the note was there: a mismatch too.
			return noteWritten{}, &writeError{kind: writePrecondition, status: http.StatusPreconditionFailed, msg: fmt.Sprintf("etag mismatch: note %q does not exist", w.rel)}
		}
		if errRes := checkIfMatch(existing, w.ifMatch); errRes != nil {
			return noteWritten{}, &writeError{kind: writePrecondition, status: http.StatusPreconditionFailed, msg: callToolResultText(errRes)}
		}
	}
	content, err := w.content(existing)
	if errors.Is(err, errSkipWrite) {
		out := noteWritten{Path: w.rel, Skipped: true}
		if existing != nil {
			out.ETag = existing.ETag()
		}
		return out, nil
	}
	if err != nil {
		var werr *writeError
		if errors.As(err, &werr) {
			return noteWritten{}, werr
		}
		return noteWritten{}, &writeError{kind: writeContent, status: http.StatusBadRequest, msg: err.Error()}
	}
	// Before the limiter: a refused rewrite does not take a place.
	if w.shrinkGuard && existing != nil {
		if msg := s.shrinkRefusal(w.rel, existing.Size, len(content)); msg != "" {
			return noteWritten{}, &writeError{kind: writeShrink, status: http.StatusConflict, msg: msg}
		}
	}
	if !w.automation {
		if msg, wait := s.writeLimitViolation(ctx, tok, len(content)); msg != "" {
			return noteWritten{}, &writeError{kind: writeLimit, status: http.StatusRequestEntityTooLarge, msg: msg, wait: wait}
		}
	}
	if err := s.writeAndIndex(w.rel, content); err != nil {
		return noteWritten{}, &writeError{kind: writeFailed, status: http.StatusInternalServerError, msg: "write failed: " + err.Error()}
	}
	created := existing == nil
	action := w.action
	if action == "" {
		action = audit.ActionUpdate
		if created {
			action = audit.ActionCreate
		}
	}
	if w.automation {
		if s.audit != nil {
			_ = s.audit.Write(audit.Entry{Source: audit.SourceAutomation, Actor: AutomationAgent, Action: action, Path: w.rel, Size: int64(len(content))})
		}
	} else {
		s.auditWrite(ctx, action, w.rel, "", int64(len(content)))
	}
	if w.schemaCheck {
		s.noteSchemaProblems(ctx, w.rel, content)
	}
	out := noteWritten{Path: w.rel, Created: created, Size: len(content)}
	if fresh, err := s.vault.Load(w.rel); err == nil {
		out.ETag = fresh.ETag()
	}
	// A new note changes the tree too; per-note listeners have no record
	// of it yet.
	if created {
		s.publishNoteChange("create", w.rel, out.ETag, true)
	} else {
		s.publishNoteChange("update", w.rel, out.ETag, false)
	}
	return out, nil
}

// shrinkRefusal is why a rewrite of rel from oldSize to newSize bytes is
// refused, or "" when it is not: with the guard on, a note of at least
// shrinkMinBytes may not drop under shrinkPercent % of its size. A
// placeholder or a stub written over a note by mistake looks like that; a
// note split into others, or reset on purpose, passes allow_shrink.
func (s *Server) shrinkRefusal(rel string, oldSize int64, newSize int) string {
	if s.shrinkPercent <= 0 || oldSize < s.shrinkMinBytes || oldSize <= 0 {
		return ""
	}
	if int64(newSize)*100 >= oldSize*int64(s.shrinkPercent) {
		return ""
	}
	share := fmt.Sprintf("%d%%", int64(newSize)*100/oldSize)
	if int64(newSize)*100 < oldSize {
		share = "less than 1%"
	}
	return fmt.Sprintf("refused: this would shrink %q from %d to %d bytes (%s of it), under the %d%% this server allows for a note of %d bytes or more. "+
		"Nothing was written. A placeholder or a stub written over a note looks like this. "+
		"If the shrink is meant (the note split into others, or reset), call again with allow_shrink: true; to change only a part, use memory_edit.",
		rel, oldSize, newSize, share, s.shrinkPercent, s.shrinkMinBytes)
}

// fixedContent is a noteWrite.content that writes body whatever the note is.
func fixedContent(body []byte) func(*vault.Note) ([]byte, error) {
	return func(*vault.Note) ([]byte, error) { return body, nil }
}
