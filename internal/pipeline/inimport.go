package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif" // the sizes of the images in.import stores
	_ "image/jpeg"
	_ "image/png"
	"path"
	"sort"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/frontmatter"
	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/pkgimport"
	"github.com/gosidian/gosidian/internal/vault"
)

// in.import (IMP-179): a markdown file of the IN folder becomes a note of
// the project, its tree under with.base kept under with.dest. The files it
// links are stored as attachments (named by their content: a file linked
// twice is stored once) and its links rewritten to them, as a package
// import does; the rest of the text and the frontmatter stay as written.
// Each image gets an image note (ADR-013) in <note>_img/, which points at
// the image (media) and at the note (article): llm.describe_media writes
// its description there, so the document itself is not changed.

// importedImage is an image in.import stored: where it came from and what
// its image note says about it.
type importedImage struct {
	src, inFile   string // its path under base, and in the IN folder
	sha           string
	width, height int
}

func inImport(_ context.Context, env *Env, with map[string]any) (any, error) {
	f, err := required(with, "file")
	if err != nil {
		return nil, err
	}
	file, err := env.inPath(f)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(path.Ext(file), ".md") {
		return nil, fmt.Errorf("%s is not a markdown file", file)
	}
	root := vault.INRoot(env.Project)
	src := strings.TrimPrefix(file, root+"/")
	base := inRelative(str(with, "base"))
	if base != "" {
		if !strings.HasPrefix(src, base+"/") {
			return nil, fmt.Errorf("%s is not under with.base %q", file, base)
		}
		src = strings.TrimPrefix(src, base+"/")
	}
	d, err := required(with, "dest")
	if err != nil {
		return nil, err
	}
	dest, err := env.projectPath(d)
	if err != nil {
		return nil, err
	}
	if dest == root || strings.HasPrefix(dest, root+"/") {
		return nil, fmt.Errorf("with.dest %q is inside the IN folder", d)
	}
	mediaNotes, listImages := true, true
	if b, ok := with["media_notes"].(bool); ok {
		mediaNotes = b
	}
	if b, ok := with["list_images"].(bool); ok {
		listImages = b
	}
	host := env.Engine.host
	data, err := host.ReadAttachment(env.Project, file)
	if err != nil {
		return nil, err
	}
	text := string(data)

	inFileOf := func(p string) string {
		if base == "" {
			return root + "/" + p
		}
		return root + "/" + base + "/" + p
	}
	notes := map[string]string{}    // a markdown file it links → its note
	attached := map[string]string{} // a file it links → its attachment
	images := map[string]importedImage{}
	skipped := []any{}
	read := []string{file}
	for _, l := range pkgimport.Links(text, src) {
		if _, ok := attached[l.Target]; ok {
			continue
		}
		if _, ok := notes[l.Target]; ok {
			continue
		}
		if !l.Image && strings.EqualFold(path.Ext(l.Target), ".md") {
			notes[l.Target] = dest + "/" + l.Target
			continue
		}
		in := inFileOf(l.Target)
		b, err := host.ReadAttachment(env.Project, in)
		if err != nil {
			skipped = append(skipped, map[string]any{"src": l.Target, "reason": err.Error()})
			continue
		}
		vp, err := env.StoreAttachment(path.Base(l.Target), b)
		if err != nil {
			skipped = append(skipped, map[string]any{"src": l.Target, "reason": err.Error()})
			continue
		}
		attached[l.Target] = vp
		read = append(read, in)
		if info, ok := attach.AllowedExt[strings.ToLower(path.Ext(vp))]; ok && info.IsImage {
			sum := sha256.Sum256(b)
			img := importedImage{src: l.Target, inFile: in, sha: hex.EncodeToString(sum[:])}
			if c, _, err := image.DecodeConfig(bytes.NewReader(b)); err == nil {
				img.width, img.height = c.Width, c.Height
			}
			images[vp] = img
		}
	}
	text, unresolved := pkgimport.RewriteMarkdown(text, src, notes, attached)
	if unresolved == nil {
		unresolved = []string{}
	}

	// The note keeps the document's frontmatter, with where it comes from:
	// in_file is its path in the IN folder, as text, since a file of the IN
	// folder is no note a wikilink could reach.
	notePath := dest + "/" + src
	fm := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw([]byte(text)))
	set := []frontmatter.Field{
		{Key: "pipeline", Value: "[[" + strings.TrimSuffix(env.Run.Ref, ".md") + "]]"},
		{Key: "pipeline_run", Value: env.Run.ID},
		{Key: "in_file", Value: file},
	}
	if _, has := fm["tags"]; !has {
		set = append(set, frontmatter.Field{Key: "tags", Value: []string{env.Project}})
	}
	content, err := frontmatter.SetKeys([]byte(text), set, nil)
	if err != nil {
		return nil, fmt.Errorf("the frontmatter of %s: %w", file, err)
	}
	res, err := host.WriteNote(env.Project, notePath, content, WriteUpsert, Actor(env.Run.Ref))
	if err != nil {
		return nil, err
	}

	out := map[string]any{"path": res.Path, "created": res.Created, "attachments": len(attached),
		"image_count": 0, "removed": []string{}, "unresolved": unresolved, "skipped": skipped}
	if listImages {
		out["images"] = []any{}
	}
	if mediaNotes {
		made, removed, err := env.syncMediaNotes(notePath, string(content), images)
		if err != nil {
			return nil, err
		}
		out["image_count"], out["removed"] = len(made), removed
		if listImages {
			out["images"] = made
		}
	}

	// The files it read are done, with the run; the document's status is
	// the run's, as for any file a run takes.
	if env.Engine.in != nil && len(read) > 1 {
		if err := env.Engine.in.SetStatus(env.Project, read[1:], infolder.StatusDone, env.Run.ID); err != nil {
			env.Log.Warn("in.import: marking the files it read", "err", err)
		}
	}
	return out, nil
}

