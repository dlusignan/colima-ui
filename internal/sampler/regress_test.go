package sampler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

// Review finding 1: stop then start of a profile while its worker is still
// running must not create a second worker, and the old worker must not write.
func TestForgetWhileWorkerBusyKeepsSingleFlightAndDropsOldResult(t *testing.T) {
	release := make(chan struct{})
	var cur, peak, calls atomic.Int32
	src := &fakeSource{name: "x", timeout: 10 * time.Second, collect: func(ctx context.Context, _ Target) (Partial, error) {
		n := cur.Add(1)
		defer cur.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		if calls.Add(1) == 1 {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return Partial{VMCPU: ptr(1.0), VMMemory: ptr(int64(1))}, nil
	}}
	h := newHarness(t, src)
	h.s.listEvery = 1
	h.lst.set(running("synthetic-a"))
	h.s.Start(h.ctx)
	<-h.ticks
	eventually(t, func() bool { return calls.Load() == 1 })
	stopped := running("synthetic-a")
	stopped.Status = "Stopped"
	h.lst.set(stopped)
	h.clock.Tick()
	<-h.ticks
	h.lst.set(running("synthetic-a"))
	h.clock.Tick()
	<-h.ticks
	if got := calls.Load(); got != 1 {
		t.Fatalf("collect calls = %d, want 1: a second worker started for the same profile", got)
	}
	close(release)
	h.settle()
	if len(h.sink.all()) != 0 || len(h.pub.metrics()) != 0 {
		t.Fatal("a worker that began before Forget wrote or published")
	}
	h.clock.Tick()
	<-h.ticks
	h.settle()
	if peak.Load() != 1 || len(h.sink.all()) != 1 {
		t.Fatalf("peak concurrency %d, rows %d", peak.Load(), len(h.sink.all()))
	}
}

// Review finding 2: a Collect that began before Forget must not write its
// baseline back.
func TestContainerSourceForgetDuringCollectDropsBaseline(t *testing.T) {
	d := newFakeDocker("c1")
	prov := newFakeProvider()
	prov.by["synthetic-a"] = d
	src := NewContainerSource(NewClientCache(prov, newFakeClock()))
	defer src.Close()
	tg := running("synthetic-a")
	if _, err := src.Collect(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	d.set(func(d *fakeDocker) {
		d.listHook = func() {
			select {
			case entered <- struct{}{}:
				<-release
			default:
			}
		}
	})
	done := make(chan Partial)
	go func() { p, _ := src.Collect(context.Background(), tg); done <- p }()
	<-entered
	src.Forget("synthetic-a")
	close(release)
	<-done
	d.set(func(d *fakeDocker) { d.listHook = nil })
	p, err := src.Collect(context.Background(), tg)
	if err != nil {
		t.Fatal(err)
	}
	if p.VMCPU != nil {
		t.Fatalf("pre-Forget baseline resurrected, vm cpu = %v", *p.VMCPU)
	}
}

// Review finding 3: Get honours the caller's context and does not serialize
// other callers behind a slow lookup.
func TestClientCacheGetHonoursContext(t *testing.T) {
	prov := newFakeProvider()
	prov.by["synthetic-a"] = newFakeDocker()
	prov.block = make(chan struct{})
	c := NewClientCache(prov, newFakeClock())
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Get(ctx, "synthetic-a")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	// A second caller joins the same build; releasing it serves both.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Get(context.Background(), "synthetic-a"); err != nil {
				t.Error(err)
			}
		}()
	}
	eventually(t, func() bool { return prov.callCount("synthetic-a") >= 1 })
	close(prov.block)
	wg.Wait()
	if prov.callCount("synthetic-a") != 1 {
		t.Fatalf("builds = %d, want 1", prov.callCount("synthetic-a"))
	}
}

func TestStopDoesNotWaitForSlowClientLookup(t *testing.T) {
	checkLeaks(t)
	d := newFakeDocker("c1")
	prov := newFakeProvider()
	prov.by["synthetic-a"] = d
	prov.block = make(chan struct{}) // never released
	clock := newFakeClock()
	cache := NewClientCache(prov, clock)
	h := newHarness(t, NewContainerSource(cache), NewDiskSource(cache, clock))
	h.lst.set(running("synthetic-a"))
	h.s.Start(h.ctx)
	<-h.ticks
	eventually(t, func() bool { return prov.callCount("synthetic-a") == 1 })
	done := make(chan struct{})
	go func() { h.s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop waited for the colima lookup")
	}
}

