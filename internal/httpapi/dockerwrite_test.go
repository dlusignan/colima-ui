package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/dockerc/dockerctest"
	"github.com/dlusignan/colima-ui/internal/store"
)

// recordingAudit is an ActionRecorder that keeps the rows and the state of the
// context each append saw.
type recordingAudit struct {
	mu      sync.Mutex
	rows    []store.ActionEntry
	ctxErrs []error
	err     error
}

func (a *recordingAudit) AppendAction(ctx context.Context, e store.ActionEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, e)
	a.ctxErrs = append(a.ctxErrs, ctx.Err())
	return a.err
}

func (a *recordingAudit) Rows() []store.ActionEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]store.ActionEntry(nil), a.rows...)
}

// writeEnv wires a test-only route group on top of dockerWriter:
//
//	POST /api/profiles/{name}/t/{obj}   body {"name": "...", "force": bool, "note": "..."}
//	POST /api/profiles/{name}/n/{obj}   no body
//
// Its Run calls Client.Volumes, so a Docker call shows up in the fake.
type writeEnv struct {
	gate     *fakeGate
	busy     fakeBusy
	fake     *dockerctest.Fake
	provider *dockerctest.Provider
	audit    *recordingAudit
	run      func(ctx context.Context, c dockerc.Client) error
	timeout  time.Duration
	limit    int64
	h        http.Handler
}

func newWriteEnv() *writeEnv {
	e := &writeEnv{
		gate:  &fakeGate{status: "Running"},
		fake:  &dockerctest.Fake{},
		audit: &recordingAudit{},
	}
	e.provider = &dockerctest.Provider{Client: e.fake}
	d := newDockerWriter(e.gate, e.provider, &e.busy, e.audit)
	defaultRun := func(ctx context.Context, c dockerc.Client) error {
		if e.run != nil {
			return e.run(ctx, c)
		}
		_, err := c.Volumes(ctx)
		return err
	}
	e.h = NewHandler(testPort, testToken, NewAPI(func(rt *Router) {
		rt.Handle("POST /api/profiles/{name}/t/{obj}", func(w http.ResponseWriter, r *http.Request) error {
			var in struct {
				Name  string `json:"name"`
				Force bool   `json:"force"`
				Note  string `json:"note"`
			}
			return d.serve(w, r, writeSpec{
				Action: "docker.test.write", Timeout: e.timeout, BodyLimit: e.limit,
				Body: map[string]any{"name": &in.Name, "force": &in.Force, "note": &in.Note},
				Plan: func() (writePlan, error) {
					if _, err := requireObjectName("obj", r.PathValue("obj")); err != nil {
						return writePlan{}, err
					}
					name, err := requireObjectName("name", in.Name)
					if err != nil {
						return writePlan{}, err
					}
					return writePlan{Object: name, Run: defaultRun, Result: map[string]string{"name": name}}, nil
				},
			})
		})
		rt.Handle("POST /api/profiles/{name}/n/{obj}", func(w http.ResponseWriter, r *http.Request) error {
			return d.serve(w, r, writeSpec{
				Action: "docker.test.nobody",
				Plan: func() (writePlan, error) {
					obj, err := requireImageRef("obj", r.PathValue("obj"))
					if err != nil {
						return writePlan{}, err
					}
					return writePlan{Object: obj, Run: defaultRun}, nil
				},
			})
		})
	}), nil)
	return e
}

func (e *writeEnv) post(t *testing.T, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, e.h, "POST", target, body)
}

const (
	tURL = "/api/profiles/synthetic-a/t/vol1"
	nURL = "/api/profiles/synthetic-a/n/nginx:1"
)

