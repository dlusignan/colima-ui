package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

type fakeDockerClient struct {
	mu       sync.Mutex
	list     []dockerc.Container
	listErr  error
	statsErr error
	gone     map[string]bool
	calls    map[string]int
}

func (f *fakeDockerClient) Containers(context.Context) ([]dockerc.Container, error) {
	return f.list, f.listErr
}

func (f *fakeDockerClient) Stats(_ context.Context, id string) (dockerc.Reading, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[id]++
	if f.statsErr != nil {
		return dockerc.Reading{}, f.statsErr
	}
	if f.gone[id] {
		return dockerc.Reading{}, dockerc.ErrContainerNotFound
	}
	n := uint64(f.calls[id])
	return dockerc.Reading{
		At:            time.Unix(int64(n), 0),
		CPUTotalNanos: n * 100,
		SystemNanos:   n * 1000,
		OnlineCPUs:    2,
		MemoryBytes:   4096,
	}, nil
}

func (f *fakeDockerClient) DiskUsage(context.Context) (dockerc.DiskUsage, error) {
	return dockerc.DiskUsage{}, nil
}

type fakeProvider struct {
	client dockerc.Client
	err    error
}

func (p *fakeProvider) For(context.Context, string) (dockerc.Client, error) { return p.client, p.err }

func containersServer(g ProfileGate, p dockerc.Provider) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithContainers(g, p)), nil)
}

func zeroWait(t *testing.T) {
	t.Helper()
	old := cpuSampleWait
	cpuSampleWait = 0
	t.Cleanup(func() { cpuSampleWait = old })
}

type containersBody struct {
	Containers []containerJSON `json:"containers"`
}

func TestContainersRunning(t *testing.T) {
	zeroWait(t)
	fc := &fakeDockerClient{list: []dockerc.Container{
		{ID: "bbb", Name: "web", Image: "nginx:1.27", State: "running"},
		{ID: "ccc", Name: "old", Image: "busybox", State: "exited"},
		{ID: "aaa", Name: "db", Image: "postgres:16", State: "running"},
	}}
	g := &fakeGate{status: "Running"}
	rec := call(t, containersServer(g, &fakeProvider{client: fc}), "GET", "/api/profiles/synthetic-a/containers", "")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body containersBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := body.Containers
	if len(got) != 3 || got[0].Name != "db" || got[1].Name != "old" || got[2].Name != "web" {
		t.Fatalf("order/len: %+v", got)
	}
	// Two rounds: second reading minus first is 100 of 1000 over 2 cpus = 20%.
	if got[0].CPUPercent < 19.99 || got[0].CPUPercent > 20.01 || got[0].MemoryBytes != 4096 {
		t.Fatalf("db usage: %+v", got[0])
	}
	if got[0].ID != "aaa" || got[0].Image != "postgres:16" || got[0].State != "running" {
		t.Fatalf("db fields: %+v", got[0])
	}
	if got[1].CPUPercent != 0 || got[1].MemoryBytes != 0 || got[1].State != "exited" {
		t.Fatalf("exited container should be zero: %+v", got[1])
	}
	if fc.calls["aaa"] != 2 || fc.calls["ccc"] != 0 {
		t.Fatalf("stats calls: %v", fc.calls)
	}
	if len(g.names) != 1 || g.names[0] != "synthetic-a" {
		t.Fatalf("gate names %v", g.names)
	}
}

