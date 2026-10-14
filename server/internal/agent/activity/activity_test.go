package activity

import "testing"

func TestStatusCombinesSourcesAndRunningWork(t *testing.T) {
	m := New()
	if st := m.Status(); st.Busy || len(st.Reasons) != 0 {
		t.Fatalf("fresh monitor busy: %+v", st)
	}
	open := false
	m.Add(func() string {
		if open {
			return "a preview connection is open"
		}
		return ""
	})
	end := m.Begin()
	if st := m.Status(); !st.Busy || st.Reasons[0] != "a command is running" {
		t.Fatalf("running command: %+v", st)
	}
	end()
	end2 := m.Begin()
	end2()
	if st := m.Status(); st.Busy {
		t.Fatalf("finished work still busy: %+v", st)
	}
	open = true
	if st := m.Status(); !st.Busy || len(st.Reasons) != 1 {
		t.Fatalf("source: %+v", st)
	}
}
