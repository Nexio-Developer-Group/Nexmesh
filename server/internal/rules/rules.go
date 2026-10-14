// Package rules reads and edits the agent rules files stored in the workspace's
// repository (PRD F21). Rules are Markdown files under .nexmesh/rules with a
// small front matter that says where they apply. They are versioned with the
// code because they are ordinary files, and they are read fresh on every agent
// request, so an edit takes effect immediately. Rules are guidance for the
// agent; the only enforcement is through quality gates (F23).
package rules

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/wsfs"
)

const (
	Dir         = ".nexmesh/rules"
	RootFile    = "AGENTS.md"
	MaxRules    = 50
	MaxBodySize = 32 << 10
)

// Scopes.
const (
	ScopeGlobal    = "global"
	ScopeComponent = "component"
	ScopePath      = "path"
)

// FS is the workspace file access needed here.
type FS interface {
	List(ctx context.Context, ws, path string) ([]wsfs.Entry, error)
	Read(ctx context.Context, ws, path string) (*wsfs.Text, error)
	Write(ctx context.Context, ws, path, content string, expected time.Time) error
	Mkdir(ctx context.Context, ws, path string) error
	Delete(ctx context.Context, ws, path string, recursive bool) error
}

type Rule struct {
	// ID is the file path, which is also what the editor saves to.
	ID         string   `json:"id"`
	Name       string   `json:"name"` // file name without extension
	Title      string   `json:"title"`
	Scope      string   `json:"scope"`
	Components []string `json:"components,omitempty"`
	Paths      []string `json:"paths,omitempty"`
	Protect    []string `json:"protect,omitempty"`
	Body       string   `json:"body"`
	Hash       string   `json:"hash"`
	ReadOnly   bool     `json:"readOnly,omitempty"`
	// Problem is set when the file could not be read as a rule; it is shown, not applied.
	Problem string `json:"problem,omitempty"`
}

type frontMatter struct {
	Title      string   `yaml:"title"`
	Scope      string   `yaml:"scope"`
	Components []string `yaml:"components"`
	Paths      []string `yaml:"paths"`
	Protect    []string `yaml:"protect"`
}

type Service struct {
	fs  FS
	st  store.Store
	aud *audit.Recorder
	cat *catalog.Catalog
}

func New(st store.Store, fs FS, aud *audit.Recorder, cat *catalog.Catalog) *Service {
	return &Service{fs: fs, st: st, aud: aud, cat: cat}
}

func verr(field, code, msg string) error {
	return &runtime.ValidationError{Field: field, Code: code, Message: msg}
}

func (s *Service) owns(ctx context.Context, owner, wsID string) error {
	w, err := s.st.GetWorkspace(ctx, wsID)
	if err != nil || w.OwnerID != owner {
		return store.ErrNotFound
	}
	return nil
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:6])
}

// Parse reads a rule file. The front matter is optional: a file with none is a
// global rule titled after its file name.
func Parse(file, content string) *Rule {
	r := &Rule{ID: file, Name: strings.TrimSuffix(path.Base(file), path.Ext(file)), Scope: ScopeGlobal, Hash: hash(content)}
	body := content
	if rest, ok := strings.CutPrefix(strings.ReplaceAll(content, "\r\n", "\n"), "---\n"); ok {
		if fm, after, ok := strings.Cut(rest, "\n---"); ok {
			var m frontMatter
			if err := yaml.Unmarshal([]byte(fm), &m); err != nil {
				r.Problem = "The front matter is not valid YAML."
				r.Title = r.Name
				r.Body = content
				return r
			}
			r.Title, r.Scope, r.Components, r.Paths, r.Protect = m.Title, m.Scope, m.Components, m.Paths, m.Protect
			body = strings.TrimPrefix(after, "\n")
			body = strings.TrimPrefix(body, "\n")
		}
	}
	if r.Title == "" {
		r.Title = r.Name
	}
	r.Body = strings.TrimSpace(body)
	switch r.Scope {
	case "", ScopeGlobal:
		r.Scope = ScopeGlobal
	case ScopeComponent:
		if len(r.Components) == 0 {
			r.Problem = "A component rule needs at least one component."
		}
	case ScopePath:
		if len(r.Paths) == 0 {
			r.Problem = "A path rule needs at least one path pattern."
		}
	default:
		r.Problem = "Scope must be global, component or path."
		r.Scope = ScopeGlobal
	}
	return r
}

