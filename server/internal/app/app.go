// Package app wires the control plane: store, runtime driver, services and
// HTTP routes. cmd/nexmesh uses it for production; tests use it to run the
// whole product in-process.
package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/agentcfg"
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
	"github.com/nexio-developer-group/nexmesh/server/internal/webui"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
)

// Options override parts of the default wiring (tests, embedding).
type Options struct {
	Store    store.Store
	Driver   runtime.Driver
	Probe    component.ProbeFunc
	ModelRun agentcfg.RunFunc
	// WithUI serves the embedded web app at / (production).
	WithUI bool
}

type App struct {
	Handler    http.Handler
	Store      store.Store
	Driver     runtime.Driver
	Bus        *events.Bus
	Workspaces *workspace.Service
	Components *component.Service
	Agents     *agentcfg.Service
	Secrets    *secrets.Service

	engine *ops.Engine
	agent  *agentapi.Registry
	log    *slog.Logger
	closer func()
}

// New builds the control plane. Nothing runs until Run is called.
func New(ctx context.Context, cfg config.Config, log *slog.Logger, opt Options) (*App, error) {
	cat, err := catalog.Load(cfg.CatalogDir)
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	providers, err := catalog.LoadProviders(cfg.CatalogDir)
	if err != nil {
		return nil, fmt.Errorf("providers: %w", err)
	}

	st, closer := opt.Store, func() {}
	if st == nil {
		switch cfg.Store {
		case "memory":
			st = store.NewMemory()
			log.Warn("using in-memory store: data is lost on restart")
		default:
			pg, err := store.NewPostgres(ctx, cfg.DatabaseURL)
			if err != nil {
				return nil, fmt.Errorf("postgres: %w", err)
			}
			st, closer = pg, pg.Close
		}
	}

	drv := opt.Driver
	if drv == nil {
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
			drv = docker.New(docker.Config{MaxCPU: cfg.MaxCPU, MaxRAMGB: cfg.MaxRAMGB, MaxStorageGB: cfg.MaxStorageGB,
				HostGateway: cfg.HostGateway})
		}
	}

	masterKey, created, err := secrets.LoadOrCreateMasterKey(cfg.MasterKey, cfg.MasterKeyFile)
	if err != nil {
		return nil, fmt.Errorf("master key: %w", err)
	}
	if created {
		log.Warn("generated a new master key file; back it up, secrets cannot be decrypted without it", "path", cfg.MasterKeyFile)
	}
	backend, err := secrets.NewStoreBackend(st, masterKey)
	if err != nil {
		return nil, err
	}
	sec := secrets.New(backend)
	fp := hmac.New(sha256.New, masterKey)
	fp.Write([]byte("nexmesh/test-fingerprint/v1"))
	fpKey := fp.Sum(nil)

	if cfg.AllowLoopbackEgress {
		log.Warn("loopback egress is allowed (development only): the control plane's own services are reachable by Test Connection")
	}
	guard := func() *netguard.Guard {
		return netguard.New(netguard.Policy{AllowPrivate: cfg.AllowPrivateEgress, AllowLoopback: cfg.AllowLoopbackEgress})
	}

	bus := events.NewBus()
	eng := ops.NewEngine(st, bus, log, 4)
	aud := audit.New(st, log)
	agents := agentapi.NewRegistry(st, bus)
	wsSvc := workspace.NewService(st, drv, eng, bus, aud, log).WithAgent(agents, cfg.PublicURL).WithImage(cfg.WorkspaceImage)
	compSvc := component.NewService(st, cat, bus, aud).WithRuntime(drv, eng).WithConnect(component.ConnectConfig{
		Secrets: sec, Guard: guard(), FingerprintKey: fpKey, Log: log, Probe: opt.Probe,
	})
	agentSvc := agentcfg.NewService(st, sec, guard(), aud, providers, fpKey)
	if opt.ModelRun != nil {
		agentSvc.WithRunner(opt.ModelRun)
	}
	agents.WithEnv(compSvc)
	wsSvc.WithUsage(compSvc)

	api := httpapi.New(log)
	api.RegisterCatalog(cat)
	agents.Register(api)
	api.RegisterWorkspaces(httpapi.WorkspaceDeps{Svc: wsSvc, Driver: drv, Store: st, Bus: bus, Summary: compSvc, Agent: agentSvc})
	api.RegisterComponents(compSvc)
	api.RegisterConnections(compSvc)
	api.RegisterAgent(agentSvc)
	var h http.Handler = api.Handler()
	if opt.WithUI {
		h = withUI(h, webui.Handler())
	}
	return &App{Handler: h, Store: st, Driver: drv, Bus: bus, Workspaces: wsSvc, Components: compSvc, Agents: agentSvc, Secrets: sec,
		engine: eng, agent: agents, log: log, closer: closer}, nil
}

// withUI routes /api, /agent and /healthz to the API and everything else to the UI.
func withUI(api, ui http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/healthz" || len(p) >= 5 && p[:5] == "/api/" || len(p) >= 7 && p[:7] == "/agent/" {
			api.ServeHTTP(w, r)
			return
		}
		ui.ServeHTTP(w, r)
	})
}

// Run starts background work (operations, reconciler, health checks, agent
// liveness) and blocks until ctx ends.
func (a *App) Run(ctx context.Context) {
	a.engine.Start(ctx) // after every handler is registered
	go a.Workspaces.RunReconciler(ctx, 15*time.Second)
	go a.agent.Run(ctx)
	go a.Components.RunHealth(ctx, 30*time.Second)
	go a.Components.RunReconciler(ctx, 15*time.Second)
	<-ctx.Done()
}

// Start is Run without blocking.
func (a *App) Start(ctx context.Context) {
	a.engine.Start(ctx)
	go a.Workspaces.RunReconciler(ctx, 15*time.Second)
	go a.agent.Run(ctx)
	go a.Components.RunHealth(ctx, 30*time.Second)
	go a.Components.RunReconciler(ctx, 15*time.Second)
}

func (a *App) Close() { a.closer() }
