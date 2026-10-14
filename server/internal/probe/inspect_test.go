package probe

import (
	"strings"
	"testing"
)

func TestReadOnlySQL(t *testing.T) {
	ok := []string{
		"SELECT 1", "select * from t where a = 'x;y'", "SELECT 1;", "  SHOW TABLES  ", "SHOW CREATE TABLE t", "DESCRIBE t", "desc t",
		"EXPLAIN SELECT * FROM t", "WITH a AS (SELECT 1) SELECT * FROM a", "-- c\nSELECT 1", "/* c */ SELECT 1", "SELECT 'DROP TABLE x'", "SELECT `update`, updated_at FROM t",
		"SELECT \"it's; fine\" AS s", "SELECT 'a''b; c'", "TABLE t",
	}
	for _, q := range ok {
		if e := readOnlySQL(q); e != nil {
			t.Errorf("%q should be allowed: %v", q, e)
		}
	}
	bad := map[string]string{
		"": "empty_query", "   ": "empty_query", ";": "empty_query", " ; ; ": "multiple_statements", "-- only a comment": "empty_query", "/* x */": "empty_query",
		"DROP TABLE t": "read_only", "delete from t": "read_only", "INSERT INTO t VALUES (1)": "read_only", "UPDATE t SET a=1": "read_only",
		"REPLACE INTO t VALUES (1)": "read_only", "ALTER TABLE t ADD c INT": "read_only", "TRUNCATE t": "read_only", "GRANT ALL ON *.* TO x": "read_only",
		"CALL p()": "read_only", "SET @a = 1": "read_only", "USE other": "read_only", "LOAD DATA INFILE 'x' INTO TABLE t": "read_only",
		"SELECT 1; SELECT 2": "multiple_statements", "SELECT 1; DROP TABLE t": "multiple_statements", "SELECT 1 /* ; */ ; DELETE FROM t": "multiple_statements",
		"SELECT * FROM t INTO OUTFILE '/tmp/x'": "read_only", "SELECT * FROM t INTO DUMPFILE '/tmp/x'": "read_only", "SELECT 1 INTO @v": "read_only",
		"SELECT * FROM t FOR UPDATE": "read_only", "SELECT * FROM t LOCK IN SHARE MODE": "read_only", "SELECT SLEEP(10)": "read_only",
		"select benchmark(1000000000, md5('x'))": "read_only", "SELECT GET_LOCK('a', 10)": "read_only", "SELECT LOAD_FILE('/etc/passwd')": "read_only",
		"WITH a AS (SELECT 1) DELETE FROM t": "read_only",
		"EXPLAIN ANALYZE UPDATE t SET a = 1": "read_only",
	}
	for q, code := range bad {
		e := readOnlySQL(q)
		if e == nil {
			t.Errorf("%q must be refused", q)
		} else if e.Code != code {
			t.Errorf("%q: code %q (%s), want %q", q, e.Code, e.Msg, code)
		}
	}
}

func TestStripSQLNoiseKeepsStructureAndDropsText(t *testing.T) {
	got := stripSQLNoise("SELECT 'a; DROP', \"b -- c\", `d` /* e; */ FROM t -- f; g")
	if strings.Contains(got, "DROP") || strings.Contains(got, ";") || !strings.Contains(got, "FROM t") {
		t.Fatalf("%q", got)
	}
}

func TestSplitCommandAndRedisAllowlist(t *testing.T) {
	cases := map[string][]string{
		`GET key`: {"GET", "key"}, `  GET   "a b"  `: {"GET", "a b"}, `SET k 'v w'`: {"SET", "k", "v w"}, `GET ""`: {"GET", ""}, ``: nil,
	}
	for in, want := range cases {
		got, err := splitCommand(in)
		if err != nil || len(got) != len(want) {
			t.Errorf("%q -> %v %v", in, got, err)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%q -> %v", in, got)
			}
		}
	}
	if _, err := splitCommand(`GET "oops`); err == nil {
		t.Error("an unclosed quote is an error")
	}
	for _, w := range []string{"SET", "DEL", "FLUSHALL", "FLUSHDB", "CONFIG", "KEYS", "EVAL", "EVALSHA", "SCRIPT", "SHUTDOWN", "SLAVEOF", "REPLICAOF", "MIGRATE", "DEBUG", "MODULE", "ACL", "CLIENT", "PUBLISH", "XADD", "LPUSH", "EXPIRE", "RENAME", "SAVE", "BGSAVE"} {
		if redisReadOnly[w] {
			t.Errorf("%s must not be on the read allowlist", w)
		}
	}
	for _, r := range []string{"GET", "TTL", "HGETALL", "LRANGE", "SCAN", "INFO"} {
		if !redisReadOnly[r] {
			t.Errorf("%s should be allowed", r)
		}
	}
}

func TestCellAndClip(t *testing.T) {
	if got := cell([]byte{0xff, 0xfe}); !strings.HasPrefix(got.(string), "base64:") {
		t.Errorf("binary: %v", got)
	}
	if got := cell([]byte("héllo")); got != "héllo" {
		t.Errorf("text bytes: %v", got)
	}
	long := strings.Repeat("é", 5000)
	if got := cell(long).(string); len(got) > maxCell+40 || !strings.HasSuffix(got, "(truncated)") {
		t.Errorf("clip: %d", len(got))
	}
	if cell(nil) != nil || cell(5) != 5 || cell(true) != true {
		t.Error("scalars pass through")
	}
}

func TestLimitArg(t *testing.T) {
	for in, want := range map[any]int{nil: DefaultLimit, 0: DefaultLimit, -4: DefaultLimit, 7: 7, 99999: MaxLimit, "12": 12, "x": DefaultLimit, 3.0: 3} {
		if got := limitArg(map[string]any{"limit": in}); got != want {
			t.Errorf("limit %v -> %d, want %d", in, got, want)
		}
	}
}
