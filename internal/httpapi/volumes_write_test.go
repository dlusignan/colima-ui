package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/dockerc/dockerctest"
)

type volEnv struct {
	gate     *fakeGate
	busy     *fakeBusy
	fake     *dockerctest.Fake
	provider *dockerctest.Provider
	audit    *recordingAudit
	h        http.Handler
}

func newVolEnv() *volEnv {
	e := &volEnv{gate: &fakeGate{status: "Running"}, busy: &fakeBusy{}, fake: &dockerctest.Fake{}, audit: &recordingAudit{}}
	e.provider = &dockerctest.Provider{Client: e.fake}
	e.h = NewHandler(testPort, testToken, NewAPI(WithVolumes(e.gate, e.provider, e.busy, e.audit)), nil)
	return e
}

func (e *volEnv) do(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, e.h, method, target, body)
}

const volBase = "/api/profiles/synthetic-a/volumes"

func TestVolumeCreateSuccess(t *testing.T) {
	e := newVolEnv()
	e.fake.CreatedVolume = dockerc.Volume{Name: "data", Driver: "local", Created: time.Date(2026, 4, 11, 18, 2, 55, 0, time.UTC)}
	rec := e.do(t, "POST", volBase, `{"name":"data","labels":{"team":"synthetic-secret-value"}}`)
	if rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	if m["name"] != "data" || m["driver"] != "local" || m["createdAt"] != "2026-04-11T18:02:55Z" || m["sizeBytes"] != nil || m["containers"] != nil {
		t.Fatalf("body = %v", m)
	}
	if _, has := m["labels"]; has {
		t.Fatal("create answer must have the list fields only")
	}
	c := e.fake.CallsTo("CreateVolume")
	if len(c) != 1 || c[0].Args[0] != "data" || fmt.Sprint(c[0].Args[1]) != "map[team:synthetic-secret-value]" {
		t.Fatalf("calls = %+v", c)
	}
	rows := e.audit.Rows()
	if len(rows) != 1 || rows[0].Action != "docker.volume.create" || rows[0].Outcome != "success" || *rows[0].Detail != "data" ||
		strings.Contains(fmt.Sprintf("%+v", rows[0]), "synthetic-secret-value") {
		t.Fatalf("rows = %+v", rows)
	}
	// No labels, null labels, empty labels all work.
	for _, b := range []string{`{"name":"data"}`, `{"name":"data","labels":null}`, `{"name":"data","labels":{}}`} {
		if rec := e.do(t, "POST", volBase, b); rec.Code != 201 {
			t.Fatalf("%s: %d", b, rec.Code)
		}
	}
}

