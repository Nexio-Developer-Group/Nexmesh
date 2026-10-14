package audit_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

func seed(t *testing.T) (*audit.Recorder, context.Context) {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	add := func(i int, actor, action, target string, detail map[string]any) {
		_ = st.AppendAudit(ctx, &domain.AuditEvent{ID: store.NewID("aud"), WorkspaceID: "ws1", ActorType: actor, ActorID: "u1", Action: action, Target: target, Detail: detail, At: base.Add(time.Duration(i) * time.Hour)})
	}
	add(0, domain.ActorUser, "workspace.create", "orders", nil)
	add(1, domain.ActorUser, "git.clone", "github.com/acme/app", map[string]any{"dir": "app"})
	add(2, domain.ActorSystem, "component.install", "redis", nil)
	add(3, domain.ActorUser, "git.push", "app", nil)
	add(4, domain.ActorAgent, "file.write", "=HYPERLINK(\"http://evil\")", map[string]any{"path": "a.txt"})
	_ = st.AppendAudit(ctx, &domain.AuditEvent{ID: "other", WorkspaceID: "ws2", ActorType: domain.ActorUser, Action: "git.push", At: base})
	return audit.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))), ctx
}

func actions(p *audit.Page) []string {
	var out []string
	for _, e := range p.Events {
		out = append(out, e.Action)
	}
	return out
}

func TestQueryFiltersAndOrder(t *testing.T) {
	r, ctx := seed(t)
	p, _ := r.Query(ctx, "ws1", audit.Filter{}, "", 50)
	if got := strings.Join(actions(p), ","); got != "file.write,git.push,component.install,git.clone,workspace.create" {
		t.Fatalf("newest first, ws-scoped: %s", got)
	}
	for name, tc := range map[string]struct {
		f    audit.Filter
		want string
	}{
		"actor":       {audit.Filter{ActorType: "system"}, "component.install"},
		"prefix":      {audit.Filter{Action: "git."}, "git.push,git.clone"},
		"exact":       {audit.Filter{Action: "git.push"}, "git.push"},
		"text target": {audit.Filter{Q: "REDIS"}, "component.install"},
		"text action": {audit.Filter{Q: "clone"}, "git.clone"},
		"from":        {audit.Filter{From: time.Date(2026, 3, 1, 15, 0, 0, 0, time.UTC)}, "file.write,git.push"},
		"to":          {audit.Filter{To: time.Date(2026, 3, 1, 13, 0, 0, 0, time.UTC)}, "git.clone,workspace.create"},
		"combined":    {audit.Filter{ActorType: "user", Action: "git."}, "git.push,git.clone"},
		"no match":    {audit.Filter{Q: "zzz"}, ""},
	} {
		p, _ := r.Query(ctx, "ws1", tc.f, "", 50)
		if got := strings.Join(actions(p), ","); got != tc.want {
			t.Errorf("%s: got %q want %q", name, got, tc.want)
		}
	}
}

func TestPagination(t *testing.T) {
	r, ctx := seed(t)
	var all []string
	after := ""
	for i := 0; i < 10; i++ {
		p, _ := r.Query(ctx, "ws1", audit.Filter{}, after, 2)
		all = append(all, actions(p)...)
		if p.Next == "" {
			break
		}
		after = p.Next
	}
	if strings.Join(all, ",") != "file.write,git.push,component.install,git.clone,workspace.create" {
		t.Fatalf("pages: %v", all)
	}
	// A filtered cursor never repeats or skips matching rows.
	p1, _ := r.Query(ctx, "ws1", audit.Filter{Action: "git."}, "", 1)
	p2, _ := r.Query(ctx, "ws1", audit.Filter{Action: "git."}, p1.Next, 1)
	if actions(p1)[0] != "git.push" || actions(p2)[0] != "git.clone" || p2.Next != "" {
		t.Fatalf("filtered paging: %v %v next=%q", actions(p1), actions(p2), p2.Next)
	}
}

func TestExportCSVNeutralisesFormulasAndJSONIsValid(t *testing.T) {
	r, ctx := seed(t)
	var buf bytes.Buffer
	if err := r.Export(ctx, "ws1", audit.Filter{}, "csv", &buf); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil || len(rows) != 6 || rows[0][3] != "action" {
		t.Fatalf("csv: %v %v", err, rows)
	}
	for _, row := range rows[1:] {
		for _, cell := range row {
			if strings.HasPrefix(cell, "=") || strings.HasPrefix(cell, "+") || strings.HasPrefix(cell, "@") {
				t.Fatalf("formula not neutralised: %q", cell)
			}
		}
	}
	if rows[1][4] != "'=HYPERLINK(\"http://evil\")" {
		t.Fatalf("target cell: %q", rows[1][4])
	}
	buf.Reset()
	if err := r.Export(ctx, "ws1", audit.Filter{ActorType: "user"}, "json", &buf); err != nil {
		t.Fatal(err)
	}
	var evs []domain.AuditEvent
	if err := json.Unmarshal(buf.Bytes(), &evs); err != nil || len(evs) != 3 {
		t.Fatalf("json: %v %d", err, len(evs))
	}
}
