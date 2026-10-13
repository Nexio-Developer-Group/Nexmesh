// Package probe implements Test Connection (PRD 11.5): seven ordered stages
// with a specific failure category for each. Probes run in the control plane,
// are read-only, bounded by timeouts, and every socket goes through the
// netguard SSRF guard.
package probe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
)

// Stage identifiers, in execution order.
const (
	StageInput   = "input_validation"
	StageDNS     = "dns"
	StageNetwork = "network"
	StageTLS     = "tls"
	StageAuth    = "authentication"
	StageAuthz   = "authorization"
	StageServer  = "server_details"
)

var StageOrder = []string{StageInput, StageDNS, StageNetwork, StageTLS, StageAuth, StageAuthz, StageServer}

var StageLabels = map[string]string{
	StageInput: "Input validation", StageDNS: "DNS resolution", StageNetwork: "Network reachability",
	StageTLS: "TLS handshake", StageAuth: "Authentication", StageAuthz: "Authorization", StageServer: "Server details",
}

// Params are the inputs of one probe. Fields includes secret values; they live
// only in memory for the duration of the probe.
type Params struct {
	Def     *catalog.Component
	Fields  map[string]any
	Guard   *netguard.Guard
	Scrub   func(string) string // removes registered secret values from text
	Timeout time.Duration       // per-probe budget (default 15s)
}

// Report is called as each stage starts and finishes, so the UI can show live
// progress when it streams results.
type Report func(domain.StageResult)

// StageError is a failed stage with a user-safe, specific message.
type StageError struct{ Msg string }

func (e *StageError) Error() string { return e.Msg }

func stageErr(format string, a ...any) *StageError {
	return &StageError{Msg: fmt.Sprintf(format, a...)}
}

// Target is the network endpoint a probe connects to.
type Target struct {
	Host   string
	Port   int
	Addrs  []string // all candidate host:port endpoints (kafka bootstrap list)
	UseTLS bool
	TLS    *tls.Config
	// RawTLS is false for protocols that upgrade to TLS inside the protocol
	// (MySQL); the generic TLS stage is then skipped and the driver reports it.
	RawTLS bool
}

// Details is what the server-details stage learned.
type Details struct {
	Version string
	Info    string
	Latency time.Duration
}

// session is one authenticated conversation with the remote service.
type session interface {
	Authenticate(ctx context.Context) *StageError
	Authorize(ctx context.Context) *StageError
	Details(ctx context.Context) (Details, *StageError)
	Close()
}

type opener func(t *Target, p Params) (session, *StageError)

var openers = map[string]opener{
	"redis":    openRedis,
	"mysql":    openMySQL,
	"mongodb":  openMongo,
	"rabbitmq": openRabbit,
	"kafka":    openKafka,
}

// Supported reports whether a probe exists for the component.
func Supported(componentID string) bool { _, ok := openers[componentID]; return ok }