func TestContainersEmpty(t *testing.T) {
	zeroWait(t)
	rec := call(t, containersServer(&fakeGate{status: "running"}, &fakeProvider{client: &fakeDockerClient{}}),
		"GET", "/api/profiles/default/containers", "")
	if rec.Code != 200 || rec.Body.String() != "{\"containers\":[]}\n" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestContainersNoRunningSkipsWait(t *testing.T) {
	old := cpuSampleWait
	cpuSampleWait = time.Hour
	t.Cleanup(func() { cpuSampleWait = old })
	fc := &fakeDockerClient{list: []dockerc.Container{{ID: "a", Name: "x", State: "exited"}}}
	rec := call(t, containersServer(&fakeGate{status: "running"}, &fakeProvider{client: fc}),
		"GET", "/api/profiles/default/containers", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestContainersVanishedKeepsZeros(t *testing.T) {
	zeroWait(t)
	fc := &fakeDockerClient{
		list: []dockerc.Container{{ID: "a", Name: "x", State: "running"}},
		gone: map[string]bool{"a": true},
	}
	rec := call(t, containersServer(&fakeGate{status: "running"}, &fakeProvider{client: fc}),
		"GET", "/api/profiles/default/containers", "")
	var body containersBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || len(body.Containers) != 1 || body.Containers[0].MemoryBytes != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestContainersStoppedGate(t *testing.T) {
	p := &fakeProvider{err: errors.New("must not be called")}
	rec := call(t, containersServer(&fakeGate{status: "stopped"}, p), "GET", "/api/profiles/default/containers", "")
	w := wantErr(t, rec, 409, "invalid_state")
	if w.Error.Details["status"] != "stopped" {
		t.Fatalf("details %v", w.Error.Details)
	}
}

func TestContainersProviderErrors(t *testing.T) {
	zeroWait(t)
	cases := []struct {
		name   string
		p      dockerc.Provider
		status int
		code   string
	}{
		{"vm stopped", &fakeProvider{err: dockerc.ErrVMNotRunning}, 409, "invalid_state"},
		{"docker unavailable", &fakeProvider{err: dockerc.ErrDockerUnavailable}, 502, "docker_unavailable"},
		{"list unavailable", &fakeProvider{client: &fakeDockerClient{listErr: dockerc.ErrDockerUnavailable}}, 502, "docker_unavailable"},
		{"stats unavailable", &fakeProvider{client: &fakeDockerClient{
			list:     []dockerc.Container{{ID: "a", Name: "x", State: "running"}},
			statsErr: dockerc.ErrDockerUnavailable}}, 502, "docker_unavailable"},
		{"other provider error", &fakeProvider{err: errors.New("/home/user/secret path")}, 500, "internal_error"},
		{"other list error", &fakeProvider{client: &fakeDockerClient{listErr: errors.New("/home/user/x")}}, 500, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, containersServer(&fakeGate{status: "running"}, tc.p), "GET", "/api/profiles/default/containers", "")
			wantErr(t, rec, tc.status, tc.code)
			if got := rec.Body.String(); len(got) > 0 && (strings.Contains(got, "/home/user") || strings.Contains(got, "secret")) {
				t.Fatalf("cause leaked: %s", got)
			}
		})
	}
}

func TestContainersNonDockerRuntime(t *testing.T) {
	rec := call(t, containersServer(&fakeGate{status: "running"}, &fakeProvider{err: dockerc.ErrRuntimeNotDocker}),
		"GET", "/api/profiles/default/containers", "")
	if rec.Code != 200 || rec.Body.String() != "{\"containers\":[]}\n" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestContainersGateErrors(t *testing.T) {
	p := &fakeProvider{err: errors.New("must not be called")}
	wantErr(t, call(t, containersServer(&fakeGate{err: ErrProfileNotFound}, p), "GET", "/api/profiles/nope/containers", ""), 404, "profile_not_found")
	wantErr(t, call(t, containersServer(&fakeGate{err: ErrInvalidProfileName}, p), "GET", "/api/profiles/bad/containers", ""), 404, "profile_not_found")
	wantErr(t, call(t, containersServer(&fakeGate{err: errors.New("boom")}, p), "GET", "/api/profiles/default/containers", ""), 502, "colima_failed")
}

func TestContainersNeedsToken(t *testing.T) {
	g := &fakeGate{status: "running"}
	req := httptest.NewRequest("GET", "/api/profiles/default/containers", nil)
	req.Host = goodHost
	rec := httptest.NewRecorder()
	containersServer(g, &fakeProvider{client: &fakeDockerClient{}}).ServeHTTP(rec, req)
	wantErr(t, rec, 401, "unauthorized")
	if len(g.names) != 0 {
		t.Fatal("gate reached without token")
	}
}

func TestContainersWaitHonoursCancel(t *testing.T) {
	old := cpuSampleWait
	cpuSampleWait = time.Hour
	t.Cleanup(func() { cpuSampleWait = old })
	fc := &fakeDockerClient{list: []dockerc.Container{{ID: "a", Name: "x", State: "running"}}}
	h := containersServer(&fakeGate{status: "running"}, &fakeProvider{client: fc})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/profiles/default/containers", nil).WithContext(ctx)
	req.Host = goodHost
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(rec, req); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return after cancel")
	}
	if rec.Code != 500 {
		t.Fatalf("status %d", rec.Code)
	}
}
