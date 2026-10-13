package connstr

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	cases := []struct {
		name, id, uri string
		want          map[string]any
	}{
		{"mongo full", "mongodb", "mongodb://orders_app:p%40ss%3Aword@orders.k3x9p.mongodb.net:27017/orders?authSource=admin&replicaSet=rs0&tls=true",
			map[string]any{"host": "orders.k3x9p.mongodb.net", "port": 27017, "username": "orders_app", "password": "p@ss:word", "database": "orders", "authSource": "admin", "replicaSet": "rs0", "tls": true, "srv": false}},
		{"mongo srv", "mongodb", "mongodb+srv://u:pw@cluster0.abc.mongodb.net/shop",
			map[string]any{"host": "cluster0.abc.mongodb.net", "username": "u", "password": "pw", "database": "shop", "srv": true, "tls": true}},
		{"mongo defaults port", "mongodb", "mongodb://db.internal/x", map[string]any{"host": "db.internal", "port": 27017, "database": "x", "srv": false}},
		{"mysql", "mysql", "mysql://app:s3@db.example.com:3307/shop?ssl-mode=verify_identity",
			map[string]any{"host": "db.example.com", "port": 3307, "username": "app", "password": "s3", "database": "shop", "sslMode": "verify-full"}},
		{"mysql default port", "mysql", "mysql://app@h/db", map[string]any{"host": "h", "port": 3306, "username": "app", "database": "db"}},
		{"redis tls with db", "redis", "rediss://:tok%2Fen@cache.example.com:6380/2",
			map[string]any{"host": "cache.example.com", "port": 6380, "password": "tok/en", "tls": true, "db": 2}},
		{"redis default", "redis", "redis://localhost", map[string]any{"host": "localhost", "port": 6379, "tls": false}},
		{"amqp vhost", "rabbitmq", "amqp://guest:guest@mq.example.com/%2F", map[string]any{"host": "mq.example.com", "port": 5672, "username": "guest", "password": "guest", "vhost": "/", "tls": false}},
		{"amqps named vhost", "rabbitmq", "amqps://u:p@mq/orders", map[string]any{"host": "mq", "port": 5671, "username": "u", "password": "p", "vhost": "orders", "tls": true}},
		{"amqp ipv6", "rabbitmq", "amqp://u:p@[2001:db8::1]:5673/", map[string]any{"host": "2001:db8::1", "port": 5673, "username": "u", "password": "p", "vhost": "/", "tls": false}},
	}
	for _, c := range cases {
		got, err := Parse(c.id, c.uri)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %#v\n want %#v", c.name, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct{ name, id, uri, contains string }{
		{"empty", "mongodb", "  ", "Paste"},
		{"wrong scheme", "mongodb", "mysql://u:p@h/db", "mongodb://"},
		{"no host", "redis", "redis://", "no host"},
		{"bad port", "mysql", "mysql://u:p@h:99999/db", "port"},
		{"multi host", "mongodb", "mongodb://u:p@a:1,b:2/db", "Multiple hosts"},
		{"kafka", "kafka", "kafka://x", "bootstrap"},
		{"unknown component", "nginx", "http://x", "does not support"},
		{"bad ssl mode", "mysql", "mysql://u@h/d?ssl-mode=bogus", "ssl mode"},
		{"bad redis db", "redis", "redis://h/abc", "database index"},
		{"control chars", "redis", "redis://h/\x00", "control"},
		{"too long", "redis", "redis://h/" + strings.Repeat("a", MaxLen), "too long"},
	}
	for _, c := range cases {
		_, err := Parse(c.id, c.uri)
		if err == nil || !strings.Contains(err.Error(), c.contains) {
			t.Errorf("%s: got %v, want error containing %q", c.name, err, c.contains)
		}
	}
}

func TestErrorsNeverEchoThePassword(t *testing.T) {
	const pw = "Sup3rS3cretPw"
	inputs := []struct{ id, uri string }{
		{"mongodb", "mongodb://u:" + pw + "@a:1,b:2/db"},
		{"mongodb", "mysql://u:" + pw + "@h/db"},
		{"mysql", "mysql://u:" + pw + "@h:99999/db"},
		{"mysql", "mysql://u:" + pw + "@h/db?ssl-mode=" + pw},
		{"redis", "redis://u:" + pw + "@h/notanumber"},
		{"rabbitmq", "amqp://u:" + pw + "@h:abc/v"},
		{"redis", "redis://:" + pw + "@"},
		{"rabbitmq", "http://u:" + pw + "@h"},
	}
	for _, in := range inputs {
		_, err := Parse(in.id, in.uri)
		if err == nil {
			t.Errorf("%q: expected an error", in.uri)
			continue
		}
		if strings.Contains(err.Error(), pw) {
			t.Errorf("error leaks password: %v", err)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"mongodb://u:pw@h:1/db?x=y", "mongodb+srv://h", "mysql://u@h/d?ssl-mode=required", "redis://:p@h/1", "amqp://u:p@h/%2F",
		"://", "mongodb://[::1", "redis://h:99999999999999999999", "amqp://%zz@h", "mysql://u:p@h/db?%",
	} {
		for _, id := range []string{"mongodb", "mysql", "redis", "rabbitmq", "kafka"} {
			f.Add(id, s)
		}
	}
	const pw = "ZzPwMarker"
	f.Fuzz(func(t *testing.T, id, uri string) {
		out, err := Parse(id, uri)
		if err != nil {
			if strings.Contains(uri, pw) && strings.Contains(err.Error(), pw) {
				t.Fatalf("error leaked marker: %v", err)
			}
			return
		}
		if h, _ := out["host"].(string); h == "" && id != "kafka" {
			t.Fatalf("successful parse without a host: %v", out)
		}
		if p, ok := out["port"].(int); ok && (p < 1 || p > 65535) {
			t.Fatalf("port out of range: %d", p)
		}
	})
}