// describedKeys are what llm.describe_media writes in an image note: an
// image that changed loses them. Its title is its label: until it is
// described an image note has none, and goes by its file's name, so that the
// thousands of image notes of a document do not outrank the document in a
// search with its title.
var describedKeys = []string{"title", "label", "model", "prompt_version", "described", "describe_error"}

// syncMediaNotes writes the image notes of the article from its content:
// one per image it stored, in the order the images first appear, named
// after the image's file. A note whose image did not change keeps its
// description; the image notes of the article that no image needs any more
// go (into the trash).
func (env *Env) syncMediaNotes(article, content string, images map[string]importedImage) ([]any, []string, error) {
	host := env.Engine.host
	lines := strings.Split(parser.BodyAfterFrontmatter([]byte(content)), "\n")
	type entry struct {
		ref   imageRef
		att   string
		count int
	}
	var order []*entry
	byAtt := map[string]*entry{}
	for _, r := range scanImages(lines) {
		att := strings.TrimPrefix(r.URL, attach.FileURL(""))
		if _, ok := images[att]; !ok {
			continue
		}
		if e := byAtt[att]; e != nil {
			e.count++
			continue
		}
		e := &entry{ref: r, att: att, count: 1}
		byAtt[att] = e
		order = append(order, e)
	}

	folder := mediaFolder(article)
	there := map[string]bool{}    // the notes in the folder
	mine := map[string]NoteData{} // those that are image notes of the article
	lister, _ := host.(Folders)
	if lister != nil {
		paths, err := lister.NotesIn(env.Project, folder)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range paths {
			there[p] = true
			if n, err := host.ReadNote(env.Project, p); err == nil && isMediaOf(n.Frontmatter, article) {
				mine[p] = n
			}
		}
	}

	actor := Actor(env.Run.Ref)
	used := map[string]bool{}
	made := []any{}
	for i, e := range order {
		img := images[e.att]
		stem := strings.TrimSuffix(path.Base(img.src), path.Ext(img.src))
		p := folder + "/" + stem + ".md"
		for n := 2; used[p] || there[p] && mine[p].Path == ""; n++ {
			p = fmt.Sprintf("%s/%s-%d.md", folder, stem, n)
		}
		used[p] = true
		set := []frontmatter.Field{
			{Key: "type", Value: "image"},
			{Key: "media", Value: e.att},
			{Key: "article", Value: "[[" + strings.TrimSuffix(article, ".md") + "]]"},
			{Key: "position", Value: i + 1},
			{Key: "occurrences", Value: e.count},
			{Key: "inline", Value: e.ref.Inline},
			{Key: "section", Value: sectionAt(lines, e.ref.Line)},
			{Key: "sha256", Value: img.sha},
			{Key: "in_file", Value: img.inFile},
			{Key: "pipeline", Value: "[[" + strings.TrimSuffix(env.Run.Ref, ".md") + "]]"},
			{Key: "pipeline_run", Value: env.Run.ID},
			{Key: "tags", Value: []string{env.Project, "type:image"}},
		}
		var unset []string
		if alt := strings.TrimSpace(e.ref.Alt); alt != "" {
			set = append(set, frontmatter.Field{Key: "alt_original", Value: alt})
		} else {
			unset = append(unset, "alt_original")
		}
		if img.width > 0 {
			set = append(set, frontmatter.Field{Key: "width", Value: img.width}, frontmatter.Field{Key: "height", Value: img.height})
		}
		var raw []byte
		body := ""
		if n, ok := mine[p]; ok {
			raw, body = n.Raw, n.Body
			if m, _ := n.Frontmatter["media"].(string); m != e.att {
				body = ""
				unset = append(unset, describedKeys...)
			} else if _, described := n.Frontmatter["label"]; !described {
				unset = append(unset, "title")
			}
		}
		c, err := frontmatter.SetKeys(raw, set, unset)
		if err != nil {
			return nil, nil, fmt.Errorf("the image note %s: %w", p, err)
		}
		if _, err := host.WriteNote(env.Project, p, withBody(c, body), WriteUpsert, actor); err != nil {
			return nil, nil, err
		}
		made = append(made, map[string]any{"note": p, "attachment": e.att, "src": img.src, "position": i + 1, "inline": e.ref.Inline})
	}

	removed := []string{}
	var gone []string
	for p := range mine {
		if !used[p] {
			gone = append(gone, p)
		}
	}
	sort.Strings(gone)
	for _, p := range gone {
		if err := lister.DeleteNote(env.Project, p, actor); err != nil {
			return nil, nil, err
		}
		removed = append(removed, p)
	}
	return made, removed, nil
}
