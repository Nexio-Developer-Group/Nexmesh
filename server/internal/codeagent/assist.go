package codeagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/component"
	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/secretscan"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

// Per-card assistance (PRD F19): Explain and Diagnose attach the card's own
// evidence (state, last connection test, health history, recent logs) to a
// request. The user sees exactly the text that will be sent before it is, the
// text is scrubbed of anything credential-shaped, and the model's suggested
// fixes arrive as change sets like any other agent change.

const (
	AssistExplain  = "explain"
	AssistDiagnose = "diagnose"

	previewTTL     = 10 * time.Minute
	maxPreviews    = 200
	maxAssistChars = 16000
)

// CardSource reads one card's evidence.
type CardSource interface {
	Get(ctx context.Context, owner, wsID, id string) (*domain.Component, error)
	Health(ctx context.Context, owner, wsID, id string, limit int) (*component.HealthHistory, error)
	Logs(ctx context.Context, owner, wsID, id string, tail int) (string, error)
}

// WithCards enables Explain and Diagnose.
func (s *Service) WithCards(c CardSource) *Service { s.cards = c; return s }

type AssistSection struct {
	Label string `json:"label"`
	Chars int    `json:"chars"`
}

// AssistPreview is exactly what Send will send, nothing more.
type AssistPreview struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	CardName   string          `json:"cardName"`
	Text       string          `json:"text"`
	Sections   []AssistSection `json:"sections"`
	Redactions int             `json:"redactions"`
	ExpiresAt  time.Time       `json:"expiresAt"`
}

type storedPreview struct {
	AssistPreview
	owner, ws, cid string
}

type previewBook struct {
	mu sync.Mutex
	m  map[string]*storedPreview
}

// stageHints are the checks to suggest for each failed stage of a connection
// test (PRD 11.5). They are fixed text, not model output.
var stageHints = map[string]string{
	"input_validation": "A required field is missing or malformed. Check the host, port and any name or path fields.",
	"dns":              "The host name did not resolve. Check for a typo, whether the name exists from the workspace network, and VPN or private DNS settings.",
	"network":          "A TCP connection could not be opened. Check the port, firewalls, and whether the service allow-lists this workspace's address.",
	"tls":              "The TLS handshake failed. Check the certificate's validity and name, whether a custom CA is needed, and whether the service expects TLS on this port.",
	"authentication":   "The host is reachable but the credentials were refused. Check the user and password or token, whether they were rotated, and which authentication method the service expects.",
	"authorization":    "The login worked but a basic read was refused. Check the account's permissions and the database or topic it is allowed to see.",
	"server_details":   "The server answered but its version could not be read or is outside the tested range. Check the version and compatibility.",
}

func (s *Service) previews() *previewBook {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.book == nil {
		s.book = &previewBook{m: map[string]*storedPreview{}}
	}
	return s.book
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n[…shortened]"
}

