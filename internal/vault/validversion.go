package vault

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/gosidian/gosidian/internal/frontmatter"
	"github.com/gosidian/gosidian/internal/parser"
)

// The valid version of a note (IMP-201): the frontmatter key that names the
// tag readers must see, while the note goes on changing. It sits in the
// note's text so that it follows renames, the trash and git, and it is the
// project admin's to set, not the writers': every write of a note comes
// through save, which leaves it as it was, and only SetValidVersion changes
// it. What writes to git or the disk directly is outside gosidian's rights.

// ValidVersionKey is the frontmatter key of the valid version.
const ValidVersionKey = "valid_version"

// MCPVersionKey is the frontmatter key of the version who calls over MCP
// reads (IMP-228), while the note changes: a tag, or FollowValid. It works as
// the valid version does, and is set apart from it: without it they read the
// note as it is.
const MCPVersionKey = "mcp_version"

// FollowValid as the value of MCPVersionKey is the valid version, whichever
// that is: changing it needs no second choice.
const FollowValid = "valid"

// versionKeys are the keys only their own way changes.
var versionKeys = []string{ValidVersionKey, MCPVersionKey}

// ErrValidVersion is a write that cannot keep the note's valid version, or
// a valid version for a note that cannot have one.
var ErrValidVersion = errors.New("valid version")

// ValidVersion reads the valid version from the text of the note at rel: ""
// for none.
func ValidVersion(rel string, content []byte) string {
	return versionKey(rel, content, ValidVersionKey)
}

// MCPVersion reads what the note chooses for who calls over MCP: "" for none.
func MCPVersion(rel string, content []byte) string {
	return versionKey(rel, content, MCPVersionKey)
}

// versionKey reads key from the frontmatter of the note at rel.
func versionKey(rel string, content []byte, key string) string {
	if !isMarkdown(rel) || !bytes.Contains(content, []byte(key)) {
		return ""
	}
	tag, _ := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw(content))[key].(string)
	return tag
}

// chosenTags are the tags the text of the note at rel chooses, for readers
// and for MCP: what the GC must keep.
func chosenTags(rel string, content []byte) []string {
	var tags []string
	if tag := ValidVersion(rel, content); tag != "" {
		tags = append(tags, tag)
	}
	if tag := MCPVersion(rel, content); tag != "" && tag != FollowValid && !slices.Contains(tags, tag) {
		tags = append(tags, tag)
	}
	return tags
}

// isMarkdown says whether the note at rel has a frontmatter of ---.
func isMarkdown(rel string) bool {
	return strings.EqualFold(filepath.Ext(rel), ".md")
}

// keepVersions gives content with the version keys of the file at abs, which
// it replaces: the same text when that is what it has. A write that cannot
// have them is refused: without the key the reader would see a draft.
func keepVersions(rel, abs string, content []byte) ([]byte, error) {
	if !isMarkdown(rel) {
		return content, nil
	}
	old, err := os.ReadFile(abs)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, key := range versionKeys {
		keep := versionKey(rel, old, key)
		if keep == versionKey(rel, content, key) {
			continue
		}
		if content, err = withVersionKey(content, key, keep); err != nil {
			return nil, err
		}
	}
	return content, nil
}

// WithoutVersionKeys gives the text of the note at rel without those keys,
// to compare two versions: a text that cannot lose them stays as it is.
func WithoutVersionKeys(rel string, content []byte) []byte {
	out := content
	for _, key := range versionKeys {
		if versionKey(rel, out, key) == "" {
			continue
		}
		bare, err := withVersionKey(out, key, "")
		if err != nil {
			return content
		}
		out = bare
	}
	return out
}

// emptyFrontmatter is what removing the only key of a frontmatter leaves.
var emptyFrontmatter = regexp.MustCompile(`^---\r?\n---\r?\n`)

// withVersionKey gives content with key at tag, or without it for "", and
// then without the frontmatter the key alone made.
func withVersionKey(content []byte, key, tag string) ([]byte, error) {
	var set []frontmatter.Field
	var unset []string
	if tag == "" {
		unset = []string{key}
	} else {
		set = []frontmatter.Field{{Key: key, Value: tag}}
	}
	out, err := frontmatter.SetKeys(content, set, unset)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValidVersion, err)
	}
	if tag == "" {
		out = emptyFrontmatter.ReplaceAll(out, nil)
	}
	return out, nil
}

// SetValidVersion writes tag as the valid version of the note at rel, or
// removes it for "". It is the only way to change it, and asks nothing
// about who may: that, and the tag being one of the note, is the caller's.
// The caller holds the note's lock, and indexes the note after.
func (v *Vault) SetValidVersion(rel, tag string) error {
	return v.setVersionKey(rel, ValidVersionKey, tag)
}

// SetMCPVersion writes what the note at rel chooses for who calls over MCP,
// a tag or FollowValid, or removes it for "". What SetValidVersion says of
// who may and of the lock holds here.
func (v *Vault) SetMCPVersion(rel, tag string) error {
	return v.setVersionKey(rel, MCPVersionKey, tag)
}

func (v *Vault) setVersionKey(rel, key, tag string) error {
	r, err := v.Rel(rel)
	if err != nil {
		return err
	}
	if err := v.checkNote(r); err != nil {
		return err
	}
	if !isMarkdown(r) {
		return fmt.Errorf("%w: only a markdown note has one, and %q is not", ErrValidVersion, r)
	}
	abs := filepath.Join(v.Root, filepath.FromSlash(r))
	old, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	content, err := withVersionKey(old, key, tag)
	if err != nil {
		return err
	}
	return v.write(r, abs, content, false)
}

// HasFile says whether the vault holds the file at rel.
func (v *Vault) HasFile(rel string) bool {
	r, err := v.Rel(rel)
	if err != nil {
		return false
	}
	_, err = os.Lstat(filepath.Join(v.Root, filepath.FromSlash(r)))
	return err == nil
}