// Run executes the stages in order and returns the full result. It never
// returns an error: failures are encoded in the result.
func Run(ctx context.Context, p Params, report Report) *domain.TestResult {
	if p.Timeout <= 0 {
		p.Timeout = 15 * time.Second
	}
	if p.Scrub == nil {
		p.Scrub = func(s string) string { return s }
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	res := &domain.TestResult{At: time.Now().UTC()}
	for _, s := range StageOrder {
		res.Stages = append(res.Stages, domain.StageResult{Stage: s, Status: "pending"})
	}
	set := func(stage, status, msg string) {
		for i := range res.Stages {
			if res.Stages[i].Stage == stage {
				res.Stages[i].Status, res.Stages[i].Message = status, p.Scrub(msg)
				if report != nil {
					report(res.Stages[i])
				}
			}
		}
	}
	failAt := ""
	fail := func(stage string, e *StageError) *domain.TestResult {
		set(stage, "failed", e.Msg)
		failAt = stage
		res.FailedStage, res.Message = stage, p.Scrub(e.Msg)
		skipping := false
		for _, s := range StageOrder {
			if s == failAt {
				skipping = true
				continue
			}
			if skipping {
				set(s, "skipped", "Not run because an earlier check failed.")
			}
		}
		return res
	}
	run := func(stage string) { set(stage, "running", "") }

	// 1. Input validation
	run(StageInput)
	t, e := validate(p)
	if e != nil {
		return fail(StageInput, e)
	}
	set(StageInput, "passed", "All required fields present and well formed.")

	// 2. DNS resolution (and SRV for mongodb+srv)
	run(StageDNS)
	addrs, msg, e := resolve(ctx, p, t)
	if e != nil {
		return fail(StageDNS, e)
	}
	set(StageDNS, "passed", msg)

	// 3. Network reachability: policy check, then a real TCP connect.
	run(StageNetwork)
	if e := reach(ctx, p, t, addrs); e != nil {
		return fail(StageNetwork, e)
	}
	set(StageNetwork, "passed", fmt.Sprintf("Connected to %s:%d.", t.Host, t.Port))

	// 4. TLS handshake
	run(StageTLS)
	switch {
	case !t.UseTLS:
		set(StageTLS, "skipped", "TLS is not enabled for this connection.")
		if !isLocalHost(t.Host) {
			res.Warnings = append(res.Warnings, "TLS is off for a non-local host. Enable TLS so credentials and data are encrypted in transit.")
		}
	case !t.RawTLS:
		set(StageTLS, "passed", "TLS is negotiated during authentication.")
	default:
		if e := handshake(ctx, p, t); e != nil {
			return fail(StageTLS, e)
		}
		set(StageTLS, "passed", "Certificate is valid and trusted.")
	}
	if boolField(p.Fields, "tlsInsecure") && t.UseTLS {
		res.Warnings = append(res.Warnings, "Certificate verification is disabled. The connection is encrypted but the server's identity is not checked.")
	}

	open, ok := openers[p.Def.ID]
	if !ok {
		return fail(StageAuth, stageErr("Test Connection is not available for %s yet.", p.Def.Name))
	}
	sess, oe := open(t, p)
	if oe != nil {
		return fail(StageAuth, oe)
	}
	defer sess.Close()

	// 5. Authentication
	run(StageAuth)
	if e := sess.Authenticate(ctx); e != nil {
		return fail(StageAuth, e)
	}
	set(StageAuth, "passed", "Credentials accepted.")

	// 6. Authorization (minimal read-only operation)
	run(StageAuthz)
	if e := sess.Authorize(ctx); e != nil {
		return fail(StageAuthz, e)
	}
	set(StageAuthz, "passed", "A read-only operation succeeded.")

	// 7. Server details
	run(StageServer)
	d, e := sess.Details(ctx)
	if e != nil {
		return fail(StageServer, e)
	}
	res.ServerVersion, res.LatencyMS = d.Version, d.Latency.Milliseconds()
	detail := strings.TrimSpace(strings.Join([]string{d.Version, d.Info}, " "))
	set(StageServer, "passed", fmt.Sprintf("%s · %d ms", strings.TrimSpace(detail), d.Latency.Milliseconds()))
	if d.Version != "" && !versionSupported(p.Def, d.Version) {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s %s is outside the tested range (%s). It may still work.", p.Def.Name, d.Version, strings.Join(p.Def.Versions.Supported, ", ")))
	}
	res.Passed = true
	return res
}

// versionSupported compares major.minor families with the catalog's list.
func versionSupported(def *catalog.Component, v string) bool {
	fam := func(s string) string {
		parts := strings.SplitN(strings.TrimPrefix(s, "v"), ".", 3)
		if len(parts) >= 2 {
			return parts[0] + "." + parts[1]
		}
		return parts[0]
	}
	f := fam(v)
	for _, s := range def.Versions.Supported {
		if fam(s) == f {
			return true
		}
	}
	return false
}

// ---- helpers

func str(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func intField(m map[string]any, k string, def int) int {
	switch v := m[k].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func boolField(m map[string]any, k string) bool {
	switch v := m[k].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

func isLocalHost(h string) bool {
	h = strings.ToLower(h)
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") || !strings.Contains(h, ".")
}

// validate checks required fields against the catalog and builds the Target.
func validate(p Params) (*Target, *StageError) {
	if p.Def.Connection == nil {
		return nil, stageErr("%s does not support connections.", p.Def.Name)
	}
	for _, f := range p.Def.Connection.Fields {
		v := p.Fields[f.Name]
		empty := v == nil || (f.Type != "boolean" && strings.TrimSpace(fmt.Sprint(v)) == "")
		if f.Required && empty {
			if p.Def.ID == "mongodb" && boolField(p.Fields, "srv") && f.Name == "port" {
				continue
			}
			return nil, stageErr("%s is required.", f.Label)
		}
		if f.Type == "select" && !empty && len(f.Options) > 0 {
			ok := false
			for _, o := range f.Options {
				ok = ok || o == fmt.Sprint(v)
			}
			if !ok {
				return nil, stageErr("%s must be one of: %s.", f.Label, strings.Join(f.Options, ", "))
			}
		}
		if f.Type == "number" && !empty {
			n := intField(p.Fields, f.Name, -1)
			if n < 0 {
				return nil, stageErr("%s must be a number.", f.Label)
			}
			if f.Name == "port" && (n < 1 || n > 65535) {
				return nil, stageErr("Port must be between 1 and 65535.")
			}
		}
	}
	t := &Target{RawTLS: true}
	if p.Def.ID == "kafka" {
		list := str(p.Fields, "bootstrapServers")
		for _, s := range strings.Split(list, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			h, port, err := net.SplitHostPort(s)
			if err != nil || h == "" {
				return nil, stageErr("Bootstrap servers must be a comma-separated list of host:port.")
			}
			if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
				return nil, stageErr("Bootstrap server port must be between 1 and 65535.")
			}
			t.Addrs = append(t.Addrs, s)
		}
		if len(t.Addrs) == 0 {
			return nil, stageErr("Bootstrap servers is required.")
		}
		t.Host, _ = splitHP(t.Addrs[0])
		_, pt := splitHP(t.Addrs[0])
		t.Port = pt
		proto := str(p.Fields, "securityProtocol")
		t.UseTLS = proto == "SSL" || proto == "SASL_SSL"
	} else {
		t.Host = str(p.Fields, "host")
		if strings.Contains(t.Host, "://") || strings.ContainsAny(t.Host, " /@?#") {
			return nil, stageErr("Enter just the host name (no scheme, path or credentials).")
		}
		if len(t.Host) > 253 {
			return nil, stageErr("The host name is too long.")
		}
		t.Port = intField(p.Fields, "port", p.Def.Connection.DefaultPort)
		switch p.Def.ID {
		case "mongodb":
			t.UseTLS = boolField(p.Fields, "tls") || boolField(p.Fields, "srv")
		case "redis", "rabbitmq":
			t.UseTLS = boolField(p.Fields, "tls")
		case "mysql":
			mode := str(p.Fields, "sslMode")
			t.UseTLS = mode != "" && mode != "disabled"
			t.RawTLS = false
		}
	}
	if t.UseTLS {
		cfg, e := tlsConfig(p, t.Host)
		if e != nil {
			return nil, e
		}
		t.TLS = cfg
	}
	return t, nil
}

func splitHP(s string) (string, int) {
	h, p, _ := net.SplitHostPort(s)
	n, _ := strconv.Atoi(p)
	return h, n
}

func tlsConfig(p Params, host string) (*tls.Config, *StageError) {
	cfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	// An IP literal in ServerName is verified against IP SANs and sends no SNI.
	if ca := str(p.Fields, "caCertificate"); ca != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(ca)) {
			return nil, stageErr("The CA certificate is not valid PEM.")
		}
		cfg.RootCAs = pool
	}
	if boolField(p.Fields, "tlsInsecure") {
		cfg.InsecureSkipVerify = true //nolint:gosec // explicit, visible user opt-out (PRD 11.8)
	}
	return cfg, nil
}

// resolve performs the DNS stage. For mongodb+srv it also looks up the SRV
// record and retargets the probe to the first advertised host.
func resolve(ctx context.Context, p Params, t *Target) ([]string, string, *StageError) {
	if p.Def.ID == "mongodb" && boolField(p.Fields, "srv") {
		recs, err := p.Guard.LookupSRV(ctx, "mongodb", "tcp", t.Host)
		if err != nil || len(recs) == 0 {
			return nil, "", stageErr("No SRV record found for %s. Check the cluster host name.", t.Host)
		}
		t.Host = strings.TrimSuffix(recs[0].Target, ".")
		t.Port = int(recs[0].Port)
		if t.TLS != nil && net.ParseIP(t.Host) == nil {
			t.TLS.ServerName = t.Host
		}
	}
	addrs, err := p.Guard.Lookup(ctx, t.Host)
	if err != nil {
		var dns *net.DNSError
		if errors.As(err, &dns) && dns.IsNotFound {
			return nil, "", stageErr("The host %s could not be found. Check the spelling.", t.Host)
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, "", stageErr("DNS lookup for %s timed out.", t.Host)
		}
		return nil, "", stageErr("DNS lookup for %s failed.", t.Host)
	}
	out := make([]string, len(addrs))
	for i, a := range addrs {
		out[i] = a.String()
	}
	return out, fmt.Sprintf("%s resolved to %s.", t.Host, strings.Join(out, ", ")), nil
}

// reach applies the egress policy and opens a TCP connection.
func reach(ctx context.Context, p Params, t *Target, resolved []string) *StageError {
	if _, err := p.Guard.Resolve(ctx, t.Host); err != nil {
		var be *netguard.BlockedError
		if errors.As(err, &be) {
			return stageErr("Connections to %s are not allowed: %s.", be.Addr, be.Reason)
		}
	}
	targets := []string{net.JoinHostPort(t.Host, strconv.Itoa(t.Port))}
	if len(t.Addrs) > 0 {
		targets = t.Addrs
	}
	var last error
	for _, addr := range targets {
		dctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		c, err := p.Guard.DialContext(dctx, "tcp", addr)
		cancel()
		if err == nil {
			_ = c.Close()
			if len(t.Addrs) > 0 {
				t.Host, t.Port = splitHP2(addr)
			}
			return nil
		}
		last = err
	}
	var be *netguard.BlockedError
	var ne net.Error
	switch {
	case errors.As(last, &be):
		return stageErr("Connections to %s are not allowed: %s.", be.Addr, be.Reason)
	case errors.Is(last, context.DeadlineExceeded) || (errors.As(last, &ne) && ne.Timeout()):
		return stageErr("Timed out connecting to %s:%d. A firewall or IP allow-list may be blocking this host.", t.Host, t.Port)
	case strings.Contains(fmt.Sprint(last), "refused"):
		return stageErr("Connection refused by %s:%d. Check the port and that the service is running.", t.Host, t.Port)
	}
	return stageErr("Could not connect to %s:%d.", t.Host, t.Port)
}

func splitHP2(s string) (string, int) { return splitHP(s) }

// handshake performs a standalone TLS handshake to classify certificate problems.
func handshake(ctx context.Context, p Params, t *Target) *StageError {
	dctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	conn, err := p.Guard.DialContext(dctx, "tcp", net.JoinHostPort(t.Host, strconv.Itoa(t.Port)))
	if err != nil {
		return stageErr("Could not reconnect for the TLS handshake.")
	}
	defer conn.Close()
	tc := tls.Client(conn, t.TLS)
	if err := tc.HandshakeContext(dctx); err != nil {
		return classifyTLS(err, t.Host)
	}
	return nil
}

func classifyTLS(err error, host string) *StageError {
	var ua x509.UnknownAuthorityError
	var he x509.HostnameError
	var ci x509.CertificateInvalidError
	msg := err.Error()
	switch {
	case errors.As(err, &ua):
		return stageErr("The server certificate is not trusted (unknown certificate authority). Add its CA certificate under Advanced.")
	case errors.As(err, &he):
		return stageErr("The certificate is not valid for host %s.", host)
	case errors.As(err, &ci):
		if ci.Reason == x509.Expired {
			return stageErr("The server certificate has expired or is not yet valid.")
		}
		return stageErr("The server certificate is invalid.")
	case strings.Contains(msg, "first record does not look like a TLS handshake"), strings.Contains(msg, "EOF"):
		return stageErr("The server did not complete a TLS handshake on this port. TLS may not be enabled there.")
	case strings.Contains(msg, "certificate"):
		return stageErr("The server certificate could not be verified.")
	}
	return stageErr("TLS handshake failed.")
}

type x509Cert = x509.Certificate

// verifyChain verifies a presented chain against roots (system roots when nil).
func verifyChain(raw [][]byte, roots *x509.CertPool) error {
	if len(raw) == 0 {
		return errors.New("no certificate presented")
	}
	var certs []*x509.Certificate
	for _, r := range raw {
		c, err := x509.ParseCertificate(r)
		if err != nil {
			return err
		}
		certs = append(certs, c)
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter})
	return err
}
