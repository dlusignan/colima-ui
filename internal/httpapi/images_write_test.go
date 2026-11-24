package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/dockerc/dockerctest"
)

type imgEnv struct {
	gate     *fakeGate
	busy     *fakeBusy
	fake     *dockerctest.Fake
	provider *dockerctest.Provider
	audit    *recordingAudit
	h        http.Handler
}

func newImgEnv() *imgEnv {
	e := &imgEnv{gate: &fakeGate{status: "Running"}, busy: &fakeBusy{}, fake: &dockerctest.Fake{}, audit: &recordingAudit{}}
	e.provider = &dockerctest.Provider{Client: e.fake}
	e.h = NewHandler(testPort, testToken, NewAPI(WithImages(e.gate, e.provider, e.busy, e.audit)), nil)
	return e
}

func (e *imgEnv) do(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, e.h, method, target, body)
}

func (e *imgEnv) untouched() bool {
	return len(e.fake.Calls()) == 0 && len(e.provider.Profiles()) == 0 && len(e.audit.Rows()) == 0
}

const imgBase = "/api/profiles/synthetic-a/images"

func fieldsOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)
	f, _ := d["fields"].(map[string]any)
	return f
}

func TestImageTagSuccess(t *testing.T) {
	e := newImgEnv()
	rec := e.do(t, "POST", imgBase+"/0123456789ab/tag", `{"repository":"registry.example.com:5000/team/app","tag":"v2"}`)
	if rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	c := e.fake.CallsTo("TagImage")
	if len(c) != 1 || fmt.Sprint(c[0].Args) != "[0123456789ab registry.example.com:5000/team/app v2]" {
		t.Fatalf("calls = %+v", c)
	}
	rows := e.audit.Rows()
	if len(rows) != 1 || rows[0].Action != "docker.image.tag" || rows[0].Outcome != "success" || *rows[0].Detail != "0123456789ab -> registry.example.com:5000/team/app:v2" {
		t.Fatalf("rows = %+v", rows)
	}
	e = newImgEnv()
	if rec := e.do(t, "POST", imgBase+"/team%2Fapp%3Av1/tag", `{"repository":"app","tag":"latest"}`); rec.Code != 204 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if c := e.fake.CallsTo("TagImage"); len(c) != 1 || c[0].Args[0] != "team/app:v1" {
		t.Fatalf("calls = %+v", c)
	}
}

