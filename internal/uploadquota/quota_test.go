package uploadquota

import (
	"testing"
	"time"
)

func clock(q *Quota, at *time.Time) { q.now = func() time.Time { return *at } }

// Off by default: no quota, or a nil one, never refuses.
func TestQuota_Off(t *testing.T) {
	var nilQuota *Quota
	if r := nilQuota.Reserve("user:a", 1<<40); r != nil {
		t.Errorf("nil quota refused: %v", r)
	}
	if r := New(0, 0).Reserve("user:a", 1<<40); r != nil {
		t.Errorf("zero quota refused: %v", r)
	}
	if New(0, 0).Enabled() || nilQuota.Enabled() {
		t.Error("an unset quota says it is enabled")
	}
}

// Within the window an account may upload up to the quota; the refusal
// says how long until the next upload fits; accounts do not share it; a
// refund gives the bytes back.
func TestQuota_SlidingWindow(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	q := New(100, time.Hour)
	clock(q, &now)
	if r := q.Reserve("user:a", 60); r != nil {
		t.Fatal(r)
	}
	now = now.Add(10 * time.Minute)
	if r := q.Reserve("user:a", 30); r != nil {
		t.Fatal(r)
	}
	r := q.Reserve("user:a", 20)
	if r == nil || r.Used != 90 || r.Wait != 50*time.Minute {
		t.Fatalf("over the quota = %+v, want used 90 and a wait of 50m", r)
	}
	if q.Reserve("user:b", 100) != nil {
		t.Error("another account shares the quota")
	}
	q.Refund("user:a", 30)
	if r := q.Reserve("user:a", 40); r != nil {
		t.Errorf("after a refund of 30: %v", r)
	}
	// The first upload leaves the window after an hour.
	now = now.Add(51 * time.Minute)
	if r := q.Reserve("user:a", 50); r != nil {
		t.Errorf("after the window moved: %v", r)
	}
	if r := q.Reserve("user:a", 101); r == nil || r.Wait != 0 {
		t.Errorf("more than the whole quota = %+v, want a refusal with no wait", r)
	}
}

func TestKey(t *testing.T) {
	if Key("u1", "t1") != "user:u1" || Key("", "t1") != "token:t1" {
		t.Errorf("keys = %q %q", Key("u1", "t1"), Key("", "t1"))
	}
}