// PreviewAssist builds the request for one card without sending anything.
func (s *Service) PreviewAssist(ctx context.Context, owner, wsID, cid, kind string) (*AssistPreview, error) {
	if kind != AssistExplain && kind != AssistDiagnose {
		return nil, verr("kind", "invalid_kind", "Choose explain or diagnose.")
	}
	if s.cards == nil {
		return nil, verr("card", "unavailable", "Card assistance is not available.")
	}
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	c, err := s.cards.Get(ctx, owner, wsID, cid)
	if err != nil {
		return nil, err
	}
	name := c.CatalogID
	var edges []string
	if s.arch != nil {
		if v, err := s.arch.Build(ctx, owner, wsID, nil); err == nil {
			names := map[string]string{}
			for _, n := range v.Nodes {
				names[n.ID] = n.Name
				if n.ID == c.ID {
					name = n.Name
				}
			}
			for _, e := range v.Edges {
				if e.From == c.ID || e.To == c.ID {
					edges = append(edges, fmt.Sprintf("- %s -> %s (%s)", names[e.From], names[e.To], e.State))
				}
			}
		}
	}
	var sections []struct{ label, body string }
	add := func(label, body string) {
		sections = append(sections, struct{ label, body string }{label, strings.TrimRight(body, "\n")})
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Card: %s (%s", name, c.CatalogID)
	if c.Version != "" {
		fmt.Fprintf(&b, " %s", c.Version)
	}
	mode := c.Mode
	if mode == "" {
		mode = "draft"
	}
	fmt.Fprintf(&b, ")\nMode: %s\nObserved state: %s\nDesired state: %s\n", mode, c.Observed, c.Desired)
	if c.Environment != "" {
		fmt.Fprintf(&b, "Environment: %s\n", c.Environment)
	}
	if c.Message != "" {
		fmt.Fprintf(&b, "Message: %s\n", c.Message)
	}
	add("Card", b.String())

	if t := c.LastTest; t != nil {
		var tb strings.Builder
		if t.Passed {
			fmt.Fprintf(&tb, "Last connection test passed (%d ms).\n", t.LatencyMS)
		} else {
			fmt.Fprintf(&tb, "Last connection test FAILED at stage %q: %s\n", t.FailedStage, t.Message)
			if h, ok := stageHints[t.FailedStage]; ok {
				fmt.Fprintf(&tb, "Usual causes for this stage: %s\n", h)
			}
		}
		for _, st := range t.Stages {
			fmt.Fprintf(&tb, "  %s: %s\n", st.Stage, st.Status)
		}
		add("Connection test", tb.String())
	}
	if h, err := s.cards.Health(ctx, owner, wsID, cid, 10); err == nil && len(h.Samples) > 0 {
		var hb strings.Builder
		fmt.Fprintf(&hb, "Uptime over the recent window: %.0f%%.\n", h.UptimePercent)
		for _, x := range h.Samples {
			fmt.Fprintf(&hb, "  %s %s", x.At.UTC().Format("15:04:05"), x.State)
			if x.LatencyMS > 0 {
				fmt.Fprintf(&hb, " (%d ms)", x.LatencyMS)
			}
			if x.Message != "" {
				fmt.Fprintf(&hb, " %s", x.Message)
			}
			hb.WriteString("\n")
		}
		add("Recent health", hb.String())
	}
	if logs, err := s.cards.Logs(ctx, owner, wsID, cid, 60); err == nil && strings.TrimSpace(logs) != "" {
		lines := strings.Split(strings.TrimRight(logs, "\n"), "\n")
		if len(lines) > 60 {
			lines = lines[len(lines)-60:]
		}
		add("Recent logs", strings.Join(lines, "\n"))
	}
	if len(edges) > 0 {
		add("Connections", strings.Join(edges, "\n"))
	}

	var intro string
	if kind == AssistExplain {
		intro = fmt.Sprintf("Explain the card %q on my canvas in plain language: what it is for, what its current state means, and what I should check next. Do not change anything.", name)
	} else {
		intro = fmt.Sprintf("Diagnose problems with the card %q on my canvas. From the evidence below, give the most likely cause and the checks I can run, in order. If a code or configuration change would fix it, propose it as a change set for me to review. Do not say anything is fixed until I accept a change.", name)
	}
	var text strings.Builder
	text.WriteString(intro + "\n")
	var parts []AssistSection
	for _, sec := range sections {
		body := clip(sec.body, 4000)
		text.WriteString("\n## " + sec.label + "\n" + body + "\n")
		parts = append(parts, AssistSection{Label: sec.label, Chars: len(body)})
	}
	out := text.String()
	if s.secrets != nil {
		out = s.mask(ctx, wsID, out)
	}
	out, n := secretscan.Scrub(out)
	if len(out) > maxAssistChars {
		out = out[:maxAssistChars] + "\n[…shortened]"
	}
	pv := &storedPreview{owner: owner, ws: wsID, cid: cid}
	pv.AssistPreview = AssistPreview{ID: store.NewID("prv"), Kind: kind, CardName: name, Text: out, Sections: parts, Redactions: n, ExpiresAt: s.now().Add(previewTTL)}
	book := s.previews()
	book.mu.Lock()
	for id, p := range book.m {
		if p.ExpiresAt.Before(s.now()) {
			delete(book.m, id)
		}
	}
	if len(book.m) >= maxPreviews {
		for id := range book.m {
			delete(book.m, id)
			break
		}
	}
	book.m[pv.ID] = pv
	book.mu.Unlock()
	ap := pv.AssistPreview
	return &ap, nil
}

// SendAssist sends exactly the previewed text as a new conversation.
func (s *Service) SendAssist(ctx context.Context, owner, wsID, cid, previewID string) (*Session, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	book := s.previews()
	book.mu.Lock()
	pv, ok := book.m[previewID]
	if ok && (pv.owner != owner || pv.ws != wsID || pv.cid != cid) {
		ok = false
	}
	if ok && pv.ExpiresAt.Before(s.now()) {
		delete(book.m, previewID)
		ok = false
	}
	if ok {
		delete(book.m, previewID)
	}
	book.mu.Unlock()
	if !ok {
		return nil, verr("preview", "preview_expired", "That preview is no longer available. Preview again to see what will be sent.")
	}
	title := map[string]string{AssistExplain: "Explain", AssistDiagnose: "Diagnose"}[pv.Kind] + ": " + pv.CardName
	sess, err := s.CreateSession(ctx, owner, wsID, title)
	if err != nil {
		return nil, err
	}
	s.aud.User(ctx, owner, wsID, "agent.assist_sent", cid, map[string]any{"kind": pv.Kind, "chars": len(pv.Text), "redactions": pv.Redactions})
	out, err := s.Send(ctx, owner, wsID, sess.ID, pv.Text)
	if err != nil {
		_ = s.st.DeleteDoc(ctx, KindSession, sess.ID)
		return nil, err
	}
	return out, nil
}
