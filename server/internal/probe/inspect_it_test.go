package probe_test

// Integration tests for the inspectors (PRD F13) against real services. Same
// switches as it_test.go: NEXMESH_IT=1 and scripts/it/start-services.sh.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
	"github.com/nexio-developer-group/nexmesh/server/internal/probe"
)

func inspect(t *testing.T, id string, fields map[string]any, op string, args map[string]any) (*probe.InspectResult, *probe.InspectError) {
	t.Helper()
	secret := fmt.Sprint(fields["password"])
	res, err := probe.Inspect(context.Background(), probe.Params{Def: def(t, id), Fields: fields, Guard: netguard.New(lo), Timeout: 30 * time.Second,
		Scrub: func(s string) string { return strings.ReplaceAll(s, secret, "[redacted]") }}, probe.InspectRequest{Op: op, Args: args})
	if res != nil {
		for _, row := range res.Rows {
			for _, c := range row {
				if secret != "" && strings.Contains(fmt.Sprint(c), secret) {
					t.Fatalf("a result contains the password: %v", row)
				}
			}
		}
	}
	return res, err
}

func mustInspect(t *testing.T, id string, fields map[string]any, op string, args map[string]any) *probe.InspectResult {
	t.Helper()
	res, err := inspect(t, id, fields, op, args)
	if err != nil {
		t.Fatalf("%s %s: %s (%s)", id, op, err.Msg, err.Code)
	}
	return res
}

func refused(t *testing.T, id string, fields map[string]any, op string, args map[string]any, code string) {
	t.Helper()
	res, err := inspect(t, id, fields, op, args)
	if err == nil {
		t.Fatalf("%s %s %v should be refused, got %+v", id, op, args, res)
	}
	if code != "" && err.Code != code {
		t.Fatalf("%s %s %v: code %q (%s), want %q", id, op, args, err.Code, err.Msg, code)
	}
}

