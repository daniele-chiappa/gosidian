// Package chatbot serves the notes `type: chat` (IMP-191): a ```chat block
// of YAML says which pipeline answers, which scopes the bot opens, who may
// chat and where the conversations go. The server hands it to the chat
// plugin (its tool chat_configure) with a run token of the pipeline and the
// secrets it needs, signs the tokens of gosidian's users for the bot, and
// moves the old conversations to the trash. The plugin serves the widget
// and the API on the internet, and starts the pipeline on each message;
// docs/chat.md is the contract between the two.
package chatbot

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/gosidian/gosidian/internal/parser"
)

// Lang is the fence of the block; NoteType the type of a bot's note.
const (
	Lang     = "chat"
	NoteType = "chat"
)

// DefaultPlugin is the plugin a block that names none uses.
const DefaultPlugin = "chat"

// Spec is a bot as its note declares it.
type Spec struct {
	// Name is the bot's name in the plugin and in the widget's address;
	// default the note's name, made a slug.
	Name string `json:"name"`
	// Plugin is the chat plugin that serves it.
	Plugin string `json:"plugin"`
	// Pipeline is the pipeline that answers, a note of the project
	// (relative to it), which starts on chat.
	Pipeline string `json:"pipeline"`
	Title    string `json:"title,omitempty"`
	Welcome  string `json:"welcome,omitempty"`
	// Scopes are those the bot opens: the person chatting chooses among
	// them, and the plugin hands the pipeline their filters.
	Scopes []Scope `json:"scopes,omitempty"`
	// Folders makes scopes of the folders under one, at configure time,
	// after Scopes: the person chatting picks one per level.
	Folders       *Folders       `json:"folders,omitempty"`
	Access        Access         `json:"access"`
	Sources       Sources        `json:"sources"`
	Conversations Conversations  `json:"conversations"`
	Limits        map[string]int `json:"limits,omitempty"`
}

// Scope is a part of the archive the bot may answer from. A scope of a
// folder has its subfolders' as Children.
type Scope struct {
	Name     string         `json:"name"`
	Label    string         `json:"label,omitempty"`
	Filter   map[string]any `json:"filter,omitempty"`
	Children []Scope        `json:"children,omitempty"`
}

// Folders are the scopes of a folder's tree: a scope for each folder under
// Path, down Levels levels, whose filter keeps the chunks whose meta Key
// holds that folder or one under it. The pipeline that cuts the documents
// writes Key, the document's folder as the chunks' doc names it.
type Folders struct {
	Path   string   `json:"path"`
	Levels int      `json:"levels"`
	Key    string   `json:"key"`
	Labels []string `json:"labels,omitempty"`
}

// Access says who may chat. Keys and Signed name secrets of gosidian
// (gosidian secret set), never their values.
type Access struct {
	// Users: the accounts of gosidian that read the bot's project, with a
	// token gosidian signs (the note's own chat among them). Default true.
	Users bool `json:"users"`
	// Keys are public keys of web sites, each allowed from its origins.
	Keys []Key `json:"keys,omitempty"`
	// Signed is the secret a site signs its users' tokens with.
	Signed string `json:"signed,omitempty"`
	// Open: anyone who reaches the plugin, without a key or a token.
	Open bool `json:"open"`
}

// Key is a web site's key: its value is the secret Secret.
type Key struct {
	Name    string   `json:"name"`
	Secret  string   `json:"secret"`
	Origins []string `json:"origins,omitempty"`
}

// Sources says how an answer links its sources: BaseURL, when set, is
// the public address of the documents; empty, the notes in gosidian.
type Sources struct {
	BaseURL string `json:"base_url,omitempty"`
}

// Conversations says where the pipeline writes the conversations, and how
// long they are kept (0: always).
type Conversations struct {
	Folder string        `json:"folder,omitempty"`
	Keep   time.Duration `json:"keep,omitempty"`
}

// Problem is what keeps a block from reading as a bot; Line counts from
// the block's first line.
type Problem struct {
	Line int    `json:"line,omitempty"`
	Msg  string `json:"message"`
}

