// Package catalog loads and validates the technology catalog (PRD Section 8,
// 11.2, 20.3.11). Manifests are YAML files validated against a JSON Schema,
// plus cross-field rules that a schema cannot express.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

const (
	ModeInstall = "install"
	ModeConnect = "connect"
)

type Component struct {
	SchemaVersion  int         `json:"schemaVersion" yaml:"schemaVersion"`
	ID             string      `json:"id" yaml:"id"`
	Name           string      `json:"name" yaml:"name"`
	Monogram       string      `json:"monogram" yaml:"monogram"`
	Order          int         `json:"order,omitempty" yaml:"order,omitempty"`
	Categories     []string    `json:"categories" yaml:"categories"`
	Kind           string      `json:"kind" yaml:"kind"`
	Description    string      `json:"description" yaml:"description"`
	SupportedModes []string    `json:"supportedModes" yaml:"supportedModes"`
	Versions       Versions    `json:"versions" yaml:"versions"`
	Ports          []int       `json:"ports,omitempty" yaml:"ports,omitempty"`
	Install        *Install    `json:"install,omitempty" yaml:"install,omitempty"`
	Resources      *Resources  `json:"resources,omitempty" yaml:"resources,omitempty"`
	Health         *Health     `json:"health,omitempty" yaml:"health,omitempty"`
	Connection     *Connection `json:"connection,omitempty" yaml:"connection,omitempty"`
}

type Versions struct {
	Default   string   `json:"default" yaml:"default"`
	Supported []string `json:"supported" yaml:"supported"`
}

type Install struct {
	Strategy string            `json:"strategy" yaml:"strategy"`
	Image    string            `json:"image,omitempty" yaml:"image,omitempty"`
	Tool     string            `json:"tool,omitempty" yaml:"tool,omitempty"`
	Manager  string            `json:"manager,omitempty" yaml:"manager,omitempty"`
	Command  string            `json:"command,omitempty" yaml:"command,omitempty"`
	Packages []string          `json:"packages,omitempty" yaml:"packages,omitempty"`
	Requires []string          `json:"requires,omitempty" yaml:"requires,omitempty"`
	Ports    []int             `json:"ports,omitempty" yaml:"ports,omitempty"`
	Env      map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Volumes  []Volume          `json:"volumes,omitempty" yaml:"volumes,omitempty"`
}

type Volume struct {
	Name   string  `json:"name" yaml:"name"`
	Path   string  `json:"path" yaml:"path"`
	SizeGB float64 `json:"sizeGB" yaml:"sizeGB"`
}

type Resources struct {
	CPU       float64 `json:"cpu" yaml:"cpu"`
	RAMMB     int     `json:"ramMB" yaml:"ramMB"`
	StorageGB float64 `json:"storageGB" yaml:"storageGB"`
}

