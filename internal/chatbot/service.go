package chatbot

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/plugins"
)

// The tools of a chat plugin (docs/chat.md).
const (
	ToolConfigure = "chat_configure"
	ToolRemove    = "chat_remove"
)

// Note is a note of the vault as the bots read it.
type Note struct {
	Path    string
	Project string
	Content []byte
}

// Host is the server, which the bots are read and the old conversations
// dropped through.
type Host interface {
	// ChatNotes are the notes `type: chat` of every project.
	ChatNotes() ([]Note, error)
	// Note reads one note; an error when there is none.
	Note(path string) (Note, error)
	// PipelineOn is how the pipeline note at path starts; an error says
	// why it cannot run (no such note, a project without pipelines).
	PipelineOn(path string) (string, error)
	// Folders are the folders under root (a path of the vault) that hold
	// notes, at any depth; the media folders of the notes are none.
	Folders(root string) ([]string, error)
	// NotesBefore are the notes of a folder, at any depth, last changed
	// before t.
	NotesBefore(folder string, t time.Time) ([]string, error)
	// Discard moves a note to the trash, as actor.
	Discard(path, actor string) error
}

// Plugins calls the tools of the plugins.
type Plugins interface {
	State(plugin string) (plugins.State, bool)
	CallTool(ctx context.Context, plugin, tool string, args, meta map[string]any) (*mcp.CallToolResult, error)
	FilesURL(plugin string) string
}

// Tokens are the run tokens of the pipelines.
type Tokens interface {
	CreateToken(ref, name, createdBy string) (jobs.RunToken, string, error)
	CheckToken(secret string) (jobs.RunToken, error)
	RevokeToken(id string) error
}

// Secrets are gosidian's secrets.
type Secrets interface {
	Get(name string) (string, error)
	Set(name, value string) error
	Delete(name string) error
}

// State is where a bot stands in its plugin, as the web UI shows it.
type State struct {
	Bot     string `json:"bot"`
	Plugin  string `json:"plugin"`
	Note    string `json:"note"`
	Project string `json:"project"`
	// Configured: the plugin took the bot's last configuration.
	Configured bool      `json:"configured"`
	Error      string    `json:"error,omitempty"`
	Updated    time.Time `json:"updated"`
	// What the plugin answered: its address on the internet, the page of
	// the bot's chat, the widget's script.
	PublicURL string `json:"public_url,omitempty"`
	EmbedURL  string `json:"embed_url,omitempty"`
	ScriptURL string `json:"script_url,omitempty"`
	// Users: gosidian's accounts may chat (UserToken).
	Users bool `json:"users"`
	// Keys are the names of the sites' keys.
	Keys    []string `json:"keys,omitempty"`
	TokenID string   `json:"token_id,omitempty"`
}

func (s State) key() string { return s.Plugin + "/" + s.Bot }

// Options tune a Service.
type Options struct {
	// GosidianURL is the server's public address, for the links of the
	// sources to its notes.
	GosidianURL string
	// Every is how often the bots are handed to their plugins again (a
	// plugin keeps them in memory) and the old conversations go; default
	// 5 minutes.
	Every time.Duration
	// CallTimeout caps a call of a plugin's tool; default 30 seconds.
	CallTimeout time.Duration
	// TokenTTL is how long a token gosidian signs for a user is good;
	// default an hour.
	TokenTTL time.Duration
	Logger   *slog.Logger
	Now      func() time.Time
}

// Service keeps the bots of the vault configured in their plugins.
type Service struct {
	host    Host
	plugins Plugins
	tokens  Tokens
	secrets Secrets
	opts    Options
	file    string

	mu     sync.Mutex
	states map[string]State // by plugin/bot

	// reconcile runs one at a time.
	run     sync.Mutex
	changed chan string
}

// New is the service of the bots, its state in stateDir/chatbots.json.
// secrets may be nil: the bots then cannot be configured, and say so.
func New(host Host, pl Plugins, tokens Tokens, secrets Secrets, stateDir string, opts Options) (*Service, error) {
	if opts.Every <= 0 {
		opts.Every = 5 * time.Minute
	}
	if opts.CallTimeout <= 0 {
		opts.CallTimeout = 30 * time.Second
	}
	if opts.TokenTTL <= 0 {
		opts.TokenTTL = time.Hour
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	s := &Service{host: host, plugins: pl, tokens: tokens, secrets: secrets, opts: opts,
		file: filepath.Join(stateDir, "chatbots.json"), states: map[string]State{}, changed: make(chan string, 64)}
	b, err := os.ReadFile(s.file)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var list []State
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, fmt.Errorf("%s: %w", s.file, err)
		}
		for _, st := range list {
			s.states[st.key()] = st
		}
	}
	return s, nil
}

