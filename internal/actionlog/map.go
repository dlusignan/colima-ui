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
	if s.Action == operations.ActionImagePull {
		return entryForPull(s, e)
	}
	if s.Action == operations.ActionCachePrune {
		return entryForCachePrune(s, e)
	}
	var detail string
	switch s.State {
	case operations.StateSucceeded:
		e.Outcome = OutcomeSuccess
	case operations.StateFailed:
		e.Outcome = OutcomeFailed
		if s.NotRun {
			// Colima never started, so its exit status would be a lie.
			detail = "not run: profile was not stopped"
		} else if s.ExitCode != nil {
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
	if s.Action == operations.ActionDelete && s.DeleteData {
		if detail != "" {
			detail += "; "
		}
		detail += "with runtime data"
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

// ActionImagePull is the action name of a finished image pull, the same name
// the synchronous Docker write routes use.
const ActionImagePull = "docker.image.pull"

// entryForPull maps a finished image pull. The detail is the reference the
// submitter validated, never output; a subject that is not plain printable
// ASCII is left out instead of stored.
func entryForPull(s operations.JobSummary, e store.ActionEntry) (store.ActionEntry, bool) {
	e.Action = ActionImagePull
	switch s.State {
	case operations.StateSucceeded:
		e.Outcome = OutcomeSuccess
	case operations.StateFailed:
		e.Outcome = OutcomeFailed
	case operations.StateCancelled:
		e.Outcome = OutcomeCancelled
	default:
		return e, false
	}
	if d := s.Subject; d != "" && len(d) <= 255 && !strings.ContainsFunc(d, func(r rune) bool { return r < 0x20 || r > 0x7e }) {
		e.Detail = &d
	}
	return e, true
}

// GlobalProfile is the action log key of a row that belongs to no profile (a
// cache prune). The store keeps one log per profile name and refuses an empty
// one. The name starts with an underscore, which no valid profile name can, so
// it never collides with a real profile.
const GlobalProfile = "_global"

// entryForCachePrune maps a finished cache prune. The detail names the exact
// vector from the job's fixed subject (never from output), and says "not run"
// when colima never started, so a row never claims colima ran when it did not.
// A cancelled prune may have removed part of the cache.
func entryForCachePrune(s operations.JobSummary, e store.ActionEntry) (store.ActionEntry, bool) {
	e.Profile = GlobalProfile
	vector := "colima prune --force"
	if s.Subject == operations.PruneSubjectAll {
		vector += " --all"
	}
	var detail string
	switch s.State {
	case operations.StateSucceeded:
		e.Outcome = OutcomeSuccess
		detail = vector
	case operations.StateFailed:
		e.Outcome = OutcomeFailed
		if s.NotRun {
			detail = "not run: colima could not be started (" + vector + ")"
		} else {
			detail = vector + " did not succeed"
		}
	case operations.StateCancelled:
		e.Outcome = OutcomeCancelled
		if s.NotRun {
			detail = "not run: cancelled before colima started (" + vector + ")"
		} else {
			detail = vector + " cancelled; some cached files may already be removed"
		}
	default:
		return e, false
	}
	e.Detail = &detail
	return e, true
}