// List reads every rules file in the workspace, sorted by name.
func (s *Service) List(ctx context.Context, owner, wsID string) ([]*Rule, error) {
	if err := s.owns(ctx, owner, wsID); err != nil {
		return nil, err
	}
	return s.read(ctx, wsID), nil
}

func (s *Service) read(ctx context.Context, wsID string) []*Rule {
	var out []*Rule
	if t, err := s.fs.Read(ctx, wsID, RootFile); err == nil && len(t.Content) <= MaxBodySize {
		r := Parse(RootFile, t.Content)
		r.Scope, r.Title, r.ReadOnly, r.Problem = ScopeGlobal, RootFile, true, ""
		r.Components, r.Paths, r.Protect = nil, nil, nil
		r.Body = strings.TrimSpace(t.Content)
		out = append(out, r)
	}
	es, err := s.fs.List(ctx, wsID, Dir)
	if err != nil {
		return out
	}
	sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
	n := 0
	for _, e := range es {
		if e.Type != "file" || !strings.HasSuffix(strings.ToLower(e.Name), ".md") {
			continue
		}
		if n++; n > MaxRules {
			break
		}
		if e.Size > MaxBodySize {
			out = append(out, &Rule{ID: e.Path, Name: strings.TrimSuffix(e.Name, path.Ext(e.Name)), Title: e.Name, Scope: ScopeGlobal, Problem: "This file is larger than 32 KB, so it is not used."})
			continue
		}
		t, err := s.fs.Read(ctx, wsID, e.Path)
		if err != nil {
			continue
		}
		out = append(out, Parse(e.Path, t.Content))
	}
	return out
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug makes a safe file name from a title.
func Slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	return s
}

// SaveInput is what the editor sends.
type SaveInput struct {
	Name       string   `json:"name"` // file name without extension; empty derives it from the title
	Title      string   `json:"title"`
	Scope      string   `json:"scope"`
	Components []string `json:"components"`
	Paths      []string `json:"paths"`
	Protect    []string `json:"protect"`
	Body       string   `json:"body"`
}

// Render writes a rule as a file.
func Render(in SaveInput) (string, error) {
	fm := frontMatter{Title: strings.TrimSpace(in.Title), Scope: in.Scope, Components: clean(in.Components), Paths: clean(in.Paths), Protect: clean(in.Protect)}
	b, err := yaml.Marshal(fm)
	if err != nil {
		return "", err
	}
	return "---\n" + string(b) + "---\n\n" + strings.TrimSpace(in.Body) + "\n", nil
}

