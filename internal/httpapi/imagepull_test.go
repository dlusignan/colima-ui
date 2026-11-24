package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/actionlog"
	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/dockerc/dockerctest"
	"github.com/dlusignan/colima-ui/internal/operations"
)

// instantLauncher starts a colima "process" that ends at once with exit 0.
type instantLauncher struct{}

type instantProc struct{ ch chan string }

func (instantLauncher) Launch(context.Context, operations.Request) (colima.Process, error) {
	p := &instantProc{ch: make(chan string)}
	close(p.ch)
	return p, nil
}
func (p *instantProc) Lines() <-chan string       { return p.ch }
func (p *instantProc) Wait() (colima.Exit, error) { return colima.Exit{}, nil }
func (p *instantProc) Cancel()                    {}

type pullEnv struct {
	gate     *fakeGate
	busy     *fakeBusy
	fake     *dockerctest.Fake
	provider *dockerctest.Provider
	audit    *recordingAudit
	rec      *actionlog.Recorder
	mgr      *operations.Manager
	h        http.Handler
}

func newPullEnv(t *testing.T) *pullEnv {
	t.Helper()
	base := runtime.NumGoroutine()
	e := &pullEnv{gate: &fakeGate{status: "Running"}, busy: &fakeBusy{}, fake: &dockerctest.Fake{}, audit: &recordingAudit{}}
	e.provider = &dockerctest.Provider{Client: e.fake}
	e.rec = actionlog.New(e.audit)
	e.mgr = operations.New(operations.Options{Launcher: instantLauncher{}, Observers: []operations.Observer{e.rec}})
	e.h = NewHandler(testPort, testToken, NewAPI(WithJobs(e.mgr, e.gate), WithImagePull(e.gate, e.provider, e.busy, e.mgr)), nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.mgr.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		if err := e.rec.Close(ctx); err != nil {
			t.Errorf("recorder: %v", err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if n := runtime.NumGoroutine(); n > base {
			buf := make([]byte, 1<<16)
			t.Errorf("goroutine leak: %d now, %d before\n%s", n, base, buf[:runtime.Stack(buf, true)])
		}
	})
	return e
}

const pullPath = "/api/profiles/synthetic-a/images/pull"

func (e *pullEnv) post(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, e.h, "POST", pullPath, body)
}

func (e *pullEnv) job(t *testing.T, id string) map[string]any {
	t.Helper()
	rec := call(t, e.h, "GET", "/api/jobs/"+id, "")
	if rec.Code != 200 {
		t.Fatalf("GET job: %d %s", rec.Code, rec.Body.String())
	}
	return decodeMap(t, rec)
}

func (e *pullEnv) waitState(t *testing.T, id, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		j := e.job(t, id)
		if j["state"] == want {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s is %v, want %s", id, j["state"], want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func outputLines(j map[string]any) []string {
	var out []string
	for _, l := range j["output"].([]any) {
		out = append(out, l.(map[string]any)["line"].(string))
	}
	return out
}

func (e *pullEnv) accepted(t *testing.T, body string) string {
	t.Helper()
	rec := e.post(t, body)
	if rec.Code != 202 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	return decodeMap(t, rec)["jobId"].(string)
}

func (e *pullEnv) untouched() bool {
	return len(e.fake.Calls()) == 0 && len(e.provider.Profiles()) == 0 && len(e.audit.Rows()) == 0
}

// blocking returns a PullFunc that signals started and waits for ctx.
func blocking(started chan<- struct{}) func(context.Context, func(string)) error {
	return func(ctx context.Context, emit func(string)) error {
		emit("layer one")
		started <- struct{}{}
		<-ctx.Done()
		return fmt.Errorf("dockerc: %w", ctx.Err())
	}
}

func (e *pullEnv) auditRows(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for len(e.audit.Rows()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("audit rows = %d, want %d", len(e.audit.Rows()), n)
		}
		time.Sleep(2 * time.Millisecond)
	}
	var out []string
	for _, r := range e.audit.Rows() {
		d := "<nil>"
		if r.Detail != nil {
			d = *r.Detail
		}
		out = append(out, r.Action+"|"+r.Outcome+"|"+d+"|"+r.Profile)
	}
	return out
}

