package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/operations"
)

type fakeMgr struct {
	mu        sync.Mutex
	calls     []string
	submitReq []operations.Request
	submit    func(operations.Request) (operations.JobSummary, error)
	get       func(string) (operations.JobSummary, error)
	cancel    func(context.Context, string) (operations.JobSummary, error)
	busy      func(string) (string, bool)
}

func (f *fakeMgr) rec(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeMgr) Submit(r operations.Request) (operations.JobSummary, error) {
	f.rec("submit")
	f.mu.Lock()
	f.submitReq = append(f.submitReq, r)
	f.mu.Unlock()
	if f.submit != nil {
		return f.submit(r)
	}
	return operations.JobSummary{ID: "j_abc123"}, nil
}

func (f *fakeMgr) Get(id string) (operations.JobSummary, error) {
	f.rec("get")
	return f.get(id)
}

func (f *fakeMgr) Cancel(ctx context.Context, id string) (operations.JobSummary, error) {
	f.rec("cancel")
	return f.cancel(ctx, id)
}

func (f *fakeMgr) Busy(p string) (string, bool) {
	f.rec("busy")
	if f.busy != nil {
		return f.busy(p)
	}
	return "", false
}

type fakeGate struct {
	mu     sync.Mutex
	names  []string
	status string
	err    error
	onCall func()
}

func (g *fakeGate) ProfileStatus(_ context.Context, name string) (string, error) {
	g.mu.Lock()
	g.names = append(g.names, name)
	g.mu.Unlock()
	if g.onCall != nil {
		g.onCall()
	}
	return g.status, g.err
}

func jobsServer(m JobsDeps, g ProfileGate) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithJobs(m, g)), nil)
}

func call(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = goodHost
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func wantErr(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) wireError {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d (%s)", rec.Code, status, rec.Body.String())
	}
	w := decodeErr(t, rec)
	if w.Error.Code != code {
		t.Fatalf("code %q, want %q", w.Error.Code, code)
	}
	return w
}

func TestJobRoutesNeedToken(t *testing.T) {
	m, g := &fakeMgr{}, &fakeGate{status: "stopped"}
	h := jobsServer(m, g)
	for _, rt := range [][2]string{
		{"POST", "/api/profiles/default/start"},
		{"POST", "/api/profiles/default/stop"},
		{"POST", "/api/profiles/default/restart"},
		{"GET", "/api/jobs/j_1"},
		{"POST", "/api/jobs/j_1/cancel"},
	} {
		req := httptest.NewRequest(rt[0], rt[1], nil)
		req.Host = goodHost
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		wantErr(t, rec, 401, "unauthorized")
		if rec.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Fatalf("%v: no WWW-Authenticate", rt)
		}
	}
	if len(m.calls) != 0 || len(g.names) != 0 {
		t.Fatalf("handlers ran without token: %v %v", m.calls, g.names)
	}
}

