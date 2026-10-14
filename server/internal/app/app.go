// Package app wires the control plane: store, runtime driver, services and
// HTTP routes. cmd/nexmesh uses it for production; tests use it to run the
// whole product in-process.
package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/nexio-developer-group/nexmesh/server/internal/auth"
	"github.com/nexio-developer-group/nexmesh/server/internal/definition"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/envvars"
	"github.com/nexio-developer-group/nexmesh/server/internal/gates"
	"github.com/nexio-developer-group/nexmesh/server/internal/gitcred"
	"github.com/nexio-developer-group/nexmesh/server/internal/gitops"
	"github.com/nexio-developer-group/nexmesh/server/internal/idle"
	"github.com/nexio-developer-group/nexmesh/server/internal/preview"
	"github.com/nexio-developer-group/nexmesh/server/internal/stacks"
	"github.com/nexio-developer-group/nexmesh/server/internal/tunnel"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/agentcfg"
	"github.com/nexio-developer-group/nexmesh/server/internal/architecture"
	"github.com/nexio-developer-group/nexmesh/server/internal/audit"
	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/changeset"
	"github.com/nexio-developer-group/nexmesh/server/internal/codeagent"
	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/config"
	"github.com/nexio-developer-group/nexmesh/server/internal/deployexport"
	"github.com/nexio-developer-group/nexmesh/server/internal/events"
	"github.com/nexio-developer-group/nexmesh/server/internal/httpapi"
	"github.com/nexio-developer-group/nexmesh/server/internal/inspector"
	"github.com/nexio-developer-group/nexmesh/server/internal/metrics"
	"github.com/nexio-developer-group/nexmesh/server/internal/netguard"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/rules"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime/docker"
	"github.com/nexio-developer-group/nexmesh/server/internal/secrets"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
	"github.com/nexio-developer-group/nexmesh/server/internal/webui"
	"github.com/nexio-developer-group/nexmesh/server/internal/wiring"
	"github.com/nexio-developer-group/nexmesh/server/internal/workspace"
	"github.com/nexio-developer-group/nexmesh/server/internal/wsfs"
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

	engine       *ops.Engine
	metrics      *metrics.Service
	adoptOnStart bool
	agent        *agentapi.Registry
	log          *slog.Logger
	closer       func()
	agentRuns    *codeagent.Service
	gateRuns     *gates.Service
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
		case "file":
			if cfg.DataDir == "" {
				return nil, fmt.Errorf("NEXMESH_STORE=file needs NEXMESH_DATA_DIR")
			}
			ps, err := store.OpenPersistent(filepath.Join(cfg.DataDir, "data.json"))
			if err != nil {
				return nil, fmt.Errorf("file store: %w", err)
			}
			st, closer = ps, ps.Close
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
			if cfg.Addr != "" {
				f.Runner = localAgentRunner(cfg.Addr, log)
				base := localWorkspaceBase()
				f.SnapshotDir = func(id string) string { return filepath.Join(base, id) }
				f.SnapshotRoot = base + "-snapshots"
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
	wireSvc := wiring.New(st, cat, compSvc, bus, aud)
	metricsSvc := metrics.New(st, cat, drv)
	envSvc := envvars.NewService(st, sec, aud, wiring.Source{Base: compSvc, Wires: wireSvc})
	agents.WithEnv(envSvc)
	wsSvc.WithOnStart(compSvc.HealthForWorkspace).WithOnEnvChange(compSvc.HealthForWorkspace)
	wsSvc.WithCleanup(envSvc.DeleteAllSecrets)
	gitSvc := gitcred.NewService(st, sec, aud)
	wsSvc.WithCleanup(gitSvc.DeleteAll)
	wsSvc.WithUsage(compSvc)

	tracker := idle.NewTracker()
	wsSvc.WithDefaultIdle(cfg.DefaultIdleMinutes).WithIdle(tracker, func(ctx context.Context, wsID string) (bool, []string) {
		var st struct {
			Busy    bool     `json:"busy"`
			Reasons []string `json:"reasons"`
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := agents.Call(cctx, wsID, "activity.status", struct{}{}, &st); err != nil {
			return false, nil // offline or old agent: nothing running that we know of
		}
		return st.Busy, st.Reasons
	})
	wsSvc.WithCleanup(func(_ context.Context, w *domain.Workspace) { tracker.Forget(w.ID); metricsSvc.Forget(w.ID) })
	api := httpapi.New(log)
	api.WithActivity(tracker)
	api.WithAuthAudit(aud)
	api.RegisterAuth(auth.NewService(st, cfg.AllowSignup), !cfg.AuthDisabled, cfg.PublicURL)
	if cfg.AuthDisabled {
		log.Warn("authentication is disabled (NEXMESH_AUTH=disabled): every request acts as one local user")
	}
	api.RegisterCatalog(cat)
	agents.Register(api)
	api.RegisterWorkspaces(httpapi.WorkspaceDeps{Svc: wsSvc, Driver: drv, Store: st, Bus: bus, Summary: compSvc, Agent: agentSvc})
	api.RegisterComponents(compSvc)
	api.RegisterVariables(envSvc)
	api.RegisterMetrics(metricsSvc)
	api.RegisterInspector(inspector.New(compSvc, aud))
	api.RegisterWiring(wireSvc)
	archSvc := architecture.New(st, cat, wireSvc)
	api.RegisterArchitecture(archSvc, aud)
	api.RegisterDeployExport(deployexport.New(st, cat, compSvc, wireSvc, wsfs.New(agents), aud), httpapi.FilesDeps{Svc: wsSvc, Agent: agents, Audit: aud})
	rulesSvc := rules.New(st, wsfs.New(agents), aud, cat)
	csSvc := changeset.New(st, wsfs.New(agents), aud).WithRules(func(ctx context.Context, wsID string, files []string) ([]changeset.AppliedRule, []string) {
		res := rulesSvc.Resolve(ctx, wsID, files)
		out := make([]changeset.AppliedRule, len(res.Rules))
		for i, r := range res.Rules {
			out[i] = changeset.AppliedRule{ID: r.ID, Title: r.Title, Scope: r.Scope, Hash: r.Hash}
		}
		return out, res.Warnings
	})
	gateSvc := gates.New(st, wsfs.New(agents), aud, csSvc)
	csSvc.WithGates(gateSvc)
	api.RegisterGates(gateSvc, httpapi.FilesDeps{Svc: wsSvc, Agent: agents, Audit: aud})
	csSvc.WithImpact(impactOf(envSvc, wireSvc, st, cat, func(ctx context.Context, wsID string) string {
		if w, err := st.GetWorkspace(ctx, wsID); err == nil {
			return w.OwnerID
		}
		return ""
	}))
	api.RegisterRules(rulesSvc, httpapi.FilesDeps{Svc: wsSvc, Agent: agents, Audit: aud})
	api.RegisterChangeSets(csSvc, httpapi.FilesDeps{Svc: wsSvc, Agent: agents, Audit: aud})
	caSvc := codeagent.New(st, wsfs.New(agents), csSvc, aud, func(ctx context.Context, owner, wsID, cfgID string) (codeagent.Model, error) {
		c, _, err := agentSvc.ChatClient(ctx, owner, wsID, cfgID)
		if err != nil {
			return nil, err
		}
		return c, nil
	}).WithContext(archSvc, envSvc).WithCanvas(canvasOps{cat: cat, comps: compSvc, wires: wireSvc}).WithCards(compSvc).WithRules(func(ctx context.Context, wsID string) ([]string, error) {
		return rulesSvc.ContextTexts(ctx, wsID), nil
	}).WithSecretValues(func(ctx context.Context, wsID string) []string {
		env, err := envSvc.Environment(ctx, wsID)
		if err != nil {
			return nil
		}
		vals := make([]string, 0, len(env))
		for k, v := range env {
			if secretish.MatchString(k) {
				vals = append(vals, v)
			}
		}
		return vals
	})
	api.RegisterCodeAgent(caSvc)
	wsSvc.WithCleanup(func(_ context.Context, w *domain.Workspace) {
		caSvc.ForgetWorkspace(w.ID)
		gateSvc.ForgetWorkspace(w.ID)
		tracker.Forget(w.ID)
		metricsSvc.Forget(w.ID)
	})
	api.RegisterAudit(aud, wsSvc)
	api.RegisterSnapshots(wsSvc)
	defSvc := definition.NewService(st, cat, wsSvc, compSvc, envSvc, eng, aud).WithAgent(agents).WithPhase3(definition.P3{Wires: wireSvc, Rules: rulesSvc, Gates: gateSvc, Agent: caSvc})
	api.RegisterDefinition(defSvc)
	api.RegisterDevContainer(defSvc, cat)
	if tpls, err := catalog.LoadTemplates(cfg.CatalogDir, cat); err != nil {
		return nil, fmt.Errorf("templates: %w", err)
	} else {
		api.RegisterStacks(stacks.NewService(st, cat, tpls, compSvc, aud))
	}
	api.RegisterGitCredentials(gitSvc)
	pv := preview.NewService(preview.Config{Domain: cfg.PreviewDomain, Key: fpKey}, st, tunnel.NewDialer(agents))
	pv.EnableSharing(cfg.AllowShareLinks, aud)
	api.RegisterPorts(httpapi.FilesDeps{Svc: wsSvc, Agent: agents, Audit: aud}, st, pv)
	api.RegisterShares(httpapi.FilesDeps{Svc: wsSvc, Agent: agents, Audit: aud}, pv)
	agents.Tap(func(ws, method string, params json.RawMessage) {
		if method == "ports.changed" {
			var p any
			_ = json.Unmarshal(params, &p)
			bus.Publish(ws, events.Event{Type: "ports", Data: p})
		}
	})
	api.RegisterGit(httpapi.FilesDeps{Svc: wsSvc, Agent: agents, Audit: aud}, gitops.NewService(agents, gitSvc, aud))
	api.RegisterConnections(compSvc)
	api.RegisterAgent(agentSvc)
	api.RegisterFiles(httpapi.FilesDeps{Svc: wsSvc, Agent: agents, Audit: aud})
	api.RegisterTerminal(httpapi.FilesDeps{Svc: wsSvc, Agent: agents, Audit: aud})
	var h http.Handler = api.Handler()
	h = withPreviewHost(h, pv, tracker)
	if opt.WithUI {
		h = withPreviewHost(withUI(api.Handler(), webui.Handler()), pv, tracker)
	}
	return &App{Handler: h, Store: st, Driver: drv, Bus: bus, Workspaces: wsSvc, Components: compSvc, Agents: agentSvc, Secrets: sec,
		engine: eng, metrics: metricsSvc, agent: agents, log: log, closer: closer, agentRuns: caSvc, gateRuns: gateSvc, adoptOnStart: cfg.Store == "file" && cfg.Driver == "fake"}, nil
}

// withPreviewHost sends requests for preview origins straight to the preview
// proxy; everything else continues to the normal handler.
func withPreviewHost(next http.Handler, pv *preview.Service, act httpapi.Activity) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ws, _, ok := pv.MatchHost(r.Host); ok {
			defer httpapi.TrackRequest(act, r, ws)()
		}
		if pv.ServeHost(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withUI routes /api, /agent, /share and /healthz to the API and everything else to the UI.
func withUI(api, ui http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/healthz" || strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/agent/") || strings.HasPrefix(p, "/share/") {
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
	go a.Workspaces.RunIdleStopper(ctx, time.Minute)
	go a.agent.Run(ctx)
	go a.Components.RunHealth(ctx, 30*time.Second)
	go a.Components.RunReconciler(ctx, 15*time.Second)
	<-ctx.Done()
}

// Start is Run without blocking.
func (a *App) Start(ctx context.Context) {
	a.engine.Start(ctx)
	if a.adoptOnStart {
		// A persistent store outlives the simulated runtime: bring workspaces
		// and their services back before reconcilers look for them.
		if ids := a.Workspaces.Adopt(ctx); len(ids) > 0 {
			a.log.Info("restored workspaces from the data file", "count", len(ids))
		}
		a.Components.Adopt(ctx)
	}
	go a.Workspaces.RunReconciler(ctx, 15*time.Second)
	go a.agent.Run(ctx)
	go a.Components.RunHealth(ctx, 30*time.Second)
	go a.Components.RunReconciler(ctx, 15*time.Second)
	go a.metrics.Run(ctx, metrics.Interval)
}

// secretish matches variable names whose values must never reach a model.
var secretish = regexp.MustCompile(`(?i)(KEY|SECRET|TOKEN|PASS|PWD|URI|URL|DSN|CREDENTIAL|AUTH)`)

func (a *App) Close() {
	if f, ok := a.Driver.(*runtime.Fake); ok {
		f.Shutdown()
	}
	if a.agentRuns != nil {
		a.agentRuns.Close()
	}
	if a.gateRuns != nil {
		a.gateRuns.Close()
	}
	a.closer()
}