// Review finding 4: a hung profile list must not stop sampling.
func TestHungListerDoesNotStallTicks(t *testing.T) {
	src := &fakeSource{name: "x", collect: func(context.Context, Target) (Partial, error) {
		return Partial{VMCPU: ptr(1.0), VMMemory: ptr(int64(1))}, nil
	}}
	h := newHarness(t, src)
	h.s.listEvery = 1
	h.s.listTO = 20 * time.Millisecond
	h.lst.set(running("synthetic-a"))
	h.start()
	h.lst.mu.Lock()
	h.lst.hang = true
	h.lst.mu.Unlock()
	h.tick()
	h.tick()
	if len(h.sink.scope("synthetic-a", "vm")) != 3 {
		t.Fatalf("rows = %d, sampling must continue on the previous set", len(h.sink.scope("synthetic-a", "vm")))
	}
	if got := h.logs.count(slog.LevelWarn, "listing profiles failed"); got != 1 {
		t.Fatalf("list warnings = %d", got)
	}
}

// Review finding 5: a docker outage re-lists once, not every tick.
func TestOutageRelistsOnlyAtStart(t *testing.T) {
	h, d, _, _ := dockerHarness(t, "c1")
	h.s.listEvery = 1000
	h.start()
	d.set(func(d *fakeDocker) { d.listErr = dockerc.ErrDockerUnavailable })
	for i := 0; i < 10; i++ {
		h.tick()
	}
	if got := h.lst.count(); got > 2 {
		t.Fatalf("list calls = %d during an outage, want at most 2 (start + outage start)", got)
	}
}

// Review finding 6: a timeout does not reset the baseline, a gone engine does.
func TestBaselineResetOnlyOnEngineErrors(t *testing.T) {
	d := newFakeDocker("c1")
	prov := newFakeProvider()
	prov.by["synthetic-a"] = d
	src := NewContainerSource(NewClientCache(prov, newFakeClock()))
	defer src.Close()
	tg := running("synthetic-a")
	ctx := context.Background()
	if p, _ := src.Collect(ctx, tg); p.VMCPU != nil {
		t.Fatal("first collect has no baseline")
	}
	d.set(func(d *fakeDocker) { d.listErr = context.DeadlineExceeded })
	if _, err := src.Collect(ctx, tg); err == nil {
		t.Fatal("want timeout error")
	}
	d.set(func(d *fakeDocker) { d.listErr = nil })
	if p, err := src.Collect(ctx, tg); err != nil || p.VMCPU == nil {
		t.Fatalf("a timeout reset the baseline: %+v %v", p, err)
	}
	d.set(func(d *fakeDocker) { d.listErr = dockerc.ErrDockerUnavailable })
	_, _ = src.Collect(ctx, tg)
	d.set(func(d *fakeDocker) { d.listErr = nil })
	if p, _ := src.Collect(ctx, tg); p.VMCPU != nil {
		t.Fatal("an unavailable engine must reset the baseline")
	}
}

// Review finding 7: a refresh that outlives Forget leaves no cache entry and a
// stopped VM is not negative cached.
func TestDiskRefreshAfterForgetLeavesNothingBehind(t *testing.T) {
	prov := newFakeProvider()
	prov.block = make(chan struct{})
	clock := newFakeClock()
	cache := NewClientCache(prov, clock)
	disk := NewDiskSource(cache, clock)
	defer disk.Close()
	tg := running("synthetic-a")
	if _, err := disk.Collect(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return prov.callCount("synthetic-a") == 1 })
	disk.Forget("synthetic-a")
	cache.Forget("synthetic-a")
	close(prov.block) // For answers ErrVMNotRunning (no client registered)
	disk.wait()
	cache.mu.Lock()
	n := len(cache.entries)
	cache.mu.Unlock()
	if n != 0 {
		t.Fatalf("cache entries = %d after Forget", n)
	}
	disk.mu.Lock()
	n = len(disk.state)
	disk.mu.Unlock()
	if n != 0 {
		t.Fatal("disk state recreated")
	}
}

func TestClientCacheDoesNotNegativeCacheStoppedVM(t *testing.T) {
	prov := newFakeProvider() // no client registered: ErrVMNotRunning
	c := NewClientCache(prov, newFakeClock())
	defer c.Close()
	for i := 0; i < 3; i++ {
		if _, err := c.Get(context.Background(), "synthetic-a"); !errors.Is(err, dockerc.ErrVMNotRunning) {
			t.Fatal(err)
		}
	}
	if prov.callCount("synthetic-a") != 3 {
		t.Fatalf("calls = %d, want 3 (stopped VM not remembered)", prov.callCount("synthetic-a"))
	}
}

// Review finding 8: cancelling the parent context without Stop still ends the
// background disk read.
func TestParentCancelClosesSources(t *testing.T) {
	checkLeaks(t)
	h, d, _, disk := dockerHarness(t, "c1")
	d.set(func(d *fakeDocker) { d.diskBlock = make(chan struct{}) })
	h.start()
	eventually(t, func() bool { return d.diskCallCount() == 1 })
	h.stop()
	done := make(chan struct{})
	go func() { disk.wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("disk read still running after the parent context was cancelled")
	}
}
