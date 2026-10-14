package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// TemplateComponent is one card in a stack template.
type TemplateComponent struct {
	ID            string  `json:"id" yaml:"id"`
	X             float64 `json:"x" yaml:"x"`
	Y             float64 `json:"y" yaml:"y"`
	SuggestedMode string  `json:"suggestedMode,omitempty" yaml:"suggestedMode,omitempty"`
	Version       string  `json:"version,omitempty" yaml:"version,omitempty"`
}

// Template is a curated stack (PRD F3): components, default versions and
// suggested modes. It carries no connection values, secrets or scripts.
type Template struct {
	SchemaVersion int                 `json:"schemaVersion" yaml:"schemaVersion"`
	ID            string              `json:"id" yaml:"id"`
	Name          string              `json:"name" yaml:"name"`
	Description   string              `json:"description" yaml:"description"`
	Order         int                 `json:"order,omitempty" yaml:"order,omitempty"`
	Components    []TemplateComponent `json:"components" yaml:"components"`
}

// ValidateTemplateComponents checks ids, modes and versions against the catalog.
// It is shared by shipped templates and by personal ones saved from a canvas.
func (c *Catalog) ValidateTemplateComponents(comps []TemplateComponent) error {
	for _, tc := range comps {
		def, ok := c.Get(tc.ID)
		if !ok {
			return fmt.Errorf("unknown component %q", tc.ID)
		}
		if tc.SuggestedMode != "" && !def.SupportsMode(tc.SuggestedMode) {
			return fmt.Errorf("%s does not support mode %q", tc.ID, tc.SuggestedMode)
		}
		if tc.Version != "" && !def.HasVersion(tc.Version) {
			return fmt.Errorf("%s has no version %q", tc.ID, tc.Version)
		}
	}
	return nil
}

// LoadTemplates reads dir/templates/*.yaml, validated against template.schema.json
// and the loaded catalog.
func LoadTemplates(dir string, cat *Catalog) ([]*Template, error) {
	sch, err := jsonschema.NewCompiler().Compile(filepath.Join(dir, "schema", "template.schema.json"))
	if err != nil {
		return nil, fmt.Errorf("compile template schema: %w", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "templates", "*.yaml"))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []*Template
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var doc any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		jb, _ := json.Marshal(doc)
		var jdoc any
		_ = json.Unmarshal(jb, &jdoc)
		if err := sch.Validate(jdoc); err != nil {
			return nil, fmt.Errorf("%s: schema: %w", filepath.Base(f), err)
		}
		var t Template
		if err := yaml.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("%s: duplicate template id %q", filepath.Base(f), t.ID)
		}
		seen[t.ID] = true
		if err := cat.ValidateTemplateComponents(t.Components); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		out = append(out, &t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
