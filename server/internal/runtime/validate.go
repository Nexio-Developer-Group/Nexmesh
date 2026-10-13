package runtime

import "fmt"

// ValidationError carries a stable code the UI can map to a field.
type ValidationError struct {
	Field   string
	Code    string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

const (
	MinCPU       = 1
	MinRAMGB     = 1
	MinStorageGB = 5
)

// ValidateResources checks a requested resource combination against the
// backend's capabilities (PRD 6.2, 15: "rejects unsupported or unavailable
// resource combinations with a clear error").
func ValidateResources(r Resources, caps Capabilities) error {
	if r.CPU < MinCPU || r.CPU > caps.MaxCPU {
		return &ValidationError{"cpu", "cpu_out_of_range", fmt.Sprintf("CPU must be between %d and %d vCPU on this host.", MinCPU, caps.MaxCPU)}
	}
	if r.RAMGB < MinRAMGB || r.RAMGB > caps.MaxRAMGB {
		return &ValidationError{"ramGb", "ram_out_of_range", fmt.Sprintf("RAM must be between %d and %d GB on this host.", MinRAMGB, caps.MaxRAMGB)}
	}
	if r.StorageGB < MinStorageGB || r.StorageGB > caps.MaxStorageGB {
		return &ValidationError{"storageGb", "storage_out_of_range", fmt.Sprintf("Storage must be between %d and %d GB on this host.", MinStorageGB, caps.MaxStorageGB)}
	}
	if caps.FreeRAMGB > 0 && float64(r.RAMGB) > caps.FreeRAMGB {
		return &ValidationError{"ramGb", "ram_unavailable", fmt.Sprintf("Requested %d GB RAM, host has %.1f GB free.", r.RAMGB, caps.FreeRAMGB)}
	}
	if r.GPU && !caps.GPU {
		msg := "GPU access is unavailable: this execution backend has no GPU drivers installed."
		if caps.GPUReason != "" {
			msg = "GPU access is unavailable: " + caps.GPUReason
		}
		return &ValidationError{"gpu", "gpu_unavailable", msg}
	}
	return nil
}
