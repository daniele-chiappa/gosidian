package main

import (
	"log"
	"slices"
	"strings"
	"sync"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/marks"
	"github.com/gosidian/gosidian/internal/trash"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
)

// wireAuthors keeps the authors of the notes in the index (IMP-127
// iteration 3): the audit log replayed at start, then each write as the log
// takes it. Errors are logged, never fatal: the authors are a convenience
// of queries, not a guarantee.
func wireAuthors(idx *index.Index, al *audit.Log, names *authorNames) {
	var events []index.AuthorEvent
	if err := al.Each(func(e audit.Entry) {
		events = append(events, authorEvents(e, names)...)
	}); err != nil {
		log.Printf("authors: read the audit log: %v", err)
	}
	if err := idx.RebuildAuthors(events); err != nil {
		log.Printf("authors: rebuild: %v", err)
	} else {
		log.Printf("authors: %d write(s) replayed from the audit log", len(events))
	}
	al.OnWrite(func(e audit.Entry) {
		for _, ev := range authorEvents(e, names) {
			if err := idx.RecordAuthor(ev); err != nil {
				log.Printf("authors: record %s %s: %v", ev.Action, ev.Path, err)
			}
		}
	})
}

// wireGitWriters tells the git sync who wrote each note and attachment
// (IMP-222, F-01, BUG-148): the commit that takes its file is made in that
// name, as modified_by shows it, and says what was done. v's locks tell it
// the writes on their way, whose files wait for the commit after their
// record (BUG-147).
func wireGitWriters(s *gitsync.Sync, al *audit.Log, names *authorNames, v *vault.Vault) {
	if v != nil {
		s.SetBusy(v.PathBusy)
	}
	al.OnWrite(func(e audit.Entry) {
		if origin, _, ok := restoreFromTrash(e); ok {
			s.RecordWrite(gitsync.RestoreFromTrash, origin, e.Path, names.of(e))
			return
		}
		// The attachments are files of the history too, not notes of the
		// authors (BUG-148), and who sets a valid version is no author.
		if e.Action == audit.ActionUploadAttachment || e.Action == audit.ActionDeleteAttachment || e.Action == audit.ActionNoteValid {
			s.RecordWrite(string(e.Action), e.Path, e.To, names.of(e))
			return
		}
		// A project's flags take it out of git, or track it again, at the
		// commit after: no file changes to ask for it.
		if e.Action == audit.ActionProjectFlagsUpdate {
			s.RequestCommit()
			return
		}
		if ev, ok := authorEvent(e, names); ok {
			s.RecordWrite(ev.Action, ev.Path, ev.To, ev.By)
		}
	})
}

// authorEvents reads an audit entry as the writes of the notes it holds,
// for their authors in the index: a restore from the trash is a create of
// each note it brought back, by who restored it (BUG-148).
func authorEvents(e audit.Entry, names *authorNames) []index.AuthorEvent {
	if _, notes, ok := restoreFromTrash(e); ok {
		by := names.of(e)
		evs := make([]index.AuthorEvent, 0, len(notes))
		for _, n := range notes {
			evs = append(evs, index.AuthorEvent{TS: e.TS, Action: string(audit.ActionCreate), Path: n, By: by})
		}
		return evs
	}
	if ev, ok := authorEvent(e, names); ok {
		return []index.AuthorEvent{ev}
	}
	return nil
}

// restoreFromTrash reads an audit entry as a restore from the trash: a
// create whose path is the trash entry's id, and whose To lists the notes
// restored, joined by commas (internal/api/v1/trash.go). It gives the place
// restored, a note, a folder or a project, from the id, and the notes under
// it (BUG-148). The log keeps that shape, so the restores already in it read
// the same. A part of To that starts no path under the place goes with the
// one before: a note's name may hold a comma.
func restoreFromTrash(e audit.Entry) (origin string, notes []string, ok bool) {
	if e.Action != audit.ActionCreate || strings.Contains(e.Path, "/") {
		return "", nil, false
	}
	if origin = trash.Origin(e.Path); !trash.ValidOrigin(origin) {
		return "", nil, false
	}
	inside := func(p string) bool { return p == origin || strings.HasPrefix(p, origin+"/") }
	for _, part := range strings.Split(e.To, ",") {
		if n := len(notes); n > 0 && !inside(part) {
			notes[n-1] += "," + part
		} else {
			notes = append(notes, part)
		}
	}
	return origin, slices.DeleteFunc(notes, func(p string) bool { return !inside(p) }), true
}

// authorEvent reads an audit entry as a write of a note, or of a project's
// notes; other entries (attachments, tokens, settings…) are not.
func authorEvent(e audit.Entry, names *authorNames) (index.AuthorEvent, bool) {
	switch e.Action {
	case audit.ActionCreate, audit.ActionUpdate, audit.ActionAppend, audit.ActionDelete, audit.ActionRename,
		audit.ActionRestore, audit.ActionDeleteProject, audit.ActionRenameProject:
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

// wireChat tells the bots' service of the notes written, moved and
// deleted (IMP-191): a bot's note is handed to its plugin again soon after.
// A folder or a project goes as its path with a trailing slash, and a
// restore from the trash as the place it brings back (BUG-152).
func wireChat(c interface{ Changed(path string) }, al *audit.Log) {
	al.OnWrite(func(e audit.Entry) {
		if origin, _, ok := restoreFromTrash(e); ok {
			c.Changed(chatPlace(origin))
			return
		}
		switch e.Action {
		case audit.ActionCreate, audit.ActionUpdate, audit.ActionAppend, audit.ActionRestore:
			c.Changed(e.Path)
		case audit.ActionDelete:
			c.Changed(chatPlace(e.Path))
		case audit.ActionRename:
			c.Changed(chatPlace(e.Path))
			c.Changed(chatPlace(e.To))
		case audit.ActionDeleteProject:
			c.Changed(e.Path + "/")
		case audit.ActionRenameProject:
			c.Changed(e.Path + "/")
			c.Changed(e.To + "/")
		}
	})
}

// chatPlace is a path as the bots' service reads it: a note as it is, any
// other path, a folder or a project, with a trailing slash (as for the
// marks, "v1.2" is a folder).
func chatPlace(p string) string {
	if strings.HasSuffix(strings.ToLower(p), ".md") {
		return p
	}
	return p + "/"
}

// wireMarks keeps the marks of the notes with them (IMP-188): a note, a
// folder or a project renamed takes its marks along, deleted loses them.
func wireMarks(m *marks.Store, al *audit.Log) {
	al.OnWrite(func(e audit.Entry) {
		var err error
		// Marks are on markdown notes: any other path is a folder, "v1.2"
		// included (a file renamed then matches no set).
		folder := !strings.HasSuffix(strings.ToLower(e.Path), ".md")
		switch e.Action {
		case audit.ActionRename:
			if e.To == "" {
				return
			}
			if folder {
				err = m.Renamed(e.Path+"/", e.To+"/")
			} else {
				err = m.Renamed(e.Path, e.To)
			}
		case audit.ActionDelete:
			if folder {
				err = m.Deleted(e.Path + "/")
			} else {
				err = m.Deleted(e.Path)
			}
		case audit.ActionRenameProject:
			err = m.Renamed(e.Path+"/", e.To+"/")
		case audit.ActionDeleteProject:
			err = m.Deleted(e.Path + "/")
		default:
			return
		}
		if err != nil {
			log.Printf("marks: %s %s: %v", e.Action, e.Path, err)
		}
	})
}
