package main

import (
	"log"
	"strings"
	"sync"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/webauth"
)

// wireAuthors keeps the authors of the notes in the index (IMP-127
// iteration 3): the audit log replayed at start, then each write as the log
// takes it. Errors are logged, never fatal: the authors are a convenience
// of queries, not a guarantee.
func wireAuthors(idx *index.Index, al *audit.Log, tokens *auth.Store, accounts *webauth.Store) {
	names := &authorNames{tokens: tokens, accounts: accounts}
	var events []index.AuthorEvent
	if err := al.Each(func(e audit.Entry) {
		if ev, ok := authorEvent(e, names); ok {
			events = append(events, ev)
		}
	}); err != nil {
		log.Printf("authors: read the audit log: %v", err)
	}
	if err := idx.RebuildAuthors(events); err != nil {
		log.Printf("authors: rebuild: %v", err)
	} else {
		log.Printf("authors: %d write(s) replayed from the audit log", len(events))
	}
	al.OnWrite(func(e audit.Entry) {
		if ev, ok := authorEvent(e, names); ok {
			if err := idx.RecordAuthor(ev); err != nil {
				log.Printf("authors: record %s %s: %v", e.Action, e.Path, err)
			}
		}
	})
}

// authorEvent reads an audit entry as a write of a note, or of a project's
// notes; other entries (attachments, tokens, settings…) are not.
func authorEvent(e audit.Entry, names *authorNames) (index.AuthorEvent, bool) {
	switch e.Action {
	case audit.ActionCreate, audit.ActionUpdate, audit.ActionAppend, audit.ActionDelete, audit.ActionRename,
		audit.ActionDeleteProject, audit.ActionRenameProject:
	default:
		return index.AuthorEvent{}, false
	}
	return index.AuthorEvent{TS: e.TS, Action: string(e.Action), Path: e.Path, To: e.To, By: names.of(e)}, true
}

// authorNames shows who wrote an audit entry: a web UI write by its
// username; an MCP write by the token's name and the account that owns it,
// "claude-cli (admin)", or the name alone once the token is gone. The tokens
// are looked up in a map, rebuilt when an id is missing from it.
type authorNames struct {
	tokens   *auth.Store
	accounts *webauth.Store
	mu       sync.Mutex
	byID     map[string]auth.Token
}

func (n *authorNames) of(e audit.Entry) string {
	if e.Source == audit.SourceAutomation {
		return "automation"
	}
	if e.Source == audit.SourceHTTP {
		if e.Actor != "" {
			return e.Actor
		}
		if u, ok := n.user(e.UserID); ok {
			return u
		}
		return ""
	}
	name, _, _ := strings.Cut(e.Actor, "@")
	t, ok := n.token(e.Token)
	if !ok {
		if name == "" {
			return e.Token
		}
		return name
	}
	if name == "" {
		name = t.Name
	}
	owner := "admin"
	if t.OwnerUserID != "" {
		if u, ok := n.user(t.OwnerUserID); ok {
			owner = u
		}
	}
	return name + " (" + owner + ")"
}

func (n *authorNames) token(id string) (auth.Token, bool) {
	if id == "" || n.tokens == nil {
		return auth.Token{}, false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if t, ok := n.byID[id]; ok {
		return t, true
	}
	n.byID = map[string]auth.Token{}
	for _, t := range n.tokens.List() {
		n.byID[t.ID] = t
	}
	t, ok := n.byID[id]
	return t, ok
}

func (n *authorNames) user(id string) (string, bool) {
	if id == "" || n.accounts == nil {
		return "", false
	}
	if u, ok := n.accounts.UserByID(id); ok {
		return u.Username, true
	}
	return "", false
}
