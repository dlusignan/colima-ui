package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/events"
	"github.com/dlusignan/colima-ui/internal/httpapi"
	"github.com/dlusignan/colima-ui/internal/store"
)

// fakeRunner stands in for the colima adapter. It never runs a binary.
type fakeRunner struct {
	mu      sync.Mutex
	list    string
	listErr error
	streams [][]string
	procs   []*fakeProc
	runs    int
	// script, when set, makes Stream return a process that emits these lines
	// and then exits with exit (instead of waiting to be cancelled).
	script []string
	exit   int
	// hold, when non-nil, keeps a scripted process alive after its lines until
	// the channel is closed.
	hold chan struct{}
}

func (f *fakeRunner) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs
}

func (f *fakeRunner) Run(_ context.Context, args []string) (colima.Result, error) {
	f.mu.Lock()
	f.runs++
	f.mu.Unlock()
	if f.listErr != nil {
		return colima.Result{}, f.listErr
	}
	return colima.Result{Stdout: []byte(f.list)}, nil
}

func (f *fakeRunner) Stream(_ context.Context, args []string) (colima.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streams = append(f.streams, append([]string(nil), args...))
	if f.script != nil {
		return newScriptProc(f.script, f.exit, f.hold), nil
	}
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

// scriptProc emits its lines, optionally waits for hold, then exits normally.
type scriptProc struct {
	lines  chan string
	exit   int
	done   chan struct{} // closed by Cancel
	ended  chan struct{} // closed when the script ran to its end
	cancel sync.Once
}

func newScriptProc(lines []string, exit int, hold chan struct{}) *scriptProc {
	p := &scriptProc{lines: make(chan string), exit: exit, done: make(chan struct{}), ended: make(chan struct{})}
	go func() {
		defer close(p.ended)
		defer close(p.lines)
		for _, l := range lines {
			select {
			case p.lines <- l:
			case <-p.done:
				return
			}
		}
		if hold != nil {
			select {
			case <-hold:
			case <-p.done:
			}
		}
	}()
	return p
}

func (p *scriptProc) Lines() <-chan string { return p.lines }
func (p *scriptProc) Wait() (colima.Exit, error) {
	select {
	case <-p.done:
		return colima.Exit{ExitCode: -1, Killed: true}, nil
	case <-p.ended:
	}
	select { // a cancel that raced the end of the script still wins
	case <-p.done:
		return colima.Exit{ExitCode: -1, Killed: true}, nil
	default:
	}
	return colima.Exit{ExitCode: p.exit}, nil
}
func (p *scriptProc) Cancel() { p.cancel.Do(func() { close(p.done) }) }

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

// testApp builds an app over a fake runner and a history database under
// t.TempDir(). The app is shut down when the test ends. Background loops are
// not started (newAppWith never starts them).
func testApp(t *testing.T, r *fakeRunner) *app {
	t.Helper()
	a, _ := testAppAt(t, r)
	return a
}

// testAppAt is testApp that also returns the history database path.
func testAppAt(t *testing.T, r *fakeRunner) (*app, string) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "fileonly"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "fileonly", "colima.yaml"), []byte("cpu: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := t.TempDir()
	env := map[string]string{"COLIMA_HOME": home}
	a, err := newAppWith(r, func(k string) string { return env[k] },
		func() (string, error) { return "", nil }, func() (string, error) { return cfg, nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.shutdown(ctx)
	})
	return a, filepath.Join(cfg, "colima-ui", "history.db")
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
	}, func() (string, error) { return "", nil }, func() (string, error) { return t.TempDir(), nil })
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
	// Another spelling of the name must not slip past the busy check.
	for _, n := range []string{"Fileonly", "FILEONLY"} {
		rec = do("PUT", "/api/profiles/"+n+"/config", `{"cpu":7}`, "Bearer "+tok)
		if rec.Code != 404 && rec.Code != 409 {
			t.Fatalf("spelling %s: %d %s", n, rec.Code, rec.Body)
		}
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

// ---- wiring tests: stream, history, sampler and shutdown order ----

// ownGoroutines counts goroutines that are inside one of the long-lived
// backend packages. A leak after shutdown shows up as a higher count.
func ownGoroutines() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "runtime.Stack") {
			continue
		}
		for _, pkg := range []string{"internal/store.", "internal/actionlog.", "internal/sampler.", "internal/operations.", "internal/events."} {
			if strings.Contains(g, "colima-ui/"+pkg) {
				n++
				break
			}
		}
	}
	return n
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertNoLeak(t *testing.T, baseline int) {
	t.Helper()
	eventually(t, "backend goroutines to exit", func() bool { return ownGoroutines() <= baseline })
}

