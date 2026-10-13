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
