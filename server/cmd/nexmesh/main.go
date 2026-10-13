package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/config"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/httpapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime/docker"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fatal := func(msg string, err error) {
		log.Error(msg, "err", err)
		os.Exit(1)
	}

	cat, err := catalog.Load(cfg.CatalogDir)
	if err != nil {
		fatal("catalog", err)
	}

	var st store.Store
	switch cfg.Store {
	case "memory":
		st = store.NewMemory()
		log.Warn("using in-memory store: data is lost on restart")
	default:
		st, err = store.NewPostgres(ctx, cfg.DatabaseURL)
		if err != nil {
			fatal("postgres", err)
		}
	}
	defer st.Close()

	var drv runtime.Driver
	switch cfg.Driver {
	case "fake":
		f := runtime.NewFake(runtime.Capabilities{MaxCPU: cfg.MaxCPU, MaxRAMGB: cfg.MaxRAMGB, MaxStorageGB: cfg.MaxStorageGB, GPU: cfg.HostGPU,
			GPUReason: "this execution backend has no GPU drivers installed.", StorageShrink: true})
		if ms, _ := strconv.Atoi(os.Getenv("NEXMESH_FAKE_DELAY_MS")); ms > 0 {
			f.Delay = time.Duration(ms) * time.Millisecond
		}
		drv = f
		log.Warn("using fake runtime driver: no real containers are created")
	default:
		drv = docker.New(docker.Config{MaxCPU: cfg.MaxCPU, MaxRAMGB: cfg.MaxRAMGB, MaxStorageGB: cfg.MaxStorageGB})
	}

	bus := events.NewBus()
	eng := ops.NewEngine(st, bus, log, 4)
	aud := audit.New(st, log)
	agents := agentapi.NewRegistry(st, bus)
	wsSvc := workspace.NewService(st, drv, eng, bus, aud, log).WithAgent(agents, cfg.PublicURL)

	go wsSvc.RunReconciler(ctx, 15*time.Second)
	go agents.Run(ctx)

	api := httpapi.New(log)
	api.RegisterCatalog(cat)
	agents.Register(api)
	masterKey, created, err := secrets.LoadOrCreateMasterKey(cfg.MasterKey, cfg.MasterKeyFile)
	if err != nil {
		fatal("master key", err)
	}
	if created {
		log.Warn("generated a new master key file; back it up, secrets cannot be decrypted without it", "path", cfg.MasterKeyFile)
	}
	backend, err := secrets.NewStoreBackend(st, masterKey)
	if err != nil {
		fatal("secrets", err)
	}
	if cfg.AllowLoopbackEgress {
		log.Warn("loopback egress is allowed (development only): the control plane's own services are reachable by Test Connection")
	}
	sec := secrets.New(backend)
	fp := hmac.New(sha256.New, masterKey)
	fp.Write([]byte("nexmesh/test-fingerprint/v1"))
	compSvc := component.NewService(st, cat, bus, aud).WithRuntime(drv, eng).WithConnect(component.ConnectConfig{
		Secrets: sec, Guard: netguard.New(netguard.Policy{AllowPrivate: cfg.AllowPrivateEgress, AllowLoopback: cfg.AllowLoopbackEgress}),
		FingerprintKey: fp.Sum(nil), Log: log,
	})
	agents.WithEnv(compSvc)
	wsSvc.WithUsage(compSvc)
	go compSvc.RunHealth(ctx, 30*time.Second)
	api.RegisterWorkspaces(httpapi.WorkspaceDeps{Svc: wsSvc, Driver: drv, Store: st, Bus: bus, Summary: compSvc})
	api.RegisterComponents(compSvc)
	api.RegisterConnections(compSvc)
	eng.Start(ctx) // after every handler is registered

	srv := &http.Server{Addr: cfg.Addr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("control plane listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("server", err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}
