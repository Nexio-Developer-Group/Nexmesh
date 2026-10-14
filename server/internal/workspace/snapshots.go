package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/ops"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

// MaxSnapshots bounds saved copies per workspace.
const MaxSnapshots = 5

var (
	ErrSnapshotLimit      = fmt.Errorf("A workspace can keep %d snapshots. Delete one first.", MaxSnapshots)
	ErrSnapshotNotStopped = errors.New("Stop the workspace before restoring a snapshot, so no files change while it is replaced.")
	ErrSnapshotBusy       = errors.New("That snapshot is still being created.")
)

// SnapshotError is a user-correctable problem.
type SnapshotError struct{ Field, Code, Msg string }

func (e *SnapshotError) Error() string { return e.Msg }

func (s *Service) registerSnapshotOps(eng *ops.Engine) {
	eng.Register("snapshot_workspace", s.snapshotHandler)
	eng.Register("restore_snapshot", s.restoreHandler)
}

// CreateSnapshot saves the workspace's files. The workspace may be running.
func (s *Service) CreateSnapshot(ctx context.Context, owner, id, name string) (*domain.Snapshot, *domain.Operation, error) {
	w, err := s.Get(ctx, owner, id)
	if err != nil {
		return nil, nil, err
	}
	if w.Status != domain.WSReady && w.Status != domain.WSStopped {
		return nil, nil, ErrBadState
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return nil, nil, &SnapshotError{"name", "invalid", "Name the snapshot (up to 60 characters)."}
	}
	snap := domain.Snapshot{ID: store.NewID("snap"), Name: name, State: domain.SnapCreating, CreatedAt: time.Now().UTC(), CreatedBy: owner}
	if _, err := s.st.UpdateWorkspace(ctx, id, func(w *domain.Workspace) error {
		if len(w.Snapshots) >= MaxSnapshots {
			return ErrSnapshotLimit
		}
		for _, x := range w.Snapshots {
			if strings.EqualFold(x.Name, name) {
				return &SnapshotError{"name", "name_taken", "A snapshot with this name already exists."}
			}
		}
		w.Snapshots = append(w.Snapshots, snap)
		return nil
	}); err != nil {
		return nil, nil, err
	}
	op, err := s.ops.EnqueueParams(ctx, id, "", "snapshot_workspace", map[string]any{"snapshotId": snap.ID})
	if err != nil {
		s.dropSnapshot(ctx, id, snap.ID)
		return nil, nil, err
	}
	s.aud.User(ctx, owner, id, "snapshot.create", name, map[string]any{"snapshotId": snap.ID})
	return &snap, op, nil
}

func (s *Service) updateSnapshot(ctx context.Context, wsID, snapID string, fn func(*domain.Snapshot)) {
	_, _ = s.st.UpdateWorkspace(ctx, wsID, func(w *domain.Workspace) error {
		for i := range w.Snapshots {
			if w.Snapshots[i].ID == snapID {
				fn(&w.Snapshots[i])
			}
		}
		return nil
	})
}

func (s *Service) dropSnapshot(ctx context.Context, wsID, snapID string) {
	_, _ = s.st.UpdateWorkspace(ctx, wsID, func(w *domain.Workspace) error {
		out := w.Snapshots[:0]
		for _, x := range w.Snapshots {
			if x.ID != snapID {
				out = append(out, x)
			}
		}
		w.Snapshots = out
		return nil
	})
}

func (s *Service) snapshotHandler(ctx context.Context, op *domain.Operation, p ops.Progress) error {
	snapID, _ := op.Params["snapshotId"].(string)
	p.Logf("saving a copy of the workspace files")
	size, err := s.drv.SnapshotWorkspace(ctx, op.WorkspaceID, snapID)
	if err != nil {
		s.updateSnapshot(context.Background(), op.WorkspaceID, snapID, func(x *domain.Snapshot) { x.State, x.Error = domain.SnapFailed, err.Error() })
		return err
	}
	s.updateSnapshot(ctx, op.WorkspaceID, snapID, func(x *domain.Snapshot) { x.State, x.SizeBytes = domain.SnapReady, size })
	p.Logf("snapshot saved (%d bytes)", size)
	if w, err := s.st.GetWorkspace(ctx, op.WorkspaceID); err == nil {
		s.publish(w)
	}
	return nil
}

// RestoreSnapshot replaces the workspace's files with a snapshot's. It needs
// explicit confirmation because current files are overwritten.
func (s *Service) RestoreSnapshot(ctx context.Context, owner, id, snapID string, confirm bool) (*domain.Operation, error) {
	w, err := s.Get(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	if w.Status != domain.WSStopped {
		return nil, ErrSnapshotNotStopped
	}
	var snap *domain.Snapshot
	for i := range w.Snapshots {
		if w.Snapshots[i].ID == snapID {
			snap = &w.Snapshots[i]
		}
	}
	if snap == nil {
		return nil, store.ErrNotFound
	}
	if snap.State == domain.SnapCreating {
		return nil, ErrSnapshotBusy
	}
	if snap.State != domain.SnapReady {
		return nil, &SnapshotError{"snapshot", "snapshot_failed", "That snapshot did not complete and can't be restored."}
	}
	if !confirm {
		return nil, &SnapshotError{"confirm", "confirmation_required", "Restoring replaces every file in the workspace with the snapshot's. Confirm to continue."}
	}
	s.aud.User(ctx, owner, id, "snapshot.restore", snap.Name, map[string]any{"snapshotId": snapID})
	return s.ops.EnqueueParams(ctx, id, "", "restore_snapshot", map[string]any{"snapshotId": snapID})
}

func (s *Service) restoreHandler(ctx context.Context, op *domain.Operation, p ops.Progress) error {
	snapID, _ := op.Params["snapshotId"].(string)
	p.Logf("restoring files from the snapshot")
	if err := s.drv.RestoreSnapshot(ctx, op.WorkspaceID, snapID); err != nil {
		if errors.Is(err, runtime.ErrNotFound) {
			s.updateSnapshot(context.Background(), op.WorkspaceID, snapID, func(x *domain.Snapshot) { x.State, x.Error = domain.SnapFailed, "The snapshot data is missing." })
		}
		return err
	}
	p.Logf("restore complete; start the workspace to resume")
	return nil
}

// DeleteSnapshot removes a snapshot and its data.
func (s *Service) DeleteSnapshot(ctx context.Context, owner, id, snapID string) error {
	w, err := s.Get(ctx, owner, id)
	if err != nil {
		return err
	}
	var snap *domain.Snapshot
	for i := range w.Snapshots {
		if w.Snapshots[i].ID == snapID {
			snap = &w.Snapshots[i]
		}
	}
	if snap == nil {
		return store.ErrNotFound
	}
	if snap.State == domain.SnapCreating {
		return ErrSnapshotBusy
	}
	if err := s.drv.DeleteSnapshot(ctx, id, snapID); err != nil {
		return err
	}
	s.dropSnapshot(ctx, id, snapID)
	s.aud.User(ctx, owner, id, "snapshot.delete", snap.Name, map[string]any{"snapshotId": snapID})
	if w, err := s.st.GetWorkspace(ctx, id); err == nil {
		s.publish(w)
	}
	return nil
}
