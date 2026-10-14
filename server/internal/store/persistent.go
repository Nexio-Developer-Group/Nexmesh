package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
)

// Persistent is the in-memory store with its contents saved to one JSON file.
// It exists for single-machine local use (the double-click install), where
// running PostgreSQL would be unreasonable. State is saved shortly after it
// changes and again on Close; the file is replaced atomically, so a crash
// never leaves a half-written file.
type Persistent struct {
	*Memory
	path string

	mu       sync.Mutex
	lastHash string
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

type persisted struct {
	Version  int                              `json:"version"`
	WS       map[string]json.RawMessage       `json:"ws"`
	Comps    map[string]json.RawMessage       `json:"comps"`
	Ops      map[string]json.RawMessage       `json:"ops"`
	Agents   map[string]json.RawMessage       `json:"agents"`
	Audit    []*domain.AuditEvent             `json:"audit"`
	Secrets  map[string]persistedSecret       `json:"secrets"`
	Users    map[string]json.RawMessage       `json:"users"`
	Sessions map[string]json.RawMessage       `json:"sessions"`
	Health   map[string][]domain.HealthSample `json:"health"`
	Tpls     map[string]json.RawMessage       `json:"tpls"`
	Docs     map[string]persistedDoc          `json:"docs"`
}

// persistedSecret mirrors domain.SecretRecord, whose JSON tags deliberately
// hide the encrypted fields from API responses.
type persistedSecret struct {
	Ciphertext []byte    `json:"ct"`
	WrappedKey []byte    `json:"wk"`
	CreatedAt  time.Time `json:"at"`
}

type persistedDoc struct {
	WS   string          `json:"ws"`
	Data json.RawMessage `json:"data"`
	Seq  int64           `json:"seq"`
}

func rawMap(in map[string][]byte) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = json.RawMessage(v)
	}
	return out
}

func byteMap(in map[string]json.RawMessage) map[string][]byte {
	out := make(map[string][]byte, len(in))
	for k, v := range in {
		out[k] = []byte(v)
	}
	return out
}

// OpenPersistent loads path if it exists (otherwise starts empty) and begins
// saving changes in the background.
func OpenPersistent(path string) (*Persistent, error) {
	p := &Persistent{Memory: NewMemory(), path: path, stop: make(chan struct{}), done: make(chan struct{})}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var d persisted
		if err := json.Unmarshal(b, &d); err != nil {
			return nil, errors.New("the data file " + path + " is damaged (" + err.Error() + "); move it aside to start fresh")
		}
		p.load(&d)
		p.lastHash = hashOf(b)
	}
	go p.loop()
	return p, nil
}

func (p *Persistent) load(d *persisted) {
	m := p.Memory
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ws, m.comps, m.ops, m.agents = byteMap(d.WS), byteMap(d.Comps), byteMap(d.Ops), byteMap(d.Agents)
	m.users, m.sessions, m.tpls = byteMap(d.Users), byteMap(d.Sessions), byteMap(d.Tpls)
	m.audit = d.Audit
	m.docs = make(map[string]memDoc, len(d.Docs))
	for k, pd := range d.Docs {
		m.docs[k] = memDoc{ws: pd.WS, data: []byte(pd.Data), seq: pd.Seq}
		if pd.Seq > m.docSeq {
			m.docSeq = pd.Seq
		}
	}
	for id, ps := range d.Secrets {
		m.secrets[id] = &domain.SecretRecord{ID: id, Ciphertext: ps.Ciphertext, WrappedKey: ps.WrappedKey, CreatedAt: ps.CreatedAt}
	}
	if d.Health != nil {
		m.health = d.Health
	}
}

func (p *Persistent) snapshot() ([]byte, error) {
	m := p.Memory
	m.mu.Lock()
	secs := make(map[string]persistedSecret, len(m.secrets))
	for id, r := range m.secrets {
		secs[id] = persistedSecret{Ciphertext: r.Ciphertext, WrappedKey: r.WrappedKey, CreatedAt: r.CreatedAt}
	}
	docs := make(map[string]persistedDoc, len(m.docs))
	for k, md := range m.docs {
		docs[k] = persistedDoc{WS: md.ws, Data: json.RawMessage(md.data), Seq: md.seq}
	}
	d := persisted{Version: 1, Docs: docs, WS: rawMap(m.ws), Comps: rawMap(m.comps), Ops: rawMap(m.ops), Agents: rawMap(m.agents),
		Audit: m.audit, Secrets: secs, Users: rawMap(m.users), Sessions: rawMap(m.sessions), Health: m.health, Tpls: rawMap(m.tpls)}
	b, err := json.Marshal(&d)
	m.mu.Unlock()
	return b, err
}

func hashOf(b []byte) string {
	var h uint64 = 1469598103934665603
	for _, c := range b {
		h = (h ^ uint64(c)) * 1099511628211
	}
	return string(rune(len(b))) + string([]byte{byte(h), byte(h >> 8), byte(h >> 16), byte(h >> 24), byte(h >> 32), byte(h >> 40), byte(h >> 48), byte(h >> 56)})
}

// Save writes the current state if it changed since the last save.
func (p *Persistent) Save() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, err := p.snapshot()
	if err != nil {
		return err
	}
	if h := hashOf(b); h == p.lastHash {
		return nil
	} else {
		p.lastHash = h
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

func (p *Persistent) loop() {
	defer close(p.done)
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			_ = p.Save()
		case <-p.stop:
			return
		}
	}
}

// Close stops the background saver and writes a final copy.
func (p *Persistent) Close() {
	p.once.Do(func() {
		close(p.stop)
		<-p.done
		_ = p.Save()
	})
}