func TestDockerWriteSuccess(t *testing.T) {
	e := newWriteEnv()
	rec := e.post(t, tURL, `{"name":"pgdata","force":true,"note":"synthetic-secret-note"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"pgdata"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if n := len(e.fake.CallsTo("Volumes")); n != 1 {
		t.Fatalf("docker calls = %d", n)
	}
	rows := e.audit.Rows()
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	r := rows[0]
	if r.Profile != "synthetic-a" || r.Action != "docker.test.write" || r.Outcome != "success" ||
		r.Detail == nil || *r.Detail != "pgdata" {
		t.Fatalf("row = %+v", r)
	}
	if strings.Contains(fmt.Sprintf("%+v %q", r, *r.Detail), "synthetic-secret-note") {
		t.Fatal("body content reached the action log")
	}
	if got := e.provider.Profiles(); len(got) != 1 || got[0] != "synthetic-a" {
		t.Fatalf("provider profiles = %v", got)
	}
}

func TestDockerWriteNoBodyRouteAnswers204(t *testing.T) {
	e := newWriteEnv()
	for _, body := range []string{"", "{}", " {} \n"} {
		rec := e.post(t, nURL, body)
		if rec.Code != 204 || rec.Body.Len() != 0 {
			t.Fatalf("body %q: %d %q", body, rec.Code, rec.Body.String())
		}
	}
	for _, body := range []string{`{"a":1}`, `x`, `[]`, strings.Repeat(" ", 2000)} {
		wantErr(t, e.post(t, nURL, body), 400, "bad_request")
	}
	if n := len(e.fake.Calls()); n != 3 {
		t.Fatalf("docker calls = %d, want 3", n)
	}
}

// TestDockerWriteRejections is the table of every refusal before Docker: none
// makes a Docker call, asks the provider, or writes an action log row.
func TestDockerWriteRejections(t *testing.T) {
	long := strings.Repeat("a", 129)
	tests := []struct {
		name   string
		setup  func(*writeEnv)
		target string
		body   string
		status int
		code   string
		field  string // expected key of details.fields
	}{
		{"unknown profile", func(e *writeEnv) { e.gate.err = ErrProfileNotFound }, tURL, `{"name":"x"}`, 404, "profile_not_found", ""},
		{"malformed profile", func(e *writeEnv) { e.gate.err = ErrInvalidProfileName }, tURL, `{"name":"x"}`, 404, "profile_not_found", ""},
		{"gate failure", func(e *writeEnv) { e.gate.err = errors.New("colima exploded at /home/user") }, tURL, `{"name":"x"}`, 502, "colima_failed", ""},
		{"stopped", func(e *writeEnv) { e.gate.status = "Stopped" }, tURL, `{"name":"x"}`, 409, "invalid_state", ""},
		{"busy", func(e *writeEnv) { e.busy = fakeBusy{id: "j_1"} }, tURL, `{"name":"x"}`, 409, "profile_busy", ""},
		{"busy before body", func(e *writeEnv) { e.busy = fakeBusy{id: "j_1"} }, tURL, `garbage`, 409, "profile_busy", ""},
		{"stopped before body", func(e *writeEnv) { e.gate.status = "stopped" }, tURL, `garbage`, 409, "invalid_state", ""},
		{"not json", nil, tURL, `garbage`, 400, "bad_request", ""},
		{"empty body", nil, tURL, ``, 400, "bad_request", ""},
		{"array body", nil, tURL, `[]`, 400, "bad_request", ""},
		{"null body", nil, tURL, `null`, 400, "bad_request", ""},
		{"unknown key", nil, tURL, `{"name":"x","extra":1}`, 400, "bad_request", ""},
		{"duplicate key", nil, tURL, `{"name":"x","name":"y"}`, 400, "bad_request", ""},
		{"wrong case key", nil, tURL, `{"Name":"x"}`, 400, "bad_request", ""},
		{"wrong type", nil, tURL, `{"name":1}`, 400, "bad_request", ""},
		{"trailing data", nil, tURL, `{"name":"x"} {"name":"y"}`, 400, "bad_request", ""},
		{"oversize", func(e *writeEnv) { e.limit = 64 }, tURL, `{"name":"x","note":"` + strings.Repeat("n", 200) + `"}`, 400, "bad_request", ""},
		{"missing name", nil, tURL, `{}`, 422, "invalid_request", "name"},
		{"empty name", nil, tURL, `{"name":""}`, 422, "invalid_request", "name"},
		{"dotdot name", nil, tURL, `{"name":"../x"}`, 422, "invalid_request", "name"},
		{"slash name", nil, tURL, `{"name":"a/b"}`, 422, "invalid_request", "name"},
		{"escaped dots name", nil, tURL, `{"name":"%2e%2e"}`, 422, "invalid_request", "name"},
		{"newline name", nil, tURL, `{"name":"a\nb"}`, 422, "invalid_request", "name"},
		{"nul name", nil, tURL, `{"name":"a\u0000b"}`, 422, "invalid_request", "name"},
		{"129 char name", nil, tURL, `{"name":"` + long + `"}`, 422, "invalid_request", "name"},
		{"dotdot path", nil, "/api/profiles/synthetic-a/t/%2e%2e", `{"name":"x"}`, 422, "invalid_request", "obj"},
		{"encoded slash path", nil, "/api/profiles/synthetic-a/t/a%2Fb", `{"name":"x"}`, 422, "invalid_request", "obj"},
		{"newline path", nil, "/api/profiles/synthetic-a/t/a%0Ab", `{"name":"x"}`, 422, "invalid_request", "obj"},
		{"129 char path", nil, "/api/profiles/synthetic-a/t/" + long, `{"name":"x"}`, 422, "invalid_request", "obj"},
		{"bad image ref", nil, "/api/profiles/synthetic-a/n/Bad%20Ref", ``, 422, "invalid_request", "obj"},
		{"image ref escaped dots", nil, "/api/profiles/synthetic-a/n/%2e%2e", ``, 422, "invalid_request", "obj"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newWriteEnv()
			if tc.setup != nil {
				tc.setup(e)
			}
			rec := e.post(t, tc.target, tc.body)
			wantErr(t, rec, tc.status, tc.code)
			m := decodeMap(t, rec)
			details := m["error"].(map[string]any)["details"].(map[string]any)
			if tc.field != "" {
				fields, _ := details["fields"].(map[string]any)
				if _, ok := fields[tc.field]; !ok || len(fields) != 1 {
					t.Fatalf("details = %v", details)
				}
			}
			if tc.code == "profile_busy" && details["jobId"] != "j_1" {
				t.Fatalf("details = %v", details)
			}
			for _, leak := range []string{"/home/user", "exploded", "%2e", "..", "\\n"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Fatalf("answer echoes %q: %s", leak, rec.Body.String())
				}
			}
			if c := e.fake.Calls(); len(c) != 0 {
				t.Fatalf("docker calls: %+v", c)
			}
			if p := e.provider.Profiles(); len(p) != 0 {
				t.Fatalf("provider asked: %v", p)
			}
			if r := e.audit.Rows(); len(r) != 0 {
				t.Fatalf("action log rows: %+v", r)
			}
		})
	}
}

func TestDockerWriteDockerFailures(t *testing.T) {
	secret := "ERR secret /home/user/.colima/synthetic daemon text"
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not found", fmt.Errorf("%w: %s", dockerc.ErrObjectNotFound, secret), 404, "not_found"},
		{"conflict", fmt.Errorf("%w: %s", dockerc.ErrConflict, secret), 409, "conflict"},
		{"invalid", fmt.Errorf("%w: %s", dockerc.ErrInvalidRequest, secret), 422, "invalid_request"},
		{"unavailable", fmt.Errorf("%w: %s", dockerc.ErrDockerUnavailable, secret), 502, "docker_unavailable"},
		{"daemon", fmt.Errorf("%w: %s", dockerc.ErrDaemon, secret), 502, "docker_unavailable"},
		{"deadline", fmt.Errorf("%s: %w", secret, context.DeadlineExceeded), 502, "docker_unavailable"},
		{"unknown", errors.New(secret), 500, "internal_error"},
		{"bad name from dockerc", fmt.Errorf("%w: %s", dockerc.ErrBadName, secret), 500, "internal_error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newWriteEnv()
			e.fake.FailWith("Volumes", tc.err)
			rec := e.post(t, tURL, `{"name":"pgdata"}`)
			wantErr(t, rec, tc.status, tc.code)
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "/home") {
				t.Fatalf("docker text reached the browser: %s", rec.Body.String())
			}
			if tc.status == 404 || tc.status == 409 && tc.code == "conflict" {
				d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)
				if d["name"] != "pgdata" || len(d) != 1 {
					t.Fatalf("details = %v", d)
				}
			}
			rows := e.audit.Rows()
			if len(rows) != 1 || rows[0].Outcome != "failed" || rows[0].Detail == nil || *rows[0].Detail != "pgdata" {
				t.Fatalf("rows = %+v", rows)
			}
		})
	}
}

func TestDockerWriteProviderFailures(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"vm stopped meanwhile", dockerc.ErrVMNotRunning, 409, "invalid_state"},
		{"not docker", dockerc.ErrRuntimeNotDocker, 409, "invalid_state"},
		{"socket gone", fmt.Errorf("%w: cannot reach docker socket", dockerc.ErrDockerUnavailable), 502, "docker_unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newWriteEnv()
			e.provider.Err = tc.err
			wantErr(t, e.post(t, tURL, `{"name":"pgdata"}`), tc.status, tc.code)
			if c := e.fake.Calls(); len(c) != 0 {
				t.Fatalf("docker calls: %+v", c)
			}
			// The attempt reached the Docker step, so it is on the log.
			if rows := e.audit.Rows(); len(rows) != 1 || rows[0].Outcome != "failed" {
				t.Fatalf("rows = %+v", rows)
			}
		})
	}
}

func TestDockerWriteFailingActionLogChangesNothing(t *testing.T) {
	e := newWriteEnv()
	e.audit.err = errors.New("disk full at /home/user/history.db")

	rec := e.post(t, tURL, `{"name":"pgdata"}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "disk full") {
		t.Fatalf("success path: %d %s", rec.Code, rec.Body.String())
	}
	e.fake.FailWith("Volumes", dockerc.ErrConflict)
	rec = e.post(t, tURL, `{"name":"pgdata"}`)
	wantErr(t, rec, 409, "conflict")
	if strings.Contains(rec.Body.String(), "disk full") {
		t.Fatal("append error reached the browser")
	}
	if n := len(e.audit.Rows()); n != 2 {
		t.Fatalf("append attempts = %d", n)
	}
}

