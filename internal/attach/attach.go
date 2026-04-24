// Package attach provides shared file-attachment validation, hashing, and
// storage logic used by both the HTTP upload handler and the MCP tools.
package attach

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxBytes is the largest raw attachment the system accepts (10 MiB).
const MaxBytes = 10 << 20

// ExtInfo describes an allowed attachment extension.
type ExtInfo struct {
	MIME    string
	IsImage bool
}

// AllowedExt maps lowercase extensions to their metadata. Only files whose
// extension appears here are accepted.
var AllowedExt = map[string]ExtInfo{
	// Images
	".png":  {MIME: "image/png", IsImage: true},
	".jpg":  {MIME: "image/jpeg", IsImage: true},
	".jpeg": {MIME: "image/jpeg", IsImage: true},
	".gif":  {MIME: "image/gif", IsImage: true},
	".webp": {MIME: "image/webp", IsImage: true},
	".svg":  {MIME: "image/svg+xml", IsImage: true},
	// Documents
	".pdf":  {MIME: "application/pdf", IsImage: false},
	".csv":  {MIME: "text/csv", IsImage: false},
	".json": {MIME: "application/json", IsImage: false},
	".txt":  {MIME: "text/plain", IsImage: false},
	".zip":  {MIME: "application/zip", IsImage: false},
	".docx": {MIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", IsImage: false},
	".xlsx": {MIME: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", IsImage: false},
}

// ValidateExt checks that ext (lowercase, with leading dot) is in the
// allowlist. Returns the MIME type, whether it is an image, or an error.
func ValidateExt(ext string) (mime string, isImage bool, err error) {
	info, ok := AllowedExt[strings.ToLower(ext)]
	if !ok {
		return "", false, fmt.Errorf("unsupported file type: %s", ext)
	}
	return info.MIME, info.IsImage, nil
}

// HashFilename computes a short, collision-resistant filename from the file
// data: the first 16 hex characters of the SHA-256 digest plus the extension.
func HashFilename(data []byte, ext string) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16] + ext
}

// RelPath builds the vault-relative storage path for an attachment.
// With a project it returns "<project>/attachments/<filename>"; without one
// it returns "attachments/<filename>".
func RelPath(project, filename string) string {
	if project != "" {
		return project + "/attachments/" + filename
	}
	return "attachments/" + filename
}

// MarkdownRef returns the markdown syntax to embed or link the attachment.
// Images get ![](/vault-files/...), non-images get [origName](/vault-files/...).
func MarkdownRef(vaultRelPath, origFilename string, isImage bool) string {
	url := "/vault-files/" + vaultRelPath
	if isImage {
		return "![](" + url + ")"
	}
	return "[" + origFilename + "](" + url + ")"
}

// Saver is the subset of vault.Vault that Store needs.
type Saver interface {
	Rel(p string) (string, error)
	Save(rel string, content []byte) error
}

// Result holds what Store returns on success.
type Result struct {
	Path     string // vault-relative path
	Filename string // hashed filename
	Markdown string // ready-to-insert markdown
}

// ValidateSourcePath checks that sourcePath is an absolute path pointing to an
// existing regular file inside one of the allowedRoots. Returns an error
// describing the violation, or nil when the path is safe to read.
func ValidateSourcePath(sourcePath string, allowedRoots []string) error {
	clean := filepath.Clean(sourcePath)
	if !filepath.IsAbs(clean) {
		return errors.New("source_path must be absolute")
	}
	allowed := false
	for _, root := range allowedRoots {
		root = filepath.Clean(root)
		// Ensure the root ends with a separator so "/vault" doesn't match "/vault-other".
		prefix := root + string(filepath.Separator)
		if strings.HasPrefix(clean, prefix) || clean == root {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("source_path %q is not inside any allowed upload root", sourcePath)
	}
	fi, err := os.Stat(clean)
	if err != nil {
		return fmt.Errorf("source_path: %w", err)
	}
	if fi.IsDir() {
		return errors.New("source_path is a directory, not a file")
	}
	return nil
}

// StoreFromPath reads a file from the local filesystem and stores it as an
// attachment. The file must be inside one of allowedRoots. If overrideFilename
// is empty, the basename of sourcePath is used for extension detection and
// markdown link text.
func StoreFromPath(v Saver, sourcePath, overrideFilename, project string, allowedRoots []string) (*Result, error) {
	if err := ValidateSourcePath(sourcePath, allowedRoots); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Clean(sourcePath))
	if err != nil {
		return nil, fmt.Errorf("read source file: %w", err)
	}
	filename := overrideFilename
	if filename == "" {
		filename = filepath.Base(sourcePath)
	}
	return Store(v, data, filename, project)
}

// Store validates, hashes, and persists an attachment. origFilename is the
// user-provided name (used for extension detection and link text).
func Store(v Saver, data []byte, origFilename, project string) (*Result, error) {
	ext := strings.ToLower(filepath.Ext(origFilename))
	_, isImage, err := ValidateExt(ext)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBytes {
		return nil, errors.New("file too large (max 10 MiB)")
	}

	filename := HashFilename(data, ext)
	rel := RelPath(project, filename)

	if _, err := v.Rel(rel); err != nil {
		return nil, fmt.Errorf("invalid attachment path: %w", err)
	}
	if err := v.Save(rel, data); err != nil {
		return nil, fmt.Errorf("save attachment: %w", err)
	}

	return &Result{
		Path:     rel,
		Filename: filename,
		Markdown: MarkdownRef(rel, origFilename, isImage),
	}, nil
}