func TestVolumeCreateRefusals(t *testing.T) {
	many := `{`
	for i := 0; i < 17; i++ {
		many += fmt.Sprintf(`"k%d":"v",`, i)
	}
	many = strings.TrimSuffix(many, ",") + `}`
	tests := []struct {
		name, body string
		status     int
		code, key  string
	}{
		{"no body", ``, 400, "bad_request", ""},
		{"unknown key", `{"name":"a","driver":"nfs"}`, 400, "bad_request", ""},
		{"driver opts", `{"name":"a","driverOpts":{"device":"/x"}}`, 400, "bad_request", ""},
		{"wrong case", `{"Name":"a"}`, 400, "bad_request", ""},
		{"duplicate top key", `{"name":"a","name":"b"}`, 400, "bad_request", ""},
		{"labels not object", `{"name":"a","labels":["k"]}`, 422, "invalid_request", "labels"},
		{"labels string", `{"name":"a","labels":"k=v"}`, 422, "invalid_request", "labels"},
		{"label value number", `{"name":"a","labels":{"k":1}}`, 422, "invalid_request", "labels"},
		{"label value null", `{"name":"a","labels":{"k":null}}`, 422, "invalid_request", "labels"},
		{"label value object", `{"name":"a","labels":{"k":{"x":"y"}}}`, 422, "invalid_request", "labels"},
		{"duplicate label", `{"name":"a","labels":{"k":"1","k":"2"}}`, 422, "invalid_request", "labels"},
		{"case duplicate label", `{"name":"a","labels":{"Env":"1","env":"2"}}`, 422, "invalid_request", "labels"},
		{"17 labels", `{"name":"a","labels":` + many + `}`, 422, "invalid_request", "labels"},
		{"bad key", `{"name":"a","labels":{"k k":"v"}}`, 422, "invalid_request", "labels"},
		{"reserved key", `{"name":"a","labels":{"com.docker.compose.project":"v"}}`, 422, "invalid_request", "labels"},
		{"trailing labels data", `{"name":"a","labels":{"k":"v"} {"x":"y"}}`, 400, "bad_request", ""},
		{"zero width value", `{"name":"a","labels":{"k":"a\u200bb"}}`, 422, "invalid_request", "labels"},
		{"bidi value", `{"name":"a","labels":{"k":"a\u202eb"}}`, 422, "invalid_request", "labels"},
		{"zero width key", `{"name":"a","labels":{"k\u200b":"v"}}`, 422, "invalid_request", "labels"},
		{"control value", `{"name":"a","labels":{"k":"a\nb"}}`, 422, "invalid_request", "labels"},
		{"long value", `{"name":"a","labels":{"k":"` + strings.Repeat("v", 257) + `"}}`, 422, "invalid_request", "labels"},
		{"bad name", `{"name":"../a"}`, 422, "invalid_request", "name"},
		{"missing name", `{}`, 422, "invalid_request", "name"},
		{"oversize", `{"name":"a","labels":{"k":"` + strings.Repeat("v", 20000) + `"}}`, 400, "bad_request", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newVolEnv()
			rec := e.do(t, "POST", volBase, tc.body)
			wantErr(t, rec, tc.status, tc.code)
			if tc.key != "" {
				d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)
				f, _ := d["fields"].(map[string]any)
				if _, ok := f[tc.key]; !ok || len(f) != 1 {
					t.Fatalf("details = %v", d)
				}
			}
			for _, leak := range []string{"com.docker", "k k", "\\n", "../a"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Fatalf("answer echoes %q: %s", leak, rec.Body.String())
				}
			}
			if len(e.fake.Calls()) != 0 || len(e.provider.Profiles()) != 0 || len(e.audit.Rows()) != 0 {
				t.Fatal("Docker, provider or audit reached")
			}
		})
	}
}

func TestVolumeInspect(t *testing.T) {
	e := newVolEnv()
	size := int64(12)
	e.fake.VolumeDetail = dockerc.VolumeDetail{
		Name: "data", Driver: "local", Created: time.Date(2026, 4, 11, 18, 2, 55, 0, time.UTC),
		SizeBytes: &size, Labels: map[string]string{"k": "v"}, ContainerNames: []string{"web", "db"},
	}
	rec := e.do(t, "GET", volBase+"/data", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	if m["name"] != "data" || m["sizeBytes"] != float64(12) || m["containers"] != float64(2) ||
		m["labels"].(map[string]any)["k"] != "v" || len(m["containerNames"].([]any)) != 2 {
		t.Fatalf("body = %v", m)
	}
	for _, k := range []string{"mountpoint", "Mountpoint", "options", "status"} {
		if _, has := m[k]; has {
			t.Fatalf("key %q present", k)
		}
	}
	// Nil maps and slices from the client become {} and [].
	e.fake.VolumeDetail = dockerc.VolumeDetail{Name: "data"}
	rec = e.do(t, "GET", volBase+"/data", "")
	if !strings.Contains(rec.Body.String(), `"labels":{}`) || !strings.Contains(rec.Body.String(), `"containerNames":[]`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if rows := e.audit.Rows(); len(rows) != 0 {
		t.Fatalf("inspect is a read and leaves no row: %+v", rows)
	}
	// Not blocked by a running job either, and still a failure when Docker fails.
	*e.busy = fakeBusy{id: "j_1"}
	if rec := e.do(t, "GET", volBase+"/data", ""); rec.Code != 200 {
		t.Fatalf("busy profile: %d", rec.Code)
	}
	e.fake.FailWith("InspectVolume", dockerc.ErrObjectNotFound)
	wantErr(t, e.do(t, "GET", volBase+"/data", ""), 404, "not_found")
	if rows := e.audit.Rows(); len(rows) != 0 {
		t.Fatalf("failed inspect left a row: %+v", rows)
	}
	// A body on a GET is refused like on any no-body write route.
	wantErr(t, e.do(t, "GET", volBase+"/data", `{"a":1}`), 400, "bad_request")
}

func TestVolumeRemove(t *testing.T) {
	e := newVolEnv()
	rec := e.do(t, "DELETE", volBase+"/data", "")
	if rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	c := e.fake.CallsTo("RemoveVolume")
	if len(c) != 1 || len(c[0].Args) != 1 || c[0].Args[0] != "data" {
		t.Fatalf("calls = %+v (no extra argument such as force is possible)", c)
	}
	rows := e.audit.Rows()
	if len(rows) != 1 || rows[0].Action != "docker.volume.remove" || rows[0].Outcome != "success" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestVolumeRemoveErrors(t *testing.T) {
	secret := "/home/user/.colima/synthetic"
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"in use", &dockerc.InUseError{Containers: 3}, 409, "conflict"},
		{"wrapped in use", fmt.Errorf("x %s: %w", secret, &dockerc.InUseError{Containers: 3}), 409, "conflict"},
		{"docker 409", dockerc.ErrConflict, 409, "conflict"},
		{"unknown", dockerc.ErrObjectNotFound, 404, "not_found"},
		{"docker down", dockerc.ErrDockerUnavailable, 502, "docker_unavailable"},
		{"daemon", fmt.Errorf("%w %s", dockerc.ErrDaemon, secret), 502, "docker_unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newVolEnv()
			e.fake.FailWith("RemoveVolume", tc.err)
			rec := e.do(t, "DELETE", volBase+"/data", "")
			wantErr(t, rec, tc.status, tc.code)
			d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)
			if tc.code == "conflict" || tc.code == "not_found" {
				if d["name"] != "data" {
					t.Fatalf("details = %v", d)
				}
			}
			if strings.Contains(tc.name, "in use") {
				if d["containers"] != float64(3) {
					t.Fatalf("details = %v", d)
				}
			} else if _, has := d["containers"]; has {
				t.Fatalf("details = %v", d)
			}
			if strings.Contains(rec.Body.String(), secret) {
				t.Fatal("daemon text leaked")
			}
			if rows := e.audit.Rows(); len(rows) != 1 || rows[0].Outcome != "failed" {
				t.Fatalf("rows = %+v", rows)
			}
		})
	}
}