func (s *Service) save() error {
	list := make([]State, 0, len(s.states))
	for _, st := range s.states {
		list = append(list, st)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].key() < list[j].key() })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.file)
}

// Changed is told that a note was written, moved or deleted: a bot's note
// is handed to its plugin again soon after.
func (s *Service) Changed(path string) {
	select {
	case s.changed <- path:
	default:
	}
}

// Start hands the bots to their plugins now, again every Options.Every
// and soon after a bot's note changed, until ctx ends.
func (s *Service) Start(ctx context.Context) {
	tick := time.NewTicker(s.opts.Every)
	defer tick.Stop()
	// The plugins are checked at the server's start: give them a moment.
	first := time.NewTimer(10 * time.Second)
	defer first.Stop()
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			s.Reconcile(ctx)
		case <-tick.C:
			s.Reconcile(ctx)
		case p := <-s.changed:
			if s.concerns(p) {
				debounce = time.After(2 * time.Second)
			}
		case <-debounce:
			debounce = nil
			s.Reconcile(ctx)
		}
	}
}

// concerns says whether a change of the note at p may change a bot: it
// is a bot's note, or now holds a ```chat block.
func (s *Service) concerns(p string) bool {
	if !strings.HasSuffix(p, ".md") {
		return false
	}
	s.mu.Lock()
	for _, st := range s.states {
		if st.Note == p {
			s.mu.Unlock()
			return true
		}
	}
	s.mu.Unlock()
	n, err := s.host.Note(p)
	return err == nil && len(Find(n.Content)) > 0
}

// Reconcile hands every bot to its plugin, takes out of the plugins the
// bots whose note went, and moves the old conversations to the trash.
func (s *Service) Reconcile(ctx context.Context) {
	s.run.Lock()
	defer s.run.Unlock()
	notes, err := s.host.ChatNotes()
	if err != nil {
		s.opts.Logger.Warn("chat: listing the bots", "err", err)
		return
	}
	seen := map[string]bool{}
	for _, n := range notes {
		spec, problems, ok := FromNote(n.Path, n.Content)
		if !ok {
			continue
		}
		st := State{Bot: spec.Name, Plugin: spec.Plugin, Note: n.Path, Project: n.Project}
		if seen[st.key()] {
			s.opts.Logger.Warn("chat: two notes name the same bot", "bot", st.key(), "note", n.Path)
			continue
		}
		seen[st.key()] = true
		if len(problems) > 0 {
			s.setError(st, "the ```chat block: "+problems[0].String())
			continue
		}
		s.configure(ctx, n, spec)
		s.prune(n, spec)
	}
	s.mu.Lock()
	var gone []State
	for k, st := range s.states {
		if !seen[k] {
			gone = append(gone, st)
		}
	}
	s.mu.Unlock()
	for _, st := range gone {
		s.remove(ctx, st)
	}
}

// ConfigureNote hands the bot of one note to its plugin now.
func (s *Service) ConfigureNote(ctx context.Context, notePath string) (State, error) {
	s.run.Lock()
	defer s.run.Unlock()
	n, err := s.host.Note(notePath)
	if err != nil {
		return State{}, err
	}
	spec, problems, ok := FromNote(n.Path, n.Content)
	if !ok {
		return State{}, errors.New("the note has no ```chat block")
	}
	st := State{Bot: spec.Name, Plugin: spec.Plugin, Note: n.Path, Project: n.Project}
	if len(problems) > 0 {
		s.setError(st, "the ```chat block: "+problems[0].String())
	} else {
		s.configure(ctx, n, spec)
	}
	got, _ := s.state(st.key())
	return got, nil
}

func (s *Service) state(key string) (State, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[key]
	return st, ok
}

// Status is the state of the bot of a note: ok is false when it was never
// handed to a plugin.
func (s *Service) Status(notePath string) (State, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.states {
		if st.Note == notePath {
			return st, true
		}
	}
	return State{}, false
}

func (s *Service) put(st State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st.Updated = s.opts.Now()
	// A bot is logged when it changes, not at every reconcile.
	switch old, ok := s.states[st.key()]; {
	case st.Error != "" && (!ok || old.Error != st.Error):
		s.opts.Logger.Warn("chat: a bot is not configured", "bot", st.key(), "note", st.Note, "err", st.Error)
	case st.Configured && (!ok || !old.Configured):
		s.opts.Logger.Info("chat: bot configured", "bot", st.key(), "note", st.Note, "embed", st.EmbedURL)
	}
	s.states[st.key()] = st
	if err := s.save(); err != nil {
		s.opts.Logger.Warn("chat: saving the bots' state", "err", err)
	}
}

