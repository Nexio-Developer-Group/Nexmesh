package definition

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/envvars"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

// Issue is one problem found while validating a definition.
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

var keyRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Validate checks everything that can be checked without creating anything:
// names, references into the catalog, and that no secret value is present.
func Validate(d *Definition, cat *catalog.Catalog) []Issue {
	var out []Issue
	add := func(path, format string, a ...any) { out = append(out, Issue{path, fmt.Sprintf(format, a...)}) }

	if err := workspace.ValidateName(d.Workspace.Name); err != nil {
		add("workspace.name", "%s", err.Error())
	}
	if d.Workspace.OS != "" && d.Workspace.OS != "ubuntu-24.04" {
		add("workspace.os", "Operating system %q is not available. Only ubuntu-24.04 is supported.", d.Workspace.OS)
	}
	if d.Workspace.CPU < 1 || d.Workspace.RAMGB < 1 || d.Workspace.StorageGB < 1 {
		add("workspace", "cpu, ramGb and storageGb must each be at least 1.")
	}
	if m := d.Workspace.IdleStopMinutes; m != 0 && (m < 5 || m > 1440) {
		add("workspace.idleStopMinutes", "idleStopMinutes must be 0 or between 5 and 1440.")
	}

	keys := map[string]bool{}
	for i, c := range d.Components {
		p := fmt.Sprintf("components[%d]", i)
		if !keyRE.MatchString(c.Key) {
			add(p+".key", "key %q must be lowercase letters, digits and hyphens.", c.Key)
		} else if keys[c.Key] {
			add(p+".key", "duplicate key %q.", c.Key)
		}
		keys[c.Key] = true
		def, ok := cat.Get(c.Catalog)
		if !ok {
			add(p+".catalog", "unknown technology %q.", c.Catalog)
			continue
		}
		switch c.Mode {
		case "install", "connect", "draft":
		default:
			add(p+".mode", "mode must be install, connect or draft.")
		}
		if c.Mode == "install" && !def.SupportsMode("install") || c.Mode == "connect" && !def.SupportsMode("connect") {
			add(p+".mode", "%s does not support mode %q.", def.Name, c.Mode)
		}
		if c.Version != "" && !def.HasVersion(c.Version) {
			add(p+".version", "%s has no version %q (supported: %s).", def.Name, c.Version, strings.Join(def.Versions.Supported, ", "))
		}
		if c.Desired != "" && c.Desired != "running" && c.Desired != "stopped" {
			add(p+".desired", "desired must be running or stopped.")
		}
		secretNames := map[string]bool{}
		known := map[string]bool{}
		if def.Connection != nil {
			for _, f := range def.Connection.Fields {
				known[f.Name] = true
				if f.Type == "password" {
					secretNames[f.Name] = true
				}
			}
		}
		for k := range c.Connection {
			if secretNames[k] {
				add(p+".connection."+k, "this file contains a value for the credential %q. Remove it; credentials are re-entered after import.", k)
			} else if !known[k] {
				add(p+".connection."+k, "%s has no connection field %q.", def.Name, k)
			}
		}
		for k := range c.Settings {
			if sensitiveKey(k) {
				add(p+".settings."+k, "settings must not contain secrets (%q).", k)
			}
		}
		if c.Environment != "" && c.Environment != "development" && c.Environment != "staging" && c.Environment != "production" {
			add(p+".environment", "environment must be development, staging or production.")
		}
	}
	vnames := map[string]bool{}
	for i, v := range d.Variables {
		p := fmt.Sprintf("variables[%d]", i)
		if err := envvars.ValidateName(v.Name); err != nil {
			add(p+".name", "%s", err.Error())
		}
		if v.Secret && v.Value != "" {
			add(p+".value", "this file contains a value for the secret variable %q. Remove it; secrets are re-entered after import.", v.Name)
		}
		if v.Component != "" && !keys[v.Component] {
			add(p+".component", "unknown component key %q.", v.Component)
		}
		id := v.Component + "|" + v.Environment + "|" + v.Name
		if vnames[id] {
			add(p+".name", "duplicate variable %q.", v.Name)
		}
		vnames[id] = true
	}
	for i, p := range d.Ports {
		if p.Port < 1 || p.Port > 65535 {
			add(fmt.Sprintf("ports[%d].port", i), "port must be between 1 and 65535.")
		}
	}
	validatePhase3(d, keys, add)
	return out
}

// Todo is something the user must supply after an import.
type Todo struct {
	Kind    string `json:"kind"` // credential | secret_variable | agent_key | confirm_install
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// Todos lists what an import leaves for the user: credentials, secret
// variables, agent keys, and services that need an install confirmation.
func Todos(d *Definition, cat *catalog.Catalog, autoInstall bool) []Todo {
	var out []Todo
	for _, c := range d.Components {
		name := c.Catalog
		if def, ok := cat.Get(c.Catalog); ok {
			name = def.Name
		}
		if c.Name != "" {
			name = c.Name + " (" + name + ")"
		}
		for _, f := range c.SecretFields {
			if c.Mode == "connect" {
				out = append(out, Todo{"credential", name, "Enter the " + f + " on the Connect tab and test the connection."})
			}
		}
		if c.Mode == "install" && !autoInstall {
			out = append(out, Todo{"confirm_install", name, "Open the card and confirm the install."})
		}
		if c.Mode == "connect" && len(c.SecretFields) == 0 {
			out = append(out, Todo{"credential", name, "Open the card, test the connection and add it."})
		}
	}
	for _, v := range d.Variables {
		if v.Secret {
			out = append(out, Todo{"secret_variable", v.Name, "Add the value under Variables."})
		}
	}
	for _, a := range d.Agents {
		if a.NeedsKey {
			out = append(out, Todo{"agent_key", a.Name, "Add the API key under Code Agent and test the configuration."})
		}
	}
	return append(out, todosPhase3(d, cat)...)
}
