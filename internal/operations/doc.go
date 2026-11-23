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
// The manager imports internal/colima only for Process and Exit (and for
// ColimaLauncher). Tests drive it with a fake Launcher and a fake Process.
package operations
