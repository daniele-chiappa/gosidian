// Package attach provides shared file-attachment validation, hashing, and
// storage logic used by both the HTTP upload handler and the MCP tools.
package attach

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// DataURI builds an RFC 2397 data: URI for the given bytes, using the MIME of
// ext from AllowedExt. Used to inline vault attachments for the HTML-note
// render and for self-contained downloads, so the stored note can keep a
// lightweight reference while the presentation layer embeds the bytes.
//
// Fails closed: an extension outside AllowedExt returns "" rather than falling
// back to application/octet-stream, so a file that was never meant to be an
// attachment (a .jsonl, .db, .toml under the vault) cannot be embedded by a
// caller that forgot to filter. Callers must treat "" as "do not inline".
func DataURI(data []byte, ext string) string {
	info, ok := AllowedExt[strings.ToLower(ext)]
	if !ok {
		return ""
	}
	return "data:" + info.MIME + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// SetInertHeaders makes a response carrying vault bytes inert (ADR-021,
// BUG-030): whatever the content is — an SVG with a <script>, an HTML note —
// nothing executes in the app origin when the URL is opened directly. Shared
// by /vault-files/ and the MCP /download endpoint. The sandbox CSP does not
// affect <img> embedding.
func SetInertHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'none'; style-src 'none'; sandbox")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
}

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

