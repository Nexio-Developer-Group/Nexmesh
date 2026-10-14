package runtime

import "context"

// Stats is a point-in-time reading of a container's resource use (PRD F5).
type Stats struct {
	// CPUPercent is of one core, as `docker stats` reports it (100 = one full core).
	CPUPercent    float64 `json:"cpuPercent"`
	MemUsedBytes  int64   `json:"memUsedBytes"`
	MemLimitBytes int64   `json:"memLimitBytes"`
	// DiskUsedBytes is the size of the container's data, or -1 when it cannot be measured.
	DiskUsedBytes int64 `json:"diskUsedBytes"`
	// Simulated marks readings from the simulated runtime, not a real container.
	Simulated bool `json:"simulated,omitempty"`
}

// StatsProvider is implemented by drivers that can report resource use. It is
// separate from Driver so a backend without it simply reports no metrics.
type StatsProvider interface {
	WorkspaceStats(ctx context.Context, id string) (Stats, error)
	ServiceStats(ctx context.Context, workspaceID, name string) (Stats, error)
}
