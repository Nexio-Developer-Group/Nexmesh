package rules

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/wsfs"
)

type memFS struct {
	mu    sync.Mutex
	files map[string]string
}

func (m *memFS) List(_ context.Context, _, dir string) ([]wsfs.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []wsfs.Entry
	for p, c := range m.files {
		if d, name, ok := cut(p); ok && d == dir {
			out = append(out, wsfs.Entry{Name: name, Path: p, Type: "file", Size: int64(len(c))})
		}
	}
	if len(out) == 0 {
		return nil, wsfs.ErrNotFound
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func cut(p string) (string, string, bool) {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "", p, false
	}
	return p[:i], p[i+1:], true
}
func (m *memFS) Read(_ context.Context, _, p string) (*wsfs.Text, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.files[p]
	if !ok {
		return nil, wsfs.ErrNotFound
	}
	return &wsfs.Text{Content: c}, nil
}
func (m *memFS) Write(_ context.Context, _, p, c string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[p] = c
	return nil
}
func (m *memFS) Mkdir(context.Context, string, string) error { return nil }
func (m *memFS) Delete(_ context.Context, _, p string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[p]; !ok {
		return wsfs.ErrNotFound
	}
	delete(m.files, p)
	return nil
}

func fx(t *testing.T) (*Service, *memFS, context.Context) {
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMemory()
	ctx := context.Background()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "w", Status: domain.WSReady})
	_ = st.CreateComponent(ctx, &domain.Component{ID: "c1", WorkspaceID: "ws1", CatalogID: "redis", Mode: domain.ModeInstalled})
	fs := &memFS{files: map[string]string{}}
	return New(st, fs, audit.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))), cat), fs, ctx
}

func TestParseFrontMatter(t *testing.T) {
	r := Parse(".nexmesh/rules/api.md", "---\ntitle: API style\nscope: path\npaths: [src/api/**]\nprotect: [migrations/]\n---\n\nUse snake_case.\n")
	if r.Title != "API style" || r.Scope != ScopePath || r.Paths[0] != "src/api/**" || r.Protect[0] != "migrations/" || r.Body != "Use snake_case." || r.Problem != "" {
		t.Fatalf("%+v", r)
	}
	g := Parse(".nexmesh/rules/plain.md", "Just do tests.\n")
	if g.Scope != ScopeGlobal || g.Title != "plain" || g.Body != "Just do tests." {
		t.Fatalf("%+v", g)
	}
	for name, in := range map[string]string{
		"bad yaml":      "---\ntitle: [unclosed\n---\nbody",
		"no components": "---\nscope: component\n---\nbody",
		"no paths":      "---\nscope: path\n---\nbody",
		"unknown scope": "---\nscope: galaxy\n---\nbody",
	} {
		if r := Parse("x.md", in); r.Problem == "" {
			t.Errorf("%s: expected a problem", name)
		}
	}
	if Parse("a.md", "x").Hash == Parse("a.md", "y").Hash {
		t.Fatal("hash must follow content")
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pat, file string
		want      bool
	}{
		{"src/api/**", "src/api/users/get.go", true}, {"src/api/**", "src/web/x.go", false}, {"src/api", "src/api/x.go", true},
		{"*.sql", "db/001_init.sql", true}, {"*.sql", "init.sql", true}, {"*.sql", "init.sql.bak", false},
		{"migrations/", "migrations/001.sql", true}, {"migrations/", "migrations", false}, {"migrations/", "app/migrations/001.sql", true}, {"/migrations/", "app/migrations/001.sql", false}, {"/migrations/", "migrations/001.sql", true},
		{"**/*.test.ts", "src/a/b.test.ts", true}, {"**/*.test.ts", "b.test.ts", true},
		{"src/*.go", "src/main.go", true}, {"src/*.go", "src/sub/main.go", false}, {"docs/?.md", "docs/a.md", true}, {"docs/?.md", "docs/ab.md", false},
		{"go.mod", "go.mod", true}, {"go.mod", "sub/go.mod", true}, {"", "x", false}, {"./src/**", "src/a", true}, {"a.b", "aXb", false},
	}
	for _, c := range cases {
		if got := GlobMatch(c.pat, c.file); got != c.want {
			t.Errorf("GlobMatch(%q,%q)=%v want %v", c.pat, c.file, got, c.want)
		}
	}
}

func TestApplicableAndProtected(t *testing.T) {
	all := []*Rule{
		Parse("g.md", "global"),
		Parse("comp.md", "---\nscope: component\ncomponents: [redis]\n---\nredis rule"),
		Parse("other.md", "---\nscope: component\ncomponents: [kafka]\n---\nkafka rule"),
		Parse("path.md", "---\nscope: path\npaths: [src/api/**]\nprotect: [infra/]\n---\napi rule"),
		Parse("bad.md", "---\nscope: path\n---\nno paths"),
	}
	cards := []Card{{Catalog: "redis", Name: "Redis"}}
	title := func(rs []*Rule) string {
		var s []string
		for _, r := range rs {
			s = append(s, r.Name)
		}
		return strings.Join(s, ",")
	}
	if got := title(Applicable(all, cards, []string{"src/api/a.go"})); got != "g,comp,path" {
		t.Fatalf("with api file: %s", got)
	}
	if got := title(Applicable(all, cards, []string{"README.md"})); got != "g,comp" {
		t.Fatalf("no path match: %s", got)
	}
	if got := title(Applicable(all, nil, nil)); got != "g" {
		t.Fatalf("empty canvas: %s", got)
	}
	if p := Protected(all, []string{"infra/main.tf", "src/api/a.go"}); len(p) != 1 || p["infra/main.tf"] == nil {
		t.Fatalf("%v", p)
	}
}

