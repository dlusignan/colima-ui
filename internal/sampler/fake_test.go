package sampler

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/events"
	"github.com/dlusignan/colima-ui/internal/store"
)

// ---- clock ----

type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
	step    time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 2, 3, 4, 5, 700, time.UTC), step: 5 * time.Second}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTicker(time.Duration) Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTicker{ch: make(chan time.Time, 1)}
	c.tickers = append(c.tickers, t)
	return t
}

// Tick advances time by one interval and fires every ticker.
func (c *fakeClock) Tick() {
	c.mu.Lock()
	c.now = c.now.Add(c.step)
	now := c.now
	ts := append([]*fakeTicker(nil), c.tickers...)
	c.mu.Unlock()
	for _, t := range ts {
		t.fire(now)
	}
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func (c *fakeClock) liveTickers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.tickers {
		if !t.isStopped() {
			n++
		}
	}
	return n
}

type fakeTicker struct {
	ch      chan time.Time
	mu      sync.Mutex
	stopped bool
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               { t.mu.Lock(); t.stopped = true; t.mu.Unlock() }
func (t *fakeTicker) isStopped() bool     { t.mu.Lock(); defer t.mu.Unlock(); return t.stopped }
func (t *fakeTicker) fire(now time.Time) {
	select {
	case t.ch <- now:
	default:
	}
}

// ---- sink, publisher, lister ----

type fakeSink struct {
	mu      sync.Mutex
	batches [][]store.Sample
	err     error
	block   func(ctx context.Context) // optional, called before recording
}

func (s *fakeSink) AppendSamples(ctx context.Context, rows []store.Sample) error {
	if s.block != nil {
		s.block(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.batches = append(s.batches, append([]store.Sample(nil), rows...))
	return nil
}

func (s *fakeSink) all() []store.Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.Sample
	for _, b := range s.batches {
		out = append(out, b...)
	}
	return out
}

func (s *fakeSink) setErr(err error) { s.mu.Lock(); s.err = err; s.mu.Unlock() }

func (s *fakeSink) scope(profile, scope string) []store.Sample {
	var out []store.Sample
	for _, r := range s.all() {
		if r.Profile == profile && r.Scope == scope {
			out = append(out, r)
		}
	}
	return out
}

type fakePub struct {
	mu  sync.Mutex
	evs []events.Event
}

func (p *fakePub) Publish(e events.Event) { p.mu.Lock(); p.evs = append(p.evs, e); p.mu.Unlock() }

func (p *fakePub) metrics() []events.Metric {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []events.Metric
	for _, e := range p.evs {
		out = append(out, e.(events.Metric))
	}
	return out
}

type fakeLister struct {
	mu      sync.Mutex
	targets []Target
	err     error
	calls   int
	hang    bool // Targets waits for ctx to end
}

func (l *fakeLister) Targets(ctx context.Context) ([]Target, error) {
	l.mu.Lock()
	l.calls++
	hang := l.hang
	l.mu.Unlock()
	if hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	return append([]Target(nil), l.targets...), nil
}

func (l *fakeLister) set(ts ...Target) { l.mu.Lock(); l.targets = ts; l.mu.Unlock() }
func (l *fakeLister) setErr(err error) { l.mu.Lock(); l.err = err; l.mu.Unlock() }
func (l *fakeLister) count() int       { l.mu.Lock(); defer l.mu.Unlock(); return l.calls }

func running(name string) Target {
	return Target{Name: name, Status: "Running", Runtime: "docker", CPUs: 4, Memory: 8 << 30, Disk: 100 << 30}
}

// ---- log capture ----

type logRec struct {
	level slog.Level
	msg   string
}

type logCapture struct {
	mu   sync.Mutex
	recs []logRec
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *logCapture) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool { fmt.Fprintf(&b, " %s=%v", a.Key, a.Value); return true })
	c.mu.Lock()
	c.recs = append(c.recs, logRec{r.Level, b.String()})
	c.mu.Unlock()
	return nil
}
func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *logCapture) WithGroup(string) slog.Handler      { return c }

func (c *logCapture) count(min slog.Level, contains string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, r := range c.recs {
		if r.level >= min && strings.Contains(r.msg, contains) {
			n++
		}
	}
	return n
}

func (c *logCapture) warns() int { return c.count(slog.LevelWarn, "") }

// ---- docker fakes ----

type fakeDocker struct {
	mu         sync.Mutex
	containers []dockerc.Container
	readings   map[string]dockerc.Reading
	listErr    error
	statsErr   error
	disk       dockerc.DiskUsage
	diskErr    error
	diskBlock  chan struct{} // when non-nil DiskUsage waits for it or ctx
	diskCalls  int
	listHook   func() // optional, called by Containers before it answers
}

func (d *fakeDocker) Containers(context.Context) ([]dockerc.Container, error) {
	d.mu.Lock()
	hook := d.listHook
	d.mu.Unlock()
	if hook != nil {
		hook()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.listErr != nil {
		return nil, d.listErr
	}
	return append([]dockerc.Container(nil), d.containers...), nil
}

func (d *fakeDocker) Stats(_ context.Context, id string) (dockerc.Reading, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.statsErr != nil {
		return dockerc.Reading{}, d.statsErr
	}
	r, ok := d.readings[id]
	if !ok {
		return dockerc.Reading{}, dockerc.ErrContainerNotFound
	}
	return r, nil
}

func (d *fakeDocker) DiskUsage(ctx context.Context) (dockerc.DiskUsage, error) {
	d.mu.Lock()
	d.diskCalls++
	block, err, du := d.diskBlock, d.diskErr, d.disk
	d.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return dockerc.DiskUsage{}, ctx.Err()
		}
	}
	return du, err
}