type testServer struct {
	base  string
	token string
	srv   *httpapi.Server
	errCh chan error
}

// serve runs the full handler (host guard, token guard, api) on a loopback
// port, exactly as main does.
func serve(t *testing.T, a *app) *testServer {
	t.Helper()
	ts := &testServer{token: "synthetic-token", errCh: make(chan error, 1)}
	ts.srv = httpapi.NewServer(ts.token, a.handler(), nil)
	ln, err := ts.srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ts.base = "http://" + ln.Addr().String()
	go func() { ts.errCh <- ts.srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ts.srv.Shutdown(ctx)
	})
	return ts
}

func (ts *testServer) do(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, ts.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

type sseEvent struct {
	name string
	data map[string]any
}

// openStream connects to /api/events and returns once the server has
// subscribed the client (the handler flushes its headers only then). Events
// arrive on the channel, which closes when the stream ends.
func (ts *testServer) openStream(t *testing.T, query string) (<-chan sseEvent, *http.Response) {
	t.Helper()
	req, err := http.NewRequest("GET", ts.base+"/api/events"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	t.Cleanup(func() { resp.Body.Close() })
	out := make(chan sseEvent, 256)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		var name string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				var m map[string]any
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m) == nil {
					out <- sseEvent{name: name, data: m}
				}
			}
		}
	}()
	return out, resp
}

func nextJobEvent(t *testing.T, ch <-chan sseEvent) sseEvent {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				t.Fatal("stream ended")
			}
			if strings.HasPrefix(e.name, "job.") {
				return e
			}
		case <-timeout:
			t.Fatal("timed out waiting for a job event")
		}
	}
}

type actionRow struct{ Action, Outcome string }

func (ts *testServer) actions(t *testing.T, profile string) []actionRow {
	t.Helper()
	code, body := ts.do(t, "GET", "/api/profiles/"+profile+"/actions", "")
	if code != 200 {
		t.Fatalf("actions: %d %s", code, body)
	}
	var v struct{ Entries []actionRow }
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	return v.Entries
}

func TestAppEndToEndStreamAndHistory(t *testing.T) {
	baseline := ownGoroutines()
	r := &fakeRunner{list: listJSON, script: []string{"one", "two", "three"}, exit: 0}
	a, dbPath := testAppAt(t, r)
	ts := serve(t, a)

	// The stream sits behind the token, like every /api route.
	req, _ := http.NewRequest("GET", ts.base+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("stream without token: %d", resp.StatusCode)
	}

	ch, _ := ts.openStream(t, "?profile=fileonly")

	if code, body := ts.do(t, "POST", "/api/profiles/fileonly/start", ""); code != 202 {
		t.Fatalf("start: %d %s", code, body)
	}
	var jobID string
	for i, want := range []string{"one", "two", "three"} {
		e := nextJobEvent(t, ch)
		if e.name != "job.output" || e.data["line"] != want || e.data["seq"] != float64(i+1) || e.data["profile"] != "fileonly" {
			t.Fatalf("output %d: %#v", i, e)
		}
		id, _ := e.data["jobId"].(string)
		if id == "" || (jobID != "" && id != jobID) {
			t.Fatalf("job id %q vs %q", id, jobID)
		}
		jobID = id
	}
	fin := nextJobEvent(t, ch)
	if fin.name != "job.finished" || fin.data["state"] != "succeeded" || fin.data["exitCode"] != float64(0) || fin.data["jobId"] != jobID {
		t.Fatalf("finished: %#v", fin)
	}

	// The same job is one action row.
	eventually(t, "action row", func() bool { return len(ts.actions(t, "fileonly")) == 1 })
	if rows := ts.actions(t, "fileonly"); rows[0].Action != "start" || rows[0].Outcome != "success" {
		t.Fatalf("rows: %#v", rows)
	}

	// A second, failing job: one more finished event, one more row.
	r.mu.Lock()
	r.script, r.exit = []string{"boom"}, 2
	r.mu.Unlock()
	if code, body := ts.do(t, "POST", "/api/profiles/fileonly/start", ""); code != 202 {
		t.Fatalf("second start: %d %s", code, body)
	}
	if e := nextJobEvent(t, ch); e.name != "job.output" || e.data["line"] != "boom" {
		t.Fatalf("second output: %#v", e)
	}
	fin = nextJobEvent(t, ch)
	if fin.name != "job.finished" || fin.data["state"] != "failed" || fin.data["exitCode"] != float64(2) {
		t.Fatalf("second finished: %#v", fin)
	}
	eventually(t, "second action row", func() bool { return len(ts.actions(t, "fileonly")) == 2 })
	if rows := ts.actions(t, "fileonly"); rows[0].Outcome != "failed" || rows[1].Outcome != "success" {
		t.Fatalf("rows: %#v", rows)
	}
	// Exactly one finished event per job: nothing more arrives.
	select {
	case e := <-ch:
		t.Fatalf("unexpected extra event %#v", e)
	case <-time.After(150 * time.Millisecond):
	}

	// A config write lands in the same log.
	if code, body := ts.do(t, "PUT", "/api/profiles/fileonly/config", `{"cpu":3}`); code != 200 {
		t.Fatalf("config: %d %s", code, body)
	}
	eventually(t, "config row", func() bool {
		rows := ts.actions(t, "fileonly")
		return len(rows) == 3 && rows[0].Action == "config_change"
	})

	// The metrics route answers (no samples were taken: the sampler is not started).
	if code, body := ts.do(t, "GET", "/api/profiles/fileonly/metrics", ""); code != 200 || !strings.Contains(body, `"points":[]`) {
		t.Fatalf("metrics: %d %s", code, body)
	}
	if code, _ := ts.do(t, "GET", "/api/profiles/ghost/metrics", ""); code != 404 {
		t.Fatalf("metrics ghost: %d", code)
	}
	if code, _ := ts.do(t, "GET", "/api/profiles/ghost/actions", ""); code != 404 {
		t.Fatalf("actions ghost: %d", code)
	}

	// The history folder is private to the user.
	st, err := os.Stat(filepath.Dir(dbPath))
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("data dir: %v %v", st, err)
	}
	if st, err := os.Stat(dbPath); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("db file: %v %v", st, err)
	}

	a.closeStreams()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := ts.srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	assertNoLeak(t, baseline)
}

