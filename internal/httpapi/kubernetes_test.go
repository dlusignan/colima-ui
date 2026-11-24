package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dlusignan/colima-ui/internal/operations"
)

type fakeProbe struct {
	mu      sync.Mutex
	enabled bool
	err     error
	calls   int
}

func (p *fakeProbe) KubernetesEnabled(string) (bool, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return p.enabled, p.err
}

func k8sServer(m JobsDeps, g ProfileGate, p KubernetesProbe) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithKubernetes(m, g, p)), nil)
}

const (
	resetBody  = `{"confirm":"reset-kubernetes"}`
	deleteBody = `{"confirm":"delete-kubernetes"}`
)

var k8sRoutes = []struct {
	verb string
	act  operations.Action
	body string
}{
	{"start", operations.ActionKubernetesStart, ""},
	{"stop", operations.ActionKubernetesStop, ""},
	{"reset", operations.ActionKubernetesReset, resetBody},
	{"delete", operations.ActionKubernetesDelete, deleteBody},
}

func TestKubernetesRoutesNeedToken(t *testing.T) {
	m, g, p := &fakeMgr{}, &fakeGate{status: "running"}, &fakeProbe{enabled: true}
	h := k8sServer(m, g, p)
	targets := [][2]string{{"GET", "/api/profiles/dev/kubernetes"}}
	for _, rt := range k8sRoutes {
		targets = append(targets, [2]string{"POST", "/api/profiles/dev/kubernetes/" + rt.verb})
	}
	for _, rt := range targets {
		req := httptest.NewRequest(rt[0], rt[1], strings.NewReader(resetBody))
		req.Host = goodHost
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatalf("%v: status %d", rt, rec.Code)
		}
	}
	if len(g.names) != 0 || len(m.calls) != 0 || p.calls != 0 {
		t.Fatal("handler ran without a token")
	}
}