func TestImagePullAcceptsAndRunsAsAJob(t *testing.T) {
	e := newPullEnv(t)
	e.fake.PullLines = []string{"Pulling from library/postgres", "aaaaaaaaaaaa: waiting", "Status: downloaded newer image"}
	rec := e.post(t, `{"reference":"postgres:18-alpine"}`)
	if rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	got := decodeMap(t, rec)
	id, _ := got["jobId"].(string)
	if len(got) != 4 || id == "" || got["action"] != "image-pull" || got["profile"] != "synthetic-a" || got["state"] != "running" {
		t.Fatalf("answer = %v", got)
	}
	j := e.waitState(t, id, "succeeded")
	want := "Pulling postgres:18-alpine|Pulling from library/postgres|aaaaaaaaaaaa: waiting|Status: downloaded newer image"
	if strings.Join(outputLines(j), "|") != want {
		t.Fatalf("output = %q", outputLines(j))
	}
	if j["action"] != "image-pull" || j["exitCode"].(float64) != 0 {
		t.Fatalf("job = %v", j)
	}
	if c := e.fake.CallsTo("PullImage"); len(c) != 1 || c[0].Args[0] != "postgres:18-alpine" {
		t.Fatalf("calls = %+v", c)
	}
	if rows := e.auditRows(t, 1); rows[0] != "docker.image.pull|success|postgres:18-alpine|synthetic-a" {
		t.Fatalf("rows = %q", rows)
	}
}

func TestImagePullDigestReference(t *testing.T) {
	e := newPullEnv(t)
	ref := "ghcr.io/owner/app@sha256:" + strings.Repeat("ab", 32)
	id := e.accepted(t, `{"reference":"`+ref+`"}`)
	e.waitState(t, id, "succeeded")
	if rows := e.auditRows(t, 1); rows[0] != "docker.image.pull|success|"+ref+"|synthetic-a" {
		t.Fatalf("rows = %q", rows)
	}
}

func TestImagePullRefusalsTouchNothing(t *testing.T) {
	hex64 := strings.Repeat("a", 64)
	tests := []struct {
		name, body string
		status     int
		code       string
		field      bool
	}{
		{"no body", ``, 422, "invalid_request", true},
		{"empty object", `{}`, 422, "invalid_request", true},
		{"empty reference", `{"reference":""}`, 422, "invalid_request", true},
		{"null reference", `{"reference":null}`, 422, "invalid_request", true},
		{"unknown key", `{"reference":"a:1","platform":"linux/arm64"}`, 400, "bad_request", false},
		{"registry auth key", `{"reference":"a:1","auth":{"username":"u"}}`, 400, "bad_request", false},
		{"wrong case key", `{"Reference":"a:1"}`, 400, "bad_request", false},
		{"duplicate key", `{"reference":"a:1","reference":"b:1"}`, 400, "bad_request", false},
		{"nested body", `{"image":{"reference":"a:1"}}`, 400, "bad_request", false},
		{"number", `{"reference":5}`, 400, "bad_request", false},
		{"array", `["a:1"]`, 400, "bad_request", false},
		{"trailing data", `{"reference":"a:1"} {}`, 400, "bad_request", false},
		{"not json", `a:1`, 400, "bad_request", false},
		{"oversize", `{"reference":"` + strings.Repeat("a", 5000) + `"}`, 400, "bad_request", false},
		{"no tag", `{"reference":"postgres"}`, 422, "invalid_request", true},
		{"registry port no tag", `{"reference":"localhost:5000/app"}`, 422, "invalid_request", true},
		{"empty tag", `{"reference":"postgres:"}`, 422, "invalid_request", true},
		{"uppercase", `{"reference":"Postgres:18"}`, 422, "invalid_request", true},
		{"space", `{"reference":"post gres:18"}`, 422, "invalid_request", true},
		{"trailing space", `{"reference":"postgres:18 "}`, 422, "invalid_request", true},
		{"semicolon", `{"reference":"postgres:18;id"}`, 422, "invalid_request", true},
		{"command substitution", `{"reference":"postgres:$(id)"}`, 422, "invalid_request", true},
		{"backtick", "{\"reference\":\"postgres:`id`\"}", 422, "invalid_request", true},
		{"double dash", `{"reference":"--rm"}`, 422, "invalid_request", true},
		{"leading dash tag", `{"reference":"a:--x"}`, 422, "invalid_request", true},
		{"query injection", `{"reference":"a:1&fromSrc=http://x"}`, 422, "invalid_request", true},
		{"newline", `{"reference":"a:1\nX-Registry-Auth: x"}`, 422, "invalid_request", true},
		{"nul", `{"reference":"a:1\u0000"}`, 422, "invalid_request", true},
		{"zero width", `{"reference":"a:1​"}`, 422, "invalid_request", true},
		{"bidi", `{"reference":"a:1‮"}`, 422, "invalid_request", true},
		{"traversal", `{"reference":"a/../b:1"}`, 422, "invalid_request", true},
		{"url", `{"reference":"http://evil.example/x:1"}`, 422, "invalid_request", true},
		{"over long", `{"reference":"` + strings.Repeat("a", 250) + `:tag12"}`, 422, "invalid_request", true},
		{"hex64 name", `{"reference":"` + hex64 + `:1"}`, 422, "invalid_request", true},
		{"hex64 digest", `{"reference":"` + hex64 + `@sha256:` + hex64 + `"}`, 422, "invalid_request", true},
		{"sha256 name", `{"reference":"sha256:abc"}`, 422, "invalid_request", true},
		{"bad digest", `{"reference":"a@sha256:zz"}`, 422, "invalid_request", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newPullEnv(t)
			rec := e.post(t, tc.body)
			wantErr(t, rec, tc.status, tc.code)
			if tc.field {
				if f := fieldsOf(t, rec); len(f) != 1 || f["reference"] == nil {
					t.Fatalf("fields = %v", f)
				}
			}
			for _, leak := range []string{"fromSrc", "evil", "X-Registry", "u200b", "$(id)"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Fatalf("answer echoes %q", leak)
				}
			}
			if !e.untouched() {
				t.Fatal("Docker, provider or audit reached")
			}
			if _, running := e.mgr.Busy("synthetic-a"); running {
				t.Fatal("slot taken")
			}
		})
	}
}

