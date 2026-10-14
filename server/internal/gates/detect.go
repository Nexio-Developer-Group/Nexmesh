package gates

import (
	"context"
	"encoding/json"
	"strings"
)

// Detect looks at the project files in the workspace root and suggests gates.
// Nothing is saved: the user reviews the list and confirms it (PRD F23).
func (s *Service) Detect(ctx context.Context, owner, wsID string) ([]Gate, error) {
	if err := s.owns(ctx, owner, wsID); err != nil {
		return nil, err
	}
	read := func(p string) (string, bool) {
		t, err := s.fs.Read(ctx, wsID, p)
		if err != nil {
			return "", false
		}
		return t.Content, true
	}
	var out []Gate
	add := func(id, name, kind, cmd, why string, policy string) {
		out = append(out, Gate{ID: id, Name: name, Kind: kind, Command: cmd, Policy: policy, Enabled: true, TimeoutSec: DefaultTimeout, Detected: why})
	}
	if _, ok := read("go.mod"); ok {
		add("go-format", "Go format", KindFormat, `test -z "$(gofmt -l .)"`, "go.mod found", PolicyAdvisory)
		add("go-vet", "Go vet", KindLint, "go vet ./...", "go.mod found", PolicyRequired)
		add("go-test", "Go tests", KindTest, "go test ./...", "go.mod found", PolicyRequired)
		add("go-build", "Go build", KindBuild, "go build ./...", "go.mod found", PolicyRequired)
	}
	if pj, ok := read("package.json"); ok {
		var pkg struct {
			Scripts         map[string]string `json:"scripts"`
			DependencyNames map[string]string `json:"dependencies"`
			Dev             map[string]string `json:"devDependencies"`
		}
		_ = json.Unmarshal([]byte(pj), &pkg)
		pm := "npm"
		if _, ok := read("pnpm-lock.yaml"); ok {
			pm = "pnpm"
		} else if _, ok := read("yarn.lock"); ok {
			pm = "yarn"
		}
		run := func(script string) string {
			if pm == "npm" {
				return "npm run " + script
			}
			return pm + " run " + script
		}
		pick := func(names ...string) string {
			for _, n := range names {
				if _, ok := pkg.Scripts[n]; ok {
					return n
				}
			}
			return ""
		}
		if sc := pick("format:check", "prettier:check", "check-format", "format-check"); sc != "" {
			add("node-format", "Format check", KindFormat, run(sc), "package.json script "+sc, PolicyAdvisory)
		}
		if sc := pick("lint", "lint:check", "eslint"); sc != "" {
			add("node-lint", "Lint", KindLint, run(sc), "package.json script "+sc, PolicyRequired)
		}
		if sc := pick("typecheck", "type-check", "tsc", "check-types"); sc != "" {
			add("node-typecheck", "Type check", KindTypecheck, run(sc), "package.json script "+sc, PolicyRequired)
		} else if _, ok := read("tsconfig.json"); ok {
			add("node-typecheck", "Type check", KindTypecheck, "npx --no-install tsc --noEmit", "tsconfig.json found", PolicyRequired)
		}
		if sc := pick("test", "test:unit"); sc != "" && !strings.Contains(pkg.Scripts[sc], "no test specified") {
			add("node-test", "Tests", KindTest, run(sc), "package.json script "+sc, PolicyRequired)
		}
		if sc := pick("build"); sc != "" {
			add("node-build", "Build", KindBuild, run(sc), "package.json script "+sc, PolicyRequired)
		}
	}
	pyproject, hasPyproject := read("pyproject.toml")
	_, hasReq := read("requirements.txt")
	if hasPyproject || hasReq {
		if strings.Contains(pyproject, "[tool.ruff") {
			add("py-lint", "Ruff lint", KindLint, "ruff check .", "[tool.ruff] in pyproject.toml", PolicyRequired)
			add("py-format", "Ruff format", KindFormat, "ruff format --check .", "[tool.ruff] in pyproject.toml", PolicyAdvisory)
		} else if strings.Contains(pyproject, "[tool.black") {
			add("py-format", "Black", KindFormat, "black --check .", "[tool.black] in pyproject.toml", PolicyAdvisory)
		}
		if strings.Contains(pyproject, "[tool.mypy") {
			add("py-typecheck", "Mypy", KindTypecheck, "mypy .", "[tool.mypy] in pyproject.toml", PolicyRequired)
		}
		_, hasTests := read("tests/__init__.py")
		_, hasConftest := read("conftest.py")
		if strings.Contains(pyproject, "[tool.pytest") || strings.Contains(pyproject, "pytest") || hasTests || hasConftest {
			add("py-test", "Pytest", KindTest, "pytest -q", "pytest configuration found", PolicyRequired)
		}
	}
	if _, ok := read("Cargo.toml"); ok {
		add("rust-format", "Cargo fmt", KindFormat, "cargo fmt --check", "Cargo.toml found", PolicyAdvisory)
		add("rust-lint", "Cargo clippy", KindLint, "cargo clippy -- -D warnings", "Cargo.toml found", PolicyRequired)
		add("rust-test", "Cargo test", KindTest, "cargo test", "Cargo.toml found", PolicyRequired)
		add("rust-build", "Cargo build", KindBuild, "cargo build", "Cargo.toml found", PolicyRequired)
	}
	if mk, ok := read("Makefile"); ok && len(out) == 0 {
		for _, target := range []string{"lint", "test", "build"} {
			if strings.Contains(mk, "\n"+target+":") || strings.HasPrefix(mk, target+":") {
				kind := map[string]string{"lint": KindLint, "test": KindTest, "build": KindBuild}[target]
				add("make-"+target, "make "+target, kind, "make "+target, "Makefile target "+target, PolicyRequired)
			}
		}
	}
	add("secrets", "Secret scan", KindSecrets, BuiltinSecrets, "built in", PolicyRequired)
	add("deps", "Dependency check", KindDeps, BuiltinDeps, "built in", PolicyAdvisory)
	return out, nil
}
