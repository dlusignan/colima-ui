package operations

import (
	"context"
	"fmt"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// Subjects of a cache-prune job: which vector it ran. The action log reads
// them, so they are fixed strings, never user input.
const (
	PruneSubjectCache = "cache"
	PruneSubjectAll   = "all"
)

// Closing lines of a cache-prune job. They are fixed text: colima's own output
// is streamed as it comes, but what this task adds never carries stderr, paths
// or an error string.
const (
	PruneCancelledLine = "Cache prune cancelled. Some cached files may already have been removed."
	pruneNotStartedMsg = "error: colima could not be started."
	pruneFailedMsg     = "error: colima prune did not finish."
)

// CachePruneTask is the body of a cache-prune job: it runs `colima prune
// --force` (plus --all when includeLima) through the adapter and streams its
// output. The task returns only after colima has been reaped, so cancel and
// shutdown wait for the child to be gone. A cancelled prune can leave a
// partly cleared cache; the closing line says so.
func CachePruneTask(s colima.Streamer, includeLima bool) TaskFunc {
	return func(ctx context.Context, emit func(string)) error {
		p, err := colima.Prune(ctx, s, includeLima)
		if err != nil {
			if ctx.Err() != nil {
				emit(PruneCancelledLine)
				return fmt.Errorf("%w: %w", ErrTaskNotRun, ctx.Err())
			}
			emit(pruneNotStartedMsg)
			return fmt.Errorf("%w: colima prune could not start", ErrTaskNotRun)
		}
		defer p.Cancel()
		for line := range p.Lines() {
			emit(line)
		}
		exit, werr := p.Wait()
		switch {
		case werr == nil && !exit.Killed && exit.ExitCode == 0:
			return nil // a cancel that raced a clean exit still succeeded
		case ctx.Err() != nil:
			emit(PruneCancelledLine)
			return ctx.Err()
		case werr != nil || exit.Killed:
			emit(pruneFailedMsg)
			return fmt.Errorf("colima prune did not finish")
		}
		emit(fmt.Sprintf("error: colima prune exited with status %d.", exit.ExitCode))
		return fmt.Errorf("colima prune failed")
	}
}
