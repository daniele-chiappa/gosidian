package mcp

// memory_ingest as: "package" (IMP-116): a folder or a .zip imported in one
// call, under a destination folder. The package is read and planned first
// (pkgimport); the plan is checked as a whole (access, sizes, notes already
// there) and written all or not at all, as one unit of the write limit.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/pkgimport"
)

// packageIntent is what a package import was asked to do.
type packageIntent struct {
	Project     string
	Dest        string
	DryRun      bool
	Overwrite   bool
	AllowShrink bool
}

func packageIntentOf(project string, req mcp.CallToolRequest) packageIntent {
	return packageIntent{
		Project:     project,
		Dest:        strings.TrimSpace(req.GetString("dest", "")),
		DryRun:      req.GetBool("dry_run", false),
		Overwrite:   req.GetBool("overwrite", false),
		AllowShrink: req.GetBool("allow_shrink", false),
	}
}

// ingestPackage reads the package a memory_ingest call names (a folder or a
// .zip in the bridge dir, a server path, or base64 zip data) and imports it.
func (s *Server) ingestPackage(ctx context.Context, project string, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	in := packageIntentOf(project, req)
	if _, errRes := s.packageDest(ctx, in); errRes != nil {
		return errRes, nil
	}
	lim := pkgimport.Limits{MaxFiles: s.packageMaxFiles, MaxBytes: s.packageMaxBytes}
	bridge := strings.TrimSpace(req.GetString("bridge_filename", ""))
	source := strings.TrimSpace(req.GetString("source_path", ""))
	data := req.GetString("data", "")
	var (
		entries []pkgimport.Entry
		skipped []pkgimport.Skipped
		consume string
		err     error
	)
	switch {
	case bridge != "":
		if consume, err = s.bridgePath(bridge); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		entries, skipped, err = readPackagePath(consume, lim)
	case source != "":
		real, srcErr := s.checkSource(ctx, source)
		if srcErr != nil {
			return mcp.NewToolResultError(srcErr.Error()), nil
		}
		entries, skipped, err = readPackagePath(real, lim)
	case data != "":
		raw, decErr := base64.StdEncoding.DecodeString(data)
		if decErr != nil {
			return mcp.NewToolResultError("invalid base64: " + decErr.Error()), nil
		}
		entries, skipped, err = pkgimport.ReadZip(raw, lim)
	default:
		return mcp.NewToolResultError("a package needs a source: bridge_filename (a folder or .zip staged in the bridge dir), source_path (a folder or .zip on the server), data (a base64 .zip), or transfer:\"http\" to upload a .zip"), nil
	}
	if err != nil {
		return mcp.NewToolResultError("package: " + err.Error()), nil
	}
	res, errOut := s.importPackage(ctx, in, entries, skipped)
	if errOut == nil && res != nil && !res.IsError && !in.DryRun && consume != "" {
		_ = os.RemoveAll(consume) // consume the staged package (best-effort)
	}
	return res, errOut
}

// readPackagePath reads a folder, or a .zip file.
func readPackagePath(p string, lim pkgimport.Limits) ([]pkgimport.Entry, []pkgimport.Skipped, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("%q not found", filepath.Base(p))
		}
		return nil, nil, err
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return nil, nil, fmt.Errorf("%q is a symbolic link, not followed", filepath.Base(p))
	case fi.IsDir():
		return pkgimport.ReadDir(p, lim)
	case strings.EqualFold(filepath.Ext(p), ".zip"):
		if fi.Size() > lim.MaxBytes {
			return nil, nil, fmt.Errorf("%w: the .zip is larger than %d bytes", pkgimport.ErrTooLarge, lim.MaxBytes)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		return pkgimport.ReadZip(raw, lim)
	}
	return nil, nil, fmt.Errorf("%q is neither a folder nor a .zip", filepath.Base(p))
}

// packageDest checks the destination folder: inside the project, and
// writable by the caller.
func (s *Server) packageDest(ctx context.Context, in packageIntent) (*auth.Token, *mcp.CallToolResult) {
	dest := strings.Trim(path.Clean("/"+in.Dest), "/")
	if in.Dest == "" || dest == "" {
		return nil, mcp.NewToolResultErrorf("dest is required: the vault folder the package goes into, such as %s/docs/guide", in.Project)
	}
	if dest != in.Project && !strings.HasPrefix(dest, in.Project+"/") {
		return nil, mcp.NewToolResultErrorf("dest %q must be inside the project %s", dest, in.Project)
	}
	return s.authorizeWrite(ctx, dest+"/package.md")
}

