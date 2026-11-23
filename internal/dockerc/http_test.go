package dockerc

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestContainers(t *testing.T) {
	var gotPath, gotQuery string
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Write([]byte(listJSON))
	}))
	cs, err := newHTTPClient(sock).Containers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1.41/containers/json" || gotQuery != "all=1" {
		t.Fatalf("request = %s?%s", gotPath, gotQuery)
	}
	want := []Container{
		{ID: "aaaaaaaaaaaa", Name: "synthetic-web", Image: "synthetic/web:1", State: "running",
			Status: "Up 2 hours", Created: time.Unix(1700000000, 0).UTC(),
			Ports: []Port{
				{ContainerPort: 80, Protocol: "tcp"},
				{ContainerPort: 443, Protocol: "tcp", HostIP: "0.0.0.0", HostPort: 8443},
			}},
		// No Ports key in the answer: the field stays nil.
		{ID: "bbbbbbbbbbbb", Name: "synthetic-db", Image: "synthetic/db:2", State: "exited",
			Status: "Exited (0) 3 days ago", Created: time.Unix(1700000100, 0).UTC()},
	}
	if len(cs) != 2 || !reflect.DeepEqual(cs[0], want[0]) || !reflect.DeepEqual(cs[1], want[1]) {
		t.Fatalf("got %+v", cs)
	}
}

func TestStatsOneShot(t *testing.T) {
	var gotPath, gotQuery string
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Write([]byte(statsV2JSON))
	}))
	r, err := newHTTPClient(sock).Stats(context.Background(), "aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1.41/containers/aaaaaaaaaaaa/stats" || gotQuery != "one-shot=true&stream=false" {
		t.Fatalf("request = %s?%s", gotPath, gotQuery)
	}
	if r.CPUTotalNanos != 2000 || r.SystemNanos != 10000 || r.OnlineCPUs != 4 ||
		r.MemoryBytes != 700 || r.MemoryLimit != 4096 || r.At.IsZero() {
		t.Fatalf("reading %+v", r)
	}
}

func TestStatsWithPrecpuAndPerCPUFallback(t *testing.T) {
	body := `{"cpu_stats":{"cpu_usage":{"total_usage":50,"percpu_usage":[1,2]},"system_cpu_usage":100},
	 "precpu_stats":{"cpu_usage":{"total_usage":10},"system_cpu_usage":40},
	 "memory_stats":{"usage":10,"limit":20,"stats":{"total_inactive_file":4}}}`
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	r, err := newHTTPClient(sock).Stats(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if r.OnlineCPUs != 2 || r.MemoryBytes != 6 {
		t.Fatalf("reading %+v", r)
	}
}

func TestStatsRejectsBadID(t *testing.T) {
	c := newHTTPClient(filepath.Join(t.TempDir(), "x"))
	for _, id := range []string{"", "../etc", "a/b", "a b", "a?x=1", "-x", strings.Repeat("a", 200)} {
		_, err := c.Stats(context.Background(), id)
		if err == nil || errors.Is(err, ErrDockerUnavailable) {
			t.Errorf("id %q: err = %v, want validation error before any dial", id, err)
		}
	}
}

func TestStatsGone(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusConflict} {
		sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"message":"no such container"}`, code)
		}))
		_, err := newHTTPClient(sock).Stats(context.Background(), "abc")
		if !errors.Is(err, ErrContainerNotFound) {
			t.Errorf("status %d: err = %v", code, err)
		}
	}
}

func TestDiskUsage(t *testing.T) {
	var gotPath string
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(dfJSON))
	}))
	du, err := newHTTPClient(sock).DiskUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := DiskUsage{ImagesBytes: 900, ContainersBytes: 10, VolumesBytes: 70, BuildCacheBytes: 11, LayersBytes: 900}
	if du != want || gotPath != "/v1.41/system/df" {
		t.Fatalf("got %+v at %s", du, gotPath)
	}
}

func TestSocketMissingAndDialFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.sock")
	c := newHTTPClient(missing)
	for name, call := range map[string]func() error{
		"containers": func() error { _, e := c.Containers(context.Background()); return e },
		"stats":      func() error { _, e := c.Stats(context.Background(), "abc"); return e },
		"df":         func() error { _, e := c.DiskUsage(context.Background()); return e },
	} {
		err := call()
		if !errors.Is(err, ErrDockerUnavailable) {
			t.Errorf("%s: err = %v", name, err)
		}
		if strings.Contains(err.Error(), missing) || strings.Contains(err.Error(), "/") {
			t.Errorf("%s: error text leaks a path: %q", name, err)
		}
	}
}

func TestNon200IsUnavailable(t *testing.T) {
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom /secret/path", http.StatusInternalServerError)
	}))
	_, err := newHTTPClient(sock).Containers(context.Background())
	if !errors.Is(err, ErrDockerUnavailable) || strings.Contains(err.Error(), "/secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestMalformedAndOversizedBodies(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"x":`)) }))
		_, err := newHTTPClient(sock).Containers(context.Background())
		if !errors.Is(err, ErrDockerUnavailable) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("oversized", func(t *testing.T) {
		var served atomic.Int64
		sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("["))
			chunk := []byte(strings.Repeat(" ", 1024))
			for i := 0; i < 4096; i++ {
				n, err := w.Write(chunk)
				served.Add(int64(n))
				if err != nil {
					return
				}
			}
			w.Write([]byte("]"))
		}))
		c := newHTTPClient(sock)
		c.maxBody = 64 << 10
		_, err := c.Containers(context.Background())
		if !errors.Is(err, ErrDockerUnavailable) || !strings.Contains(err.Error(), "too large") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDeadlineAndCancellation(t *testing.T) {
	release := make(chan struct{})
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release) })

	t.Run("per call deadline", func(t *testing.T) {
		c := newHTTPClient(sock)
		c.listTimeout, c.statsTimeout, c.diskUsageTimeout = 50*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond
		for name, call := range map[string]func() error{
			"containers": func() error { _, e := c.Containers(context.Background()); return e },
			"stats":      func() error { _, e := c.Stats(context.Background(), "abc"); return e },
			"df":         func() error { _, e := c.DiskUsage(context.Background()); return e },
		} {
			start := time.Now()
			err := call()
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
				t.Errorf("%s: err = %v after %v", name, err, time.Since(start))
			}
		}
	})
	t.Run("default timeouts", func(t *testing.T) {
		c := newHTTPClient(sock)
		if c.listTimeout != 5*time.Second || c.statsTimeout != 5*time.Second || c.diskUsageTimeout != 15*time.Second {
			t.Fatalf("timeouts %v %v %v", c.listTimeout, c.statsTimeout, c.diskUsageTimeout)
		}
	})
	t.Run("caller cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(30*time.Millisecond, cancel)
		_, err := newHTTPClient(sock).Containers(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestNoProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) }))
	if _, err := newHTTPClient(sock).Containers(context.Background()); err != nil {
		t.Fatalf("proxy env must be ignored: %v", err)
	}
}

