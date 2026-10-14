package terminal

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"
)

type sink struct {
	mu   sync.Mutex
	out  map[string][]byte
	exit map[string]int
}

func newSink() *sink { return &sink{out: map[string][]byte{}, exit: map[string]int{}} }

func (s *sink) emit(method string, p any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch method {
	case "pty.output":
		e := p.(outputEvent)
		b, _ := base64.StdEncoding.DecodeString(e.Data)
		s.out[e.ID] = append(s.out[e.ID], b...)
	case "pty.exit":
		m := p.(map[string]any)
		s.exit[m["id"].(string)] = m["exitCode"].(int)
	}
}

func (s *sink) text(id string) string { s.mu.Lock(); defer s.mu.Unlock(); return string(s.out[id]) }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestShellEchoEnvAndScrollback(t *testing.T) {
	dir := t.TempDir()
	sk := newSink()
	m := NewManager(dir, sk.emit, func(context.Context) (map[string]string, error) {
		return map[string]string{"PROD_ORDERS_DB_URI": "mongodb://x"}, nil
	})
	defer m.CloseAll()
	info, err := m.Open(context.Background(), OpenParams{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	_ = m.Write(info.ID, "echo marker-$PROD_ORDERS_DB_URI; pwd\n")
	waitFor(t, "output", func() bool {
		return strings.Contains(sk.text(info.ID), "marker-mongodb://x") && strings.Contains(sk.text(info.ID), dir)
	})

	// A later attach (new browser tab) replays what happened while detached.
	at, err := m.Attach(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := base64.StdEncoding.DecodeString(at.Scrollback)
	if !strings.Contains(string(snap), "marker-mongodb://x") || at.End != int64(len(sk.out[info.ID])) && at.End < int64(len(snap)) {
		t.Fatalf("scrollback wrong: end=%d snap=%q", at.End, snap)
	}

	if err := m.Resize(info.ID, 120, 40); err != nil {
		t.Fatal(err)
	}
	_ = m.Write(info.ID, "stty size\n")
	waitFor(t, "resized", func() bool { return strings.Contains(sk.text(info.ID), "40 120") })

	_ = m.Write(info.ID, "exit 7\n")
	waitFor(t, "exit event", func() bool { sk.mu.Lock(); defer sk.mu.Unlock(); _, ok := sk.exit[info.ID]; return ok })
	if sk.exit[info.ID] != 7 {
		t.Fatalf("exit code = %d", sk.exit[info.ID])
	}
	if err := m.Write(info.ID, "x"); err == nil {
		t.Fatal("write after exit must fail")
	}
}

func TestScrollbackIsBoundedAndOffsetsMonotonic(t *testing.T) {
	sk := newSink()
	m := NewManager(t.TempDir(), sk.emit, nil)
	defer m.CloseAll()
	info, _ := m.Open(context.Background(), OpenParams{})
	_ = m.Write(info.ID, "head -c 300000 /dev/zero | tr '\\0' 'a'; echo DO\"\"NE\n")
	waitFor(t, "flood", func() bool { return strings.Contains(sk.text(info.ID), "DONE\r\n") })
	at, _ := m.Attach(info.ID)
	snap, _ := base64.StdEncoding.DecodeString(at.Scrollback)
	if len(snap) > ScrollbackBytes || len(snap) < ScrollbackBytes-1 {
		t.Fatalf("scrollback len = %d, want %d", len(snap), ScrollbackBytes)
	}
	if at.End < 300000 {
		t.Fatalf("end offset %d should count every byte", at.End)
	}
}

func TestSessionLimitAndClose(t *testing.T) {
	m := NewManager(t.TempDir(), newSink().emit, nil)
	defer m.CloseAll()
	var ids []string
	for i := 0; i < MaxSessions; i++ {
		in, err := m.Open(context.Background(), OpenParams{})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, in.ID)
	}
	if _, err := m.Open(context.Background(), OpenParams{}); err == nil {
		t.Fatal("limit not enforced")
	}
	if err := m.Close(ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(context.Background(), OpenParams{}); err != nil {
		t.Fatalf("slot not freed: %v", err)
	}
	if len(m.List()) != MaxSessions {
		t.Fatalf("list = %d", len(m.List()))
	}
	if err := m.Close("nope"); err == nil {
		t.Fatal("closing unknown id must fail")
	}
}

func TestBusyReportsForegroundChildren(t *testing.T) {
	sk := newSink()
	m := NewManager(t.TempDir(), sk.emit, nil)
	defer m.CloseAll()
	info, _ := m.Open(context.Background(), OpenParams{Title: "build"})
	time.Sleep(200 * time.Millisecond)
	if b := m.Busy(); b != "" {
		t.Fatalf("idle prompt reported busy: %q", b)
	}
	_ = m.Write(info.ID, "sleep 3\n")
	waitFor(t, "busy", func() bool { return m.Busy() != "" })
	if b := m.Busy(); !strings.Contains(b, "build") {
		t.Fatalf("busy reason: %q", b)
	}
	// Ctrl-C returns the shell to its prompt.
	_ = m.Write(info.ID, "\x03")
	waitFor(t, "idle again", func() bool { return m.Busy() == "" })
}
