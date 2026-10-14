// Package codeagent runs the Code Agent: a model that reads the canvas and the
// workspace, proposes file changes for review, and runs commands only within
// the permission level the user chose (PRD F17). The agent has no tool that
// reads secret values, changes the canvas, or touches anything outside the
// workspace; anything it asks for beyond its tools is refused and recorded.
package codeagent

import (
	"path"
	"regexp"
	"strings"
)

// Permission levels, each including the one before.
const (
	LevelRead    = "read"    // read files, list, search, see the canvas
	LevelPropose = "propose" // + stage changes for review (default)
	LevelExecute = "execute" // + run commands (risky ones need approval)
)

func levelRank(l string) int {
	switch l {
	case LevelRead:
		return 1
	case LevelPropose:
		return 2
	case LevelExecute:
		return 3
	}
	return 0
}

// ValidLevel reports whether l is a known permission level.
func ValidLevel(l string) bool { return levelRank(l) > 0 }

// Allows reports whether level may use a tool that needs min.
func Allows(level, min string) bool { return levelRank(level) >= levelRank(min) }

var secretFileNames = map[string]bool{
	".npmrc": true, ".netrc": true, ".pypirc": true, ".git-credentials": true, ".pgpass": true,
	"credentials": true, "credentials.json": true, "service-account.json": true, "secrets.json": true,
	"secrets.yml": true, "secrets.yaml": true, "id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
	"kubeconfig": true, ".dockercfg": true, "master.key": true,
}

var secretExt = map[string]bool{".pem": true, ".key": true, ".p12": true, ".pfx": true, ".jks": true, ".keystore": true, ".kdbx": true}

var envExamples = map[string]bool{".env.example": true, ".env.sample": true, ".env.template": true, ".env.dist": true}

// SecretPath reports whether a workspace path may hold secrets and so is
// never shown to the model or written by it. Secrets live in Variables.
func SecretPath(p string) bool {
	c := strings.ToLower(path.Clean(strings.ReplaceAll(p, "\\", "/")))
	base := path.Base(c)
	if envExamples[base] {
		return false
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env") {
		return true
	}
	if secretFileNames[base] || secretExt[path.Ext(base)] {
		return true
	}
	for _, seg := range strings.Split(c, "/") {
		if seg == ".ssh" || seg == ".aws" || seg == ".gnupg" || seg == ".kube" {
			return true
		}
	}
	return false
}

// CommandVerdict classifies a shell command.
type CommandVerdict int

const (
	// CmdSafe commands only read, build or test; they run without asking.
	CmdSafe CommandVerdict = iota
	// CmdRisky commands could change or delete things; they wait for approval.
	CmdRisky
	// CmdForbidden commands are never run, even with approval.
	CmdForbidden
)

var forbiddenCmd = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[\s;&|])(printenv|env|export\s+-p|set)\s*($|[;&|])`),
	regexp.MustCompile(`(?i)/proc/[^\s]*/environ`),
	regexp.MustCompile(`(?i)\bcompgen\s+-e\b`),
	regexp.MustCompile(`(?i)\$\{?[A-Z0-9_]*(KEY|SECRET|TOKEN|PASSWORD|PASSWD|PASS|CREDENTIAL|DSN|URI)[A-Z0-9_]*\}?`),
	regexp.MustCompile(`(?i)\brm\s+(-[a-z]*[rf][a-z]*\s+)+(/|~|\$HOME|/\*)(\s|$)`),
	regexp.MustCompile(`:\(\)\s*\{\s*:\s*\|\s*:`),
	regexp.MustCompile(`(?i)\bmkfs(\.\w+)?\b`),
	regexp.MustCompile(`(?i)\bdd\b[^|;&]*\bof=/dev/`),
	regexp.MustCompile(`(?i)>\s*/dev/(sd|nvme|vd)`),
	regexp.MustCompile(`(?i)\b(shutdown|reboot|halt|poweroff)\b`),
}

// safeStart lists commands that only read, build or test.
var safeStart = []*regexp.Regexp{
	regexp.MustCompile(`^(ls|pwd|echo|true|false|date|whoami|uname|which|wc|head|tail|sort|uniq|diff|stat|file|tree|du|df|basename|dirname|realpath)(\s|$)`),
	regexp.MustCompile(`^(cat|grep|egrep|fgrep|rg|less|more)(\s|$)`),
	regexp.MustCompile(`^find\s`),
	regexp.MustCompile(`^git\s+(status|diff|log|show|branch|rev-parse|ls-files|blame|remote\s+-v|config\s+--get)(\s|$)`),
	regexp.MustCompile(`^go\s+(test|vet|build|version|list|env\s+GO\w+)(\s|$)`),
	regexp.MustCompile(`^(npm|pnpm|yarn)\s+(test|run\s+(test|lint|build|typecheck|check)[\w:-]*|ls|list|outdated)(\s|$)`),
	regexp.MustCompile(`^(pytest|python3?\s+-m\s+(pytest|unittest|mypy|ruff|flake8|black\s+--check)|ruff|mypy|flake8|tsc\s+--noEmit|eslint|prettier\s+--check)(\s|$)`),
	regexp.MustCompile(`^(cargo|mvn|gradle|dotnet)\s+(test|check|build|verify)(\s|$)`),
	regexp.MustCompile(`^make\s+(test|check|lint|build|verify)[\w:-]*(\s|$)`),
	regexp.MustCompile(`^(node|python3?|go|java|rustc)\s+(--version|-v|version)(\s|$)`),
}

var unsafeFind = regexp.MustCompile(`(?i)\s-(delete|exec|execdir|ok|fprint\w*)\b`)
var shellMeta = regexp.MustCompile("[><`]|\\$\\(|\\$\\{")
var segSplit = regexp.MustCompile(`\s*(\|\||&&|;|\|)\s*`)

// ClassifyCommand decides how a command is handled. Every part of a compound
// command must be safe for the whole to be safe; redirections and substitution
// make a command risky because they can write files outside change review.
func ClassifyCommand(cmd string) (CommandVerdict, string) {
	c := strings.TrimSpace(cmd)
	if c == "" {
		return CmdForbidden, "The command is empty."
	}
	if len(c) > 4000 {
		return CmdForbidden, "The command is too long."
	}
	if strings.ContainsAny(c, "\x00") {
		return CmdForbidden, "The command contains invalid characters."
	}
	for _, re := range forbiddenCmd {
		if re.MatchString(c) {
			return CmdForbidden, "That command could expose secrets or damage the machine, so Nexmesh never runs it."
		}
	}
	if strings.Contains(c, "\n") {
		return CmdRisky, "It runs several lines."
	}
	if shellMeta.MatchString(c) {
		return CmdRisky, "It uses redirection or substitution, which can write files without review."
	}
	for _, seg := range segSplit.Split(c, -1) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if unsafeFind.MatchString(seg) {
			return CmdRisky, "find with -delete or -exec can change files."
		}
		for _, f := range strings.Fields(seg) {
			if !strings.HasPrefix(f, "-") && SecretPath(f) {
				return CmdForbidden, "That command reads a file that can hold secrets (" + f + "). Secrets live in Variables and are never shown to the agent."
			}
		}
		ok := false
		for _, re := range safeStart {
			if re.MatchString(seg) {
				ok = true
				break
			}
		}
		if !ok {
			return CmdRisky, "It is not on the list of read-only, build and test commands."
		}
	}
	return CmdSafe, ""
}
