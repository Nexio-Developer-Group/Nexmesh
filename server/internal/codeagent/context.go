package codeagent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/architecture"
	"github.com/nexio-developer-group/nexmesh/server/internal/envvars"
)

// ArchSource and VarSource are the canvas views the agent may see. Both
// already omit connection details and secret values; the context adds nothing
// beyond their names.
type ArchSource interface {
	Build(ctx context.Context, owner, wsID string, highlight map[string]bool) (*architecture.View, error)
}
type VarSource interface {
	List(ctx context.Context, owner, wsID string) (*envvars.Listing, error)
}

// Context is what the agent is told about the workspace. It never carries a
// secret value, a connection string, a host or a port.
type Context struct {
	Workspace   string              `json:"workspace"`
	OS          string              `json:"os"`
	Environment string              `json:"environment"`
	Runtimes    []string            `json:"runtimes"`
	Cards       []architecture.Node `json:"cards"`
	Lines       []architecture.Edge `json:"lines"`
	Problems    []string            `json:"problems"`
	Variables   []VarName           `json:"variables"`
	Rules       []string            `json:"rules,omitempty"`
}

type VarName struct {
	Name   string `json:"name"`
	Secret bool   `json:"secret"`
	Source string `json:"source"` // user | <card name>
}

// BuildContext collects the canvas context for a workspace.
func (s *Service) BuildContext(ctx context.Context, owner, wsID string) (*Context, error) {
	ws, err := s.workspace(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	c := &Context{Workspace: ws.Name, OS: ws.OS, Environment: ws.ActiveEnvironment, Runtimes: []string{}, Cards: []architecture.Node{}, Lines: []architecture.Edge{}, Problems: []string{}, Variables: []VarName{}}
	if c.Environment == "" {
		c.Environment = "development"
	}
	for name, rt := range ws.Runtimes {
		if rt.Active != "" {
			c.Runtimes = append(c.Runtimes, name+" "+rt.Active)
		}
	}
	sort.Strings(c.Runtimes)
	if s.arch != nil {
		if v, err := s.arch.Build(ctx, owner, wsID, nil); err == nil {
			c.Cards, c.Lines = v.Nodes, v.Edges
			for _, d := range v.Drift {
				c.Problems = append(c.Problems, d.Message)
			}
		}
	}
	if s.vars != nil {
		if l, err := s.vars.List(ctx, owner, wsID); err == nil {
			for _, v := range l.Variables {
				c.Variables = append(c.Variables, VarName{Name: v.Name, Secret: v.Secret, Source: "user"})
			}
			for _, g := range l.Generated {
				c.Variables = append(c.Variables, VarName{Name: g.Name, Secret: true, Source: g.ComponentName})
			}
		}
	}
	if s.rules != nil {
		c.Rules, _ = s.rules(ctx, wsID)
	}
	return c, nil
}

// Render turns a context into prompt text.
func (c *Context) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Workspace: %s (%s). Active environment: %s.\n", c.Workspace, c.OS, c.Environment)
	if len(c.Runtimes) > 0 {
		fmt.Fprintf(&b, "Runtimes: %s.\n", strings.Join(c.Runtimes, ", "))
	}
	if len(c.Cards) == 0 {
		b.WriteString("The canvas has no cards yet.\n")
	} else {
		b.WriteString("Canvas cards:\n")
		names := map[string]string{}
		for _, n := range c.Cards {
			names[n.ID] = n.Name
			ver := ""
			if n.Version != "" {
				ver = " " + n.Version
			}
			fmt.Fprintf(&b, "- %s%s [%s, %s, %s]\n", n.Name, ver, n.Layer, n.Mode, n.State)
		}
		if len(c.Lines) > 0 {
			b.WriteString("Connection lines:\n")
			for _, e := range c.Lines {
				fmt.Fprintf(&b, "- %s -> %s (%s, %d variables)\n", names[e.From], names[e.To], e.State, e.Variables)
			}
		}
	}
	if len(c.Problems) > 0 {
		b.WriteString("Problems on the canvas:\n")
		for _, p := range c.Problems {
			b.WriteString("- " + p + "\n")
		}
	}
	if len(c.Variables) > 0 {
		b.WriteString("Environment variable names available to code (values are hidden from you; read them in code as environment variables):\n")
		for _, v := range c.Variables {
			tag := ""
			if v.Secret {
				tag = " (secret)"
			}
			fmt.Fprintf(&b, "- %s%s\n", v.Name, tag)
		}
	}
	if len(c.Rules) > 0 {
		b.WriteString("Rules you must follow in this workspace:\n")
		for _, r := range c.Rules {
			b.WriteString(r + "\n")
		}
	}
	return b.String()
}
