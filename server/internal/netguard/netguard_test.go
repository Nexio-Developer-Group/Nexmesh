package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func blocked(err error) bool { var b *BlockedError; return errors.As(err, &b) }

func TestCheckTable(t *testing.T) {
	strict := New(Policy{})
	lan := New(Policy{AllowPrivate: true})
	dev := New(Policy{AllowPrivate: true, AllowLoopback: true})
	cases := []struct {
		addr                   string
		strictOK, lanOK, devOK bool
	}{
		{"8.8.8.8", true, true, true},
		{"2606:4700:4700::1111", true, true, true},
		{"10.0.0.5", false, true, true},
		{"172.16.3.4", false, true, true},
		{"172.32.0.1", true, true, true}, // just outside 172.16/12
		{"192.168.1.1", false, true, true},
		{"100.64.1.1", false, true, true}, // CGNAT
		{"fd12:3456::1", false, true, true},
		{"127.0.0.1", false, false, true},
		{"::1", false, false, true},
		{"169.254.169.254", false, false, false}, // metadata, never allowed
		{"169.254.1.1", false, false, false},     // link-local
		{"fe80::1", false, false, false},
		{"100.100.100.200", false, false, false},
		{"fd00:ec2::254", false, false, false},
		{"::ffff:169.254.169.254", false, false, false}, // IPv4-mapped bypass attempt
		{"::ffff:10.0.0.1", false, true, true},
		{"::ffff:127.0.0.1", false, false, true},
		{"64:ff9b::a9fe:a9fe", false, false, false}, // NAT64 wrapping 169.254.169.254
		{"0.0.0.0", false, false, false},
		{"::", false, false, false},
		{"224.0.0.1", false, false, false},
		{"240.0.0.1", false, false, false},
		{"203.0.113.9", false, false, false},
	}
	for _, c := range cases {
		a := netip.MustParseAddr(c.addr)
		for name, want := range map[string]struct {
			g  *Guard
			ok bool
		}{"strict": {strict, c.strictOK}, "lan": {lan, c.lanOK}, "dev": {dev, c.devOK}} {
			err := want.g.Check(a)
			if (err == nil) != want.ok {
				t.Errorf("%s %s: err=%v want ok=%v", name, c.addr, err, want.ok)
			}
			if err != nil && !blocked(err) {
				t.Errorf("%s %s: error type %T", name, c.addr, err)
			}
		}
	}
}

type fakeResolver struct {
	answers [][]netip.Addr // answer per successive call
	calls   atomic.Int32
}

func (f *fakeResolver) LookupNetIP(_ context.Context, _, _ string) ([]netip.Addr, error) {
	i := int(f.calls.Add(1)) - 1
	if i >= len(f.answers) {
		i = len(f.answers) - 1
	}
	return f.answers[i], nil
}

func addrs(s ...string) []netip.Addr {
	var out []netip.Addr
	for _, x := range s {
		out = append(out, netip.MustParseAddr(x))
	}
	return out
}

func TestResolveRejectsMixedAnswers(t *testing.T) {
	g := New(Policy{})
	g.Resolver = &fakeResolver{answers: [][]netip.Addr{addrs("8.8.8.8", "10.0.0.7")}}
	if _, err := g.Resolve(context.Background(), "mixed.example"); !blocked(err) {
		t.Fatalf("mixed public+private answer must be rejected, got %v", err)
	}
}

func TestResolveBlocksMetadataHostnames(t *testing.T) {
	g := New(Policy{AllowPrivate: true})
	for _, h := range []string{"metadata.google.internal", "Metadata.Google.Internal.", "metadata", "instance-data"} {
		if _, err := g.Resolve(context.Background(), h); !blocked(err) {
			t.Errorf("%s not blocked: %v", h, err)
		}
	}
	if _, err := g.Resolve(context.Background(), "169.254.169.254"); !blocked(err) {
		t.Error("metadata IP literal not blocked")
	}
	if _, err := g.Resolve(context.Background(), "[::ffff:169.254.169.254]"); !blocked(err) {
		t.Error("bracketed mapped metadata literal not blocked")
	}
}

// DNS rebinding: the name first resolves to a public IP and then to loopback.
// The dialer must resolve exactly once and connect to the vetted IP literal.
func TestDialResolvesOnceDefeatingRebinding(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	rs := &fakeResolver{answers: [][]netip.Addr{addrs("203.0.113.50"), addrs("127.0.0.1")}}
	g := New(Policy{})
	g.Resolver = rs
	g.Timeout = 300 * time.Millisecond
	// First answer is a documentation address (blocked), proving the check runs
	// on the resolved address and that the second answer is never consulted.
	_, err = g.DialContext(context.Background(), "tcp", net.JoinHostPort("rebind.example", itoa(port)))
	if !blocked(err) {
		t.Fatalf("want blocked, got %v", err)
	}
	if rs.calls.Load() != 1 {
		t.Fatalf("resolver called %d times, want exactly 1", rs.calls.Load())
	}

	// With a public first answer the dial must target that literal, not re-resolve.
	rs2 := &fakeResolver{answers: [][]netip.Addr{addrs("198.51.101.1"), addrs("127.0.0.1")}}
	g2 := New(Policy{})
	g2.Resolver = rs2
	g2.Timeout = 200 * time.Millisecond
	_, _ = g2.DialContext(context.Background(), "tcp", net.JoinHostPort("rebind2.example", itoa(port)))
	if rs2.calls.Load() != 1 {
		t.Fatalf("resolver called %d times, want 1", rs2.calls.Load())
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestDialLoopbackPolicy(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Close()
		}
	}()
	if _, err := New(Policy{}).DialContext(context.Background(), "tcp", ln.Addr().String()); !blocked(err) {
		t.Fatalf("loopback must be blocked by default: %v", err)
	}
	c, err := New(Policy{AllowLoopback: true}).DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("loopback should connect when allowed: %v", err)
	}
	c.Close()
}

func TestHTTPClientBlocksRedirectToMetadata(t *testing.T) {
	// The first hop is allowed (loopback test server), the redirect target is not.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()
	g := New(Policy{AllowLoopback: true, AllowPrivate: true})
	_, err := g.HTTPClient(2 * time.Second).Get(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "cloud metadata") {
		t.Fatalf("redirect to metadata must be blocked, got %v", err)
	}
}

func TestHTTPClientBlocksLoopbackByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if _, err := New(Policy{}).HTTPClient(2 * time.Second).Get(srv.URL); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("got %v", err)
	}
}
