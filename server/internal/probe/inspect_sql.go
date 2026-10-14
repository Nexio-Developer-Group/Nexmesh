package probe

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"
)

// readOnlySQL decides whether a statement is a single read statement. It is one
// of two layers: the statement also runs inside a READ ONLY transaction, so the
// server itself refuses a write that slips past this check.
func readOnlySQL(q string) *InspectError {
	clean := stripSQLNoise(q)
	if strings.TrimSpace(clean) == "" {
		return ierr("empty_query", "Enter a statement.")
	}
	if strings.Contains(strings.TrimRight(strings.TrimSpace(clean), ";"), ";") {
		return ierr("multiple_statements", "Run one statement at a time.")
	}
	words := strings.Fields(strings.ToLower(strings.TrimRight(strings.TrimSpace(clean), ";")))
	if len(words) == 0 {
		return ierr("empty_query", "Enter a statement.")
	}
	switch words[0] {
	case "select", "show", "describe", "desc", "explain", "with", "table", "values":
	default:
		return ierr("read_only", "Only read statements are allowed here (SELECT, SHOW, DESCRIBE, EXPLAIN). %q would change data.", strings.ToUpper(words[0]))
	}
	for i, bad := range blockedSQL {
		// The statement-keyword check would reject "SHOW CREATE TABLE", which only reads.
		if i == len(blockedSQL)-1 && (words[0] == "show" || words[0] == "describe" || words[0] == "desc") {
			continue
		}
		if bad.MatchString(clean) {
			return ierr("read_only", "That statement is not allowed in read-only mode (%s).", bad.String()[3:len(bad.String())-3])
		}
	}
	return nil
}

var blockedSQL = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\binto\s+(outfile|dumpfile|@)`),
	regexp.MustCompile(`(?i)\bfor\s+(update|share)\b`),
	regexp.MustCompile(`(?i)\block\s+in\s+share\s+mode\b`),
	regexp.MustCompile(`(?i)\b(sleep|benchmark|get_lock|release_lock|load_file|sys_exec|pg_sleep)\s*\(`),
	regexp.MustCompile(`(?i)\b(insert|update|delete|replace|drop|alter|create|truncate|grant|revoke|rename|call|set|lock|unlock|load|handler)\b\s`),
}

// stripSQLNoise removes comments and the contents of string literals so
// keywords inside text are not mistaken for statements, and a semicolon inside
// a string is not counted.
func stripSQLNoise(q string) string {
	var b strings.Builder
	rs := []rune(q)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '-' && i+1 < len(rs) && rs[i+1] == '-', c == '#':
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
			b.WriteRune(' ')
		case c == '/' && i+1 < len(rs) && rs[i+1] == '*':
			i += 2
			for i+1 < len(rs) && !(rs[i] == '*' && rs[i+1] == '/') {
				i++
			}
			i++
			b.WriteRune(' ')
		case c == '\'' || c == '"' || c == '`':
			q := c
			b.WriteRune(q)
			i++
			for i < len(rs) {
				if rs[i] == '\\' && q != '`' {
					i += 2
					continue
				}
				if rs[i] == q {
					if i+1 < len(rs) && rs[i+1] == q { // doubled quote
						i += 2
						continue
					}
					break
				}
				i++
			}
			b.WriteRune(q)
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

func inspectMySQL(ctx context.Context, t *Target, p Params, req InspectRequest) (*InspectResult, *InspectError) {
	s0, oe := openMySQL(t, p)
	if oe != nil {
		return nil, connectErr(oe)
	}
	s := s0.(*mysqlSession)
	defer s.Close()
	if e := s.Authenticate(ctx); e != nil {
		return nil, connectErr(e)
	}
	db := str(p.Fields, "database")
	arg := func(k string) string {
		if v := argStr(req.Args, k); v != "" {
			return v
		}
		if k == "database" {
			return db
		}
		return ""
	}
	// Every statement runs in a read-only transaction that is always rolled back.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, ierr("connection_failed", "Could not start a read-only session: %s", p.Scrub(firstLine(err)))
	}
	defer func() { _ = tx.Rollback() }()
	_, _ = tx.ExecContext(ctx, "SET SESSION MAX_EXECUTION_TIME=10000")

	var q string
	var args []any
	limit := limitArg(req.Args)
	switch req.Op {
	case "databases":
		q = "SHOW DATABASES"
	case "tables":
		if arg("database") == "" {
			return nil, ierr("bad_args", "Choose a database.")
		}
		q = "SELECT table_name AS `table`, table_type AS type, table_rows AS approx_rows FROM information_schema.tables WHERE table_schema = ? ORDER BY table_name"
		args = []any{arg("database")}
	case "schema":
		if arg("database") == "" || argStr(req.Args, "table") == "" {
			return nil, ierr("bad_args", "Choose a database and a table.")
		}
		q = "SELECT column_name AS `column`, column_type AS type, is_nullable AS nullable, column_key AS `key`, column_default AS `default`, extra FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position"
		args = []any{arg("database"), argStr(req.Args, "table")}
	case "query":
		q = argStr(req.Args, "sql")
		if e := readOnlySQL(q); e != nil {
			return nil, e
		}
		q = strings.TrimRight(strings.TrimSpace(q), "; \n\t")
		if d := argStr(req.Args, "database"); d != "" && d != db {
			if _, err := tx.ExecContext(ctx, "USE `"+strings.ReplaceAll(d, "`", "``")+"`"); err != nil {
				return nil, ierr("bad_args", "Database %q is not available to this account.", d)
			}
		}
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, ierr("query_failed", "%s", p.Scrub(sqlMessage(err)))
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	res := &InspectResult{Columns: cols, Rows: [][]any{}}
	var pl payload
	for rows.Next() {
		if len(res.Rows) >= limit {
			res.Truncated = true
			res.Note = "Showing the first rows only. Add a WHERE or LIMIT to narrow the query."
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, ierr("query_failed", "%s", p.Scrub(firstLine(err)))
		}
		row := make([]any, len(vals))
		for i, v := range vals {
			row[i] = cell(v)
		}
		if !pl.add(row) {
			res.Truncated = true
			res.Note = "The result is large; showing the rows that fit."
			break
		}
		res.Rows = append(res.Rows, row)
	}
	if err := rows.Err(); err != nil && !res.Truncated {
		return nil, ierr("query_failed", "%s", p.Scrub(sqlMessage(err)))
	}
	return res, nil
}

func sqlMessage(err error) string {
	m := firstLine(err)
	if strings.Contains(m, "1792") || strings.Contains(strings.ToLower(m), "read only transaction") {
		return "The database refused to change data in read-only mode."
	}
	if strings.Contains(m, "3024") || strings.Contains(strings.ToLower(m), "maximum statement execution time") {
		return "The query took longer than the time limit (10 seconds) and was stopped."
	}
	return m
}

var _ = time.Second
