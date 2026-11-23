package sampler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/events"
	"github.com/dlusignan/colima-ui/internal/store"
)

const (
	// DefaultInterval is the fixed sampling interval (C4 decision). Only tests
	// override it.
	DefaultInterval = 5 * time.Second
	// defaultListEvery re-reads the profile list every 6th tick (30 s).
	defaultListEvery = 6
)

// Options configures a Sampler. All fields are optional.
type Options struct {
	Interval  time.Duration // default 5 s; set by tests only
	ListEvery int           // ticks between profile list refreshes, default 6
	Clock     Clock         // default RealClock
	Logger    *slog.Logger  // default slog.Default()
}

// Sampler is the Metrics Sampler.
type Sampler struct {
	lister    Lister
	sources   []Source
	sink      Sink
	pub       Publisher
	clock     Clock
	interval  time.Duration
	listEvery int
	logs      *logOnce
	log       *slog.Logger

	startOnce sync.Once
	cancel    context.CancelFunc
	cancelMu  sync.Mutex
	wg        sync.WaitGroup
	refresh   chan struct{}
	relist    atomic.Bool
	active    atomic.Int32 // running workers
	skipped   atomic.Int64 // ticks skipped because a worker was still busy

	// Loop goroutine only.
	sampling []Target
	known    map[string]string
	profs    map[string]*profState

	tickHook func(n int) // tests: called after each tick has launched its workers
}

type profState struct{ busy atomic.Bool }

// New builds a Sampler. Sources that have a Close method are closed by Stop.
func New(lister Lister, sources []Source, sink Sink, pub Publisher, opts Options) *Sampler {
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.ListEvery <= 0 {
		opts.ListEvery = defaultListEvery
	}
	if opts.Clock == nil {
		opts.Clock = RealClock{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Sampler{
		lister: lister, sources: sources, sink: sink, pub: pub,
		clock: opts.Clock, interval: opts.Interval, listEvery: opts.ListEvery,
		logs: newLogOnce(opts.Logger), log: opts.Logger,
		refresh: make(chan struct{}, 1),
		known:   map[string]string{}, profs: map[string]*profState{},
	}
}

// Start launches the loop. The first tick runs immediately. A second call, or
// a call after Stop, does nothing.
func (s *Sampler) Start(ctx context.Context) {
	s.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(ctx)
		s.cancelMu.Lock()
		s.cancel = cancel
		s.cancelMu.Unlock()
		s.wg.Add(1)
		go s.run(ctx)
	})
}

// Stop cancels the loop and every worker, waits for them, then closes the
// sources that can be closed. It is safe to call more than once and before
// Start. An in-flight tick is dropped without writing.
func (s *Sampler) Stop() {
	// Burn the Once so a later Start is a no-op.
	s.startOnce.Do(func() {})
	s.cancelMu.Lock()
	cancel := s.cancel
	s.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	for _, src := range s.sources {
		if c, ok := src.(interface{ Close() }); ok {
			c.Close()
		}
	}
}

// Refresh asks for a profile list refresh soon, for example after a job
// finished. It never blocks.
func (s *Sampler) Refresh() {
	select {
	case s.refresh <- struct{}{}:
	default:
	}
}

func (s *Sampler) run(ctx context.Context) {
	defer s.wg.Done()
	tk := s.clock.NewTicker(s.interval)
	defer tk.Stop()
	n := 0
	s.tick(ctx, n)
	n++
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C():
			s.tick(ctx, n)
			n++
		case <-s.refresh:
			s.onRefresh(ctx)
		}
	}
}

func (s *Sampler) tick(ctx context.Context, n int) {
	if ctx.Err() != nil {
		return
	}
	force := s.relist.Swap(false)
	if force || n%s.listEvery == 0 {
		s.syncTargets(ctx)
	}
	at := s.clock.Now().Truncate(time.Second)
	for _, t := range s.sampling {
		s.launch(ctx, t, at)
	}
	if s.tickHook != nil {
		s.tickHook(n)
	}
}

// onRefresh re-reads the list and samples only profiles that were not being
// sampled, so the regular cadence is not disturbed.
func (s *Sampler) onRefresh(ctx context.Context) {
	was := map[string]bool{}
	for _, t := range s.sampling {
		was[t.Name] = true
	}
	s.syncTargets(ctx)
	at := s.clock.Now().Truncate(time.Second)
	for _, t := range s.sampling {
		if !was[t.Name] {
			s.launch(ctx, t, at)
		}
	}
}