func TestImageTagRefusals(t *testing.T) {
	long := strings.Repeat("a", 129)
	tests := []struct {
		name, path, body string
		status           int
		code             string
		keys             []string
	}{
		{"no body", "/a/tag", ``, 400, "bad_request", nil},
		{"unknown key", "/a/tag", `{"repository":"a","tag":"b","force":true}`, 400, "bad_request", nil},
		{"wrong case", "/a/tag", `{"Repository":"a","tag":"b"}`, 400, "bad_request", nil},
		{"duplicate", "/a/tag", `{"repository":"a","repository":"b","tag":"b"}`, 400, "bad_request", nil},
		{"tag number", "/a/tag", `{"repository":"a","tag":1}`, 400, "bad_request", nil},
		{"oversize", "/a/tag", `{"repository":"` + strings.Repeat("a", 5000) + `","tag":"b"}`, 400, "bad_request", nil},
		{"missing both", "/a/tag", `{}`, 422, "invalid_request", []string{"repository", "tag"}},
		{"missing tag", "/a/tag", `{"repository":"a"}`, 422, "invalid_request", []string{"tag"}},
		{"missing repository", "/a/tag", `{"tag":"v1"}`, 422, "invalid_request", []string{"repository"}},
		{"null tag", "/a/tag", `{"repository":"a","tag":null}`, 422, "invalid_request", []string{"tag"}},
		{"repository with tag", "/a/tag", `{"repository":"a:b","tag":"v1"}`, 422, "invalid_request", []string{"repository"}},
		{"repository with digest", "/a/tag", `{"repository":"a@sha256:` + strings.Repeat("a", 64) + `","tag":"v1"}`, 422, "invalid_request", []string{"repository"}},
		{"uppercase repository", "/a/tag", `{"repository":"App","tag":"v1"}`, 422, "invalid_request", []string{"repository"}},
		{"traversal repository", "/a/tag", `{"repository":"../x","tag":"v1"}`, 422, "invalid_request", []string{"repository"}},
		{"query injection repository", "/a/tag", `{"repository":"a&force=1","tag":"v1"}`, 422, "invalid_request", []string{"repository"}},
		{"zero width repository", "/a/tag", `{"repository":"a​b","tag":"v1"}`, 422, "invalid_request", []string{"repository"}},
		{"id shaped repository", "/a/tag", `{"repository":"` + strings.Repeat("a", 64) + `","tag":"v1"}`, 422, "invalid_request", []string{"repository"}},
		{"port only", "/a/tag", `{"repository":"host:5000","tag":"v1"}`, 422, "invalid_request", []string{"repository"}},
		{"empty tag", "/a/tag", `{"repository":"a","tag":""}`, 422, "invalid_request", []string{"tag"}},
		{"long tag", "/a/tag", `{"repository":"a","tag":"` + strings.Repeat("a", 129) + `"}`, 422, "invalid_request", []string{"tag"}},
		{"dash tag", "/a/tag", `{"repository":"a","tag":"-x"}`, 422, "invalid_request", []string{"tag"}},
		{"slash tag", "/a/tag", `{"repository":"a","tag":"a/b"}`, 422, "invalid_request", []string{"tag"}},
		{"colon tag", "/a/tag", `{"repository":"a","tag":"a:b"}`, 422, "invalid_request", []string{"tag"}},
		{"space tag", "/a/tag", `{"repository":"a","tag":"a b"}`, 422, "invalid_request", []string{"tag"}},
		{"zero width tag", "/a/tag", `{"repository":"a","tag":"v​1"}`, 422, "invalid_request", []string{"tag"}},
		{"bidi tag", "/a/tag", `{"repository":"a","tag":"v‮1"}`, 422, "invalid_request", []string{"tag"}},
		{"newline tag", "/a/tag", `{"repository":"a","tag":"v1\n"}`, 422, "invalid_request", []string{"tag"}},
		{"total too long", "/a/tag", `{"repository":"` + strings.Repeat("a", 200) + `/` + strings.Repeat("b", 40) + `","tag":"` + strings.Repeat("t", 128) + `"}`, 422, "invalid_request", []string{"repository"}},
		{"bad image", "/A%20b/tag", `{"repository":"a","tag":"v1"}`, 422, "invalid_request", []string{"image"}},
		{"long image", "/" + long + long + long + "/tag", `{"repository":"a","tag":"v1"}`, 422, "invalid_request", []string{"image"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newImgEnv()
			rec := e.do(t, "POST", imgBase+tc.path, tc.body)
			wantErr(t, rec, tc.status, tc.code)
			if tc.keys != nil {
				f := fieldsOf(t, rec)
				if len(f) != len(tc.keys) {
					t.Fatalf("fields = %v", f)
				}
				for _, k := range tc.keys {
					if f[k] == nil {
						t.Fatalf("fields = %v, want %s", f, k)
					}
				}
			}
			for _, leak := range []string{"../x", "force=1", "u200b"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Fatalf("answer echoes %q", leak)
				}
			}
			if !e.untouched() {
				t.Fatal("Docker, provider or audit reached")
			}
		})
	}
}

