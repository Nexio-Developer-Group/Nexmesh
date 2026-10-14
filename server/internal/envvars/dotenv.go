package envvars

import (
	"fmt"
	"sort"
	"strings"
)

// ParsedVar is one assignment found in a .env file.
type ParsedVar struct {
	Name  string
	Value string
	Line  int
}

// LineError points at a line that could not be imported.
type LineError struct {
	Line int    `json:"line"`
	Msg  string `json:"message"`
}

// ParseDotenv reads dotenv syntax: KEY=value, optional "export ", # comments,
// single-quoted literals, double-quoted values with \n \t \" \\ escapes, and
// quoted values that span lines. Later duplicates win.
func ParseDotenv(src string) ([]ParsedVar, []LineError) {
	src = strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(src, "\n")
	var out []ParsedVar
	var errs []LineError
	seen := map[string]int{}
	for i := 0; i < len(lines); i++ {
		raw := strings.TrimSpace(lines[i])
		start := i + 1
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "export "))
		name, rest, ok := strings.Cut(raw, "=")
		name = strings.TrimSpace(name)
		if !ok {
			errs = append(errs, LineError{start, "Expected NAME=value."})
			continue
		}
		if err := ValidateName(name); err != nil {
			errs = append(errs, LineError{start, fmt.Sprintf("%q: %s", name, err.Error())})
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		var val string
		switch {
		case strings.HasPrefix(rest, `"`), strings.HasPrefix(rest, "'"):
			q := rest[:1]
			body := rest[1:]
			var sb strings.Builder
			closed := false
			for {
				j := 0
				for j < len(body) {
					c := body[j]
					if q == `"` && c == '\\' && j+1 < len(body) {
						switch body[j+1] {
						case 'n':
							sb.WriteByte('\n')
						case 't':
							sb.WriteByte('\t')
						case '"', '\\':
							sb.WriteByte(body[j+1])
						default:
							sb.WriteByte(c)
							sb.WriteByte(body[j+1])
						}
						j += 2
						continue
					}
					if string(c) == q {
						closed = true
						break
					}
					sb.WriteByte(c)
					j++
				}
				if closed {
					break
				}
				i++
				if i >= len(lines) {
					break
				}
				sb.WriteByte('\n')
				body = lines[i]
			}
			if !closed {
				errs = append(errs, LineError{start, "Unterminated quoted value."})
				continue
			}
			val = sb.String()
		default:
			if k := strings.Index(rest, " #"); k >= 0 {
				rest = rest[:k]
			}
			val = strings.TrimSpace(rest)
		}
		if p, dup := seen[name]; dup {
			out[p].Value, out[p].Line = val, start
			continue
		}
		seen[name] = len(out)
		out = append(out, ParsedVar{Name: name, Value: val, Line: start})
	}
	return out, errs
}

var secretHints = []string{"SECRET", "TOKEN", "PASSWORD", "PASSWD", "PASS", "KEY", "CREDENTIAL", "PRIVATE", "AUTH", "DSN", "URI", "DATABASE_URL"}

// LooksSecret guesses whether a variable name should be stored as a secret.
func LooksSecret(name string) bool {
	u := strings.ToUpper(name)
	for _, h := range secretHints {
		if strings.Contains(u, h) {
			return true
		}
	}
	return false
}

// ImportResult summarises an import (or a dry run of one).
type ImportResult struct {
	Created []string    `json:"created"`
	Updated []string    `json:"updated"`
	Skipped []string    `json:"skipped"` // existed and overwrite was off
	Secrets []string    `json:"secrets"` // names stored as secrets
	Errors  []LineError `json:"errors"`
	DryRun  bool        `json:"dryRun"`
}

// ExportNames renders a names-only .env template: no values, secrets marked,
// generated connection variables listed as comments.
func ExportNames(l *Listing) string {
	var sb strings.Builder
	sb.WriteString("# Environment variables for this Nexmesh workspace (names only; values are not exported).\n")
	rows := append([]Row(nil), l.Variables...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	done := map[string]bool{}
	for _, r := range rows {
		if done[r.Name] {
			continue
		}
		done[r.Name] = true
		if r.Description != "" {
			sb.WriteString("# " + strings.ReplaceAll(r.Description, "\n", " ") + "\n")
		}
		if r.Secret {
			sb.WriteString("# secret\n")
		}
		sb.WriteString(r.Name + "=\n")
	}
	if len(l.Generated) > 0 {
		sb.WriteString("\n# Provided automatically by connected services (do not set):\n")
		for _, g := range l.Generated {
			sb.WriteString("# " + g.Name + "  (from " + g.ComponentName + ")\n")
		}
	}
	return sb.String()
}
