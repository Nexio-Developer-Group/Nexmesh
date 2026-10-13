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

// Provider is a code agent provider manifest (PRD 12.3): provider lists are
// catalog-driven so contributors can add providers without changing the UI.
type Provider struct {
	SchemaVersion int      `json:"schemaVersion" yaml:"schemaVersion"`
	ID            string   `json:"id" yaml:"id"`
	Name          string   `json:"name" yaml:"name"`
	Tagline       string   `json:"tagline" yaml:"tagline"`
	Order         int      `json:"order" yaml:"order"`
	Hosted        bool     `json:"hosted" yaml:"hosted"`
	Protocols     []string `json:"protocols" yaml:"protocols"`
	BaseURL       string   `json:"baseUrl,omitempty" yaml:"baseUrl,omitempty"`
	DefaultModel  string   `json:"defaultModel,omitempty" yaml:"defaultModel,omitempty"`
	Notice        string   `json:"notice,omitempty" yaml:"notice,omitempty"`
	ComingSoon    []string `json:"comingSoon,omitempty" yaml:"comingSoon,omitempty"`
	Fields        []Field  `json:"fields" yaml:"fields"`
}

// LoadProviders reads dir/providers/*.yaml, validated against provider.schema.json.
func LoadProviders(dir string) ([]*Provider, error) {
	sch, err := jsonschema.NewCompiler().Compile(filepath.Join(dir, "schema", "provider.schema.json"))
	if err != nil {
		return nil, fmt.Errorf("compile provider schema: %w", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "providers", "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no provider manifests in %s", dir)
	}
	var out []*Provider
	seen := map[string]bool{}
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
		var p Provider
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if seen[p.ID] {
			return nil, fmt.Errorf("%s: duplicate provider id %q", filepath.Base(f), p.ID)
		}
		seen[p.ID] = true
		if p.Hosted && p.BaseURL == "" {
			return nil, fmt.Errorf("%s: hosted providers need a baseUrl", filepath.Base(f))
		}
		out = append(out, &p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out, nil
}
