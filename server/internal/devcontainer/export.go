package devcontainer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/nexio-developer-group/nexmesh/server/internal/definition"
)

// Exported is the file plus a report of what it could and could not carry.
type Exported struct {
	File   []byte       `json:"-"`
	Report []ReportItem `json:"report"`
}

const baseImage = "mcr.microsoft.com/devcontainers/base:ubuntu-24.04"

var runtimeFeature = map[string]string{
	"nodejs": "ghcr.io/devcontainers/features/node:1",
	"python": "ghcr.io/devcontainers/features/python:1",
	"go":     "ghcr.io/devcontainers/features/go:1",
}

// Export renders a dev container file from a workspace definition (which is
// already secret-free). Secret variables and credentials are never written.
func Export(d *definition.Definition) (*Exported, error) {
	r := &rep{}
	cfg := map[string]any{}
	cfg["name"] = d.Workspace.Name
	cfg["image"] = baseImage
	r.add("image", Applied, "Ubuntu 24.04 base image, matching the Nexmesh workspace OS.")

	features := map[string]any{}
	var services []map[string]any
	for _, c := range d.Components {
		if f, ok := runtimeFeature[c.Catalog]; ok && c.Mode == "install" {
			opts := map[string]any{}
			if c.Version != "" {
				opts["version"] = c.Version
			}
			features[f] = opts
			r.add("features", Applied, "%s %s as the %s feature.", c.Catalog, c.Version, f)
			continue
		}
		services = append(services, map[string]any{"id": c.Catalog, "mode": c.Mode, "version": c.Version, "name": c.Name})
		switch {
		case c.Mode == "connect":
			r.add("components."+c.Key, Unsupported, "A connection to %s can't be represented in devcontainer.json; connection details and credentials are not exported.", c.Catalog)
		default:
			r.add("components."+c.Key, Unsupported, "%s runs as a separate service, which devcontainer.json can't start. It is listed under customizations.nexmesh.", c.Catalog)
		}
	}
	if len(features) > 0 {
		cfg["features"] = features
	}

	if len(d.Ports) > 0 {
		var fp []int
		attrs := map[string]any{}
		for _, p := range d.Ports {
			fp = append(fp, p.Port)
			if p.Label != "" {
				attrs[strconv.Itoa(p.Port)] = map[string]any{"label": p.Label}
			}
		}
		sort.Ints(fp)
		cfg["forwardPorts"] = fp
		if len(attrs) > 0 {
			cfg["portsAttributes"] = attrs
		}
		r.add("forwardPorts", Applied, "%d declared port(s).", len(d.Ports))
	}

	env := map[string]string{}
	var secretNames []string
	for _, v := range d.Variables {
		switch {
		case v.Component != "":
			r.add("variables."+v.Name, Unsupported, "Component-scoped variable %q has no dev container equivalent.", v.Name)
		case v.Secret:
			secretNames = append(secretNames, v.Name)
			r.add("variables."+v.Name, Unsupported, "Secret values are never exported; the name is listed under customizations.nexmesh.")
		default:
			env[v.Name] = v.Value
		}
	}
	if len(env) > 0 {
		cfg["containerEnv"] = env
		r.add("containerEnv", Applied, "%d variable(s).", len(env))
	}

	cfg["hostRequirements"] = map[string]any{
		"cpus":    d.Workspace.CPU,
		"memory":  fmt.Sprintf("%dgb", d.Workspace.RAMGB),
		"storage": fmt.Sprintf("%dgb", d.Workspace.StorageGB),
	}
	r.add("hostRequirements", Applied, "%d vCPU, %d GB RAM, %d GB storage.", d.Workspace.CPU, d.Workspace.RAMGB, d.Workspace.StorageGB)
	if len(d.Agents) > 0 {
		r.add("agents", Unsupported, "Code agent settings have no dev container equivalent.")
	}

	nx := map[string]any{"definitionSchemaVersion": definition.SchemaVersion}
	if len(services) > 0 {
		nx["components"] = services
	}
	if len(secretNames) > 0 {
		sort.Strings(secretNames)
		nx["secretVariables"] = secretNames
	}
	cfg["customizations"] = map[string]any{"nexmesh": nx}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return &Exported{File: append(b, '\n'), Report: r.items}, nil
}