func clean(l []string) []string {
	var out []string
	for _, x := range l {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// Save writes (creates or replaces) a rules file.
func (s *Service) Save(ctx context.Context, owner, wsID string, in SaveInput) (*Rule, error) {
	if err := s.owns(ctx, owner, wsID); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" || len(title) > 100 {
		return nil, verr("title", "invalid_title", "Give the rule a title of up to 100 characters.")
	}
	if strings.TrimSpace(in.Body) == "" {
		return nil, verr("body", "empty_body", "Write the rule.")
	}
	if len(in.Body) > MaxBodySize {
		return nil, verr("body", "body_too_large", "A rule can be up to 32 KB.")
	}
	switch in.Scope {
	case ScopeGlobal:
	case ScopeComponent:
		if len(clean(in.Components)) == 0 {
			return nil, verr("components", "components_required", "Name the components this rule applies to.")
		}
	case ScopePath:
		if len(clean(in.Paths)) == 0 {
			return nil, verr("paths", "paths_required", "Give the paths this rule applies to, such as src/api/**.")
		}
	default:
		return nil, verr("scope", "invalid_scope", "Choose global, component or path.")
	}
	for _, p := range append(clean(in.Paths), clean(in.Protect)...) {
		if strings.Contains(p, "..") || strings.ContainsRune(p, 0) {
			return nil, verr("paths", "invalid_path", "Path patterns are relative to the workspace folder.")
		}
	}
	name := Slug(in.Name)
	if name == "" {
		name = Slug(title)
	}
	if name == "" {
		return nil, verr("name", "invalid_name", "Use letters and numbers in the rule's name.")
	}
	text, err := Render(in)
	if err != nil {
		return nil, err
	}
	file := Dir + "/" + name + ".md"
	if err := s.fs.Mkdir(ctx, wsID, Dir); err != nil {
		return nil, err
	}
	if err := s.fs.Write(ctx, wsID, file, text, time.Time{}); err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, wsID, "rules.saved", file, map[string]any{"scope": in.Scope})
	return Parse(file, text), nil
}

// Delete removes a rules file.
func (s *Service) Delete(ctx context.Context, owner, wsID, name string) error {
	if err := s.owns(ctx, owner, wsID); err != nil {
		return err
	}
	n := Slug(name)
	if n == "" || n != name {
		return verr("name", "invalid_name", "That is not a rule name.")
	}
	file := Dir + "/" + n + ".md"
	if err := s.fs.Delete(ctx, wsID, file, false); err != nil {
		if err == wsfs.ErrNotFound {
			return store.ErrNotFound
		}
		return err
	}
	s.aud.User(ctx, owner, wsID, "rules.deleted", file, nil)
	return nil
}

// Card identifies a canvas card for component rules.
type Card struct{ Catalog, Name string }

func matchesCard(r *Rule, cards []Card) bool {
	for _, want := range r.Components {
		w := strings.ToLower(strings.TrimSpace(want))
		for _, c := range cards {
			if w == strings.ToLower(c.Catalog) || strings.EqualFold(w, c.Name) || strings.HasPrefix(strings.ToLower(c.Name), w+" (") {
				return true
			}
		}
	}
	return false
}

func matchesAnyPath(patterns []string, file string) bool {
	for _, p := range patterns {
		if GlobMatch(p, file) {
			return true
		}
	}
	return false
}

// Applicable returns the rules that apply to work touching files, given the
// canvas cards: global rules, component rules whose component is on the
// canvas, and path rules matching at least one file. Rules with a problem are
// never applied. With no files, path rules are left out.
func Applicable(all []*Rule, cards []Card, files []string) []*Rule {
	var out []*Rule
	for _, r := range all {
		if r.Problem != "" {
			continue
		}
		switch r.Scope {
		case ScopeGlobal:
			out = append(out, r)
		case ScopeComponent:
			if matchesCard(r, cards) {
				out = append(out, r)
			}
		case ScopePath:
			for _, f := range files {
				if matchesAnyPath(r.Paths, f) {
					out = append(out, r)
					break
				}
			}
		}
	}
	return out
}

// Protected returns the files that fall under any rule's protected paths.
func Protected(all []*Rule, files []string) map[string][]string {
	out := map[string][]string{}
	for _, r := range all {
		if r.Problem != "" {
			continue
		}
		for _, f := range files {
			if matchesAnyPath(r.Protect, f) {
				out[f] = append(out[f], r.Title)
			}
		}
	}
	return out
}

