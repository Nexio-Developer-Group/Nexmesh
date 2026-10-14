package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/nexio-developer-group/nexmesh/server/internal/agent"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := agent.ConfigFromEnv()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := os.Getenv("NEXMESH_WORKSPACE_ROOT")
	if root == "" {
		root = "/workspace"
	}
	ag := agent.New(cfg, log)
	stopServe, err := ag.Serve(ctx, root)
	if err != nil {
		log.Error("cannot open workspace root", "root", root, "err", err)
		os.Exit(2)
	}
	defer stopServe()
	if err := ag.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("agent stopped", "err", err)
		os.Exit(1)
	}
}