func (p Problem) String() string {
	if p.Line > 0 {
		return "line " + strconv.Itoa(p.Line) + ": " + p.Msg
	}
	return p.Msg
}

// Block is a ```chat block of a note, at its line.
type Block struct {
	Line int
	Src  string
}

// Find returns the chat blocks of a note's body.
func Find(body []byte) []Block {
	fenced := parser.FindFenced(body, Lang)
	out := make([]Block, 0, len(fenced))
	for _, b := range fenced {
		out = append(out, Block{Line: strings.Count(string(body[:b.Start]), "\n") + 1, Src: b.Spec})
	}
	return out
}

var (
	botName    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
	pluginName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	secretName = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	metaKey    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	lineRe     = regexp.MustCompile(`line (\d+)`)
	fieldRe    = regexp.MustCompile(`line (\d+): field (\S+) not found in type \S+`)
	nonSlug    = regexp.MustCompile(`[^a-z0-9]+`)
)

// Limits of a block.
const (
	maxScopes = 100
	maxKeys   = 50
	maxText   = 2000
	maxLevels = 3
	maxLabel  = 100
)

// DefaultFolderKey is the key of the chunks' meta a scope of a folder
// filters on, when the block names none.
const DefaultFolderKey = "folder"

// limitNames are the limits a block may set, handed to the plugin.
var limitNames = map[string]bool{"per_minute": true, "per_session": true, "max_tokens": true, "max_message": true}

type rawSpec struct {
	Name     string `yaml:"name"`
	Plugin   string `yaml:"plugin"`
	Pipeline string `yaml:"pipeline"`
	Title    string `yaml:"title"`
	Welcome  string `yaml:"welcome"`
	Scopes   []struct {
		Name   string         `yaml:"name"`
		Label  string         `yaml:"label"`
		Filter map[string]any `yaml:"filter"`
	} `yaml:"scopes"`
	Folders *struct {
		Path   string   `yaml:"path"`
		Levels int      `yaml:"levels"`
		Key    string   `yaml:"key"`
		Labels []string `yaml:"labels"`
	} `yaml:"folders"`
	Access *struct {
		Users *bool `yaml:"users"`
		Keys  []struct {
			Name    string   `yaml:"name"`
			Secret  string   `yaml:"secret"`
			Origins []string `yaml:"origins"`
		} `yaml:"keys"`
		Signed *struct {
			Secret string `yaml:"secret"`
		} `yaml:"signed"`
		Open bool `yaml:"open"`
	} `yaml:"access"`
	Sources struct {
		BaseURL string `yaml:"base_url"`
	} `yaml:"sources"`
	Conversations struct {
		Folder string `yaml:"folder"`
		Keep   string `yaml:"keep"`
	} `yaml:"conversations"`
	Limits map[string]int `yaml:"limits"`
}

