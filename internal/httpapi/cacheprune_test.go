package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/actionlog"
	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/operations"
)

const prunePath = "/api/colima/cache/prune"

// holdProc is a colima process that runs until cancelled.
type holdProc struct {
	ch   chan string
	once sync.Once
}

func (p *holdProc) Lines() <-chan string { return p.ch }
func (p *holdProc) Wait() (colima.Exit, error) {
	<-p.ch
	return colima.Exit{ExitCode: -1, Killed: true}, context.Canceled
}
func (p *holdProc) Cancel() { p.once.Do(func() { close(p.ch) }) }

// holdLauncher starts processes that never end by themselves.
type holdLauncher struct{}

func (holdLauncher) Launch(ctx context.Context, _ operations.Request) (colima.Process, error) {
	p := &holdProc{ch: make(chan string)}
	go func() { <-ctx.Done(); p.Cancel() }()
	return p, nil
}

type pruneEnv struct {
	mgr   *operations.Manager
	audit *recordingAudit
	h     http.Handler
	mu    sync.Mutex
	runs  []bool // includeLima of every task body built
}

func newPruneEnv(t *testing.T, body func(context.Context, func(string)) error) *pruneEnv {
	t.Helper()
	base := runtime.NumGoroutine()
	e := &pruneEnv{audit: &recordingAudit{}}
	rec := actionlog.New(e.audit)
	e.mgr = operations.New(operations.Options{Launcher: holdLauncher{}, Observers: []operations.Observer{rec}})
	gate := &fakeGate{status: "Running"}
	e.h = NewHandler(testPort, testToken, NewAPI(WithJobs(e.mgr, gate),
		WithCachePrune(e.mgr, func(all bool) operations.TaskFunc {
			e.mu.Lock()
			e.runs = append(e.runs, all)
			e.mu.Unlock()
			return body
		})), nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.mgr.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		if err := rec.Close(ctx); err != nil {
			t.Errorf("recorder: %v", err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if n := runtime.NumGoroutine(); n > base {
			t.Errorf("goroutine leak: %d now, %d before", n, base)
		}
	})
	return e
}

func (e *pruneEnv) built() []bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]bool(nil), e.runs...)
}

func okBody(context.Context, func(string)) error { return nil }

func TestCachePruneBodyValidation(t *testing.T) {
	e := newPruneEnv(t, okBody)
	for _, c := range []struct {
		name, body string
		status     int
		code       string
	}{
		{"empty body", ``, 422, "confirmation_required"},
		{"empty object", `{}`, 422, "confirmation_required"},
		{"null confirm", `{"confirm":null}`, 422, "confirmation_required"},
		{"empty confirm", `{"confirm":""}`, 422, "confirmation_required"},
		{"wrong confirm", `{"confirm":"yes"}`, 422, "confirmation_required"},
		{"other prune confirm", `{"confirm":"delete-unused-images"}`, 422, "confirmation_required"},
		{"case differs", `{"confirm":"Delete-Cached-Assets"}`, 422, "confirmation_required"},
		{"trailing space", `{"confirm":"delete-cached-assets "}`, 422, "confirmation_required"},
		{"only includeLima", `{"includeLima":true}`, 422, "confirmation_required"},
		{"unknown key", `{"confirm":"delete-cached-assets","all":true}`, 400, "bad_request"},
		{"wrong case key", `{"Confirm":"delete-cached-assets"}`, 400, "bad_request"},
		{"wrong case includeLima", `{"confirm":"delete-cached-assets","IncludeLima":true}`, 400, "bad_request"},
		{"duplicate key", `{"confirm":"delete-cached-assets","confirm":"delete-cached-assets"}`, 400, "bad_request"},
		{"duplicate includeLima", `{"confirm":"delete-cached-assets","includeLima":true,"includeLima":false}`, 400, "bad_request"},
		{"confirm is a number", `{"confirm":1}`, 400, "bad_request"},
		{"confirm is an object", `{"confirm":{"a":1}}`, 400, "bad_request"},
		{"includeLima is a string", `{"confirm":"delete-cached-assets","includeLima":"true"}`, 400, "bad_request"},
		{"includeLima is a number", `{"confirm":"delete-cached-assets","includeLima":1}`, 400, "bad_request"},
		{"array", `[]`, 400, "bad_request"},
		{"string", `"delete-cached-assets"`, 400, "bad_request"},
		{"not json", `confirm=delete-cached-assets`, 400, "bad_request"},
		{"trailing data", `{"confirm":"delete-cached-assets"} {}`, 400, "bad_request"},
		{"truncated", `{"confirm":"delete-cached-assets"`, 400, "bad_request"},
		{"oversized", `{"confirm":"delete-cached-assets","x":"` + strings.Repeat("a", 2000) + `"}`, 400, "bad_request"},
	} {
		t.Run(c.name, func(t *testing.T) {
			wantErr(t, call(t, e.h, "POST", prunePath, c.body), c.status, c.code)
		})
	}
	if n := len(e.built()); n != 0 {
		t.Fatalf("%d tasks were built for refused requests", n)
	}
	if rows := e.audit.Rows(); len(rows) != 0 {
		t.Fatalf("rows for refused requests: %+v", rows)
	}
}