// packageNoteResult is a note of a package in the result.
type packageNoteResult struct {
	Path             string   `json:"path"`
	Src              string   `json:"src"`
	Exists           bool     `json:"exists,omitempty"`
	Created          bool     `json:"created,omitempty"`
	FrontmatterAdded bool     `json:"frontmatter_added,omitempty"`
	Unresolved       []string `json:"unresolved,omitempty"`
}

// errCreatedMeanwhile is a note of a package that appeared between the
// check and the write, without overwrite.
var errCreatedMeanwhile = errors.New("created meanwhile")

// importPackage checks a read package and writes it, all or nothing: every
// note is checked first (path, access, size, a note already there without
// overwrite), and a write that fails midway takes back the ones done. It
// takes one place in the write rate, none when the ingestion took it
// already (writeCharge: the upload of a ticket).
func (s *Server) importPackage(ctx context.Context, in packageIntent, entries []pkgimport.Entry, skipped []pkgimport.Skipped) (*mcp.CallToolResult, error) {
	tok, errRes := s.packageDest(ctx, in)
	if errRes != nil {
		return errRes, nil
	}
	dest := strings.Trim(path.Clean("/"+in.Dest), "/")
	plan, err := pkgimport.Build(entries, skipped, pkgimport.Options{
		Project: in.Project,
		Dest:    dest,
		IsNote: func(name string) bool {
			switch strings.ToLower(path.Ext(name)) {
			case ".md":
				return true
			case ".html":
				return s.vault.HTMLNotesEnabled()
			}
			return false
		},
		AttachmentPath: func(name string, data []byte) (string, error) {
			ext := strings.ToLower(path.Ext(name))
			if ext == ".html" {
				return "", errors.New("html notes are disabled on this instance")
			}
			if _, _, err := attach.ValidateExt(ext); err != nil {
				return "", errors.New("not a note, and not an accepted attachment type")
			}
			if len(data) > attach.MaxBytes {
				return "", errors.New("attachment too large (max 10 MiB)")
			}
			if _, err := attach.VerifyMIME(data, ext); err != nil {
				return "", err
			}
			return attach.RelPath(in.Project, attach.HashFilename(data, ext)), nil
		},
	})
	if err != nil {
		return mcp.NewToolResultError("package: " + err.Error()), nil
	}
	if len(plan.Notes) == 0 && len(plan.Attachments) == 0 {
		return mcp.NewToolResultError("the package holds no note and no attachment to import"), nil
	}

	// Check everything before writing anything.
	notes := make([]packageNoteResult, len(plan.Notes))
	var problems, conflicts []string
	for i, n := range plan.Notes {
		notes[i] = packageNoteResult{Path: n.Path, Src: n.Src, FrontmatterAdded: n.FrontmatterAdded, Unresolved: n.Unresolved}
		rel, err := s.vault.Rel(n.Path)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: %v", n.Src, err))
			continue
		case !s.vault.IsNoteFile(rel) || !tok.AllowsPath(rel):
			problems = append(problems, fmt.Sprintf("%s: %s is not a note path this token may write", n.Src, rel))
			continue
		case s.maxNoteBytes > 0 && int64(len(n.Data)) > s.maxNoteBytes:
			problems = append(problems, fmt.Sprintf("%s: %d bytes, past the limit of %d for a note", n.Src, len(n.Data), s.maxNoteBytes))
		}
		if prev, err := s.vault.Load(rel); err == nil {
			notes[i].Exists = true
			switch {
			case !in.Overwrite:
				conflicts = append(conflicts, rel)
			// The shrink guard of memory_update: an almost empty entry of
			// the package emptied a big note (BUG-113, S3-5).
			case !in.AllowShrink && s.shrinkRefusal(rel, prev.Size, len(n.Data)) != "":
				problems = append(problems, fmt.Sprintf("%s would shrink %s from %d to %d bytes (pass allow_shrink:true if meant)", n.Src, rel, prev.Size, len(n.Data)))
			}
		}
	}
	unresolved := 0
	for _, n := range notes {
		unresolved += len(n.Unresolved)
	}
	out := map[string]any{
		"dest":  dest,
		"notes": notes,
	}
	if len(plan.Attachments) > 0 {
		paths := make([]string, len(plan.Attachments))
		for i, a := range plan.Attachments {
			paths[i] = a.Path
		}
		out["attachments"] = paths
	}
	if len(plan.Skipped) > 0 {
		out["skipped"] = plan.Skipped
	}
	if unresolved > 0 {
		out["unresolved"] = unresolved
	}
	if len(problems) > 0 {
		return mcp.NewToolResultErrorf("package not imported, nothing written: %s", strings.Join(firstN(problems, 10), "; ")), nil
	}
	if in.DryRun {
		out["dry_run"] = true
		if len(conflicts) > 0 {
			out["would_need_overwrite"] = conflicts
		}
		return mcp.NewToolResultJSON(out)
	}
	if len(conflicts) > 0 {
		return mcp.NewToolResultErrorf("package not imported, nothing written: %d note(s) already exist (%s); pass overwrite:true to replace them, or dry_run:true to see the plan",
			len(conflicts), strings.Join(firstN(conflicts, 10), ", ")), nil
	}
	if msg, _ := s.writeLimitViolation(ctx, tok, 0); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	// Attachments first: they are content-addressed, so one already there
	// is the same file and stays. The new ones count against the upload
	// quota, all together, before any is written (IMP-034).
	var fresh []int
	var freshBytes int64
	for i, a := range plan.Attachments {
		if abs, err := s.vault.Abs(a.Path); err == nil {
			if _, err := os.Stat(abs); err == nil {
				continue
			}
		}
		fresh = append(fresh, i)
		freshBytes += int64(len(a.Data))
	}
	if _, refusal := s.reserveUpload(tok, freshBytes); refusal != nil {
		return mcp.NewToolResultError("package not imported, nothing written: " + refusal.Error()), nil
	}
	for _, i := range fresh {
		a := plan.Attachments[i]
		if err := s.vault.SaveAttachment(a.Path, a.Data, attach.ExtSet()); err != nil {
			// The bytes not written go back to the quota.
			s.uploadQuota.Refund(uploadKey(tok), freshBytes)
			return mcp.NewToolResultErrorFromErr("package not imported: attachment "+a.Src, err), nil
		}
		freshBytes -= int64(len(a.Data))
		s.auditWrite(ctx, audit.ActionUploadAttachment, a.Path, "", int64(len(a.Data)))
	}
	type done struct {
		rel     string
		created bool
		prev    []byte
	}
	var written []done
	rollback := func() {
		for i := len(written) - 1; i >= 0; i-- {
			d := written[i]
			_ = s.underLock(d.rel, func() error {
				if d.created {
					_ = s.vault.Delete(d.rel)
					_ = s.index.Delete(d.rel)
					return nil
				}
				return s.writeAndIndex(d.rel, d.prev)
			})
		}
	}
	for i, n := range plan.Notes {
		rel, _ := s.vault.Rel(n.Path)
		d := done{rel: rel, created: true}
		err := s.underLock(rel, func() error {
			if prev, err := s.vault.Load(rel); err == nil {
				if !in.Overwrite {
					return errCreatedMeanwhile
				}
				d.created, d.prev = false, prev.Content
			}
			return s.writeAndIndex(rel, n.Data)
		})
		if errors.Is(err, errCreatedMeanwhile) {
			rollback()
			return mcp.NewToolResultErrorf("package not imported: %s was created meanwhile; nothing is left written", rel), nil
		}
		if err != nil {
			rollback()
			return mcp.NewToolResultErrorFromErr("package not imported, the notes written are taken back: "+rel, err), nil
		}
		written = append(written, d)
		notes[i].Created, notes[i].Exists = d.created, false
	}
	for i, d := range written {
		action, change := audit.ActionCreate, "create"
		if !d.created {
			action, change = audit.ActionUpdate, "update"
		}
		s.auditWrite(ctx, action, d.rel, "", int64(len(plan.Notes[i].Data)))
		s.noteSchemaProblems(ctx, d.rel, plan.Notes[i].Data)
		// One note event each, with the new ETag, as writeNote sends: an
		// editor open on a replaced note learns it changed (S3-5). The tree
		// event goes once, below.
		etag := ""
		if fresh, err := s.vault.Load(d.rel); err == nil {
			etag = fresh.ETag()
		}
		s.publishNoteChange(change, d.rel, etag, false)
	}
	s.auditWrite(ctx, audit.ActionIngestPackage, dest, "", int64(len(written)))
	s.publishTreeChange("create", dest, map[string]any{"package": len(written)})
	out["kind"] = "package"
	if unresolved > 0 {
		out["hint"] = "some relative links name files that are not in the package: they were left as written (see unresolved on each note)"
	}
	return mcp.NewToolResultJSON(out)
}

func firstN(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(xs[:n:n], fmt.Sprintf("… %d more", len(xs)-n))
}
