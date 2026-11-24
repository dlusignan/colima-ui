// Package operations is the Operation Manager. It runs colima jobs, at most
// one per profile at a time, captures their bounded output, lets callers
// cancel and subscribe to them, and tells Observers about output and
// completion.
//
// A profile has one slot. The slot is held either by a running job (Submit) or
// by an exclusive hold (TryExclusive, used by config writes). The slot is a
// reservation made under the manager mutex; no mutex is held while a Launcher
// or a TryExclusive callback runs.
//
// Job states: running, then exactly one of succeeded (exit 0), failed (non-zero
// exit, or killed without a cancel request, or an unexpected Wait error) or
// cancelled (killed after Cancel or Shutdown). A cancel that races a clean exit
// can end succeeded; Cancel returns the true final job.
//
// Task jobs (SubmitTask) run a Go function instead of a colima process, for
// Docker work such as an image pull. They share the job table, bounded output,
// cancel, subscriptions, observers and shutdown, but they never take the
// profile's slot (a long pull must not block start, stop or restart) and are
// limited on their own: a per profile, per action cap and one running task per
// subject. Their limits are released when the final state is recorded.
//
// The manager imports internal/colima only for Process and Exit (and for
// ColimaLauncher). Tests drive it with a fake Launcher and a fake Process.
package operations
