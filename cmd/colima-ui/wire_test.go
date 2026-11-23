package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/httpapi"
)

// fakeRunner stands in for the colima adapter. It never runs a binary.
type fakeRunner struct {
	mu      sync.Mutex
	list    string
	listErr error
	streams [][]string
	procs   []*fakeProc
}

func (f *fakeRunner) Run(_ context.Context, args []string) (colima.Result, error) {
	if f.listErr != nil {
		return colima.Result{}, f.listErr
	}
	return colima.Result{Stdout: []byte(f.list)}, nil
}

func (f *fakeRunner) Stream(_ context.Context, args []string) (colima.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streams = append(f.streams, append([]string(nil), args...))
	p := &fakeProc{lines: make(chan string), done: make(chan struct{}), cancelled: make(chan struct{})}
	f.procs = append(f.procs, p)
	return p, nil
}

func (f *fakeRunner) streamCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.streams)
}

type fakeProc struct {
	lines     chan string
	done      chan struct{}
	cancelled chan struct{}
	once      sync.Once
}

func (p *fakeProc) Lines() <-chan string { return p.lines }
func (p *fakeProc) Wait() (colima.Exit, error) {
	<-p.done
	return colima.Exit{ExitCode: -1, Killed: true}, nil
}
func (p *fakeProc) Cancel() {
	p.once.Do(func() { close(p.cancelled); close(p.lines); close(p.done) })
}

type fakeFiles struct {
	names []string
	err   error
}

func (f fakeFiles) ListProfiles() ([]string, error) { return f.names, f.err }

const listJSON = `{"name":"default","status":"Running","runtime":"docker"}
{"name":"dev","status":"Stopped","runtime":"docker"}
{"name":"odd","status":"Broken","runtime":"docker"}
`

