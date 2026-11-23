package sampler

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/events"
)

// dockerHarness wires the production sources to a fake docker engine.
func dockerHarness(t *testing.T, ids ...string) (*harness, *fakeDocker, *fakeProvider, *DiskSource) {
	t.Helper()
	d := newFakeDocker(ids...)
	d.disk = dockerc.DiskUsage{LayersBytes: 100, ContainersBytes: 20, VolumesBytes: 3, BuildCacheBytes: 4, ImagesBytes: 999}
	prov := newFakeProvider()
	prov.by["synthetic-a"] = d
	clock := newFakeClock()
	cache := NewClientCache(prov, clock)
	disk := NewDiskSource(cache, clock)
	h := newHarness(t, NewContainerSource(cache), disk)
	h.clock = clock
	h.s.clock = clock
	h.lst.set(running("synthetic-a"))
	return h, d, prov, disk
}

func TestFirstTickWritesVMRowOnlyThenContainers(t *testing.T) {
	h, d, _, _ := dockerHarness(t, "c1", "c2")
	h.start()
	rows := h.sink.all()
	if len(rows) != 1 || rows[0].Scope != "vm" {
		t.Fatalf("first tick rows = %+v, want one vm row", rows)
	}
	if rows[0].CPUPercent != nil {
		t.Fatalf("first vm cpu = %v, want NULL (no baseline)", *rows[0].CPUPercent)
	}
	if len(h.pub.metrics()) != 0 {
		t.Fatal("no metric event without a cpu figure")
	}
	if rows[0].MemoryBytes == nil || *rows[0].MemoryBytes != 20 {
		t.Fatalf("first vm memory = %v, want 20", rows[0].MemoryBytes)
	}
	if rows[0].TakenAt.Nanosecond() != 0 {
		t.Fatalf("takenAt not truncated: %v", rows[0].TakenAt)
	}

	d.advance(50, 200)
	h.tick()
	vm := h.sink.scope("synthetic-a", "vm")
	if len(vm) != 2 {
		t.Fatalf("vm rows = %d, want 2", len(vm))
	}
	if got := *vm[1].CPUPercent; got < 99.9 || got > 100.1 {
		t.Fatalf("vm cpu = %v, want 100 (sum of two 50s)", got)
	}
	if got := *vm[1].MemoryBytes; got != 400 {
		t.Fatalf("vm memory = %d, want 400", got)
	}
	for _, id := range []string{"c1", "c2"} {
		cr := h.sink.scope("synthetic-a", id)
		if len(cr) != 1 {
			t.Fatalf("%s rows = %d, want 1 (second sighting)", id, len(cr))
		}
		if cr[0].DiskBytes != nil {
			t.Fatalf("container disk must be NULL")
		}
		if got := *cr[0].CPUPercent; got < 49.9 || got > 50.1 {
			t.Fatalf("%s cpu = %v, want 50", id, got)
		}
		if *cr[0].MemoryBytes != 200 {
			t.Fatalf("%s memory = %d", id, *cr[0].MemoryBytes)
		}
	}
	if !vm[0].TakenAt.Before(vm[1].TakenAt) {
		t.Fatalf("takenAt did not advance")
	}
}

func TestPublishesMetricEventsMatchingRows(t *testing.T) {
	h, d, _, disk := dockerHarness(t, "c1")
	h.start()
	disk.wait()
	d.advance(10, 77)
	h.tick()
	var vm, c1 *events.Metric
	ms := h.pub.metrics()
	for i := range ms {
		switch ms[i].Scope {
		case "vm":
			vm = &ms[i]
		case "c1":
			c1 = &ms[i]
		}
	}
	if vm == nil || c1 == nil {
		t.Fatalf("events = %+v", ms)
	}
	if vm.Profile != "synthetic-a" || vm.MemoryBytes != 77 {
		t.Fatalf("vm event = %+v", *vm)
	}
	if vm.DiskBytes == nil || *vm.DiskBytes != 127 { // layers+containers+volumes+build, no images
		t.Fatalf("vm disk = %v, want 127", vm.DiskBytes)
	}
	if c1.DiskBytes != nil || c1.MemoryBytes != 77 {
		t.Fatalf("container event = %+v", *c1)
	}
	if !vm.TakenAt.Equal(h.sink.scope("synthetic-a", "vm")[1].TakenAt) {
		t.Fatalf("event takenAt differs from row")
	}
}

