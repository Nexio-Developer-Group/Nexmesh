package probe

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Commands that only read. Anything else, including KEYS (which can stall a
// large server), is refused.
var redisReadOnly = map[string]bool{
	"GET": true, "MGET": true, "STRLEN": true, "GETRANGE": true, "EXISTS": true, "TYPE": true, "TTL": true, "PTTL": true,
	"HGET": true, "HGETALL": true, "HLEN": true, "HMGET": true, "HKEYS": true, "HVALS": true, "HEXISTS": true,
	"LLEN": true, "LRANGE": true, "LINDEX": true, "SCARD": true, "SMEMBERS": true, "SISMEMBER": true, "SRANDMEMBER": true,
	"ZCARD": true, "ZRANGE": true, "ZSCORE": true, "ZRANK": true, "ZCOUNT": true, "ZREVRANGE": true,
	"DBSIZE": true, "INFO": true, "XLEN": true, "XRANGE": true, "XREVRANGE": true,
	"SCAN": true, "HSCAN": true, "SSCAN": true, "ZSCAN": true, "TIME": true, "PING": true,
}

// splitCommand splits a command line, honouring single and double quotes.
func splitCommand(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	var q rune
	has := false
	for _, r := range s {
		switch {
		case q != 0:
			if r == q {
				q = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			q, has = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if has || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if q != 0 {
		return nil, fmt.Errorf("a quote is not closed")
	}
	if has || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out, nil
}

func inspectRedis(ctx context.Context, t *Target, p Params, req InspectRequest) (*InspectResult, *InspectError) {
	s0, oe := openRedis(t, p)
	if oe != nil {
		return nil, connectErr(oe)
	}
	s := s0.(*redisSession)
	defer s.Close()
	if e := s.Authenticate(ctx); e != nil {
		return nil, connectErr(e)
	}
	limit := limitArg(req.Args)
	res := &InspectResult{Rows: [][]any{}}
	fail := func(err error) (*InspectResult, *InspectError) {
		m := strings.ToLower(err.Error())
		if strings.Contains(m, "noperm") {
			return nil, ierr("permission", "This account is not allowed to run that command.")
		}
		return nil, ierr("query_failed", "%s", p.Scrub(firstLine(err)))
	}

	switch req.Op {
	case "keys":
		pattern := argStr(req.Args, "pattern")
		if pattern == "" {
			pattern = "*"
		}
		cursor := uint64(0)
		if c := argStr(req.Args, "cursor"); c != "" {
			n, err := strconv.ParseUint(c, 10, 64)
			if err != nil {
				return nil, ierr("bad_args", "The cursor is not valid.")
			}
			cursor = n
		}
		keys, next, err := s.c.Scan(ctx, cursor, pattern, int64(limit)).Result()
		if err != nil {
			return fail(err)
		}
		if len(keys) > limit {
			keys = keys[:limit]
		}
		sort.Strings(keys)
		pipe := s.c.Pipeline()
		types := make([]*redis.StatusCmd, len(keys))
		ttls := make([]*redis.DurationCmd, len(keys))
		for i, k := range keys {
			types[i], ttls[i] = pipe.Type(ctx, k), pipe.TTL(ctx, k)
		}
		_, _ = pipe.Exec(ctx)
		res.Columns = []string{"key", "type", "ttl"}
		for i, k := range keys {
			ttl := "no expiry"
			if d := ttls[i].Val(); d >= 0 {
				ttl = d.Round(time.Second).String()
			} else if d == -2 {
				ttl = "gone"
			}
			res.Rows = append(res.Rows, []any{cell(k), types[i].Val(), ttl})
		}
		if next != 0 {
			res.Next = strconv.FormatUint(next, 10)
			res.Note = "More keys exist. Use the next page to continue; SCAN may return fewer or more keys than asked for."
		}
	case "get":
		key := argStr(req.Args, "key")
		if key == "" {
			return nil, ierr("bad_args", "Enter a key.")
		}
		typ, err := s.c.Type(ctx, key).Result()
		if err != nil {
			return fail(err)
		}
		ttl, _ := s.c.TTL(ctx, key).Result()
		res.Note = "Type " + typ
		if ttl >= 0 {
			res.Note += ", expires in " + ttl.Round(time.Second).String()
		}
		switch typ {
		case "none":
			return nil, ierr("not_found", "That key does not exist.")
		case "string":
			v, err := s.c.Get(ctx, key).Result()
			if err != nil {
				return fail(err)
			}
			res.Columns, res.Rows = []string{"value"}, [][]any{{cell(v)}}
		case "hash":
			m, err := s.c.HGetAll(ctx, key).Result()
			if err != nil {
				return fail(err)
			}
			ks := make([]string, 0, len(m))
			for k := range m {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			res.Columns = []string{"field", "value"}
			for _, k := range ks {
				if len(res.Rows) >= limit {
					res.Truncated = true
					break
				}
				res.Rows = append(res.Rows, []any{cell(k), cell(m[k])})
			}
		case "list":
			v, err := s.c.LRange(ctx, key, 0, int64(limit-1)).Result()
			if err != nil {
				return fail(err)
			}
			res.Columns = []string{"index", "value"}
			for i, x := range v {
				res.Rows = append(res.Rows, []any{i, cell(x)})
			}
			if n, _ := s.c.LLen(ctx, key).Result(); int(n) > len(v) {
				res.Truncated = true
			}
		case "set":
			v, _, err := s.c.SScan(ctx, key, 0, "*", int64(limit)).Result()
			if err != nil {
				return fail(err)
			}
			sort.Strings(v)
			res.Columns = []string{"member"}
			for _, x := range v {
				res.Rows = append(res.Rows, []any{cell(x)})
			}
			if n, _ := s.c.SCard(ctx, key).Result(); int(n) > len(v) {
				res.Truncated = true
			}
		case "zset":
			v, err := s.c.ZRangeWithScores(ctx, key, 0, int64(limit-1)).Result()
			if err != nil {
				return fail(err)
			}
			res.Columns = []string{"member", "score"}
			for _, z := range v {
				res.Rows = append(res.Rows, []any{cell(z.Member), z.Score})
			}
			if n, _ := s.c.ZCard(ctx, key).Result(); int(n) > len(v) {
				res.Truncated = true
			}
		case "stream":
			v, err := s.c.XRevRangeN(ctx, key, "+", "-", int64(limit)).Result()
			if err != nil {
				return fail(err)
			}
			res.Columns = []string{"id", "fields"}
			for _, m := range v {
				res.Rows = append(res.Rows, []any{m.ID, cell(fmt.Sprint(m.Values))})
			}
			res.Note += ", newest first"
		default:
			return nil, ierr("unsupported", "Reading %s keys is not supported.", typ)
		}
	case "command":
		args, err := splitCommand(argStr(req.Args, "command"))
		if err != nil || len(args) == 0 {
			return nil, ierr("bad_args", "Enter a command, for example TTL mykey.")
		}
		name := strings.ToUpper(args[0])
		if !redisReadOnly[name] {
			return nil, ierr("read_only", "%s is not a read command, so it is not allowed here. Allowed: %s.", name, allowedRedis())
		}
		if name == "INFO" && len(args) > 2 {
			return nil, ierr("bad_args", "INFO takes at most one section.")
		}
		anyArgs := make([]any, len(args))
		anyArgs[0] = name
		for i := 1; i < len(args); i++ {
			anyArgs[i] = args[i]
		}
		v, err := s.c.Do(ctx, anyArgs...).Result()
		if err != nil {
			return fail(err)
		}
		res.Columns = []string{"reply"}
		flat := flattenRedis(v, 0)
		for i, line := range flat {
			if i >= limit {
				res.Truncated = true
				break
			}
			res.Rows = append(res.Rows, []any{cell(line)})
		}
	}
	return res, nil
}

func allowedRedis() string {
	var n []string
	for k := range redisReadOnly {
		n = append(n, k)
	}
	sort.Strings(n)
	return strings.Join(n, ", ")
}

func flattenRedis(v any, depth int) []string {
	switch x := v.(type) {
	case nil:
		return []string{"(nil)"}
	case []any:
		var out []string
		for _, e := range x {
			if depth < 3 {
				out = append(out, flattenRedis(e, depth+1)...)
			} else {
				out = append(out, fmt.Sprint(e))
			}
		}
		if len(out) == 0 {
			return []string{"(empty)"}
		}
		return out
	case map[any]any:
		var out []string
		for k, e := range x {
			out = append(out, fmt.Sprintf("%v: %v", k, e))
		}
		sort.Strings(out)
		return out
	case string:
		if strings.Contains(x, "\n") {
			return strings.Split(strings.TrimRight(x, "\r\n"), "\n")
		}
		return []string{x}
	}
	return []string{fmt.Sprint(v)}
}
