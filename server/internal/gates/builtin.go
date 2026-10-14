package gates

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/nexio-developer-group/nexmesh/server/internal/secretscan"
	"regexp"
	"sort"
	"strings"
)

// Built-in gates run in the control plane over the workspace's text files, so
// they work without any tool installed in the workspace.
const (
	BuiltinSecrets = "builtin:secrets"
	BuiltinDeps    = "builtin:deps"
)

var placeholderRe = regexp.MustCompile(`(?i)(example|placeholder|changeme|your[_-]|<[^>]+>|xxxx|\*\*\*\*|dummy|sample|fake|test)`)

var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true, ".venv": true, "target": true, "__pycache__": true, ".next": true}

func isSecretFile(p string) bool {
	b := strings.ToLower(p[strings.LastIndex(p, "/")+1:])
	return b == ".env" || (strings.HasPrefix(b, ".env.") && !strings.HasSuffix(b, ".example") && !strings.HasSuffix(b, ".sample") && !strings.HasSuffix(b, ".template"))
}

func mask(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "…" + "(" + fmt.Sprint(len(s)) + " chars)"
}

// scanSecrets looks for secret-shaped text. It never prints the match.
func (s *Service) scanSecrets(ctx context.Context, wsID string) (ok bool, log string) {
	const maxFiles, maxBytes, maxFindings = 600, 200 << 10, 50
	queue := []string{"."}
	files := 0
	var findings []string
	for len(queue) > 0 && files < maxFiles && ctx.Err() == nil {
		dir := queue[0]
		queue = queue[1:]
		es, err := s.fs.List(ctx, wsID, dir)
		if err != nil {
			continue
		}
		sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
		for _, e := range es {
			switch {
			case e.Type == "dir":
				if !skipDirs[e.Name] {
					queue = append(queue, e.Path)
				}
			case e.Type == "file" && e.Size <= maxBytes && !isSecretFile(e.Path):
				files++
				if files > maxFiles {
					break
				}
				t, err := s.fs.Read(ctx, wsID, e.Path)
				if err != nil {
					continue
				}
				for i, line := range strings.Split(t.Content, "\n") {
					if len(line) > 2000 {
						continue
					}
					if name, m, ok := secretscan.Find(line); ok && !placeholderRe.MatchString(m) {
						findings = append(findings, fmt.Sprintf("%s:%d  possible %s (%s)", e.Path, i+1, name, mask(m)))
					}
				}
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Scanned %d text file(s).\n", files)
	if len(findings) == 0 {
		b.WriteString("No secret-like text found.\n")
		return true, b.String()
	}
	if len(findings) > maxFindings {
		findings = append(findings[:maxFindings], "… and more")
	}
	b.WriteString("Found text that looks like a secret. Move it to Variables and read it from the environment:\n")
	for _, f := range findings {
		b.WriteString("  " + f + "\n")
	}
	return false, b.String()
}

var floatingVersion = regexp.MustCompile(`^(\*|latest|next|x|)$`)

// checkDeps does basic hygiene checks on dependency files. It is not a
// vulnerability scanner.
func (s *Service) checkDeps(ctx context.Context, wsID string) (ok bool, log string) {
	var problems []string
	var b strings.Builder
	read := func(p string) (string, bool) {
		t, err := s.fs.Read(ctx, wsID, p)
		if err != nil {
			return "", false
		}
		return t.Content, true
	}
	checked := 0
	if pj, found := read("package.json"); found {
		checked++
		var pkg struct {
			Deps map[string]string `json:"dependencies"`
			Dev  map[string]string `json:"devDependencies"`
		}
		if err := json.Unmarshal([]byte(pj), &pkg); err != nil {
			problems = append(problems, "package.json is not valid JSON.")
		} else {
			n := len(pkg.Deps) + len(pkg.Dev)
			_, a := read("package-lock.json")
			_, p := read("pnpm-lock.yaml")
			_, y := read("yarn.lock")
			_, bn := read("bun.lockb")
			if n > 0 && !(a || p || y || bn) {
				problems = append(problems, "package.json has dependencies but there is no lockfile, so installs are not reproducible.")
			}
			for _, group := range []map[string]string{pkg.Deps, pkg.Dev} {
				names := make([]string, 0, len(group))
				for k := range group {
					names = append(names, k)
				}
				sort.Strings(names)
				for _, name := range names {
					v := strings.TrimSpace(group[name])
					switch {
					case floatingVersion.MatchString(v):
						problems = append(problems, fmt.Sprintf("%s uses the floating version %q.", name, v))
					case strings.HasPrefix(v, "git+") || strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "github:"):
						problems = append(problems, fmt.Sprintf("%s is installed from a URL, not a registry version.", name))
					}
				}
			}
		}
	}
	if rq, found := read("requirements.txt"); found {
		checked++
		for i, line := range strings.Split(rq, "\n") {
			l := strings.TrimSpace(line)
			if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "-") {
				continue
			}
			if !strings.Contains(l, "==") && !strings.Contains(l, "@") {
				problems = append(problems, fmt.Sprintf("requirements.txt line %d (%s) is not pinned with ==.", i+1, truncate(l, 40)))
			}
		}
	}
	if gm, found := read("go.mod"); found {
		checked++
		if _, sum := read("go.sum"); !sum && strings.Contains(gm, "require") {
			problems = append(problems, "go.mod has requirements but go.sum is missing.")
		}
		if strings.Contains(gm, "replace ") && strings.Contains(gm, "=> ../") {
			problems = append(problems, "go.mod has a replace directive pointing outside the repository.")
		}
	}
	if cg, found := read("Cargo.toml"); found {
		checked++
		if _, lock := read("Cargo.lock"); !lock && strings.Contains(cg, "[dependencies]") {
			problems = append(problems, "Cargo.toml has dependencies but Cargo.lock is missing.")
		}
	}
	if checked == 0 {
		return true, "No dependency files found, so there is nothing to check.\n"
	}
	fmt.Fprintf(&b, "Checked %d dependency file(s). This is a hygiene check, not a vulnerability scan.\n", checked)
	if len(problems) == 0 {
		b.WriteString("No problems found.\n")
		return true, b.String()
	}
	for _, p := range problems {
		b.WriteString("  - " + p + "\n")
	}
	return false, b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