const pruneOK = `{"confirm":"delete-unused-volumes"}`

func TestVolumePrune(t *testing.T) {
	e := newVolEnv()
	n, size := 2, int64(1024)
	e.fake.PruneResult = dockerc.PruneResult{Removed: []string{"a", "b"}, RemovedCount: &n, ReclaimedBytes: &size, Complete: true}
	rec := e.do(t, "POST", volBase+"/prune", pruneOK)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	if len(m["removed"].([]any)) != 2 || m["removedCount"] != float64(2) || m["truncated"] != false ||
		m["reclaimedBytes"] != float64(1024) || m["includesNamedVolumes"] != true || m["resultComplete"] != true {
		t.Fatalf("body = %v", m)
	}
	e.fake.PruneResult = dockerc.PruneResult{Complete: true, RemovedCount: new(int), ReclaimedBytes: new(int64)}
	rec = e.do(t, "POST", volBase+"/prune", " "+pruneOK+"\n")
	if !strings.Contains(rec.Body.String(), `"removed":[]`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	rows := e.audit.Rows()
	if len(rows) != 2 || rows[0].Action != "docker.volume.prune" || rows[0].Detail != nil || rows[0].Outcome != "success" {
		t.Fatalf("rows = %+v", rows)
	}
}

// A prune whose answer Docker sent unreadable already deleted the volumes: the
// route says success with unknown figures and the log row says success.
func TestVolumePruneUnknownResultIsSuccess(t *testing.T) {
	e := newVolEnv()
	e.fake.PruneResult = dockerc.PruneResult{Removed: []string{}}
	rec := e.do(t, "POST", volBase+"/prune", pruneOK)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	if m["removedCount"] != nil || m["reclaimedBytes"] != nil || m["resultComplete"] != false ||
		m["includesNamedVolumes"] != true || len(m["removed"].([]any)) != 0 {
		t.Fatalf("body = %v", m)
	}
	if rows := e.audit.Rows(); len(rows) != 1 || rows[0].Outcome != "success" {
		t.Fatalf("rows = %+v", rows)
	}
	// Truncation is reported.
	n := 1500
	e.fake.PruneResult = dockerc.PruneResult{Removed: []string{"a"}, RemovedCount: &n, Truncated: true}
	m = decodeMap(t, e.do(t, "POST", volBase+"/prune", pruneOK))
	if m["truncated"] != true || m["removedCount"] != float64(1500) {
		t.Fatalf("body = %v", m)
	}
}

func TestVolumePruneNeedsTheConfirmation(t *testing.T) {
	for name, body := range map[string]string{
		"empty": ``, "spaces": "  \n", "empty object": `{}`, "null confirm": `{"confirm":null}`,
		"empty string": `{"confirm":""}`, "wrong string": `{"confirm":"yes"}`, "wrong case": `{"confirm":"DELETE-UNUSED-VOLUMES"}`,
		"trailing space": `{"confirm":"delete-unused-volumes "}`, "true": `{"confirm":"true"}`,
	} {
		e := newVolEnv()
		rec := e.do(t, "POST", volBase+"/prune", body)
		wantErr(t, rec, 422, "invalid_request")
		d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)
		if f, _ := d["fields"].(map[string]any); len(f) != 1 || f["confirm"] == nil {
			t.Fatalf("%s: details = %v", name, d)
		}
		if len(e.fake.Calls()) != 0 || len(e.audit.Rows()) != 0 {
			t.Fatalf("%s: Docker or the log reached", name)
		}
	}
	for name, body := range map[string]string{
		"extra key":  `{"confirm":"delete-unused-volumes","filters":{"all":["false"]}}`,
		"only extra": `{"filters":{}}`, "wrong case key": `{"Confirm":"delete-unused-volumes"}`,
		"duplicate": `{"confirm":"x","confirm":"delete-unused-volumes"}`, "not an object": `"delete-unused-volumes"`,
		"array": `[]`, "null": `null`, "garbage": `x`, "wrong type": `{"confirm":1}`,
		"trailing data": pruneOK + ` {}`, "oversize": `{"confirm":"` + strings.Repeat("a", 2000) + `"}`,
	} {
		e := newVolEnv()
		wantErr(t, e.do(t, "POST", volBase+"/prune", body), 400, "bad_request")
		if len(e.fake.Calls()) != 0 || len(e.audit.Rows()) != 0 {
			t.Fatalf("%s: Docker or the log reached", name)
		}
	}
	// GET never prunes, and the prune path is a volume name for GET.
	e := newVolEnv()
	rec := e.do(t, "GET", volBase+"/prune", "")
	if rec.Code != 200 || len(e.fake.CallsTo("PruneVolumes")) != 0 || len(e.fake.CallsTo("InspectVolume")) != 1 {
		t.Fatalf("GET prune: %d, calls %+v", rec.Code, e.fake.Calls())
	}
}

