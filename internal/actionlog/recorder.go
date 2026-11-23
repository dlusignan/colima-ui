package actionlog

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlusignan/colima-ui/internal/operations"
	"github.com/dlusignan/colima-ui/internal/store"
)

const (
	defaultQueueSize    = 64
	defaultWriteTimeout = 5 * time.Second
)

// sink is the part of the store the recorder writes through.
type sink interface {
	AppendAction(ctx context.Context, e store.ActionEntry) error
}

// Option configures a Recorder.
type Option func(*Recorder)

// WithClock sets the time source used for config change rows.
func WithClock(now func() time.Time) Option { return func(r *Recorder) { r.now = now } }

// WithQueueSize sets the queue capacity (default 64).
func WithQueueSize(n int) Option {
	return func(r *Recorder) {
		if n > 0 {
			r.queueSize = n
		}
	}
}

// WithWriteTimeout bounds each store write (default 5 s).
func WithWriteTimeout(d time.Duration) Option {
	return func(r *Recorder) {
		if d > 0 {
			r.writeTimeout = d
		}
	}
}

// WithLogger sets the logger (default slog.Default()).
func WithLogger(l *slog.Logger) Option {
	return func(r *Recorder) {
		if l != nil {
			r.log = l
		}
	}
}

// Recorder writes action log rows without ever blocking its callers.
type Recorder struct {
	sink         sink
	now          func() time.Time
	queueSize    int
	writeTimeout time.Duration
	log          *slog.Logger

	queue chan store.ActionEntry
	stop  chan struct{}
	done  chan struct{}

	mu     sync.RWMutex
	closed bool

	abandoned   atomic.Bool
	dropped     atomic.Uint64
	overflowing atomic.Bool
	failing     bool // writer goroutine only
}

var _ operations.Observer = (*Recorder)(nil)

// New starts a Recorder writing to s. Call Close to stop it.
func New(s sink, opts ...Option) *Recorder {
	r := &Recorder{
		sink:         s,
		now:          time.Now,
		queueSize:    defaultQueueSize,
		writeTimeout: defaultWriteTimeout,
		log:          slog.Default(),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	for _, o := range opts {
		o(r)
	}
	r.queue = make(chan store.ActionEntry, r.queueSize)
	go r.run()
	return r
}

// JobOutput implements operations.Observer and does nothing.
func (r *Recorder) JobOutput(operations.JobLine) {}

// JobFinished implements operations.Observer. It never blocks.
func (r *Recorder) JobFinished(s operations.JobSummary) {
	e, ok := entryForJob(s)
	if !ok {
		r.log.Warn("action log: job finished in a non-final state", "profile", s.Profile, "action", string(s.Action))
		return
	}
	r.enqueue(e)
}

// RecordConfigChange records an applied config change. fields are the changed
// field names (cpu, memory, disk). It never blocks.
func (r *Recorder) RecordConfigChange(profile string, fields []string) {
	e, ok := entryForConfig(profile, fields, r.now())
	if !ok {
		return
	}
	r.enqueue(e)
}

// Dropped is the number of rows lost to a full queue, a closed recorder or a
// Close deadline.
func (r *Recorder) Dropped() uint64 { return r.dropped.Load() }

func (r *Recorder) enqueue(e store.ActionEntry) {
	if !r.tryQueue(e) {
		r.dropped.Add(1)
		// Log once per overflow burst, outside the lock.
		if r.overflowing.CompareAndSwap(false, true) {
			r.log.Warn("action log: queue full or closed, rows dropped", "profile", e.Profile, "action", e.Action)
		}
		return
	}
	r.overflowing.Store(false)
}

func (r *Recorder) tryQueue(e store.ActionEntry) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return false
	}
	select {
	case r.queue <- e:
		return true
	default:
		return false
	}
}

// Close stops accepting rows and waits for the queued ones to be written. It
// returns ctx.Err() when the deadline passes first; the writer then counts the
// rows it skips as dropped. It is safe to call more than once.
func (r *Recorder) Close(ctx context.Context) error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.stop)
	}
	r.mu.Unlock()

	select {
	case <-r.done:
		return nil
	default:
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		r.abandoned.Store(true)
		if n := len(r.queue); n > 0 {
			r.log.Warn("action log: close deadline passed, rows will be dropped", "rows", n)
		}
		return ctx.Err()
	}
}

func (r *Recorder) run() {
	defer close(r.done)
	for {
		select {
		case e := <-r.queue:
			r.handle(e)
		case <-r.stop:
			for {
				select {
				case e := <-r.queue:
					r.handle(e)
				default:
					return
				}
			}
		}
	}
}

// handle writes e, or counts it as dropped once Close gave up waiting.
func (r *Recorder) handle(e store.ActionEntry) {
	if r.abandoned.Load() {
		r.dropped.Add(1)
		return
	}
	r.write(e)
}

func (r *Recorder) write(e store.ActionEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), r.writeTimeout)
	defer cancel()
	err := r.sink.AppendAction(ctx, e)
	switch {
	case err != nil && (r.abandoned.Load() || errors.Is(err, store.ErrClosed)):
		// shutting down: stay quiet
	case err != nil && !r.failing:
		r.failing = true
		r.log.Error("action log: write failed", "profile", e.Profile, "action", e.Action, "err", err)
	case err != nil:
		// still failing: already reported for this burst
	case r.failing:
		r.failing = false
		r.log.Info("action log: writes recovered")
	}
}
