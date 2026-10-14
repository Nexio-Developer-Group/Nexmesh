package app

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	"github.com/nexio-developer-group/nexmesh/server/internal/agent"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
)

const welcomeFile = `# Welcome to your Nexmesh workspace

This folder is the workspace root. Files you create here are real files, and the
terminal opens a real shell in this directory.

(Local development driver: the workspace runs on the machine hosting the
control plane instead of in a container.)
`

// localWorkspaceBase is the directory holding one subdirectory of files per
// local workspace (and, beside it, their snapshots).
func localWorkspaceBase() string {
	if b := os.Getenv("NEXMESH_FAKE_WORKSPACE_DIR"); b != "" {
		return b
	}
	return filepath.Join(os.TempDir(), "nexmesh-fake-workspaces")
}

// localAgentRunner starts an in-process workspace agent for the fake driver so
// local development and end-to-end tests get real files, shells and ports.
// Each workspace gets its own directory that survives stop/start.
func localAgentRunner(addr string, log *slog.Logger) func(spec runtime.WorkspaceSpec) func(remove bool) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = "8080"
	}
	control := "http://127.0.0.1:" + port
	base := localWorkspaceBase()
	return func(spec runtime.WorkspaceSpec) func(bool) {
		dir := filepath.Join(base, spec.ID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Error("local workspace dir", "err", err)
			return func(bool) {}
		}
		if ents, _ := os.ReadDir(dir); len(ents) == 0 {
			_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte(welcomeFile), 0o644)
		}
		ag := agent.New(agent.Config{ControlURL: control, WorkspaceID: spec.Env["NEXMESH_WORKSPACE_ID"], Token: spec.Env["NEXMESH_AGENT_TOKEN"]},
			log.With("local-agent", spec.ID))
		ctx, cancel := context.WithCancel(context.Background())
		stopServe, err := ag.Serve(ctx, dir)
		if err != nil {
			cancel()
			log.Error("local agent", "err", err)
			return func(bool) {}
		}
		go func() { _ = ag.Run(ctx) }()
		return func(remove bool) {
			cancel()
			stopServe()
			if remove {
				_ = os.RemoveAll(dir)
			}
		}
	}
}
