package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/domain"
	"github.com/nexio-developer-group/nexmesh/server/internal/runtime"
	"github.com/nexio-developer-group/nexmesh/server/internal/store"
)

func snapRig(t *testing.T) (*idleRig, *runtime.Fake) {
	r := newIdleRig(t)
	// the rig's driver is the Fake created in newIdleRig; fetch it through a second reference
	return r, r.drv
}

func (r *idleRig) waitSnap(t *testing.T, id, state string) domain.Snapshot {
	t.Helper()
	for i := 0; i < 300; i++ {
		w, _ := r.st.GetWorkspace(r.ctx, "ws1")
		for _, s := range w.Snapshots {
			if s.ID == id && s.State == state {
				return s
			}
		}
		timeSleep()
	}
	w, _ := r.st.GetWorkspace(r.ctx, "ws1")
	t.Fatalf("snapshot %s never reached %q: %+v", id, state, w.Snapshots)
	return domain.Snapshot{}
}

func TestSnapshotLifecycle(t *testing.T) {
	r, _ := snapRig(t)
	snap, _, err := r.svc.CreateSnapshot(r.ctx, "u1", "ws1", "  before refactor ")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Name != "before refactor" || snap.State != domain.SnapCreating {
		t.Fatalf("%+v", snap)
	}
	done := r.waitSnap(t, snap.ID, domain.SnapReady)
	if done.SizeBytes <= 0 {
		t.Fatalf("size: %+v", done)
	}

	// Validation and ownership.
	var se *SnapshotError
	if _, _, err := r.svc.CreateSnapshot(r.ctx, "u1", "ws1", "BEFORE refactor"); !errors.As(err, &se) || se.Code != "name_taken" {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, _, err := r.svc.CreateSnapshot(r.ctx, "u1", "ws1", "  "); !errors.As(err, &se) || se.Field != "name" {
		t.Fatalf("empty name: %v", err)
	}
	if _, _, err := r.svc.CreateSnapshot(r.ctx, "intruder", "ws1", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other user: %v", err)
	}

	// Restore needs a stopped workspace and an explicit confirmation.
	if _, err := r.svc.RestoreSnapshot(r.ctx, "u1", "ws1", snap.ID, true); !errors.Is(err, ErrSnapshotNotStopped) {
		t.Fatalf("restore while running: %v", err)
	}
	if _, err := r.svc.Stop(r.ctx, "u1", "ws1"); err != nil {
		t.Fatal(err)
	}
	r.waitStatus(t, domain.WSStopped)
	if _, err := r.svc.RestoreSnapshot(r.ctx, "u1", "ws1", snap.ID, false); !errors.As(err, &se) || se.Code != "confirmation_required" {
		t.Fatalf("unconfirmed restore: %v", err)
	}
	if _, err := r.svc.RestoreSnapshot(r.ctx, "u1", "ws1", "snap_missing", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown snapshot: %v", err)
	}
	if op, err := r.svc.RestoreSnapshot(r.ctx, "u1", "ws1", snap.ID, true); err != nil || op.Type != "restore_snapshot" {
		t.Fatalf("restore: %v %+v", err, op)
	}

	if err := r.svc.DeleteSnapshot(r.ctx, "u1", "ws1", snap.ID); err != nil {
		t.Fatal(err)
	}
	if w, _ := r.st.GetWorkspace(r.ctx, "ws1"); len(w.Snapshots) != 0 {
		t.Fatalf("not deleted: %+v", w.Snapshots)
	}
	if err := r.svc.DeleteSnapshot(r.ctx, "u1", "ws1", snap.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("double delete: %v", err)
	}

	evs, _ := r.st.ListAudit(r.ctx, "ws1", 50)
	seen := map[string]bool{}
	for _, e := range evs {
		seen[e.Action] = true
	}
	for _, a := range []string{"snapshot.create", "snapshot.restore", "snapshot.delete"} {
		if !seen[a] {
			t.Errorf("missing audit %s", a)
		}
	}
}

func TestSnapshotLimitAndFailure(t *testing.T) {
	r, drv := snapRig(t)
	for i := 0; i < MaxSnapshots; i++ {
		s, _, err := r.svc.CreateSnapshot(r.ctx, "u1", "ws1", "s"+string(rune('a'+i)))
		if err != nil {
			t.Fatal(err)
		}
		r.waitSnap(t, s.ID, domain.SnapReady)
	}
	if _, _, err := r.svc.CreateSnapshot(r.ctx, "u1", "ws1", "one too many"); !errors.Is(err, ErrSnapshotLimit) {
		t.Fatalf("limit: %v", err)
	}
	w, _ := r.st.GetWorkspace(r.ctx, "ws1")
	_ = r.svc.DeleteSnapshot(r.ctx, "u1", "ws1", w.Snapshots[0].ID)

	drv.FailNext["snapshot"] = errors.New("no space left on device")
	s, _, err := r.svc.CreateSnapshot(r.ctx, "u1", "ws1", "will fail")
	if err != nil {
		t.Fatal(err)
	}
	failed := r.waitSnap(t, s.ID, domain.SnapFailed)
	if !strings.Contains(failed.Error, "no space left") {
		t.Fatalf("failure reason not recorded: %+v", failed)
	}
	if _, err := r.svc.Stop(r.ctx, "u1", "ws1"); err != nil {
		t.Fatal(err)
	}
	r.waitStatus(t, domain.WSStopped)
	var se *SnapshotError
	if _, err := r.svc.RestoreSnapshot(r.ctx, "u1", "ws1", s.ID, true); !errors.As(err, &se) || se.Code != "snapshot_failed" {
		t.Fatalf("restoring a failed snapshot: %v", err)
	}
	_ = context.Background()
}
