package idle

import (
	"testing"
	"time"
)

func TestTracker(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tr := NewTracker()
	tr.SetClock(func() time.Time { return now })
	if d := tr.IdleFor("ws"); d != 0 {
		t.Fatalf("fresh tracker: %v", d)
	}
	now = now.Add(10 * time.Minute)
	if d := tr.IdleFor("ws"); d != 10*time.Minute {
		t.Fatalf("idle counts from tracker start when no activity seen: %v", d)
	}
	tr.Touch("ws")
	now = now.Add(4 * time.Minute)
	if d := tr.IdleFor("ws"); d != 4*time.Minute {
		t.Fatalf("after touch: %v", d)
	}
	end := tr.Open("ws")
	now = now.Add(time.Hour)
	if d := tr.IdleFor("ws"); d != 0 {
		t.Fatalf("open connection: %v", d)
	}
	end()
	end() // idempotent
	if d := tr.IdleFor("ws"); d != 0 {
		t.Fatalf("just closed: %v", d)
	}
	if d := tr.IdleFor("other"); d != time.Hour+4*time.Minute+0 && d <= 0 {
		t.Fatalf("other workspace: %v", d)
	}
	tr.Forget("ws")
}
