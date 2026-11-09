package gitsync

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gosidian/gosidian/internal/layout"
	"github.com/gosidian/gosidian/internal/vault"
)

// The tags of a note (IMP-201): a version someone named, such as the one a
// reader must see while the note goes on changing. A tag sits on a commit,
// which can hold more notes, so its message ends with the line that says
// which one; a pipeline's tag has none and is of its whole commit.

// ErrBadLabel is a label no tag can have; ErrLabelTaken one on another
// version of the note.
var (
	ErrBadLabel   = errors.New("bad label")
	ErrLabelTaken = errors.New("label taken")
)

// MaxTagLabel caps the label of a note's tag, in bytes.
const MaxTagLabel = 64

// noteLine starts the last line of the message of a note's tag.
const noteLine = "Note: "

// NoteTag is a tag on a commit of a note's history.
type NoteTag struct {
	// Name is the whole tag; Label its last part, what people call it.
	Name, Label string
	SHA         string
	ShortSHA    string
	// By is who tagged, Date when; for a lightweight tag, Date is the
	// commit's and By is empty.
	By   string
	Date time.Time
	// Message is what the tag says, without the line naming the note.
	Message string
	// Path is the note's path in that commit.
	Path string
}

// TagDone is a tag of TagNote: Pushed says the push went, PushError why it
// did not, with push on.
type TagDone struct {
	NoteTag
	Pushed    bool
	PushError string
}

