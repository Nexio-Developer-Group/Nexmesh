package probe

import (
	"crypto/x509"
	"errors"
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
)

func loadDef(t *testing.T, id string) *catalog.Component {
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := cat.Get(id)
	return d
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name   string
		id     string
		fields map[string]any
		want   string // substring of the error, "" for ok
	}{
		{"redis ok", "redis", map[string]any{"host": "h", "port": 6379}, ""},
		{"redis missing host", "redis", map[string]any{"port": 6379}, "Host is required"},
		{"url as host", "redis", map[string]any{"host": "redis://h", "port": 1}, "just the host name"},
		{"credentials in host", "redis", map[string]any{"host": "user:pw@h", "port": 1}, "just the host name"},
		{"port string", "redis", map[string]any{"host": "h", "port": "6379"}, ""},
		{"port range", "redis", map[string]any{"host": "h", "port": 70000}, "Port must be between"},
		{"mongo needs db and creds", "mongodb", map[string]any{"host": "h", "port": 1}, "Database is required"},
		{"mongo srv needs no port", "mongodb", map[string]any{"host": "h", "srv": true, "database": "d", "username": "u", "password": "p"}, ""},
		{"mysql ssl mode options", "mysql", map[string]any{"host": "h", "port": 1, "database": "d", "username": "u", "password": "p", "sslMode": "x"}, "must be one of"},
		{"kafka list", "kafka", map[string]any{"bootstrapServers": "a:9092, b:9093"}, ""},
		{"kafka bad", "kafka", map[string]any{"bootstrapServers": "a"}, "host:port"},
		{"kafka port", "kafka", map[string]any{"bootstrapServers": "a:0"}, "port must be"},
		{"bad ca", "redis", map[string]any{"host": "h", "port": 1, "tls": true, "caCertificate": "nope"}, "not valid PEM"},
	}
	for _, c := range cases {
		_, e := validate(Params{Def: loadDef(t, c.id), Fields: c.fields})
		switch {
		case c.want == "" && e != nil:
			t.Errorf("%s: unexpected error %v", c.name, e)
		case c.want != "" && (e == nil || !strings.Contains(e.Msg, c.want)):
			t.Errorf("%s: got %v, want %q", c.name, e, c.want)
		}
	}
}

func TestKafkaTargetsAndTLSDecision(t *testing.T) {
	tg, e := validate(Params{Def: loadDef(t, "kafka"), Fields: map[string]any{"bootstrapServers": "a.example:9092,b.example:9093", "securityProtocol": "SASL_SSL"}})
	if e != nil || len(tg.Addrs) != 2 || !tg.UseTLS || tg.TLS == nil || tg.TLS.ServerName != "a.example" {
		t.Fatalf("%+v %v", tg, e)
	}
	tg, _ = validate(Params{Def: loadDef(t, "mysql"), Fields: map[string]any{"host": "h", "port": 3306, "database": "d", "username": "u", "password": "p", "sslMode": "required"}})
	if !tg.UseTLS || tg.RawTLS {
		t.Fatalf("mysql negotiates TLS inside the protocol: %+v", tg)
	}
}

func TestClassifyTLS(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{x509.UnknownAuthorityError{Cert: &x509.Certificate{}}, "not trusted"},
		{x509.HostnameError{Host: "x", Certificate: &x509.Certificate{}}, "not valid for host db.example"},
		{x509.CertificateInvalidError{Cert: &x509.Certificate{}, Reason: x509.Expired}, "expired"},
		{x509.CertificateInvalidError{Cert: &x509.Certificate{}, Reason: x509.CANotAuthorizedForThisName}, "invalid"},
		{errors.New("tls: first record does not look like a TLS handshake"), "did not complete a TLS handshake"},
		{errors.New("EOF"), "did not complete a TLS handshake"},
		{errors.New("something else"), "TLS handshake failed"},
	}
	for _, c := range cases {
		if got := classifyTLS(c.err, "db.example"); !strings.Contains(got.Msg, c.want) {
			t.Errorf("%v -> %q, want %q", c.err, got.Msg, c.want)
		}
	}
}

func TestVersionSupported(t *testing.T) {
	d := loadDef(t, "mongodb") // supports 8.0, 7.0, 6.0
	for v, want := range map[string]bool{"7.0.14": true, "8.0.1": true, "5.0.9": false, "v6.0.19": true, "7": false} {
		if got := versionSupported(d, v); got != want {
			t.Errorf("%s: got %v want %v", v, got, want)
		}
	}
}

func TestEveryConnectableComponentHasAProber(t *testing.T) {
	cat, _ := catalog.Load("../../../catalog")
	for _, c := range cat.All() {
		if c.SupportsMode(catalog.ModeConnect) && !Supported(c.ID) {
			t.Errorf("%s supports Connect but has no prober", c.ID)
		}
	}
}

func TestLocalHostDetection(t *testing.T) {
	for h, want := range map[string]bool{"localhost": true, "127.0.0.1": true, "db": true, "db.internal": true, "cluster0.mongodb.net": false} {
		if isLocalHost(h) != want {
			t.Errorf("%s: want %v", h, want)
		}
	}
}