func TestActionAccepted(t *testing.T) {
	for _, c := range []struct {
		path   string
		action operations.Action
		status string
	}{
		{"start", operations.ActionStart, "stopped"},
		{"start", operations.ActionStart, "broken"},
		{"stop", operations.ActionStop, "running"},
		{"stop", operations.ActionStop, "broken"},
		{"restart", operations.ActionRestart, "running"},
		{"restart", operations.ActionRestart, "stopped"},
	} {
		t.Run(c.path+" from "+c.status, func(t *testing.T) {
			m, g := &fakeMgr{}, &fakeGate{status: c.status}
			rec := call(t, jobsServer(m, g), "POST", "/api/profiles/dev/"+c.path, "")
			if rec.Code != 202 {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if rec.Body.String() != `{"jobId":"j_abc123"}`+"\n" {
				t.Fatalf("body %q", rec.Body.String())
			}
			if len(m.submitReq) != 1 || !reflect.DeepEqual(m.submitReq[0], operations.Request{Action: c.action, Profile: "dev"}) {
				t.Fatalf("submit %+v", m.submitReq)
			}
		})
	}
}

func TestActionStatusIsCaseInsensitive(t *testing.T) {
	m, g := &fakeMgr{}, &fakeGate{status: "Running"}
	w := wantErr(t, call(t, jobsServer(m, g), "POST", "/api/profiles/dev/start", ""), 409, "invalid_state")
	if w.Error.Details["status"] != "running" {
		t.Fatalf("details %v", w.Error.Details)
	}
}

func TestActionBodies(t *testing.T) {
	for _, b := range []string{"", "{}", " {} \n"} {
		m, g := &fakeMgr{}, &fakeGate{status: "stopped"}
		if rec := call(t, jobsServer(m, g), "POST", "/api/profiles/dev/start", b); rec.Code != 202 {
			t.Fatalf("%q: %d", b, rec.Code)
		}
	}
	for _, b := range []string{`{"force":true}`, `[]`, `x`, strings.Repeat("a", 5000)} {
		m, g := &fakeMgr{}, &fakeGate{status: "stopped"}
		wantErr(t, call(t, jobsServer(m, g), "POST", "/api/profiles/dev/start", b), 400, "bad_request")
		if len(g.names) != 0 || len(m.calls) != 0 {
			t.Fatalf("body %.10q: gate or manager reached", b)
		}
	}
}

func TestActionInvalidState(t *testing.T) {
	for _, c := range []struct{ path, status string }{{"start", "running"}, {"stop", "stopped"}} {
		m, g := &fakeMgr{}, &fakeGate{status: c.status}
		w := wantErr(t, call(t, jobsServer(m, g), "POST", "/api/profiles/dev/"+c.path, ""), 409, "invalid_state")
		if w.Error.Details["status"] != c.status {
			t.Fatalf("details %v", w.Error.Details)
		}
		if len(m.submitReq) != 0 {
			t.Fatal("submit called on invalid state")
		}
	}
}

func TestBusyComesBeforeInvalidState(t *testing.T) {
	// A start is in flight: the list still says stopped, and a stop must get
	// profile_busy, not invalid_state.
	m := &fakeMgr{busy: func(string) (string, bool) { return "j_run", true }}
	g := &fakeGate{status: "stopped"}
	w := wantErr(t, call(t, jobsServer(m, g), "POST", "/api/profiles/dev/stop", ""), 409, "profile_busy")
	if w.Error.Details["jobId"] != "j_run" {
		t.Fatalf("details %v", w.Error.Details)
	}
	if len(m.submitReq) != 0 {
		t.Fatal("submit called while busy")
	}
}

func TestActionHandlerOrder(t *testing.T) {
	m := &fakeMgr{}
	g := &fakeGate{status: "stopped"}
	call(t, jobsServer(m, g), "POST", "/api/profiles/dev/start", "")
	m.mu.Lock()
	got := strings.Join(m.calls, ",")
	m.mu.Unlock()
	if got != "busy,submit" || len(g.names) != 1 {
		t.Fatalf("calls %q, gate %v", got, g.names)
	}
}

func TestGateRunsBeforeManager(t *testing.T) {
	for _, gerr := range []error{ErrInvalidProfileName, ErrProfileNotFound, errors.New("boom")} {
		m := &fakeMgr{}
		g := &fakeGate{err: gerr}
		call(t, jobsServer(m, g), "POST", "/api/profiles/dev/start", "")
		if len(m.calls) != 0 {
			t.Fatalf("manager reached after gate error %v: %v", gerr, m.calls)
		}
	}
}

func TestGateErrors(t *testing.T) {
	m := &fakeMgr{}
	// Unknown but valid name: echoed in details.
	w := wantErr(t, call(t, jobsServer(m, &fakeGate{err: ErrProfileNotFound}), "POST", "/api/profiles/ghost/start", ""), 404, "profile_not_found")
	if w.Error.Details["name"] != "ghost" {
		t.Fatalf("details %v", w.Error.Details)
	}
	// Wrapped sentinel still works.
	wrapped := fmt.Errorf("lookup: %w", ErrProfileNotFound)
	wantErr(t, call(t, jobsServer(m, &fakeGate{err: wrapped}), "POST", "/api/profiles/ghost/start", ""), 404, "profile_not_found")
	// Malformed: never echoed.
	rec := call(t, jobsServer(m, &fakeGate{err: ErrInvalidProfileName}), "POST", "/api/profiles/%3Cscript%3E/start", "")
	w = wantErr(t, rec, 404, "profile_not_found")
	if len(w.Error.Details) != 0 || strings.Contains(rec.Body.String(), "script") {
		t.Fatalf("malformed name echoed: %s", rec.Body.String())
	}
	// Colima failure: generic, real error not leaked.
	rec = call(t, jobsServer(m, &fakeGate{err: errors.New("exec /usr/local/bin/colima: boom")}), "POST", "/api/profiles/dev/start", "")
	wantErr(t, rec, 502, "colima_failed")
	if strings.Contains(rec.Body.String(), "boom") || strings.Contains(rec.Body.String(), "/usr") {
		t.Fatalf("leak: %s", rec.Body.String())
	}
}

// strictGate mimics the production gate's validation so the handler path is
// tested with hostile names end to end.
type strictGate struct{ called *[]string }

func (g strictGate) ProfileStatus(_ context.Context, name string) (string, error) {
	*g.called = append(*g.called, name)
	for _, r := range name {
		if !(r >= 'a' && r <= 'z') {
			return "", ErrInvalidProfileName
		}
	}
	if name == "" || name == "ghost" {
		return "", ErrProfileNotFound
	}
	return "stopped", nil
}

func TestHostileNamesNeverReachManager(t *testing.T) {
	for _, name := range []string{"..", "..%2F..%2Fetc", "%2e%2e", "-rf", "--help", "a%00b", "a%2Fb", "a%5Cb", "a%20b", "%3Bls", "x%0Ay", strings.Repeat("a", 500) + "%2F"} {
		var called []string
		m := &fakeMgr{}
		h := NewHandler(testPort, testToken, NewAPI(WithJobs(m, strictGate{&called})), nil)
		rec := call(t, h, "POST", "/api/profiles/"+name+"/start", "")
		if rec.Code == 202 || len(m.calls) != 0 {
			t.Fatalf("%q: status %d, manager calls %v", name, rec.Code, m.calls)
		}
		if strings.Contains(rec.Body.String(), "etc") || strings.Contains(rec.Body.String(), "help") {
			t.Fatalf("%q echoed: %s", name, rec.Body.String())
		}
	}
}

func TestNoProfileCreatedForUnknownName(t *testing.T) {
	var called []string
	m := &fakeMgr{}
	h := NewHandler(testPort, testToken, NewAPI(WithJobs(m, strictGate{&called})), nil)
	wantErr(t, call(t, h, "POST", "/api/profiles/ghost/start", ""), 404, "profile_not_found")
	if len(m.submitReq) != 0 {
		t.Fatalf("submit for unknown profile: %v", m.submitReq)
	}
}

func TestSubmitErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
		jobID  any
	}{
		{"busy with job", &operations.BusyError{Profile: "dev", JobID: "j_x"}, 409, "profile_busy", "j_x"},
		{"busy exclusive", &operations.BusyError{Profile: "dev"}, 409, "profile_busy", nil},
		{"wrapped busy", fmt.Errorf("w: %w", &operations.BusyError{Profile: "dev", JobID: "j_y"}), 409, "profile_busy", "j_y"},
		{"closed", operations.ErrClosed, 503, "shutting_down", nil},
		{"id exhausted", operations.ErrIDExhausted, 500, "internal_error", nil},
		{"launcher", errors.New("start /home/user/bin/colima: no such file"), 502, "colima_failed", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &fakeMgr{submit: func(operations.Request) (operations.JobSummary, error) { return operations.JobSummary{}, c.err }}
			rec := call(t, jobsServer(m, &fakeGate{status: "stopped"}), "POST", "/api/profiles/dev/start", "")
			w := wantErr(t, rec, c.status, c.code)
			if got := w.Error.Details["jobId"]; got != c.jobID {
				t.Fatalf("jobId %v, want %v (details %v)", got, c.jobID, w.Error.Details)
			}
			if strings.Contains(rec.Body.String(), "/home") {
				t.Fatalf("leak: %s", rec.Body.String())
			}
		})
	}
}

