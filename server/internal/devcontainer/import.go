// Package devcontainer converts between dev container configuration files
// (devcontainer.json) and Nexmesh workspace definitions (PRD F6). Every field
// of an imported file appears in a compatibility report as applied, adapted or
// not supported: nothing is dropped silently.
package devcontainer

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/definition"
	"github.com/nexio-developer-group/nexmesh/server/internal/envvars"
)

const (
	Applied     = "applied"
	Adapted     = "adapted"
	Unsupported = "unsupported"
)

type ReportItem struct {
	Field  string `json:"field"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type Imported struct {
	Definition *definition.Definition `json:"definition"`
	PostCreate []string               `json:"postCreate"`
	Report     []ReportItem           `json:"report"`
}

type rep struct{ items []ReportItem }

func (r *rep) add(field, status, format string, a ...any) {
	r.items = append(r.items, ReportItem{field, status, fmt.Sprintf(format, a...)})
}

var nameRE = regexp.MustCompile(`[^a-z0-9]+`)

func workspaceName(s string) (string, bool) {
	n := strings.Trim(nameRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if n == "" {
		n = "devcontainer"
	}
	if len(n) < 2 {
		n += "-ws"
	}
	if len(n) > 48 {
		n = strings.Trim(n[:48], "-")
	}
	return n, n != s
}

// featureRuntime maps a dev container feature id to a catalog runtime.
func featureRuntime(id string) string {
	base := id
	if i := strings.LastIndex(base, ":"); i >= 0 && !strings.Contains(base[i:], "/") {
		base = base[:i]
	}
	switch base {
	case "ghcr.io/devcontainers/features/node":
		return "nodejs"
	case "ghcr.io/devcontainers/features/python":
		return "python"
	case "ghcr.io/devcontainers/features/go":
		return "go"
	}
	return ""
}

// pickVersion chooses the closest supported catalog version for a requested
// one. "lts", "latest", "" and "os-provided" mean the catalog default.
func pickVersion(def *catalog.Component, want string) (v string, exact bool) {
	want = strings.TrimSpace(strings.TrimPrefix(want, "v"))
	switch strings.ToLower(want) {
	case "", "latest", "lts", "os-provided", "stable":
		return def.Versions.Default, false
	}
	for _, s := range def.Versions.Supported {
		if s == want {
			return s, true
		}
	}
	// Prefix match on major or major.minor: the first (newest listed) supported version that starts with it.
	for _, s := range def.Versions.Supported {
		if strings.HasPrefix(s, want+".") {
			return s, false
		}
	}
	return def.Versions.Default, false
}

var memRE = regexp.MustCompile(`^(?i)\s*(\d+(?:\.\d+)?)\s*(gb|g|mb|m|tb|t)?b?\s*$`)

func toGB(s string) (int, bool) {
	m := memRE.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	f, _ := strconv.ParseFloat(m[1], 64)
	switch strings.ToLower(m[2]) {
	case "mb", "m":
		f /= 1024
	case "tb", "t":
		f *= 1024
	}
	if f < 1 {
		f = 1
	}
	return int(f + 0.999), true
}

var imageRuntimes = []struct {
	re      *regexp.Regexp
	runtime string
}{
	{regexp.MustCompile(`(?:javascript-node|typescript-node|universal|node)[:\-]?(\d+)?`), "nodejs"},
	{regexp.MustCompile(`devcontainers/python:(?:\d+-)?(\d+(?:\.\d+)?)`), "python"},
	{regexp.MustCompile(`devcontainers/go:(?:\d+-)?(\d+(?:\.\d+)?)`), "go"},
}

func asStrings(v any) []string {
	switch c := v.(type) {
	case string:
		if strings.TrimSpace(c) == "" {
			return nil
		}
		return []string{c}
	case []any:
		parts := make([]string, 0, len(c))
		for _, p := range c {
			parts = append(parts, fmt.Sprint(p))
		}
		return []string{shellJoin(parts)}
	case map[string]any:
		keys := make([]string, 0, len(c))
		for k := range c {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			out = append(out, asStrings(c[k])...)
		}
		return out
	}
	return nil
}

func shellJoin(parts []string) string {
	q := make([]string, len(parts))
	for i, p := range parts {
		if p == "" || strings.ContainsAny(p, " \t\"'$`\\&|;<>()*?{}[]!#~") {
			q[i] = "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
		} else {
			q[i] = p
		}
	}
	return strings.Join(q, " ")
}

