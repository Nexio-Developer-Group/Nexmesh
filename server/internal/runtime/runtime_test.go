package runtime

import (
	"context"
	"errors"
	"testing"
)

var caps = Capabilities{MaxCPU: 12, MaxRAMGB: 24, MaxStorageGB: 200}

func TestValidateResources(t *testing.T) {
	cases := []struct {
		name string
		r    Resources
		code string
	}{
		{"ok", Resources{4, 8, 40, false}, ""},
		{"cpu high", Resources{13, 8, 40, false}, "cpu_out_of_range"},
		{"ram high", Resources{4, 25, 40, false}, "ram_out_of_range"},
		{"storage low", Resources{4, 8, 1, false}, "storage_out_of_range"},
		{"gpu", Resources{4, 8, 40, true}, "gpu_unavailable"},
	}
	for _, c := range cases {
		err := ValidateResources(c.r, caps)
		if c.code == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Code != c.code {
			t.Errorf("%s: got %v want %s", c.name, err, c.code)
		}
	}
	free := caps
	free.FreeRAMGB = 11.2
	var ve *ValidationError
	if err := ValidateResources(Resources{2, 16, 80, false}, free); !errors.As(err, &ve) || ve.Code != "ram_unavailable" {
		t.Errorf("free ram: %v", err)
	}
}

func TestFakeLifecycle(t *testing.T) {
	f := NewFake(caps)
	ctx := context.Background()
	var steps []string
	spec := WorkspaceSpec{ID: "w1", Resources: Resources{2, 4, 20, false}}
	if err := f.CreateWorkspace(ctx, spec, func(s, _ string) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("steps %v", steps)
	}
	o, _ := f.InspectWorkspace(ctx, "w1")
	if !o.Exists || !o.Running {
		t.Fatalf("%+v", o)
	}
	_ = f.StopWorkspace(ctx, "w1")
	o, _ = f.InspectWorkspace(ctx, "w1")
	if o.Running {
		t.Fatal("still running")
	}
	if err := f.CreateWorkspace(ctx, WorkspaceSpec{ID: "w2", Resources: Resources{99, 4, 20, false}}, func(string, string) {}); err == nil {
		t.Fatal("expected validation error")
	}
	_ = f.RemoveWorkspace(ctx, "w1")
	o, _ = f.InspectWorkspace(ctx, "w1")
	if o.Exists {
		t.Fatal("not removed")
	}
}