func TestBusyRaceAtSubmit(t *testing.T) {
	// Busy said free, but another request took the slot: Submit decides.
	m := &fakeMgr{submit: func(operations.Request) (operations.JobSummary, error) {
		return operations.JobSummary{}, &operations.BusyError{Profile: "dev", JobID: "j_win"}
	}}
	w := wantErr(t, call(t, jobsServer(m, &fakeGate{status: "stopped"}), "POST", "/api/profiles/dev/start", ""), 409, "profile_busy")
	if w.Error.Details["jobId"] != "j_win" {
		t.Fatalf("details %v", w.Error.Details)
	}
}

func TestActionMethodAndPaths(t *testing.T) {
	h := jobsServer(&fakeMgr{}, &fakeGate{status: "stopped"})
	for _, m := range []string{"GET", "PUT", "DELETE", "PATCH"} {
		rec := call(t, h, m, "/api/profiles/dev/start", "")
		wantErr(t, rec, 405, "method_not_allowed")
		if !strings.Contains(rec.Header().Get("Allow"), "POST") {
			t.Fatalf("Allow %q", rec.Header().Get("Allow"))
		}
	}
	wantErr(t, call(t, h, "POST", "/api/profiles/dev/explode", ""), 404, "not_found")
	// ServeMux redirects unclean paths before the router sees them.
	if rec := call(t, h, "POST", "/api/profiles//start", ""); rec.Code == 202 {
		t.Fatalf("empty name accepted: %d", rec.Code)
	}
}