var knownIgnored = map[string]string{
	"remoteUser":                  "Workspaces run as the default user.",
	"containerUser":               "Workspaces run as the default user.",
	"updateRemoteUserUID":         "Not applicable: workspaces manage their own user.",
	"workspaceFolder":             "The workspace root is fixed at /workspace.",
	"workspaceMount":              "The workspace root is fixed at /workspace.",
	"mounts":                      "Custom mounts are not available in workspaces.",
	"runArgs":                     "Container run arguments are not available in workspaces.",
	"privileged":                  "Privileged containers are never allowed.",
	"capAdd":                      "Extra Linux capabilities are never granted.",
	"securityOpt":                 "Security options cannot be changed.",
	"init":                        "Not applicable.",
	"overrideCommand":             "Not applicable.",
	"shutdownAction":              "Workspaces stop by the idle rule in Settings.",
	"userEnvProbe":                "Not applicable.",
	"waitFor":                     "Not applicable.",
	"overrideFeatureInstallOrder": "Features are not installed as dev container features.",
	"secrets":                     "Secrets are entered under Variables after import.",
	"initializeCommand":           "Runs on the host before creation, which Nexmesh never does.",
	"postStartCommand":            "Runs on every start; run it in the terminal when needed.",
	"postAttachCommand":           "Runs on every attach; run it in the terminal when needed.",
	"otherPortsAttributes":        "Port defaults are not supported; declare ports individually.",
	"$schema":                     "",
}