func TestImagePullProfileGateRules(t *testing.T) {
	e := newPullEnv(t)
	e.gate.err = ErrProfileNotFound
	wantErr(t, e.post(t, `{"reference":"a:1"}`), 404, "profile_not_found")
	e.gate.err = ErrInvalidProfileName
	wantErr(t, e.post(t, `{"reference":"a:1"}`), 404, "profile_not_found")
	e.gate.err, e.gate.status = nil, "stopped"
	wantErr(t, e.post(t, `{"reference":"a:1"}`), 409, "invalid_state")
	e.gate.status = "running"
	e.busy.id = "j_start1"
	wantErr(t, e.post(t, `{"reference":"a:1"}`), 409, "profile_busy")
	e.busy.id = ""
	e.provider.Err = dockerc.ErrRuntimeNotDocker
	wantErr(t, e.post(t, `{"reference":"a:1"}`), 409, "invalid_state")
	e.provider.Err = dockerc.ErrVMNotRunning
	wantErr(t, e.post(t, `{"reference":"a:1"}`), 409, "invalid_state")
	e.provider.Err = dockerc.ErrDockerUnavailable
	wantErr(t, e.post(t, `{"reference":"a:1"}`), 502, "docker_unavailable")
	if len(e.fake.Calls()) != 0 || len(e.audit.Rows()) != 0 {
		t.Fatal("a refused request reached Docker or the log")
	}
}

