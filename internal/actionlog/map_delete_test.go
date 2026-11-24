package actionlog

import (
	"testing"

	"github.com/dlusignan/colima-ui/internal/operations"
)

func TestDeleteRowsNoteRuntimeData(t *testing.T) {
	for _, c := range []struct {
		state  operations.State
		code   *int
		data   bool
		detail string
	}{
		{operations.StateSucceeded, intp(0), false, ""},
		{operations.StateSucceeded, intp(0), true, "with runtime data"},
		{operations.StateFailed, intp(2), true, "colima exited with status 2; with runtime data"},
		{operations.StateCancelled, nil, true, "cancelled before it finished; with runtime data"},
	} {
		s := summary(operations.ActionDelete, c.state, c.code)
		s.DeleteData = c.data
		e, ok := entryForJob(s)
		if !ok || e.Action != "delete" {
			t.Fatalf("%+v %v", e, ok)
		}
		switch {
		case c.detail == "" && e.Detail != nil:
			t.Fatalf("detail %q, want none", *e.Detail)
		case c.detail != "" && (e.Detail == nil || *e.Detail != c.detail):
			t.Fatalf("detail %v, want %q", e.Detail, c.detail)
		}
	}
	// The flag never leaks into other actions.
	s := summary(operations.ActionStop, operations.StateSucceeded, intp(0))
	s.DeleteData = true
	if e, _ := entryForJob(s); e.Detail != nil {
		t.Fatalf("stop detail %q", *e.Detail)
	}
}