// NoteTagName is the tag of label on the note at rel:
// note/<path>/<label>. The path stays as it is when git takes it, .md
// included: a file is no folder, so no tag ever sits where another one
// needs a folder. Otherwise it is made valid with a mark of the real path,
// so that two notes never share a tag; if that is still no name, or too
// long, the mark alone.
func NoteTagName(rel, label string) (string, error) {
	switch {
	case label == "":
		return "", fmt.Errorf("%w: it is empty", ErrBadLabel)
	case len(label) > MaxTagLabel:
		return "", fmt.Errorf("%w %q: longer than %d bytes", ErrBadLabel, label, MaxTagLabel)
	case strings.Contains(label, "/"):
		return "", fmt.Errorf("%w %q: it holds /", ErrBadLabel, label)
	}
	valid := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return '-'
		}
		return r
	}, rel)
	sum := sha256.Sum256([]byte(rel))
	mark := hex.EncodeToString(sum[:6])
	var err error
	for _, path := range []string{rel, valid + "-" + mark, mark} {
		name := "note/" + path + "/" + label
		if err = CheckTag(name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("%w %q: %w", ErrBadLabel, label, err)
}

// TagNote names a version of the note at rel with label: the commit sha of
// its history, or without it the note's last commit, after committing what
// is pending. The tag is annotated, in the name of by, and message is what
// it says. The same label on the same version is the tag already there; on
// another version, under this name or an older one, it is an error, as a
// tag never moves. A version that is not there is ErrNoVersion. A failed
// push does not fail it: PushError says why, and the next push takes the
// tag.
func (s *Sync) TagNote(ctx context.Context, rel, sha, label, message, by string) (TagDone, error) {
	if !s.cfg.Enabled {
		return TagDone{}, ErrDisabled
	}
	name, err := NoteTagName(rel, label)
	if err != nil {
		return TagDone{}, err
	}
	message = strings.TrimSpace(message)
	if len(message) > MaxCommitMessage || strings.ContainsRune(message, 0) || !utf8.ValidString(message) {
		return TagDone{}, fmt.Errorf("%w: its message is not text, or too long", ErrBadLabel)
	}
	if s.run("git", "check-ref-format", "refs/tags/"+name) != nil {
		return TagDone{}, fmt.Errorf("%w: git does not take %q as a tag", ErrBadLabel, name)
	}
	if err := s.CommitWrites(); err != nil {
		return TagDone{}, err
	}

	s.workMu.Lock()
	defer s.workMu.Unlock()
	c, err := s.versionToTag(rel, sha)
	if err != nil {
		return TagDone{}, err
	}
	tags, err := s.NoteTags(rel)
	if err != nil {
		return TagDone{}, err
	}
	for _, t := range tags {
		if t.Label != label {
			continue
		}
		if t.SHA != c.SHA {
			return TagDone{}, fmt.Errorf("%w: %s is on commit %s already, and a tag is never moved", ErrLabelTaken, label, t.ShortSHA)
		}
		return TagDone{NoteTag: t}, nil
	}
	if message == "" {
		message = label + " of " + c.Path
	}
	if err := s.tagBy(by, name, c.SHA, message+"\n\n"+noteLine+c.Path); err != nil {
		return TagDone{}, err
	}
	done := TagDone{NoteTag: NoteTag{Name: name, Label: label, SHA: c.SHA, ShortSHA: c.ShortSHA, By: authorName(by), Date: time.Now(), Message: message, Path: c.Path}}
	if !s.cfg.Push {
		s.succeeded()
		return done, nil
	}
	if err := s.push(ctx, "refs/tags/"+name); err != nil {
		done.PushError = s.failed(err).Error()
		return done, nil
	}
	done.Pushed = true
	s.succeeded()
	return done, nil
}

// versionToTag finds the commit a tag of the note at rel goes on: sha, or
// without it the last one. A version where the note is deleted is none.
func (s *Sync) versionToTag(rel, sha string) (Commit, error) {
	if sha != "" {
		v, err := s.Version(rel, sha, 0)
		if err != nil {
			return Commit{}, err
		}
		if v.Status == "deleted" {
			return Commit{}, ErrNoVersion
		}
		return v.Commit, nil
	}
	h, err := s.history(rel, 1)
	if err != nil {
		return Commit{}, err
	}
	if len(h) == 0 || h[0].Status == "deleted" {
		return Commit{}, ErrNoVersion
	}
	return h[0], nil
}

// NoteTags gives the tags on the commits of the note's history, newest
// commit first and in order of name within a commit: those naming the note
// and those naming no note. A commit that deleted the note holds no
// version of it.
func (s *Sync) NoteTags(rel string) ([]NoteTag, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	commits, err := s.history(rel, 0)
	if err != nil {
		return nil, err
	}
	order := make(map[string]int, len(commits))
	for i, c := range commits {
		if c.Status != "deleted" {
			order[c.SHA] = i
		}
	}
	all, err := s.tags()
	if err != nil {
		return nil, err
	}
	var out []NoteTag
	for _, t := range all {
		i, ok := order[t.SHA]
		if !ok {
			continue
		}
		c := commits[i]
		if t.Path != "" && t.Path != c.Path {
			continue
		}
		t.Path, t.ShortSHA = c.Path, c.ShortSHA
		if t.Date.IsZero() {
			t.Date = c.Date
		}
		out = append(out, t)
	}
	slices.SortStableFunc(out, func(a, b NoteTag) int {
		return cmp.Or(cmp.Compare(order[a.SHA], order[b.SHA]), cmp.Compare(a.Name, b.Name))
	})
	return out, nil
}

// tags reads every tag of the vault: Path is the note it names, if any.
func (s *Sync) tags() ([]NoteTag, error) {
	out, err := s.capture("git", "for-each-ref", "refs/tags",
		"--format=%(refname:strip=2)%00%(objectname)%00%(*objectname)%00%(taggerdate:iso-strict)%00%(taggername)%00%(contents)%1e")
	if err != nil {
		return nil, err
	}
	var tags []NoteTag
	for _, rec := range strings.Split(out, "\x1e") {
		f := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x00", 6)
		if len(f) != 6 {
			continue
		}
		t := NoteTag{Name: f[0], SHA: f[1], By: f[4]}
		// A tag of a tag object is on the commit it peels to; a lightweight
		// one has no object, no date and no message of its own.
		if f[2] != "" {
			t.SHA = f[2]
			t.Date, _ = time.Parse(time.RFC3339, f[3])
			t.Message, t.Path = splitNoteLine(f[5])
		}
		t.Label = tagLabel(t.Name)
		tags = append(tags, t)
	}
	return tags, nil
}

// tagLabel is a tag's last part.
func tagLabel(tag string) string {
	return tag[strings.LastIndexByte(tag, '/')+1:]
}

// splitNoteLine parts a tag's message from its last line, when that names
// a note.
func splitNoteLine(message string) (text, path string) {
	message = strings.TrimSpace(message)
	i := strings.LastIndexByte(message, '\n') + 1
	if rest, ok := strings.CutPrefix(message[i:], noteLine); ok {
		return strings.TrimSpace(message[:i]), rest
	}
	return message, ""
}

// tagged reads what a tag says: its commit, and the note it names, if any.
// A tag that is not there is ErrNoVersion.
func (s *Sync) tagged(tag string) (Commit, string, error) {
	if tag == "" || s.run("git", "check-ref-format", "refs/tags/"+tag) != nil {
		return Commit{}, "", ErrNoVersion
	}
	out, err := s.capture("git", "for-each-ref", "refs/tags/"+tag,
		"--format=%(objectname)%00%(*objectname)%00%(*authorname) %(*authoremail)%00%(*authordate:iso-strict)%00%(*subject)%00%(contents)")
	if err != nil {
		return Commit{}, "", err
	}
	f := strings.SplitN(strings.TrimRight(out, "\n"), "\x00", 6)
	if len(f) != 6 {
		return Commit{}, "", ErrNoVersion
	}
	if f[1] == "" || len(f[1]) < 7 {
		return Commit{SHA: f[0]}, "", nil // lightweight: the tag is the commit
	}
	date, _ := time.Parse(time.RFC3339, f[3])
	_, note := splitNoteLine(f[5])
	return Commit{SHA: f[1], ShortSHA: f[1][:7], Author: f[2], Date: date, Subject: f[4]}, note, nil
}

// TagVersion reads the note at rel as the commit of tag holds it, as
// Version does. A tag that is not there, or not on the note's history, is
// ErrNoVersion. A tag that names the note says all it takes, in a few ms;
// the walk of Version is a second for an old version of a big vault (5,300
// commits).
func (s *Sync) TagVersion(rel, tag string, max int64) (Version, error) {
	if !s.cfg.Enabled {
		return Version{}, ErrDisabled
	}
	c, note, err := s.tagged(tag)
	if err != nil {
		return Version{}, err
	}
	if note == rel {
		c.Path = rel
		if v, err := s.readVersion(c, max); err == nil {
			return v, nil
		}
	}
	return s.Version(rel, c.SHA, max)
}

// chosenVersion reads the version a note names as its valid one. Unlike a
// tag anyone may ask for, that key only the project's admin writes, so the
// tag is of the note whatever its path is now: read under the name it had,
// without the history, which does not know a rename until its commit. A tag
// that names no note, a pipeline's, is read at rel, and the walk finds it
// under an older name.
func (s *Sync) chosenVersion(rel, tag string, max int64) (Version, error) {
	if !s.cfg.Enabled {
		return Version{}, ErrDisabled
	}
	c, note, err := s.tagged(tag)
	if err != nil {
		return Version{}, err
	}
	if c.Path = note; c.Path == "" {
		c.Path = rel
	}
	if c.ShortSHA != "" {
		if v, err := s.readVersion(c, max); err == nil {
			return v, nil
		}
	}
	return s.Version(rel, c.SHA, max)
}

// FindTag finds want among a note's tags: a whole name, or a label only one
// of them has.
func FindTag(tags []NoteTag, want string) (NoteTag, bool) {
	var found []NoteTag
	for _, t := range tags {
		if t.Name == want {
			return t, true
		}
		if t.Label == want {
			found = append(found, t)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return NoteTag{}, false
}

// Valid is the valid version of a note, as a reader gets it.
type Valid struct {
	// Tag is the tag the note names, Label its last part.
	Tag, Label string
	// Version is the note at that tag, when Missing is false.
	Version
	// Same says the note now is that version, but for the key itself.
	Same bool
	// Missing says the tag gives no text: it is not there, not on the
	// note's history, over max bytes, or git sync is off. A reader sees the
	// note as it is, and is told.
	Missing bool
}

// Valid reads the valid version content names, the text of the note at rel
// now: nil for a note without one. It works on a nil Sync, for a server
// without git sync.
func (s *Sync) Valid(rel string, content []byte, max int64) *Valid {
	tag := vault.ValidVersion(rel, content)
	if tag == "" {
		return nil
	}
	return s.chosen(rel, content, tag, max)
}

// ForMCP reads the version content chooses for who calls over MCP (IMP-228):
// nil for a note without a choice, or one that follows the valid version
// where there is none. It works on a nil Sync, as Valid does.
func (s *Sync) ForMCP(rel string, content []byte, max int64) *Valid {
	switch tag := vault.MCPVersion(rel, content); tag {
	case "":
		return nil
	case vault.FollowValid:
		return s.Valid(rel, content, max)
	default:
		return s.chosen(rel, content, tag, max)
	}
}

// chosen reads the tag a key of content names.
func (s *Sync) chosen(rel string, content []byte, tag string, max int64) *Valid {
	out := &Valid{Tag: tag, Label: tagLabel(tag), Missing: true}
	if s == nil {
		return out
	}
	v, err := s.chosenVersion(rel, tag, max)
	if err != nil || v.Content == nil {
		return out
	}
	out.Version, out.Missing = v, false
	out.Same = bytes.Equal(vault.WithoutVersionKeys(rel, v.Content), vault.WithoutVersionKeys(rel, content))
	return out
}

// FileAt reads the file rel as commit sha holds it, at most max bytes when
// max is above 0: ErrNoVersion when the commit has no such file, or it is
// larger.
func (s *Sync) FileAt(sha, rel string, max int64) ([]byte, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	object := sha + ":" + rel
	size, err := s.capture("git", "cat-file", "-s", object)
	if err != nil {
		return nil, ErrNoVersion
	}
	n, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
	if err != nil {
		return nil, err
	}
	if max > 0 && n > max {
		return nil, ErrNoVersion
	}
	data, err := s.capture("git", "cat-file", "blob", object)
	if err != nil {
		return nil, err
	}
	return []byte(data), nil
}

// maxDocument caps the design read from a version, and versionTextMax a
// note's version, for what its attachments are: well above what a write
// lets through, so nothing that exists is left out.
const (
	maxDocument    = 1 << 30
	versionTextMax = 1 << 30
)

// versionTexts gives what names the attachments of a version: its text and
// the document of its design, which names the images placed.
func (s *Sync) versionTexts(v Version) ([][]byte, error) {
	texts := [][]byte{v.Content}
	if design := layout.DesignFile(v.Content); design != "" {
		doc, err := s.FileAt(v.SHA, design, maxDocument)
		if err != nil && !errors.Is(err, ErrNoVersion) {
			return nil, err
		}
		if doc != nil {
			texts = append(texts, doc)
		}
	}
	return texts, nil
}

// ValidText gives the text of the valid version of the note at rel, whose
// text now is body: "" for a note without one, or whose version does not
// read. What the full-text index searches (index.SetSearchText): a note's
// key says the version, so its hash says all of it.
func (s *Sync) ValidText(rel, body string) string {
	if !strings.Contains(body, vault.ValidVersionKey) {
		return ""
	}
	v := s.Valid(rel, []byte(body), versionTextMax)
	if v == nil || v.Missing {
		return ""
	}
	return string(v.Content)
}

// ValidTexts gives what names the attachments the valid version tag of the
// note at rel needs, for the attachments' GC; nothing for a tag that gives
// no version. Any other failure is an error: the GC must not guess.
func (s *Sync) ValidTexts(rel, tag string) ([][]byte, error) {
	if s == nil {
		return nil, nil
	}
	v, err := s.chosenVersion(rel, tag, versionTextMax)
	if errors.Is(err, ErrNoVersion) || errors.Is(err, ErrDisabled) || (err == nil && v.Content == nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.versionTexts(v)
}

// RestoreFiles brings back the attachments the version names and vlt no
// longer holds, as the commit has them, from the attachments of the
// version's project: a version chosen long after its images went through
// the GC. It gives the paths restored, those it could save before an error.
func (s *Sync) RestoreFiles(v Version, vlt *vault.Vault, allowedExt map[string]bool) ([]string, error) {
	project, _, ok := strings.Cut(v.Path, "/")
	if !ok || v.Content == nil {
		return nil, nil
	}
	out, err := s.capture("git", "--literal-pathspecs", "ls-tree", "-r", "-z", "--name-only", v.SHA, "--", project+"/")
	if err != nil {
		return nil, err
	}
	byName := map[string][]string{}
	var names []string
	for _, rel := range strings.Split(out, "\x00") {
		if !strings.Contains("/"+rel, "/attachments/") || vlt.HasFile(rel) {
			continue
		}
		name := path.Base(rel)
		if byName[name] == nil {
			names = append(names, name)
		}
		byName[name] = append(byName[name], rel)
	}
	if len(names) == 0 {
		return nil, nil
	}
	texts, err := s.versionTexts(v)
	if err != nil {
		return nil, err
	}
	var restored []string
	for _, name := range vault.NamedIn(names, texts) {
		for _, rel := range byName[name] {
			data, err := s.FileAt(v.SHA, rel, 0)
			if err != nil {
				return restored, err
			}
			if err := vlt.SaveAttachment(rel, data, allowedExt); err != nil {
				return restored, err
			}
			restored = append(restored, rel)
		}
	}
	return restored, nil
}