type fakeClient struct {
	mu       sync.Mutex
	inFlight int
	maxSeen  int
	gone     map[string]bool
	fail     map[string]error
	readings map[string]Reading
	calls    atomic.Int64
}

func (f *fakeClient) Containers(context.Context) ([]Container, error) { return nil, nil }
func (f *fakeClient) DiskUsage(context.Context) (DiskUsage, error)    { return DiskUsage{}, nil }
func (f *fakeClient) Stats(ctx context.Context, id string) (Reading, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.inFlight++
	if f.inFlight > f.maxSeen {
		f.maxSeen = f.inFlight
	}
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.inFlight--; f.mu.Unlock() }()
	select {
	case <-time.After(10 * time.Millisecond):
	case <-ctx.Done():
		return Reading{}, ctx.Err()
	}
	if f.gone[id] {
		return Reading{}, ErrContainerNotFound
	}
	if err := f.fail[id]; err != nil {
		return Reading{}, err
	}
	return f.readings[id], nil
}

func TestSnapshotConcurrencyCap(t *testing.T) {
	f := &fakeClient{readings: map[string]Reading{}}
	var cs []Container
	for i := 0; i < 20; i++ {
		id := string(rune('a'+i)) + "id"
		cs = append(cs, Container{ID: id, State: "running"})
	}
	out, err := Snapshot(context.Background(), f, cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 20 || f.maxSeen > 4 || f.maxSeen < 2 {
		t.Fatalf("out=%d maxSeen=%d", len(out), f.maxSeen)
	}
}

func TestSnapshotSemantics(t *testing.T) {
	f := &fakeClient{
		gone: map[string]bool{"gone": true},
		readings: map[string]Reading{
			"up":  {CPUTotalNanos: 200, SystemNanos: 1000, OnlineCPUs: 2, MemoryBytes: 50},
			"new": {CPUTotalNanos: 200, SystemNanos: 1000, OnlineCPUs: 2, MemoryBytes: 7},
		},
	}
	prev := map[string]Reading{"up": {CPUTotalNanos: 100, SystemNanos: 500}}
	cs := []Container{
		{ID: "up", State: "running"}, {ID: "new", State: "running"},
		{ID: "gone", State: "running"}, {ID: "off", State: "exited"},
	}
	out, err := Snapshot(context.Background(), f, cs, prev)
	if err != nil {
		t.Fatal(err)
	}
	if got := out["up"].CPUPercent; got != 40 || out["up"].MemoryBytes != 50 {
		t.Errorf("up = %+v", out["up"])
	}
	if out["new"].CPUPercent != 0 || out["new"].MemoryBytes != 7 {
		t.Errorf("new = %+v", out["new"])
	}
	if _, ok := out["gone"]; ok {
		t.Error("vanished container should be skipped")
	}
	if u, ok := out["off"]; !ok || u != (Usage{}) {
		t.Errorf("off = %+v ok=%v", u, ok)
	}
	if f.calls.Load() != 3 {
		t.Errorf("stats calls = %d, want 3 (none for exited)", f.calls.Load())
	}
}

func TestSnapshotErrorAndCancel(t *testing.T) {
	boom := errors.New("boom")
	f := &fakeClient{fail: map[string]error{"bad": boom}}
	cs := []Container{{ID: "bad", State: "running"}, {ID: "ok", State: "running"}}
	if _, err := Snapshot(context.Background(), f, cs, nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Snapshot(ctx, &fakeClient{}, cs, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestSnapshotNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		f := &fakeClient{fail: map[string]error{"bad": errors.New("boom")}}
		cs := []Container{{ID: "bad", State: "running"}, {ID: "a", State: "running"}, {ID: "b", State: "running"}}
		Snapshot(context.Background(), f, cs, nil)
	}
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+1 {
		t.Fatalf("goroutines %d -> %d", before, after)
	}
}