func TestStoppedAndNonDockerProfilesAreSkippedQuietly(t *testing.T) {
	var calls atomic.Int32
	src := &fakeSource{name: "x", collect: func(context.Context, Target) (Partial, error) {
		calls.Add(1)
		return Partial{VMCPU: ptr(1.0), VMMemory: ptr(int64(1))}, nil
	}}
	h := newHarness(t, src)
	stopped := running("synthetic-stopped")
	stopped.Status = "Stopped"
	other := running("synthetic-containerd")
	other.Runtime = "containerd"
	h.lst.set(stopped, other)
	h.start()
	for i := 0; i < 8; i++ {
		h.tick()
	}
	if calls.Load() != 0 || len(h.sink.all()) != 0 || len(h.pub.metrics()) != 0 {
		t.Fatalf("stopped profiles were sampled")
	}
	if h.logs.warns() != 0 {
		t.Fatalf("warnings logged for stopped profiles")
	}
	if n := h.logs.count(slog.LevelInfo, "runtime is not docker"); n != 1 {
		t.Fatalf("non docker info lines = %d, want exactly 1", n)
	}
}

func TestRunningToStoppedLogsOnceAndForgets(t *testing.T) {
	src := &fakeSource{name: "x", collect: func(context.Context, Target) (Partial, error) {
		return Partial{VMCPU: ptr(1.0), VMMemory: ptr(int64(1))}, nil
	}}
	h := newHarness(t, src)
	h.s.listEvery = 1
	h.lst.set(running("synthetic-a"))
	h.start()
	st := running("synthetic-a")
	st.Status = "Stopped"
	h.lst.set(st)
	h.tick()
	h.tick()
	h.tick()
	if n := h.logs.count(slog.LevelInfo, "sampling paused"); n != 1 {
		t.Fatalf("stopped transition lines = %d", n)
	}
	if got := src.forgotten(); len(got) != 1 || got[0] != "synthetic-a" {
		t.Fatalf("forgotten = %v", got)
	}
	if h.logs.warns() != 0 {
		t.Fatal("unexpected warning")
	}
	rows := len(h.sink.all())
	h.tick()
	if len(h.sink.all()) != rows {
		t.Fatal("stopped profile still recorded")
	}
	// Disappearing from the list entirely also forgets.
	h.lst.set(running("synthetic-b"))
	h.tick()
	h.tick()
	if got := src.forgotten(); len(got) != 1 {
		t.Fatalf("stopped profile forgotten twice: %v", got)
	}
}

func TestRestartResetsBaseline(t *testing.T) {
	h, d, prov, _ := dockerHarness(t, "c1")
	h.s.listEvery = 1
	h.start()
	d.advance(40, 5)
	h.tick()
	if len(h.sink.scope("synthetic-a", "c1")) != 1 {
		t.Fatal("c1 should be recorded from the second tick")
	}
	// Stop, then start again with a fresh engine whose counters restarted.
	st := running("synthetic-a")
	st.Status = "Stopped"
	h.lst.set(st)
	h.tick()
	d2 := newFakeDocker("c1")
	prov.mu.Lock()
	prov.by["synthetic-a"] = d2
	prov.mu.Unlock()
	h.lst.set(running("synthetic-a"))
	h.tick()
	rows := h.sink.scope("synthetic-a", "c1")
	if len(rows) != 1 {
		t.Fatalf("c1 rows after restart tick = %d, want still 1 (new baseline)", len(rows))
	}
	last := h.sink.scope("synthetic-a", "vm")
	if last[len(last)-1].CPUPercent != nil {
		t.Fatalf("restart vm cpu must be NULL, got %v", *last[len(last)-1].CPUPercent)
	}
	d2.advance(25, 9)
	h.tick()
	if len(h.sink.scope("synthetic-a", "c1")) != 2 {
		t.Fatal("c1 not recorded after new baseline")
	}
}

