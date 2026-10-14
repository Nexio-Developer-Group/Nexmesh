package codeagent

import (
	"context"
	"strings"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

// Canvas proposals (PRD F17): the agent may suggest a new card in Draft state
// or a connection between two cards. Nothing changes on the canvas until the
// user accepts. The agent has no tool that installs, starts or deletes.

const KindProposal = "canvasproposal"

const (
	ProposalCard       = "card"
	ProposalConnection = "connection"

	ProposalPending  = "pending"
	ProposalAccepted = "accepted"
	ProposalRejected = "rejected"
	ProposalFailed   = "failed"
)

type Proposal struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId"`
	Kind        string `json:"kind"`
	Reason      string `json:"reason"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	// card
	CatalogID string `json:"catalogId,omitempty"`
	CardName  string `json:"cardName,omitempty"`
	// connection
	FromID   string `json:"fromId,omitempty"`
	ToID     string `json:"toId,omitempty"`
	FromName string `json:"fromName,omitempty"`
	ToName   string `json:"toName,omitempty"`
	// ResultID is the card or line created when the proposal was accepted.
	ResultID  string `json:"resultId,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// CanvasOps is how an accepted proposal changes the canvas.
type CanvasOps interface {
	// Catalog returns a technology's display name.
	Catalog(id string) (string, bool)
	// AddDraft adds a Draft card. Nothing is installed.
	AddDraft(ctx context.Context, owner, wsID, catalogID string, index int) (string, error)
	// CheckWire reports why a line between two cards is not possible, if it is not.
	CheckWire(ctx context.Context, owner, wsID, fromID, toID string) error
	// AddWire creates the line, as confirmed by the user.
	AddWire(ctx context.Context, owner, wsID, fromID, toID string) (string, error)
}

// WithCanvas enables card and connection proposals.
func (s *Service) WithCanvas(c CanvasOps) *Service { s.canvas = c; return s }

func (s *Service) newProposal(ctx context.Context, rc *runCtx, p *Proposal) *Proposal {
	p.ID, p.WorkspaceID, p.SessionID, p.Status = store.NewID("prop"), rc.sess.WorkspaceID, rc.sess.ID, ProposalPending
	p.CreatedAt = s.now().Format("2006-01-02T15:04:05Z")
	_ = store.PutJSON(ctx, s.st, KindProposal, p.WorkspaceID, p.ID, p)
	s.aud.Record(ctx, domain.ActorAgent, rc.sess.ID, p.WorkspaceID, "agent.canvas_proposal", p.ID, map[string]any{"kind": p.Kind, "reason": truncate(p.Reason, 200)})
	return p
}

// resolveCard finds a card by id, catalog id or name.
func (s *Service) resolveCard(ctx context.Context, owner, wsID, ref string) (id, name string, err error) {
	if s.arch == nil {
		return "", "", verr("card", "no_canvas", "The canvas is not available.")
	}
	v, aerr := s.arch.Build(ctx, owner, wsID, nil)
	if aerr != nil {
		return "", "", aerr
	}
	ref = strings.TrimSpace(ref)
	var hits []string
	var names []string
	for _, n := range v.Nodes {
		if strings.EqualFold(ref, n.ID) || strings.EqualFold(ref, n.Catalog) || strings.EqualFold(ref, n.Name) || strings.HasPrefix(strings.ToLower(n.Name), strings.ToLower(ref)+" (") {
			hits = append(hits, n.ID)
			names = append(names, n.Name)
		}
	}
	switch len(hits) {
	case 0:
		return "", "", verr("card", "no_such_card", "There is no card called "+ref+" on the canvas.")
	case 1:
		return hits[0], names[0], nil
	}
	return "", "", verr("card", "ambiguous_card", "More than one card matches "+ref+": "+strings.Join(names, ", ")+".")
}

func (s *Service) proposeCard(ctx context.Context, rc *runCtx, args map[string]any) (string, string, string) {
	cid, _ := args["catalog_id"].(string)
	reason, _ := args["reason"].(string)
	name, ok := s.canvas.Catalog(strings.TrimSpace(cid))
	if !ok {
		return "There is no technology with the id " + cid + " in the catalog.", CallError, ""
	}
	p := s.newProposal(ctx, rc, &Proposal{Kind: ProposalCard, CatalogID: strings.TrimSpace(cid), CardName: name, Reason: reason})
	return "Proposed adding " + name + " to the canvas as a Draft card. The user decides; nothing is installed.", CallOK, p.ID
}

func (s *Service) proposeConnection(ctx context.Context, rc *runCtx, args map[string]any) (string, string, string) {
	from, _ := args["from"].(string)
	to, _ := args["to"].(string)
	reason, _ := args["reason"].(string)
	fid, fname, err := s.resolveCard(ctx, rc.owner, rc.sess.WorkspaceID, from)
	if err != nil {
		return err.Error(), CallError, ""
	}
	tid, tname, err := s.resolveCard(ctx, rc.owner, rc.sess.WorkspaceID, to)
	if err != nil {
		return err.Error(), CallError, ""
	}
	if err := s.canvas.CheckWire(ctx, rc.owner, rc.sess.WorkspaceID, fid, tid); err != nil {
		return "That connection is not possible: " + err.Error(), CallError, ""
	}
	p := s.newProposal(ctx, rc, &Proposal{Kind: ProposalConnection, FromID: fid, ToID: tid, FromName: fname, ToName: tname, Reason: reason})
	return "Proposed a connection from " + fname + " to " + tname + ". The user reviews the variables it creates and decides.", CallOK, p.ID
}

func (s *Service) ListProposals(ctx context.Context, owner, wsID string) ([]*Proposal, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	ps, err := store.ListJSON[Proposal](ctx, s.st, KindProposal, wsID)
	if err != nil {
		return nil, err
	}
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && ps[j].CreatedAt > ps[j-1].CreatedAt; j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
	return ps, nil
}

func (s *Service) proposal(ctx context.Context, owner, wsID, id string) (*Proposal, error) {
	if _, err := s.workspace(ctx, owner, wsID); err != nil {
		return nil, err
	}
	p, err := store.GetJSON[Proposal](ctx, s.st, KindProposal, id)
	if err != nil || p.WorkspaceID != wsID {
		return nil, store.ErrNotFound
	}
	if p.Status != ProposalPending {
		return nil, verr("proposal", "already_decided", "This proposal has already been decided.")
	}
	return p, nil
}

// AcceptProposal applies one proposal as the user. Cards are added as Draft;
// connections are created with the user's confirmation.
func (s *Service) AcceptProposal(ctx context.Context, owner, wsID, id string) (*Proposal, error) {
	p, err := s.proposal(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	if s.canvas == nil {
		return nil, verr("proposal", "unavailable", "Canvas changes are not available.")
	}
	switch p.Kind {
	case ProposalCard:
		list, _ := s.ListProposals(ctx, owner, wsID)
		n := 0
		for _, o := range list {
			if o.Status == ProposalAccepted && o.Kind == ProposalCard {
				n++
			}
		}
		p.ResultID, err = s.canvas.AddDraft(ctx, owner, wsID, p.CatalogID, n)
	case ProposalConnection:
		p.ResultID, err = s.canvas.AddWire(ctx, owner, wsID, p.FromID, p.ToID)
	}
	if err != nil {
		p.Status, p.Error = ProposalFailed, truncate(err.Error(), 300)
		_ = store.PutJSON(ctx, s.st, KindProposal, wsID, p.ID, p)
		return p, nil
	}
	p.Status = ProposalAccepted
	_ = store.PutJSON(ctx, s.st, KindProposal, wsID, p.ID, p)
	s.aud.User(ctx, owner, wsID, "agent.canvas_proposal_accepted", p.ID, map[string]any{"kind": p.Kind, "result": p.ResultID})
	return p, nil
}

func (s *Service) RejectProposal(ctx context.Context, owner, wsID, id string) (*Proposal, error) {
	p, err := s.proposal(ctx, owner, wsID, id)
	if err != nil {
		return nil, err
	}
	p.Status = ProposalRejected
	_ = store.PutJSON(ctx, s.st, KindProposal, wsID, p.ID, p)
	s.aud.User(ctx, owner, wsID, "agent.canvas_proposal_rejected", p.ID, map[string]any{"kind": p.Kind})
	return p, nil
}