func TestVolumeCreateStopsWhenTheCheckFails(t *testing.T) {
	for name, err := range map[string]error{
		"docker 500": fmt.Errorf("%w: unexpected status 500", dockerc.ErrDockerUnavailable),
		"timeout":    fmt.Errorf("dockerc: request timed out: %w", context.DeadlineExceeded),
	} {
		e := newVolEnv()
		e.fake.FailWith("CreateVolume", err)
		wantErr(t, e.do(t, "POST", volBase, `{"name":"a"}`), 502, "docker_unavailable")
		if rows := e.audit.Rows(); len(rows) != 1 || rows[0].Outcome != "failed" {
			t.Fatalf("%s: rows = %+v", name, rows)
		}
	}
}

// TestVolumeRoutesSharedRules runs every route through the refusals of the
// shared helper: none may reach Docker or write a row.
func TestVolumeRoutesSharedRules(t *testing.T) {
	routes := []struct{ method, path, body string }{
		{"POST", volBase, `{"name":"a"}`},
		{"DELETE", volBase + "/a", ""},
		{"POST", volBase + "/prune", pruneOK},
	}
	setups := []struct {
		name   string
		setup  func(*volEnv)
		status int
		code   string
	}{
		{"unknown profile", func(e *volEnv) { e.gate.err = ErrProfileNotFound }, 404, "profile_not_found"},
		{"stopped", func(e *volEnv) { e.gate.status = "Stopped" }, 409, "invalid_state"},
		{"busy", func(e *volEnv) { *e.busy = fakeBusy{id: "j_1"} }, 409, "profile_busy"},
	}
	for _, r := range routes {
		for _, s := range setups {
			t.Run(r.method+" "+r.path+" "+s.name, func(t *testing.T) {
				e := newVolEnv()
				s.setup(e)
				wantErr(t, e.do(t, r.method, r.path, r.body), s.status, s.code)
				if len(e.fake.Calls()) != 0 || len(e.provider.Profiles()) != 0 || len(e.audit.Rows()) != 0 {
					t.Fatal("Docker, provider or audit reached")
				}
			})
		}
	}
	// Docker down, not-docker runtime, VM stopped under us.
	e := newVolEnv()
	e.provider.Err = dockerc.ErrDockerUnavailable
	wantErr(t, e.do(t, "DELETE", volBase+"/a", ""), 502, "docker_unavailable")
	e = newVolEnv()
	e.fake.FailWith("CreateVolume", dockerc.ErrDockerUnavailable)
	wantErr(t, e.do(t, "POST", volBase, `{"name":"a"}`), 502, "docker_unavailable")
	e = newVolEnv()
	e.provider.Err = dockerc.ErrRuntimeNotDocker
	wantErr(t, e.do(t, "POST", volBase+"/prune", pruneOK), 409, "invalid_state")
	e = newVolEnv()
	e.fake.FailWith("CreateVolume", dockerc.ErrConflict)
	wantErr(t, e.do(t, "POST", volBase, `{"name":"a"}`), 409, "conflict")
	e = newVolEnv()
	e.fake.FailWith("CreateVolume", dockerc.ErrInvalidRequest)
	wantErr(t, e.do(t, "POST", volBase, `{"name":"a"}`), 422, "invalid_request")
}