func TestFailingContainerSourceStillRecordsDiskAndLogsOnce(t *testing.T) {
	h, d, _, disk := dockerHarness(t, "c1")
	h.start() // tick 0 starts the disk read
	disk.wait()
	d.set(func(d *fakeDocker) { d.listErr = dockerc.ErrDockerUnavailable })
	for i := 0; i < 5; i++ {
		h.tick()
	}
	if got := h.logs.count(slog.LevelWarn, "containers source failed"); got != 1 {
		t.Fatalf("outage warnings = %d, want 1", got)
	}
	vm := h.sink.scope("synthetic-a", "vm")
	last := vm[len(vm)-1]
	if last.CPUPercent != nil || last.MemoryBytes != nil || last.DiskBytes == nil || *last.DiskBytes != 127 {
		t.Fatalf("disk only row = %+v", last)
	}
	for _, m := range h.pub.metrics() {
		if m.TakenAt.Equal(last.TakenAt) {
			t.Fatalf("metric event published while cpu and memory unknown: %+v", m)
		}
	}
	d.set(func(d *fakeDocker) { d.listErr = nil })
	h.tick() // recovery (client was dropped, For succeeds again after the negative cache)
	h.clock.Advance(time.Minute)
	h.tick()
	if got := h.logs.count(slog.LevelInfo, "recovered"); got < 1 {
		t.Fatalf("recovery lines = %d", got)
	}
	if got := h.logs.warns(); got != 1 {
		t.Fatalf("warnings after recovery = %d, want 1", got)
	}
}

func TestSourceTimeout(t *testing.T) {
	blocking := &fakeSource{name: "slow", timeout: 20 * time.Millisecond, collect: func(ctx context.Context, _ Target) (Partial, error) {
		<-ctx.Done()
		return Partial{}, ctx.Err()
	}}
	good := &fakeSource{name: "good", collect: func(context.Context, Target) (Partial, error) {
		return Partial{VMCPU: ptr(2.0), VMMemory: ptr(int64(3))}, nil
	}}
	h := newHarness(t, blocking, good)
	h.lst.set(running("synthetic-a"))
	h.start()
	rows := h.sink.all()
	if len(rows) != 1 || *rows[0].CPUPercent != 2 {
		t.Fatalf("rows = %+v; the good source must still record", rows)
	}
	if h.logs.count(slog.LevelWarn, "slow source failed") != 1 {
		t.Fatal("timeout not logged once")
	}
	h.tick()
	h.tick()
	if h.logs.count(slog.LevelWarn, "slow source failed") != 1 {
		t.Fatal("timeout logged again on later ticks")
	}
}