// Parse reads a block of the note at notePath. A block that does not read
// still gives what it could, with its problems.
func Parse(src, notePath string) (Spec, []Problem) {
	var r rawSpec
	dec := yaml.NewDecoder(strings.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil && err.Error() != "EOF" {
		// A block that does not read is still its note's bot, by name.
		s := Spec{Name: DefaultName(notePath), Plugin: DefaultPlugin}
		msg := err.Error()
		if m := fieldRe.FindStringSubmatch(msg); m != nil {
			line, _ := strconv.Atoi(m[1])
			return s, []Problem{{Line: line, Msg: fmt.Sprintf("%s is no field of a bot: name, plugin, pipeline, title, welcome, scopes, folders, access, sources, conversations, limits", m[2])}}
		}
		p := Problem{Msg: "the YAML does not read: " + strings.TrimPrefix(msg, "yaml: ")}
		if m := lineRe.FindStringSubmatch(msg); m != nil {
			p.Line, _ = strconv.Atoi(m[1])
		}
		return s, []Problem{p}
	}
	var problems []Problem
	bad := func(format string, args ...any) {
		problems = append(problems, Problem{Msg: fmt.Sprintf(format, args...)})
	}
	s := Spec{Name: strings.TrimSpace(r.Name), Plugin: strings.TrimSpace(r.Plugin), Pipeline: strings.Trim(strings.TrimSpace(r.Pipeline), "/"),
		Title: strings.TrimSpace(r.Title), Welcome: strings.TrimSpace(r.Welcome), Limits: r.Limits}
	if s.Name == "" {
		s.Name = DefaultName(notePath)
	}
	if !botName.MatchString(s.Name) {
		bad("name %q: lower-case letters, digits and -, starting with a letter, at most 48", s.Name)
	}
	if s.Plugin == "" {
		s.Plugin = DefaultPlugin
	}
	if !pluginName.MatchString(s.Plugin) {
		bad("plugin %q is no plugin's name", s.Plugin)
	}
	switch {
	case s.Pipeline == "":
		bad("pipeline names the pipeline that answers, a note of the project that starts on chat")
	case strings.Contains(s.Pipeline, ".."):
		bad("pipeline %q: a note of the project", s.Pipeline)
	case !strings.HasSuffix(s.Pipeline, ".md"):
		s.Pipeline += ".md"
	}
	if len(s.Title) > maxText || len(s.Welcome) > maxText {
		bad("title and welcome: at most %d characters", maxText)
	}
	if len(r.Scopes) > maxScopes {
		bad("scopes: at most %d", maxScopes)
	}
	seen := map[string]bool{}
	for i, sc := range r.Scopes {
		name := strings.TrimSpace(sc.Name)
		switch {
		case !botName.MatchString(name):
			bad("scopes[%d]: name %q, lower-case letters, digits and -", i, name)
		case seen[name]:
			bad("scopes: %q twice", name)
		}
		seen[name] = true
		label := strings.TrimSpace(sc.Label)
		if label == "" {
			label = name
		}
		s.Scopes = append(s.Scopes, Scope{Name: name, Label: label, Filter: sc.Filter})
	}
	if f := r.Folders; f != nil {
		fs := Folders{Path: strings.Trim(strings.TrimSpace(f.Path), "/"), Levels: f.Levels, Key: strings.TrimSpace(f.Key)}
		switch {
		case fs.Path == "":
			bad("folders.path names a folder of the project, whose folders become the scopes")
		case strings.Contains(fs.Path, "..") || path.Clean(fs.Path) != fs.Path:
			bad("folders.path %q: a folder of the project", fs.Path)
		}
		if fs.Levels == 0 {
			fs.Levels = 1
		}
		if fs.Levels < 1 || fs.Levels > maxLevels {
			bad("folders.levels: 1 to %d, the levels of folders the chat offers", maxLevels)
		}
		if fs.Key == "" {
			fs.Key = DefaultFolderKey
		}
		if !metaKey.MatchString(fs.Key) {
			bad("folders.key %q: a key of the chunks' meta, letters, digits, _ and -", fs.Key)
		}
		if len(f.Labels) > fs.Levels {
			bad("folders.labels: one per level, at most %d", fs.Levels)
		}
		for _, l := range f.Labels {
			l = strings.TrimSpace(l)
			if len(l) > maxLabel {
				bad("folders.labels: at most %d characters each", maxLabel)
			}
			fs.Labels = append(fs.Labels, l)
		}
		s.Folders = &fs
	}
	s.Access.Users = true
	if a := r.Access; a != nil {
		if a.Users != nil {
			s.Access.Users = *a.Users
		}
		s.Access.Open = a.Open
		if len(a.Keys) > maxKeys {
			bad("access.keys: at most %d", maxKeys)
		}
		names := map[string]bool{}
		for i, k := range a.Keys {
			key := Key{Name: strings.TrimSpace(k.Name), Secret: strings.TrimSpace(k.Secret)}
			switch {
			case !botName.MatchString(key.Name):
				bad("access.keys[%d]: name %q, lower-case letters, digits and -", i, key.Name)
			case names[key.Name]:
				bad("access.keys: %q twice", key.Name)
			}
			names[key.Name] = true
			if !secretName.MatchString(key.Secret) {
				bad("access.keys[%d] (%s): secret names a secret of gosidian (gosidian secret set <name>), not its value", i, key.Name)
			}
			if len(k.Origins) == 0 {
				bad("access.keys[%d] (%s): origins lists the sites the key works from (https://www.example.com)", i, key.Name)
			}
			for _, o := range k.Origins {
				if err := checkOrigin(o); err != nil {
					bad("access.keys[%d] (%s): %v", i, key.Name, err)
					continue
				}
				key.Origins = append(key.Origins, strings.TrimRight(strings.TrimSpace(o), "/"))
			}
			s.Access.Keys = append(s.Access.Keys, key)
		}
		if a.Signed != nil {
			s.Access.Signed = strings.TrimSpace(a.Signed.Secret)
			if !secretName.MatchString(s.Access.Signed) {
				bad("access.signed.secret names a secret of gosidian (gosidian secret set <name>), not its value")
			}
		}
	}
	if !s.Access.Users && !s.Access.Open && len(s.Access.Keys) == 0 && s.Access.Signed == "" {
		bad("access: nobody may chat; users, keys, signed or open")
	}
	if u := strings.TrimSpace(r.Sources.BaseURL); u != "" {
		pu, err := url.Parse(u)
		if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
			bad("sources.base_url %q is not an http(s) address", u)
		}
		s.Sources.BaseURL = u
	}
	if f := strings.Trim(strings.TrimSpace(r.Conversations.Folder), "/"); f != "" {
		if strings.Contains(f, "..") || path.Clean(f) != f {
			bad("conversations.folder %q: a folder of the project", f)
		}
		s.Conversations.Folder = f
	}
	if k := strings.TrimSpace(r.Conversations.Keep); k != "" {
		d, err := ParseKeep(k)
		if err != nil {
			bad("conversations.keep: %v", err)
		}
		if d > 0 && s.Conversations.Folder == "" {
			bad("conversations.keep needs conversations.folder: the folder whose old conversations go")
		}
		s.Conversations.Keep = d
	}
	for k, v := range s.Limits {
		switch {
		case !limitNames[k]:
			bad("limits.%s: per_minute, per_session, max_tokens or max_message", k)
		case v < 0:
			bad("limits.%s: 0 or more", k)
		}
	}
	return s, problems
}

