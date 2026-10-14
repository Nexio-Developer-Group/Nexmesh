// Package envvars manages workspace and component environment variables
// (PRD F11) and computes the effective environment delivered to the agent.
//
// Precedence, lowest to highest:
//  1. generated connection variables (e.g. PROD_ORDERS_DB_URI)
//  2. workspace-scoped variables
//  3. component-scoped variables
//
// A user variable may therefore override a generated one; the listing marks
// such rows so the override is visible.
package envvars

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

const (
	MaxVariables  = 200
	MaxValueBytes = 32 << 10
)

var nameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// ValidationError marks a user-correctable problem on one field.
type ValidationError struct{ Field, Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func bad(field, format string, a ...any) error {
	return &ValidationError{field, fmt.Sprintf(format, a...)}
}

// Source supplies the generated connection variables.
type Source interface {
	ConnectionEnv(ctx context.Context, wsID string) (map[string]string, error)
	ExposedAs(c *domain.Component) []string
}

type Service struct {
	st  store.Store
	sec *secrets.Service
	aud *audit.Recorder
	src Source
}

func NewService(st store.Store, sec *secrets.Service, aud *audit.Recorder, src Source) *Service {
	return &Service{st: st, sec: sec, aud: aud, src: src}
}

func secretID(wsID, scope, name string) string {
	if scope == "" {
		scope = "workspace"
	}
	return "var_" + wsID + "_" + scope + "_" + name
}

func (s *Service) workspace(ctx context.Context, owner, id string) (*domain.Workspace, error) {
	w, err := s.st.GetWorkspace(ctx, id)
	if err != nil || w.OwnerID != owner {
		return nil, store.ErrNotFound
	}
	return w, nil
}

// Row is the API form of a variable; secret values are never included.
type Row struct {
	Name        string    `json:"name"`
	ComponentID string    `json:"componentId,omitempty"`
	Secret      bool      `json:"secret"`
	Value       string    `json:"value,omitempty"`
	Description string    `json:"description,omitempty"`
	Overrides   bool      `json:"overridesGenerated,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Generated describes a connection-provided variable (name and source only).
type Generated struct {
	Name          string `json:"name"`
	ComponentID   string `json:"componentId"`
	ComponentName string `json:"componentName"`
	Overridden    bool   `json:"overridden,omitempty"`
}

type Listing struct {
	Variables []Row       `json:"variables"`
	Generated []Generated `json:"generated"`
}

func (s *Service) List(ctx context.Context, owner, wsID string) (*Listing, error) {
	w, err := s.workspace(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	comps, err := s.st.ListComponents(ctx, wsID)
	if err != nil {
		return nil, err
	}
	gen := []Generated{}
	genNames := map[string]bool{}
	for _, c := range comps {
		for _, n := range s.src.ExposedAs(c) {
			gen = append(gen, Generated{Name: n, ComponentID: c.ID, ComponentName: c.ConnName})
			genNames[n] = true
		}
	}
	user := map[string]bool{}
	rows := make([]Row, 0, len(w.Variables))
	for _, v := range w.Variables {
		user[v.Name] = true
		rows = append(rows, Row{Name: v.Name, ComponentID: v.ComponentID, Secret: v.Secret, Value: v.Value, Description: v.Description,
			Overrides: genNames[v.Name], UpdatedAt: v.UpdatedAt})
	}
	for i := range gen {
		gen[i].Overridden = user[gen[i].Name]
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].ComponentID < rows[j].ComponentID
	})
	sort.Slice(gen, func(i, j int) bool { return gen[i].Name < gen[j].Name })
	return &Listing{Variables: rows, Generated: gen}, nil
}

type SetInput struct {
	Name        string
	ComponentID string
	Secret      bool
	// Value nil keeps the stored value (editing a secret's description or
	// flipping nothing); non-nil replaces it.
	Value       *string
	Description string
}

func ValidateName(name string) error {
	switch {
	case !nameRE.MatchString(name):
		return bad("name", "Use letters, digits and underscores, starting with a letter or underscore (max 128).")
	case strings.HasPrefix(strings.ToUpper(name), "NEXMESH_"):
		return bad("name", "Names starting with NEXMESH_ are reserved for Nexmesh.")
	}
	return nil
}

func (s *Service) Set(ctx context.Context, owner, wsID string, in SetInput) (*Row, error) {
	if err := ValidateName(in.Name); err != nil {
		return nil, err
	}
	if in.Value != nil && len(*in.Value) > MaxValueBytes {
		return nil, bad("value", "Value is larger than %d KB.", MaxValueBytes>>10)
	}
	if strings.ContainsRune(in.Description, 0) || len(in.Description) > 300 {
		return nil, bad("description", "Description must be under 300 characters.")
	}
	w, err := s.workspace(ctx, owner, wsID)
	if err != nil {
		return nil, err
	}
	if in.ComponentID != "" {
		c, err := s.st.GetComponent(ctx, in.ComponentID)
		if err != nil || c.WorkspaceID != wsID {
			return nil, bad("componentId", "That component is not in this workspace.")
		}
	}
	existing := find(w.Variables, in.Name, in.ComponentID)
	if existing == nil && in.Value == nil {
		return nil, bad("value", "Enter a value.")
	}
	if existing == nil && len(w.Variables) >= MaxVariables {
		return nil, bad("name", "A workspace can hold at most %d variables.", MaxVariables)
	}
	// Switching plain -> secret or back needs the value, because the stored
	// form differs and a secret's value cannot be read back.
	if existing != nil && existing.Secret != in.Secret && in.Value == nil {
		return nil, bad("value", "Enter the value again when changing between secret and plain.")
	}

	sid := secretID(wsID, in.ComponentID, in.Name)
	v := domain.Variable{Name: in.Name, ComponentID: in.ComponentID, Secret: in.Secret, Description: in.Description, UpdatedAt: time.Now().UTC(), UpdatedBy: owner}
	switch {
	case in.Secret && in.Value != nil:
		if err := s.sec.Put(ctx, sid, map[string]string{"value": *in.Value}); err != nil {
			return nil, err
		}
	case !in.Secret && in.Value != nil:
		v.Value = *in.Value
		if existing != nil && existing.Secret {
			_ = s.sec.Delete(ctx, sid)
		}
	case !in.Secret && in.Value == nil && existing != nil:
		v.Value = existing.Value
	}
	if _, err := s.st.UpdateWorkspace(ctx, wsID, func(w *domain.Workspace) error {
		for i := range w.Variables {
			if w.Variables[i].Name == v.Name && w.Variables[i].ComponentID == v.ComponentID {
				w.Variables[i] = v
				return nil
			}
		}
		w.Variables = append(w.Variables, v)
		return nil
	}); err != nil {
		return nil, err
	}
	action := "variable.created"
	if existing != nil {
		action = "variable.updated"
	}
	s.aud.User(ctx, owner, wsID, action, in.Name, map[string]any{"secret": in.Secret, "componentId": in.ComponentID, "valueChanged": in.Value != nil})
	return &Row{Name: v.Name, ComponentID: v.ComponentID, Secret: v.Secret, Value: v.Value, Description: v.Description, UpdatedAt: v.UpdatedAt}, nil
}

func find(vs []domain.Variable, name, comp string) *domain.Variable {
	for i := range vs {
		if vs[i].Name == name && vs[i].ComponentID == comp {
			return &vs[i]
		}
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, owner, wsID, componentID, name string) error {
	w, err := s.workspace(ctx, owner, wsID)
	if err != nil {
		return err
	}
	v := find(w.Variables, name, componentID)
	if v == nil {
		return store.ErrNotFound
	}
	if v.Secret {
		_ = s.sec.Delete(ctx, secretID(wsID, componentID, name))
	}
	if _, err := s.st.UpdateWorkspace(ctx, wsID, func(w *domain.Workspace) error {
		out := w.Variables[:0]
		for _, x := range w.Variables {
			if !(x.Name == name && x.ComponentID == componentID) {
				out = append(out, x)
			}
		}
		w.Variables = out
		return nil
	}); err != nil {
		return err
	}
	s.aud.User(ctx, owner, wsID, "variable.deleted", name, map[string]any{"secret": v.Secret, "componentId": componentID})
	return nil
}

// Environment is the effective environment for processes in a workspace,
// secret values included. It is served only to the workspace agent.
func (s *Service) Environment(ctx context.Context, wsID string) (map[string]string, error) {
	w, err := s.st.GetWorkspace(ctx, wsID)
	if err != nil {
		return nil, err
	}
	env, err := s.src.ConnectionEnv(ctx, wsID)
	if err != nil {
		return nil, err
	}
	if env == nil {
		env = map[string]string{}
	}
	apply := func(wantComponent bool) error {
		for _, v := range w.Variables {
			if (v.ComponentID != "") != wantComponent {
				continue
			}
			val := v.Value
			if v.Secret {
				m, err := s.sec.Get(ctx, secretID(wsID, v.ComponentID, v.Name))
				if err != nil {
					if errors.Is(err, store.ErrNotFound) {
						continue
					}
					return err
				}
				val = m["value"]
			}
			env[v.Name] = val
		}
		return nil
	}
	if err := apply(false); err != nil {
		return nil, err
	}
	if err := apply(true); err != nil {
		return nil, err
	}
	return env, nil
}

// DeleteAllSecrets removes secret values of a workspace's variables (workspace deletion).
func (s *Service) DeleteAllSecrets(ctx context.Context, w *domain.Workspace) {
	for _, v := range w.Variables {
		if v.Secret {
			_ = s.sec.Delete(ctx, secretID(w.ID, v.ComponentID, v.Name))
		}
	}
}
