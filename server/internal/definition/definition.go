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
	// Phase 3 (PRD F1, F4, F21, F23, F17): connection lines, the active
	// environment, quality gates, agent rules and the agent permission level.
	Environments *EnvironmentsDef `yaml:"environments,omitempty" json:"environments,omitempty"`
	Wires        []WireDef        `yaml:"wires,omitempty" json:"wires,omitempty"`
	Gates        []GateDef        `yaml:"gates,omitempty" json:"gates,omitempty"`
	Rules        []RuleDef        `yaml:"rules,omitempty" json:"rules,omitempty"`
	AgentPolicy  *AgentPolicyDef  `yaml:"agentPolicy,omitempty" json:"agentPolicy,omitempty"`
	// Notes explain anything left out of an export. They are ignored on import.
	Notes []string `yaml:"notes,omitempty" json:"notes,omitempty"`
}

// EnvironmentsDef records which environment the canvas is switched to.
type EnvironmentsDef struct {
	Active string `yaml:"active" json:"active"`
}

// ProfileDef is the connection a card uses in one environment. Credentials are
// named, never included.
type ProfileDef struct {
	Connection   map[string]any `yaml:"connection,omitempty" json:"connection,omitempty"`
	SecretFields []string       `yaml:"secretFields,omitempty" json:"secretFields,omitempty"`
}

// WireDef is a connection line between two cards, by component key.
type WireDef struct {
	From        string `yaml:"from" json:"from"`
	To          string `yaml:"to" json:"to"`
	Name        string `yaml:"name,omitempty" json:"name,omitempty"`
	Environment string `yaml:"environment,omitempty" json:"environment,omitempty"`
}

// GateDef is a quality gate (PRD F23).
type GateDef struct {
	ID         string `yaml:"id" json:"id"`
	Name       string `yaml:"name" json:"name"`
	Kind       string `yaml:"kind" json:"kind"`
	Command    string `yaml:"command" json:"command"`
	Policy     string `yaml:"policy" json:"policy"`
	Enabled    bool   `yaml:"enabled" json:"enabled"`
	TimeoutSec int    `yaml:"timeoutSec,omitempty" json:"timeoutSec,omitempty"`
}

// RuleDef is an agent rules file (PRD F21), carried so a new workspace starts with the same guidance.
type RuleDef struct {
	Name       string   `yaml:"name" json:"name"`
	Title      string   `yaml:"title" json:"title"`
	Scope      string   `yaml:"scope" json:"scope"`
	Components []string `yaml:"components,omitempty" json:"components,omitempty"`
	Paths      []string `yaml:"paths,omitempty" json:"paths,omitempty"`
	Protect    []string `yaml:"protect,omitempty" json:"protect,omitempty"`
	Body       string   `yaml:"body" json:"body"`
}

// AgentPolicyDef is what the Code Agent may do. Auto-accept is deliberately
// not carried: a definition must not switch it on in another workspace.
type AgentPolicyDef struct {
	Level string `yaml:"level" json:"level"`
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
	// Profiles are the connections for the other environments (PRD F4).
	Profiles map[string]ProfileDef `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	X        float64               `yaml:"x" json:"x"`
	Y        float64               `yaml:"y" json:"y"`
}

type VariableDef struct {
	Name        string `yaml:"name" json:"name"`
	Secret      bool   `yaml:"secret,omitempty" json:"secret,omitempty"`
	Value       string `yaml:"value,omitempty" json:"value,omitempty"` // plain variables only
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Component   string `yaml:"component,omitempty" json:"component,omitempty"` // component key, empty for workspace scope
	// Environment limits the variable to one environment; empty applies to all (PRD F4).
	Environment string `yaml:"environment,omitempty" json:"environment,omitempty"`
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