func (s *Service) setError(st State, msg string) {
	if old, ok := s.state(st.key()); ok {
		st.TokenID, st.PublicURL, st.EmbedURL, st.ScriptURL = old.TokenID, old.PublicURL, old.EmbedURL, old.ScriptURL
	}
	st.Configured, st.Error = false, msg
	s.put(st)
}

// secretID is the part of a bot's secrets' names that is its own.
func secretID(plugin, bot string) string {
	sum := sha256.Sum256([]byte(plugin + "/" + bot))
	return hex.EncodeToString(sum[:8])
}

// tokenName is the name of a bot's run token.
func tokenName(plugin, bot string) string { return "chat:" + plugin + "/" + bot }

func randomSecret() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Service) configure(ctx context.Context, n Note, spec Spec) {
	st := State{Bot: spec.Name, Plugin: spec.Plugin, Note: n.Path, Project: n.Project, Users: spec.Access.Users}
	for _, k := range spec.Access.Keys {
		st.Keys = append(st.Keys, k.Name)
	}
	if old, ok := s.state(st.key()); ok {
		st.TokenID = old.TokenID
	}
	args, err := s.configureArgs(n, spec, &st)
	if err != nil {
		st.Configured, st.Error = false, err.Error()
		s.put(st)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, s.opts.CallTimeout)
	defer cancel()
	res, err := s.plugins.CallTool(cctx, spec.Plugin, ToolConfigure, args,
		map[string]any{plugins.MetaProject: n.Project, plugins.MetaCaller: "gosidian", plugins.MetaAccess: plugins.AccessWrite})
	switch {
	case err != nil:
		st.Error = fmt.Sprintf("plugin %s: %v", spec.Plugin, err)
	case res.IsError:
		st.Error = fmt.Sprintf("plugin %s refused the bot: %s", spec.Plugin, resultText(res))
	default:
		var out struct {
			PublicURL string `json:"public_url"`
			EmbedURL  string `json:"embed_url"`
			ScriptURL string `json:"script_url"`
		}
		if err := json.Unmarshal(plugins.ResultJSON(res), &out); err != nil {
			st.Error = fmt.Sprintf("plugin %s: its answer is not the one of %s: %v", spec.Plugin, ToolConfigure, err)
			break
		}
		st.Configured, st.PublicURL, st.EmbedURL, st.ScriptURL = true, out.PublicURL, out.EmbedURL, out.ScriptURL
	}
	s.put(st)
}

