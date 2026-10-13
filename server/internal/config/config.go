// Package config loads control-plane configuration from the environment.
package config

import (
	"os"
	"strconv"
)

type Config struct {
	Addr        string // HTTP listen address
	Store       string // "memory" or "postgres"
	DatabaseURL string
	Driver      string // "fake" or "docker"
	CatalogDir  string
	PublicURL   string // how workspace containers reach the control plane
	MasterKey   string // base64 32-byte key for the secrets service
	// Host capacity limits enforced by workspace validation (PRD 6.2).
	MaxCPU       int
	MaxRAMGB     int
	MaxStorageGB int
	HostGPU      bool
}

func Load() Config {
	return Config{
		Addr:         env("NEXMESH_ADDR", ":8080"),
		Store:        env("NEXMESH_STORE", "postgres"),
		DatabaseURL:  env("NEXMESH_DATABASE_URL", "postgres://nexmesh:nexmesh@localhost:5432/nexmesh?sslmode=disable"),
		Driver:       env("NEXMESH_DRIVER", "docker"),
		CatalogDir:   env("NEXMESH_CATALOG_DIR", "../catalog"),
		PublicURL:    env("NEXMESH_PUBLIC_URL", "http://host.docker.internal:8080"),
		MasterKey:    os.Getenv("NEXMESH_MASTER_KEY"),
		MaxCPU:       envInt("NEXMESH_MAX_CPU", 12),
		MaxRAMGB:     envInt("NEXMESH_MAX_RAM_GB", 24),
		MaxStorageGB: envInt("NEXMESH_MAX_STORAGE_GB", 200),
		HostGPU:      os.Getenv("NEXMESH_HOST_GPU") == "true",
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