func col(res *probe.InspectResult, name string) []string {
	idx := -1
	for i, c := range res.Columns {
		if c == name {
			idx = i
		}
	}
	var out []string
	for _, r := range res.Rows {
		if idx >= 0 {
			out = append(out, fmt.Sprint(r[idx]))
		}
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// ---- Redis

func TestInspectRedis(t *testing.T) {
	itEnv(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("127.0.0.1:%d", envInt("NX_IT_REDIS_PORT")), Password: pass()})
	defer c.Close()
	c.FlushDB(ctx)
	c.Set(ctx, "inspect:str", "hello", time.Hour)
	c.HSet(ctx, "inspect:hash", "f1", "v1", "f2", "v2")
	c.RPush(ctx, "inspect:list", "a", "b", "c")
	c.SAdd(ctx, "inspect:set", "x", "y")
	c.ZAdd(ctx, "inspect:z", redis.Z{Score: 2, Member: "m"})
	c.XAdd(ctx, &redis.XAddArgs{Stream: "inspect:stream", Values: map[string]any{"k": "v"}})
	for i := 0; i < 30; i++ {
		c.Set(ctx, fmt.Sprintf("bulk:%02d", i), i, 0)
	}
	f := redisFields(nil)

	keys := mustInspect(t, "redis", f, "keys", map[string]any{"pattern": "inspect:*"})
	if got := col(keys, "key"); len(got) != 6 || !has(got, "inspect:str") {
		t.Fatalf("keys: %v", got)
	}
	types := map[string]string{}
	for _, r := range keys.Rows {
		types[fmt.Sprint(r[0])] = fmt.Sprint(r[1])
	}
	for k, want := range map[string]string{"inspect:str": "string", "inspect:hash": "hash", "inspect:list": "list", "inspect:set": "set", "inspect:z": "zset", "inspect:stream": "stream"} {
		if types[k] != want {
			t.Errorf("%s is %q, want %q", k, types[k], want)
		}
	}
	for _, r := range keys.Rows {
		if fmt.Sprint(r[0]) == "inspect:str" && fmt.Sprint(r[2]) == "no expiry" {
			t.Error("the TTL should be shown")
		}
	}
	// Paging: a small page comes with a cursor that continues.
	first := mustInspect(t, "redis", f, "keys", map[string]any{"pattern": "bulk:*", "limit": 10})
	if first.Next == "" {
		t.Fatal("a partial scan must return a cursor")
	}
	seen := map[string]bool{}
	for cur, n := "", 0; n < 50; n++ {
		r := mustInspect(t, "redis", f, "keys", map[string]any{"pattern": "bulk:*", "limit": 10, "cursor": cur})
		for _, k := range col(r, "key") {
			seen[k] = true
		}
		if cur = r.Next; cur == "" {
			break
		}
	}
	if len(seen) != 30 {
		t.Fatalf("paging should reach every key, got %d", len(seen))
	}

	for key, want := range map[string]string{"inspect:str": "hello", "inspect:hash": "v1", "inspect:list": "b", "inspect:set": "x", "inspect:z": "m", "inspect:stream": "k"} {
		r := mustInspect(t, "redis", f, "get", map[string]any{"key": key})
		if !strings.Contains(fmt.Sprint(r.Rows), want) {
			t.Errorf("get %s: %v", key, r.Rows)
		}
	}
	refused(t, "redis", f, "get", map[string]any{"key": "no-such-key"}, "not_found")

	r := mustInspect(t, "redis", f, "command", map[string]any{"command": `TTL "inspect:str"`})
	if n := fmt.Sprint(r.Rows[0][0]); n == "-1" || n == "-2" {
		t.Fatalf("ttl %s", n)
	}
	r = mustInspect(t, "redis", f, "command", map[string]any{"command": "HGETALL inspect:hash"})
	if len(r.Rows) != 2 && len(r.Rows) != 4 { // RESP3 returns a map (2 rows), RESP2 a flat list (4)
		t.Fatalf("flattened reply: %v", r.Rows)
	}
	mustInspect(t, "redis", f, "command", map[string]any{"command": "INFO server"})

	// Anything that is not a read is refused, and nothing was changed.
	for _, cmd := range []string{"SET inspect:str hacked", "DEL inspect:str", "FLUSHALL", "CONFIG GET *", "KEYS *", "EVAL 'return 1' 0", "SHUTDOWN", "  ", `GET "unterminated`} {
		refused(t, "redis", f, "command", map[string]any{"command": cmd}, "")
	}
	if v, _ := c.Get(ctx, "inspect:str").Result(); v != "hello" {
		t.Fatalf("a refused command changed data: %q", v)
	}
	// A wrong password is a connection problem and never echoes the password.
	bad := redisFields(map[string]any{"password": "definitely-wrong"})
	if _, err := inspect(t, "redis", bad, "keys", nil); err == nil || err.Code != "connection_failed" || strings.Contains(err.Msg, "definitely-wrong") {
		t.Fatalf("%+v", err)
	}
	refused(t, "redis", f, "nope", nil, "unknown_op")
}

// ---- MySQL

func mysqlRoot(t *testing.T, sql string) {
	sock := filepath.Join(os.Getenv("NX_IT_DIR"), "mysql", "mysql.sock")
	if out, err := exec.Command("mariadb", "--socket="+sock, "-uroot", "-e", sql).CombinedOutput(); err != nil {
		t.Fatalf("mariadb: %v\n%s", err, out)
	}
}

func TestInspectMySQL(t *testing.T) {
	itEnv(t)
	mysqlRoot(t, fmt.Sprintf(`
CREATE DATABASE IF NOT EXISTS orders;
USE orders;
DROP TABLE IF EXISTS orders.items;
CREATE TABLE orders.items (id INT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(40) NOT NULL, note TEXT, updated_at DATETIME DEFAULT '2026-01-02 03:04:05');
INSERT INTO orders.items (name, note) SELECT CONCAT('item-', seq), REPEAT('x', 3000) FROM seq_1_to_250;
DROP TABLE IF EXISTS orders.audit_log;
CREATE TABLE orders.audit_log (n INT);
CREATE USER IF NOT EXISTS 'writer'@'%%' IDENTIFIED BY '%s';
GRANT ALL ON orders.* TO 'writer'@'%%';
FLUSH PRIVILEGES;`, pass()))
	mysqlRoot(t, "DROP FUNCTION IF EXISTS orders.sneaky;\nDELIMITER //\nCREATE FUNCTION orders.sneaky() RETURNS INT MODIFIES SQL DATA BEGIN INSERT INTO orders.audit_log VALUES (1); RETURN 1; END//\nDELIMITER ;")
	f := mysqlFields(nil)

	if got := col(mustInspect(t, "mysql", f, "databases", nil), "Database"); !has(got, "orders") || has(got, "secret_db") {
		t.Fatalf("a SELECT-only account sees only what it may: %v", got)
	}
	tbl := mustInspect(t, "mysql", f, "tables", nil) // defaults to the connection's database
	if got := col(tbl, "table"); !has(got, "items") {
		t.Fatalf("tables: %v (%v)", got, tbl.Columns)
	}
	sch := mustInspect(t, "mysql", f, "schema", map[string]any{"table": "items"})
	if got := col(sch, "column"); len(got) != 4 || got[0] != "id" {
		t.Fatalf("schema: %v", sch.Rows)
	}

	q := mustInspect(t, "mysql", f, "query", map[string]any{"sql": "SELECT id, name, note FROM items ORDER BY id"})
	if len(q.Rows) != probe.DefaultLimit || !q.Truncated || q.Note == "" {
		t.Fatalf("the default limit should cap and say so: %d truncated=%v", len(q.Rows), q.Truncated)
	}
	if note := fmt.Sprint(q.Rows[0][2]); !strings.HasSuffix(note, "(truncated)") || len(note) > 2100 {
		t.Fatalf("long cells are clipped: %d", len(note))
	}
	small := mustInspect(t, "mysql", f, "query", map[string]any{"sql": "SELECT COUNT(*) AS n, MAX(updated_at) AS latest FROM items", "limit": 5})
	if small.Truncated || fmt.Sprint(small.Rows[0][0]) != "250" || !strings.HasPrefix(fmt.Sprint(small.Rows[0][1]), "2026-01-02") {
		t.Fatalf("%+v", small)
	}
	mustInspect(t, "mysql", f, "query", map[string]any{"sql": "SHOW CREATE TABLE items;"})
	mustInspect(t, "mysql", f, "query", map[string]any{"sql": "-- a comment\nSELECT 'drop table items; delete from items' AS text"}) // keywords in text are text
	mustInspect(t, "mysql", f, "query", map[string]any{"sql": "EXPLAIN SELECT * FROM items"})

	for _, bad := range []string{
		"DROP TABLE items", "DELETE FROM items", "INSERT INTO items (name) VALUES ('x')", "UPDATE items SET name = 'x'",
		"/* hidden */ DELETE FROM items", "SELECT 1; DROP TABLE items", "SELECT * FROM items INTO OUTFILE '/tmp/x'",
		"SELECT SLEEP(5)", "SELECT * FROM items FOR UPDATE", "SET GLOBAL read_only = 0", "TRUNCATE items", "", "   ;  ",
	} {
		refused(t, "mysql", f, "query", map[string]any{"sql": bad}, "")
	}

	// The second layer: a statement that passes the check but writes is refused by
	// the server, because every query runs in a READ ONLY transaction. The writer
	// account has every privilege, so only the transaction stands in the way.
	w := mysqlFields(map[string]any{"username": "writer"})
	res, err := inspect(t, "mysql", w, "query", map[string]any{"sql": "SELECT sneaky()"})
	if err == nil {
		t.Fatalf("a function that writes must not run: %+v", res)
	}
	out := mustInspect(t, "mysql", w, "query", map[string]any{"sql": "SELECT COUNT(*) FROM audit_log"})
	if fmt.Sprint(out.Rows[0][0]) != "0" {
		t.Fatalf("the sneaky function wrote a row: %v", out.Rows)
	}
	// Wrong password: a connection problem without the password in it.
	if _, err := inspect(t, "mysql", mysqlFields(map[string]any{"password": "wrong-wrong"}), "databases", nil); err == nil || err.Code != "connection_failed" {
		t.Fatalf("%+v", err)
	}
}

// ---- MongoDB

func TestInspectMongo(t *testing.T) {
	itEnv(t)
	mongoBootstrap(t)
	ctx := context.Background()
	uri := fmt.Sprintf("mongodb://127.0.0.1:%d/?directConnect=true", envInt("NX_IT_MONGO_PORT"))
	ac, _ := mongo.Connect(options.Client().ApplyURI(uri).SetAuth(options.Credential{Username: "root", Password: pass(), AuthSource: "admin"}))
	defer ac.Disconnect(ctx)
	coll := ac.Database("orders").Collection("inspect_items")
	_ = coll.Drop(ctx)
	var docs []any
	for i := 0; i < 150; i++ {
		docs = append(docs, bson.D{{Key: "n", Value: i}, {Key: "name", Value: fmt.Sprintf("doc-%03d", i)}, {Key: "tags", Value: bson.A{"a", "b"}}})
	}
	_, _ = coll.InsertMany(ctx, docs)
	f := mongoFields(nil)

	if got := col(mustInspect(t, "mongodb", f, "databases", nil), "database"); !has(got, "orders") {
		t.Fatalf("databases: %v", got)
	}
	cs := mustInspect(t, "mongodb", f, "collections", nil)
	if got := col(cs, "collection"); !has(got, "inspect_items") {
		t.Fatalf("collections: %v", got)
	}
	sch := mustInspect(t, "mongodb", f, "schema", map[string]any{"collection": "inspect_items"})
	if got := col(sch, "field"); !has(got, "n") || !has(got, "tags") || !strings.Contains(sch.Note, "sample") {
		t.Fatalf("schema: %v %q", got, sch.Note)
	}
	if n := mustInspect(t, "mongodb", f, "count", map[string]any{"collection": "inspect_items", "filter": `{"n": {"$gte": 100}}`}); fmt.Sprint(n.Rows[0][0]) != "50" {
		t.Fatalf("count: %v", n.Rows)
	}
	all := mustInspect(t, "mongodb", f, "find", map[string]any{"collection": "inspect_items"})
	if len(all.Rows) != probe.DefaultLimit || !all.Truncated {
		t.Fatalf("default limit: %d truncated=%v", len(all.Rows), all.Truncated)
	}
	top := mustInspect(t, "mongodb", f, "find", map[string]any{"collection": "inspect_items", "filter": `{"n": {"$lt": 10}}`, "sort": `{"n": -1}`, "limit": 3})
	if len(top.Rows) != 3 || !strings.Contains(fmt.Sprint(top.Rows[0][1]), `"doc-009"`) || !top.Truncated { // ten match, three shown
		t.Fatalf("%v", top.Rows)
	}
	for _, bad := range []string{`{"$where": "sleep(5000)"}`, `{"a": {"$function": {"body": "x", "args": [], "lang": "js"}}}`, `{"$or": [{"$where": "1"}]}`, `{not json`} {
		refused(t, "mongodb", f, "find", map[string]any{"collection": "inspect_items", "filter": bad}, "")
	}
	refused(t, "mongodb", f, "find", map[string]any{}, "bad_args")
	if n, _ := coll.CountDocuments(ctx, bson.D{}); n != 150 {
		t.Fatalf("the data changed: %d", n)
	}
	if _, err := inspect(t, "mongodb", mongoFields(map[string]any{"password": "wrong-wrong"}), "databases", nil); err == nil || err.Code != "connection_failed" {
		t.Fatalf("%+v", err)
	}
}

// ---- Kafka

func TestInspectKafka(t *testing.T) {
	itEnv(t)
	if envInt("NX_IT_KAFKA_PORT") == 0 {
		t.Skip()
	}
	ctx := context.Background()
	addr := fmt.Sprintf("127.0.0.1:%d", envInt("NX_IT_KAFKA_PORT"))
	topic := fmt.Sprintf("inspect-%d", time.Now().UnixNano()%1e9)
	conn, err := kafka.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.CreateTopics(kafka.TopicConfig{Topic: topic, NumPartitions: 2, ReplicationFactor: 1}); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	w := &kafka.Writer{Addr: kafka.TCP(addr), Topic: topic, Balancer: &kafka.Hash{}, AllowAutoTopicCreation: true}
	var msgs []kafka.Message
	for i := 0; i < 30; i++ {
		msgs = append(msgs, kafka.Message{Partition: 0, Key: []byte(fmt.Sprintf("k%d", i)), Value: []byte(fmt.Sprintf("message-%d", i))})
	}
	for tries := 0; tries < 20; tries++ {
		if err = (&kafka.Writer{Addr: kafka.TCP(addr), Topic: topic, Balancer: &kafka.LeastBytes{}}).WriteMessages(ctx, msgs...); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = w.Close()
	if err != nil {
		t.Fatal(err)
	}
	f := kafkaFields(nil)

	topics := mustInspect(t, "kafka", f, "topics", nil)
	if !has(col(topics, "topic"), topic) {
		t.Fatalf("topics: %v", col(topics, "topic"))
	}
	parts := mustInspect(t, "kafka", f, "partitions", map[string]any{"topic": topic})
	if len(parts.Rows) != 2 {
		t.Fatalf("partitions: %v", parts.Rows)
	}
	var total int64
	for _, r := range parts.Rows {
		if n, ok := r[5].(int64); ok {
			total += n
		}
	}
	if total != 30 {
		t.Fatalf("message counts should add up to what was produced, got %d: %v", total, parts.Rows)
	}
	// Sample the most recent ten of a partition that has at least that many.
	var sample *probe.InspectResult
	var next int64
	for _, r := range parts.Rows {
		if n, _ := r[5].(int64); n >= 10 {
			next, _ = r[4].(int64)
			sample = mustInspect(t, "kafka", f, "sample", map[string]any{"topic": topic, "partition": r[0], "limit": 10})
			break
		}
	}
	if sample == nil || len(sample.Rows) != 10 {
		t.Fatalf("sample: %+v", sample)
	}
	offs := col(sample, "offset")
	if offs[0] != fmt.Sprint(next-10) || offs[9] != fmt.Sprint(next-1) || !strings.HasPrefix(fmt.Sprint(sample.Rows[9][3]), "message-") {
		t.Fatalf("the most recent ten, oldest first, ending at offset %d: %v", next-1, sample.Rows)
	}
	missing := fmt.Sprintf("missing-%d", time.Now().UnixNano())
	refused(t, "kafka", f, "partitions", map[string]any{"topic": missing}, "not_found")
	refused(t, "kafka", f, "sample", map[string]any{"topic": missing}, "not_found")
	// ...and asking about a topic that does not exist must not create it.
	if all := mustInspect(t, "kafka", f, "topics", nil); has(col(all, "topic"), missing) {
		t.Fatal("inspecting a missing topic created it")
	}
	refused(t, "kafka", f, "sample", map[string]any{}, "bad_args")
	// Sampling never joins a consumer group, so it leaves no trace in lag.
	cc, _ := kafka.Dial("tcp", addr)
	defer cc.Close()
	groups, err := (&kafka.Client{Addr: kafka.TCP(addr)}).ListGroups(ctx, &kafka.ListGroupsRequest{})
	if err == nil {
		for _, g := range groups.Groups {
			if strings.Contains(g.GroupID, "nexmesh") {
				t.Fatalf("an inspector created a consumer group: %v", g.GroupID)
			}
		}
	}
}

// ---- RabbitMQ

func TestInspectRabbit(t *testing.T) {
	itEnv(t)
	if out, err := exec.Command("rabbitmqctl", "-n", "nxit@localhost", "set_user_tags", "app", "monitoring").CombinedOutput(); err != nil {
		t.Skipf("rabbitmqctl unavailable: %v %s", err, out)
	}
	conn, err := amqp.Dial(fmt.Sprintf("amqp://app:%s@127.0.0.1:%d/orders", pass(), envInt("NX_IT_AMQP_PORT")))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, _ := conn.Channel()
	if _, err := ch.QueueDeclare("inspect-q", false, true, false, false, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_ = ch.PublishWithContext(context.Background(), "", "inspect-q", false, false, amqp.Publishing{Body: []byte("m")})
	}
	time.Sleep(2 * time.Second) // the management stats refresh periodically
	f := rabbitFields(map[string]any{"managementUrl": fmt.Sprintf("http://127.0.0.1:%d", envInt("NX_IT_AMQP_MGMT_PORT"))})

	qs := mustInspect(t, "rabbitmq", f, "queues", map[string]any{"vhost": "orders"})
	found := false
	for _, r := range qs.Rows {
		if fmt.Sprint(r[0]) == "inspect-q" {
			found = true
		}
	}
	if !found {
		t.Fatalf("queues: %v", qs.Rows)
	}
	ov := mustInspect(t, "rabbitmq", f, "overview", nil)
	if !strings.Contains(fmt.Sprint(ov.Rows), "version") {
		t.Fatalf("overview: %v", ov.Rows)
	}
	refused(t, "rabbitmq", rabbitFields(nil), "queues", nil, "needs_management_url")
	bad := rabbitFields(map[string]any{"managementUrl": fmt.Sprintf("http://127.0.0.1:%d", envInt("NX_IT_AMQP_MGMT_PORT")), "password": "wrong-wrong"})
	refused(t, "rabbitmq", bad, "queues", nil, "permission")
}

func TestInspectUsesTheEgressGuard(t *testing.T) {
	// No services needed: a blocked address is refused before any connection.
	for _, id := range []string{"redis", "mysql", "mongodb"} {
		f := map[string]any{"host": "169.254.169.254", "port": 1234, "database": "d", "username": "u", "password": "p"}
		res, err := probe.Inspect(context.Background(), probe.Params{Def: def(t, id), Fields: f, Guard: netguard.New(netguard.Policy{AllowPrivate: true, AllowLoopback: true})},
			probe.InspectRequest{Op: map[string]string{"redis": "keys", "mysql": "databases", "mongodb": "databases"}[id]})
		if err == nil || !strings.Contains(err.Msg, "not allowed") {
			t.Fatalf("%s: %+v %+v", id, res, err)
		}
	}
	if _, err := probe.Inspect(context.Background(), probe.Params{Def: def(t, "nginx"), Guard: netguard.New(lo)}, probe.InspectRequest{Op: "x"}); err == nil || err.Code != "not_inspectable" {
		t.Fatalf("%+v", err)
	}
}