func TestSaveListDeleteAreRealFiles(t *testing.T) {
	s, fs, ctx := fx(t)
	r, err := s.Save(ctx, "u1", "ws1", SaveInput{Title: "API style!", Scope: ScopePath, Paths: []string{"src/api/**"}, Protect: []string{"migrations/"}, Body: "Use snake_case."})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != ".nexmesh/rules/api-style.md" {
		t.Fatalf("id %s", r.ID)
	}
	if !strings.HasPrefix(fs.files[r.ID], "---\n") || !strings.Contains(fs.files[r.ID], "Use snake_case.") {
		t.Fatalf("file: %q", fs.files[r.ID])
	}
	fs.files["AGENTS.md"] = "Be kind."
	list, _ := s.List(ctx, "u1", "ws1")
	if len(list) != 2 || list[0].ID != "AGENTS.md" || !list[0].ReadOnly || list[1].Paths[0] != "src/api/**" {
		t.Fatalf("%+v", list)
	}
	if err := s.Delete(ctx, "u1", "ws1", "api-style"); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.files[r.ID]; ok {
		t.Fatal("not deleted")
	}
	if err := s.Delete(ctx, "u1", "ws1", "api-style"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "u1", "ws1", "../../etc/passwd"); err == nil {
		t.Fatal("path traversal in delete name")
	}
	if _, err := s.List(ctx, "intruder", "ws1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("ownership")
	}
}

func TestSaveValidation(t *testing.T) {
	s, _, ctx := fx(t)
	bad := func(code string, in SaveInput) {
		t.Helper()
		_, err := s.Save(ctx, "u1", "ws1", in)
		var ve *runtime.ValidationError
		if !errors.As(err, &ve) || ve.Code != code {
			t.Fatalf("want %s got %v", code, err)
		}
	}
	bad("invalid_title", SaveInput{Scope: ScopeGlobal, Body: "x"})
	bad("empty_body", SaveInput{Title: "t", Scope: ScopeGlobal})
	bad("invalid_scope", SaveInput{Title: "t", Scope: "x", Body: "x"})
	bad("components_required", SaveInput{Title: "t", Scope: ScopeComponent, Body: "x"})
	bad("paths_required", SaveInput{Title: "t", Scope: ScopePath, Body: "x"})
	bad("invalid_path", SaveInput{Title: "t", Scope: ScopePath, Paths: []string{"../x"}, Body: "x"})
	bad("invalid_path", SaveInput{Title: "t", Scope: ScopeGlobal, Protect: []string{"../etc"}, Body: "x"})
	bad("invalid_name", SaveInput{Title: "!!!", Scope: ScopeGlobal, Body: "x"})
	bad("body_too_large", SaveInput{Title: "t", Scope: ScopeGlobal, Body: strings.Repeat("x", MaxBodySize+1)})
}

func TestEditsTakeEffectOnNextRead(t *testing.T) {
	s, _, ctx := fx(t)
	if got := s.ContextTexts(ctx, "ws1"); len(got) != 0 {
		t.Fatal(got)
	}
	_, _ = s.Save(ctx, "u1", "ws1", SaveInput{Title: "Tests", Scope: ScopeGlobal, Body: "Write tests first."})
	if got := s.ContextTexts(ctx, "ws1"); len(got) != 1 || !strings.Contains(got[0], "Write tests first.") {
		t.Fatalf("%v", got)
	}
	_, _ = s.Save(ctx, "u1", "ws1", SaveInput{Name: "tests", Title: "Tests", Scope: ScopeGlobal, Body: "Prefer table tests."})
	got := s.ContextTexts(ctx, "ws1")
	if len(got) != 1 || strings.Contains(got[0], "first") || !strings.Contains(got[0], "table tests") {
		t.Fatalf("edit not picked up: %v", got)
	}
}

func TestResolveRecordsVersionsAndWarnings(t *testing.T) {
	s, _, ctx := fx(t)
	_, _ = s.Save(ctx, "u1", "ws1", SaveInput{Title: "Global", Scope: ScopeGlobal, Body: "g", Protect: []string{"infra/"}})
	_, _ = s.Save(ctx, "u1", "ws1", SaveInput{Title: "Cache", Scope: ScopeComponent, Components: []string{"Redis"}, Body: "r"})
	_, _ = s.Save(ctx, "u1", "ws1", SaveInput{Title: "Kafka", Scope: ScopeComponent, Components: []string{"kafka"}, Body: "k"})
	_, _ = s.Save(ctx, "u1", "ws1", SaveInput{Title: "API", Scope: ScopePath, Paths: []string{"src/api/**"}, Body: "a"})
	res := s.Resolve(ctx, "ws1", []string{"infra/main.tf", "README.md"})
	var titles []string
	for _, r := range res.Rules {
		titles = append(titles, r.Title)
		if r.Hash == "" {
			t.Fatal("hash")
		}
	}
	sort.Strings(titles)
	if strings.Join(titles, ",") != "Cache,Global" {
		t.Fatalf("applied: %v", titles)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "infra/main.tf") {
		t.Fatalf("warnings: %v", res.Warnings)
	}
}
