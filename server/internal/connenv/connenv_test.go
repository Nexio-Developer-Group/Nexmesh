package connenv

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/connstr"
)

func TestPrefixAndNames(t *testing.T) {
	if got := Prefix("prod-orders-db"); got != "PROD_ORDERS_DB" {
		t.Fatal(got)
	}
	for _, n := range []string{"prod-orders-db", "a", "redis_cache1"} {
		if !ValidName(n) {
			t.Errorf("%s should be valid", n)
		}
	}
	for _, n := range []string{"", "Prod", "1db", "has space", "a.b", strings.Repeat("a", 49), "x;rm"} {
		if ValidName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
	names := Names("mongodb", "prod-orders-db")
	if names[0] != "PROD_ORDERS_DB_URI" || len(names) != 6 {
		t.Fatal(names)
	}
	if k := Names("kafka", "events"); k[0] != "EVENTS_BOOTSTRAP_SERVERS" {
		t.Fatal(k)
	}
}

// URI is the inverse of connstr.Parse for every component with a standard string.
func TestURIRoundTripsThroughParser(t *testing.T) {
	cases := []struct {
		id string
		f  map[string]any
	}{
		{"mongodb", map[string]any{"host": "orders.k3x9p.mongodb.net", "port": 27017, "database": "orders", "username": "orders_app", "password": "p@ss:w/rd#1", "authSource": "admin", "replicaSet": "rs0", "tls": true, "srv": false}},
		{"mongodb", map[string]any{"host": "cluster0.abc.mongodb.net", "database": "shop", "username": "u", "password": "pw", "srv": true, "tls": true}},
		{"mysql", map[string]any{"host": "db.example.com", "port": 3307, "database": "shop", "username": "app", "password": "s3 cr&t", "sslMode": "verify-full"}},
		{"redis", map[string]any{"host": "cache.example.com", "port": 6380, "password": "tok/en?x", "tls": true, "db": 2}},
		{"redis", map[string]any{"host": "localhost", "port": 6379, "tls": false}},
		{"rabbitmq", map[string]any{"host": "mq.example.com", "port": 5672, "username": "guest", "password": "guest", "vhost": "/", "tls": false}},
		{"rabbitmq", map[string]any{"host": "mq", "port": 5671, "username": "u", "password": "p%40", "vhost": "orders/eu", "tls": true}},
	}
	for _, c := range cases {
		uri := URI(c.id, c.f)
		got, err := connstr.Parse(c.id, uri)
		if err != nil {
			t.Errorf("%s %q: %v", c.id, uri, err)
			continue
		}
		for k, want := range c.f {
			if k == "port" && c.f["srv"] == true {
				continue
			}
			gv, ok := got[k]
			if !ok && (want == "" || want == false || want == 0 || want == nil) {
				continue
			}
			if !reflect.DeepEqual(gv, want) {
				t.Errorf("%s %q field %s: parsed %#v, want %#v", c.id, uri, k, gv, want)
			}
		}
	}
}

func TestBuildExposesPredictableVariables(t *testing.T) {
	env := Build("mongodb", "prod-orders-db", map[string]any{"host": "h.example.com", "port": 27017, "database": "orders", "username": "app", "password": "s3cret", "authSource": "admin"})
	var keys []string
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"PROD_ORDERS_DB_DATABASE", "PROD_ORDERS_DB_HOST", "PROD_ORDERS_DB_PASSWORD", "PROD_ORDERS_DB_PORT", "PROD_ORDERS_DB_URI", "PROD_ORDERS_DB_USERNAME"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("%v", keys)
	}
	if !strings.Contains(env["PROD_ORDERS_DB_URI"], "s3cret") || env["PROD_ORDERS_DB_PORT"] != "27017" {
		t.Fatalf("%v", env)
	}
	k := Build("kafka", "events", map[string]any{"bootstrapServers": "a:9092,b:9092", "username": "key", "password": "secret"})
	if k["EVENTS_BOOTSTRAP_SERVERS"] != "a:9092,b:9092" || k["EVENTS_PASSWORD"] != "secret" || len(k) != 3 {
		t.Fatalf("%v", k)
	}
	r := Build("redis", "cache", map[string]any{"host": "h", "port": 6379})
	if _, ok := r["CACHE_PASSWORD"]; ok {
		t.Fatal("empty values must not be exported")
	}
}