func TestVolumePathParameterHostileNames(t *testing.T) {
	for _, p := range []string{"%2e%2e", "a%2Fb", "a%0Ab", "a%00b", strings.Repeat("a", 129), "a%20b", "%2e", "a%5Cb", "a%3Fb", "a%23b"} {
		for _, method := range []string{"GET", "DELETE"} {
			e := newVolEnv()
			rec := e.do(t, method, volBase+"/"+p, "")
			if rec.Code != 422 && rec.Code != 404 && rec.Code != 301 && rec.Code != 307 {
				t.Errorf("%s %s: %d", method, p, rec.Code)
			}
			if len(e.fake.Calls()) != 0 || len(e.audit.Rows()) != 0 {
				t.Errorf("%s %s reached Docker or the log", method, p)
			}
		}
	}
	// The prune path is not a volume named prune for POST; GET and DELETE of
	// that name are ordinary volume calls.
	e := newVolEnv()
	if rec := e.do(t, "DELETE", volBase+"/prune", ""); rec.Code != 204 {
		t.Fatalf("delete volume named prune: %d", rec.Code)
	}
	if c := e.fake.CallsTo("RemoveVolume"); len(c) != 1 || c[0].Args[0] != "prune" {
		t.Fatalf("calls = %+v", c)
	}
	// Wrong methods.
	wantErr(t, e.do(t, "PUT", volBase+"/a", ""), 405, "method_not_allowed")
	wantErr(t, e.do(t, "POST", volBase+"/a", `{}`), 405, "method_not_allowed")
	wantErr(t, e.do(t, "DELETE", volBase, ""), 405, "method_not_allowed")
}

func TestVolumeRoutesNeedToken(t *testing.T) {
	e := newVolEnv()
	for _, r := range [][2]string{{"POST", volBase}, {"GET", volBase + "/a"}, {"DELETE", volBase + "/a"}, {"POST", volBase + "/prune"}} {
		req := httptest.NewRequest(r[0], r[1], strings.NewReader(`{"name":"a"}`))
		req.Host = goodHost
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Errorf("%v: %d", r, rec.Code)
		}
	}
	if len(e.fake.Calls()) != 0 {
		t.Fatal("docker reached without a token")
	}
}

func TestParseLabelsRefusesTrailingData(t *testing.T) {
	for _, raw := range []string{`{"k":"v"} {"x":"y"}`, `{"k":"v"}x`, `{"k":"v"} []`} {
		if _, err := parseLabels([]byte(raw)); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	if m, err := parseLabels([]byte(" {\"k\":\"v\"} ")); err != nil || m["k"] != "v" {
		t.Fatalf("%v %v", m, err)
	}
}

func TestVolumeInspectStillNeedsARunningKnownProfile(t *testing.T) {
	e := newVolEnv()
	e.gate.status = "Stopped"
	wantErr(t, e.do(t, "GET", volBase+"/a", ""), 409, "invalid_state")
	e = newVolEnv()
	e.gate.err = ErrProfileNotFound
	wantErr(t, e.do(t, "GET", volBase+"/a", ""), 404, "profile_not_found")
	if len(e.fake.Calls()) != 0 {
		t.Fatal("docker reached")
	}
}
