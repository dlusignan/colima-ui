package actionlog

import (
	"testing"

	"github.com/dlusignan/colima-ui/internal/operations"
)

func TestCachePruneRowsAreGlobalAndHonest(t *testing.T) {
	for _, c := range []struct {
		state   operations.State
		subject string
		notRun  bool
		outcome string
		detail  string
	}{
		{operations.StateSucceeded, operations.PruneSubjectCache, false, OutcomeSuccess, "colima prune --force"},
		{operations.StateSucceeded, operations.PruneSubjectAll, false, OutcomeSuccess, "colima prune --force --all"},
		{operations.StateFailed, operations.PruneSubjectAll, false, OutcomeFailed, "colima prune --force --all did not succeed"},
		{operations.StateFailed, operations.PruneSubjectCache, true, OutcomeFailed, "not run: colima could not be started (colima prune --force)"},
		{operations.StateCancelled, operations.PruneSubjectCache, false, OutcomeCancelled, "colima prune --force cancelled; some cached files may already be removed"},
		{operations.StateCancelled, operations.PruneSubjectAll, true, OutcomeCancelled, "not run: cancelled before colima started (colima prune --force --all)"},
		// Anything but the exact constant never turns on --all.
		{operations.StateSucceeded, "ALL; rm -rf /", false, OutcomeSuccess, "colima prune --force"},
	} {
		s := summary(operations.ActionCachePrune, c.state, intp(1))
		s.Profile, s.Subject, s.NotRun = "", c.subject, c.notRun
		e, ok := entryForJob(s)
		if !ok || e.Action != "cache-prune" || e.Profile != GlobalProfile || e.Outcome != c.outcome ||
			e.Detail == nil || *e.Detail != c.detail {
			t.Fatalf("%+v: got %+v %v", c, e, ok)
		}
	}
	if _, ok := entryForJob(summary(operations.ActionCachePrune, operations.StateRunning, nil)); ok {
		t.Fatal("running job mapped")
	}
}
