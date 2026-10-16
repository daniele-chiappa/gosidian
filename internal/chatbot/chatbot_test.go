package chatbot

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/plugins"
)

const bot = "---\ntitle: Assistente\ntype: chat\n---\n\n# Assistente\n\n```chat\n" + `pipeline: pipelines/chat
title: Assistente della documentazione
scopes:
  - {name: magazzino, label: Magazzino, filter: {meta: {area: magazzino}}}
  - {name: vendite}
access:
  keys: [{name: sito, secret: chat-sito, origins: ["https://www.example.com"]}]
  signed: {secret: chat-firma}
conversations: {folder: chat/assistente, keep: 30d}
limits: {per_minute: 10}
` + "```\n"

func TestParse(t *testing.T) {
	s, problems, ok := FromNote("p/bots/Assistente Doc.md", []byte(bot))
	if !ok || len(problems) > 0 {
		t.Fatalf("%v %v", ok, problems)
	}
	if s.Name != "assistente-doc" || s.Plugin != "chat" || s.Pipeline != "pipelines/chat.md" || !s.Access.Users ||
		len(s.Scopes) != 2 || s.Scopes[1].Label != "vendite" || s.Conversations.Keep != 30*24*time.Hour || s.Limits["per_minute"] != 10 {
		t.Errorf("%+v", s)
	}
	for src, want := range map[string]string{
		"pipeline: x\nname: Bad Name":         "name",
		"title: x":                            "pipeline names",
		"pipeline: ../x":                      "a note of the project",
		"pipeline: x\naccess: {users: false}": "nobody may chat",
		"pipeline: x\naccess: {keys: [{name: s, secret: chat-s}]}":                                  "origins lists",
		"pipeline: x\naccess: {keys: [{name: s, secret: 'Ab12+/=', origins: [https://a.example]}]}": "names a secret",
		"pipeline: x\naccess: {keys: [{name: s, secret: k, origins: [https://a.example/page]}]}":    "scheme and host",
		"pipeline: x\nconversations: {keep: 30d}":                                                   "needs conversations.folder",
		"pipeline: x\nconversations: {folder: c, keep: soon}":                                       "days",
		"pipeline: x\nlimits: {burst: 3}":                                                           "per_minute",
		"pipeline: x\nscopes: [{name: a}, {name: a}]":                                               "twice",
		"pipeline: x\nfolders: {levels: 2}":                                                         "folders.path names",
		"pipeline: x\nfolders: {path: ../docs}":                                                     "folders.path",
		"pipeline: x\nfolders: {path: docs, levels: 4}":                                             "folders.levels",
		"pipeline: x\nfolders: {path: docs, key: a.b}":                                              "folders.key",
		"pipeline: x\nfolders: {path: docs, labels: [Area, Topic]}":                                 "folders.labels",
		"pipeline: x\nunknown: 1":                                                                   "unknown is no field of a bot",
		"pipeline: [x":                                                                              "does not read",
	} {
		_, problems := Parse(src, "p/b.md")
		if len(problems) == 0 || !strings.Contains(problems[0].Msg, want) {
			t.Errorf("%q: %v, want %q", src, problems, want)
		}
	}
	_, problems = Parse("pipeline: x\n\nunknown: 1", "p/b.md")
	if problems[0].Line != 3 {
		t.Errorf("the line of a problem: %+v", problems[0])
	}
	if _, problems, _ := FromNote("p/b.md", []byte(bot+bot)); len(problems) != 1 || !strings.Contains(problems[0].Msg, "2 ```chat blocks") {
		t.Errorf("two blocks: %v", problems)
	}
	if _, _, ok := FromNote("p/b.md", []byte("# none\n")); ok {
		t.Error("a note without a block")
	}
	f, problems := Parse("pipeline: x\nfolders: {path: /documents/manual/}", "p/b.md")
	if len(problems) > 0 || f.Folders == nil || f.Folders.Path != "documents/manual" || f.Folders.Levels != 1 || f.Folders.Key != "folder" || len(f.Folders.Labels) != 0 {
		t.Errorf("folders: %+v %v", f.Folders, problems)
	}
	for in, want := range map[string]string{"p/a/Il Bot.md": "il-bot", "p/2024.md": "bot-2024", "p/!!.md": "bot-"} {
		if got := DefaultName(in); got != want {
			t.Errorf("DefaultName(%q) = %q, want %q", in, got, want)
		}
	}
}

