package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/dockerc/dockerctest"
)

const (
	caID64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	caID12 = "0123456789ab"
	caBase = "/api/profiles/synthetic-a/containers/"
)

type caVerb struct {
	verb   string // start, stop, restart
	method string // fake method name
	action string
}

var caVerbs = []caVerb{
	{"start", "StartContainer", "docker.container.start"},
	{"stop", "StopContainer", "docker.container.stop"},
	{"restart", "RestartContainer", "docker.container.restart"},
}

func newCAEnv() *volEnv {
	e := &volEnv{gate: &fakeGate{status: "Running"}, busy: &fakeBusy{}, fake: &dockerctest.Fake{}, audit: &recordingAudit{}}
	e.provider = &dockerctest.Provider{Client: e.fake}
	e.h = NewHandler(testPort, testToken, NewAPI(WithContainerActions(e.gate, e.provider, e.busy, e.audit)), nil)
	return e
}

func noDockerCall(t *testing.T, e *volEnv) {
	t.Helper()
	if n := len(e.fake.Calls()); n != 0 {
		t.Fatalf("%d Docker calls: %+v", n, e.fake.Calls())
	}
	if n := len(e.provider.Profiles()); n != 0 {
		t.Fatalf("provider asked %d times", n)
	}
}

func TestContainerActionChanged(t *testing.T) {
	for _, v := range caVerbs {
		for _, id := range []string{caID64} {
			e := newCAEnv()
			rec := e.do(t, "POST", caBase+id+"/"+v.verb, "")
			if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"changed":true}` {
				t.Fatalf("%s: %d %s", v.verb, rec.Code, rec.Body.String())
			}
			c := e.fake.CallsTo(v.method)
			if len(c) != 1 || len(c[0].Args) != 1 || c[0].Args[0] != id {
				t.Fatalf("%s calls = %+v", v.verb, e.fake.Calls())
			}
			rows := e.audit.Rows()
			if len(rows) != 1 || rows[0].Action != v.action || rows[0].Outcome != "success" || rows[0].Profile != "synthetic-a" ||
				rows[0].Detail == nil || *rows[0].Detail != caID12+" changed=true" {
				t.Fatalf("%s rows = %+v", v.verb, rows)
			}
		}
	}
}

func TestContainerActionUnchanged304(t *testing.T) {
	for _, v := range caVerbs[:2] { // restart never answers 304
		e := newCAEnv()
		e.fake.ContainerUnchanged = true
		rec := e.do(t, "POST", caBase+caID64+"/"+v.verb, "")
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"changed":false}` {
			t.Fatalf("%s: %d %s", v.verb, rec.Code, rec.Body.String())
		}
		rows := e.audit.Rows()
		if len(rows) != 1 || rows[0].Outcome != "success" || *rows[0].Detail != caID12+" changed=false" {
			t.Fatalf("%s rows = %+v", v.verb, rows)
		}
	}
}

func TestContainerActionDockerErrors(t *testing.T) {
	secret := "/home/user/.colima/synthetic"
	tests := []struct {
		name    string
		err     error
		status  int
		code    string
		changed string
	}{
		{"unknown", dockerc.ErrObjectNotFound, 404, "not_found", "false"},
		{"state refused", fmt.Errorf("%w %s", dockerc.ErrConflict, secret), 409, "conflict", "false"},
		{"docker down", dockerc.ErrDockerUnavailable, 502, "docker_unavailable", "unknown"},
		{"daemon", fmt.Errorf("%w %s", dockerc.ErrDaemon, secret), 502, "docker_unavailable", "unknown"},
		{"timeout", fmt.Errorf("x: %w", context.DeadlineExceeded), 502, "docker_unavailable", "unknown"},
		{"vm stopped under us", dockerc.ErrVMNotRunning, 409, "invalid_state", "false"},
		{"not docker", dockerc.ErrRuntimeNotDocker, 409, "invalid_state", "false"},
	}
	for _, v := range caVerbs {
		for _, tc := range tests {
			t.Run(v.verb+" "+tc.name, func(t *testing.T) {
				e := newCAEnv()
				e.fake.FailWith(v.method, tc.err)
				rec := e.do(t, "POST", caBase+caID64+"/"+v.verb, "")
				wantErr(t, rec, tc.status, tc.code)
				if strings.Contains(rec.Body.String(), secret) {
					t.Fatal("daemon text leaked")
				}
				rows := e.audit.Rows()
				if len(rows) != 1 || rows[0].Action != v.action || rows[0].Outcome != "failed" || *rows[0].Detail != caID12+" changed="+tc.changed {
					t.Fatalf("rows = %+v", rows)
				}
				if tc.code == "conflict" {
					if m := decodeMap(t, rec)["error"].(map[string]any)["message"]; m != containerConflictMessage ||
						strings.Contains(fmt.Sprint(m), "in use or protected") {
						t.Fatalf("message = %v", m)
					}
				}
			})
		}
	}
}