func fixedJob() operations.JobSummary {
	fin := time.Date(2026, 11, 23, 1, 10, 42, 0, time.UTC)
	code := 1
	return operations.JobSummary{
		ID: "j_8f3a2c", Profile: "default", Action: operations.ActionStart, State: operations.StateFailed,
		StartedAt:  time.Date(2026, 11, 23, 3, 10, 0, 0, time.FixedZone("x", 2*3600)).UTC(),
		FinishedAt: &fin, ExitCode: &code,
		Output:    []operations.Line{{Seq: 3, Text: "\x1b[32mhello\x1b[0m"}, {Seq: 4, Text: "next"}},
		Truncated: true, DroppedLines: 2,
		Tail: []string{"/home/user/.colima/secret-tail"},
	}
}

func TestGetJob(t *testing.T) {
	m := &fakeMgr{get: func(id string) (operations.JobSummary, error) {
		if id != "j_8f3a2c" {
			t.Errorf("id %q", id)
		}
		return fixedJob(), nil
	}}
	rec := call(t, jobsServer(m, &fakeGate{}), "GET", "/api/jobs/j_8f3a2c", "")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"id": "j_8f3a2c", "profile": "default", "action": "start", "state": "failed",
		"startedAt": "2026-11-23T01:10:00Z", "finishedAt": "2026-11-23T01:10:42Z", "exitCode": float64(1),
		"truncated": true, "droppedLines": float64(2),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	out := got["output"].([]any)
	if len(out) != 2 || out[0].(map[string]any)["seq"] != float64(3) || out[0].(map[string]any)["line"] != "\x1b[32mhello\x1b[0m" {
		t.Fatalf("output %v", out)
	}
	if len(got) != 10 {
		t.Fatalf("unexpected keys: %v", got)
	}
	if strings.Contains(rec.Body.String(), "secret-tail") || strings.Contains(strings.ToLower(rec.Body.String()), "tail") {
		t.Fatalf("Tail leaked: %s", rec.Body.String())
	}
}

