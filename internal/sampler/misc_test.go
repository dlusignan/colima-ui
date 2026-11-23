package sampler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/dockerc"
)

func TestLogOnce(t *testing.T) {
	lc := &logCapture{}
	l := newLogOnce(slog.New(lc))
	k := logKey{"p", "s"}
	for i := 0; i < 5; i++ {
		l.set(k, true, "failed", errors.New("x"))
	}
	l.set(k, false, "s", nil)
	l.set(k, false, "s", nil)
	if lc.count(slog.LevelWarn, "failed") != 1 || lc.count(slog.LevelInfo, "recovered") != 1 {
		t.Fatalf("records = %+v", lc.recs)
	}
	l.set(k, true, "failed", nil)
	l.forget("p")
	l.set(k, false, "s", nil)
	if lc.count(slog.LevelInfo, "recovered") != 1 {
		t.Fatal("forget must clear state silently")
	}
}

func TestLogOnceConcurrent(t *testing.T) {
	l := newLogOnce(slog.New(&logCapture{}))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				l.set(logKey{"p", "s"}, j%3 == 0, "m", nil)
				l.forget("p")
			}
		}()
	}
	wg.Wait()
}

func TestClientCacheConcurrentGetBuildsOnce(t *testing.T) {
	prov := newFakeProvider()
	prov.by["synthetic-a"] = newFakeDocker()
	c := NewClientCache(prov, newFakeClock())
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Get(context.Background(), "synthetic-a"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if prov.callCount("synthetic-a") != 1 {
		t.Fatalf("provider calls = %d", prov.callCount("synthetic-a"))
	}
	c.Drop("synthetic-a")
	if _, err := c.Get(context.Background(), "synthetic-a"); err != nil || prov.callCount("synthetic-a") != 2 {
		t.Fatal("drop must force a rebuild")
	}
	c.Forget("synthetic-a")
}

func TestClientCacheDoesNotRememberCancellation(t *testing.T) {
	prov := newFakeProvider()
	prov.by["synthetic-a"] = newFakeDocker()
	prov.setErr(context.Canceled)
	c := NewClientCache(prov, newFakeClock())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Get(ctx, "synthetic-a"); err == nil {
		t.Fatal("want error")
	}
	prov.setErr(nil)
	if _, err := c.Get(context.Background(), "synthetic-a"); err != nil {
		t.Fatalf("cancelled lookup was negative cached: %v", err)
	}
}

type fakeRunner struct {
	out string
	err error
	got []string
}

func (r *fakeRunner) Run(_ context.Context, args []string) (colima.Result, error) {
	r.got = args
	return colima.Result{Stdout: []byte(r.out)}, r.err
}

func TestColimaListerMapsInstances(t *testing.T) {
	r := &fakeRunner{out: `{"name":"synthetic-a","status":"Running","runtime":"docker","cpus":2,"memory":1024,"disk":2048}
{"name":"synthetic-b","status":"Stopped","runtime":"containerd","cpus":1,"memory":1,"disk":1}
`}
	ts, err := ColimaLister{Runner: r}.Targets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || !ts[0].sampled() || ts[1].sampled() || ts[0].CPUs != 2 || ts[0].Memory != 1024 || ts[0].Disk != 2048 {
		t.Fatalf("targets = %+v", ts)
	}
	if len(r.got) != 2 || r.got[0] != "list" || r.got[1] != "--json" {
		t.Fatalf("args = %v", r.got)
	}
	r.err = errors.New("boom")
	if _, err := (ColimaLister{Runner: r}).Targets(context.Background()); err == nil {
		t.Fatal("want error")
	}
}

func TestRealClockTicks(t *testing.T) {
	var c Clock = RealClock{}
	tk := c.NewTicker(time.Millisecond)
	defer tk.Stop()
	select {
	case <-tk.C():
	case <-time.After(2 * time.Second):
		t.Fatal("no tick")
	}
	if c.Now().IsZero() {
		t.Fatal("zero now")
	}
}

func TestDiskSourceErrorSentinelsPassThrough(t *testing.T) {
	prov := newFakeProvider() // no profile registered: For gives ErrVMNotRunning
	clock := newFakeClock()
	disk := NewDiskSource(NewClientCache(prov, clock), clock)
	defer disk.Close()
	if _, err := disk.Collect(context.Background(), running("synthetic-a")); err != nil {
		t.Fatalf("first collect returns the cached state, got %v", err)
	}
	disk.wait()
	_, err := disk.Collect(context.Background(), running("synthetic-a"))
	if !errors.Is(err, dockerc.ErrVMNotRunning) {
		t.Fatalf("err = %v", err)
	}
}
