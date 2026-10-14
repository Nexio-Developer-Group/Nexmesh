// Package secretscan recognises text shaped like credentials. It is used to
// find secrets in code (quality gates) and to remove them from anything sent
// to a model (PRD F19, 18.2). Matching is pattern based and deliberately
// broad; it complements, and never replaces, the exact-value masking that
// knows the workspace's real secrets.
package secretscan

import "regexp"

type Pattern struct {
	Name string
	Re   *regexp.Regexp
}

var Patterns = []Pattern{
	{"private key", regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----[\s\S]*?(?:-----END (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----|$)`)},
	{"AWS access key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"GitHub token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}\b`)},
	{"Slack token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
	{"Stripe live key", regexp.MustCompile(`\b[sr]k_live_[A-Za-z0-9]{16,}\b`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"model API key", regexp.MustCompile(`\bsk-(?:ant-)?[A-Za-z0-9_-]{24,}\b`)},
	{"bearer token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{20,}`)},
	{"JSON web token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},
	{"hard-coded credential", regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret|token|passwd|password|pwd)\b\s*[:=]\s*['"][^'"\s]{12,}['"]`)},
	{"connection string with password", regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^\s:/@'"]+:[^\s@'"]{3,}@[^\s'"]+`)},
}

// scrubOnly are broader shapes used only when removing text, where a false
// positive costs nothing: unquoted assignments such as in logs and env dumps.
var scrubOnly = []Pattern{
	{"credential assignment", regexp.MustCompile(`(?i)\b[A-Za-z0-9_]*(?:api[_-]?key|secret|token|passwd|password|pwd)\b\s*[:=]\s*['"]?[^'"\s,;]{6,}['"]?`)},
}

// Find returns the first pattern that matches line.
func Find(line string) (name, match string, ok bool) {
	for _, p := range Patterns {
		if m := p.Re.FindString(line); m != "" {
			return p.Name, m, true
		}
	}
	return "", "", false
}

// Scrub replaces every credential-shaped match with [hidden] and says how many it replaced.
func Scrub(text string) (string, int) {
	n := 0
	for _, p := range append(append([]Pattern{}, Patterns...), scrubOnly...) {
		text = p.Re.ReplaceAllStringFunc(text, func(string) string { n++; return "[hidden]" })
	}
	return text, n
}
