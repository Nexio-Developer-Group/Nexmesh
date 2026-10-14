// Package metrics reports resource use per card (PRD F5, Phase 3): CPU, memory
// and storage for installed components, and latency for connected ones. It keeps
// only a short in-memory trend (no long-term retention, which is out of scope).
package metrics

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

const (
	// HistoryLen is how many samples are kept per card: 60 at 30 s is 30 minutes.
	HistoryLen = 60
	// Interval is the sampling period.
	Interval = 30 * time.Second
)

type Sample struct {
	At         time.Time `json:"at"`
	CPUPercent float64   `json:"cpuPercent"`
	MemUsedMB  float64   `json:"memUsedMb"`
	MemLimitMB float64   `json:"memLimitMb"`
	DiskUsedMB float64   `json:"diskUsedMb"` // -1 when not measurable
	LatencyMS  int64     `json:"latencyMs,omitempty"`
	Simulated  bool      `json:"simulated,omitempty"`
}

type Card struct {
	ID        string   `json:"id"`
	CatalogID string   `json:"catalogId"`
	Name      string   `json:"name"`
	Mode      string   `json:"mode"` // installed | connected
	Current   *Sample  `json:"current,omitempty"`
	History   []Sample `json:"history"`
	// Unavailable explains why a card has no reading (stopped, not measurable).
	Unavailable string `json:"unavailable,omitempty"`
}

type Snapshot struct {
	At        time.Time `json:"at"`
	Workspace *Card     `json:"workspace,omitempty"`
	Limits    struct {
		CPU       int `json:"cpu"`
		RAMGB     int `json:"ramGb"`
		StorageGB int `json:"storageGb"`
	} `json:"limits"`
	Components []Card `json:"components"`
	Simulated  bool   `json:"simulated,omitempty"`
}

type Service struct {
	st   store.Store
	cat  *catalog.Catalog
	sp   runtime.StatsProvider // nil when the backend cannot report
	mu   sync.Mutex
	hist map[string][]Sample
}

func New(st store.Store, cat *catalog.Catalog, drv runtime.Driver) *Service {
	sp, _ := drv.(runtime.StatsProvider)
	return &Service{st: st, cat: cat, sp: sp, hist: map[string][]Sample{}}
}

func mb(b int64) float64 {
	if b < 0 {
		return -1
	}
	return float64(b) / (1 << 20)
}

func toSample(s runtime.Stats, at time.Time) Sample {
	return Sample{At: at, CPUPercent: round1(s.CPUPercent), MemUsedMB: round1(mb(s.MemUsedBytes)), MemLimitMB: round1(mb(s.MemLimitBytes)), DiskUsedMB: round1(mb(s.DiskUsedBytes)), Simulated: s.Simulated}
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }

func (s *Service) push(key string, sm Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.hist[key]
	if n := len(h); n > 0 && sm.At.Sub(h[n-1].At) < 5*time.Second {
		h[n-1] = sm // a page polling quickly updates the latest point instead of flooding the trend
		s.hist[key] = h
		return
	}
	h = append(h, sm)
	if len(h) > HistoryLen {
		h = h[len(h)-HistoryLen:]
	}
	s.hist[key] = h
}

func (s *Service) history(key string) []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Sample(nil), s.hist[key]...)
}

// Forget drops a workspace's history (workspace deleted).
func (s *Service) Forget(wsID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.hist {
		if len(k) > len(wsID) && k[:len(wsID)+1] == wsID+"/" {
			delete(s.hist, k)
		}
	}
}

// service container name for an installed card (see component.serviceName).
func svcName(def *catalog.Component) string { return def.ID }

// Sample takes a reading of every running container of a workspace and records
// it. It is what the background sampler calls.
func (s *Service) Sample(ctx context.Context, wsID string) {
	_, _ = s.collect(ctx, wsID)
}

// Collect returns the current readings and recent trend. It records the
// reading too, so a page that is open sees the trend grow.
func (s *Service) Collect(ctx context.Context, owner, wsID string) (*Snapshot, error) {
	w, err := s.st.GetWorkspace(ctx, wsID)
	if err != nil || w.OwnerID != owner {
		return nil, store.ErrNotFound
	}
	return s.collect(ctx, wsID)
}

func (s *Service) collect(ctx context.Context, wsID string) (*Snapshot, error) {
	w, err := s.st.GetWorkspace(ctx, wsID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	snap := &Snapshot{At: now, Components: []Card{}}
	snap.Limits.CPU, snap.Limits.RAMGB, snap.Limits.StorageGB = w.CPU, w.RAMGB, w.StorageGB
	if s.sp == nil {
		return snap, nil
	}
	read := func(key string, fn func() (runtime.Stats, error), card *Card) {
		st, err := fn()
		if err != nil {
			card.Unavailable = "Not running, so there is nothing to measure."
			card.History = s.history(key)
			return
		}
		sm := toSample(st, now)
		card.Current = &sm
		if st.Simulated {
			snap.Simulated = true
		}
		s.push(key, sm)
		card.History = s.history(key)
	}
	if w.Status == domain.WSReady {
		c := &Card{ID: w.ID, Name: "Workspace", Mode: "installed"}
		read(wsID+"/workspace", func() (runtime.Stats, error) { return s.sp.WorkspaceStats(ctx, wsID) }, c)
		snap.Workspace = c
	}
	comps, err := s.st.ListComponents(ctx, wsID)
	if err != nil {
		return nil, err
	}
	for _, comp := range comps {
		def, ok := s.cat.Get(comp.CatalogID)
		if !ok {
			continue
		}
		card := Card{ID: comp.ID, CatalogID: comp.CatalogID, Name: def.Name, Mode: "installed"}
		switch {
		case comp.Mode == domain.ModeInstalled && def.Install != nil && def.Install.Strategy == "image":
			if comp.Observed != domain.StateRunning {
				card.Unavailable = "Not running, so there is nothing to measure."
				card.History = s.history(wsID + "/" + comp.ID)
			} else {
				read(wsID+"/"+comp.ID, func() (runtime.Stats, error) { return s.sp.ServiceStats(ctx, wsID, svcName(def)) }, &card)
			}
		case comp.Mode == domain.ModeConnected:
			card.Mode = "connected"
			card.Name = def.Name + " (" + comp.ConnName + ")"
			hs, _ := s.st.ListHealth(ctx, comp.ID, HistoryLen)
			// ListHealth is newest first; trends read oldest first.
			for i := len(hs) - 1; i >= 0; i-- {
				card.History = append(card.History, Sample{At: hs[i].At, LatencyMS: hs[i].LatencyMS, DiskUsedMB: -1})
			}
			if n := len(card.History); n > 0 {
				cur := card.History[n-1]
				card.Current = &cur
			} else {
				card.Unavailable = "No health check has run yet."
			}
		default:
			continue
		}
		snap.Components = append(snap.Components, card)
	}
	sort.SliceStable(snap.Components, func(i, j int) bool { return snap.Components[i].Name < snap.Components[j].Name })
	return snap, nil
}

// Run samples every ready workspace until ctx ends.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			list, err := s.st.ListWorkspaces(ctx, "")
			if err != nil {
				continue
			}
			for _, w := range list {
				if w.Status == domain.WSReady {
					s.Sample(ctx, w.ID)
				}
			}
		}
	}
}