func TestContainerActionProviderFailures(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{dockerc.ErrDockerUnavailable, 502, "docker_unavailable"},
		{dockerc.ErrVMNotRunning, 409, "invalid_state"},
	} {
		e := newCAEnv()
		e.provider.Err = tc.err
		wantErr(t, e.do(t, "POST", caBase+caID64+"/stop", ""), tc.status, tc.code)
		if len(e.fake.Calls()) != 0 {
			t.Fatal("Docker called")
		}
		if rows := e.audit.Rows(); len(rows) != 1 || rows[0].Outcome != "failed" {
			t.Fatalf("rows = %+v", rows)
		}
	}
}

func TestContainerActionNotFoundNamesOnlyTheValidID(t *testing.T) {
	e := newCAEnv()
	e.fake.FailWith("StartContainer", dockerc.ErrObjectNotFound)
	rec := e.do(t, "POST", caBase+caID64+"/start", "")
	d := wantErr(t, rec, 404, "not_found").Error.Details
	if d["name"] != caID64 {
		t.Fatalf("details = %v", d)
	}
}

func TestContainerActionGateAndBusy(t *testing.T) {
	for _, v := range caVerbs {
		for _, tc := range []struct {
			name   string
			setup  func(*volEnv)
			status int
			code   string
		}{
			{"unknown profile", func(e *volEnv) { e.gate.err = ErrProfileNotFound }, 404, "profile_not_found"},
			{"bad profile", func(e *volEnv) { e.gate.err = ErrInvalidProfileName }, 404, "profile_not_found"},
			{"gate fails", func(e *volEnv) { e.gate.err = fmt.Errorf("boom /home/user") }, 502, "colima_failed"},
			{"stopped", func(e *volEnv) { e.gate.status = "Stopped" }, 409, "invalid_state"},
			{"broken", func(e *volEnv) { e.gate.status = "Broken" }, 409, "invalid_state"},
			{"busy", func(e *volEnv) { *e.busy = fakeBusy{id: "j_1"} }, 409, "profile_busy"},
		} {
			t.Run(v.verb+" "+tc.name, func(t *testing.T) {
				e := newCAEnv()
				tc.setup(e)
				rec := e.do(t, "POST", caBase+caID64+"/"+v.verb, "")
				wantErr(t, rec, tc.status, tc.code)
				if strings.Contains(rec.Body.String(), "/home/user") {
					t.Fatal("leak")
				}
				noDockerCall(t, e)
				if n := len(e.audit.Rows()); n != 0 {
					t.Fatalf("%d rows for a refused request", n)
				}
			})
		}
	}
}

func TestContainerActionStoppedVMDetails(t *testing.T) {
	e := newCAEnv()
	e.gate.status = "Stopped"
	w := wantErr(t, e.do(t, "POST", caBase+caID64+"/start", ""), 409, "invalid_state")
	if w.Error.Details["status"] != "stopped" {
		t.Fatalf("details = %v", w.Error.Details)
	}
}

func TestContainerActionBodies(t *testing.T) {
	for _, v := range caVerbs {
		for _, body := range []string{`{}`, `{"t":0}`, `{"timeout":1}`, `null`, `[]`, `x`, `0`, `"a"`, strings.Repeat(" ", 10) + `{}`, strings.Repeat("a", 5000)} {
			e := newCAEnv()
			wantErr(t, e.do(t, "POST", caBase+caID64+"/"+v.verb, body), 400, "bad_request")
			noDockerCall(t, e)
			if len(e.audit.Rows()) != 0 {
				t.Fatal("row for a refused body")
			}
		}
		// Whitespace only is an empty body.
		e := newCAEnv()
		if rec := e.do(t, "POST", caBase+caID64+"/"+v.verb, " \n"); rec.Code != 200 {
			t.Fatalf("%s whitespace body: %d", v.verb, rec.Code)
		}
	}
}

