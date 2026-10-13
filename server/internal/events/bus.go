// Package events is an in-process pub/sub used to push live state to the
// browser over Server-Sent Events (PRD 20.3.5: no broker needed for the MVP).
package events

import (
	"encoding/json"
	"sync"
)

type Event struct {
	Type string `json:"type"` // workspace | component | operation | component_removed
	Data any    `json:"data"`
}

type Bus struct {
	mu   sync.Mutex
	subs map[string]map[chan Event]struct{}
}

func NewBus() *Bus { return &Bus{subs: map[string]map[chan Event]struct{}{}} }

// Subscribe returns a channel of events for a workspace and a cancel func.
func (b *Bus) Subscribe(workspaceID string) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	if b.subs[workspaceID] == nil {
		b.subs[workspaceID] = map[chan Event]struct{}{}
	}
	b.subs[workspaceID][ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[workspaceID][ch]; ok {
			delete(b.subs[workspaceID], ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

// Publish never blocks: a slow subscriber drops events (the UI refetches on reconnect).
func (b *Bus) Publish(workspaceID string, e Event) {
	// Round-trip so subscribers never share mutable pointers with the caller.
	if raw, err := json.Marshal(e.Data); err == nil {
		e.Data = json.RawMessage(raw)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[workspaceID] {
		select {
		case ch <- e:
		default:
		}
	}
}
