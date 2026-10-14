package httpapi

import (
	"encoding/json"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestCatalogEndpoint(t *testing.T) {
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.RegisterCatalog(cat)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/catalog", nil))
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	var body struct {
		Categories []struct {
			ID    string
			Items []string
		} `json:"categories"`
		Components []map[string]any `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Components) != 18 || len(body.Categories) != 6 {
		t.Fatalf("%d components, %d categories", len(body.Components), len(body.Categories))
	}
}

type fakeActivity struct {
	touched []string
	open    int
	ended   int
}

func (f *fakeActivity) Touch(ws string) { f.touched = append(f.touched, ws) }
func (f *fakeActivity) Open(ws string) func() {
	f.open++
	return func() { f.ended++ }
}

func TestRequestsForAWorkspaceAreTracked(t *testing.T) {
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	act := &fakeActivity{}
	s.WithActivity(act)
	s.Handle("GET /api/workspaces/{id}/files", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	s.Handle("GET /api/workspaces/{id}/events", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	s.Handle("GET /api/workspaces", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	for _, p := range []string{"/api/workspaces/ws_abc123/files", "/api/workspaces", "/healthz"} {
		res, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	if len(act.touched) != 1 || act.touched[0] != "ws_abc123" {
		t.Fatalf("touched: %v (listing and healthz must not count as workspace use)", act.touched)
	}
	res, _ := http.Get(ts.URL + "/api/workspaces/ws_abc123/events")
	res.Body.Close()
	if act.open != 1 || act.ended != 1 || len(act.touched) != 1 {
		t.Fatalf("event streams hold the workspace open for their lifetime: open=%d ended=%d", act.open, act.ended)
	}
}