// Text renders a rule for the agent's context.
func Text(r *Rule) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s", r.Title)
	switch r.Scope {
	case ScopeComponent:
		fmt.Fprintf(&b, " (applies to %s)", strings.Join(r.Components, ", "))
	case ScopePath:
		fmt.Fprintf(&b, " (applies to files matching %s)", strings.Join(r.Paths, ", "))
	}
	b.WriteString("\n" + r.Body)
	if len(r.Protect) > 0 {
		fmt.Fprintf(&b, "\nProtected paths, do not change unless the user explicitly asks: %s", strings.Join(r.Protect, ", "))
	}
	return b.String()
}

// GlobMatch matches a workspace path against a pattern. ** crosses folders, *
// and ? stay within one. A pattern with no slash matches a file or folder of
// that name anywhere; a trailing slash means everything under that folder.
func GlobMatch(pattern, file string) bool {
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	file = path.Clean(strings.ReplaceAll(file, "\\", "/"))
	if pattern == "" {
		return false
	}
	dirOnly := strings.HasSuffix(pattern, "/")
	pattern = strings.TrimSuffix(pattern, "/")
	pattern = strings.TrimPrefix(pattern, "./")
	anchored := strings.HasPrefix(pattern, "/")
	pattern = strings.TrimPrefix(pattern, "/")
	anywhere := !anchored && !strings.Contains(pattern, "/")
	var re strings.Builder
	re.WriteString("^")
	if anywhere {
		re.WriteString("(?:.*/)?")
	}
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					re.WriteString("(?:.*/)?")
				} else {
					re.WriteString(".*")
				}
			} else {
				re.WriteString("[^/]*")
			}
		case '?':
			re.WriteString("[^/]")
		default:
			re.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	if dirOnly {
		re.WriteString("/.+$")
	} else {
		re.WriteString("(?:/.*)?$")
	}
	ok, _ := regexp.MatchString(re.String(), file)
	return ok
}

// Cards lists the canvas cards for component rules.
func (s *Service) Cards(ctx context.Context, wsID string) []Card {
	comps, err := s.st.ListComponents(ctx, wsID)
	if err != nil {
		return nil
	}
	var out []Card
	for _, c := range comps {
		if def, ok := s.cat.Get(c.CatalogID); ok {
			out = append(out, Card{Catalog: c.CatalogID, Name: def.Name})
		}
	}
	return out
}

// Applied is a rule that applied to a piece of work, with the version of the
// text that was used so the record stays accurate after later edits.
type Applied struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Scope string `json:"scope"`
	Hash  string `json:"hash"`
}

// Resolution is the rules that apply to a set of files, and a warning for each
// file under a protected path.
type Resolution struct {
	Rules    []Applied
	Warnings []string
}

// Resolve reads the rules fresh and decides which apply to files.
func (s *Service) Resolve(ctx context.Context, wsID string, files []string) Resolution {
	all := s.read(ctx, wsID)
	var res Resolution
	for _, r := range Applicable(all, s.Cards(ctx, wsID), files) {
		res.Rules = append(res.Rules, Applied{ID: r.ID, Title: r.Title, Scope: r.Scope, Hash: r.Hash})
	}
	prot := Protected(all, files)
	paths := make([]string, 0, len(prot))
	for f := range prot {
		paths = append(paths, f)
	}
	sort.Strings(paths)
	for _, f := range paths {
		res.Warnings = append(res.Warnings, f+" is a protected path ("+strings.Join(prot[f], ", ")+"). Check this change carefully.")
	}
	return res
}

// ContextTexts returns the rule texts the agent sees: global rules, component
// rules for cards on the canvas, and every path rule (labelled with its paths).
func (s *Service) ContextTexts(ctx context.Context, wsID string) []string {
	all := s.read(ctx, wsID)
	cards := s.Cards(ctx, wsID)
	var out []string
	for _, r := range all {
		if r.Problem != "" {
			continue
		}
		if r.Scope == ScopeComponent && !matchesCard(r, cards) {
			continue
		}
		out = append(out, Text(r))
	}
	return out
}
