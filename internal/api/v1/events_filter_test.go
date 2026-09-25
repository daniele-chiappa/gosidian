package v1

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/webauth"
)

// readSSEFrame reads one event frame (up to the blank terminator line) with a
// hard deadline so a filtered-out frame that never arrives fails the test
// instead of wedging it.
func readSSEFrame(t *testing.T, reader *bufio.Reader, timeout time.Duration) string {
	t.Helper()
	type res struct {
		frame string
		err   error
	}
	ch := make(chan res, 1)
	go func() {
		var sb strings.Builder
		for {
			line, _, err := reader.ReadLine()
			if err != nil {
				ch <- res{err: err}
				return
			}
			sb.WriteString(string(line))
			sb.WriteByte('\n')
			if len(line) == 0 {
				ch <- res{frame: sb.String()}
				return
			}
		}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("read frame: %v", r.err)
		}
		return r.frame
	case <-time.After(timeout):
		t.Fatal("no frame within deadline")
		return ""
	}
}

// BUG-054: the hub is one shared stream, so frames about projects the
// subscriber may not read must be dropped, not forwarded.
func TestEvents_FiltersFramesByPrincipal(t *testing.T) {
	srv, f := startEventsServer(t)

	// One public project; everything else is private. A guest reads public only.
	if err := f.projects.Set("Open", projects.Flags{Visibility: projects.VisibilityPublic}); err != nil {
		t.Fatal(err)
	}
	guest, err := f.webauth.AddUser("g1", "guest-pass-1234", webauth.RoleGuest)
	if err != nil {
		t.Fatal(err)
	}
	// Accounts created from v2.32 on are restricted (grants only); these
	// tests exercise visibility, so lift it.
	if err := f.webauth.SetRestricted(guest.ID, false); err != nil {
		t.Fatal(err)
	}
	bearer, _, err := f.spaTokens.Create(guest.ID, "test")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/api/v1/events?token="+bearer+"&topics=note,sidebar", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	reader := bufio.NewReader(res.Body)
	// Drop the initial ": connected" comment frame and its terminator.
	for i := 0; i < 2; i++ {
		if _, _, err := reader.ReadLine(); err != nil {
			t.Fatal(err)
		}
	}

	hub := f.router.deps.Events
	waitSubscribers(t, hub, 1)
	go func() {
		// Two frames about a private project, then one the guest may see.
		// Delivery is in publish order, so the first frame the guest reads
		// must be the public one.
		hub.Publish(events.TopicNote, map[string]string{"action": "create", "path": "Secret/x.md"})
		hub.Publish(events.TopicSidebar, map[string]string{"action": "update", "project": "Secret"})
		hub.Publish(events.TopicNote, map[string]string{"action": "create", "path": "Open/y.md"})
	}()

	frame := readSSEFrame(t, reader, 2*time.Second)
	if strings.Contains(frame, "Secret") {
		t.Fatalf("private frame leaked to guest: %q", frame)
	}
	if !strings.Contains(frame, "Open/y.md") {
		t.Fatalf("public frame not delivered: %q", frame)
	}
}

// Frames that name neither a path nor a project cannot be scoped, so only
// the owner receives them; an owner also receives private frames as before.
func TestEvents_OwnerReceivesEverything(t *testing.T) {
	srv, f := startEventsServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/api/v1/events?token="+f.bearer+"&topics=note,insight", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	reader := bufio.NewReader(res.Body)
	for i := 0; i < 2; i++ {
		if _, _, err := reader.ReadLine(); err != nil {
			t.Fatal(err)
		}
	}

	hub := f.router.deps.Events
	waitSubscribers(t, hub, 1)
	go func() {
		hub.Publish(events.TopicInsight, map[string]string{"action": "digest"})
		hub.Publish(events.TopicNote, map[string]string{"action": "create", "path": "Secret/x.md"})
	}()
	if frame := readSSEFrame(t, reader, 2*time.Second); !strings.Contains(frame, "event: insight") {
		t.Fatalf("owner must receive unscoped frames, got %q", frame)
	}
	if frame := readSSEFrame(t, reader, 2*time.Second); !strings.Contains(frame, "Secret/x.md") {
		t.Fatalf("owner must receive private frames, got %q", frame)
	}
}
