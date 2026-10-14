package connimport

import (
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
)

func imp(t *testing.T) *Importer {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	return New(cat)
}

func byID(res *Result, id string) *Candidate {
	for i := range res.Candidates {
		if res.Candidates[i].CatalogID == id {
			return &res.Candidates[i]
		}
	}
	return nil
}

func TestConnectionStringIsDetectedByScheme(t *testing.T) {
	im := imp(t)
	for uri, want := range map[string]string{
		"mongodb+srv://orders_app:pw%26x@orders.k3x9p.mongodb.net/orders": "mongodb",
		"mysql://app:secret@db.example.com:3306/shop":                     "mysql",
		"mariadb://app:secret@db.example.com/shop":                        "mysql",
		"rediss://:tok@cache.example.com:6380/2":                          "redis",
		"amqps://u:p@mq.example.com/vh":                                   "rabbitmq",
	} {
		res, err := im.Import(uri, "")
		if err != nil || res.Format != "connection-string" || len(res.Candidates) != 1 || res.Candidates[0].CatalogID != want {
			t.Fatalf("%s: %v %+v", uri, err, res)
		}
	}
	res, _ := im.Import("mongodb+srv://orders_app:pw%26x@orders.k3x9p.mongodb.net/orders", "")
	c := res.Candidates[0]
	if c.Fields["host"] != "orders.k3x9p.mongodb.net" || c.Fields["password"] != "pw&x" || c.Fields["database"] != "orders" || len(c.Secrets) != 1 || c.Secrets[0] != "password" || len(c.Missing) != 0 {
		t.Fatalf("fields: %+v", c)
	}
}

func TestEnvFileUrlsAndDiscreteVariables(t *testing.T) {
	im := imp(t)
	env := `# app config
DATABASE_URL=mysql://app:s3cret@db.internal:3306/shop
CACHE_URL="redis://:cachepw@cache.internal:6379/1"
MONGO_HOST=mongo.internal
MONGO_PORT=27018
MONGO_USER=orders
MONGO_PASSWORD=mongopw
MONGO_DB=orders
RABBITMQ_HOST=mq.internal
RABBITMQ_USER=guest
KAFKA_BROKERS=PLAINTEXT://k1:9092
LOG_LEVEL=debug
BROKEN LINE
`
	res, err := im.Import(env, "")
	if err != nil || res.Format != "env" {
		t.Fatalf("%v %+v", err, res)
	}
	if len(res.Candidates) != 5 {
		t.Fatalf("want 5 candidates, got %d: %+v", len(res.Candidates), res.Candidates)
	}
	my := byID(res, "mysql")
	if my.Source != "DATABASE_URL" || my.Name != "database" || my.Fields["host"] != "db.internal" || my.Fields["username"] != "app" {
		t.Fatalf("mysql: %+v", my)
	}
	mg := byID(res, "mongodb")
	if mg.Fields["port"] != 27018 || mg.Fields["database"] != "orders" || mg.Fields["password"] != "mongopw" || len(mg.Missing) != 0 {
		t.Fatalf("mongo: %+v", mg)
	}
	rb := byID(res, "rabbitmq")
	if len(rb.Missing) != 1 || rb.Missing[0] != "password" {
		t.Fatalf("rabbitmq must report the missing password: %+v", rb)
	}
	if k := byID(res, "kafka"); k == nil || k.Fields["bootstrapServers"] != "k1:9092" {
		t.Fatalf("kafka: %+v", k)
	}
	// Lines it could not read are reported, not silently dropped.
	found := false
	for _, s := range res.Skipped {
		found = found || strings.Contains(s.Source, "line")
	}
	if !found {
		t.Fatalf("skipped: %+v", res.Skipped)
	}
}

