// Package netguard enforces the outbound-connection policy for Test
// Connection, health checks and model-endpoint tests (PRD 11.8, 12.5): a
// server-side request forgery guard applied at the socket level.
//
// Every outbound connection goes through Guard.DialContext, which resolves the
// name once, rejects the whole lookup if ANY answer is denied, then dials the
// chosen IP literal, so a second (rebinding) lookup can never happen.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// BlockedError is returned when an address is denied. Its message is safe to
// show to users: it names the policy, never a secret.
type BlockedError struct {
	Addr   string
	Reason string
}

func (e *BlockedError) Error() string { return fmt.Sprintf("blocked address %s: %s", e.Addr, e.Reason) }

var ErrNoAddress = errors.New("host did not resolve to any address")

// Resolver abstracts DNS so tests can simulate rebinding.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
}

type Policy struct {
	// AllowPrivate permits RFC 1918 / ULA / CGNAT ranges (self-hosted
	// deployments that connect to services on their own network). Loopback,
	// link-local and cloud metadata addresses stay blocked regardless.
	AllowPrivate bool
	// AllowLoopback permits 127.0.0.0/8 and ::1. Intended for tests and
	// single-machine development only.
	AllowLoopback bool
}

type Guard struct {
	Policy   Policy
	Resolver Resolver
	Timeout  time.Duration
}

func New(p Policy) *Guard {
	return &Guard{Policy: p, Resolver: net.DefaultResolver, Timeout: 8 * time.Second}
}

var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"), // AWS, Azure, GCP, OpenStack...
	netip.MustParseAddr("169.254.170.2"),   // AWS ECS task metadata
	netip.MustParseAddr("100.100.100.200"), // Alibaba Cloud
	netip.MustParseAddr("192.0.0.192"),     // Oracle Cloud (legacy)
	netip.MustParseAddr("fd00:ec2::254"),   // AWS IMDS over IPv6
}

var metadataHosts = []string{"metadata.google.internal", "metadata", "instance-data"}

var (
	cgnat    = netip.MustParsePrefix("100.64.0.0/10")
	zeroNet  = netip.MustParsePrefix("0.0.0.0/8")
	reserved = netip.MustParsePrefix("240.0.0.0/4")
	benchNet = netip.MustParsePrefix("198.18.0.0/15")
	docNets  = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("2001:db8::/32")}
	nat64    = netip.MustParsePrefix("64:ff9b::/96")
)

// Check applies the policy to one address. IPv4-mapped IPv6 addresses are
// unwrapped first so ::ffff:169.254.169.254 cannot bypass the IPv4 rules.
func (g *Guard) Check(a netip.Addr) error {
	a = a.Unmap()
	// NAT64 embeds an IPv4 address in the low 32 bits: check that address too.
	if nat64.Contains(a) {
		b := a.As16()
		if err := g.Check(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})); err != nil {
			return err
		}
	}
	for _, m := range metadataAddrs {
		if a == m {
			return &BlockedError{a.String(), "cloud metadata endpoint"}
		}
	}
	switch {
	case !a.IsValid():
		return &BlockedError{a.String(), "invalid address"}
	case a.IsUnspecified() || zeroNet.Contains(a):
		return &BlockedError{a.String(), "unspecified address"}
	case a.IsMulticast():
		return &BlockedError{a.String(), "multicast address"}
	case a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast():
		return &BlockedError{a.String(), "link-local address"}
	case a.IsLoopback():
		if g.Policy.AllowLoopback {
			return nil
		}
		return &BlockedError{a.String(), "loopback address (the control plane itself)"}
	case reserved.Contains(a) || benchNet.Contains(a):
		return &BlockedError{a.String(), "reserved address range"}
	case a.IsPrivate() || cgnat.Contains(a):
		if g.Policy.AllowPrivate {
			return nil
		}
		return &BlockedError{a.String(), "private network address (not allowed by this deployment's egress policy)"}
	}
	for _, d := range docNets {
		if d.Contains(a) {
			return &BlockedError{a.String(), "documentation address range"}
		}
	}
	return nil
}

func isMetadataHost(h string) bool {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	for _, m := range metadataHosts {
		if h == m {
			return true
		}
	}
	return false
}

// Lookup resolves host without applying the policy, so Test Connection can
// report "DNS resolved" and "address blocked" as separate stages.
func (g *Guard) Lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return []netip.Addr{a.Unmap()}, nil
	}
	addrs, err := g.Resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, ErrNoAddress
	}
	for i := range addrs {
		addrs[i] = addrs[i].Unmap()
	}
	return addrs, nil
}

// LookupSRV resolves _service._proto.name records (MongoDB +srv).
func (g *Guard) LookupSRV(ctx context.Context, service, proto, name string) ([]*net.SRV, error) {
	_, recs, err := g.Resolver.LookupSRV(ctx, service, proto, name)
	return recs, err
}

// CheckAll applies the policy to every address in a lookup.
func (g *Guard) CheckAll(addrs []netip.Addr) error {
	for _, a := range addrs {
		if err := g.Check(a); err != nil {
			return err
		}
	}
	return nil
}

// Resolve returns the host's addresses after policy checks. If any answer is
// denied the entire lookup is rejected, so a name cannot mix public and
// internal records.
func (g *Guard) Resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if isMetadataHost(host) {
		return nil, &BlockedError{host, "cloud metadata endpoint"}
	}
	if a, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		if err := g.Check(a); err != nil {
			return nil, err
		}
		return []netip.Addr{a.Unmap()}, nil
	}
	addrs, err := g.Resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, ErrNoAddress
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		if err := g.Check(a); err != nil {
			return nil, err
		}
		out = append(out, a.Unmap())
	}
	return out, nil
}

// DialContext is a drop-in net.Dialer.DialContext replacement.
func (g *Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addrs, err := g.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{
		Timeout: g.Timeout,
		// Second layer: re-check the exact address the kernel is about to connect to.
		Control: func(_, address string, _ syscallConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return err
			}
			return g.Check(ap.Addr())
		},
	}
	var lastErr error
	for _, a := range addrs {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

// HTTPClient returns a client whose every connection (including redirects)
// passes the guard, with a bounded redirect chain.
func (g *Guard) HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           g.DialContext,
			TLSHandshakeTimeout:   g.Timeout,
			ResponseHeaderTimeout: timeout,
			DisableKeepAlives:     true,
			Proxy:                 nil, // never route through an ambient proxy
		},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}
