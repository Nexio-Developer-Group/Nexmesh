// Package domain holds the core Nexmesh entities. Every entity separates
// desired state (what the user configured) from observed state (what is
// actually running), per PRD Section 13.1.
package domain

import "time"

const (
	OSUbuntu2404 = "ubuntu-24.04"
)

// Workspace provisioning / lifecycle states.
const (
	WSAllocating = "allocating"
	WSPreparing  = "preparing"
	WSStarting   = "starting"
	WSReady      = "ready" // running and usable
	WSStopped    = "stopped"
	WSFailed     = "failed"
)

type Workspace struct {
	ID          string    `json:"id"`
	OwnerID     string    `json:"ownerId"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	OS          string    `json:"os"` // immutable after creation
	CPU         int       `json:"cpu"`
	RAMGB       int       `json:"ramGb"`
	StorageGB   int       `json:"storageGb"`
	GPU         bool      `json:"gpu"`
	Status      string    `json:"status"`
	Step        string    `json:"step"` // current provisioning step
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	LastOpened  time.Time `json:"lastOpenedAt"`
	// Runtimes records resolved global runtime versions (PRD Section 9).
	Runtimes map[string]RuntimeState `json:"runtimes"`
	// RestartRequired is set when desired resources differ from applied ones.
	RestartRequired bool `json:"restartRequired"`
	// AgentConnected is observed state: the workspace agent has an active
	// outbound session with the control plane (PRD 20.3.4).
	AgentConnected bool       `json:"agentConnected"`
	AgentLastSeen  *time.Time `json:"agentLastSeen,omitempty"`
	// Variables are user-defined environment variables (PRD F11). Secret
	// values live in the secrets service; only their metadata is stored here.
	Variables []Variable `json:"variables,omitempty"`
}

// Variable is one environment variable. ComponentID is empty for
// workspace-scoped variables. Value is set only when Secret is false.
type Variable struct {
	Name        string    `json:"name"`
	ComponentID string    `json:"componentId,omitempty"`
	Secret      bool      `json:"secret"`
	Value       string    `json:"value,omitempty"`
	Description string    `json:"description,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
	UpdatedBy   string    `json:"updatedBy,omitempty"`
}

type RuntimeState struct {
	Installed []string `json:"installed"`
	Active    string   `json:"active"`
}

// Component modes.
const (
	ModeInstalled = "installed"
	ModeConnected = "connected"
	ModeUnset     = "" // card dropped, mode not chosen yet
)

// Component states (PRD 11.7 plus install lifecycle).
const (
	StateDraft      = "draft"
	StateInstalling = "installing"
	StateRunning    = "running"
	StateStopped    = "stopped"
	StateReady      = "ready" // installed and usable, nothing to run (runtimes, frameworks, tools)
	StateFailed     = "failed"
	StateTesting    = "testing"
	StateTestFailed = "test_failed"
	StateTestPassed = "test_passed"
	StateConnected  = "connected"
	StateDegraded   = "degraded"
	StateDisconnect = "disconnected"
)

type Component struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	CatalogID   string `json:"catalogId"`
	Mode        string `json:"mode"`
	Version     string `json:"version"`
	// Settings holds install-time settings (ports, volume size, ...).
	Settings map[string]any `json:"settings"`
	// Connection holds non-secret connection settings (connected mode only).
	Connection  map[string]any `json:"connection,omitempty"`
	ConnName    string         `json:"connName,omitempty"`
	Environment string         `json:"environment,omitempty"`
	SecretRef   string         `json:"secretRef,omitempty"`
	LastTest    *TestResult    `json:"lastTest,omitempty"`
	Desired     string         `json:"desiredState"`
	Observed    string         `json:"observedState"`
	Message     string         `json:"message,omitempty"`
	Progress    int            `json:"progress"`
	X           float64        `json:"x"`
	Y           float64        `json:"y"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

type TestResult struct {
	Passed        bool          `json:"passed"`
	FailedStage   string        `json:"failedStage,omitempty"`
	Message       string        `json:"message,omitempty"`
	Warnings      []string      `json:"warnings,omitempty"`
	Stages        []StageResult `json:"stages"`
	LatencyMS     int64         `json:"latencyMs,omitempty"`
	ServerVersion string        `json:"serverVersion,omitempty"`
	At            time.Time     `json:"at"`
	// Fingerprint ties the result to the exact values tested (PRD 11.5).
	Fingerprint string `json:"fingerprint"`
}

type StageResult struct {
	Stage   string `json:"stage"`
	Status  string `json:"status"` // pending|running|passed|failed|skipped
	Message string `json:"message,omitempty"`
}

// Operations are the durable async units of work (PRD 13.1, 20.3.5).
const (
	OpPending   = "pending"
	OpRunning   = "running"
	OpSucceeded = "succeeded"
	OpFailed    = "failed"
	OpCancelled = "cancelled"
)

type Operation struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspaceId"`
	ComponentID string         `json:"componentId,omitempty"`
	Type        string         `json:"type"` // provision, install_component, start_component, ...
	Params      map[string]any `json:"params,omitempty"`
	Status      string         `json:"status"`
	Progress    int            `json:"progress"`
	Log         []string       `json:"log"`
	Error       string         `json:"error,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

type AgentConfig struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspaceId"` // empty while attached to a workspace being created
	Name        string         `json:"name"`
	Provider    string         `json:"provider"`
	ModelID     string         `json:"modelId"`
	Endpoint    string         `json:"endpoint,omitempty"`
	Protocol    string         `json:"protocol,omitempty"`
	Params      map[string]any `json:"params"`
	SecretRef   string         `json:"secretRef,omitempty"`
	LastTest    *TestResult    `json:"lastTest,omitempty"`
	IsDefault   bool           `json:"isDefault"`
	CreatedAt   time.Time      `json:"createdAt"`
}

// Actor types recorded on audit events (PRD 18.4 rule 4).
const (
	ActorUser   = "user"
	ActorAgent  = "agent"
	ActorSystem = "system"
)

type AuditEvent struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspaceId,omitempty"`
	ActorType   string         `json:"actorType"`
	ActorID     string         `json:"actorId,omitempty"`
	Action      string         `json:"action"`
	Target      string         `json:"target,omitempty"`
	Detail      map[string]any `json:"detail,omitempty"` // never contains secret values
	At          time.Time      `json:"at"`
}

// SecretRecord is the encrypted form persisted by the secrets service.
type SecretRecord struct {
	ID         string    `json:"id"`
	Ciphertext []byte    `json:"-"`
	WrappedKey []byte    `json:"-"`
	CreatedAt  time.Time `json:"createdAt"`
}

// User is a Nexmesh account. PasswordHash is never serialised to clients.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	Role         string    `json:"role"`
	PasswordHash string    `json:"passwordHash,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Session is a browser session. ID is the SHA-256 of the cookie token, so a
// database leak does not yield usable cookies.
type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

const RoleOwner = "owner"
