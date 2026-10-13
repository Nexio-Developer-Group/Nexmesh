// Package runtime defines the RuntimeDriver boundary between the control
// plane and whatever actually runs workspaces (Docker first; Kubernetes
// later, PRD 20.3.1). Every method is idempotent so the reconciler can retry.
package runtime

import (
	"context"
	"errors"
	"io"
)

// Capabilities describe what the execution backend can do. GPU availability
// is a backend capability, not a browser-side check (PRD 13.1).
type Capabilities struct {
	MaxCPU       int     `json:"maxCpu"`
	MaxRAMGB     int     `json:"maxRamGb"`
	MaxStorageGB int     `json:"maxStorageGb"`
	FreeRAMGB    float64 `json:"freeRamGb"`
	GPU          bool    `json:"gpuAvailable"`
	GPUReason    string  `json:"gpuReason,omitempty"`
	// StorageShrink reports whether the backend can reduce a workspace's storage
	// allowance. Increasing is always supported (PRD 12.2, spike 1).
	StorageShrink bool `json:"storageShrink"`
}

type Resources struct {
	CPU       int
	RAMGB     int
	StorageGB int
	GPU       bool
}

type WorkspaceSpec struct {
	ID        string
	Name      string
	Image     string // base image reference, Ubuntu 24.04
	Resources Resources
	Env       map[string]string // e.g. agent registration (control URL, token)
}

// Observed is the actual state reported by the backend.
type Observed struct {
	Exists  bool
	Running bool
	Detail  string
}

type ServiceSpec struct {
	WorkspaceID string
	Name        string // stable name within the workspace network, e.g. "mongodb"
	Image       string // pinned image reference including tag
	Env         map[string]string
	Command     []string // optional command arguments after the image
	Ports       []int
	Volumes     []VolumeMount
	CPU         float64 // optional per-service cap; 0 = no cap
	RAMMB       int     // optional per-service cap; 0 = no cap
}

type VolumeMount struct {
	Name   string
	Path   string
	SizeGB float64
}

var (
	ErrNotFound       = errors.New("runtime: not found")
	ErrPortInUse      = errors.New("runtime: port already in use")
	ErrInsufficient   = errors.New("runtime: insufficient host resources")
	ErrGPUUnsupported = errors.New("runtime: gpu not available")
)

// Driver is implemented by the Docker backend and by the fake backend.
type Driver interface {
	Capabilities(ctx context.Context) (Capabilities, error)

	// Workspace container lifecycle. progress is called with step names
	// ("preparing", "starting") so the UI reports real provisioning state.
	CreateWorkspace(ctx context.Context, spec WorkspaceSpec, progress func(step, detail string)) error
	StartWorkspace(ctx context.Context, id string) error
	StopWorkspace(ctx context.Context, id string) error
	RemoveWorkspace(ctx context.Context, id string) error
	// UpdateResources applies CPU and RAM changes to a running workspace without a restart.
	UpdateResources(ctx context.Context, id string, res Resources) error
	// RecreateWorkspace replaces the workspace container (keeping its volume and
	// network) so changes that cannot be applied live, such as GPU access or the
	// storage allowance, take effect. Services are untouched.
	RecreateWorkspace(ctx context.Context, spec WorkspaceSpec, progress func(step, detail string)) error
	InspectWorkspace(ctx context.Context, id string) (Observed, error)

	// Exec runs a command inside the workspace container, streaming output.
	Exec(ctx context.Context, id string, cmd []string, out io.Writer) error

	// Service sidecars on the workspace's private network.
	// InstallService pulls the image and creates (but does not start) the
	// service container and its volumes. Install and start are separate
	// lifecycle actions (PRD 10 step 6).
	InstallService(ctx context.Context, spec ServiceSpec, progress func(detail string)) error
	StartService(ctx context.Context, workspaceID, name string) error
	StopService(ctx context.Context, workspaceID, name string) error
	RemoveService(ctx context.Context, workspaceID, name string, deleteData bool) error
	InspectService(ctx context.Context, workspaceID, name string) (Observed, error)
}
