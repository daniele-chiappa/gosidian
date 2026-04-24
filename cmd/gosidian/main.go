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

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/i18n"
	"github.com/gosidian/gosidian/internal/index"
	mcpsrv "github.com/gosidian/gosidian/internal/mcp"
	"github.com/gosidian/gosidian/internal/metrics"
	"github.com/gosidian/gosidian/internal/scaffold"
	"github.com/gosidian/gosidian/internal/server"
	"github.com/gosidian/gosidian/internal/trash"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
)

// initLogger sets up slog as the default logger and bridges the stdlib
// `log` package output through it. Format and level come from env:
//   GOSIDIAN_LOG_FORMAT  text (default) | json
//   GOSIDIAN_LOG_LEVEL   debug | info (default) | warn | error
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
	dbPath := flag.String("db", "", "path to SQLite index file (default: <vault>/.gosidian/index.db)")
	mcpAddr := flag.String("mcp-addr", "", "MCP (HTTP+SSE) listen address, e.g. 127.0.0.1:8765. Empty disables MCP.")
	flag.Parse()

	// Env var overrides: CLI > env > default.
	// Apply only when the flag is still at its default (user didn't pass it).
	envOverride(vaultDir, "GOSIDIAN_VAULT", "")
	envOverride(addr, "GOSIDIAN_ADDR", ":8080")
	envOverride(dbPath, "GOSIDIAN_DB", "")
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

	if *dbPath == "" {
		hidden := filepath.Join(absVault, ".gosidian")
		if err := os.MkdirAll(hidden, 0o755); err != nil {
			log.Fatalf("mkdir .gosidian: %v", err)
		}
		*dbPath = filepath.Join(hidden, "index.db")
	}

	metrics.Register()

	idx, err := index.Open(*dbPath)
	if err != nil {
		log.Fatalf("open index: %v", err)
	}
	defer idx.Close()

	hiddenDir := filepath.Dir(*dbPath)
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
	server.ConfigureLogin(cfg.Webauth.SessionTTL, cfg.Webauth.LoginWindow, cfg.Webauth.LoginMaxFailures)
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
	if err := syncer.Start(ctx); err != nil {
		// IMP-002: gitsync init failure is non-fatal. Log loudly, mark the
		// subsystem as degraded (Sync.Status() + Prometheus gauge), and keep
		// serving HTTP + MCP. Operators see the error via /healthz and /metrics
		// and can restart once the underlying issue (missing git binary, bad
		// remote, credentials, ...) is addressed. A fatal exit at boot would
		// take down unrelated subsystems with it.
		log.Printf("gitsync: DEGRADED at startup: %v", err)
	}
	var onChange func()
	if cfg.Git.Enabled {
		if syncer.Status().Healthy {
			log.Printf("gitsync: enabled (debounce=%s push=%v)", cfg.Git.Debounce, cfg.Git.Push)
		} else {
			log.Printf("gitsync: enabled in config but subsystem is DEGRADED — commits will be skipped")
		}
		onChange = syncer.TriggerCommit
	}

	go func() {
		if err := v.Watch(ctx, idx, onChange); err != nil && ctx.Err() == nil {
			log.Printf("watcher: %v", err)
		}
	}()

	srv := server.New(v, idx, tokenStore, cfgPath, webauthStore)
	srv.SetBuildInfo(version, cfg.Git.Enabled)
	srv.SetAuditLog(auditLog)

	// i18n: load embedded catalogues. Missing files are not fatal — the
	// catalogue falls back to key literals so developers see missing
	// translations immediately.
	if cat, err := i18n.Load(cfg.I18n.DefaultLang); err != nil {
		log.Printf("i18n: load error (using key literals as fallback): %v", err)
	} else {
		srv.SetI18n(cat, cfg.I18n.DefaultLang)
		log.Printf("i18n: loaded (default=%s, enabled=%v)", cfg.I18n.DefaultLang, cfg.I18n.EnabledLangs)
	}
	if cfg.Git.Enabled {
		srv.SetGitSync(syncer)
	}
	if cfg.Trash.Enabled {
		bin := trash.New(absVault, cfg.Trash.Retention)
		if removed, err := bin.PruneExpired(); err != nil {
			log.Printf("trash prune: %v", err)
		} else if removed > 0 {
			log.Printf("trash: pruned %d expired entries (retention %s)", removed, cfg.Trash.Retention)
		}
		srv.SetTrash(bin)
		log.Printf("trash: enabled (retention %s)", cfg.Trash.Retention)
	}
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("listening on %s", *addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	if *mcpAddr != "" {
		mcpServer := mcpsrv.New(v, idx, tokenStore)
		mcpServer.SetAuditLog(auditLog)
		mcpServer.SetWriteLimits(cfg.MCP.WritePerMinute, cfg.MCP.MaxNoteBytes)
		mcpServer.SetAllowedUploadRoots(cfg.MCP.AllowedUploadRoots)
		go func() {
			log.Printf("MCP server listening on %s", *mcpAddr)
			if err := mcpServer.ServeSSE(*mcpAddr); err != nil && ctx.Err() == nil {
				log.Fatalf("mcp: %v", err)
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
}
