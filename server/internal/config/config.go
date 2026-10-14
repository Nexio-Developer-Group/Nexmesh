// Package config loads control-plane configuration from the environment.
package config

import (
	"os"
	"strconv"
)

type Config struct {
	Addr        string // HTTP listen address
	Store       string // "memory", "file" or "postgres"
	DataDir     string // where the file store, master key and local workspaces live (local mode)
	DatabaseURL string
	Driver      string // "fake" or "docker"
	CatalogDir  string
	PublicURL   string // how workspace containers reach the control plane
	// WorkspaceImage is the base image for workspaces: "ubuntu:24.04" runs an
	// idle container; a Nexmesh workspace image (deploy/workspace) runs the agent.
	WorkspaceImage string
	// HostGateway makes the host reachable from workspace containers as
	// host.docker.internal (where the control plane is published).
	HostGateway   bool
	MasterKey     string // base64 32-byte key for the secrets service
	MasterKeyFile string // used when MasterKey is unset; created on first run
	// AllowPrivateEgress lets Test Connection and health checks reach RFC 1918
	// addresses (self-hosted deployments). Metadata and link-local stay blocked.
	AllowPrivateEgress bool

	// PreviewDomain (NEXMESH_PREVIEW_DOMAIN) gives every previewed port its own
	// origin, p<port>-ws-<id>.<domain>, which needs wildcard DNS (use "localhost"
	// for local development). Empty serves previews sandboxed under /api/preview.
	PreviewDomain string

	// AllowShareLinks (NEXMESH_ALLOW_SHARE_LINKS=true) lets owners create
	// expiring public preview links. Off by default.
	AllowShareLinks bool

	// DefaultIdleMinutes (NEXMESH_DEFAULT_IDLE_MINUTES) is the idle-stop timeout
	// new workspaces start with; 0 means never stop automatically.
	DefaultIdleMinutes int

	// AuthDisabled (NEXMESH_AUTH=disabled) skips sign-in; every request acts as one local user. Development and tests only.
	AuthDisabled bool
	// AllowSignup lets additional users register after the first (owner) account.
	AllowSignup bool
	// AllowLoopbackEgress is for local development only: it lets probes reach 127.0.0.1.
	AllowLoopbackEgress bool
	// Host capacity limits enforced by workspace validation (PRD 6.2).
	MaxCPU       int
	MaxRAMGB     int
	MaxStorageGB int
	HostGPU      bool
}

func Load() Config {
	return Config{
		Addr:                env("NEXMESH_ADDR", ":8080"),
		Store:               env("NEXMESH_STORE", "postgres"),
		DataDir:             os.Getenv("NEXMESH_DATA_DIR"),
		DatabaseURL:         env("NEXMESH_DATABASE_URL", "postgres://nexmesh:nexmesh@localhost:5432/nexmesh?sslmode=disable"),
		Driver:              env("NEXMESH_DRIVER", "docker"),
		CatalogDir:          env("NEXMESH_CATALOG_DIR", "../catalog"),
		PublicURL:           env("NEXMESH_PUBLIC_URL", "http://host.docker.internal:8080"),
		WorkspaceImage:      env("NEXMESH_WORKSPACE_IMAGE", "ubuntu:24.04"),
		HostGateway:         os.Getenv("NEXMESH_HOST_GATEWAY") != "false",
		MasterKey:           os.Getenv("NEXMESH_MASTER_KEY"),
		MasterKeyFile:       env("NEXMESH_MASTER_KEY_FILE", "nexmesh-master.key"),
		AllowPrivateEgress:  os.Getenv("NEXMESH_ALLOW_PRIVATE_EGRESS") == "true",
		PreviewDomain:       os.Getenv("NEXMESH_PREVIEW_DOMAIN"),
		DefaultIdleMinutes:  envInt("NEXMESH_DEFAULT_IDLE_MINUTES", 0),
		AllowShareLinks:     os.Getenv("NEXMESH_ALLOW_SHARE_LINKS") == "true",
		AuthDisabled:        os.Getenv("NEXMESH_AUTH") == "disabled",
		AllowSignup:         os.Getenv("NEXMESH_ALLOW_SIGNUP") == "true",
		AllowLoopbackEgress: os.Getenv("NEXMESH_DEV_ALLOW_LOOPBACK_EGRESS") == "true",
		MaxCPU:              envInt("NEXMESH_MAX_CPU", 12),
		MaxRAMGB:            envInt("NEXMESH_MAX_RAM_GB", 24),
		MaxStorageGB:        envInt("NEXMESH_MAX_STORAGE_GB", 200),
		HostGPU:             os.Getenv("NEXMESH_HOST_GPU") == "true",
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
