package operations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// DefaultRetainFinished is how many finished jobs stay readable in memory.
const DefaultRetainFinished = 50

// Options configure a Manager. Zero values take the defaults.
type Options struct {
	// Launcher starts processes. Required.
	Launcher Launcher
	// Observers are told about output and completion, in order.
	Observers []Observer
	// MaxLines and MaxBytes bound the stored output per job.
	MaxLines int
	MaxBytes int
	// SubscriberBuffer is each subscriber's live channel capacity.
	SubscriberBuffer int
	// RetainFinished is how many finished jobs stay in memory, oldest evicted
	// first.
	RetainFinished int
	// NewID generates job IDs ("j_" plus 6 hex characters by default). It is
	// called under the manager mutex, so it must be quick.
	NewID func() string
	// Now is the clock; defaults to time.Now().UTC().
	Now func() time.Time
}

// slot is a profile's reservation: a job (job set) or an exclusive hold.
type slot struct{ job *job }

// Manager runs jobs, one slot per profile.
type Manager struct {
	opts       Options
	base       context.Context
	baseCancel context.CancelFunc
	wg         sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	slots    map[string]*slot
	jobs     map[string]*job // launched jobs, running or retained
	finished []string        // finished job IDs, oldest first
}

// New builds a Manager. It panics if opts.Launcher is nil.
func New(opts Options) *Manager {
	if opts.Launcher == nil {
		panic("operations: Options.Launcher is required")
	}
	if opts.MaxLines <= 0 {
		opts.MaxLines = DefaultMaxLines
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	if opts.SubscriberBuffer <= 0 {
		opts.SubscriberBuffer = DefaultSubscriberBuffer
	}
	if opts.RetainFinished <= 0 {
		opts.RetainFinished = DefaultRetainFinished
	}
	if opts.NewID == nil {
		opts.NewID = randomID
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	// Never derived from a request context: only Cancel and Shutdown stop jobs.
	base, cancel := context.WithCancel(context.Background())
	return &Manager{
		opts:       opts,
		base:       base,
		baseCancel: cancel,
		slots:      make(map[string]*slot),
		jobs:       make(map[string]*job),
	}
}

func randomID() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("operations: crypto/rand failed: %v", err))
	}
	return "j_" + hex.EncodeToString(b[:])
}

// idInUseLocked reports whether id belongs to a known or launching job.
func (m *Manager) idInUseLocked(id string) bool {
	if _, ok := m.jobs[id]; ok {
		return true
	}
	for _, s := range m.slots {
		if s.job != nil && s.job.id == id {
			return true
		}
	}
	return false
}

// Submit starts a job for the request's profile. It fails at once with
// *BusyError if the profile's slot is taken, ErrClosed after Shutdown, or the
// Launcher's error (no job is created then). The job runs under the manager's
// own context, never the caller's.
func (m *Manager) Submit(r Request) (JobSummary, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return JobSummary{}, ErrClosed
	}
	key := slotKey(r.Profile)
	if s, ok := m.slots[key]; ok {
		busy := &BusyError{Profile: r.Profile}
		if s.job != nil {
			busy.JobID = s.job.id
		}
		m.mu.Unlock()
		return JobSummary{}, busy
	}
	id := ""
	for i := 0; i < 100; i++ {
		if c := m.opts.NewID(); !m.idInUseLocked(c) {
			id = c
			break
		}
	}
	if id == "" {
		m.mu.Unlock()
		return JobSummary{}, ErrIDExhausted
	}
	ctx, cancel := context.WithCancel(m.base)
	j := &job{
		id:        id,
		profile:   r.Profile,
		action:    r.Action,
		cancel:    cancel,
		recorded:  make(chan struct{}),
		state:     StateRunning,
		startedAt: m.opts.Now(),
		ring:      newOutputRing(m.opts.MaxLines, m.opts.MaxBytes),
	}
	m.slots[key] = &slot{job: j}
	m.wg.Add(1)
	m.mu.Unlock()

	proc, err := m.launch(ctx, r)
	if err != nil {
		m.mu.Lock()
		delete(m.slots, key)
		m.mu.Unlock()
		cancel()
		m.wg.Done()
		return JobSummary{}, err
	}
	m.mu.Lock()
	m.jobs[id] = j
	m.mu.Unlock()
	j.setProc(proc)
	go m.pump(j, proc)
	return j.summary(), nil
}