type Health struct {
	Type string `json:"type" yaml:"type"`
	Port int    `json:"port,omitempty" yaml:"port,omitempty"`
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

type Connection struct {
	Schemes     []string `json:"schemes" yaml:"schemes"`
	DefaultPort int      `json:"defaultPort,omitempty" yaml:"defaultPort,omitempty"`
	EnvPrefix   string   `json:"envPrefix" yaml:"envPrefix"`
	Fields      []Field  `json:"fields" yaml:"fields"`
}

type Field struct {
	Name        string   `json:"name" yaml:"name"`
	Label       string   `json:"label" yaml:"label"`
	Type        string   `json:"type" yaml:"type"`
	Required    bool     `json:"required" yaml:"required"`
	Advanced    bool     `json:"advanced" yaml:"advanced"`
	Default     any      `json:"default,omitempty" yaml:"default,omitempty"`
	Placeholder string   `json:"placeholder,omitempty" yaml:"placeholder,omitempty"`
	Options     []string `json:"options,omitempty" yaml:"options,omitempty"`
	Help        string   `json:"help,omitempty" yaml:"help,omitempty"`
}

func (c *Component) SupportsMode(m string) bool { return slices.Contains(c.SupportedModes, m) }

// SecretFields returns the names of fields that must be stored in the secrets store.
func (c *Component) SecretFields() []string {
	var out []string
	if c.Connection != nil {
		for _, f := range c.Connection.Fields {
			if f.Type == "password" {
				out = append(out, f.Name)
			}
		}
	}
	return out
}

func (c *Component) HasVersion(v string) bool { return slices.Contains(c.Versions.Supported, v) }

type Catalog struct {
	byID  map[string]*Component
	order []*Component
}

func (c *Catalog) Get(id string) (*Component, bool) { v, ok := c.byID[id]; return v, ok }
func (c *Catalog) All() []*Component                { return c.order }

// Categories in display order (PRD Section 8).
var CategoryOrder = []string{"database", "backend", "frontend", "messaging", "scripting", "hosting"}

// Load reads every *.yaml in dir/components, validates it against
// dir/schema/component.schema.json and the cross-field rules.
func Load(dir string) (*Catalog, error) {
	schemaPath := filepath.Join(dir, "schema", "component.schema.json")
	sch, err := jsonschema.NewCompiler().Compile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "components", "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no catalog manifests in %s", dir)
	}
	cat := &Catalog{byID: map[string]*Component{}}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		comp, err := parse(raw, sch)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if _, dup := cat.byID[comp.ID]; dup {
			return nil, fmt.Errorf("%s: duplicate id %q", filepath.Base(f), comp.ID)
		}
		cat.byID[comp.ID] = comp
		cat.order = append(cat.order, comp)
	}
	for _, c := range cat.order {
		if c.Install != nil {
			for _, r := range c.Install.Requires {
				if _, ok := cat.byID[r]; !ok {
					return nil, fmt.Errorf("%s: requires unknown component %q", c.ID, r)
				}
			}
		}
	}
	sort.SliceStable(cat.order, func(i, j int) bool {
		a, b := cat.order[i], cat.order[j]
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.Name < b.Name
	})
	return cat, nil
}

func parse(raw []byte, sch *jsonschema.Schema) (*Component, error) {
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	// Round-trip through JSON so the schema sees JSON-native types.
	jb, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var jdoc any
	if err := json.Unmarshal(jb, &jdoc); err != nil {
		return nil, err
	}
	if err := sch.Validate(jdoc); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	var c Component
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return &c, validateRules(&c)
}

func validateRules(c *Component) error {
	if !slices.Contains(c.Versions.Supported, c.Versions.Default) {
		return fmt.Errorf("default version %q not in supported list", c.Versions.Default)
	}
	if c.SupportsMode(ModeInstall) {
		if c.Install == nil {
			return fmt.Errorf("install mode requires an install block")
		}
		if c.Install.Strategy == "image" && c.Install.Image == "" {
			return fmt.Errorf("strategy image requires install.image")
		}
		if c.Install.Strategy == "mise" && c.Install.Tool == "" {
			return fmt.Errorf("strategy mise requires install.tool")
		}
	}
	if c.SupportsMode(ModeConnect) {
		if c.Kind != "service" {
			return fmt.Errorf("connect mode is only valid for service-type components (PRD 11.2)")
		}
		if c.Connection == nil {
			return fmt.Errorf("connect mode requires a connection block")
		}
		seen := map[string]bool{}
		for _, f := range c.Connection.Fields {
			if seen[f.Name] {
				return fmt.Errorf("duplicate connection field %q", f.Name)
			}
			seen[f.Name] = true
			if f.Type == "select" && len(f.Options) == 0 {
				return fmt.Errorf("select field %q has no options", f.Name)
			}
		}
	} else if c.Connection != nil {
		return fmt.Errorf("connection block present but connect mode not supported")
	}
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("empty name")
	}
	return nil
}
