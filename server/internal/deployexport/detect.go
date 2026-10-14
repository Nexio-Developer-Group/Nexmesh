package deployexport

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/wsfs"
)

// Project is an application found in the workspace.
type Project struct {
	Dir    string // "" for the workspace root
	Kind   string // node | python | go | static
	Deps   map[string]bool
	Script map[string]string
	Main   string
	PM     string // npm | pnpm | yarn
	Files  map[string]bool
	Module string // python import path of the app, for example main:app
}

type packageJSON struct {
	Main            string            `json:"main"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

var skipDirs = map[string]bool{"node_modules": true, ".git": true, "dist": true, "build": true, "venv": true, ".venv": true, "__pycache__": true, "vendor": true}

// Detect finds application projects at the workspace root and one level down.
func Detect(ctx context.Context, fs *wsfs.FS, ws string) ([]*Project, error) {
	var dirs []string
	dirs = append(dirs, "")
	root, err := fs.List(ctx, ws, "")
	if err != nil {
		return nil, err
	}
	for _, e := range root {
		if e.Type == "dir" && !strings.HasPrefix(e.Name, ".") && !skipDirs[e.Name] {
			dirs = append(dirs, e.Name)
		}
	}
	sort.Strings(dirs[1:])
	var out []*Project
	for _, d := range dirs {
		es, err := fs.List(ctx, ws, d)
		if err != nil {
			continue
		}
		files := map[string]bool{}
		for _, e := range es {
			if e.Type == "file" {
				files[e.Name] = true
			}
		}
		if p := inspect(ctx, fs, ws, d, files); p != nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func inspect(ctx context.Context, fs *wsfs.FS, ws, dir string, files map[string]bool) *Project {
	join := func(n string) string { return path.Join(dir, n) }
	switch {
	case files["package.json"]:
		p := &Project{Dir: dir, Kind: "node", Files: files, Deps: map[string]bool{}, PM: "npm"}
		if t, err := fs.Read(ctx, ws, join("package.json")); err == nil {
			var pj packageJSON
			if json.Unmarshal([]byte(t.Content), &pj) == nil {
				p.Script, p.Main = pj.Scripts, pj.Main
				for k := range pj.Dependencies {
					p.Deps[k] = true
				}
				for k := range pj.DevDependencies {
					p.Deps[k] = true
				}
			}
		}
		switch {
		case files["pnpm-lock.yaml"]:
			p.PM = "pnpm"
		case files["yarn.lock"]:
			p.PM = "yarn"
		}
		return p
	case files["requirements.txt"] || files["pyproject.toml"]:
		p := &Project{Dir: dir, Kind: "python", Files: files, Deps: map[string]bool{}}
		for _, n := range []string{"requirements.txt", "pyproject.toml"} {
			if !files[n] {
				continue
			}
			if t, err := fs.Read(ctx, ws, join(n)); err == nil {
				for _, w := range []string{"fastapi", "flask", "django", "uvicorn"} {
					if strings.Contains(strings.ToLower(t.Content), w) {
						p.Deps[w] = true
					}
				}
			}
		}
		p.Module = "main:app"
		if !files["main.py"] {
			if ok, _ := fs.Exists(ctx, ws, join("app/main.py")); ok {
				p.Module = "app.main:app"
			}
		}
		return p
	case files["go.mod"]:
		return &Project{Dir: dir, Kind: "go", Files: files, Deps: map[string]bool{}}
	case files["index.html"]:
		return &Project{Dir: dir, Kind: "static", Files: files, Deps: map[string]bool{}}
	}
	return nil
}

// matches decides whether a project is an application of the given catalog entry.
func (p *Project) matches(catalogID string) bool {
	switch catalogID {
	case "nestjs":
		return p.Kind == "node" && p.Deps["@nestjs/core"]
	case "react":
		return p.Kind == "node" && p.Deps["react"] && !p.Deps["@angular/core"]
	case "angular":
		return p.Kind == "node" && (p.Deps["@angular/core"] || p.Files["angular.json"])
	case "fastapi":
		return p.Kind == "python" && p.Deps["fastapi"]
	case "htmlcss":
		return p.Kind == "static"
	case "nodejs":
		return p.Kind == "node" && !p.Deps["@nestjs/core"] && !p.Deps["react"] && !p.Deps["@angular/core"]
	case "python":
		return p.Kind == "python" && !p.Deps["fastapi"]
	case "go":
		return p.Kind == "go"
	}
	return false
}