func TestImageInspect(t *testing.T) {
	e := newImgEnv()
	created := time.Date(2026, 5, 2, 9, 41, 7, 0, time.UTC)
	e.fake.ImageDetail = dockerc.ImageDetail{
		ID: "0123456789ab", Tags: []string{"app:v1"}, Digests: []string{"app@sha256:" + strings.Repeat("a", 64)},
		SizeBytes: 100, Created: created, Architecture: "amd64", OS: "linux", ExposedPorts: []int{80, 443},
		ContainerNames: []string{"web"}, Layers: []dockerc.ImageLayer{{Created: created, SizeBytes: 5}, {SizeBytes: 0}}, LayerCount: 2,
	}
	rec := e.do(t, "GET", imgBase+"/0123456789ab", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	layers := m["layers"].([]any)
	if m["id"] != "0123456789ab" || m["containers"] != float64(1) || m["architecture"] != "amd64" || m["os"] != "linux" ||
		m["createdAt"] != "2026-05-02T09:41:07Z" || len(m["exposedPorts"].([]any)) != 2 || m["layerCount"] != float64(2) ||
		len(layers) != 2 || layers[1].(map[string]any)["createdAt"] != nil || len(layers[0].(map[string]any)) != 2 || len(m) != 12 {
		t.Fatalf("body = %v", m)
	}
	e.fake.ImageDetail = dockerc.ImageDetail{}
	rec = e.do(t, "GET", imgBase+"/0123456789ab", "")
	for _, want := range []string{`"tags":[]`, `"digests":[]`, `"exposedPorts":[]`, `"containerNames":[]`, `"layers":[]`, `"createdAt":null`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("missing %s in %s", want, rec.Body.String())
		}
	}
	if len(e.audit.Rows()) != 0 {
		t.Fatalf("inspect is a read and leaves no row: %+v", e.audit.Rows())
	}
	*e.busy = fakeBusy{id: "j_1"}
	if rec := e.do(t, "GET", imgBase+"/app%3Av1", ""); rec.Code != 200 {
		t.Fatalf("busy profile: %d", rec.Code)
	}
	e.fake.FailWith("InspectImage", dockerc.ErrObjectNotFound)
	rec = e.do(t, "GET", imgBase+"/app%3Av1", "")
	wantErr(t, rec, 404, "not_found")
	if d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any); d["name"] != "app:v1" {
		t.Fatalf("details = %v", d)
	}
	if len(e.audit.Rows()) != 0 {
		t.Fatal("row written")
	}
	wantErr(t, e.do(t, "GET", imgBase+"/a", `{"a":1}`), 400, "bad_request")
	e2 := newImgEnv()
	e2.gate.status = "Stopped"
	wantErr(t, e2.do(t, "GET", imgBase+"/a", ""), 409, "invalid_state")
	if len(e2.fake.Calls()) != 0 {
		t.Fatal("docker reached")
	}
}