func (s *Sampler) syncTargets(ctx context.Context) {
	targets, err := s.lister.Targets(ctx)
	k := logKey{"", "list"}
	if err != nil {
		if ctx.Err() == nil {
			s.logs.set(k, true, "listing profiles failed, keeping the previous set", err)
		}
		return
	}
	s.logs.set(k, false, "listing profiles", nil)

	seen := make(map[string]bool, len(targets))
	var sampling []Target
	for _, t := range targets {
		seen[t.Name] = true
		state := "stopped"
		switch {
		case t.sampled():
			state = "sampling"
			sampling = append(sampling, t)
		case t.running():
			state = "other-runtime"
		}
		prev, had := s.known[t.Name]
		s.known[t.Name] = state
		if state == prev {
			continue
		}
		switch {
		case state == "other-runtime":
			s.log.Info("profile skipped, runtime is not docker", "profile", t.Name, "runtime", t.Runtime)
		case state == "stopped" && had && prev == "sampling":
			s.log.Info("profile stopped, sampling paused", "profile", t.Name)
		}
		if prev == "sampling" {
			s.forgetProfile(t.Name)
		}
	}
	for name, state := range s.known {
		if !seen[name] {
			delete(s.known, name)
			if state == "sampling" {
				s.forgetProfile(name)
			}
		}
	}
	s.sampling = sampling
}

func (s *Sampler) forgetProfile(name string) {
	for _, src := range s.sources {
		src.Forget(name)
	}
	s.logs.forget(name)
	delete(s.profs, name)
}

func (s *Sampler) launch(ctx context.Context, t Target, at time.Time) {
	ps := s.profs[t.Name]
	if ps == nil {
		ps = &profState{}
		s.profs[t.Name] = ps
	}
	if !ps.busy.CompareAndSwap(false, true) {
		s.skipped.Add(1)
		return
	}
	s.wg.Add(1)
	s.active.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.active.Add(-1)
		defer ps.busy.Store(false)
		s.sample(ctx, t, at)
	}()
}

type sourceResult struct {
	p   Partial
	err error
}

// sample runs every source for one profile concurrently, merges what came
// back, writes one batch and publishes.
func (s *Sampler) sample(ctx context.Context, t Target, at time.Time) {
	results := make([]sourceResult, len(s.sources))
	var wg sync.WaitGroup
	for i, src := range s.sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, src.Timeout())
			defer cancel()
			p, err := src.Collect(cctx, t)
			results[i] = sourceResult{p, err}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}

	var m Partial
	for i, r := range results {
		src := s.sources[i]
		s.noteSource(t.Name, src.Name(), r.err)
		if r.p.VMCPU != nil && m.VMCPU == nil {
			m.VMCPU = r.p.VMCPU
		}
		if r.p.VMMemory != nil && m.VMMemory == nil {
			m.VMMemory = r.p.VMMemory
		}
		if r.p.VMDisk != nil && m.VMDisk == nil {
			m.VMDisk = r.p.VMDisk
		}
		m.Containers = append(m.Containers, r.p.Containers...)
		m.Truncated = m.Truncated || r.p.Truncated
	}
	s.logs.set(logKey{t.Name, "container-cap"}, m.Truncated, "too many containers, recording the busiest", nil)

	var rows []store.Sample
	var evs []events.Event
	if m.VMCPU != nil || m.VMMemory != nil || m.VMDisk != nil {
		rows = append(rows, store.Sample{Profile: t.Name, Scope: VMScope, TakenAt: at, CPUPercent: m.VMCPU, MemoryBytes: m.VMMemory, DiskBytes: m.VMDisk})
	}
	if m.VMCPU != nil && m.VMMemory != nil {
		evs = append(evs, events.Metric{Profile: t.Name, Scope: VMScope, TakenAt: at, CPUPercent: *m.VMCPU, MemoryBytes: *m.VMMemory, DiskBytes: m.VMDisk})
	}
	for _, c := range m.Containers {
		rows = append(rows, store.Sample{Profile: t.Name, Scope: c.ID, TakenAt: at, CPUPercent: ptr(c.CPUPercent), MemoryBytes: ptr(c.MemoryBytes)})
		evs = append(evs, events.Metric{Profile: t.Name, Scope: c.ID, TakenAt: at, CPUPercent: c.CPUPercent, MemoryBytes: c.MemoryBytes})
	}
	if len(rows) == 0 {
		return
	}
	err := s.sink.AppendSamples(ctx, rows)
	if ctx.Err() != nil {
		return
	}
	s.logs.set(logKey{t.Name, "store"}, err != nil, "writing history failed", err)
	// Publish even when the write failed: a full disk should not blind the
	// live view.
	for _, e := range evs {
		s.pub.Publish(e)
	}
}

// noteSource tracks the outage state of one source. A profile that turned out
// to be stopped or not docker is not an outage, it only asks for a re-list.
func (s *Sampler) noteSource(profile, source string, err error) {
	k := logKey{profile, source}
	switch {
	case err == nil:
		s.logs.set(k, false, source+" source", nil)
	case errors.Is(err, context.Canceled):
		// shutting down
	case errors.Is(err, dockerc.ErrVMNotRunning), errors.Is(err, dockerc.ErrRuntimeNotDocker):
		s.relist.Store(true)
	default:
		if errors.Is(err, dockerc.ErrDockerUnavailable) {
			s.relist.Store(true)
		}
		s.logs.set(k, true, source+" source failed", err)
	}
}
