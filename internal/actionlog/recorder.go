package actionlog

import (
	"context"
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

	abandoned atomic.Bool
	dropped   atomic.Uint64
	failing   bool // writer goroutine only
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
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		r.dropped.Add(1)
		return
	}
	select {
	case r.queue <- e:
	default:
		r.dropped.Add(1)
		r.log.Warn("action log: queue full, row dropped", "profile", e.Profile, "action", e.Action)
	}
}

// Close stops accepting rows and waits for the queued ones to be written. It
// returns ctx.Err() when the deadline passes first; rows still queued then are
// counted as dropped. It is safe to call more than once.
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
	case <-ctx.Done():
		r.abandoned.Store(true)
		if n := len(r.queue); n > 0 {
			r.dropped.Add(uint64(n))
			r.log.Warn("action log: close deadline passed, rows dropped", "rows", n)
		}
		return ctx.Err()
	}
}

func (r *Recorder) run() {
	defer close(r.done)
	for {
		select {
		case e := <-r.queue:
			if r.abandoned.Load() {
				continue // already counted by Close
			}
			r.write(e)
		case <-r.stop:
			for {
				select {
				case e := <-r.queue:
					if r.abandoned.Load() {
						continue // already counted by Close
					}
					r.write(e)
				default:
					return
				}
			}
		}
	}
}

func (r *Recorder) write(e store.ActionEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), r.writeTimeout)
	defer cancel()
	err := r.sink.AppendAction(ctx, e)
	switch {
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