func (d *fakeDocker) Info(context.Context) (dockerc.Engine, error) { return dockerc.Engine{}, nil }

func (d *fakeDocker) Images(context.Context) ([]dockerc.Image, error) { return nil, nil }

func (d *fakeDocker) Volumes(context.Context) ([]dockerc.Volume, error) { return nil, nil }

func (d *fakeDocker) Networks(context.Context) ([]dockerc.Network, error) { return nil, nil }

func (d *fakeDocker) set(f func(d *fakeDocker)) { d.mu.Lock(); f(d); d.mu.Unlock() }
func (d *fakeDocker) diskCallCount() int        { d.mu.Lock(); defer d.mu.Unlock(); return d.diskCalls }

// advance moves every container's counters forward so CPUPercent is
// cpuDelta/sysDelta*cpus*100 = pct for each running container.
func (d *fakeDocker) advance(pct float64, mem uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.containers {
		r := d.readings[c.ID]
		r.SystemNanos += 1000
		r.CPUTotalNanos += uint64(pct * 10) // pct/100*1000 per cpu, 1 cpu
		r.OnlineCPUs = 1
		r.MemoryBytes = mem
		d.readings[c.ID] = r
	}
}

func newFakeDocker(ids ...string) *fakeDocker {
	d := &fakeDocker{readings: map[string]dockerc.Reading{}}
	for _, id := range ids {
		d.containers = append(d.containers, dockerc.Container{ID: id, Name: id, State: "running"})
		d.readings[id] = dockerc.Reading{SystemNanos: 1000, CPUTotalNanos: 100, OnlineCPUs: 1, MemoryBytes: 10}
	}
	return d
}

type fakeProvider struct {
	mu    sync.Mutex
	by    map[string]dockerc.Client
	err   error
	calls map[string]int
	block chan struct{} // when non-nil For waits for it or ctx
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{by: map[string]dockerc.Client{}, calls: map[string]int{}}
}

func (p *fakeProvider) For(ctx context.Context, profile string) (dockerc.Client, error) {
	p.mu.Lock()
	p.calls[profile]++
	block := p.block
	p.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	c, ok := p.by[profile]
	if !ok {
		return nil, dockerc.ErrVMNotRunning
	}
	return c, nil
}

func (p *fakeProvider) callCount(profile string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[profile]
}

func (p *fakeProvider) setErr(err error) { p.mu.Lock(); p.err = err; p.mu.Unlock() }

// ---- generic fake source ----

type fakeSource struct {
	name    string
	timeout time.Duration
	collect func(ctx context.Context, t Target) (Partial, error)
	mu      sync.Mutex
	forgot  []string
}

func (s *fakeSource) Name() string { return s.name }
func (s *fakeSource) Timeout() time.Duration {
	if s.timeout == 0 {
		return time.Second
	}
	return s.timeout
}
func (s *fakeSource) Collect(ctx context.Context, t Target) (Partial, error) {
	return s.collect(ctx, t)
}
func (s *fakeSource) Forget(p string) { s.mu.Lock(); s.forgot = append(s.forgot, p); s.mu.Unlock() }
func (s *fakeSource) forgotten() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.forgot...)
}

// ---- harness ----

type harness struct {
	t     *testing.T
	clock *fakeClock
	lst   *fakeLister
	sink  *fakeSink
	pub   *fakePub
	logs  *logCapture
	s     *Sampler
	ticks chan int
	ctx   context.Context
	stop  context.CancelFunc
}

func newHarness(t *testing.T, sources ...Source) *harness {
	t.Helper()
	h := &harness{t: t, clock: newFakeClock(), lst: &fakeLister{}, sink: &fakeSink{}, pub: &fakePub{}, logs: &logCapture{}, ticks: make(chan int, 1024)}
	h.s = New(h.lst, sources, h.sink, h.pub, Options{Clock: h.clock, Logger: slog.New(h.logs)})
	h.s.tickHook = func(n int) { h.ticks <- n }
	h.ctx, h.stop = context.WithCancel(context.Background())
	t.Cleanup(func() { h.s.Stop(); h.stop() })
	return h
}

// start launches the sampler and waits for the immediate first tick.
func (h *harness) start() {
	h.s.Start(h.ctx)
	h.waitTick()
}

func (h *harness) waitTick() {
	h.t.Helper()
	select {
	case <-h.ticks:
	case <-time.After(5 * time.Second):
		h.t.Fatal("tick did not run")
	}
	h.settle()
}

// tick fires the ticker once and waits until its workers are done.
func (h *harness) tick() {
	h.t.Helper()
	h.clock.Tick()
	h.waitTick()
}

func (h *harness) settle() {
	h.t.Helper()
	eventually(h.t, func() bool { return h.s.active.Load() == 0 })
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

// checkLeaks fails the test if goroutines started during it are still alive.
func checkLeaks(t *testing.T) {
	t.Helper()
	before := runtime.NumGoroutine()
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for runtime.NumGoroutine() > before {
			if time.Now().After(deadline) {
				buf := make([]byte, 1<<16)
				buf = buf[:runtime.Stack(buf, true)]
				t.Fatalf("goroutine leak: %d before, %d after\n%s", before, runtime.NumGoroutine(), buf)
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
}
