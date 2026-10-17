package gitsync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// The commit a pipeline asks for (IMP-201): the node vault.commit makes
// one commit per revision of a document, at once, and may name it with a
// tag, doc/<code>/<revision>, so that `git show <tag>:<path>` gives that
// revision back.

// ErrDisabled is CommitNow's error while git sync is off.
var ErrDisabled = errors.New("git sync is off")

// MaxCommitMessage caps the message of CommitNow, in bytes.
const MaxCommitMessage = 8 << 10

// CommitResult is what CommitNow did.
type CommitResult struct {
	// SHA is the commit made, or HEAD when nothing was pending; Short its
	// abbreviation.
	SHA   string
	Short string
	// Committed is false when nothing was pending.
	Committed bool
	// Tag is the tag on SHA, when one was asked: made, or there already.
	Tag string
	// Pushed says the push went; PushError why it did not, with push on.
	Pushed    bool
	PushError string
}

// CommitNow commits at once what is pending in the vault, as the debounced
// commit would (the same author, the same files left out, the listeners
// told), with message. With tag, it puts that annotated tag on the commit,
// or on HEAD when nothing was pending: a tag already there on the same
// commit is fine, on another one an error, as a tag never moves. Then,
// with push on, it pushes the branch and the tag. It runs one at a time
// with the debounced commits and takes over a pending one. A failed push
// does not fail it: PushError says why, and the status records it.
func (s *Sync) CommitNow(ctx context.Context, message, tag string) (CommitResult, error) {
	if !s.cfg.Enabled {
		return CommitResult{}, ErrDisabled
	}
	message = strings.TrimSpace(message)
	switch {
	case message == "":
		return CommitResult{}, errors.New("the commit message is empty")
	case len(message) > MaxCommitMessage:
		return CommitResult{}, fmt.Errorf("the commit message is %d bytes, at most %d", len(message), MaxCommitMessage)
	case strings.ContainsRune(message, 0) || !utf8.ValidString(message):
		return CommitResult{}, errors.New("the commit message is not text")
	}
	if tag != "" {
		if err := CheckTag(tag); err != nil {
			return CommitResult{}, err
		}
	}
	s.mu.Lock()
	down, why := s.initFailed, s.status.LastError
	s.mu.Unlock()
	if down {
		return CommitResult{}, fmt.Errorf("git sync did not start: %s", why)
	}
	if tag != "" && s.run("git", "check-ref-format", "refs/tags/"+tag) != nil {
		return CommitResult{}, fmt.Errorf("tag %q: git does not take it as a tag name", tag)
	}
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()

	s.workMu.Lock()
	defer s.workMu.Unlock()
	// What a write triggers from now on goes to the next commit, as with
	// a flush.
	s.mu.Lock()
	s.pending = false
	s.mu.Unlock()

	committed, err := s.commit(message)
	if err != nil {
		return CommitResult{}, s.failed(err)
	}
	sha, short, err := s.head()
	if err != nil {
		if !committed {
			return CommitResult{}, fmt.Errorf("nothing to commit, and no commit yet: %w", err)
		}
		return CommitResult{}, s.failed(err)
	}
	res := CommitResult{SHA: sha, Short: short, Committed: committed}
	if tag != "" {
		if err := s.tag(tag, sha, message); err != nil {
			if committed {
				return res, fmt.Errorf("committed %s, but %w", short, err)
			}
			return res, err
		}
		res.Tag = tag
	}
	if !s.cfg.Push || (!committed && tag == "") {
		s.succeeded()
		return res, nil
	}
	var refs []string
	if tag != "" {
		refs = []string{"refs/tags/" + tag}
	}
	if err := s.push(ctx, refs...); err != nil {
		res.PushError = s.failed(err).Error()
		return res, nil
	}
	res.Pushed = true
	s.succeeded()
	return res, nil
}

// head reads HEAD's SHA and its abbreviation.
func (s *Sync) head() (sha, short string, err error) {
	out, err := s.capture("git", "log", "-1", "--format=%H %h")
	if err != nil {
		return "", "", err
	}
	sha, short, ok := strings.Cut(strings.TrimSpace(out), " ")
	if !ok || len(sha) != 40 {
		return "", "", fmt.Errorf("git log: %q", out)
	}
	return sha, short, nil
}

// tag puts the annotated tag name, with message, on sha. A tag there
// already on sha is fine; on another commit it is an error.
func (s *Sync) tag(name, sha, message string) error {
	if have, err := s.capture("git", "rev-parse", "-q", "--verify", "refs/tags/"+name+"^{commit}"); err == nil {
		have = strings.TrimSpace(have)
		if have == sha {
			return nil
		}
		return fmt.Errorf("tag %s is on commit %.12s already, not on %.12s: a tag is never moved", name, have, sha)
	}
	if err := s.run("git", "tag", "-a", "-m", message, name, sha); err != nil {
		return fmt.Errorf("git tag %s: %w", name, err)
	}
	return nil
}

// MaxTagLen caps a tag of CommitNow, in bytes.
const MaxTagLen = 200

// tagReserved are first parts a tag may not have: git's own names for the
// kinds of refs, and the remote, which a tag would shadow or be shadowed
// by when a name is resolved.
var tagReserved = []string{"refs", "heads", "tags", "remotes", "origin", "HEAD"}

// CheckTag refuses a tag name git would not take (the rules of git
// check-ref-format), or one that could pass for something else. A tag of
// the vault must have at least two parts (doc/manual-A-en/V2), its first
// part not v and a digit (v2, v2.71.3: a release, if the vault and the
// code ever share a remote) nor one of git's own names (refs, heads, tags,
// remotes, origin, HEAD).
func CheckTag(name string) error {
	bad := func(why string) error { return fmt.Errorf("tag %q: %s", name, why) }
	switch {
	case name == "":
		return errors.New("an empty tag")
	case len(name) > MaxTagLen:
		return bad(fmt.Sprintf("longer than %d bytes", MaxTagLen))
	case !utf8.ValidString(name):
		return bad("not UTF-8")
	case strings.HasPrefix(name, "-"):
		return bad("it starts with -")
	case strings.Contains(name, ".."):
		return bad("it holds ..")
	case strings.Contains(name, "@{") || name == "@":
		return bad("it holds @{")
	case strings.HasSuffix(name, "."):
		return bad("it ends with .")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return bad("it holds a control character")
		}
		if strings.ContainsRune(" ~^:?*[\\", r) {
			return bad(fmt.Sprintf("it holds %q", r))
		}
	}
	parts := strings.Split(name, "/")
	for _, p := range parts {
		switch {
		case p == "":
			return bad("an empty part: it starts or ends with /, or holds //")
		case strings.HasPrefix(p, "."):
			return bad("a part starts with .")
		case strings.HasSuffix(p, ".lock"):
			return bad("a part ends with .lock")
		}
	}
	if len(parts) < 2 {
		return bad("it needs a / (doc/<code>/<revision>)")
	}
	first := parts[0]
	if len(first) >= 2 && (first[0] == 'v' || first[0] == 'V') && first[1] >= '0' && first[1] <= '9' {
		return bad("it starts like a release tag (v and a digit)")
	}
	for _, r := range tagReserved {
		if strings.EqualFold(first, r) {
			return bad(fmt.Sprintf("its first part is %s, a name of git's own", r))
		}
	}
	return nil
}