func TestKubernetesActionAccepted(t *testing.T) {
	for _, c := range k8sRoutes {
		t.Run(c.verb, func(t *testing.T) {
			m, g, p := &fakeMgr{}, &fakeGate{status: "Running"}, &fakeProbe{enabled: true}
			rec := call(t, k8sServer(m, g, p), "POST", "/api/profiles/dev/kubernetes/"+c.verb, c.body)
			if rec.Code != 202 || !strings.Contains(rec.Body.String(), `"jobId":"j_abc123"`) {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			if len(m.submitReq) != 1 || m.submitReq[0].Action != c.act || m.submitReq[0].Profile != "dev" {
				t.Fatalf("submit = %+v", m.submitReq)
			}
		})
	}
}

func TestKubernetesRefusedUnlessExactlyRunning(t *testing.T) {
	for _, c := range k8sRoutes {
		for _, status := range []string{"stopped", "broken", "unknown", "", "runningx"} {
			m, g, p := &fakeMgr{}, &fakeGate{status: status}, &fakeProbe{enabled: true}
			w := wantErr(t, call(t, k8sServer(m, g, p), "POST", "/api/profiles/dev/kubernetes/"+c.verb, c.body), 409, "invalid_state")
			if w.Error.Details["status"] != status {
				t.Fatalf("%s %q: details %v", c.verb, status, w.Error.Details)
			}
			if len(m.submitReq) != 0 || p.calls != 0 {
				t.Fatalf("%s %q: went past the state check", c.verb, status)
			}
		}
	}
}

func TestKubernetesDisabledStartsNothing(t *testing.T) {
	for _, c := range k8sRoutes {
		m, g, p := &fakeMgr{}, &fakeGate{status: "running"}, &fakeProbe{enabled: false}
		w := wantErr(t, call(t, k8sServer(m, g, p), "POST", "/api/profiles/dev/kubernetes/"+c.verb, c.body), 409, "invalid_state")
		if w.Error.Details["reason"] != "kubernetes_not_enabled" || w.Error.Details["status"] != "running" {
			t.Fatalf("%s: details %v", c.verb, w.Error.Details)
		}
		if len(m.submitReq) != 0 {
			t.Fatalf("%s: submit called", c.verb)
		}
	}
}

func TestKubernetesBusyBeatsTheStatusReads(t *testing.T) {
	// A start is in flight: the list says stopped, and the answer must be busy.
	m := &fakeMgr{busy: func(string) (string, bool) { return "j_run", true }}
	g, p := &fakeGate{status: "stopped"}, &fakeProbe{enabled: true}
	for _, c := range k8sRoutes {
		w := wantErr(t, call(t, k8sServer(m, g, p), "POST", "/api/profiles/dev/kubernetes/"+c.verb, c.body), 409, "profile_busy")
		if w.Error.Details["jobId"] != "j_run" {
			t.Fatalf("details %v", w.Error.Details)
		}
	}
	if len(g.names) != 0 || p.calls != 0 || len(m.submitReq) != 0 {
		t.Fatal("status read before the busy check")
	}
}

func TestKubernetesSubmitRacesMapToConflicts(t *testing.T) {
	for _, c := range []struct {
		err    error
		code   string
		reason any
	}{
		{&operations.BusyError{Profile: "dev", JobID: "j_x"}, "profile_busy", nil},
	} {
		m := &fakeMgr{submit: func(operations.Request) (operations.JobSummary, error) { return operations.JobSummary{}, c.err }}
		w := wantErr(t, call(t, k8sServer(m, &fakeGate{status: "running"}, &fakeProbe{enabled: true}),
			"POST", "/api/profiles/dev/kubernetes/reset", resetBody), 409, c.code)
		if w.Error.Details["reason"] != c.reason {
			t.Fatalf("%v: details %v", c.err, w.Error.Details)
		}
	}
	m := &fakeMgr{submit: func(operations.Request) (operations.JobSummary, error) {
		return operations.JobSummary{}, errors.New("exec /usr/bin/colima: denied")
	}}
	rec := call(t, k8sServer(m, &fakeGate{status: "running"}, &fakeProbe{enabled: true}), "POST", "/api/profiles/dev/kubernetes/stop", "")
	wantErr(t, rec, 502, "colima_failed")
	if strings.Contains(rec.Body.String(), "/usr/bin") {
		t.Fatal("raw error leaked")
	}
}

func TestKubernetesUnknownAndInvalidProfile(t *testing.T) {
	for _, c := range k8sRoutes {
		wantErr(t, call(t, k8sServer(&fakeMgr{}, &fakeGate{err: ErrProfileNotFound}, &fakeProbe{}), "POST", "/api/profiles/dev/kubernetes/"+c.verb, c.body), 404, "profile_not_found")
		m, p := &fakeMgr{}, &fakeProbe{enabled: true}
		w := wantErr(t, call(t, k8sServer(m, &fakeGate{err: ErrInvalidProfileName}, p), "POST", "/api/profiles/--x/kubernetes/"+c.verb, c.body), 404, "profile_not_found")
		if len(w.Error.Details) != 0 || len(m.submitReq) != 0 || p.calls != 0 {
			t.Fatalf("hostile name echoed or went on: %v", w.Error.Details)
		}
		wantErr(t, call(t, k8sServer(&fakeMgr{}, &fakeGate{err: errors.New("boom")}, &fakeProbe{}), "POST", "/api/profiles/dev/kubernetes/"+c.verb, c.body), 502, "colima_failed")
	}
}

func TestKubernetesStartStopBodies(t *testing.T) {
	for _, verb := range []string{"start", "stop"} {
		for _, body := range []string{"", "  ", "{}", " {} "} {
			m := &fakeMgr{}
			if rec := call(t, k8sServer(m, &fakeGate{status: "running"}, &fakeProbe{enabled: true}), "POST", "/api/profiles/dev/kubernetes/"+verb, body); rec.Code != 202 {
				t.Fatalf("%s %q: %d", verb, body, rec.Code)
			}
		}
		for _, body := range []string{`{"confirm":"reset-kubernetes"}`, `{"a":1}`, `[]`, `x`, strings.Repeat(" ", 2000) + "{}"} {
			g := &fakeGate{status: "running"}
			wantErr(t, call(t, k8sServer(&fakeMgr{}, g, &fakeProbe{enabled: true}), "POST", "/api/profiles/dev/kubernetes/"+verb, body), 400, "bad_request")
			if len(g.names) != 0 {
				t.Fatal("gate ran for a bad body")
			}
		}
	}
}

func TestKubernetesConfirmEnforced(t *testing.T) {
	for _, c := range []struct{ verb, good, other string }{
		{"reset", resetBody, deleteBody},
		{"delete", deleteBody, resetBody},
	} {
		for _, body := range []string{
			"", "{}", `{"confirm":""}`, `{"confirm":"yes"}`, c.other,
			`{"confirm":"` + strings.ToUpper(strings.Trim(c.good[len(`{"confirm":"`):], `"}`)) + `"}`,
			`{"confirm":" ` + c.verb + `-kubernetes"}`, `{"confirm":"` + c.verb + `-kubernetes "}`,
			`{"confirm":null}`,
		} {
			m, g := &fakeMgr{}, &fakeGate{status: "running"}
			w := wantErr(t, call(t, k8sServer(m, g, &fakeProbe{enabled: true}), "POST", "/api/profiles/dev/kubernetes/"+c.verb, body), 422, "invalid_request")
			if _, ok := w.Error.Details["fields"].(map[string]any)["confirm"]; !ok {
				t.Fatalf("%s %q: details %v", c.verb, body, w.Error.Details)
			}
			if len(m.submitReq) != 0 || len(g.names) != 0 {
				t.Fatalf("%s %q: ran past the confirm check", c.verb, body)
			}
		}
		for _, body := range []string{
			"null", `{"Confirm":"` + c.verb + `-kubernetes"}`, `{"CONFIRM":"` + c.verb + `-kubernetes"}`,
			`{"confirm":"` + c.verb + `-kubernetes","confirm":"` + c.verb + `-kubernetes"}`,
			`{"confirm":"x","confirm":"` + c.verb + `-kubernetes"}`,
			`{"confirm":5}`, `{"confirm":["x"]}`, `{"confirm":"` + c.verb + `-kubernetes","extra":1}`,
			`{"confirm":"` + c.verb + `-kubernetes"} {}`, `{"confirm":`, `[]`, `"x"`,
			`{"confirm":"` + strings.Repeat("a", 2000) + `"}`,
		} {
			m := &fakeMgr{}
			wantErr(t, call(t, k8sServer(m, &fakeGate{status: "running"}, &fakeProbe{enabled: true}), "POST", "/api/profiles/dev/kubernetes/"+c.verb, body), 400, "bad_request")
			if len(m.submitReq) != 0 {
				t.Fatalf("%s %q: submit called", c.verb, body)
			}
		}
		if rec := call(t, k8sServer(&fakeMgr{}, &fakeGate{status: "running"}, &fakeProbe{enabled: true}), "POST", "/api/profiles/dev/kubernetes/"+c.verb, " "+c.good+"\n"); rec.Code != 202 {
			t.Fatalf("%s good body: %d", c.verb, rec.Code)
		}
	}
}

func TestKubernetesProbeFailure(t *testing.T) {
	rec := call(t, k8sServer(&fakeMgr{}, &fakeGate{status: "running"}, &fakeProbe{err: errors.New("/home/user/x failed")}), "POST", "/api/profiles/dev/kubernetes/stop", "")
	wantErr(t, rec, 500, "internal_error")
	if strings.Contains(rec.Body.String(), "/home") {
		t.Fatal("raw error leaked")
	}
	rec = call(t, k8sServer(&fakeMgr{}, &fakeGate{status: "running"}, &fakeProbe{err: errors.New("x")}), "GET", "/api/profiles/dev/kubernetes", "")
	wantErr(t, rec, 500, "internal_error")
}

func TestKubernetesGet(t *testing.T) {
	for _, on := range []bool{true, false} {
		rec := call(t, k8sServer(&fakeMgr{}, &fakeGate{status: "running"}, &fakeProbe{enabled: on}), "GET", "/api/profiles/dev/kubernetes", "")
		want := `{"enabled":false}`
		if on {
			want = `{"enabled":true}`
		}
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != want {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	}
	p := &fakeProbe{enabled: true}
	w := wantErr(t, call(t, k8sServer(&fakeMgr{}, &fakeGate{status: "stopped"}, p), "GET", "/api/profiles/dev/kubernetes", ""), 409, "invalid_state")
	if w.Error.Details["status"] != "stopped" || p.calls != 0 {
		t.Fatalf("details %v calls %d", w.Error.Details, p.calls)
	}
	wantErr(t, call(t, k8sServer(&fakeMgr{}, &fakeGate{err: ErrProfileNotFound}, p), "GET", "/api/profiles/dev/kubernetes", ""), 404, "profile_not_found")
}