func TestRunningJobHasNulls(t *testing.T) {
	m := &fakeMgr{get: func(string) (operations.JobSummary, error) {
		return operations.JobSummary{ID: "j_1", State: operations.StateRunning, StartedAt: time.Unix(0, 0)}, nil
	}}
	rec := call(t, jobsServer(m, &fakeGate{}), "GET", "/api/jobs/j_1", "")
	body := rec.Body.String()
	for _, want := range []string{`"finishedAt":null`, `"exitCode":null`, `"output":[]`, `"startedAt":"1970-01-01T00:00:00Z"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	if strings.Contains(body, `"output":null`) {
		t.Fatal("output encoded as null")
	}
}

func TestGetJobErrors(t *testing.T) {
	m := &fakeMgr{get: func(string) (operations.JobSummary, error) { return operations.JobSummary{}, operations.ErrJobNotFound }}
	h := jobsServer(m, &fakeGate{})
	w := wantErr(t, call(t, h, "GET", "/api/jobs/j_nope", ""), 404, "job_not_found")
	if w.Error.Details["jobId"] != "j_nope" {
		t.Fatalf("details %v", w.Error.Details)
	}
	m.calls = nil
	for _, id := range []string{"a%2Fb", "%3Cb%3E", "a.b", "a%20b", strings.Repeat("a", 65), "%00"} {
		rec := call(t, h, "GET", "/api/jobs/"+id, "")
		w := wantErr(t, rec, 404, "job_not_found")
		if len(w.Error.Details) != 0 {
			t.Fatalf("%q echoed: %v", id, w.Error.Details)
		}
	}
	if len(m.calls) != 0 {
		t.Fatalf("manager reached for bad ids: %v", m.calls)
	}
	// 64 characters is the limit and valid.
	call(t, h, "GET", "/api/jobs/"+strings.Repeat("a", 64), "")
	if len(m.calls) != 1 {
		t.Fatal("64 char id rejected")
	}
	// Unexpected manager error is a generic 500.
	m.get = func(string) (operations.JobSummary, error) {
		return operations.JobSummary{}, errors.New("disk /home/user")
	}
	rec := call(t, h, "GET", "/api/jobs/j_1", "")
	wantErr(t, rec, 500, "internal_error")
	if strings.Contains(rec.Body.String(), "/home") {
		t.Fatal("leak")
	}
	wantErr(t, call(t, h, "POST", "/api/jobs/j_1", ""), 405, "method_not_allowed")
}

func cancelled() operations.JobSummary {
	j := fixedJob()
	j.State = operations.StateCancelled
	j.ExitCode = nil
	return j
}

func TestCancel(t *testing.T) {
	m := &fakeMgr{cancel: func(ctx context.Context, id string) (operations.JobSummary, error) {
		if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > 10*time.Second {
			t.Errorf("no 10s deadline on cancel ctx")
		}
		return cancelled(), nil
	}}
	rec := call(t, jobsServer(m, &fakeGate{}), "POST", "/api/jobs/j_1/cancel", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"cancelled"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret-tail") {
		t.Fatal("Tail leaked")
	}
}

func TestCancelErrors(t *testing.T) {
	cases := []struct {
		name   string
		sum    operations.JobSummary
		err    error
		status int
		code   string
		state  any
	}{
		{"unknown", operations.JobSummary{}, operations.ErrJobNotFound, 404, "job_not_found", nil},
		{"finished", operations.JobSummary{}, &operations.NotRunningError{JobID: "j_1", State: operations.StateSucceeded}, 409, "job_not_running", "succeeded"},
		{"race with clean exit", func() operations.JobSummary { j := fixedJob(); j.State = operations.StateSucceeded; return j }(), nil, 409, "job_not_running", "succeeded"},
		{"race with failure", func() operations.JobSummary { j := fixedJob(); j.State = operations.StateFailed; return j }(), nil, 409, "job_not_running", "failed"},
		{"deadline", operations.JobSummary{}, context.DeadlineExceeded, 500, "internal_error", nil},
		{"wrapped deadline", operations.JobSummary{}, fmt.Errorf("x: %w", context.DeadlineExceeded), 500, "internal_error", nil},
		{"other", operations.JobSummary{}, errors.New("weird /home/user"), 500, "internal_error", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &fakeMgr{cancel: func(context.Context, string) (operations.JobSummary, error) { return c.sum, c.err }}
			rec := call(t, jobsServer(m, &fakeGate{}), "POST", "/api/jobs/j_1/cancel", "")
			w := wantErr(t, rec, c.status, c.code)
			if got := w.Error.Details["state"]; got != c.state {
				t.Fatalf("state %v, want %v", got, c.state)
			}
			if c.status == 409 && w.Error.Details["jobId"] != "j_1" {
				t.Fatalf("details %v", w.Error.Details)
			}
			if strings.Contains(rec.Body.String(), "/home") {
				t.Fatal("leak")
			}
		})
	}
}

func TestCancelBadIDNeverReachesManager(t *testing.T) {
	m := &fakeMgr{}
	h := jobsServer(m, &fakeGate{})
	for _, id := range []string{"a%2Fb", "%3Cx%3E", strings.Repeat("z", 65)} {
		wantErr(t, call(t, h, "POST", "/api/jobs/"+id+"/cancel", ""), 404, "job_not_found")
	}
	if len(m.calls) != 0 {
		t.Fatalf("manager reached: %v", m.calls)
	}
}

func blockingCancel(ctx context.Context, _ string) (operations.JobSummary, error) {
	<-ctx.Done()
	return operations.JobSummary{}, ctx.Err()
}

func TestCancelHandlerDeadlineIs500(t *testing.T) {
	old := cancelTimeout
	cancelTimeout = 30 * time.Millisecond
	defer func() { cancelTimeout = old }()
	// A manager stuck like a blocked observer: the handler's own deadline fires.
	m := &fakeMgr{cancel: blockingCancel}
	rec := call(t, jobsServer(m, &fakeGate{}), "POST", "/api/jobs/j_1/cancel", "")
	wantErr(t, rec, 500, "internal_error")
}

func TestCancelClientDisconnectWritesNothing(t *testing.T) {
	m := &fakeMgr{cancel: blockingCancel}
	ctx, stop := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/api/jobs/j_1/cancel", nil).WithContext(ctx)
	req.Host = goodHost
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { jobsServer(m, &fakeGate{}).ServeHTTP(rec, req); close(done) }()
	time.Sleep(20 * time.Millisecond)
	stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after client disconnect")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body written to a gone client: %q", rec.Body.String())
	}
}

func TestGateDeadlineIs502(t *testing.T) {
	old := gateTimeout
	gateTimeout = 30 * time.Millisecond
	defer func() { gateTimeout = old }()
	slow := slowGate{}
	m := &fakeMgr{}
	wantErr(t, call(t, jobsServer(m, slow), "POST", "/api/profiles/dev/start", ""), 502, "colima_failed")
	if len(m.calls) != 0 {
		t.Fatal("manager reached after gate timeout")
	}
}

type slowGate struct{}

func (slowGate) ProfileStatus(ctx context.Context, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func TestShutdownBehaviour(t *testing.T) {
	// Real manager: after Shutdown, Submit gives ErrClosed and the route 503.
	mgr := operations.New(operations.Options{Launcher: failingLauncher{}})
	if err := mgr.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec := call(t, jobsServer(mgr, &fakeGate{status: "stopped"}), "POST", "/api/profiles/dev/start", "")
	wantErr(t, rec, 503, "shutting_down")
}

func TestCancelRejectsBody(t *testing.T) {
	m := &fakeMgr{cancel: func(context.Context, string) (operations.JobSummary, error) { return cancelled(), nil }}
	h := jobsServer(m, &fakeGate{})
	for _, b := range []string{`{"force":true}`, `x`} {
		wantErr(t, call(t, h, "POST", "/api/jobs/j_1/cancel", b), 400, "bad_request")
	}
	if len(m.calls) != 0 {
		t.Fatalf("manager reached: %v", m.calls)
	}
	for _, b := range []string{"", "{}"} {
		if rec := call(t, h, "POST", "/api/jobs/j_1/cancel", b); rec.Code != 200 {
			t.Fatalf("%q: %d", b, rec.Code)
		}
	}
}