// ExtSet returns the allowed extensions as a set — the shape the vault's
// attachment API and ListAttachments take (vault must not import attach).
func ExtSet() map[string]bool {
	set := make(map[string]bool, len(AllowedExt))
	for ext := range AllowedExt {
		set[ext] = true
	}
	return set
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

// VerifyMIME inspects the first 512 bytes of content and confirms the
// detected MIME family is compatible with declaredExt. This catches MIME
// spoofing where a caller declares ".png" but uploads a JS payload (only
// the extension is whitelisted by ValidateExt — without this check the
// content could be anything).
//
// The check is family-tolerant rather than exact:
//   - Images: detected MIME must begin with "image/" (covers webp, animated
//     gif, exotic png variants). SVG is text-based so text/* and
//     image/svg+xml are both accepted.
//   - PDF / ZIP: exact detected MIME match.
//   - DOCX / XLSX: these are zip containers, so application/zip is accepted
//     (also application/octet-stream as a tolerant fallback).
//   - JSON: text/* prefix or application/json.
//   - CSV / TXT: text/* prefix (DetectContentType conflates them).
//
// Returns the detected MIME on success, or an error describing the
// mismatch when content does not match the declared extension.
func VerifyMIME(content []byte, declaredExt string) (string, error) {
	info, ok := AllowedExt[strings.ToLower(declaredExt)]
	if !ok {
		return "", fmt.Errorf("unsupported file type: %s", declaredExt)
	}
	if len(content) == 0 {
		return "", errors.New("empty file content")
	}

	probe := content
	if len(probe) > 512 {
		probe = probe[:512]
	}
	detected := http.DetectContentType(probe)
	// Strip "; charset=..." suffix so comparisons are stable.
	if i := strings.Index(detected, ";"); i >= 0 {
		detected = strings.TrimSpace(detected[:i])
	}

	if info.IsImage {
		// SVG is XML/text — accept text/* and image/svg+xml.
		if strings.ToLower(declaredExt) == ".svg" {
			if strings.HasPrefix(detected, "text/") || detected == "image/svg+xml" {
				return info.MIME, nil
			}
			return "", mimeMismatch(declaredExt, info.MIME, detected)
		}
		// Other images must detect as some image/* family.
		if !strings.HasPrefix(detected, "image/") {
			return "", mimeMismatch(declaredExt, info.MIME, detected)
		}
		return detected, nil
	}

	// Document handling: per-extension tolerance.
	switch strings.ToLower(declaredExt) {
	case ".pdf":
		if detected != "application/pdf" {
			return "", mimeMismatch(declaredExt, "application/pdf", detected)
		}
	case ".zip":
		if detected != "application/zip" {
			return "", mimeMismatch(declaredExt, "application/zip", detected)
		}
	case ".docx", ".xlsx":
		// Both are zip containers; DetectContentType returns application/zip.
		// Some minimal/empty office files detect as octet-stream — accept that
		// as a tolerant fallback rather than reject legitimate empty templates.
		if detected != "application/zip" && detected != "application/octet-stream" {
			return "", mimeMismatch(declaredExt, "application/zip (zip-based office)", detected)
		}
	case ".json":
		if !strings.HasPrefix(detected, "text/") && detected != "application/json" {
			return "", mimeMismatch(declaredExt, "text/* or application/json", detected)
		}
	case ".csv", ".txt":
		if !strings.HasPrefix(detected, "text/") {
			return "", mimeMismatch(declaredExt, "text/*", detected)
		}
	default:
		// AllowedExt would have rejected unknown extensions earlier.
		return detected, nil
	}
	return info.MIME, nil
}

func mimeMismatch(ext, expected, detected string) error {
	return fmt.Errorf("MIME mismatch: declared extension %q expects %s, content detected as %s",
		ext, expected, detected)
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
	SaveAttachment(rel string, content []byte, allowedExt map[string]bool) error
}

// Result holds what Store returns on success.
type Result struct {
	Path     string // vault-relative path
	Filename string // hashed filename
	Markdown string // ready-to-insert markdown
}

// remoteSetupHint is appended to source_path errors to guide users running
// gosidian on a remote server (SSH tunnel, separate host) toward the right
// pattern: source_path is resolved server-side, so a client-side path will
// never match the allow-list. A rejection here does NOT mean the filesystem
// is not shared — the path may simply be outside the allowed roots — so the
// hint teaches the whole channel hierarchy instead of one fallback. Kept as a
// const so tests can pin it.
const remoteSetupHint = "Hint: source_path is resolved on the SERVER filesystem and must sit inside the vault, " +
	"the bridge dir (GOSIDIAN_MCP_BRIDGE_DIR), or an allowed upload root (GOSIDIAN_MCP_ALLOWED_UPLOAD_ROOTS) — " +
	"a rejection means the path is outside those roots or not mounted in the container, not necessarily that the filesystem is unshared. " +
	"Cheapest alternatives in order: stage the file in the bridge dir and pass bridge_filename; " +
	"POST it over HTTP (memory_ingest transfer:\"http\" mints a single-use upload URL, or the /upload endpoint with your bearer); " +
	"base64 'data' as the last resort for small files."

// ValidateSourcePath checks that sourcePath is an absolute path pointing to an
// existing regular file inside one of the allowedRoots. Returns an error
// describing the violation, or nil when the path is safe to read.
//
// Errors that suggest a likely remote-deployment misuse (path not inside any
// allowed root, or path simply does not exist on the server) are augmented
// with remoteSetupHint to guide the caller toward the `data` parameter
// instead of guessing at GOSIDIAN_MCP_ALLOWED_UPLOAD_ROOTS configuration.
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
		return fmt.Errorf("source_path %q is not inside any allowed upload root. %s", sourcePath, remoteSetupHint)
	}
	fi, err := os.Stat(clean)
	if err != nil {
		// File missing despite path being inside an allowed root usually means
		// the caller is on a different host than the server (the path exists
		// client-side but not server-side). Surface the same hint.
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("source_path %q does not exist on the server. %s", sourcePath, remoteSetupHint)
		}
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
	// Magic-bytes verification: catch MIME spoof (declared png, content JS).
	if _, err := VerifyMIME(data, ext); err != nil {
		return nil, err
	}

	filename := HashFilename(data, ext)
	rel := RelPath(project, filename)

	if _, err := v.Rel(rel); err != nil {
		return nil, fmt.Errorf("invalid attachment path: %w", err)
	}
	if err := v.SaveAttachment(rel, data, ExtSet()); err != nil {
		return nil, fmt.Errorf("save attachment: %w", err)
	}

	return &Result{
		Path:     rel,
		Filename: filename,
		Markdown: MarkdownRef(rel, origFilename, isImage),
	}, nil
}