type fakeHost struct {
	mu        sync.Mutex
	notes     map[string]string
	on        map[string]string
	old       []string
	discarded []string
	folders   []string
}

func (h *fakeHost) Folders(root string) ([]string, error) {
	var out []string
	for _, d := range h.folders {
		if strings.HasPrefix(d, root+"/") {
			out = append(out, d)
		}
	}
	return out, nil
}

func (h *fakeHost) ChatNotes() ([]Note, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Note
	for p, c := range h.notes {
		if strings.Contains(c, "type: chat") {
			out = append(out, Note{Path: p, Project: p[:strings.Index(p, "/")], Content: []byte(c)})
		}
	}
	return out, nil
}

func (h *fakeHost) Note(p string) (Note, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.notes[p]
	if !ok {
		return Note{}, errors.New("no note")
	}
	return Note{Path: p, Project: p[:strings.Index(p, "/")], Content: []byte(c)}, nil
}

func (h *fakeHost) PipelineOn(p string) (string, error) {
	on, ok := h.on[p]
	if !ok {
		return "", errors.New("no such note")
	}
	return on, nil
}

func (h *fakeHost) NotesBefore(folder string, t time.Time) ([]string, error) {
	var out []string
	for _, p := range h.old {
		if strings.HasPrefix(p, folder+"/") {
			out = append(out, p)
		}
	}
	return out, nil
}

func (h *fakeHost) Discard(p, actor string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.discarded = append(h.discarded, actor+" "+p)
	return nil
}

type call struct {
	plugin, tool string
	args, meta   map[string]any
}

type fakePlugins struct {
	mu     sync.Mutex
	calls  []call
	refuse bool
}

func (f *fakePlugins) CallTool(_ context.Context, plugin, tool string, args, meta map[string]any) (*mcp.CallToolResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{plugin, tool, args, meta})
	if f.refuse {
		return mcp.NewToolResultError("no such scope filter"), nil
	}
	b, _ := json.Marshal(map[string]any{"public_url": "https://chat.example.com", "embed_url": "https://chat.example.com/embed/" + args["bot"].(string), "script_url": "https://chat.example.com/chat.js"})
	return mcp.NewToolResultStructured(json.RawMessage(b), string(b)), nil
}

func (f *fakePlugins) FilesURL(string) string { return "http://gosidian:8080" }

func (f *fakePlugins) State(name string) (plugins.State, bool) {
	switch name {
	case "chat":
		return plugins.State{Name: name, Status: plugins.StatusOK}, true
	case "old":
		return plugins.State{Name: name, Status: plugins.StatusChanged}, true
	}
	return plugins.State{}, false
}

func (f *fakePlugins) last(tool string) call {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].tool == tool {
			return f.calls[i]
		}
	}
	return call{}
}

type fakeTokens struct {
	mu      sync.Mutex
	byValue map[string]jobs.RunToken
	created int
}

func (f *fakeTokens) CreateToken(ref, name, by string) (jobs.RunToken, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	t := jobs.RunToken{ID: "t" + string(rune('0'+f.created)), Ref: ref, Name: name}
	v := jobs.TokenPrefix + t.ID
	f.byValue[v] = t
	return t, v, nil
}

func (f *fakeTokens) CheckToken(v string) (jobs.RunToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.byValue[v]
	if !ok {
		return t, errors.New("invalid")
	}
	return t, nil
}

func (f *fakeTokens) RevokeToken(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for v, t := range f.byValue {
		if t.ID == id {
			delete(f.byValue, v)
			return nil
		}
	}
	return jobs.ErrNotFound
}

type fakeSecrets map[string]string

func (f fakeSecrets) Get(n string) (string, error) {
	v, ok := f[n]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}
func (f fakeSecrets) Set(n, v string) error { f[n] = v; return nil }
func (f fakeSecrets) Delete(n string) error { delete(f, n); return nil }

