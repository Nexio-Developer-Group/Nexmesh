// Package definition implements workspace definition files (PRD F12): a
// versioned, secret-free description of a workspace that can be exported,
// imported into a new workspace, and compared against a running one (drift).
package definition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	SchemaVersion = 1
	Kind          = "NexmeshWorkspace"
	maxFileBytes  = 1 << 20
)

type Definition struct {
	SchemaVersion int                   `yaml:"schemaVersion" json:"schemaVersion"`
	Kind          string                `yaml:"kind" json:"kind"`
	ExportedAt    time.Time             `yaml:"exportedAt,omitempty" json:"exportedAt,omitempty"`
	Workspace     WorkspaceDef          `yaml:"workspace" json:"workspace"`
	Runtimes      map[string]RuntimeDef `yaml:"runtimes,omitempty" json:"runtimes,omitempty"`
	Components    []ComponentDef        `yaml:"components,omitempty" json:"components,omitempty"`
	Variables     []VariableDef         `yaml:"variables,omitempty" json:"variables,omitempty"`
	Ports         []PortDef             `yaml:"ports,omitempty" json:"ports,omitempty"`
	Agents        []AgentDef            `yaml:"agents,omitempty" json:"agents,omitempty"`
}

type WorkspaceDef struct {
	Name            string `yaml:"name" json:"name"`
	Description     string `yaml:"description,omitempty" json:"description,omitempty"`
	OS              string `yaml:"os" json:"os"`
	CPU             int    `yaml:"cpu" json:"cpu"`
	RAMGB           int    `yaml:"ramGb" json:"ramGb"`
	StorageGB       int    `yaml:"storageGb" json:"storageGb"`
	GPU             bool   `yaml:"gpu,omitempty" json:"gpu,omitempty"`
	IdleStopMinutes int    `yaml:"idleStopMinutes,omitempty" json:"idleStopMinutes,omitempty"`
}

// RuntimeDef records resolved runtime versions (informational for import, compared by drift).
type RuntimeDef struct {
	Versions []string `yaml:"versions" json:"versions"`
	Active   string   `yaml:"active,omitempty" json:"active,omitempty"`
}

type ComponentDef struct {
	Key     string `yaml:"key" json:"key"`         // stable within the file
	Catalog string `yaml:"catalog" json:"catalog"` // catalog id
	Mode    string `yaml:"mode" json:"mode"`       // install | connect | draft
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
	// Desired is the state the workspace should hold the component in (running | stopped) for installed services.
	Desired     string         `yaml:"desired,omitempty" json:"desired,omitempty"`
	Settings    map[string]any `yaml:"settings,omitempty" json:"settings,omitempty"`
	Name        string         `yaml:"name,omitempty" json:"name,omitempty"` // connection name
	Environment string         `yaml:"environment,omitempty" json:"environment,omitempty"`
	Connection  map[string]any `yaml:"connection,omitempty" json:"connection,omitempty"`
	// SecretFields names the credentials to re-supply after import; their values are never in the file.
	SecretFields []string `yaml:"secretFields,omitempty" json:"secretFields,omitempty"`
	X            float64  `yaml:"x" json:"x"`
	Y            float64  `yaml:"y" json:"y"`
}

type VariableDef struct {
	Name        string `yaml:"name" json:"name"`
	Secret      bool   `yaml:"secret,omitempty" json:"secret,omitempty"`
	Value       string `yaml:"value,omitempty" json:"value,omitempty"` // plain variables only
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Component   string `yaml:"component,omitempty" json:"component,omitempty"` // component key, empty for workspace scope
}

type PortDef struct {
	Port  int    `yaml:"port" json:"port"`
	Label string `yaml:"label,omitempty" json:"label,omitempty"`
}

type AgentDef struct {
	Name     string `yaml:"name" json:"name"`
	Provider string `yaml:"provider" json:"provider"`
	Model    string `yaml:"model,omitempty" json:"model,omitempty"`
	Endpoint string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	Protocol string `yaml:"protocol,omitempty" json:"protocol,omitempty"`
	Default  bool   `yaml:"default,omitempty" json:"default,omitempty"`
	// NeedsKey is always true for hosted providers: the API key is never exported.
	NeedsKey bool `yaml:"needsKey,omitempty" json:"needsKey,omitempty"`
}

// Marshal renders the definition as YAML (a stable, reviewable, diffable form).
func Marshal(d *Definition) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# Nexmesh workspace definition. Contains no secret values.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

// ParseError is a problem reading the file itself.
type ParseError struct{ Msg string }

func (e *ParseError) Error() string { return e.Msg }

// Parse reads YAML or JSON. Unknown fields are rejected so typos and fields
// from a newer schema are not silently ignored.
func Parse(raw []byte) (*Definition, error) {
	if len(raw) == 0 {
		return nil, &ParseError{"The definition is empty."}
	}
	if len(raw) > maxFileBytes {
		return nil, &ParseError{fmt.Sprintf("The definition is larger than %d KB.", maxFileBytes>>10)}
	}
	var d Definition
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		// A file written by a newer Nexmesh may carry unknown fields: say so clearly.
		msg := err.Error()
		if strings.Contains(msg, "not found in type") {
			return nil, &ParseError{"The definition has a field this version of Nexmesh doesn't know (" + firstLine(msg) + "). It may come from a newer version."}
		}
		return nil, &ParseError{"The definition could not be read: " + firstLine(msg)}
	}
	if d.Kind != Kind {
		return nil, &ParseError{fmt.Sprintf("This is not a Nexmesh workspace definition (kind must be %q).", Kind)}
	}
	if d.SchemaVersion != SchemaVersion {
		return nil, &ParseError{fmt.Sprintf("Unsupported definition schemaVersion %d. This version of Nexmesh reads version %d.", d.SchemaVersion, SchemaVersion)}
	}
	return &d, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// ToMap converts to a JSON-native map (operation params).
func (d *Definition) ToMap() (map[string]any, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(b, &m)
}

// FromMap is the inverse of ToMap.
func FromMap(m map[string]any) (*Definition, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var d Definition
	return &d, json.Unmarshal(b, &d)
}