func TestSlowWorkerSkipsTicksInsteadOfPilingUp(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	src := &fakeSource{name: "x", timeout: 10 * time.Second, collect: func(ctx context.Context, _ Target) (Partial, error) {
		if calls.Add(1) == 1 {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return Partial{VMCPU: ptr(1.0), VMMemory: ptr(int64(1))}, nil
	}}
	h := newHarness(t, src)
	h.lst.set(running("synthetic-a"))
	h.s.Start(h.ctx)
	<-h.ticks // worker 1 is blocked
	eventually(t, func() bool { return calls.Load() == 1 })
	for i := 0; i < 3; i++ {
		h.clock.Tick()
		<-h.ticks
	}
	if got := h.s.skipped.Load(); got != 3 {
		t.Fatalf("skipped = %d, want 3", got)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("collect calls = %d, want 1 (single flight)", got)
	}
	close(release)
	h.settle()
	h.tick()
	if calls.Load() != 2 {
		t.Fatalf("sampling did not resume, calls = %d", calls.Load())
	}
}

func TestProfilesSampledIndependently(t *testing.T) {
	release := make(chan struct{})
	src := &fakeSource{name: "x", timeout: 10 * time.Second, collect: func(ctx context.Context, tg Target) (Partial, error) {
		if tg.Name == "synthetic-slow" {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return Partial{VMCPU: ptr(1.0), VMMemory: ptr(int64(1))}, nil
	}}
	h := newHarness(t, src)
	h.lst.set(running("synthetic-slow"), running("synthetic-fast"))
	h.s.Start(h.ctx)
	<-h.ticks
	eventually(t, func() bool { return len(h.sink.scope("synthetic-fast", "vm")) == 1 })
	if len(h.sink.scope("synthetic-slow", "vm")) != 0 {
		t.Fatal("slow profile recorded early")
	}
	close(release)
	eventually(t, func() bool { return len(h.sink.scope("synthetic-slow", "vm")) == 1 })
}

func TestStopCancelsInFlightTickWithoutWriting(t *testing.T) {
	checkLeaks(t)
	started := make(chan struct{})
	src := &fakeSource{name: "x", timeout: time.Minute, collect: func(ctx context.Context, _ Target) (Partial, error) {
		close(started)
		<-ctx.Done()
		return Partial{VMCPU: ptr(1.0), VMMemory: ptr(int64(1))}, ctx.Err()
	}}
	h := newHarness(t, src)
	h.lst.set(running("synthetic-a"))
	h.s.Start(h.ctx)
	<-started
	done := make(chan struct{})
	go func() { h.s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
	if len(h.sink.all()) != 0 || len(h.pub.metrics()) != 0 {
		t.Fatal("cancelled tick wrote or published")
	}
	if h.logs.warns() != 0 {
		t.Fatal("cancellation logged as an error")
	}
	if h.clock.liveTickers() != 0 {
		t.Fatal("ticker not stopped")
	}
}

func TestStopDuringSinkWriteReturns(t *testing.T) {
	checkLeaks(t)
	entered := make(chan struct{})
	src := &fakeSource{name: "x", collect: func(context.Context, Target) (Partial, error) {
		return Partial{VMCPU: ptr(1.0), VMMemory: ptr(int64(1))}, nil
	}}
	h := newHarness(t, src)
	h.sink.block = func(ctx context.Context) {
		close(entered)
		<-ctx.Done()
	}
	h.lst.set(running("synthetic-a"))
	h.s.Start(h.ctx)
	<-entered
	h.s.Stop()
	if len(h.pub.metrics()) != 0 || h.logs.warns() != 0 {
		t.Fatal("cancelled write must neither publish nor log")
	}
}

func TestParentContextCancelStopsLoop(t *testing.T) {
	checkLeaks(t)
	h := newHarness(t, &fakeSource{name: "x", collect: func(context.Context, Target) (Partial, error) { return Partial{}, nil }})
	h.start()
	h.stop()
	eventually(t, func() bool { return h.clock.liveTickers() == 0 })
	h.s.Stop()
}

func TestStopIdempotentStartOnce(t *testing.T) {
	checkLeaks(t)
	h := newHarness(t, &fakeSource{name: "x", collect: func(context.Context, Target) (Partial, error) { return Partial{}, nil }})
	h.s.Stop() // before Start
	h.s.Start(h.ctx)
	h.s.Stop()
	h.s.Stop()
	if h.lst.count() != 0 {
		t.Fatal("Start after Stop must be a no-op")
	}

	h2 := newHarness(t, &fakeSource{name: "x", collect: func(context.Context, Target) (Partial, error) { return Partial{}, nil }})
	h2.start()
	h2.s.Start(h2.ctx) // second call ignored
	if len(h2.clock.tickers) != 1 {
		t.Fatalf("tickers = %d, want 1", len(h2.clock.tickers))
	}
}

func TestStoreFailureStillPublishesAndLogsOnce(t *testing.T) {
	src := &fakeSource{name: "x", collect: func(context.Context, Target) (Partial, error) {
		return Partial{VMCPU: ptr(1.0), VMMemory: ptr(int64(5))}, nil
	}}
	h := newHarness(t, src)
	h.sink.setErr(errors.New("disk full"))
	h.lst.set(running("synthetic-a"))
	h.start()
	h.tick()
	h.tick()
	if len(h.pub.metrics()) != 3 {
		t.Fatalf("metrics = %d, want 3", len(h.pub.metrics()))
	}
	if got := h.logs.count(slog.LevelWarn, "writing history failed"); got != 1 {
		t.Fatalf("store warnings = %d", got)
	}
	h.sink.setErr(nil)
	h.tick()
	if h.logs.count(slog.LevelInfo, "recovered") != 1 {
		t.Fatal("store recovery not logged")
	}
}

func TestSourceErrorsAreIsolatedPerSource(t *testing.T) {
	bad := &fakeSource{name: "bad", collect: func(context.Context, Target) (Partial, error) {
		return Partial{}, errors.New("boom")
	}}
	good := &fakeSource{name: "good", collect: func(context.Context, Target) (Partial, error) {
		return Partial{VMCPU: ptr(1.0), VMMemory: ptr(int64(1)), VMDisk: ptr(int64(9))}, nil
	}}
	h := newHarness(t, bad, good)
	h.lst.set(running("synthetic-a"))
	h.start()
	h.tick()
	if len(h.sink.all()) != 2 || h.logs.warns() != 1 {
		t.Fatalf("rows=%d warns=%d", len(h.sink.all()), h.logs.warns())
	}
}

func TestStoppedErrorsAskForRelistNotLog(t *testing.T) {
	src := &fakeSource{name: "x", collect: func(context.Context, Target) (Partial, error) {
		return Partial{}, dockerc.ErrVMNotRunning
	}}
	h := newHarness(t, src)
	h.lst.set(running("synthetic-a"))
	h.start()
	before := h.lst.count()
	h.tick() // relist requested by tick 0
	if h.lst.count() != before+1 {
		t.Fatalf("list calls = %d, want %d (forced relist)", h.lst.count(), before+1)
	}
	if h.logs.warns() != 0 || len(h.sink.all()) != 0 {
		t.Fatal("stopped VM must be silent")
	}
}

func TestListFailureKeepsPreviousSetAndLogsOnce(t *testing.T) {
	src := &fakeSource{name: "x", collect: func(context.Context, Target) (Partial, error) {
		return Partial{VMCPU: ptr(1.0), VMMemory: ptr(int64(1))}, nil
	}}
	h := newHarness(t, src)
	h.s.listEvery = 1
	h.lst.set(running("synthetic-a"))
	h.start()
	h.lst.setErr(errors.New("colima list failed"))
	h.tick()
	h.tick()
	h.tick()
	if len(h.sink.scope("synthetic-a", "vm")) != 4 {
		t.Fatalf("sampling stopped after list failure")
	}
	if got := h.logs.count(slog.LevelWarn, "listing profiles failed"); got != 1 {
		t.Fatalf("list warnings = %d", got)
	}
	h.lst.setErr(nil)
	h.tick()
	if h.logs.count(slog.LevelInfo, "recovered") != 1 {
		t.Fatal("list recovery not logged")
	}
}

func TestRefreshSamplesNewProfileImmediately(t *testing.T) {
	src := &fakeSource{name: "x", collect: func(context.Context, Target) (Partial, error) {
		return Partial{VMCPU: ptr(1.0), VMMemory: ptr(int64(1))}, nil
	}}
	h := newHarness(t, src)
	h.start() // no profiles yet
	h.lst.set(running("synthetic-new"))
	h.s.Refresh()
	h.s.Refresh() // second call must not block
	eventually(t, func() bool { return len(h.sink.scope("synthetic-new", "vm")) == 1 })
}

func TestContainerCapKeepsBusiestAndWarnsOnce(t *testing.T) {
	ids := make([]string, 70)
	for i := range ids {
		ids[i] = string(rune('a'+i/26)) + string(rune('a'+i%26))
	}
	h, d, _, _ := dockerHarness(t, ids...)
	h.start()
	d.advance(1, 100)
	d.mu.Lock()
	for i, id := range ids {
		r := d.readings[id]
		r.MemoryBytes = uint64(1000 + i) // last ones are busiest
		d.readings[id] = r
	}
	d.mu.Unlock()
	h.tick()
	h.tick()
	count := map[string]bool{}
	for _, r := range h.sink.all() {
		if r.Scope != "vm" {
			count[r.Scope] = true
		}
	}
	if len(count) != 64 {
		t.Fatalf("recorded containers = %d, want 64", len(count))
	}
	if count[ids[0]] || !count[ids[69]] {
		t.Fatal("cap must keep the highest memory containers")
	}
	if h.logs.count(slog.LevelWarn, "too many containers") != 1 {
		t.Fatal("cap warning must be logged once")
	}
}

func TestExitedContainersAndNoContainers(t *testing.T) {
	h, d, _, _ := dockerHarness(t, "c1")
	d.set(func(d *fakeDocker) {
		d.containers = append(d.containers, dockerc.Container{ID: "dead", State: "exited"})
	})
	h.start()
	d.advance(10, 5)
	h.tick()
	if len(h.sink.scope("synthetic-a", "dead")) != 0 {
		t.Fatal("exited container recorded")
	}
	d.set(func(d *fakeDocker) { d.containers = nil })
	h.tick()
	vm := h.sink.scope("synthetic-a", "vm")
	last := vm[len(vm)-1]
	if last.CPUPercent == nil || *last.CPUPercent != 0 || *last.MemoryBytes != 0 {
		t.Fatalf("empty VM row = %+v, want zeros", last)
	}
}

func TestVanishedContainerLeavesNoState(t *testing.T) {
	h, d, _, _ := dockerHarness(t, "c1", "c2")
	h.start()
	d.advance(10, 5)
	h.tick()
	d.advance(10, 5)
	d.set(func(d *fakeDocker) { delete(d.readings, "c2") }) // vanishes between list and stats
	h.tick()
	if got := len(h.sink.scope("synthetic-a", "c2")); got != 1 {
		t.Fatalf("c2 rows = %d, want 1", got)
	}
	// c2 comes back: it is a new sighting, not recorded until the next tick.
	d.set(func(d *fakeDocker) {
		d.readings["c2"] = dockerc.Reading{SystemNanos: 1, CPUTotalNanos: 1, OnlineCPUs: 1}
	})
	h.tick()
	if got := len(h.sink.scope("synthetic-a", "c2")); got != 1 {
		t.Fatalf("c2 rows after return = %d, want 1", got)
	}
}

func TestDiskCadenceAndStaleness(t *testing.T) {
	h, d, _, disk := dockerHarness(t, "c1")
	h.start()
	disk.wait()
	if d.diskCallCount() != 1 {
		t.Fatalf("disk calls after tick 0 = %d", d.diskCallCount())
	}
	for i := 0; i < 11; i++ {
		h.tick()
	}
	disk.wait()
	if d.diskCallCount() != 1 {
		t.Fatalf("disk calls within 12 ticks = %d, want 1", d.diskCallCount())
	}
	vm := h.sink.scope("synthetic-a", "vm")
	if vm[0].DiskBytes != nil {
		t.Fatal("tick 0 has no disk value yet (async read)")
	}
	for i, r := range vm[1:] {
		if r.DiskBytes == nil || *r.DiskBytes != 127 {
			t.Fatalf("row %d disk = %v, want the cached value", i+1, r.DiskBytes)
		}
	}
	d.set(func(d *fakeDocker) { d.disk = dockerc.DiskUsage{LayersBytes: 500} })
	h.tick() // 12th Collect
	disk.wait()
	if d.diskCallCount() != 2 {
		t.Fatalf("disk calls on 12th tick = %d, want 2", d.diskCallCount())
	}
	h.tick()
	vm = h.sink.scope("synthetic-a", "vm")
	if got := *vm[len(vm)-1].DiskBytes; got != 500 {
		t.Fatalf("disk after refresh = %d", got)
	}
	// Reads fail from now on and the value ages past 3 minutes.
	d.set(func(d *fakeDocker) { d.diskErr = errors.New("df failed") })
	for i := 0; i < 11; i++ {
		h.tick()
	}
	h.tick()
	disk.wait()
	h.clock.Advance(4 * time.Minute)
	h.tick()
	vm = h.sink.scope("synthetic-a", "vm")
	if vm[len(vm)-1].DiskBytes != nil {
		t.Fatal("stale disk value must become NULL")
	}
	if got := h.logs.count(slog.LevelWarn, "disk source failed"); got != 1 {
		t.Fatalf("disk failure warnings = %d, want 1", got)
	}
}

func TestDiskReadIsSingleFlightAndAsync(t *testing.T) {
	checkLeaks(t)
	h, d, _, disk := dockerHarness(t, "c1")
	block := make(chan struct{})
	d.set(func(d *fakeDocker) { d.diskBlock = block })
	h.s.Start(h.ctx)
	h.waitTick() // tick completes although DiskUsage is blocked
	for i := 0; i < 15; i++ {
		h.tick()
	}
	if got := d.diskCallCount(); got != 1 {
		t.Fatalf("disk calls while one is in flight = %d, want 1", got)
	}
	if len(h.sink.scope("synthetic-a", "vm")) != 16 {
		t.Fatal("ticks must not wait for the disk read")
	}
	close(block)
	disk.wait()
}

func TestDiskCloseCancelsReadInFlight(t *testing.T) {
	checkLeaks(t)
	h, d, _, disk := dockerHarness(t, "c1")
	d.set(func(d *fakeDocker) { d.diskBlock = make(chan struct{}) })
	h.start()
	eventually(t, func() bool { return d.diskCallCount() == 1 })
	h.s.Stop() // closes the disk source, which must unblock the read
	disk.wait()
	if h.logs.warns() != 0 {
		t.Fatal("cancelled disk read logged")
	}
}

func TestDockerFailureDropsClientAndNegativeCaches(t *testing.T) {
	h, d, prov, _ := dockerHarness(t, "c1")
	h.start()
	if prov.callCount("synthetic-a") != 1 {
		t.Fatalf("provider calls = %d, want 1 (one client for both sources)", prov.callCount("synthetic-a"))
	}
	h.tick()
	h.tick()
	if prov.callCount("synthetic-a") != 1 {
		t.Fatal("client must be cached across ticks")
	}
	d.set(func(d *fakeDocker) { d.listErr = dockerc.ErrDockerUnavailable })
	h.tick() // fails, client dropped
	prov.setErr(dockerc.ErrDockerUnavailable)
	h.tick() // For fails, negative cached
	n := prov.callCount("synthetic-a")
	h.tick()
	h.tick()
	if prov.callCount("synthetic-a") != n {
		t.Fatalf("provider spawned during the negative cache window")
	}
	prov.setErr(nil)
	d.set(func(d *fakeDocker) { d.listErr = nil })
	h.clock.Advance(clientRetryAfter + time.Second)
	h.tick()
	if prov.callCount("synthetic-a") != n+1 {
		t.Fatal("client not rebuilt after the retry window")
	}
}

func TestLeakFreeUnderChurn(t *testing.T) {
	checkLeaks(t)
	h, d, _, _ := dockerHarness(t, "c1", "c2")
	h.s.listEvery = 2
	h.start()
	for i := 0; i < 30; i++ {
		d.advance(5, 5)
		h.tick()
		if i == 10 {
			d.set(func(d *fakeDocker) { d.listErr = dockerc.ErrDockerUnavailable })
		}
		if i == 15 {
			d.set(func(d *fakeDocker) { d.listErr = nil })
		}
	}
	h.s.Stop()
}