// Import converts devcontainer.json content into a definition plus post-create commands.
func Import(raw []byte, cat *catalog.Catalog) (*Imported, error) {
	m, err := ParseJSONC(raw)
	if err != nil {
		return nil, err
	}
	r := &rep{}
	d := &definition.Definition{SchemaVersion: definition.SchemaVersion, Kind: definition.Kind,
		Workspace: definition.WorkspaceDef{OS: "ubuntu-24.04", CPU: 2, RAMGB: 4, StorageGB: 20}}
	out := &Imported{Definition: d, PostCreate: []string{}}
	seen := map[string]bool{}
	mark := func(k string) { seen[k] = true }
	runtimes := map[string]string{} // catalog id -> version
	addRuntime := func(field, id, want string) {
		def, ok := cat.Get(id)
		if !ok {
			return
		}
		v, exact := pickVersion(def, want)
		if prev, dup := runtimes[id]; dup && prev == v {
			return
		}
		runtimes[id] = v
		if want == "" || exact {
			r.add(field, Applied, "%s %s will be installed.", def.Name, v)
		} else {
			r.add(field, Adapted, "Requested %s %q; Nexmesh installs %s %s (the closest supported version).", def.Name, want, def.Name, v)
		}
	}

	// name
	if v, ok := m["name"].(string); ok {
		mark("name")
		n, changed := workspaceName(v)
		d.Workspace.Name = n
		if changed {
			r.add("name", Adapted, "Workspace name %q became %q to fit Nexmesh naming rules.", v, n)
		} else {
			r.add("name", Applied, "Workspace name %q.", n)
		}
	} else {
		d.Workspace.Name = "devcontainer"
		r.add("name", Adapted, "No name in the file; the workspace is called %q (change it on import).", d.Workspace.Name)
	}

	// base image
	if v, ok := m["image"].(string); ok {
		mark("image")
		low := strings.ToLower(v)
		switch {
		case strings.Contains(low, "ubuntu") && (strings.Contains(low, "24.04") || strings.Contains(low, "noble")):
			r.add("image", Applied, "%s matches the Ubuntu 24.04 LTS workspace OS.", v)
		case strings.Contains(low, "ubuntu"):
			r.add("image", Adapted, "%s is not Ubuntu 24.04. Workspaces always use Ubuntu 24.04 LTS, so the image is replaced.", v)
		default:
			r.add("image", Adapted, "%s is replaced by Ubuntu 24.04 LTS, the only workspace OS.", v)
		}
		for _, ir := range imageRuntimes {
			if mm := ir.re.FindStringSubmatch(low); mm != nil && strings.Contains(low, "devcontainers/") {
				ver := ""
				if len(mm) > 1 {
					ver = mm[1]
				}
				addRuntime("image", ir.runtime, ver)
				break
			}
		}
	}
	for _, k := range []string{"build", "dockerFile", "dockerfile", "context"} {
		if _, ok := m[k]; ok {
			mark(k)
			r.add(k, Unsupported, "Custom Dockerfiles are not supported. Install what you need with components or a post-create command.")
		}
	}
	if _, ok := m["dockerComposeFile"]; ok {
		mark("dockerComposeFile")
		mark("service")
		mark("runServices")
		r.add("dockerComposeFile", Unsupported, "Compose-based dev containers are not supported. Import the services' connection details with Import connection instead.")
		if _, ok := m["service"]; ok {
			r.add("service", Unsupported, "Part of the compose configuration.")
		}
		if _, ok := m["runServices"]; ok {
			r.add("runServices", Unsupported, "Part of the compose configuration.")
		}
	}

	// features
	if feats, ok := m["features"].(map[string]any); ok {
		mark("features")
		ids := make([]string, 0, len(feats))
		for id := range feats {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			field := "features." + id
			if rt := featureRuntime(id); rt != "" {
				ver := ""
				switch o := feats[id].(type) {
				case map[string]any:
					ver, _ = o["version"].(string)
					if s, ok := o["version"].(float64); ok {
						ver = strconv.FormatFloat(s, 'f', -1, 64)
					}
				case string:
					ver = o
				}
				addRuntime(field, rt, ver)
				continue
			}
			base := id
			if i := strings.LastIndex(base, ":"); i >= 0 && !strings.Contains(base[i:], "/") {
				base = base[:i]
			}
			switch base {
			case "ghcr.io/devcontainers/features/common-utils", "ghcr.io/devcontainers/features/git":
				r.add(field, Adapted, "Already part of the Nexmesh workspace image.")
			default:
				r.add(field, Unsupported, "Only the Node.js, Python and Go features are supported.")
			}
		}
	}

	// ports
	declared := map[int]string{}
	if v, ok := m["forwardPorts"].([]any); ok {
		mark("forwardPorts")
		for _, p := range v {
			switch pv := p.(type) {
			case float64:
				declared[int(pv)] = ""
				r.add("forwardPorts", Applied, "Port %d is declared and appears in the Ports panel.", int(pv))
			case string:
				if n, err := strconv.Atoi(pv); err == nil {
					declared[n] = ""
					r.add("forwardPorts", Applied, "Port %d is declared and appears in the Ports panel.", n)
				} else {
					r.add("forwardPorts", Unsupported, "%q refers to another container (host:port), which workspaces don't have.", pv)
				}
			}
		}
	}
	if v, ok := m["portsAttributes"].(map[string]any); ok {
		mark("portsAttributes")
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			n, err := strconv.Atoi(k)
			attrs, _ := v[k].(map[string]any)
			if err != nil {
				r.add("portsAttributes."+k, Unsupported, "Port ranges and patterns are not supported.")
				continue
			}
			if label, ok := attrs["label"].(string); ok {
				declared[n] = label
				r.add("portsAttributes."+k, Applied, "Port %d is labelled %q.", n, label)
			}
			for a := range attrs {
				if a != "label" {
					r.add("portsAttributes."+k+"."+a, Unsupported, "Port attribute %q is ignored.", a)
				}
			}
		}
	}
	ports := make([]int, 0, len(declared))
	for p := range declared {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	for _, p := range ports {
		d.Ports = append(d.Ports, definition.PortDef{Port: p, Label: declared[p]})
	}

	// environment
	envs := map[string]string{}
	for _, k := range []string{"containerEnv", "remoteEnv"} {
		if v, ok := m[k].(map[string]any); ok {
			mark(k)
			names := make([]string, 0, len(v))
			for n := range v {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				val := fmt.Sprint(v[n])
				switch {
				case v[n] == nil:
					r.add(k+"."+n, Unsupported, "A null value removes the variable; there is nothing to import.")
				case strings.Contains(val, "${"):
					r.add(k+"."+n, Unsupported, "The value uses a variable substitution (%s) that Nexmesh cannot resolve.", val)
				case envvars.ValidateName(n) != nil:
					r.add(k+"."+n, Unsupported, "%q is not an allowed variable name.", n)
				case envvars.LooksSecret(n):
					d.Variables = append(d.Variables, definition.VariableDef{Name: n, Secret: true})
					r.add(k+"."+n, Adapted, "The name looks like a secret, so its value was not imported. Enter it under Variables.")
				default:
					if _, dup := envs[n]; dup {
						r.add(k+"."+n, Adapted, "Defined in both containerEnv and remoteEnv; the later value is used.")
					} else {
						r.add(k+"."+n, Applied, "Imported as a workspace variable.")
					}
					envs[n] = val
				}
			}
		}
	}
	names := make([]string, 0, len(envs))
	for n := range envs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d.Variables = append(d.Variables, definition.VariableDef{Name: n, Value: envs[n]})
	}

	// resources
	if hr, ok := m["hostRequirements"].(map[string]any); ok {
		mark("hostRequirements")
		if c, ok := hr["cpus"].(float64); ok && c >= 1 {
			d.Workspace.CPU = int(c)
			r.add("hostRequirements.cpus", Applied, "%d vCPU.", int(c))
		}
		if s, ok := hr["memory"].(string); ok {
			if gb, ok := toGB(s); ok {
				d.Workspace.RAMGB = gb
				r.add("hostRequirements.memory", Applied, "%d GB RAM.", gb)
			} else {
				r.add("hostRequirements.memory", Unsupported, "Could not read %q as a size; the default %d GB is used.", s, d.Workspace.RAMGB)
			}
		}
		if s, ok := hr["storage"].(string); ok {
			if gb, ok := toGB(s); ok {
				d.Workspace.StorageGB = gb
				r.add("hostRequirements.storage", Applied, "%d GB storage.", gb)
			} else {
				r.add("hostRequirements.storage", Unsupported, "Could not read %q as a size; the default %d GB is used.", s, d.Workspace.StorageGB)
			}
		}
		if g, ok := hr["gpu"]; ok && g != false && g != nil {
			r.add("hostRequirements.gpu", Unsupported, "GPU access is requested separately when creating a workspace, and only if the host has one.")
		}
		for k := range hr {
			if k != "cpus" && k != "memory" && k != "storage" && k != "gpu" {
				r.add("hostRequirements."+k, Unsupported, "Not supported.")
			}
		}
	} else {
		r.add("hostRequirements", Adapted, "Not specified; defaults of %d vCPU, %d GB RAM and %d GB storage are used. Adjust them on import.", d.Workspace.CPU, d.Workspace.RAMGB, d.Workspace.StorageGB)
	}

	// lifecycle commands that run once
	for _, k := range []string{"onCreateCommand", "updateContentCommand", "postCreateCommand"} {
		if v, ok := m[k]; ok {
			mark(k)
			cmds := asStrings(v)
			out.PostCreate = append(out.PostCreate, cmds...)
			if len(cmds) > 0 {
				r.add(k, Applied, "%d command(s) saved. They run once after creation only if you choose to run them.", len(cmds))
			} else {
				r.add(k, Adapted, "Empty; nothing to run.")
			}
		}
	}

	// customizations
	if c, ok := m["customizations"].(map[string]any); ok {
		mark("customizations")
		keys := make([]string, 0, len(c))
		for k := range c {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "vscode" {
				r.add("customizations.vscode", Unsupported, "Editor extensions and settings are not imported; Nexmesh has its own editor.")
			} else if k == "nexmesh" {
				r.add("customizations.nexmesh", Adapted, "Nexmesh's own section is informational on import.")
			} else {
				r.add("customizations."+k, Unsupported, "Tool-specific settings for %q are ignored.", k)
			}
		}
	}

	// everything else, so nothing is dropped silently
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if seen[k] {
			continue
		}
		if why, ok := knownIgnored[k]; ok {
			if why != "" {
				r.add(k, Unsupported, "%s", why)
			}
			continue
		}
		r.add(k, Unsupported, "Unknown or unsupported field.")
	}

	// components from runtimes, in catalog order
	x := 40.0
	rids := make([]string, 0, len(runtimes))
	for id := range runtimes {
		rids = append(rids, id)
	}
	sort.Strings(rids)
	for _, id := range rids {
		d.Components = append(d.Components, definition.ComponentDef{Key: id, Catalog: id, Mode: "install", Version: runtimes[id], Desired: "running", X: x, Y: 40})
		x += 250
	}
	out.Report = r.items
	return out, nil
}