// configureArgs are the arguments of chat_configure: the block, with the
// values of the secrets it names, a run token of the pipeline and where to
// start it.
func (s *Service) configureArgs(n Note, spec Spec, st *State) (map[string]any, error) {
	pipelinePath := n.Project + "/" + spec.Pipeline
	on, err := s.host.PipelineOn(pipelinePath)
	switch {
	case err != nil:
		return nil, fmt.Errorf("pipeline %s: %v", pipelinePath, err)
	case on != "chat":
		return nil, fmt.Errorf("pipeline %s starts on %s: a bot's starts on chat (trigger: {on: chat})", pipelinePath, on)
	}
	if s.secrets == nil {
		return nil, errors.New("the bots need gosidian's secrets: set GOSIDIAN_SECRETS_KEY")
	}
	switch ps, ok := s.plugins.State(spec.Plugin); {
	case !ok:
		return nil, fmt.Errorf("plugin %s is not registered ([[pipelines.plugins]], GOSIDIAN_PIPELINES_PLUGINS)", spec.Plugin)
	case ps.Status != plugins.StatusOK:
		return nil, fmt.Errorf("plugin %s is %s (gosidian plugin list; accept its manifest with gosidian plugin accept)", spec.Plugin, ps.Status)
	}
	base := strings.TrimRight(s.plugins.FilesURL(spec.Plugin), "/")
	if base == "" {
		return nil, errors.New("the plugin does not know gosidian's address: set [pipelines] files_url (or the plugin's files_url)")
	}
	token, err := s.runToken(pipelinePath, spec, st)
	if err != nil {
		return nil, err
	}
	access := map[string]any{"users": spec.Access.Users, "open": spec.Access.Open}
	if spec.Access.Users {
		v, err := s.secretOrNew("chat.users." + secretID(spec.Plugin, spec.Name))
		if err != nil {
			return nil, err
		}
		access["users_secret"] = v
	}
	keys := []any{}
	for _, k := range spec.Access.Keys {
		v, err := s.secrets.Get(k.Secret)
		if err != nil {
			return nil, fmt.Errorf("key %s: the secret %q: %v (gosidian secret set %s)", k.Name, k.Secret, err, k.Secret)
		}
		keys = append(keys, map[string]any{"name": k.Name, "key": v, "origins": k.Origins})
	}
	access["keys"] = keys
	if spec.Access.Signed != "" {
		v, err := s.secrets.Get(spec.Access.Signed)
		if err != nil {
			return nil, fmt.Errorf("access.signed: the secret %q: %v (gosidian secret set %s)", spec.Access.Signed, err, spec.Access.Signed)
		}
		access["signed_secret"] = v
	}
	all := spec.Scopes
	labels := []string{}
	if f := spec.Folders; f != nil {
		root := n.Project + "/" + f.Path
		folders, err := s.host.Folders(root)
		if err != nil {
			return nil, fmt.Errorf("folders.path %s: %v", f.Path, err)
		}
		taken := map[string]bool{}
		for _, sc := range spec.Scopes {
			taken[sc.Name] = true
		}
		more := ExpandFolders(*f, root, folders, taken)
		if len(more) == 0 {
			return nil, fmt.Errorf("folders.path %s: no folder with notes under it", f.Path)
		}
		all = append(append([]Scope{}, spec.Scopes...), more...)
		labels = f.Labels
	}
	scopes := scopeArgs(all)
	limits := map[string]any{}
	for k, v := range spec.Limits {
		limits[k] = v
	}
	conv := map[string]any{}
	if spec.Conversations.Folder != "" {
		conv["folder"] = spec.Conversations.Folder
	}
	if spec.Conversations.Keep > 0 {
		conv["keep_seconds"] = int64(spec.Conversations.Keep / time.Second)
	}
	return map[string]any{
		"bot": spec.Name, "note": n.Path,
		"title": spec.Title, "welcome": spec.Welcome,
		"scopes": scopes, "scope_labels": labels, "access": access, "limits": limits, "conversations": conv,
		"sources":      map[string]any{"base_url": spec.Sources.BaseURL, "gosidian_url": strings.TrimRight(s.opts.GosidianURL, "/")},
		"run":          map[string]any{"url": base + "/api/v1/pipelines/" + pipelinePath + "/runs", "token": token, "pipeline": pipelinePath},
		"gosidian_url": strings.TrimRight(s.opts.GosidianURL, "/"),
	}, nil
}

// scopeArgs are scopes as chat_configure takes them, with their children.
func scopeArgs(scopes []Scope) []any {
	out := []any{}
	for _, sc := range scopes {
		m := map[string]any{"name": sc.Name, "label": sc.Label}
		if sc.Filter != nil {
			m["filter"] = sc.Filter
		}
		if len(sc.Children) > 0 {
			m["children"] = scopeArgs(sc.Children)
		}
		out = append(out, m)
	}
	return out
}

// runToken is the bot's run token of its pipeline, kept among the
// secrets: the same while it works, a new one, the old revoked, when the
// pipeline changed or it was revoked.
func (s *Service) runToken(pipelinePath string, spec Spec, st *State) (string, error) {
	name := "chat.run." + secretID(spec.Plugin, spec.Name)
	if v, err := s.secrets.Get(name); err == nil {
		if t, err := s.tokens.CheckToken(v); err == nil && t.Ref == pipelinePath {
			st.TokenID = t.ID
			return v, nil
		}
	}
	if st.TokenID != "" {
		_ = s.tokens.RevokeToken(st.TokenID)
	}
	t, v, err := s.tokens.CreateToken(pipelinePath, tokenName(spec.Plugin, spec.Name), "gosidian")
	if err != nil {
		return "", fmt.Errorf("the run token: %w", err)
	}
	if err := s.secrets.Set(name, v); err != nil {
		_ = s.tokens.RevokeToken(t.ID)
		return "", fmt.Errorf("keeping the run token: %w", err)
	}
	st.TokenID = t.ID
	return v, nil
}

func (s *Service) secretOrNew(name string) (string, error) {
	if v, err := s.secrets.Get(name); err == nil {
		return v, nil
	}
	v := randomSecret()
	if err := s.secrets.Set(name, v); err != nil {
		return "", fmt.Errorf("the secret of the users' tokens: %w", err)
	}
	return v, nil
}