func TestDockerWriteWithoutRecorderStillWorks(t *testing.T) {
	e := newWriteEnv()
	d := newDockerWriter(e.gate, e.provider, &e.busy, nil)
	d.record(context.Background(), "p", "a", "o", nil) // must not panic
}

func TestDockerWriteAuditSurvivesClientHangUp(t *testing.T) {
	e := newWriteEnv()
	ctx, cancel := context.WithCancel(context.Background())
	e.run = func(context.Context, dockerc.Client) error {
		cancel() // the client goes away while Docker works
		return nil
	}
	req := httptest.NewRequest("POST", tURL, strings.NewReader(`{"name":"pgdata"}`)).WithContext(ctx)
	req.Host = goodHost
	req.Header.Set("Authorization", "Bearer "+testToken)
	e.h.ServeHTTP(httptest.NewRecorder(), req)
	rows := e.audit.Rows()
	if len(rows) != 1 || rows[0].Outcome != "success" {
		t.Fatalf("rows = %+v", rows)
	}
	if err := e.audit.ctxErrs[0]; err != nil {
		t.Fatalf("append ran on a cancelled context: %v", err)
	}
}

func TestDockerWriteTimeout(t *testing.T) {
	e := newWriteEnv()
	e.timeout = 40 * time.Millisecond
	e.run = func(ctx context.Context, _ dockerc.Client) error {
		<-ctx.Done()
		return fmt.Errorf("dockerc: request timed out: %w", ctx.Err())
	}
	start := time.Now()
	wantErr(t, e.post(t, tURL, `{"name":"pgdata"}`), 502, "docker_unavailable")
	if time.Since(start) > 3*time.Second {
		t.Fatal("timeout not applied")
	}
	if rows := e.audit.Rows(); len(rows) != 1 || rows[0].Outcome != "failed" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestDockerWriteBrokenPlanNeverReachesDocker(t *testing.T) {
	e := newWriteEnv()
	d := newDockerWriter(e.gate, e.provider, &e.busy, e.audit)
	for name, plan := range map[string]writePlan{
		"hostile object": {Object: "../x", Run: func(context.Context, dockerc.Client) error { t.Error("ran"); return nil }},
		"no run":         {Object: "x"},
	} {
		h := NewHandler(testPort, testToken, NewAPI(func(rt *Router) {
			rt.Handle("POST /api/profiles/{name}/b", func(w http.ResponseWriter, r *http.Request) error {
				return d.serve(w, r, writeSpec{Action: "docker.test.broken", Plan: func() (writePlan, error) { return plan, nil }})
			})
		}), nil)
		wantErr(t, call(t, h, "POST", "/api/profiles/synthetic-a/b", ""), 500, "internal_error")
		if len(e.audit.Rows()) != 0 || len(e.provider.Profiles()) != 0 {
			t.Fatalf("%s: touched docker or the log", name)
		}
	}
}

func TestDockerWriteStatusIsCaseInsensitive(t *testing.T) {
	e := newWriteEnv()
	e.gate.status = "RUNNING"
	if rec := e.post(t, tURL, `{"name":"x"}`); rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestDockerWriteConcurrent(t *testing.T) {
	e := newWriteEnv()
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"name":"vol%d"}`, i)
			if i%3 == 0 {
				body = `{"name":"../x"}`
			}
			rec := e.post(t, tURL, body)
			if (i%3 == 0) != (rec.Code == 422) {
				t.Errorf("i=%d status %d", i, rec.Code)
			}
		}(i)
	}
	wg.Wait()
	if n := len(e.audit.Rows()); n != 20 {
		t.Fatalf("rows = %d, want 20", n)
	}
	if n := len(e.fake.CallsTo("Volumes")); n != 20 {
		t.Fatalf("docker calls = %d, want 20", n)
	}
}

func TestDockerWriteRoutesNeedToken(t *testing.T) {
	e := newWriteEnv()
	req := httptest.NewRequest("POST", tURL, strings.NewReader(`{"name":"x"}`))
	req.Host = goodHost
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 401 || len(e.fake.Calls()) != 0 || len(e.audit.Rows()) != 0 {
		t.Fatalf("status %d", rec.Code)
	}
}
