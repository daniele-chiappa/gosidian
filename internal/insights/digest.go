// Package insights compiles un-triaged self-improvement insights into a
// scheduled digest note (and optionally emails it). It is the Phase 5 delivery
// channel of the self-improvement loop and is entirely optional: the scheduler
// only runs when [self_improve] enabled=true and digest_interval > 0. See plan
// 20260608-self-improve-feedback-loop.
package insights

import (
	"context"
	"fmt"
	"log/slog"
	"net/smtp"
	"sort"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/clock"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
)

// SMTPConfig holds the optional email delivery settings. When Host is empty,
// email is skipped and only the digest note is written.
type SMTPConfig struct {
	Host     string
	Port     int
	From     string
	Username string
	Password string
}

// DigestConfig is the subset of [self_improve] config the digester needs.
type DigestConfig struct {
	Project     string
	NotifyEmail string
	SMTP        SMTPConfig
}

// Digester compiles pending insights into a dated digest note and, when SMTP
// is configured, emails it.
type Digester struct {
	vault  *vault.Vault
	index  *index.Index
	cfg    DigestConfig
	logger *slog.Logger
}

// New builds a Digester. An empty project defaults to "insights"; a nil
// logger defaults to slog.Default().
func New(v *vault.Vault, idx *index.Index, cfg DigestConfig, logger *slog.Logger) *Digester {
	if cfg.Project == "" {
		cfg.Project = "insights"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Digester{vault: v, index: idx, cfg: cfg, logger: logger}
}

// pending returns the insights with status:pending in the project
// (type:insight ∩ status:pending), sorted by path for determinism.
func (d *Digester) pending() ([]index.NoteRow, error) {
	pend, err := d.index.NotesByTagInProject("status:pending", d.cfg.Project)
	if err != nil {
		return nil, err
	}
	ins, err := d.index.NotesByTagInProject("type:insight", d.cfg.Project)
	if err != nil {
		return nil, err
	}
	isInsight := make(map[string]struct{}, len(ins))
	for _, n := range ins {
		isInsight[n.Path] = struct{}{}
	}
	out := make([]index.NoteRow, 0, len(pend))
	for _, n := range pend {
		if _, ok := isInsight[n.Path]; ok {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Compile gathers the pending insights and writes a dated digest note to the
// project. It returns the digest note path and the insight count. When there
// is nothing pending it writes nothing and returns count 0.
func (d *Digester) Compile(now time.Time) (string, int, error) {
	rows, err := d.pending()
	if err != nil {
		return "", 0, err
	}
	if len(rows) == 0 {
		return "", 0, nil
	}
	date := clock.NoteDate(now)
	path := d.cfg.Project + "/digest-" + date + ".md"
	if err := d.vault.Save(path, []byte(renderDigest(d.cfg.Project, date, rows))); err != nil {
		return "", 0, err
	}
	return path, len(rows), nil
}

// Run compiles a digest and, when SMTP + a recipient are configured, emails
// it. Best-effort: errors are logged, never returned, and an empty pending
// set is a silent no-op.
func (d *Digester) Run(now time.Time) {
	path, count, err := d.Compile(now)
	if err != nil {
		d.logger.Error("insights digest compile failed", "err", err)
		return
	}
	if count == 0 {
		return
	}
	d.logger.Info("insights digest written", "path", path, "count", count)
	if d.cfg.NotifyEmail != "" && d.cfg.SMTP.Host != "" {
		if err := d.email(count, path); err != nil {
			d.logger.Warn("insights digest email failed", "err", err)
		}
	}
}

// Start runs Run() every interval until ctx is cancelled. The first run fires
// after one interval, not immediately. A non-positive interval is a no-op.
func (d *Digester) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			d.Run(now)
		}
	}
}

func (d *Digester) email(count int, notePath string) error {
	s := d.cfg.SMTP
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, s.Host)
	}
	from := s.From
	if from == "" {
		from = s.Username
	}
	subject := fmt.Sprintf("gosidian: %d self-improvement insight(s) to review", count)
	msg := "From: " + from + "\r\n" +
		"To: " + d.cfg.NotifyEmail + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		fmt.Sprintf("%d pending insight(s) await triage in the '%s' project.\nSee the digest note: %s\n", count, d.cfg.Project, notePath)
	return smtp.SendMail(addr, auth, from, []string{d.cfg.NotifyEmail}, []byte(msg))
}

func renderDigest(project, date string, rows []index.NoteRow) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: Insights digest " + date + "\n")
	b.WriteString(fmt.Sprintf("description: Scheduled digest of %d pending self-improvement insight(s).\n", len(rows)))
	b.WriteString("tags: [" + project + ", type:doc, topic:meta]\n")
	b.WriteString("type: doc\n")
	b.WriteString("created: " + date + "\n")
	b.WriteString("---\n\n")
	b.WriteString("# Insights digest — " + date + "\n\n")
	b.WriteString(fmt.Sprintf("%d insight(s) awaiting triage.\n\n", len(rows)))
	for _, r := range rows {
		title := r.Title
		if title == "" {
			title = r.Path
		}
		b.WriteString("- [[" + strings.TrimSuffix(r.Path, ".md") + "|" + title + "]]\n")
	}
	b.WriteString("\nReview with `memory_notes_by_tag(\"status:pending\", project=\"" + project + "\")`, then flip each to `status:done` or `status:archived`.\n")
	return b.String()
}