const compose = `
services:
  db:
    image: mongo:7
    ports: ["27018:27017"]
    environment:
      MONGO_INITDB_ROOT_USERNAME: root
      MONGO_INITDB_ROOT_PASSWORD: ${MONGO_PW:-devpassword}
      MONGO_INITDB_DATABASE: orders
  sql:
    image: bitnami/mariadb:11
    environment:
      - MYSQL_USER=app
      - MYSQL_PASSWORD=${SQL_PW}
      - MYSQL_DATABASE=shop
  cache:
    image: redis:7
    command: redis-server --requirepass hunter2
    ports: ["6390:6379"]
  mq:
    image: rabbitmq:3-management
    environment:
      RABBITMQ_DEFAULT_USER: guest
      RABBITMQ_DEFAULT_PASS: guest
  web:
    image: nginx:alpine
  bus:
    image: confluentinc/cp-kafka:7.6.0
`

func TestComposeServicesAreMappedWithHonestGaps(t *testing.T) {
	im := imp(t)
	res, err := im.Import(compose, "")
	if err != nil || res.Format != "compose" {
		t.Fatalf("%v %+v", err, res)
	}
	mg := byID(res, "mongodb")
	if mg.Source != "db" || mg.Fields["username"] != "root" || mg.Fields["password"] != "devpassword" || mg.Fields["port"] != 27018 || mg.Fields["database"] != "orders" {
		t.Fatalf("mongo: %+v", mg)
	}
	if len(mg.Missing) != 1 || mg.Missing[0] != "host" {
		t.Fatalf("host cannot be known from compose: %+v", mg.Missing)
	}
	my := byID(res, "mysql")
	if my.Fields["username"] != "app" || my.Fields["database"] != "shop" {
		t.Fatalf("mysql: %+v", my)
	}
	if _, has := my.Fields["password"]; has {
		t.Fatalf("an unresolved ${SQL_PW} must not become a value: %+v", my.Fields)
	}
	if r := byID(res, "redis"); r.Fields["password"] != "hunter2" || r.Fields["port"] != 6390 {
		t.Fatalf("redis: %+v", r)
	}
	if k := byID(res, "kafka"); k == nil || len(k.Missing) == 0 {
		t.Fatalf("kafka: %+v", k)
	}
	skipped := false
	for _, s := range res.Skipped {
		skipped = skipped || (s.Source == "web" && strings.Contains(s.Reason, "Not a supported"))
	}
	if !skipped {
		t.Fatalf("unsupported services must be reported: %+v", res.Skipped)
	}
	// Filtering to one component type.
	only, err := im.Import(compose, "redis")
	if err != nil || len(only.Candidates) != 1 || only.Candidates[0].CatalogID != "redis" {
		t.Fatalf("filter: %v %+v", err, only)
	}
}

func TestUnrecognisedInputGivesAClearMessage(t *testing.T) {
	im := imp(t)
	for name, in := range map[string]string{
		"empty":        "  ",
		"prose":        "hello world",
		"other scheme": "postgres://u:p@h/db",
		"other env":    "FOO=bar\nBAZ=qux",
		"wrong type":   "redis://h:6379",
	} {
		cat := ""
		if name == "wrong type" {
			cat = "mongodb"
		}
		_, err := im.Import(in, cat)
		var ie *Error
		if err == nil || !asErr(err, &ie) {
			t.Errorf("%s: want a friendly error, got %v", name, err)
			continue
		}
		if name != "empty" && !strings.Contains(ie.Msg, "Supported:") {
			t.Errorf("%s: message should name what is supported: %q", name, ie.Msg)
		}
	}
	if _, err := im.Import(strings.Repeat("A=1\n", MaxInput), ""); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("size limit: %v", err)
	}
}

func asErr(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}

func TestErrorsNeverEchoPasswords(t *testing.T) {
	im := imp(t)
	res, err := im.Import("mongodb://user:TopSecretPw@host1,host2/db", "")
	text := ""
	if err != nil {
		text = err.Error()
	}
	if res != nil {
		for _, s := range res.Skipped {
			text += s.Reason
		}
	}
	if strings.Contains(text, "TopSecretPw") {
		t.Fatalf("password echoed: %q", text)
	}
}