func newService(t *testing.T) (*Service, *fakeHost, *fakePlugins, *fakeTokens, fakeSecrets) {
	t.Helper()
	host := &fakeHost{notes: map[string]string{"p/bots/assistente.md": bot}, on: map[string]string{"p/pipelines/chat.md": "chat", "p/pipelines/batch.md": "manual"}}
	pl := &fakePlugins{}
	tok := &fakeTokens{byValue: map[string]jobs.RunToken{}}
	sec := fakeSecrets{"chat-sito": "pk-sito-123", "chat-firma": "firma-segreta"}
	s, err := New(host, pl, tok, sec, t.TempDir(), Options{GosidianURL: "https://vault.example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	return s, host, pl, tok, sec
}

func TestService_Reconcile(t *testing.T) {
	s, host, pl, tok, sec := newService(t)
	ctx := context.Background()
	s.Reconcile(ctx)
	st, ok := s.Status("p/bots/assistente.md")
	if !ok || !st.Configured || st.Error != "" || st.EmbedURL != "https://chat.example.com/embed/assistente" || st.Bot != "assistente" || !st.Users {
		t.Fatalf("state: %+v", st)
	}
	c := pl.last(ToolConfigure)
	run := c.args["run"].(map[string]any)
	access := c.args["access"].(map[string]any)
	if c.plugin != "chat" || run["url"] != "http://gosidian:8080/api/v1/pipelines/p/pipelines/chat.md/runs" || !strings.HasPrefix(run["token"].(string), jobs.TokenPrefix) ||
		access["signed_secret"] != "firma-segreta" || access["users_secret"] == "" || c.meta["gosidian/project"] != "p" {
		t.Errorf("chat_configure: %+v / %+v", c.args, c.meta)
	}
	if keys := access["keys"].([]any); keys[0].(map[string]any)["key"] != "pk-sito-123" {
		t.Errorf("keys: %v", keys)
	}
	if sc := c.args["scopes"].([]any); len(sc) != 2 || sc[0].(map[string]any)["filter"] == nil {
		t.Errorf("scopes: %v", sc)
	}
	if conv := c.args["conversations"].(map[string]any); conv["keep_seconds"] != int64(30*24*3600) {
		t.Errorf("conversations: %v", conv)
	}
	if _, ok := c.args["project"]; ok {
		t.Error("project in the arguments: it is in _meta")
	}
	if fo := s.FrameOrigins(); len(fo) != 1 || fo[0] != "https://chat.example.com/embed/assistente" {
		t.Errorf("frame origins: %v", fo)
	}
	// Again: the same run token, handed again.
	s.Reconcile(ctx)
	if tok.created != 1 || pl.last(ToolConfigure).args["run"].(map[string]any)["token"] != run["token"] {
		t.Errorf("the run token changed: %d", tok.created)
	}
	// Revoked from outside: a new one.
	_ = tok.RevokeToken(st.TokenID)
	s.Reconcile(ctx)
	if tok.created != 2 {
		t.Errorf("no new token after a revoke: %d", tok.created)
	}
	// A plugin that refuses, and a pipeline that does not start on chat.
	pl.refuse = true
	s.Reconcile(ctx)
	if st, _ := s.Status("p/bots/assistente.md"); st.Configured || !strings.Contains(st.Error, "no such scope filter") {
		t.Errorf("refused: %+v", st)
	}
	pl.refuse = false
	host.notes["p/bots/assistente.md"] = strings.Replace(bot, "pipelines/chat", "pipelines/batch", 1)
	s.Reconcile(ctx)
	if st, _ := s.Status("p/bots/assistente.md"); st.Configured || !strings.Contains(st.Error, "starts on manual") {
		t.Errorf("not a chat pipeline: %+v", st)
	}
	host.notes["p/bots/assistente.md"] = strings.Replace(bot, "chat-firma", "chat-assente", 1)
	s.Reconcile(ctx)
	if st, _ := s.Status("p/bots/assistente.md"); st.Configured || !strings.Contains(st.Error, "gosidian secret set chat-assente") {
		t.Errorf("a missing secret: %+v", st)
	}
	for plugin, want := range map[string]string{"none": "is not registered", "old": "is changed"} {
		host.notes["p/bots/assistente.md"] = strings.Replace(bot, "```chat\n", "```chat\nplugin: "+plugin+"\n", 1)
		s.Reconcile(ctx)
		host.mu.Lock()
		delete(host.notes, "p/bots/assistente.md")
		host.mu.Unlock()
		st, _ := s.Status("p/bots/assistente.md")
		if st.Configured || !strings.Contains(st.Error, want) {
			t.Errorf("plugin %s: %+v", plugin, st)
		}
		s.Reconcile(ctx) // the bot of that plugin goes
	}
	host.notes["p/bots/assistente.md"] = bot
	s.Reconcile(ctx)
	// A state kept across a restart.
	s2, err := New(host, pl, tok, sec, s.file[:strings.LastIndex(s.file, "/")], Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Status("p/bots/assistente.md"); !ok {
		t.Error("the state did not survive")
	}
	// The note goes: the bot goes from the plugin, its token and secrets
	// with it.
	host.mu.Lock()
	delete(host.notes, "p/bots/assistente.md")
	host.mu.Unlock()
	s.Reconcile(ctx)
	if _, ok := s.Status("p/bots/assistente.md"); ok {
		t.Error("the state of a bot gone")
	}
	if c := pl.last(ToolRemove); c.args["bot"] != "assistente" {
		t.Errorf("chat_remove: %+v", c)
	}
	if len(tok.byValue) != 0 {
		t.Errorf("tokens left: %v", tok.byValue)
	}
	for n := range sec {
		if strings.HasPrefix(n, "chat.") {
			t.Errorf("a secret left: %s", n)
		}
	}
}

func TestService_UserToken(t *testing.T) {
	s, _, _, _, sec := newService(t)
	if _, _, _, err := s.UserToken("p/bots/assistente.md", "mario"); err == nil {
		t.Error("a token before the bot is configured")
	}
	s.Reconcile(context.Background())
	tok, st, exp, err := s.UserToken("p/bots/assistente.md", "mario")
	if err != nil || st.Bot != "assistente" || exp.Before(time.Now()) {
		t.Fatal(tok, err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %s", tok)
	}
	// As the plugin checks it: HS256 with the users' secret.
	var secret string
	for n, v := range sec {
		if strings.HasPrefix(n, "chat.users.") {
			secret = v
		}
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) != parts[2] {
		t.Error("the signature does not check")
	}
	var claims map[string]any
	b, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(b, &claims)
	if claims["sub"] != "mario" || claims["bot"] != "assistente" || claims["iss"] != "gosidian" || claims["exp"] == nil {
		t.Errorf("claims: %v", claims)
	}
}

func TestService_UsersOff(t *testing.T) {
	s, host, pl, _, _ := newService(t)
	host.notes["p/bots/assistente.md"] = strings.Replace(bot, "access:\n", "access:\n  users: false\n", 1)
	s.Reconcile(context.Background())
	if _, _, _, err := s.UserToken("p/bots/assistente.md", "mario"); err == nil || !strings.Contains(err.Error(), "access.users") {
		t.Errorf("users off: %v", err)
	}
	if a := pl.last(ToolConfigure).args["access"].(map[string]any); a["users_secret"] != nil || a["users"] != false {
		t.Errorf("access: %v", a)
	}
}

func TestService_Prune(t *testing.T) {
	s, host, _, _, _ := newService(t)
	host.old = []string{"p/chat/assistente/2026-06-01.md", "p/chat/altro/x.md"}
	s.Reconcile(context.Background())
	if len(host.discarded) != 1 || host.discarded[0] != "chat:chat/assistente p/chat/assistente/2026-06-01.md" {
		t.Errorf("discarded: %v", host.discarded)
	}
}

func TestService_NoSecrets(t *testing.T) {
	host := &fakeHost{notes: map[string]string{"p/bots/assistente.md": bot}, on: map[string]string{"p/pipelines/chat.md": "chat"}}
	s, err := New(host, &fakePlugins{}, &fakeTokens{byValue: map[string]jobs.RunToken{}}, nil, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.Reconcile(context.Background())
	if st, _ := s.Status("p/bots/assistente.md"); st.Configured || !strings.Contains(st.Error, "GOSIDIAN_SECRETS_KEY") {
		t.Errorf("%+v", st)
	}
}

func TestExpandFolders(t *testing.T) {
	root := "p/docs"
	folders := []string{
		"p/docs/Cucina/Primi/Pasta fresca",
		"p/docs/Cucina/Primi/Risotti/Antichi",
		"p/docs/Cucina/Dolci",
		"p/docs/Giardino e orto/Attività di stagione/Calendario",
		"p/other/Cucina",
	}
	got := ExpandFolders(Folders{Path: "docs", Levels: 2, Key: "categoria"}, root, folders, map[string]bool{"cucina": true})
	if len(got) != 2 {
		t.Fatalf("level 1: %+v", got)
	}
	orto, cucina := got[1], got[0]
	if orto.Name != "giardino-e-orto" || orto.Label != "Giardino e orto" || len(orto.Children) != 1 ||
		orto.Children[0].Name != "giardino-e-orto-attivita-di-stagione" || orto.Children[0].Label != "Attività di stagione" {
		t.Errorf("accents and spaces: %+v", orto)
	}
	if len(orto.Children[0].Children) != 0 {
		t.Errorf("deeper than the levels: %+v", orto.Children[0].Children)
	}
	// cucina is a scope of the block's: this one has the hash.
	if !strings.HasPrefix(cucina.Name, "cucina-") || len(cucina.Name) != len("cucina-")+6 || len(cucina.Children) != 2 {
		t.Errorf("a name taken: %+v", cucina)
	}
	in := cucina.Children[1].Filter["meta"].(map[string]any)["categoria"].(map[string]any)["in"].([]string)
	want := []string{"p/docs/Cucina/Primi", "p/docs/Cucina/Primi/Pasta fresca", "p/docs/Cucina/Primi/Risotti", "p/docs/Cucina/Primi/Risotti/Antichi"}
	if strings.Join(in, "|") != strings.Join(want, "|") {
		t.Errorf("the folders of Primi, at any depth: %v", in)
	}
	long := ExpandFolders(Folders{Path: "docs", Levels: 1, Key: "f"}, root, []string{"p/docs/" + strings.Repeat("Molto lungo ", 6)}, nil)
	if n := long[0].Name; len(n) > 48 || !botName.MatchString(n) {
		t.Errorf("a long name: %q", n)
	}
	if n := ExpandFolders(Folders{Path: "docs", Levels: 1, Key: "f"}, root, []string{"p/docs/2024"}, nil)[0].Name; n != "f-2024" {
		t.Errorf("a name that starts with a digit: %q", n)
	}
}

func TestService_Folders(t *testing.T) {
	s, host, pl, _, _ := newService(t)
	host.notes["p/bots/assistente.md"] = strings.Replace(bot, "access:\n", "folders: {path: docs, levels: 2, key: categoria, labels: [Area, Modulo]}\naccess:\n", 1)
	s.Reconcile(context.Background())
	if st, _ := s.Status("p/bots/assistente.md"); st.Configured || !strings.Contains(st.Error, "no folder with notes") {
		t.Errorf("no folders: %+v", st)
	}
	host.folders = []string{"p/docs/Cucina/Primi", "p/docs/Cucina/Dolci", "p/docs/Attrezzi"}
	s.Reconcile(context.Background())
	if st, _ := s.Status("p/bots/assistente.md"); !st.Configured {
		t.Fatalf("%+v", st)
	}
	c := pl.last(ToolConfigure)
	sc := c.args["scopes"].([]any)
	if len(sc) != 4 || sc[2].(map[string]any)["name"] != "attrezzi" || sc[2].(map[string]any)["children"] != nil || sc[3].(map[string]any)["name"] != "cucina" || len(sc[3].(map[string]any)["children"].([]any)) != 2 {
		t.Errorf("scopes: %v", sc)
	}
	if l := c.args["scope_labels"].([]string); len(l) != 2 || l[1] != "Modulo" {
		t.Errorf("scope_labels: %v", c.args["scope_labels"])
	}
}