func TestProfileGate(t *testing.T) {
	g := profileGate{runner: &fakeRunner{list: listJSON}, files: fakeFiles{names: []string{"fileonly"}}}
	ok := map[string]string{"default": "running", "dev": "stopped", "odd": "broken", "fileonly": "stopped"}
	for name, want := range ok {
		got, err := g.ProfileStatus(t.Context(), name)
		if err != nil || got != want {
			t.Fatalf("%s: %q %v, want %q", name, got, err, want)
		}
	}
	// default exists even with no VM and no file.
	empty := profileGate{runner: &fakeRunner{list: ""}, files: fakeFiles{}}
	if got, err := empty.ProfileStatus(t.Context(), "default"); err != nil || got != "stopped" {
		t.Fatalf("default on empty machine: %q %v", got, err)
	}
	if _, err := (profileGate{runner: &fakeRunner{}}).ProfileStatus(t.Context(), "default"); err != nil {
		t.Fatalf("nil files: %v", err)
	}
	for _, name := range []string{"ghost", "dev2", "Dev"} {
		if _, err := g.ProfileStatus(t.Context(), name); !errors.Is(err, httpapi.ErrProfileNotFound) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	bad := []string{"", "..", ".", "a..b", "a/b", `a\b`, "../x", "-x", "--help", "_templates", ".hidden", "a b", "a\x00b", "a\nb", "a;b", "$x", strings.Repeat("a", 65)}
	for _, name := range bad {
		if _, err := g.ProfileStatus(t.Context(), name); !errors.Is(err, httpapi.ErrInvalidProfileName) {
			t.Fatalf("%q: %v", name, err)
		}
	}
}

func TestProfileGateValidatesBeforeListing(t *testing.T) {
	// A runner that fails proves a bad name is rejected before colima runs.
	g := profileGate{runner: &fakeRunner{listErr: errors.New("must not run")}}
	if _, err := g.ProfileStatus(t.Context(), "../x"); !errors.Is(err, httpapi.ErrInvalidProfileName) {
		t.Fatalf("got %v", err)
	}
}

func TestProfileGateFailures(t *testing.T) {
	g := profileGate{runner: &fakeRunner{listErr: errors.New("boom")}}
	_, err := g.ProfileStatus(t.Context(), "dev")
	if err == nil || errors.Is(err, httpapi.ErrProfileNotFound) || errors.Is(err, httpapi.ErrInvalidProfileName) {
		t.Fatalf("list failure: %v", err)
	}
	g = profileGate{runner: &fakeRunner{}, files: fakeFiles{err: errors.New("boom")}}
	_, err = g.ProfileStatus(t.Context(), "dev")
	if err == nil || errors.Is(err, httpapi.ErrProfileNotFound) {
		t.Fatalf("files failure: %v", err)
	}
}

func testApp(t *testing.T, r *fakeRunner) *app {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "fileonly"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "fileonly", "colima.yaml"), []byte("cpu: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"COLIMA_HOME": home}
	a, err := newAppWith(r, func(k string) string { return env[k] }, func() (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func doReq(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAppStartFlow(t *testing.T) {
	r := &fakeRunner{list: listJSON}
	a := testApp(t, r)
	h := a.handler()

	// Unknown profile: 404 and colima never launched, so nothing is created.
	rec := doReq(t, h, "POST", "/api/profiles/ghost/start")
	if rec.Code != 404 || r.streamCount() != 0 {
		t.Fatalf("unknown: %d, streams %d", rec.Code, r.streamCount())
	}
	// Hostile name.
	rec = doReq(t, h, "POST", "/api/profiles/-rf/start")
	if rec.Code != 404 || r.streamCount() != 0 {
		t.Fatalf("hostile: %d", rec.Code)
	}
	// Running profile cannot be started.
	if rec := doReq(t, h, "POST", "/api/profiles/default/start"); rec.Code != 409 || !strings.Contains(rec.Body.String(), "invalid_state") {
		t.Fatalf("running: %d %s", rec.Code, rec.Body.String())
	}
	// File-only profile starts.
	rec = doReq(t, h, "POST", "/api/profiles/fileonly/start")
	if rec.Code != 202 || r.streamCount() != 1 {
		t.Fatalf("fileonly: %d %s streams %d", rec.Code, rec.Body.String(), r.streamCount())
	}
	// A second action on it is busy.
	if rec := doReq(t, h, "POST", "/api/profiles/fileonly/stop"); rec.Code != 409 || !strings.Contains(rec.Body.String(), "profile_busy") {
		t.Fatalf("busy: %d %s", rec.Code, rec.Body.String())
	}

	// Shutdown cancels the running job's process and returns.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := a.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.procs[0].cancelled:
	default:
		t.Fatal("running job not cancelled on shutdown")
	}
	// After shutdown a new action is a 503.
	if rec := doReq(t, h, "POST", "/api/profiles/dev/start"); rec.Code != 503 {
		t.Fatalf("after shutdown: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAppRejectsRelativeColimaHome(t *testing.T) {
	_, err := newAppWith(&fakeRunner{}, func(k string) string {
		if k == "COLIMA_HOME" {
			return "relative/dir"
		}
		return ""
	}, func() (string, error) { return "", nil })
	if err == nil {
		t.Fatal("relative COLIMA_HOME accepted")
	}
}

func TestAppConfigRoutesRequireTokenAndWork(t *testing.T) {
	r := &fakeRunner{list: listJSON}
	a := testApp(t, r)
	const tok = "synthetic-token"
	h := httpapi.NewHandler(8787, tok, a.handler(), nil)
	do := func(method, path, body, auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "127.0.0.1:8787"
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, m := range []string{"GET", "PUT"} {
		if rec := do(m, "/api/profiles/fileonly/config", `{"cpu":3}`, ""); rec.Code != 401 {
			t.Fatalf("%s without token: %d", m, rec.Code)
		}
		if rec := do(m, "/api/profiles/fileonly/config", `{"cpu":3}`, "Bearer wrong"); rec.Code != 401 {
			t.Fatalf("%s wrong token: %d", m, rec.Code)
		}
	}
	rec := do("GET", "/api/profiles/fileonly/config", "", "Bearer "+tok)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"cpu":2`) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	rec = do("PUT", "/api/profiles/fileonly/config", `{"cpu":3}`, "Bearer "+tok)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"cpu":3`) {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	// A running job on the profile makes the write a 409 and changes nothing.
	if rec := do("POST", "/api/profiles/fileonly/start", "", "Bearer "+tok); rec.Code != 202 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	rec = do("PUT", "/api/profiles/fileonly/config", `{"cpu":5}`, "Bearer "+tok)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "profile_busy") || !strings.Contains(rec.Body.String(), "jobId") {
		t.Fatalf("busy put: %d %s", rec.Code, rec.Body)
	}
	if rec := do("GET", "/api/profiles/fileonly/config", "", "Bearer "+tok); !strings.Contains(rec.Body.String(), `"cpu":3`) {
		t.Fatalf("file changed while busy: %s", rec.Body)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := a.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