// historyAt reopens the database after shutdown and returns the rows of a
// profile, newest first.
func historyAt(t *testing.T, dbPath, profile string) []store.ActionEntry {
	t.Helper()
	s, err := store.Open(t.Context(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.RecentActions(t.Context(), profile, 50)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// A job that finishes right before shutdown still gets its row: the manager
// drains before the recorder closes, and the store closes last.
func TestShutdownFlushesLastActionRow(t *testing.T) {
	baseline := ownGoroutines()
	r := &fakeRunner{list: listJSON, script: []string{"x"}, exit: 0}
	a, dbPath := testAppAt(t, r)
	h := a.handler()
	sub, err := a.hub.Subscribe("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if rec := doReq(t, h, "POST", "/api/profiles/fileonly/start"); rec.Code != 202 {
		t.Fatalf("start: %d", rec.Code)
	}
	// Wait for the hub event, which the hub observer sends after the recorder
	// has queued its row, then shut down at once.
	waitFinished(t, sub)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := a.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if n := a.rec.Dropped(); n != 0 {
		t.Fatalf("dropped %d rows", n)
	}
	rows := historyAt(t, dbPath, "fileonly")
	if len(rows) != 1 || rows[0].Action != "start" || rows[0].Outcome != "success" {
		t.Fatalf("rows: %#v", rows)
	}
	assertNoLeak(t, baseline)
}

func waitFinished(t *testing.T, sub *events.Subscription) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case f := <-sub.C:
			if strings.Contains(string(f), "event: job.finished") {
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for job.finished")
		}
	}
}

// A job still running at shutdown is cancelled and its row is written before
// the store closes.
func TestShutdownRecordsCancelledJob(t *testing.T) {
	baseline := ownGoroutines()
	hold := make(chan struct{})
	defer close(hold)
	r := &fakeRunner{list: listJSON, script: []string{"x"}, hold: hold}
	a, dbPath := testAppAt(t, r)
	if rec := doReq(t, a.handler(), "POST", "/api/profiles/fileonly/start"); rec.Code != 202 {
		t.Fatalf("start: %d", rec.Code)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := a.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	rows := historyAt(t, dbPath, "fileonly")
	if len(rows) != 1 || rows[0].Outcome != "cancelled" {
		t.Fatalf("rows: %#v", rows)
	}
	assertNoLeak(t, baseline)
}

// Hub.Shutdown must run before http.Server.Shutdown: an open stream otherwise
// holds the HTTP shutdown until its timeout.
func TestCloseStreamsBeforeServerShutdown(t *testing.T) {
	a := testApp(t, &fakeRunner{list: listJSON})
	ts := serve(t, a)
	ch, _ := ts.openStream(t, "")

	// Without closeStreams the stream holds Shutdown past a short budget.
	short, cancelShort := context.WithTimeout(t.Context(), 300*time.Millisecond)
	err := ts.srv.Shutdown(short)
	cancelShort()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown with an open stream returned %v, want deadline exceeded", err)
	}

	start := time.Now()
	a.closeStreams()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := ts.srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown after closeStreams: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("shutdown took %s", d)
	}
	select {
	case _, ok := <-ch:
		if ok {
			// A pending frame is fine; the channel must still close.
			for range ch {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end")
	}
	// New streams are refused while shutting down.
	if rec := doReq(t, a.handler(), "GET", "/api/events"); rec.Code != 503 {
		t.Fatalf("stream after shutdown: %d", rec.Code)
	}
}

// newAppWith starts nothing; start launches the sampler, which lists the
// profiles at once, and shutdown stops it without a leaked goroutine.
func TestSamplerStartsOnlyOnStart(t *testing.T) {
	baseline := ownGoroutines()
	r := &fakeRunner{list: listJSON}
	a := testApp(t, r)
	time.Sleep(100 * time.Millisecond)
	if n := r.runCount(); n != 0 {
		t.Fatalf("colima list ran %d times before start", n)
	}
	if err := a.start(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the first sampler tick", func() bool { return r.runCount() > 0 })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := a.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	n := r.runCount()
	time.Sleep(100 * time.Millisecond)
	if r.runCount() != n {
		t.Fatal("sampler kept running after shutdown")
	}
	assertNoLeak(t, baseline)
}

// A job finishing asks the sampler to refresh its profile list.
func TestJobFinishedRefreshesSampler(t *testing.T) {
	r := &fakeRunner{list: listJSON, script: []string{"x"}}
	a := testApp(t, r)
	if rec := doReq(t, a.handler(), "POST", "/api/profiles/fileonly/start"); rec.Code != 202 {
		t.Fatalf("start: %d", rec.Code)
	}
	// The sampler is not started, so the request stays queued: a second
	// Refresh finds the buffer full, which is the non-blocking behaviour.
	done := make(chan struct{})
	go func() { a.sampler.Refresh(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Refresh blocked")
	}
}

func TestStoreOpenFailureIsFatal(t *testing.T) {
	baseline := ownGoroutines()
	newWith := func(dataDir string) error {
		env := map[string]string{"COLIMA_HOME": t.TempDir(), "COLIMA_UI_DATA_DIR": dataDir}
		_, err := newAppWith(&fakeRunner{}, func(k string) string { return env[k] },
			func() (string, error) { return "", nil },
			func() (string, error) { return "", errors.New("must not be asked") })
		return err
	}

	// A schema newer than this binary.
	dir := filepath.Join(t.TempDir(), "data")
	s, err := store.Open(t.Context(), store.Options{Path: filepath.Join(dir, store.FileName)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	err = newWith(dir)
	var tooNew store.ErrSchemaTooNew
	if !errors.As(err, &tooNew) || !strings.Contains(err.Error(), "can be deleted") {
		t.Fatalf("too new: %v", err)
	}

	// A data folder that cannot be created (its parent is a file).
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := newWith(filepath.Join(file, "sub")); err == nil || !strings.Contains(err.Error(), "history database") {
		t.Fatalf("unwritable: %v", err)
	}
	// A relative data folder is refused before anything opens.
	if err := newWith("relative/dir"); err == nil || !strings.Contains(err.Error(), "history database") {
		t.Fatalf("relative: %v", err)
	}
	assertNoLeak(t, baseline)
}

// A failure after the store opened (here a bad COLIMA_HOME) closes it again.
func TestFailedStartReleasesStore(t *testing.T) {
	baseline := ownGoroutines()
	cfg := t.TempDir()
	_, err := newAppWith(&fakeRunner{}, func(k string) string {
		if k == "COLIMA_HOME" {
			return "relative/dir"
		}
		return ""
	}, func() (string, error) { return "", nil }, func() (string, error) { return cfg, nil })
	if err == nil {
		t.Fatal("expected an error")
	}
	assertNoLeak(t, baseline)
	// The file is closed and reusable: opening it again succeeds at once.
	s, err := store.Open(t.Context(), store.Options{Path: filepath.Join(cfg, "colima-ui", store.FileName)})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

// Shutdown twice is harmless (the test cleanup runs it again).
func TestShutdownIdempotent(t *testing.T) {
	a := testApp(t, &fakeRunner{list: listJSON})
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		err := a.shutdown(ctx)
		cancel()
		if err != nil {
			t.Fatalf("shutdown %d: %v", i, err)
		}
	}
}
