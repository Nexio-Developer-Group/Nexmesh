package probe_test

// Integration tests: run against real Redis, MariaDB, MongoDB, RabbitMQ and
// Kafka. Start them with scripts/it/start-services.sh and enable with
// NEXMESH_IT=1 (CI does this). Without it these tests are skipped.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
	"github.com/nexio-developer-group/nexmesh/server/internal/probe"
)

func itEnv(t *testing.T) {
	if os.Getenv("NEXMESH_IT") != "1" {
		t.Skip("set NEXMESH_IT=1 and start scripts/it/start-services.sh to run integration tests")
	}
}

func envInt(k string) int {
	var n int
	fmt.Sscan(os.Getenv(k), &n)
	return n
}

var (
	pass     = func() string { return os.Getenv("NX_IT_PASS") }
	certsDir = func() string { return filepath.Join(os.Getenv("NX_IT_DIR"), "certs") }
)

func readFile(t *testing.T, name string) string {
	b, err := os.ReadFile(filepath.Join(certsDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func def(t *testing.T, id string) *catalog.Component {
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := cat.Get(id)
	return d
}

func run(t *testing.T, id string, fields map[string]any, pol netguard.Policy) *domain.TestResult {
	t.Helper()
	g := netguard.New(pol)
	var secrets []string
	for k, v := range fields {
		if strings.Contains(strings.ToLower(k), "password") && fmt.Sprint(v) != "" {
			secrets = append(secrets, fmt.Sprint(v))
		}
	}
	res := probe.Run(context.Background(), probe.Params{Def: def(t, id), Fields: fields, Guard: g, Timeout: 20 * time.Second,
		Scrub: func(s string) string {
			for _, x := range secrets {
				s = strings.ReplaceAll(s, x, "[redacted]")
			}
			return s
		}}, nil)
	// A result must never contain secret values, whatever happened.
	raw, _ := json.Marshal(res)
	for _, x := range secrets {
		if x != "" && strings.Contains(string(raw), x) {
			t.Fatalf("result leaks a secret: %s", raw)
		}
	}
	return res
}

var lo = netguard.Policy{AllowLoopback: true}

func expectPass(t *testing.T, r *domain.TestResult) {
	t.Helper()
	if !r.Passed {
		t.Fatalf("expected pass, failed at %s: %s\nstages: %+v", r.FailedStage, r.Message, r.Stages)
	}
	for _, s := range r.Stages {
		if s.Status != "passed" && s.Status != "skipped" {
			t.Fatalf("stage %s is %s", s.Stage, s.Status)
		}
	}
}

func expectFail(t *testing.T, r *domain.TestResult, stage, contains string) {
	t.Helper()
	if r.Passed || r.FailedStage != stage || !strings.Contains(r.Message, contains) {
		t.Fatalf("want failure at %s containing %q; got passed=%v stage=%s msg=%q", stage, contains, r.Passed, r.FailedStage, r.Message)
	}
	// Stages after the failure are skipped, the ones before passed.
	seen := false
	for _, s := range r.Stages {
		switch {
		case s.Stage == stage:
			seen = true
			if s.Status != "failed" {
				t.Fatalf("stage %s status %s", s.Stage, s.Status)
			}
		case seen && s.Status != "skipped":
			t.Fatalf("stage %s after failure is %s", s.Stage, s.Status)
		case !seen && s.Status != "passed" && s.Status != "skipped":
			t.Fatalf("stage %s before failure is %s", s.Stage, s.Status)
		}
	}
}

// ---- generic stages (use Redis as the carrier)

func redisFields(over map[string]any) map[string]any {
	f := map[string]any{"host": "127.0.0.1", "port": envInt("NX_IT_REDIS_PORT"), "password": pass(), "db": 0}
	for k, v := range over {
		f[k] = v
	}
	return f
}

func TestRedisPass(t *testing.T) {
	itEnv(t)
	r := run(t, "redis", redisFields(nil), lo)
	expectPass(t, r)
	if !strings.HasPrefix(r.ServerVersion, "7.") {
		t.Fatalf("version %q", r.ServerVersion)
	}
	if len(r.Stages) != 7 {
		t.Fatalf("stages: %d", len(r.Stages))
	}
}

func TestRedisFailures(t *testing.T) {
	itEnv(t)
	expectFail(t, run(t, "redis", redisFields(map[string]any{"password": "wrong-password"}), lo), probe.StageAuth, "authentication failed")
	expectFail(t, run(t, "redis", redisFields(map[string]any{"username": "limited"}), lo), probe.StageAuthz, "may not run INFO")
	expectFail(t, run(t, "redis", redisFields(map[string]any{"port": 1}), lo), probe.StageNetwork, "refused")
	expectFail(t, run(t, "redis", redisFields(map[string]any{"host": "no-such-host.invalid"}), lo), probe.StageDNS, "could not be found")
	expectFail(t, run(t, "redis", redisFields(map[string]any{"host": ""}), lo), probe.StageInput, "Host is required")
	expectFail(t, run(t, "redis", redisFields(map[string]any{"host": "redis://x"}), lo), probe.StageInput, "just the host name")
	expectFail(t, run(t, "redis", redisFields(map[string]any{"port": 70000}), lo), probe.StageInput, "Port must be between")
}

func TestEgressPolicyBlocksLoopbackAndMetadata(t *testing.T) {
	itEnv(t)
	expectFail(t, run(t, "redis", redisFields(nil), netguard.Policy{}), probe.StageNetwork, "loopback")
	expectFail(t, run(t, "redis", redisFields(map[string]any{"host": "169.254.169.254"}), netguard.Policy{AllowPrivate: true, AllowLoopback: true}), probe.StageNetwork, "metadata")
	expectFail(t, run(t, "redis", redisFields(map[string]any{"host": "10.1.2.3"}), netguard.Policy{}), probe.StageNetwork, "private network")
}

func TestRedisTLS(t *testing.T) {
	itEnv(t)
	tlsPort := envInt("NX_IT_REDIS_TLS_PORT")
	ca := readFile(t, "ca.pem")
	host := map[string]any{"host": "localhost", "port": tlsPort, "tls": true}
	// Trusted CA: passes the TLS stage.
	r := run(t, "redis", redisFields(merge(host, map[string]any{"caCertificate": ca})), lo)
	expectPass(t, r)
	if tl := stage(r, probe.StageTLS); tl.Status != "passed" {
		t.Fatalf("tls stage %+v", tl)
	}
	// System roots do not trust the test CA.
	expectFail(t, run(t, "redis", redisFields(host), lo), probe.StageTLS, "not trusted")
	// Wrong CA.
	expectFail(t, run(t, "redis", redisFields(merge(host, map[string]any{"caCertificate": readFile(t, "other-ca.pem")})), lo), probe.StageTLS, "not trusted")
	// Hostname mismatch: certificate is for localhost / 127.0.0.1 only.
	expectFail(t, run(t, "redis", redisFields(merge(host, map[string]any{"host": "127.0.0.2", "caCertificate": ca})), lo), probe.StageTLS, "not valid for host")
	// Explicit opt-out passes and warns.
	r = run(t, "redis", redisFields(merge(host, map[string]any{"tlsInsecure": true})), lo)
	expectPass(t, r)
	if !hasWarning(r, "Certificate verification is disabled") {
		t.Fatalf("missing insecure warning: %v", r.Warnings)
	}
	// TLS requested against a plaintext port.
	expectFail(t, run(t, "redis", redisFields(map[string]any{"tls": true, "caCertificate": ca}), lo), probe.StageTLS, "TLS")
	// Not valid PEM.
	expectFail(t, run(t, "redis", redisFields(merge(host, map[string]any{"caCertificate": "garbage"})), lo), probe.StageInput, "not valid PEM")
}

func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func stage(r *domain.TestResult, name string) domain.StageResult {
	for _, s := range r.Stages {
		if s.Stage == name {
			return s
		}
	}
	return domain.StageResult{}
}

func hasWarning(r *domain.TestResult, sub string) bool {
	for _, w := range r.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestReportCallbackStreamsEveryStage(t *testing.T) {
	itEnv(t)
	var events []string
	probe.Run(context.Background(), probe.Params{Def: def(t, "redis"), Fields: redisFields(nil), Guard: netguard.New(lo)}, func(s domain.StageResult) {
		events = append(events, s.Stage+":"+s.Status)
	})
	if events[0] != "input_validation:running" || events[len(events)-1] != "server_details:passed" || len(events) != 14 {
		t.Fatalf("%v", events)
	}
}

// ---- MySQL (MariaDB speaks the MySQL protocol)

func mysqlFields(over map[string]any) map[string]any {
	f := map[string]any{"host": "127.0.0.1", "port": envInt("NX_IT_MYSQL_PORT"), "database": "orders", "username": "app", "password": pass(), "sslMode": "disabled"}
	for k, v := range over {
		f[k] = v
	}
	return f
}

func TestMySQL(t *testing.T) {
	itEnv(t)
	r := run(t, "mysql", mysqlFields(nil), lo)
	expectPass(t, r)
	if !strings.HasPrefix(r.ServerVersion, "10.") {
		t.Fatalf("version %q", r.ServerVersion)
	}
	if !hasWarning(r, "outside the tested range") {
		t.Fatalf("MariaDB 10.x is outside MySQL's tested range: %v", r.Warnings)
	}
	expectFail(t, run(t, "mysql", mysqlFields(map[string]any{"password": "nope"}), lo), probe.StageAuth, "authentication failed")
	expectFail(t, run(t, "mysql", mysqlFields(map[string]any{"username": "nodb", "database": "secret_db"}), lo), probe.StageAuth, "not available to this account")
	expectFail(t, run(t, "mysql", mysqlFields(map[string]any{"database": "does_not_exist"}), lo), probe.StageAuth, "not available")
	expectFail(t, run(t, "mysql", mysqlFields(map[string]any{"port": 2}), lo), probe.StageNetwork, "refused")
	expectFail(t, run(t, "mysql", mysqlFields(map[string]any{"sslMode": "required"}), lo), probe.StageAuth, "TLS")
	expectFail(t, run(t, "mysql", mysqlFields(map[string]any{"sslMode": "bogus"}), lo), probe.StageInput, "must be one of")
}

// ---- MongoDB

func mongoBootstrap(t *testing.T) {
	port := envInt("NX_IT_MONGO_PORT")
	uri := fmt.Sprintf("mongodb://127.0.0.1:%d/?directConnect=true", port)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Disconnect(ctx)
	admin := c.Database("admin")
	// Localhost exception: the first user can be created without credentials.
	err = admin.RunCommand(ctx, bson.D{{Key: "createUser", Value: "root"}, {Key: "pwd", Value: pass()}, {Key: "roles", Value: bson.A{"root"}}}).Err()
	if err != nil && !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "requires authentication") {
		t.Fatalf("bootstrap root: %v", err)
	}
	ac, err := mongo.Connect(options.Client().ApplyURI(uri).SetAuth(options.Credential{Username: "root", Password: pass(), AuthSource: "admin"}))
	if err != nil {
		t.Fatal(err)
	}
	defer ac.Disconnect(ctx)
	for _, u := range []struct {
		name, db, role, roleDB string
	}{{"app", "orders", "readWrite", "orders"}, {"limited", "orders", "read", "other_db"}} {
		cmd := bson.D{{Key: "createUser", Value: u.name}, {Key: "pwd", Value: pass()}, {Key: "roles", Value: bson.A{bson.D{{Key: "role", Value: u.role}, {Key: "db", Value: u.roleDB}}}}}
		if err := ac.Database(u.db).RunCommand(ctx, cmd).Err(); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("create %s: %v", u.name, err)
		}
	}
	_, _ = ac.Database("orders").Collection("items").InsertOne(ctx, bson.D{{Key: "x", Value: 1}})
}

func mongoFields(over map[string]any) map[string]any {
	f := map[string]any{"host": "127.0.0.1", "port": envInt("NX_IT_MONGO_PORT"), "database": "orders", "username": "app", "password": pass(), "authSource": "orders"}
	for k, v := range over {
		f[k] = v
	}
	return f
}

func TestMongo(t *testing.T) {
	itEnv(t)
	mongoBootstrap(t)
	r := run(t, "mongodb", mongoFields(nil), lo)
	expectPass(t, r)
	if r.ServerVersion != "7.0.14" {
		t.Fatalf("version %q", r.ServerVersion)
	}
	expectFail(t, run(t, "mongodb", mongoFields(map[string]any{"password": "wrong"}), lo), probe.StageAuth, "authentication failed")
	expectFail(t, run(t, "mongodb", mongoFields(map[string]any{"username": "limited"}), lo), probe.StageAuthz, "may not read database")
	expectFail(t, run(t, "mongodb", mongoFields(map[string]any{"port": 3}), lo), probe.StageNetwork, "refused")
	expectFail(t, run(t, "mongodb", mongoFields(map[string]any{"tls": true}), lo), probe.StageTLS, "TLS")
	expectFail(t, run(t, "mongodb", mongoFields(map[string]any{"srv": true, "host": "no-such-cluster.invalid"}), lo), probe.StageDNS, "SRV")
}

// ---- RabbitMQ

func rabbitFields(over map[string]any) map[string]any {
	f := map[string]any{"host": "127.0.0.1", "port": envInt("NX_IT_AMQP_PORT"), "username": "app", "password": pass(), "vhost": "orders"}
	for k, v := range over {
		f[k] = v
	}
	return f
}

func TestRabbitMQ(t *testing.T) {
	itEnv(t)
	r := run(t, "rabbitmq", rabbitFields(nil), lo)
	expectPass(t, r)
	if r.ServerVersion == "" {
		t.Fatal("no server version")
	}
	expectFail(t, run(t, "rabbitmq", rabbitFields(map[string]any{"password": "wrong"}), lo), probe.StageAuth, "authentication failed")
	expectFail(t, run(t, "rabbitmq", rabbitFields(map[string]any{"username": "noperm"}), lo), probe.StageAuth, "virtual host")
	expectFail(t, run(t, "rabbitmq", rabbitFields(map[string]any{"vhost": "missing"}), lo), probe.StageAuth, "virtual host")
	expectFail(t, run(t, "rabbitmq", rabbitFields(map[string]any{"port": 4}), lo), probe.StageNetwork, "refused")
	expectFail(t, run(t, "rabbitmq", rabbitFields(map[string]any{"tls": true}), lo), probe.StageTLS, "TLS")
	// A port that speaks another protocol (Redis) is not AMQP.
	expectFail(t, run(t, "rabbitmq", rabbitFields(map[string]any{"port": envInt("NX_IT_REDIS_PORT")}), lo), probe.StageAuth, "AMQP")
}

// ---- Kafka

func kafkaFields(over map[string]any) map[string]any {
	f := map[string]any{"bootstrapServers": fmt.Sprintf("127.0.0.1:%d", envInt("NX_IT_KAFKA_PORT")), "securityProtocol": "PLAINTEXT"}
	for k, v := range over {
		f[k] = v
	}
	return f
}

func TestKafka(t *testing.T) {
	itEnv(t)
	if envInt("NX_IT_KAFKA_PORT") == 0 {
		t.Skip()
	}
	r := run(t, "kafka", kafkaFields(nil), lo)
	expectPass(t, r)
	if !strings.Contains(stage(r, probe.StageServer).Message, "broker(s)") {
		t.Fatalf("%+v", stage(r, probe.StageServer))
	}
	expectFail(t, run(t, "kafka", kafkaFields(map[string]any{"bootstrapServers": "127.0.0.1:5"}), lo), probe.StageNetwork, "refused")
	expectFail(t, run(t, "kafka", kafkaFields(map[string]any{"bootstrapServers": "not-a-list"}), lo), probe.StageInput, "host:port")
	expectFail(t, run(t, "kafka", kafkaFields(map[string]any{"securityProtocol": "SSL"}), lo), probe.StageTLS, "TLS")
	expectFail(t, run(t, "kafka", kafkaFields(map[string]any{"securityProtocol": "SASL_PLAINTEXT"}), lo), probe.StageAuth, "SASL needs")
	// Fallback: second bootstrap server works when the first is down.
	r = run(t, "kafka", kafkaFields(map[string]any{"bootstrapServers": fmt.Sprintf("127.0.0.1:5,127.0.0.1:%d", envInt("NX_IT_KAFKA_PORT"))}), lo)
	expectPass(t, r)
}
