package httpapi

import (
	"net/http"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
)

type categoryDTO struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Items []string `json:"items"` // component ids
}

var categoryLabels = map[string]string{
	"database":  "Database",
	"backend":   "Backend",
	"frontend":  "Frontend",
	"messaging": "Event queues and messaging",
	"scripting": "Scripting",
	"hosting":   "Hosting and web infrastructure",
	"external":  "External services",
}

// RegisterCatalog adds GET /api/catalog.
func (s *Server) RegisterCatalog(cat *catalog.Catalog) {
	s.Handle("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) {
		cats := []categoryDTO{}
		for _, id := range catalog.CategoryOrder {
			d := categoryDTO{ID: id, Label: categoryLabels[id], Items: []string{}}
			for _, c := range cat.All() {
				for _, cc := range c.Categories {
					if cc == id {
						d.Items = append(d.Items, c.ID)
					}
				}
			}
			cats = append(cats, d)
		}
		WriteJSON(w, http.StatusOK, map[string]any{"categories": cats, "components": cat.All()})
	})
}
