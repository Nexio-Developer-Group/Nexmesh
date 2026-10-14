package probe

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Inspectors (PRD F13) are lightweight, read-only views of a connected service:
// browse databases, collections, keys, topics and queues, and run read-only
// queries with a row limit and a timeout. They run in the control plane through
// the same guarded dialer as Test Connection, so credentials never reach the
// browser. Results are returned to the caller and never stored.

const (
	DefaultLimit = 100
	MaxLimit     = 500
	maxCell      = 2000
	maxPayload   = 1 << 20 // 1 MB of cells in one response
	opTimeout    = 12 * time.Second
)

// InspectRequest is one operation. Args depend on Op (see ListOps).
type InspectRequest struct {
	Op   string         `json:"op"`
	Args map[string]any `json:"args"`
}

// InspectResult is a table of results.
type InspectResult struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated,omitempty"`
	Note      string   `json:"note,omitempty"`
	ElapsedMS int64    `json:"elapsedMs"`
	// Next is a cursor for paging key listings.
	Next string `json:"next,omitempty"`
}

// InspectError is a user-safe problem: a refused write, a bad argument, a
// connection failure. Code is stable for the UI.
type InspectError struct {
	Code string
	Msg  string
}

func (e *InspectError) Error() string { return e.Msg }

func ierr(code, format string, a ...any) *InspectError {
	return &InspectError{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// Op describes an operation a service supports, for the UI.
type Op struct {
	Name  string  `json:"name"`
	Label string  `json:"label"`
	Args  []OpArg `json:"args"`
}

type OpArg struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Required bool   `json:"required,omitempty"`
	Kind     string `json:"kind,omitempty"` // text | code | number
	Hint     string `json:"hint,omitempty"`
}

var ops = map[string][]Op{
	"mysql": {
		{"databases", "Databases", nil},
		{"tables", "Tables", []OpArg{{"database", "Database", false, "text", "Defaults to the connection's database"}}},
		{"schema", "Table schema", []OpArg{{"database", "Database", false, "text", ""}, {"table", "Table", true, "text", ""}}},
		{"query", "Read-only query", []OpArg{{"sql", "SQL", true, "code", "SELECT, SHOW, DESCRIBE or EXPLAIN. One statement."}, {"limit", "Row limit", false, "number", ""}}},
	},
	"mongodb": {
		{"databases", "Databases", nil},
		{"collections", "Collections", []OpArg{{"database", "Database", false, "text", "Defaults to the connection's database"}}},
		{"schema", "Collection schema (sampled)", []OpArg{{"database", "Database", false, "text", ""}, {"collection", "Collection", true, "text", ""}}},
		{"find", "Find documents", []OpArg{{"database", "Database", false, "text", ""}, {"collection", "Collection", true, "text", ""}, {"filter", "Filter (JSON)", false, "code", `{"status": "active"}`}, {"sort", "Sort (JSON)", false, "code", `{"_id": -1}`}, {"limit", "Limit", false, "number", ""}}},
		{"count", "Count documents", []OpArg{{"database", "Database", false, "text", ""}, {"collection", "Collection", true, "text", ""}, {"filter", "Filter (JSON)", false, "code", ""}}},
	},
	"redis": {
		{"keys", "Browse keys", []OpArg{{"pattern", "Pattern", false, "text", "For example user:*"}, {"cursor", "Cursor", false, "text", ""}, {"limit", "Keys per page", false, "number", ""}}},
		{"get", "Read a key", []OpArg{{"key", "Key", true, "text", ""}, {"limit", "Limit", false, "number", ""}}},
		{"command", "Read command", []OpArg{{"command", "Command", true, "code", "For example TTL user:1 (read commands only)"}}},
	},
	"kafka": {
		{"topics", "Topics", nil},
		{"partitions", "Partitions and offsets", []OpArg{{"topic", "Topic", true, "text", ""}}},
		{"sample", "Sample messages", []OpArg{{"topic", "Topic", true, "text", ""}, {"partition", "Partition", false, "number", "Default 0"}, {"limit", "Messages", false, "number", "The most recent N"}}},
	},
	"rabbitmq": {
		{"queues", "Queues", []OpArg{{"vhost", "Virtual host", false, "text", "All by default"}}},
		{"overview", "Overview", nil},
	},
}

// Ops lists what the inspector can do for a catalog entry, nil if it has none.
func Ops(componentID string) []Op { return ops[componentID] }

// Inspectable reports whether a component has an inspector.
func Inspectable(componentID string) bool { return len(ops[componentID]) > 0 }

func argStr(a map[string]any, k string) string {
	if v, ok := a[k]; ok && v != nil {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func argInt(a map[string]any, k string, def int) int {
	switch v := a[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil {
			return n
		}
	}
	return def
}

func limitArg(a map[string]any) int {
	n := argInt(a, "limit", DefaultLimit)
	if n < 1 {
		n = DefaultLimit
	}
	if n > MaxLimit {
		n = MaxLimit
	}
	return n
}

// cell makes a value safe to show: text is truncated, bytes that are not valid
// UTF-8 become base64, times and other types become strings.
func cell(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		if utf8.Valid(x) {
			return clip(string(x))
		}
		return "base64:" + clip(base64.StdEncoding.EncodeToString(x))
	case string:
		return clip(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return x
	}
	return clip(fmt.Sprint(v))
}

func clip(s string) string {
	if len(s) <= maxCell {
		return s
	}
	r := []rune(s)
	for len(string(r)) > maxCell {
		r = r[:len(r)-len(r)/10-1]
	}
	return string(r) + "… (truncated)"
}

// payload tracks the size of a result so one response cannot grow without bound.
type payload struct{ n int }

func (p *payload) add(row []any) bool {
	for _, c := range row {
		switch x := c.(type) {
		case string:
			p.n += len(x)
		default:
			p.n += 8
		}
	}
	return p.n <= maxPayload
}

// Inspect runs one operation. It never returns a partial connection: the
// session is opened and closed inside the call.
func Inspect(ctx context.Context, p Params, req InspectRequest) (res *InspectResult, ie *InspectError) {
	// Whatever a driver or a malformed query does, the control plane keeps running.
	defer func() {
		if r := recover(); r != nil {
			res, ie = nil, ierr("internal", "The inspector hit an unexpected problem. The request was not completed.")
		}
	}()
	if !Inspectable(p.Def.ID) {
		return nil, ierr("not_inspectable", "%s has no inspector.", p.Def.Name)
	}
	found := false
	for _, o := range ops[p.Def.ID] {
		found = found || o.Name == req.Op
	}
	if !found {
		names := make([]string, 0)
		for _, o := range ops[p.Def.ID] {
			names = append(names, o.Name)
		}
		sort.Strings(names)
		return nil, ierr("unknown_op", "Unknown operation %q. Available: %s.", req.Op, strings.Join(names, ", "))
	}
	if req.Args == nil {
		req.Args = map[string]any{}
	}
	if p.Scrub == nil {
		p.Scrub = func(s string) string { return s }
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	t, e := validate(p)
	if e != nil {
		return nil, ierr("bad_connection", "%s", e.Msg)
	}
	addrs, _, e := resolve(ctx, p, t)
	if e != nil {
		return nil, ierr("unreachable", "%s", e.Msg)
	}
	if e := reach(ctx, p, t, addrs); e != nil {
		return nil, ierr("unreachable", "%s", e.Msg)
	}

	start := time.Now()
	switch p.Def.ID {
	case "mysql":
		res, ie = inspectMySQL(ctx, t, p, req)
	case "mongodb":
		res, ie = inspectMongo(ctx, t, p, req)
	case "redis":
		res, ie = inspectRedis(ctx, t, p, req)
	case "kafka":
		res, ie = inspectKafka(ctx, t, p, req)
	case "rabbitmq":
		res, ie = inspectRabbit(ctx, t, p, req)
	}
	if ie != nil {
		return nil, ie
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && res != nil {
		res.Truncated = true
		res.Note = strings.TrimSpace(res.Note + " The time limit was reached; the result is partial.")
	}
	res.ElapsedMS = time.Since(start).Milliseconds()
	if res.Rows == nil {
		res.Rows = [][]any{}
	}
	return res, nil
}

// connectErr turns a stage error from a session's own Authenticate into an inspector error.
func connectErr(e *StageError) *InspectError { return ierr("connection_failed", "%s", e.Msg) }