func TestImagePullNeedsTheToken(t *testing.T) {
	e := newPullEnv(t)
	req := httptest.NewRequest("POST", pullPath, strings.NewReader(`{"reference":"a:1"}`))
	req.Host = goodHost
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 401 || len(e.fake.Calls()) != 0 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestImagePullLimitsAndDuplicates(t *testing.T) {
	e := newPullEnv(t)
	started := make(chan struct{}, 8)
	e.fake.PullFunc = blocking(started)
	id1 := e.accepted(t, `{"reference":"a:1"}`)
	id2 := e.accepted(t, `{"reference":"b:1"}`)
	<-started
	<-started

	rec := e.post(t, `{"reference":"c:1"}`)
	w := wantErr(t, rec, 409, "conflict")
	if w.Error.Details["limit"] != float64(2) {
		t.Fatalf("details = %v", w.Error.Details)
	}
	rec = e.post(t, `{"reference":"a:1"}`)
	w = wantErr(t, rec, 409, "conflict")
	if w.Error.Details["name"] != "a:1" {
		t.Fatalf("details = %v", w.Error.Details)
	}
	if n := len(e.fake.CallsTo("PullImage")); n != 2 {
		t.Fatalf("%d pulls reached Docker", n)
	}
	// Another profile has its own budget.
	rec = call(t, e.h, "POST", "/api/profiles/synthetic-b/images/pull", `{"reference":"a:1"}`)
	if rec.Code != 202 {
		t.Fatalf("other profile: %d %s", rec.Code, rec.Body.String())
	}
	// Cancelling one frees a place.
	if rec := call(t, e.h, "POST", "/api/jobs/"+id1+"/cancel", ""); rec.Code != 200 {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		rec = e.post(t, `{"reference":"c:1"}`)
		if rec.Code == 202 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("place never freed: %d %s", rec.Code, rec.Body.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = id2
}

func TestImagePullDoesNotHoldTheProfileSlot(t *testing.T) {
	e := newPullEnv(t)
	started := make(chan struct{}, 1)
	e.fake.PullFunc = blocking(started)
	e.accepted(t, `{"reference":"a:1"}`)
	<-started
	if id, running := e.mgr.Busy("synthetic-a"); running {
		t.Fatalf("pull holds the slot (job %s)", id)
	}
	// While the pull runs, the VM can be restarted and config writes can hold the slot.
	if rec := call(t, e.h, "POST", "/api/profiles/synthetic-a/restart", ""); rec.Code != 202 {
		t.Fatalf("restart blocked by a pull: %d %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, running := e.mgr.Busy("synthetic-a"); !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restart job never ended")
		}
		time.Sleep(2 * time.Millisecond)
	}
	held, err := e.mgr.TryExclusive("synthetic-a", func() error { return nil })
	if err != nil || !held {
		t.Fatalf("TryExclusive blocked by a pull: %v %v", held, err)
	}
}

func TestImagePullCancelEndsCancelled(t *testing.T) {
	e := newPullEnv(t)
	started := make(chan struct{}, 1)
	sawCancel := make(chan struct{})
	e.fake.PullFunc = func(ctx context.Context, emit func(string)) error {
		started <- struct{}{}
		<-ctx.Done()
		close(sawCancel)
		return fmt.Errorf("dockerc: %w", ctx.Err())
	}
	id := e.accepted(t, `{"reference":"a:1"}`)
	<-started
	rec := call(t, e.h, "POST", "/api/jobs/"+id+"/cancel", "")
	if rec.Code != 200 || decodeMap(t, rec)["state"] != "cancelled" {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	select {
	case <-sawCancel:
	case <-time.After(3 * time.Second):
		t.Fatal("the pull's context was not cancelled")
	}
	j := e.job(t, id)
	if j["exitCode"] != nil {
		t.Fatalf("exitCode = %v", j["exitCode"])
	}
	if rows := e.auditRows(t, 1); rows[0] != "docker.image.pull|cancelled|a:1|synthetic-a" {
		t.Fatalf("rows = %q", rows)
	}
	// Cancelling again is the usual 409.
	wantErr(t, call(t, e.h, "POST", "/api/jobs/"+id+"/cancel", ""), 409, "job_not_running")
}

func TestImagePullFailureShowsTheRegistryMessageCleaned(t *testing.T) {
	e := newPullEnv(t)
	e.fake.PullFunc = func(_ context.Context, emit func(string)) error {
		emit("Pulling from library/nope")
		return &dockerc.PullError{Kind: dockerc.ErrPullFailed,
			Message: "pull access denied for nope, repository does not exist"}
	}
	id := e.accepted(t, `{"reference":"nope:1"}`)
	j := e.waitState(t, id, "failed")
	lines := outputLines(j)
	if last := lines[len(lines)-1]; last != "error: pull access denied for nope, repository does not exist" {
		t.Fatalf("output = %q", lines)
	}
	if rows := e.auditRows(t, 1); rows[0] != "docker.image.pull|failed|nope:1|synthetic-a" {
		t.Fatalf("rows = %q", rows)
	}
	// The place is free again: the same reference can be tried again.
	e.fake.PullFunc = nil
	deadline := time.Now().Add(3 * time.Second)
	for e.post(t, `{"reference":"nope:1"}`).Code != 202 {
		if time.Now().After(deadline) {
			t.Fatal("limits not released after a failure")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestImagePullFailureTextNeverCarriesRawErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"plain error with path", errors.New("dial unix /home/user/.colima/synthetic/docker.sock: refused"), "The pull failed."},
		{"unavailable", fmt.Errorf("%w: cannot reach docker socket /home/user/x", dockerc.ErrDockerUnavailable), "Docker is not reachable."},
		{"hostile pull error", &dockerc.PullError{Kind: dockerc.ErrPullFailed, Message: "\x1b[31mdenied\x1b[0m‮ at /home/user/.colima/x/y\x00"}, "error: denied at [path]"},
		{"json pull error", &dockerc.PullError{Kind: dockerc.ErrPullFailed, Message: `{"message":"raw"}`}, "The pull failed."},
		{"empty pull error", &dockerc.PullError{Kind: dockerc.ErrDaemon}, "The pull failed."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newPullEnv(t)
			e.fake.PullFunc = func(context.Context, func(string)) error { return tc.err }
			id := e.accepted(t, `{"reference":"a:1"}`)
			j := e.waitState(t, id, "failed")
			lines := outputLines(j)
			last := lines[len(lines)-1]
			if !strings.HasSuffix(last, tc.want) {
				t.Fatalf("last line = %q, want suffix %q", last, tc.want)
			}
			for _, l := range lines {
				if strings.ContainsAny(l, "\x00\x1b‮{") || strings.Contains(l, "/home") || strings.Contains(l, "synthetic") {
					t.Fatalf("unsafe output line %q", l)
				}
			}
		})
	}
}

func TestImagePullHostileProgressLinesAreCleanedAndBounded(t *testing.T) {
	e := newPullEnv(t)
	e.fake.PullFunc = func(_ context.Context, emit func(string)) error {
		emit("a\x1b[2Jb\x00c\r\nd")
		emit("\x07\x08")
		emit(strings.Repeat("x", 200_000))
		emit(`{"raw":"json"}`)
		emit("line two​three")
		emit("")
		return nil
	}
	id := e.accepted(t, `{"reference":"a:1"}`)
	j := e.waitState(t, id, "succeeded")
	for _, l := range outputLines(j) {
		if len(l) > 1200 {
			t.Fatalf("line of %d bytes", len(l))
		}
		for _, r := range l {
			if r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x200b {
				t.Fatalf("control character in %q", l)
			}
		}
		if strings.Contains(l, "raw") {
			t.Fatalf("raw JSON echoed: %q", l)
		}
	}
	got := outputLines(j)
	if len(got) != 4 || got[1] != "abc d" || got[2] != strings.Repeat("x", 299)+"…" || got[3] != "line twothree" {
		t.Fatalf("lines = %q", got)
	}
}

func TestImagePullPanicFailsTheJobSafely(t *testing.T) {
	e := newPullEnv(t)
	e.fake.PullFunc = func(context.Context, func(string)) error { panic("synthetic /home/user/secret") }
	id := e.accepted(t, `{"reference":"a:1"}`)
	j := e.waitState(t, id, "failed")
	for _, l := range outputLines(j) {
		if strings.Contains(l, "secret") {
			t.Fatalf("panic text in output: %q", l)
		}
	}
	if rows := e.auditRows(t, 1); !strings.Contains(rows[0], "|failed|a:1|") {
		t.Fatalf("rows = %q", rows)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		deadline := time.Now().Add(3 * time.Second)
		e.fake.PullFunc = nil
		for e.post(t, `{"reference":"a:1"}`).Code != 202 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	}()
	wg.Wait()
	if rec := e.post(t, `{"reference":"zz:1"}`); rec.Code != 202 {
		t.Fatalf("limits not released after a panic: %d", rec.Code)
	}
}

func TestImagePullShutdownCancelsAndAwaits(t *testing.T) {
	e := newPullEnv(t)
	started := make(chan struct{}, 2)
	e.fake.PullFunc = blocking(started)
	id1 := e.accepted(t, `{"reference":"a:1"}`)
	id2 := e.accepted(t, `{"reference":"b:1"}`)
	<-started
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.mgr.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{id1, id2} {
		if j := e.job(t, id); j["state"] != "cancelled" {
			t.Fatalf("job %s = %v", id, j["state"])
		}
	}
	wantErr(t, e.post(t, `{"reference":"c:1"}`), 503, "shutting_down")
	if rows := e.auditRows(t, 2); len(rows) != 2 {
		t.Fatalf("rows = %q", rows)
	}
}

func TestImagePullRequiresAManager(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
	}()
	WithImagePull(&fakeGate{}, &dockerctest.Provider{}, &fakeBusy{}, nil)
}

func TestImagePullAnswerIsValidJSONWithFixedKeys(t *testing.T) {
	e := newPullEnv(t)
	rec := e.post(t, `{"reference":"a:1"}`)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 4 {
		t.Fatalf("%v %v", err, got)
	}
}

func TestImagePullCancelledLineIsAlwaysRecorded(t *testing.T) {
	e := newPullEnv(t)
	started := make(chan struct{}, 1)
	e.fake.PullFunc = func(ctx context.Context, _ func(string)) error {
		started <- struct{}{}
		<-ctx.Done()
		return fmt.Errorf("dockerc: %w", ctx.Err())
	}
	for i := 0; i < 200; i++ {
		var id string
		deadline := time.Now().Add(3 * time.Second)
		for {
			rec := e.post(t, `{"reference":"a:1"}`)
			if rec.Code == 202 {
				id = decodeMap(t, rec)["jobId"].(string)
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("iteration %d: %d", i, rec.Code)
			}
			time.Sleep(time.Millisecond)
		}
		<-started
		if rec := call(t, e.h, "POST", "/api/jobs/"+id+"/cancel", ""); rec.Code != 200 {
			t.Fatalf("cancel: %d", rec.Code)
		}
		lines := outputLines(e.job(t, id))
		if lines[len(lines)-1] != "Pull cancelled." {
			t.Fatalf("iteration %d: output = %q", i, lines)
		}
	}
}

// NoAudit keeps the shared writer from appending a row even when an audit store
// is wired (the job observer writes the only pull row), while an ordinary spec
// on the same writer still does.
func TestNoAuditSpecWritesNoRowWithAStoreBehindIt(t *testing.T) {
	gate, busy, fake := &fakeGate{status: "Running"}, &fakeBusy{}, &dockerctest.Fake{}
	audit := &recordingAudit{}
	w := newDockerWriter(gate, &dockerctest.Provider{Client: fake}, busy, audit)
	run := func(context.Context, dockerc.Client) error { return nil }
	h := NewHandler(testPort, testToken, NewAPI(func(rt *Router) {
		rt.Handle("POST /api/profiles/{name}/x", func(rw http.ResponseWriter, r *http.Request) error {
			return w.serve(rw, r, writeSpec{Action: "docker.test.noaudit", NoAudit: true,
				Plan: func() (writePlan, error) { return writePlan{Run: run}, nil }})
		})
		rt.Handle("POST /api/profiles/{name}/y", func(rw http.ResponseWriter, r *http.Request) error {
			return w.serve(rw, r, writeSpec{Action: "docker.test.audit",
				Plan: func() (writePlan, error) { return writePlan{Run: run}, nil }})
		})
	}), nil)
	if rec := call(t, h, "POST", "/api/profiles/synthetic-a/x", ""); rec.Code != 204 {
		t.Fatalf("x: %d", rec.Code)
	}
	if n := len(audit.Rows()); n != 0 {
		t.Fatalf("NoAudit wrote %d rows", n)
	}
	if rec := call(t, h, "POST", "/api/profiles/synthetic-a/y", ""); rec.Code != 204 {
		t.Fatalf("y: %d", rec.Code)
	}
	if rows := audit.Rows(); len(rows) != 1 || rows[0].Action != "docker.test.audit" {
		t.Fatalf("rows = %+v", rows)
	}
}