func TestContainerActionHostileIDs(t *testing.T) {
	// Every id the mux routes to the handler is a 422.
	ids := []string{
		"web", "synthetic-web", "0123456789a", caID12, caID64[:63], caID64 + "0", strings.ToUpper(caID64),
		caID64[:63] + "g", "%2e%2e", "0123456789a%2fb", caID64[:62] + "%2f", caID64[:60] + "%3fx",
		caID64[:63] + "%23", caID64[:63] + "%00", caID64[:63] + "%0a", caID64[:61] + "%e2%80%8b",
		caID64[:61] + "%e2%80%ae", caID64[:63] + "+", "%ff", "%", caID64[:63] + "%5c", "sha256:" + caID64,
		"sha256:" + caID64[:57],
	}
	for _, v := range caVerbs {
		for _, id := range ids {
			if _, err := url.ParseRequestURI(caBase + id + "/" + v.verb); err != nil {
				continue // net/http itself answers 400 before any handler
			}
			e := newCAEnv()
			rec := e.do(t, "POST", caBase+id+"/"+v.verb, "")
			if rec.Code != 422 {
				t.Errorf("%s %q: %d %s", v.verb, id, rec.Code, rec.Body.String())
			}
			noDockerCall(t, e)
			if len(e.audit.Rows()) != 0 {
				t.Errorf("%s %q: audit row for a hostile id", v.verb, id)
			}
			if dec, err := url.PathUnescape(id); err == nil && len(dec) > 3 && strings.Contains(rec.Body.String(), dec) {
				t.Errorf("%s %q echoed: %s", v.verb, id, rec.Body.String())
			}
		}
		// Dot segments never reach the handler: the mux cleans the path (301).
		for _, id := range []string{"..", "."} {
			e := newCAEnv()
			if rec := e.do(t, "POST", caBase+id+"/"+v.verb, ""); rec.Code != 301 {
				t.Errorf("%s %q: %d", v.verb, id, rec.Code)
			}
			noDockerCall(t, e)
		}
	}
}

func TestContainerActionInvalidIDIs422WithFixedMessage(t *testing.T) {
	e := newCAEnv()
	rec := e.do(t, "POST", caBase+"synthetic-web/stop", "")
	w := wantErr(t, rec, 422, "invalid_request")
	f, _ := w.Error.Details["fields"].(map[string]any)
	if f["id"] != invalidContainerIDMessage || strings.Contains(rec.Body.String(), "synthetic-web") {
		t.Fatalf("details = %v", w.Error.Details)
	}
}

func TestContainerActionMethodsAndAuth(t *testing.T) {
	e := newCAEnv()
	for _, m := range []string{"GET", "PUT", "DELETE", "PATCH"} {
		wantErr(t, e.do(t, m, caBase+caID64+"/start", ""), 405, "method_not_allowed")
	}
	// No kill, pause, remove or other verb exists.
	for _, verb := range []string{"kill", "pause", "unpause", "remove", "rm", "exec", "stop/", "start/extra"} {
		rec := e.do(t, "POST", caBase+caID64+"/"+verb, "")
		if rec.Code != 404 {
			t.Errorf("%s: %d", verb, rec.Code)
		}
	}
	// No token, no action.
	req := httptest.NewRequest("POST", caBase+caID64+"/start", nil)
	req.Host = goodHost
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	noDockerCall(t, e)
}

func TestContainerActionAuditDetailHasNoRequestText(t *testing.T) {
	e := newCAEnv()
	e.do(t, "POST", caBase+caID64+"/restart", "")
	rows := e.audit.Rows()
	if len(rows) != 1 || strings.Contains(*rows[0].Detail, caID64) {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestContainerActionSurvivesClientHangUp(t *testing.T) {
	// The write is already under way at Docker when the browser leaves: the
	// call must still run with a live context and the row must be written.
	e := newCAEnv()
	rec := e.do(t, "POST", caBase+caID64+"/stop", "")
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	if rows := e.audit.Rows(); len(rows) != 1 || e.audit.ctxErrs[0] != nil {
		t.Fatalf("rows = %+v errs = %v", rows, e.audit.ctxErrs)
	}
}
