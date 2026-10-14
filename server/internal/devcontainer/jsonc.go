package devcontainer

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// StripJSONC removes // and /* */ comments and trailing commas, leaving
// string contents untouched, so devcontainer.json (JSON with comments) can be
// decoded with encoding/json.
func StripJSONC(in []byte) []byte {
	var out bytes.Buffer
	i, n := 0, len(in)
	for i < n {
		c := in[i]
		switch {
		case c == '"':
			out.WriteByte(c)
			i++
			for i < n {
				out.WriteByte(in[i])
				if in[i] == '\\' && i+1 < n {
					out.WriteByte(in[i+1])
					i += 2
					continue
				}
				if in[i] == '"' {
					i++
					break
				}
				i++
			}
		case c == '/' && i+1 < n && in[i+1] == '/':
			for i < n && in[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && in[i+1] == '*':
			i += 2
			for i+1 < n && !(in[i] == '*' && in[i+1] == '/') {
				if in[i] == '\n' {
					out.WriteByte('\n')
				}
				i++
			}
			i += 2
		default:
			out.WriteByte(c)
			i++
		}
	}
	return removeTrailingCommas(out.Bytes())
}

func removeTrailingCommas(in []byte) []byte {
	var out bytes.Buffer
	n := len(in)
	for i := 0; i < n; i++ {
		c := in[i]
		if c == '"' {
			out.WriteByte(c)
			for i++; i < n; i++ {
				out.WriteByte(in[i])
				if in[i] == '\\' && i+1 < n {
					i++
					out.WriteByte(in[i])
					continue
				}
				if in[i] == '"' {
					break
				}
			}
			continue
		}
		if c == ',' {
			j := i + 1
			for j < n && (in[j] == ' ' || in[j] == '\t' || in[j] == '\n' || in[j] == '\r') {
				j++
			}
			if j < n && (in[j] == '}' || in[j] == ']') {
				continue // trailing comma
			}
		}
		out.WriteByte(c)
	}
	return out.Bytes()
}

// ParseJSONC decodes a devcontainer.json document.
func ParseJSONC(raw []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("The file is empty.")
	}
	var m map[string]any
	if err := json.Unmarshal(StripJSONC(raw), &m); err != nil {
		return nil, fmt.Errorf("This is not valid devcontainer.json (%s).", jsonErr(err))
	}
	if m == nil {
		return nil, fmt.Errorf("devcontainer.json must be a JSON object.")
	}
	return m, nil
}

func jsonErr(err error) string {
	if se, ok := err.(*json.SyntaxError); ok {
		return fmt.Sprintf("syntax error at byte %d", se.Offset)
	}
	if ute, ok := err.(*json.UnmarshalTypeError); ok {
		return "wrong type for " + ute.Field
	}
	return "unreadable JSON"
}