// launch calls the Launcher, turning a panic or a nil process into an error so
// the reserved slot is always released.
func (m *Manager) launch(ctx context.Context, r Request) (p colima.Process, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			p, err = nil, fmt.Errorf("operations: launcher panicked: %v", rec)
		}
	}()
	p, err = m.opts.Launcher.Launch(ctx, r)
	if err == nil && p == nil {
		err = fmt.Errorf("operations: launcher returned no process")
	}
	return p, err
}

// pump is the job's only goroutine. It always drains Lines, doing short
// non-blocking work per line, then records the end state, releases the slot
// and finally tells observers.
func (m *Manager) pump(j *job, proc colima.Process) {
	defer m.wg.Done()
	defer j.cancel()
	for text := range proc.Lines() {
		m.notifyOutput(j.appendLine(text))
	}
	exit, err := proc.Wait()
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	state, code := classify(exit, err, j.cancelReq.Load() || closed)
	j.finish(state, code, exit.Tail, m.opts.Now())

	m.mu.Lock()
	if key := slotKey(j.profile); m.slots[key] != nil && m.slots[key].job == j {
		delete(m.slots, key)
	}
	m.finished = append(m.finished, j.id)
	for len(m.finished) > m.opts.RetainFinished {
		delete(m.jobs, m.finished[0])
		m.finished = m.finished[1:]
	}
	m.mu.Unlock()

	m.notifyFinished(j.summary())
}

// Get returns a snapshot of a running or retained job.
func (m *Manager) Get(id string) (JobSummary, error) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return JobSummary{}, ErrJobNotFound
	}
	return j.summary(), nil
}

// Cancel stops a running job and returns its final snapshot once the state is
// recorded (not once observers have run). A cancel that raced a clean exit can
// return a succeeded job with a nil error. A finished job gives
// *NotRunningError. If ctx ends first, Cancel returns ctx.Err(); the job is
// still being cancelled.
func (m *Manager) Cancel(ctx context.Context, id string) (JobSummary, error) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return JobSummary{}, ErrJobNotFound
	}
	j.mu.Lock()
	state := j.state
	j.mu.Unlock()
	if state != StateRunning {
		return JobSummary{}, &NotRunningError{JobID: id, State: state}
	}
	j.requestCancel()
	select {
	case <-j.recorded:
		return j.summary(), nil
	case <-ctx.Done():
		return JobSummary{}, ctx.Err()
	}
}

// Busy reports the running job on a profile, for activeJobId. It is false
// while the slot is an exclusive hold (there is no job then).
func (m *Manager) Busy(profile string) (jobID string, running bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.slots[slotKey(profile)]; ok && s.job != nil {
		return s.job.id, true
	}
	return "", false
}

// TryExclusive runs fn while holding the profile's slot, so Submit on that
// profile fails with a *BusyError with an empty JobID for the duration. If the
// slot is taken it returns false, nil without running fn. No manager mutex is
// held while fn runs, so fn may call back into the manager (a nested Submit or
// TryExclusive on the same profile just sees busy). The slot is released even
// if fn panics. It returns ErrClosed after Shutdown.
func (m *Manager) TryExclusive(profile string, fn func() error) (held bool, err error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false, ErrClosed
	}
	key := slotKey(profile)
	if _, ok := m.slots[key]; ok {
		m.mu.Unlock()
		return false, nil
	}
	s := &slot{}
	m.slots[key] = s
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if m.slots[key] == s {
			delete(m.slots, key)
		}
		m.mu.Unlock()
		m.wg.Done()
	}()
	return true, fn()
}

// Shutdown refuses new work, cancels every running job (they end cancelled and
// observers still hear about them) and waits for the pumps and any exclusive
// holders, returning ctx.Err() if that outlives ctx.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	var running []*job
	for _, s := range m.slots {
		if s.job != nil {
			running = append(running, s.job)
		}
	}
	m.mu.Unlock()
	for _, j := range running {
		j.requestCancel()
	}
	m.baseCancel()
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// slotKey folds a profile name for the slot table. Colima's folders sit on a
// case-insensitive filesystem by default, so Dev and dev are one profile and
// must never hold two slots. Jobs keep the spelling that was requested.
func slotKey(profile string) string { return strings.ToLower(profile) }
