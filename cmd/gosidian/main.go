package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	apiv1 "github.com/gosidian/gosidian/internal/api/v1"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/i18n"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/insights"
	"github.com/gosidian/gosidian/internal/ldap"
	mcpsrv "github.com/gosidian/gosidian/internal/mcp"
	"github.com/gosidian/gosidian/internal/metrics"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/scaffold"
	"github.com/gosidian/gosidian/internal/server"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/statedir"
	"github.com/gosidian/gosidian/internal/trash"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
)

// initLogger sets up slog as the default logger and bridges the stdlib
// `log` package output through it. Format and level come from env:
//
//	GOSIDIAN_LOG_FORMAT  text (default) | json
//	GOSIDIAN_LOG_LEVEL   debug | info (default) | warn | error
func initLogger() {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("GOSIDIAN_LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}

	var h slog.Handler
	if strings.EqualFold(os.Getenv("GOSIDIAN_LOG_FORMAT"), "json") {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))

	// Bridge: the legacy log.Printf calls in internal/* keep working but
	// route their lines through the same slog handler at INFO level.
	log.SetFlags(0)
	log.SetOutput(slog.NewLogLogger(h, slog.LevelInfo).Writer())
}

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	initLogger()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "token":
			runTokenCmd(os.Args[2:])
			return
		case "healthcheck":
			runHealthcheckCmd(os.Args[2:])
			return
		case "user":
			runUserCmd(os.Args[2:])
			return
		case "import-vault":
			runImportCmd(os.Args[2:])
			return
		}
	}

	vaultDir := flag.String("vault", "", "path to vault directory (required)")
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "", "path to SQLite index file (default: <state-dir>/index.db)")
	stateDir := flag.String("state-dir", "", "directory for machine-owned state — credentials, config, project flags, audit log, index (default: <vault>/.gosidian; env GOSIDIAN_STATE_DIR). Setting it moves those files out of the vault root, migrating them once at boot.")
	mcpAddr := flag.String("mcp-addr", "", "Optional standalone MCP (HTTP+SSE) listen address (e.g. 127.0.0.1:8765). Deprecated: MCP is always served on the web port at /mcp/sse. Set this only when a separate listener is required for backward compatibility.")
	flag.Parse()

	// Env var overrides: CLI > env > default.
	// Apply only when the flag is still at its default (user didn't pass it).
	envOverride(vaultDir, "GOSIDIAN_VAULT", "")
	envOverride(addr, "GOSIDIAN_ADDR", ":8080")
	envOverride(dbPath, "GOSIDIAN_DB", "")
	envOverride(stateDir, statedir.EnvVar, "")
	envOverride(mcpAddr, "GOSIDIAN_MCP_ADDR", "")

	if *vaultDir == "" {
		log.Fatal("--vault is required")
	}
	absVault, err := filepath.Abs(*vaultDir)
	if err != nil {
		log.Fatalf("vault path: %v", err)
	}
	if st, err := os.Stat(absVault); err != nil || !st.IsDir() {
		log.Fatalf("vault dir not accessible: %v", err)
	}

	// State dir (ADR-023): credentials, config, project flags, audit log and
	// the index live here. Default <vault>/.gosidian (unchanged); an explicit
	// --state-dir / GOSIDIAN_STATE_DIR moves them out of the vault root with a
	// one-time migration of the known files. templates/ and trash/ are vault
	// content and always stay under <vault>/.gosidian.
	sdir, sdirDefault, err := statedir.Resolve(absVault, *stateDir, "")
	if err != nil {
		log.Fatalf("state dir: %v", err)
	}
	legacyDir := filepath.Join(absVault, statedir.DefaultSubdir)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		log.Fatalf("mkdir .gosidian: %v", err)
	}
	if sdirDefault {
		log.Printf("state dir: %s (default, inside the vault)", sdir)
	} else {
		if err := os.MkdirAll(sdir, 0o700); err != nil {
			log.Fatalf("mkdir state dir: %v", err)
		}
		moved, err := statedir.Migrate(legacyDir, sdir, log.Printf)
		if err != nil {
			log.Fatalf("state dir migration: %v", err)
		}
		if len(moved) > 0 {
			log.Printf("state dir: moved %v from %s to %s", moved, legacyDir, sdir)
		}
		log.Printf("state dir: %s (custom)", sdir)
	}
	if *dbPath == "" {
		*dbPath = filepath.Join(sdir, "index.db")
	}

	metrics.Register()

	idx, err := index.Open(*dbPath)
	if err != nil {
		log.Fatalf("open index: %v", err)
	}
	defer idx.Close()

	// Every machine-owned file follows the state dir, not the index path:
	// --db relocates the index alone.
	hiddenDir := sdir
	tokensPath := filepath.Join(hiddenDir, "tokens.json")
	tokenStore, err := auth.Open(tokensPath)
	if err != nil {
		log.Fatalf("open token store: %v", err)
	}

	authPath := filepath.Join(hiddenDir, "auth.json")
	webauthStore, err := webauth.Open(authPath)
	if err != nil {
		log.Fatalf("open web auth: %v", err)
	}

	auditPath := filepath.Join(hiddenDir, "audit.jsonl")
	auditLog, err := audit.Open(auditPath)
	if err != nil {
		log.Fatalf("open audit log: %v", err)
	}
	log.Printf("audit log: %s", auditPath)

	projectsPath := filepath.Join(hiddenDir, "projects.json")
	projectsStore, err := projects.Open(projectsPath)
	if err != nil {
		log.Fatalf("open projects flag store: %v", err)
	}

	gitTokenPath := filepath.Join(hiddenDir, "gitsync.json")
	gitTokenStore, err := gitsync.OpenTokenStore(gitTokenPath)
	if err != nil {
		log.Fatalf("open gitsync token store: %v", err)
	}

	// v2.0: SPA browser-session token store, separate from MCP tokens.
	// Wired unconditionally so the /api/v1/login endpoint always works;
	// the SPA shell itself is opt-in via GOSIDIAN_SPA_MODE.
	spaTokensPath := filepath.Join(hiddenDir, "spa_tokens.json")
	spaTokenStore, err := auth.OpenSpaTokens(spaTokensPath)
	if err != nil {
		log.Fatalf("open spa token store: %v", err)
	}
	spaTokenStore.StartCleanup(1 * time.Hour)
	defer spaTokenStore.Close()

	// v2.0: SSE events hub. Subscribers connect via /api/v1/events; the
	// vault watcher and MCP write handlers publish here in subsequent
	// phases. Always allocated (cheap) even when SPA mode is off.
	eventsHub := events.New(events.HubOptions{Logger: slog.Default()})
	if webauthStore.Enabled() {
		users := webauthStore.ListUsers()
		log.Printf("web auth: enabled (%d account(s), owner=%q, TOTP=%v)", len(users), webauthStore.Username(), webauthStore.TOTPEnabled())
	} else {
		log.Printf("web auth: disabled (run `gosidian user setup` to provision)")
	}

	// v1.4 migration: wire the disable-user cascade so disabling a member
	// in /admin/users immediately revokes their MCP tokens. Also retro-assign
	// any ownerless tokens (created pre-v1.4 or via CLI) to the first owner,
	// so the web UI /admin/tokens page is never stuck with orphan tokens
	// that a member cannot see.
	webauthStore.SetOnUserDisabled(func(userID string) {
		revoked := tokenStore.RevokeByOwner(userID)
		if revoked > 0 {
			log.Printf("webauth: user %s disabled, revoked %d MCP token(s)", userID, revoked)
		}
	})
	if owner := webauthStore.FirstOwner(); owner != nil {
		if updated := tokenStore.AssignOwnerToOrphans(owner.ID); updated > 0 {
			log.Printf("auth: migrated %d ownerless token(s) to owner %s", updated, owner.Username)
		}
	}

	cfgPath := filepath.Join(hiddenDir, "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.ApplyEnv(); err != nil {
		log.Fatalf("apply env: %v", err)
	}

	// v2.2: global TOTP policy from config. Backward-compat: if the mode
	// resolves to "off" (the default) but an account already has a secret
	// (e.g. an owner provisioned via `user setup --totp`), bump to "optional"
	// so their two-factor stays enforced after the upgrade rather than going
	// silently dormant.
	totpMode := cfg.Webauth.TOTPMode
	if (totpMode == "" || totpMode == "off") && webauthStore.AnyTOTPEnrolled() {
		totpMode = "optional"
		log.Printf("web auth: TOTP mode bumped off→optional (enrolled account present)")
	}
	webauthStore.SetTOTPMode(totpMode)
	log.Printf("web auth: TOTP mode = %s", webauthStore.TOTPMode())
	if cfg.Webauth.OpenMode == "readonly" {
		log.Printf("web auth: OPEN MODE (read-only) — token-less requests run as anonymous guest, public projects only. Anyone who can reach this server can read public projects.")
	}

	// v2.2: optional LDAP auth. When enabled, unknown users that authenticate
	// against the directory are auto-provisioned as guests (webauth.Authenticate).
	var ldapAuth webauth.LDAPAuthenticator
	if cfg.LDAP.Enabled {
		lc, lerr := ldap.New(ldap.Config{
			URL:          cfg.LDAP.URL,
			StartTLS:     cfg.LDAP.StartTLS,
			SkipVerify:   cfg.LDAP.SkipVerify,
			BindDN:       cfg.LDAP.BindDN,
			BindPassword: cfg.LDAP.BindPassword,
			UserBaseDN:   cfg.LDAP.UserBaseDN,
			UserFilter:   cfg.LDAP.UserFilter,
		})
		if lerr != nil {
			log.Fatalf("ldap config: %v", lerr)
		}
		ldapAuth = lc
		log.Printf("ldap: enabled (url=%q base=%q filter=%q)", cfg.LDAP.URL, cfg.LDAP.UserBaseDN, cfg.LDAP.UserFilter)
	}

	if tokenStore.Empty() {
		log.Printf("auth: token store empty, running in open mode (provision via `gosidian token create`)")
	} else {
		log.Printf("auth: %d token(s) loaded from %s", len(tokenStore.List()), tokensPath)
	}

	// v1.8: seed bootstrap templates at first boot. Idempotent —
	// existing template folders are never overwritten, absent ones are
	// populated from the binary-embedded copies.
	if seeded, err := scaffold.SeedTemplates(absVault, mcpsrv.EmbeddedTemplatesFS(), mcpsrv.EmbeddedTemplatesRoot); err != nil {
		log.Printf("templates seed failed: %v (scaffold tool may return stale data)", err)
	} else if len(seeded) > 0 {
		log.Printf("templates: seeded %v under %s/.gosidian/templates/", seeded, absVault)
	}

	v := vault.New(absVault)
	if cfg.Vault.CacheSize != 128 {
		v.SetCacheSize(cfg.Vault.CacheSize)
		log.Printf("vault cache size set to %d", cfg.Vault.CacheSize)
	}
	v.SetHTMLNotes(cfg.Vault.HTMLNotes)
	if cfg.Vault.HTMLNotes {
		log.Printf("html notes enabled: single-file .html treated as first-class notes (ADR-011)")
	}
	v.SetMediaNotes(cfg.Vault.MediaNotes)
	if cfg.Vault.MediaNotes {
		log.Printf("media notes enabled: markdown notes with type:image + media: resolved as image notes (ADR-013)")
	}
	v.SetTableNotes(cfg.Vault.TableNotes)
	if cfg.Vault.TableNotes {
		log.Printf("table notes enabled: markdown notes with type:table + media: resolved as CSV table notes (ADR-016)")
	}
	// Web-side login rate-limit + session TTL configuration moved
	// to internal/api/v1 — the legacy server.ConfigureLogin retired
	// at the v2.0 cutover alongside the cookie-session middleware.

	// Seed the shared global projects (public + private) when the feature is
	// enabled, so they exist + are flagged before the initial scan picks them
	// up. Idempotent: existing folders, flags and READMEs are preserved.
	if cfg.Global.Enabled {
		seedGlobalProjects(v, projectsStore, cfg.Global)
		// Seed the bootstrap templates into the global public project so they
		// are editable as notes; the scaffold tools read from there when the
		// global feature is on. Idempotent.
		gtdir := filepath.Join(absVault, cfg.Global.PublicProject, "templates")
		if seeded, err := scaffold.SeedTemplatesInto(gtdir, mcpsrv.EmbeddedTemplatesFS(), mcpsrv.EmbeddedTemplatesRoot); err != nil {
			log.Printf("global templates seed failed: %v", err)
		} else if len(seeded) > 0 {
			log.Printf("global: seeded templates %v under %s/templates/", seeded, cfg.Global.PublicProject)
		}
	}

	log.Printf("scanning vault %s", absVault)
	if err := v.ScanInto(idx); err != nil {
		log.Fatalf("scan: %v", err)
	}
	if err := idx.ResolveAll(); err != nil {
		log.Fatalf("resolve links: %v", err)
	}
	if all, err := idx.AllNotes(); err == nil {
		metrics.NotesGauge.Set(float64(len(all)))
	}
	log.Printf("scan complete")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	syncer := gitsync.New(absVault, cfg.Git)
	syncer.SetProjects(projectsStore)
	syncer.SetTokenStore(gitTokenStore)
	if err := syncer.Start(ctx); err != nil {
		// IMP-002: gitsync init failure is non-fatal. Log loudly, mark the
		// subsystem as degraded (Sync.Status() + Prometheus gauge), and keep
		// serving HTTP + MCP. Operators see the error via /healthz and /metrics
		// and can restart once the underlying issue (missing git binary, bad
		// remote, credentials, ...) is addressed. A fatal exit at boot would
		// take down unrelated subsystems with it.
		log.Printf("gitsync: DEGRADED at startup: %v", err)
	}
	// fsnotify-driven onChange fans out to two consumers:
	//   - gitsync.TriggerCommit when git sync is enabled (debounced
	//     auto-commit of vault changes).
	//   - eventsHub publish on the `tree` topic so SSE subscribers
	//     invalidate their sidebar cache. Carries no path data
	//     because the v1 watcher signature doesn't surface it; the
	//     SPA refetches /api/v1/tree on receipt. Per-path note
	//     events come from the MCP/api write hooks instead, which
	//     do know which path changed.
	onChange := func() {
		if cfg.Git.Enabled {
			syncer.TriggerCommit()
		}
		if eventsHub != nil {
			eventsHub.Publish(events.TopicTree, map[string]any{
				"action": "fs_change",
				"source": "watcher",
			})
		}
	}
	if cfg.Git.Enabled {
		if syncer.Status().Healthy {
			log.Printf("gitsync: enabled (debounce=%s push=%v)", cfg.Git.Debounce, cfg.Git.Push)
		} else {
			log.Printf("gitsync: enabled in config but subsystem is DEGRADED — commits will be skipped")
		}
	}

	go func() {
		if err := v.Watch(ctx, idx, onChange); err != nil && ctx.Err() == nil {
			log.Printf("watcher: %v", err)
		}
	}()

	srv := server.New(v, idx)
	srv.SetBuildInfo(version, cfg.Git.Enabled)

	// i18n: load embedded catalogues. /api/v1/i18n serves them to the
	// SPA; load failures are not fatal — vue-i18n falls back to bundle-
	// time strings, dev-friendly enough that we don't bring down boot.
	if _, err := i18n.Load(cfg.I18n.DefaultLang); err != nil {
		log.Printf("i18n: load error (SPA falls back to bundled strings): %v", err)
	} else {
		log.Printf("i18n: loaded (default=%s, enabled=%v)", cfg.I18n.DefaultLang, cfg.I18n.EnabledLangs)
	}
	if cfg.Git.Enabled {
		srv.SetGitSync(syncer)
	}
	var trashBin *trash.Bin
	if cfg.Trash.Enabled {
		trashBin = trash.New(absVault, cfg.Trash.Retention)
		if removed, err := trashBin.PruneExpired(); err != nil {
			log.Printf("trash prune: %v", err)
		} else if removed > 0 {
			log.Printf("trash: pruned %d expired entries (retention %s)", removed, cfg.Trash.Retention)
		}
		log.Printf("trash: enabled (retention %s)", cfg.Trash.Retention)
	}
	// MCP is always wired and mounted on the web mux at /mcp/sse — single-port
	// mode is the recommended deployment shape (one SSH tunnel forwards
	// :8080 and exposes both web UI and MCP). The legacy standalone listener
	// is opt-in via --mcp-addr / GOSIDIAN_MCP_ADDR for backward compatibility.
	mcpServer := mcpsrv.New(v, idx, tokenStore)
	mcpServer.SetAuditLog(auditLog)
	mcpServer.SetWriteLimits(cfg.MCP.WritePerMinute, cfg.MCP.MaxNoteBytes)
	mcpServer.SetAllowedUploadRoots(cfg.MCP.AllowedUploadRoots)
	mcpServer.SetBridgeDir(cfg.MCP.BridgeDir)
	if cfg.MCP.BridgeDir != "" {
		log.Printf("mcp bridge dir: %s (stage files here, reference by bridge_filename — IMP-059)", cfg.MCP.BridgeDir)
	}
	mcpServer.SetIngestURLAllowlist(cfg.MCP.IngestURLAllowlist)
	if len(cfg.MCP.IngestURLAllowlist) > 0 {
		log.Printf("mcp ingest url allowlist: %s (memory_ingest url source enabled)", strings.Join(cfg.MCP.IngestURLAllowlist, ", "))
	}
	mcpServer.SetProjects(projectsStore)
	// MCP write handlers publish on the SSE hub so SPA subscribers
	// see external-tab + agent edits in real time.
	mcpServer.SetEvents(eventsHub)
	mcpServer.SetLintExtraAllowedTags(cfg.Lint.FrontmatterTagVocabulary.ExtraAllowed)
	mcpServer.SetLintHotOversizeLimit(cfg.Lint.HotOversizeBytes)
	mcpServer.SetSelfImprove(cfg.SelfImprove.Enabled, cfg.SelfImprove.TargetProject)
	mcpServer.SetSelfImproveNudge(cfg.SelfImprove.EveryNCalls, cfg.SelfImprove.MaxNudgesPerSession, time.Duration(cfg.SelfImprove.CooldownMinutes)*time.Minute)
	mcpServer.SetGlobal(cfg.Global.Enabled, cfg.Global.PublicProject, cfg.Global.PrivateProject)
	mcpServer.SetAgentAnchors(cfg.AgentAnchors.Enabled)

	// v2.0: REST API router under /api/v1/. Mounted always (purely
	// additive). The SPA shell on `/` is gated by env var below.
	apiv1.Version = version
	apiv1.DefaultLang = cfg.I18n.DefaultLang
	apiv1.EnabledLangs = cfg.I18n.EnabledLangs
	apiv1.OpenMode = cfg.Webauth.OpenMode == "readonly"
	apiv1.SelfImproveEnabled = cfg.SelfImprove.Enabled
	apiv1.SelfImproveProject = cfg.SelfImprove.TargetProject

	// Phase 5: optional scheduled digest of pending insights (+ email when
	// SMTP is set). Only runs when the loop is on and an interval is given.
	if cfg.SelfImprove.Enabled && cfg.SelfImprove.DigestInterval > 0 {
		digester := insights.New(v, idx, insights.DigestConfig{
			Project:     cfg.SelfImprove.TargetProject,
			NotifyEmail: cfg.SelfImprove.NotifyEmail,
			SMTP: insights.SMTPConfig{
				Host:     cfg.SelfImprove.SMTPHost,
				Port:     cfg.SelfImprove.SMTPPort,
				From:     cfg.SelfImprove.SMTPFrom,
				Username: cfg.SelfImprove.SMTPUsername,
				Password: cfg.SelfImprove.SMTPPassword,
			},
		}, slog.Default())
		go digester.Start(ctx, cfg.SelfImprove.DigestInterval)
		log.Printf("self-improve: digest scheduler enabled (interval=%s)", cfg.SelfImprove.DigestInterval)
	}

	apiRouter := apiv1.NewRouter(&apiv1.Deps{
		Auth: &apiv1.AuthDeps{
			WebAuth:   webauthStore,
			SpaAuth:   spaTokenStore,
			MCPTokens: tokenStore,
			LDAP:      ldapAuth,
			Logger:    slog.Default(),
			OpenMode:  cfg.Webauth.OpenMode == "readonly",
		},
		Audit:      auditLog,
		Vault:      v,
		Events:     eventsHub,
		Index:      idx,
		Renderer:   parser.NewRenderer(),
		Trash:      trashBin,
		Projects:   projectsStore,
		GitSync:    syncer, // nil-safe; History returns "git sync disabled" when cfg off
		ConfigPath: cfgPath,
	})
	srv.MountAPIv1(apiRouter)
	srv.SetVaultFileAuthorizer(apiRouter.VaultFileAuthorizer()) // ADR-022: attachments share the API auth
	srv.MountMCP(mcpServer.Handler("/mcp"))
	// v2.0 cutover: the SPA is the only frontend. The legacy
	// GOSIDIAN_SPA_MODE flag was retired alongside the HTMX
	// templates and per-page handlers — see docs/migration-v2.md.

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("listening on %s (web + MCP at /mcp/sse)", *addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	var legacyMCPSrv *http.Server
	if *mcpAddr != "" {
		log.Printf("MCP legacy listener on %s (DEPRECATED — clients should use %s/mcp/sse)", *mcpAddr, *addr)
		legacyMCPSrv = &http.Server{
			Addr:              *mcpAddr,
			Handler:           mcpServer.Handler(""),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			if err := legacyMCPSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed && ctx.Err() == nil {
				log.Fatalf("mcp legacy: %v", err)
			}
		}()
	}

	<-ctx.Done()
	log.Printf("shutting down")
	// Flush any pending git commit before exit.
	if cfg.Git.Enabled {
		syncer.Flush()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	if legacyMCPSrv != nil {
		_ = legacyMCPSrv.Shutdown(shutdownCtx)
	}
}
