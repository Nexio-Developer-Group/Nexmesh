package architecture_test

import (
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/architecture"
	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/wiring"
)

type rig struct {
	ctx  context.Context
	st   *store.Memory
	wire *wiring.Service
	arch *architecture.Service
}

func newRig(t *testing.T) *rig {
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	bus := events.NewBus()
	aud := audit.New(st, log)
	comp := component.NewService(st, cat, bus, aud)
	ctx := context.Background()
	_ = st.CreateWorkspace(ctx, &domain.Workspace{ID: "ws1", OwnerID: "u1", Name: "shop", Status: domain.WSReady})
	w := wiring.New(st, cat, comp, bus, aud)
	return &rig{ctx, st, w, architecture.New(st, cat, w)}
}

func (r *rig) card(t *testing.T, id, mode, state string) *domain.Component {
	c := &domain.Component{ID: store.NewID("cmp"), WorkspaceID: "ws1", CatalogID: id, Mode: mode, Observed: state, Version: "1.0", Settings: map[string]any{}}
	if err := r.st.CreateComponent(r.ctx, c); err != nil {
		t.Fatal(err)
	}
	return c
}

func (r *rig) link(t *testing.T, a, b *domain.Component, name string) *wiring.View {
	v, err := r.wire.Create(r.ctx, domain.ActorUser, "u1", "u1", "ws1", wiring.Input{FromID: a.ID, ToID: b.ID, Name: name, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestViewGroupsByLayerAndMatchesTheCanvas(t *testing.T) {
	r := newRig(t)
	react := r.card(t, "react", "installed", "ready")
	nest := r.card(t, "nestjs", "installed", "ready")
	mongo := r.card(t, "mongodb", "installed", "running")
	redis := r.card(t, "redis", "installed", "running")
	r.link(t, react, nest, "api")
	r.link(t, nest, mongo, "")
	r.link(t, nest, redis, "")

	v, err := r.arch.Build(r.ctx, "u1", "ws1", nil)
	if err != nil {
		t.Fatal(err)
	}
	layers := map[string]string{}
	for _, n := range v.Nodes {
		layers[n.Catalog] = n.Layer
	}
	want := map[string]string{"react": "frontend", "nestjs": "backend", "mongodb": "data", "redis": "messaging"}
	for k, l := range want {
		if layers[k] != l {
			t.Errorf("%s in layer %q, want %q", k, layers[k], l)
		}
	}
	if len(v.Edges) != 3 || len(v.Drift) != 0 {
		t.Fatalf("edges=%d drift=%+v", len(v.Edges), v.Drift)
	}
	// It follows the canvas: removing a line removes the edge on the next build.
	l, _ := r.wire.List(r.ctx, "u1", "ws1")
	_ = r.wire.Delete(r.ctx, domain.ActorUser, "u1", "u1", "ws1", l[0].ID)
	v2, _ := r.arch.Build(r.ctx, "u1", "ws1", nil)
	if len(v2.Edges) != 2 {
		t.Fatalf("view did not follow the canvas: %d edges", len(v2.Edges))
	}
}

func TestDriftMatchesObservedState(t *testing.T) {
	r := newRig(t)
	nest := r.card(t, "nestjs", "installed", "ready")
	mongo := r.card(t, "mongodb", "installed", "running")
	lone := r.card(t, "redis", "installed", "running")
	failed := r.card(t, "mysql", "installed", "failed")
	_, _ = r.st.UpdateComponent(r.ctx, failed.ID, func(c *domain.Component) error { c.Message = "port 3306 already in use"; return nil })
	r.link(t, nest, mongo, "")
	r.link(t, nest, failed, "")

	v, _ := r.arch.Build(r.ctx, "u1", "ws1", nil)
	kinds := map[string][]string{}
	for _, d := range v.Drift {
		kinds[d.Kind] = append(kinds[d.Kind], d.Message)
	}
	if got := strings.Join(kinds["isolated"], ";"); !strings.Contains(got, "Redis") || strings.Contains(got, "MongoDB") {
		t.Errorf("isolated: %v", kinds["isolated"])
	}
	u := strings.Join(kinds["unreachable"], ";")
	if !strings.Contains(u, "MySQL") || !strings.Contains(u, "port 3306") {
		t.Errorf("unreachable should explain the failed card: %v", kinds["unreachable"])
	}
	_ = lone
}

func TestExportsContainNoSecretsAndAreWellFormed(t *testing.T) {
	r := newRig(t)
	nest := r.card(t, "nestjs", "installed", "ready")
	redis := r.card(t, "redis", "connected", "connected")
	_, _ = r.st.UpdateComponent(r.ctx, redis.ID, func(c *domain.Component) error {
		c.ConnName = "cache <script>"
		c.Connection = map[string]any{"host": "secret-host.internal", "password": "hunter2"}
		return nil
	})
	r.link(t, nest, redis, "cache")
	v, _ := r.arch.Build(r.ctx, "u1", "ws1", map[string]bool{nest.ID: true})

	svg := architecture.SVG(v)
	md := architecture.Markdown(v)
	mm := architecture.Mermaid(v)
	for name, out := range map[string]string{"svg": svg, "markdown": md, "mermaid": mm} {
		for _, bad := range []string{"hunter2", "secret-host.internal", "<script>"} {
			if strings.Contains(out, bad) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
	}
	// The SVG must parse as XML.
	d := xml.NewDecoder(strings.NewReader(svg))
	for {
		if _, err := d.Token(); err != nil {
			if err != io.EOF {
				t.Fatalf("svg is not well-formed: %v", err)
			}
			break
		}
	}
	if !strings.Contains(mm, "flowchart LR") || !strings.Contains(mm, "subgraph backend") || !strings.Contains(mm, "class n") {
		t.Errorf("mermaid:\n%s", mm)
	}
	if !strings.Contains(md, "## Connections") || !strings.Contains(md, "```mermaid") {
		t.Errorf("markdown:\n%s", md)
	}
	if !strings.Contains(svg, "healthy") {
		t.Error("svg should write the line state as a word")
	}
}

func TestEmptyCanvasAndOwnership(t *testing.T) {
	r := newRig(t)
	v, err := r.arch.Build(r.ctx, "u1", "ws1", nil)
	if err != nil || len(v.Nodes) != 0 {
		t.Fatal(err)
	}
	if !strings.Contains(architecture.SVG(v), "No components") {
		t.Error("empty svg message")
	}
	if _, err := r.arch.Build(r.ctx, "other", "ws1", nil); err == nil {
		t.Fatal("another owner must not see the architecture")
	}
}
