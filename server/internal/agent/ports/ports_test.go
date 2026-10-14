package ports

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const header = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

func fixture(t *testing.T, tcp, tcp6 string) *Scanner {
	t.Helper()
	d := t.TempDir()
	_ = os.MkdirAll(filepath.Join(d, "net"), 0o755)
	_ = os.WriteFile(filepath.Join(d, "net/tcp"), []byte(header+tcp), 0o644)
	_ = os.WriteFile(filepath.Join(d, "net/tcp6"), []byte(header+tcp6), 0o644)
	// process 42 owns socket inode 1111 (port 3000)
	_ = os.MkdirAll(filepath.Join(d, "42/fd"), 0o755)
	_ = os.Symlink("socket:[1111]", filepath.Join(d, "42/fd/7"))
	_ = os.WriteFile(filepath.Join(d, "42/comm"), []byte("node\n"), 0o644)
	return &Scanner{Proc: d, Ignore: map[int]bool{9999: true}}
}

func TestParsesListenSocketsOnly(t *testing.T) {
	s := fixture(t,
		"   0: 00000000:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1111 1 0 0 100 0 0 10 0\n"+ // :3000 any
			"   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 2222 1 0 0 100 0 0 10 0\n"+ // :8080 loopback
			"   2: 0100007F:A000 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 3333 1 0 0 100 0 0 10 0\n"+ // established
			"   3: 00000000:270F 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 4444 1 0 0 100 0 0 10 0\n", // :9999 ignored
		"   0: 00000000000000000000000001000000:1F90 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 5555 1 0 0 100 0 0 10 0\n"+ // [::1]:8080
			"   1: 00000000000000000000000000000000:1388 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 6666 1 0 0 100 0 0 10 0\n") // [::]:5000
	got := s.Scan()
	want := []Port{{Port: 3000, Process: "node", PID: 42}, {Port: 5000}, {Port: 8080, Local: true}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("port %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestDualStackAnyInterfaceWins(t *testing.T) {
	s := fixture(t,
		"   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1 1 0 0 100 0 0 10 0\n",
		"   0: 00000000000000000000000000000000:1F90 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 2 1 0 0 100 0 0 10 0\n")
	if g := s.Scan(); len(g) != 1 || g[0].Local {
		t.Fatalf("%+v", g)
	}
}

func TestRealListener(t *testing.T) {
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skip("no /proc")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	found := false
	for _, p := range NewScanner().Scan() {
		if p.Port == port && p.Local {
			found = true
		}
	}
	if !found {
		t.Fatalf("listener on %d not detected", port)
	}
}

func TestWatchEmitsOnChangeOnly(t *testing.T) {
	// A fixture /proc keeps this deterministic: other tests open real listeners concurrently.
	s := fixture(t, "", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan []Port, 10)
	go s.Watch(ctx, 30*time.Millisecond, func(p []Port) { got <- p })
	if first := <-got; len(first) != 0 {
		t.Fatalf("initial: %+v", first)
	}
	select {
	case <-got:
		t.Fatal("emitted without a change")
	case <-time.After(250 * time.Millisecond):
	}
	line := "   0: 00000000:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1111 1 0 0 100 0 0 10 0\n"
	if err := os.WriteFile(filepath.Join(s.Proc, "net/tcp"), []byte(header+line), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-got:
		if len(p) != 1 || p[0].Port != 3000 || p[0].Process != "node" {
			t.Fatalf("change: %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no emit after the listener appeared")
	}
	select {
	case <-got:
		t.Fatal("re-emitted an unchanged list")
	case <-time.After(250 * time.Millisecond):
	}
}