// DefaultName is the name of a bot whose block names none: its note's
// name, made a slug.
func DefaultName(notePath string) string {
	n := strings.ToLower(strings.TrimSuffix(path.Base(notePath), ".md"))
	n = strings.Trim(nonSlug.ReplaceAllString(n, "-"), "-")
	if n == "" || n[0] < 'a' || n[0] > 'z' {
		n = "bot-" + n
	}
	if len(n) > 48 {
		n = strings.TrimRight(n[:48], "-")
	}
	return n
}

// ParseKeep reads how long conversations are kept: 90d, 12h, 0.
func ParseKeep(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "0" {
		return 0, nil
	}
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%q: days (90d), or a duration (12h)", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q: days (90d), or a duration (12h)", s)
	}
	return d, nil
}

// checkOrigin checks a site's origin: scheme and host, no path.
func checkOrigin(o string) error {
	o = strings.TrimRight(strings.TrimSpace(o), "/")
	u, err := url.Parse(o)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return fmt.Errorf("origin %q: scheme and host, https://www.example.com", o)
	}
	return nil
}

// FromNote reads the bot of a note: ok is false when the note has no
// ```chat block; the problems say why it does not read, the note's
// problems too (more blocks, a type other than chat).
func FromNote(notePath string, body []byte) (Spec, []Problem, bool) {
	blocks := Find(body)
	switch len(blocks) {
	case 0:
		return Spec{}, nil, false
	case 1:
	default:
		return Spec{}, []Problem{{Msg: fmt.Sprintf("the note has %d ```chat blocks, a bot has one", len(blocks))}}, true
	}
	s, problems := Parse(blocks[0].Src, notePath)
	for i := range problems {
		if problems[i].Line > 0 {
			problems[i].Line += blocks[0].Line
		}
	}
	return s, problems, true
}
