package actionlog

import (
	"fmt"
	"strings"
	"time"

	"github.com/dlusignan/colima-ui/internal/operations"
	"github.com/dlusignan/colima-ui/internal/store"
)

// Outcome values stored in action_log.
const (
	OutcomeSuccess   = "success"
	OutcomeFailed    = "failed"
	OutcomeCancelled = "cancelled"
)

// ActionConfigChange is the action name of an applied configuration change.
const ActionConfigChange = "config_change"

// entryForJob maps a finished job to a row. ok is false for a state that is
// not final.
func entryForJob(s operations.JobSummary) (e store.ActionEntry, ok bool) {
	e = store.ActionEntry{
		Profile:   s.Profile,
		StartedAt: s.StartedAt,
		Action:    string(s.Action),
	}
	var detail string
	switch s.State {
	case operations.StateSucceeded:
		e.Outcome = OutcomeSuccess
	case operations.StateFailed:
		e.Outcome = OutcomeFailed
		if s.ExitCode != nil {
			detail = fmt.Sprintf("colima exited with status %d", *s.ExitCode)
		} else {
			detail = "colima did not exit normally"
		}
	case operations.StateCancelled:
		e.Outcome = OutcomeCancelled
		detail = "cancelled before it finished"
	default:
		return e, false
	}
	if detail != "" {
		e.Detail = &detail
	}
	return e, true
}

var configFieldOrder = []string{"cpu", "memory", "disk"}

// entryForConfig maps a config change to a row. ok is false when no known
// field was changed.
func entryForConfig(profile string, fields []string, now time.Time) (e store.ActionEntry, ok bool) {
	var names []string
	for _, f := range configFieldOrder {
		for _, g := range fields {
			if f == g {
				names = append(names, f)
				break
			}
		}
	}
	if len(names) == 0 {
		return e, false
	}
	detail := "changed " + strings.Join(names, ", ")
	return store.ActionEntry{
		Profile:   profile,
		StartedAt: now,
		Action:    ActionConfigChange,
		Outcome:   OutcomeSuccess,
		Detail:    &detail,
	}, true
}