// remove takes a bot whose note went out of its plugin, revokes its run
// token and drops its secrets.
func (s *Service) remove(ctx context.Context, st State) {
	cctx, cancel := context.WithTimeout(ctx, s.opts.CallTimeout)
	defer cancel()
	res, err := s.plugins.CallTool(cctx, st.Plugin, ToolRemove, map[string]any{"bot": st.Bot},
		map[string]any{plugins.MetaProject: st.Project, plugins.MetaCaller: "gosidian", plugins.MetaAccess: plugins.AccessWrite})
	if err != nil || res.IsError {
		// The run token goes all the same: the plugin can start nothing.
		s.opts.Logger.Warn("chat: removing a bot from its plugin", "bot", st.key(), "err", err)
	}
	if st.TokenID != "" {
		_ = s.tokens.RevokeToken(st.TokenID)
	}
	if s.secrets != nil {
		_ = s.secrets.Delete("chat.run." + secretID(st.Plugin, st.Bot))
		_ = s.secrets.Delete("chat.users." + secretID(st.Plugin, st.Bot))
	}
	s.mu.Lock()
	delete(s.states, st.key())
	if err := s.save(); err != nil {
		s.opts.Logger.Warn("chat: saving the bots' state", "err", err)
	}
	s.mu.Unlock()
	s.opts.Logger.Info("chat: bot removed", "bot", st.key(), "note", st.Note)
}

// prune moves to the trash the conversations of a bot older than its
// conversations.keep.
func (s *Service) prune(n Note, spec Spec) {
	if spec.Conversations.Keep <= 0 || spec.Conversations.Folder == "" {
		return
	}
	folder := path.Join(n.Project, spec.Conversations.Folder)
	old, err := s.host.NotesBefore(folder, s.opts.Now().Add(-spec.Conversations.Keep))
	if err != nil {
		s.opts.Logger.Warn("chat: the old conversations", "bot", spec.Name, "folder", folder, "err", err)
		return
	}
	for _, p := range old {
		if p == n.Path {
			continue
		}
		if err := s.host.Discard(p, "chat:"+spec.Plugin+"/"+spec.Name); err != nil {
			s.opts.Logger.Warn("chat: an old conversation", "path", p, "err", err)
		}
	}
	if len(old) > 0 {
		s.opts.Logger.Info("chat: old conversations to the trash", "bot", spec.Name, "count", len(old))
	}
}

// FrameOrigins are the addresses of the bots' pages, which their notes
// show in a frame: the SPA's Content-Security-Policy admits them.
func (s *Service) FrameOrigins() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, st := range s.states {
		if st.Configured && st.EmbedURL != "" {
			out = append(out, st.EmbedURL)
		}
	}
	return out
}

// UserToken signs a token for a user of gosidian to chat with the bot of a
// note: a JWT (HS256) with the bot's users secret. The caller checked
// that the user reads the note's project.
func (s *Service) UserToken(notePath, user string) (token string, st State, expires time.Time, err error) {
	st, ok := s.Status(notePath)
	switch {
	case !ok:
		return "", st, expires, errors.New("the bot is not configured yet")
	case !st.Users:
		return "", st, expires, errors.New("the bot does not take gosidian's users (access.users is false)")
	case !st.Configured:
		return "", st, expires, fmt.Errorf("the bot is not configured: %s", st.Error)
	case s.secrets == nil:
		return "", st, expires, errors.New("the bots need gosidian's secrets: set GOSIDIAN_SECRETS_KEY")
	}
	secret, err := s.secrets.Get("chat.users." + secretID(st.Plugin, st.Bot))
	if err != nil {
		return "", st, expires, fmt.Errorf("the secret of the users' tokens: %w", err)
	}
	now := s.opts.Now()
	expires = now.Add(s.opts.TokenTTL)
	token, err = SignToken(secret, map[string]any{"iss": "gosidian", "sub": user, "bot": st.Bot,
		"iat": now.Unix(), "exp": expires.Unix()})
	return token, st, expires, err
}

// SignToken is a JWT (HS256) of claims, signed with secret: the tokens a
// site signs for its users have the same form (docs/chat.md).
func SignToken(secret string, claims map[string]any) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	msg := header + "." + base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	return msg + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := mcp.AsTextContent(c); ok {
			b.WriteString(tc.Text)
		}
	}
	t := b.String()
	if len(t) > 300 {
		t = t[:300] + "…"
	}
	return t
}