func TestCachePruneAcceptsAndRecordsAGlobalRow(t *testing.T) {
	for _, c := range []struct {
		body   string
		all    bool
		detail string
	}{
		{`{"confirm":"delete-cached-assets"}`, false, "colima prune --force"},
		{`{"confirm":"delete-cached-assets","includeLima":false}`, false, "colima prune --force"},
		{`{"confirm":"delete-cached-assets","includeLima":true}`, true, "colima prune --force --all"},
		{`{"includeLima":true,"confirm":"delete-cached-assets"}`, true, "colima prune --force --all"},
	} {
		e := newPruneEnv(t, okBody)
		rec := call(t, e.h, "POST", prunePath, c.body)
		if rec.Code != 202 {
			t.Fatalf("%s: %d %s", c.body, rec.Code, rec.Body.String())
		}
		m := decodeMap(t, rec)
		if m["action"] != "cache-prune" || m["profile"] != "" || m["state"] != "running" || m["jobId"] == "" {
			t.Fatalf("answer = %v", m)
		}
		if got := e.built(); len(got) != 1 || got[0] != c.all {
			t.Fatalf("built = %v", got)
		}
		deadline := time.Now().Add(3 * time.Second)
		for len(e.audit.Rows()) < 1 && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
		rows := e.audit.Rows()
		if len(rows) != 1 || rows[0].Profile != actionlog.GlobalProfile || rows[0].Action != "cache-prune" ||
			rows[0].Outcome != "success" || rows[0].Detail == nil || *rows[0].Detail != c.detail {
			t.Fatalf("rows = %+v", rows)
		}
	}
}

func TestCachePruneRefusedWhileCreateOrStartRuns(t *testing.T) {
	for _, act := range []operations.Action{operations.ActionStart, operations.ActionCreate} {
		e := newPruneEnv(t, okBody)
		sum, err := e.mgr.Submit(operations.Request{Action: act, Profile: "synthetic-a"})
		if err != nil {
			t.Fatal(err)
		}
		w := wantErr(t, call(t, e.h, "POST", prunePath, `{"confirm":"delete-cached-assets"}`), 409, "invalid_state")
		if w.Error.Details["reason"] != "assets_in_use" || w.Error.Details["status"] != "starting" || w.Error.Details["jobId"] != sum.ID {
			t.Fatalf("details = %v", w.Error.Details)
		}
		if len(e.built()) != 1 || len(e.audit.Rows()) != 0 {
			t.Fatal("a refused prune started or logged something")
		}
		if _, err := e.mgr.Cancel(context.Background(), sum.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCachePruneAllowedWhileStopOrRestartRuns(t *testing.T) {
	e := newPruneEnv(t, okBody)
	sum, _ := e.mgr.Submit(operations.Request{Action: operations.ActionStop, Profile: "synthetic-a"})
	if rec := call(t, e.h, "POST", prunePath, `{"confirm":"delete-cached-assets"}`); rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	e.mgr.Cancel(context.Background(), sum.ID)
}

func TestCachePruneSecondConcurrentIsRefusedAndCancelFreesIt(t *testing.T) {
	started := make(chan struct{}, 2)
	e := newPruneEnv(t, func(ctx context.Context, emit func(string)) error {
		started <- struct{}{}
		<-ctx.Done()
		emit(operations.PruneCancelledLine)
		return ctx.Err()
	})
	rec := call(t, e.h, "POST", prunePath, `{"confirm":"delete-cached-assets"}`)
	if rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	id := decodeMap(t, rec)["jobId"].(string)
	<-started
	for _, body := range []string{`{"confirm":"delete-cached-assets"}`, `{"confirm":"delete-cached-assets","includeLima":true}`} {
		wantErr(t, call(t, e.h, "POST", prunePath, body), 409, "conflict")
	}
	// It never holds a profile slot: a start on any profile still goes through.
	if _, running := e.mgr.Busy("synthetic-a"); running {
		t.Fatal("slot held")
	}
	c := call(t, e.h, "POST", "/api/jobs/"+id+"/cancel", "")
	if c.Code != 200 || decodeMap(t, c)["state"] != "cancelled" {
		t.Fatalf("cancel: %d %s", c.Code, c.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		rec := call(t, e.h, "POST", prunePath, `{"confirm":"delete-cached-assets"}`)
		if rec.Code == 202 {
			<-started
			id2 := decodeMap(t, rec)["jobId"].(string)
			call(t, e.h, "POST", "/api/jobs/"+id2+"/cancel", "")
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("limit not released: %d", rec.Code)
		}
		time.Sleep(2 * time.Millisecond)
	}
	for len(e.audit.Rows()) < 2 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	for _, r := range e.audit.Rows() {
		if r.Outcome != "cancelled" || !strings.Contains(*r.Detail, "may already be removed") {
			t.Fatalf("row = %+v", r)
		}
	}
}

func TestCachePruneRouteNeedsTheBearerToken(t *testing.T) {
	e := newPruneEnv(t, okBody)
	req, _ := http.NewRequest("POST", prunePath, strings.NewReader(`{"confirm":"delete-cached-assets"}`))
	req.Host = goodHost
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("no token: %d", rec.Code)
	}
	if len(e.built()) != 0 {
		t.Fatal("task built without a token")
	}
}