func TestImageRemove(t *testing.T) {
	e := newImgEnv()
	n := 3
	e.fake.ImageRemoval = dockerc.ImageRemoval{Untagged: []string{"app:v1"}, Deleted: []string{"0123456789ab"}, Count: &n, Complete: true}
	rec := e.do(t, "DELETE", imgBase+"/app%3Av1", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	if len(m["untagged"].([]any)) != 1 || len(m["deleted"].([]any)) != 1 || m["removedCount"] != float64(3) ||
		m["truncated"] != false || m["resultComplete"] != true || len(m) != 5 {
		t.Fatalf("body = %v", m)
	}
	if c := e.fake.CallsTo("RemoveImage"); len(c) != 1 || c[0].Args[0] != "app:v1" {
		t.Fatalf("calls = %+v", c)
	}
	rows := e.audit.Rows()
	if len(rows) != 1 || rows[0].Action != "docker.image.remove" || *rows[0].Detail != "app:v1" || rows[0].Outcome != "success" {
		t.Fatalf("rows = %+v", rows)
	}
	e.fake.ImageRemoval = dockerc.ImageRemoval{}
	rec = e.do(t, "DELETE", imgBase+"/0123456789ab", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"removedCount":null`) || !strings.Contains(rec.Body.String(), `"resultComplete":false`) ||
		!strings.Contains(rec.Body.String(), `"untagged":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestImageRemoveErrors(t *testing.T) {
	secret := "/home/user/.colima/synthetic"
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"in use", &dockerc.InUseError{Containers: 3}, 409, "conflict"},
		{"wrapped in use", fmt.Errorf("x %s: %w", secret, &dockerc.InUseError{Containers: 3}), 409, "conflict"},
		{"docker refusal", dockerc.ErrConflict, 409, "conflict"},
		{"unknown", dockerc.ErrObjectNotFound, 404, "not_found"},
		{"docker down", dockerc.ErrDockerUnavailable, 502, "docker_unavailable"},
		{"daemon", fmt.Errorf("%w %s", dockerc.ErrDaemon, secret), 502, "docker_unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newImgEnv()
			e.fake.FailWith("RemoveImage", tc.err)
			rec := e.do(t, "DELETE", imgBase+"/app%3Av1", "")
			wantErr(t, rec, tc.status, tc.code)
			d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)
			if tc.code != "docker_unavailable" && d["name"] != "app:v1" {
				t.Fatalf("details = %v", d)
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

func TestImageTagErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"target tag on another image": {dockerc.ErrConflict, 409, "conflict"},
		"unknown":                     {dockerc.ErrObjectNotFound, 404, "not_found"},
		"rejected":                    {dockerc.ErrInvalidRequest, 422, "invalid_request"},
		"docker 500":                  {fmt.Errorf("%w: /home/user/x", dockerc.ErrDaemon), 502, "docker_unavailable"},
	} {
		e := newImgEnv()
		e.fake.FailWith("TagImage", tc.err)
		rec := e.do(t, "POST", imgBase+"/app/tag", `{"repository":"a","tag":"b"}`)
		wantErr(t, rec, tc.status, tc.code)
		if strings.Contains(rec.Body.String(), "/home/user") {
			t.Fatalf("%s: leaked", name)
		}
		if rows := e.audit.Rows(); len(rows) != 1 || rows[0].Outcome != "failed" {
			t.Fatalf("%s: rows = %+v", name, rows)
		}
	}
}

const imgPruneOK = `{"confirm":"delete-unused-images"}`

func TestImagePrune(t *testing.T) {
	e := newImgEnv()
	n, b := 2, int64(1000)
	e.fake.ImagePrune = dockerc.ImagePruneResult{
		ImageRemoval:   dockerc.ImageRemoval{Untagged: []string{"a:1"}, Deleted: []string{"0123456789ab"}, Count: &n, Complete: true},
		ReclaimedBytes: &b,
	}
	rec := e.do(t, "POST", imgBase+"/prune", imgPruneOK)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	if m["removedCount"] != float64(2) || m["reclaimedBytes"] != float64(1000) || m["all"] != false || m["resultComplete"] != true || len(m) != 7 {
		t.Fatalf("body = %v", m)
	}
	rec = e.do(t, "POST", imgBase+"/prune", `{"confirm":"delete-unused-images","all":true}`)
	if m := decodeMap(t, rec); m["all"] != true {
		t.Fatalf("body = %v", m)
	}
	c := e.fake.CallsTo("PruneImages")
	if len(c) != 2 || c[0].Args[0] != false || c[1].Args[0] != true {
		t.Fatalf("calls = %+v", c)
	}
	rows := e.audit.Rows()
	if len(rows) != 2 || rows[0].Action != "docker.image.prune" || rows[0].Detail != nil {
		t.Fatalf("rows = %+v", rows)
	}
	e.fake.ImagePrune = dockerc.ImagePruneResult{}
	rec = e.do(t, "POST", imgBase+"/prune", imgPruneOK)
	m = decodeMap(t, rec)
	if rec.Code != 200 || m["removedCount"] != nil || m["reclaimedBytes"] != nil || m["resultComplete"] != false {
		t.Fatalf("%d %v", rec.Code, m)
	}
}

func TestImagePruneNeedsTheConfirmation(t *testing.T) {
	for name, body := range map[string]string{
		"empty": ``, "empty object": `{}`, "all only": `{"all":true}`, "null confirm": `{"confirm":null}`, "empty string": `{"confirm":""}`,
		"wrong": `{"confirm":"yes"}`, "wrong case": `{"confirm":"DELETE-UNUSED-IMAGES"}`, "volume text": `{"confirm":"delete-unused-volumes"}`,
		"network text": `{"confirm":"delete-unused-networks"}`, "trailing space": `{"confirm":"delete-unused-images "}`,
		"all with wrong": `{"confirm":"x","all":true}`,
	} {
		e := newImgEnv()
		rec := e.do(t, "POST", imgBase+"/prune", body)
		wantErr(t, rec, 422, "invalid_request")
		if f := fieldsOf(t, rec); len(f) != 1 || f["confirm"] == nil {
			t.Fatalf("%s: fields = %v", name, f)
		}
		if !e.untouched() {
			t.Fatalf("%s: reached Docker", name)
		}
	}
	for name, body := range map[string]string{
		"filters": `{"confirm":"delete-unused-images","filters":{"until":["1h"]}}`, "until": `{"confirm":"delete-unused-images","until":"1h"}`,
		"all string": `{"confirm":"delete-unused-images","all":"true"}`, "all number": `{"confirm":"delete-unused-images","all":1}`,
		"wrong case key": `{"Confirm":"delete-unused-images"}`, "duplicate": `{"confirm":"x","confirm":"delete-unused-images"}`,
		"array": `[]`, "null": `null`, "garbage": `x`, "trailing": imgPruneOK + ` {}`, "oversize": `{"confirm":"` + strings.Repeat("a", 2000) + `"}`,
		"duplicate all": `{"confirm":"delete-unused-images","all":false,"all":true}`,
	} {
		e := newImgEnv()
		wantErr(t, e.do(t, "POST", imgBase+"/prune", body), 400, "bad_request")
		if !e.untouched() {
			t.Fatalf("%s: reached Docker", name)
		}
	}
	e := newImgEnv()
	if rec := e.do(t, "GET", imgBase+"/prune", ""); rec.Code != 200 || len(e.fake.CallsTo("PruneImages")) != 0 {
		t.Fatalf("GET prune: %d", rec.Code)
	}
	if rec := e.do(t, "DELETE", imgBase+"/prune", ""); rec.Code != 200 || len(e.fake.CallsTo("PruneImages")) != 0 || e.fake.CallsTo("RemoveImage")[0].Args[0] != "prune" {
		t.Fatalf("DELETE prune: %d", rec.Code)
	}
}

func TestImageRoutesSharedRules(t *testing.T) {
	routes := []struct{ method, path, body string }{
		{"POST", imgBase + "/a/tag", `{"repository":"a","tag":"b"}`},
		{"DELETE", imgBase + "/a", ""},
		{"POST", imgBase + "/prune", imgPruneOK},
	}
	setups := []struct {
		name   string
		setup  func(*imgEnv)
		status int
		code   string
	}{
		{"unknown profile", func(e *imgEnv) { e.gate.err = ErrProfileNotFound }, 404, "profile_not_found"},
		{"stopped", func(e *imgEnv) { e.gate.status = "Stopped" }, 409, "invalid_state"},
		{"busy", func(e *imgEnv) { *e.busy = fakeBusy{id: "j_1"} }, 409, "profile_busy"},
	}
	for _, r := range routes {
		for _, s := range setups {
			t.Run(r.method+" "+r.path+" "+s.name, func(t *testing.T) {
				e := newImgEnv()
				s.setup(e)
				wantErr(t, e.do(t, r.method, r.path, r.body), s.status, s.code)
				if !e.untouched() {
					t.Fatal("Docker, provider or audit reached")
				}
			})
		}
	}
	e := newImgEnv()
	e.provider.Err = dockerc.ErrDockerUnavailable
	wantErr(t, e.do(t, "DELETE", imgBase+"/a", ""), 502, "docker_unavailable")
	e = newImgEnv()
	e.provider.Err = dockerc.ErrRuntimeNotDocker
	wantErr(t, e.do(t, "POST", imgBase+"/prune", imgPruneOK), 409, "invalid_state")
}

func TestImagePathParameterHostileRefs(t *testing.T) {
	for _, p := range []string{"%2e%2e", "a%0Ab", "a%00b", strings.Repeat("a", 300), "a%20b", "%2e", "a%5Cb", "a%3Fb", "a%23b", "a%E2%80%8Bb", "a%E2%80%AEb",
		"A", "a%2F%2Fb", "%2Fa", "a%2F", "a%2F..%2Fb", "a%25b", "a%3Bb", "a:b:c"} {
		for _, method := range []string{"GET", "DELETE"} {
			e := newImgEnv()
			rec := e.do(t, method, imgBase+"/"+p, "")
			if rec.Code != 422 && rec.Code != 404 && rec.Code != 301 && rec.Code != 307 && rec.Code != 400 {
				t.Errorf("%s %s: %d", method, p, rec.Code)
			}
			if !e.untouched() {
				t.Errorf("%s %s reached Docker or the log", method, p)
			}
		}
		e := newImgEnv()
		rec := e.do(t, "POST", imgBase+"/"+p+"/tag", `{"repository":"a","tag":"b"}`)
		if rec.Code == 204 || !e.untouched() {
			t.Errorf("tag %s: %d", p, rec.Code)
		}
	}
	e := newImgEnv()
	wantErr(t, e.do(t, "PUT", imgBase+"/a", ""), 405, "method_not_allowed")
	wantErr(t, e.do(t, "POST", imgBase+"/a", `{}`), 405, "method_not_allowed")
	wantErr(t, e.do(t, "DELETE", imgBase, ""), 405, "method_not_allowed")
	wantErr(t, e.do(t, "GET", imgBase+"/a/tag", ""), 405, "method_not_allowed")
}

func TestImageRoutesNeedToken(t *testing.T) {
	e := newImgEnv()
	for _, r := range [][2]string{{"POST", imgBase + "/a/tag"}, {"GET", imgBase + "/a"}, {"DELETE", imgBase + "/a"}, {"POST", imgBase + "/prune"}} {
		req := httptest.NewRequest(r[0], r[1], strings.NewReader(`{}`))
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

// A removal that stopped part-way answers with the error and says what was
// already removed (validated strings only).
func TestImageRemovePartialFailureReportsWhatWasRemoved(t *testing.T) {
	secret := "/home/user/.colima/synthetic"
	for name, tc := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"conflict": {dockerc.ErrConflict, 409, "conflict"},
		"daemon":   {fmt.Errorf("%w %s", dockerc.ErrDaemon, secret), 502, "docker_unavailable"},
	} {
		e := newImgEnv()
		e.fake.FailWith("RemoveImage", &dockerc.PartialRemovalError{
			Removal: dockerc.ImageRemoval{Untagged: []string{"app:v1"}, Deleted: nil}, Err: tc.err,
		})
		rec := e.do(t, "DELETE", imgBase+"/0123456789ab", "")
		wantErr(t, rec, tc.status, tc.code)
		d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)
		if u, _ := d["untagged"].([]any); len(u) != 1 || u[0] != "app:v1" {
			t.Fatalf("%s: details = %v", name, d)
		}
		if dl, ok := d["deleted"].([]any); !ok || len(dl) != 0 {
			t.Fatalf("%s: details = %v", name, d)
		}
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("%s: leaked", name)
		}
		if rows := e.audit.Rows(); len(rows) != 1 || rows[0].Outcome != "failed" {
			t.Fatalf("%s: rows = %+v", name, rows)
		}
	}
	// A plain error carries no such keys.
	e := newImgEnv()
	e.fake.FailWith("RemoveImage", dockerc.ErrConflict)
	d := decodeMap(t, e.do(t, "DELETE", imgBase+"/a", ""))["error"].(map[string]any)["details"].(map[string]any)
	if _, has := d["untagged"]; has {
		t.Fatalf("details = %v", d)
	}
}

func TestImageTagRefusesTheSha256Repository(t *testing.T) {
	e := newImgEnv()
	rec := e.do(t, "POST", imgBase+"/a/tag", `{"repository":"sha256","tag":"`+strings.Repeat("a", 64)+`"}`)
	wantErr(t, rec, 422, "invalid_request")
	if !e.untouched() {
		t.Fatal("reached Docker")
	}
}
