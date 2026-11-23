package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dlusignan/colima-ui/internal/config"
	"github.com/dlusignan/colima-ui/internal/operations"
)

type fakeConfig struct {
	mu       sync.Mutex
	profile  *config.Profile
	readErr  error
	readErrs []error // consumed one per Read, before readErr
	applyErr error
	changes  []config.Change
	applied  []config.Changes
	applyN   int
	readN    int
}

func (f *fakeConfig) Read(string) (*config.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readN++
	if len(f.readErrs) > 0 {
		e := f.readErrs[0]
		f.readErrs = f.readErrs[1:]
		if e != nil {
			return nil, e
		}
	} else if f.readErr != nil {
		return nil, f.readErr
	}
	return f.profile, nil
}

func (f *fakeConfig) Apply(_ string, ch config.Changes) ([]config.Change, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applyN++
	f.applied = append(f.applied, ch)
	return f.changes, f.applyErr
}

type fakeBusy struct{ id string }

func (b fakeBusy) Busy(string) (string, bool) { return b.id, b.id != "" }

func ci(n int) *int         { return &n }
func cf(f float64) *float64 { return &f }

func cfgReq(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return m
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	m := decodeMap(t, rec)
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func cfgAPI(svc ConfigService, busy ConfigBusy) http.Handler {
	return NewAPI(WithConfig(svc, busy))
}

func TestConfigGet(t *testing.T) {
	f := &fakeConfig{profile: &config.Profile{CPU: ci(4), Memory: cf(8), Disk: ci(120)}}
	rec := cfgReq(t, cfgAPI(f, fakeBusy{}), "GET", "/api/profiles/dev/config", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	want := `{"name":"dev","cpu":4,"memoryGiB":8,"diskGiB":120}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestConfigGetFillsDefaultsAndKeepsFractionalMemory(t *testing.T) {
	f := &fakeConfig{profile: &config.Profile{}}
	rec := cfgReq(t, cfgAPI(f, fakeBusy{}), "GET", "/api/profiles/default/config", "")
	if got := strings.TrimSpace(rec.Body.String()); got != `{"name":"default","cpu":2,"memoryGiB":2,"diskGiB":100}` {
		t.Fatalf("defaults: %s", got)
	}
	f.profile = &config.Profile{Memory: cf(2.5)}
	rec = cfgReq(t, cfgAPI(f, fakeBusy{}), "GET", "/api/profiles/default/config", "")
	if m := decodeMap(t, rec); m["memoryGiB"] != 2.5 {
		t.Fatalf("fractional: %v", m)
	}
}

func TestConfigGetErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not found", config.ErrProfileNotFound, 404, "profile_not_found"},
		{"invalid name", fmt.Errorf("%w: x", config.ErrInvalidProfileName), 404, "profile_not_found"},
		{"parse", &config.ParseError{Line: 3, Message: "secret/path"}, 500, "internal_error"},
		{"type", &config.TypeError{Problems: []string{"cpu"}}, 500, "internal_error"},
		{"read", &config.ReadError{Err: errors.New("/home/user/.colima/x boom")}, 500, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := cfgReq(t, cfgAPI(&fakeConfig{readErr: tc.err}, fakeBusy{}), "GET", "/api/profiles/dev/config", "")
			if rec.Code != tc.status || errCode(t, rec) != tc.code {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if b := rec.Body.String(); strings.Contains(b, "secret") || strings.Contains(b, "/home") || strings.Contains(b, "boom") {
				t.Fatalf("leak: %s", b)
			}
		})
	}
}

func TestConfigMalformedNameNotEchoed(t *testing.T) {
	f := &fakeConfig{readErr: config.ErrInvalidProfileName}
	rec := cfgReq(t, cfgAPI(f, fakeBusy{}), "GET", "/api/profiles/%3Cscript%3E/config", "")
	if rec.Code != 404 || strings.Contains(rec.Body.String(), "script") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestConfigPutSuccessMapsFieldNames(t *testing.T) {
	f := &fakeConfig{
		profile: &config.Profile{CPU: ci(6), Memory: cf(12), Disk: ci(100)},
		changes: []config.Change{{Field: "cpu", Class: config.ClassRestart}, {Field: "memory", Class: config.ClassRestart}, {Field: "disk", Class: config.ClassRecreate}},
	}
	rec := cfgReq(t, cfgAPI(f, fakeBusy{}), "PUT", "/api/profiles/default/config", `{"cpu":6,"memoryGiB":12}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	want := `{"config":{"name":"default","cpu":6,"memoryGiB":12,"diskGiB":100},"restartRequired":["cpu","memoryGiB","diskGiB"]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("got %s", got)
	}
	a := f.applied[0]
	if a.CPU == nil || *a.CPU != 6 || a.Memory == nil || *a.Memory != 12 || a.Disk != nil {
		t.Fatalf("applied %+v", a)
	}
}

func TestConfigPutNoRestartEncodesEmptyArray(t *testing.T) {
	for _, ch := range [][]config.Change{nil, {}} {
		f := &fakeConfig{profile: &config.Profile{}, changes: ch}
		rec := cfgReq(t, cfgAPI(f, fakeBusy{}), "PUT", "/api/profiles/default/config", `{"diskGiB":50}`)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"restartRequired":[]`) {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
}

func TestConfigPutEmptyObjectIsNoOp(t *testing.T) {
	for _, body := range []string{`{}`, `{"cpu":null}`, `null`} {
		f := &fakeConfig{profile: &config.Profile{CPU: ci(3)}}
		rec := cfgReq(t, cfgAPI(f, fakeBusy{}), "PUT", "/api/profiles/default/config", body)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"restartRequired":[]`) || !strings.Contains(rec.Body.String(), `"cpu":3`) {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
		if a := f.applied[0]; a.CPU != nil || a.Memory != nil || a.Disk != nil {
			t.Fatalf("%s: applied %+v", body, a)
		}
	}
}

func TestConfigPutBadBodyNeverReachesService(t *testing.T) {
	big := `{"cpu":1,"pad":"` + strings.Repeat("a", 2<<20) + `"}`
	bodies := map[string]string{
		"unknown field":     `{"cpu":2,"hostname":"x"}`,
		"unknown only":      `{"memory":4}`,
		"name field":        `{"name":"other","cpu":2}`,
		"fractional cpu":    `{"cpu":2.5}`,
		"string cpu":        `{"cpu":"2"}`,
		"fractional disk":   `{"diskGiB":10.5}`,
		"string memory":     `{"memoryGiB":"4"}`,
		"huge number":       `{"memoryGiB":1e999}`,
		"overflow int":      `{"cpu":99999999999999999999}`,
		"trailing data":     `{"cpu":2}{"cpu":3}`,
		"trailing garbage":  `{"cpu":2} x`,
		"array":             `[1]`,
		"malformed":         `{"cpu":`,
		"not json":          `cpu=2`,
		"oversized body":    big,
		"duplicate unknown": `{"cpu":2,"cpu2":1}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			f := &fakeConfig{profile: &config.Profile{}}
			rec := cfgReq(t, cfgAPI(f, fakeBusy{}), "PUT", "/api/profiles/default/config", body)
			if rec.Code != 400 || errCode(t, rec) != "bad_request" {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if f.applyN != 0 {
				t.Fatal("Apply reached with a bad body")
			}
		})
	}
}

func TestConfigPutValidation422(t *testing.T) {
	f := &fakeConfig{profile: &config.Profile{}, applyErr: &config.ValidationError{Fields: []config.FieldError{
		{Field: "cpu", Message: "must be between 1 and 16"},
		{Field: "memory", Message: "must be between 1 and 64 GiB"},
		{Field: "disk", Message: "cannot be smaller than the current 60 GiB"},
	}}}
	rec := cfgReq(t, cfgAPI(f, fakeBusy{}), "PUT", "/api/profiles/default/config", `{"cpu":99,"memoryGiB":0,"diskGiB":1}`)
	if rec.Code != 422 || errCode(t, rec) != "validation_failed" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	e := decodeMap(t, rec)["error"].(map[string]any)
	fields := e["details"].(map[string]any)["fields"].(map[string]any)
	if len(fields) != 3 || fields["cpu"] != "must be between 1 and 16" || fields["memoryGiB"] == nil || fields["diskGiB"] == nil {
		t.Fatalf("fields %v", fields)
	}
	if _, bad := fields["memory"]; bad {
		t.Fatal("unmapped config name leaked")
	}
	if f.readN != 0 {
		t.Fatal("re-read after a failed apply")
	}
}

func TestConfigPutBusy(t *testing.T) {
	err := &config.BusyError{Profile: "default"}
	rec := cfgReq(t, cfgAPI(&fakeConfig{applyErr: err}, fakeBusy{id: "j_abc"}), "PUT", "/api/profiles/default/config", `{"cpu":3}`)
	if rec.Code != 409 || errCode(t, rec) != "profile_busy" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)
	if d["jobId"] != "j_abc" {
		t.Fatalf("details %v", d)
	}
	// A write hold has no job: the id is simply omitted.
	rec = cfgReq(t, cfgAPI(&fakeConfig{applyErr: err}, fakeBusy{}), "PUT", "/api/profiles/default/config", `{"cpu":3}`)
	d = decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)
	if rec.Code != 409 || len(d) != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// Wrapped busy errors map the same way.
	rec = cfgReq(t, cfgAPI(&fakeConfig{applyErr: fmt.Errorf("wrap: %w", err)}, fakeBusy{}), "PUT", "/api/profiles/default/config", `{"cpu":3}`)
	if rec.Code != 409 {
		t.Fatalf("wrapped: %d", rec.Code)
	}
}

func TestConfigPutNotFoundAndShutdown(t *testing.T) {
	rec := cfgReq(t, cfgAPI(&fakeConfig{applyErr: config.ErrProfileNotFound}, fakeBusy{}), "PUT", "/api/profiles/ghost/config", `{"cpu":3}`)
	if rec.Code != 404 || errCode(t, rec) != "profile_not_found" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = cfgReq(t, cfgAPI(&fakeConfig{applyErr: fmt.Errorf("x: %w", config.ErrInvalidProfileName)}, fakeBusy{}), "PUT", "/api/profiles/..x/config", `{"cpu":3}`)
	if rec.Code != 404 {
		t.Fatalf("invalid: %d", rec.Code)
	}
	rec = cfgReq(t, cfgAPI(&fakeConfig{applyErr: operations.ErrClosed}, fakeBusy{}), "PUT", "/api/profiles/default/config", `{"cpu":3}`)
	if rec.Code != 503 || errCode(t, rec) != "shutting_down" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestConfigPutUnexpectedErrorsAre500WithoutText(t *testing.T) {
	errs := []error{
		config.ErrAliasedValue, config.ErrMultiDocument, config.ErrUnwritableTarget, config.ErrConflict, config.ErrNoGate,
		&config.ReadError{Err: errors.New("/home/user/x")},
		&config.ParseError{Line: 1, Message: "/home/user/x"},
		&config.TypeError{Problems: []string{"/home/user/x"}},
		&config.WriteError{Err: errors.New("/home/user/x")},
		errors.New("/home/user/x surprise"),
	}
	for _, e := range errs {
		f := &fakeConfig{applyErr: e}
		rec := cfgReq(t, cfgAPI(f, fakeBusy{}), "PUT", "/api/profiles/default/config", `{"cpu":3}`)
		if rec.Code != 500 || errCode(t, rec) != "internal_error" {
			t.Fatalf("%v: %d %s", e, rec.Code, rec.Body)
		}
		if b := rec.Body.String(); strings.Contains(b, "/home") || strings.Contains(strings.ToLower(b), "anchor") || strings.Contains(b, "surprise") {
			t.Fatalf("%v leaked: %s", e, b)
		}
	}
}

func TestConfigPutReadAfterApplyFailureIs500(t *testing.T) {
	f := &fakeConfig{readErr: config.ErrProfileNotFound}
	rec := cfgReq(t, cfgAPI(f, fakeBusy{}), "PUT", "/api/profiles/default/config", `{"cpu":3}`)
	// The file vanished between write and re-read: still a mapped answer, never a panic.
	if rec.Code != 404 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	f = &fakeConfig{readErr: &config.ParseError{Message: "x"}}
	if rec := cfgReq(t, cfgAPI(f, fakeBusy{}), "PUT", "/api/profiles/default/config", `{"cpu":3}`); rec.Code != 500 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestConfigMethodsAndRouteShape(t *testing.T) {
	h := cfgAPI(&fakeConfig{profile: &config.Profile{}}, fakeBusy{})
	for _, m := range []string{"POST", "PATCH", "DELETE"} {
		if rec := cfgReq(t, h, m, "/api/profiles/default/config", `{}`); rec.Code != 405 {
			t.Fatalf("%s: %d", m, rec.Code)
		}
	}
}

// ---- end to end against the real Config Service ----

// mutexGate is a Gate that refuses instead of waiting, like the manager.
type mutexGate struct{ mu sync.Mutex }

func (g *mutexGate) TryExclusive(_ string, fn func() error) (bool, error) {
	if !g.mu.TryLock() {
		return false, nil
	}
	defer g.mu.Unlock()
	return true, fn()
}

type busyGate struct{}

func (busyGate) TryExclusive(string, func() error) (bool, error) { return false, nil }

func realConfig(t *testing.T, content string, gate config.Gate, vm bool) (http.Handler, string) {
	t.Helper()
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "default"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(base, "default", "colima.yaml")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, err := config.New(base, config.WithGate(gate),
		config.WithLimits(config.Limits{MaxCPU: 16, MaxMemoryGiB: 64, MaxDiskGiB: 1000}),
		config.WithVMRunning(func(string) (bool, error) { return vm, nil }))
	if err != nil {
		t.Fatal(err)
	}
	return cfgAPI(svc, fakeBusy{}), file
}

func TestConfigRealServiceRoundTrip(t *testing.T) {
	h, file := realConfig(t, "cpu: 2\nmemory: 4\nruntime: docker\n", &mutexGate{}, true)
	rec := cfgReq(t, h, "GET", "/api/profiles/default/config", "")
	if got := strings.TrimSpace(rec.Body.String()); got != `{"name":"default","cpu":2,"memoryGiB":4,"diskGiB":100}` {
		t.Fatalf("get: %s", got)
	}
	rec = cfgReq(t, h, "PUT", "/api/profiles/default/config", `{"memoryGiB":2.5,"diskGiB":80}`)
	want := `{"config":{"name":"default","cpu":2,"memoryGiB":2.5,"diskGiB":80},"restartRequired":["memoryGiB","diskGiB"]}`
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	b, _ := os.ReadFile(file)
	if !strings.Contains(string(b), "runtime: docker") || !strings.Contains(string(b), "memory: 2.5") {
		t.Fatalf("file: %q", b)
	}
}

func TestConfigRealServiceInvalidChangesNothing(t *testing.T) {
	h, file := realConfig(t, "cpu: 2\ndisk: 60\n", &mutexGate{}, true)
	before, _ := os.ReadFile(file)
	rec := cfgReq(t, h, "PUT", "/api/profiles/default/config", `{"cpu":999,"memoryGiB":0.5,"diskGiB":10}`)
	if rec.Code != 422 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	fields := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any)
	for _, k := range []string{"cpu", "memoryGiB", "diskGiB"} {
		if _, ok := fields[k].(string); !ok {
			t.Fatalf("missing %s in %v", k, fields)
		}
	}
	// Hostile numbers too.
	for _, body := range []string{`{"cpu":0}`, `{"cpu":-1}`, `{"memoryGiB":-4}`, `{"diskGiB":0}`, `{"memoryGiB":1e300}`} {
		if rec := cfgReq(t, h, "PUT", "/api/profiles/default/config", body); rec.Code != 422 {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(before, after) {
		t.Fatalf("file changed: %q", after)
	}
}

func TestConfigRealServiceBusyWritesNothing(t *testing.T) {
	h, file := realConfig(t, "cpu: 2\n", busyGate{}, true)
	rec := cfgReq(t, h, "PUT", "/api/profiles/default/config", `{"cpu":4}`)
	if rec.Code != 409 || errCode(t, rec) != "profile_busy" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if b, _ := os.ReadFile(file); string(b) != "cpu: 2\n" {
		t.Fatalf("file: %q", b)
	}
	// Reads still work while busy.
	if rec := cfgReq(t, h, "GET", "/api/profiles/default/config", ""); rec.Code != 200 {
		t.Fatalf("get while busy: %d", rec.Code)
	}
}

func TestConfigRealServiceAliasedValueIs500NoWrite(t *testing.T) {
	content := "cpu: &n 2\ndisk: *n\n"
	h, file := realConfig(t, content, &mutexGate{}, true)
	rec := cfgReq(t, h, "PUT", "/api/profiles/default/config", `{"cpu":4}`)
	if rec.Code != 500 || errCode(t, rec) != "internal_error" || strings.Contains(strings.ToLower(rec.Body.String()), "anchor") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if b, _ := os.ReadFile(file); string(b) != content {
		t.Fatalf("file: %q", b)
	}
}

func TestConfigRealServiceCorruptFileIs500Opaque(t *testing.T) {
	h, _ := realConfig(t, "cpu: [unclosed\n", &mutexGate{}, true)
	for _, rec := range []*httptest.ResponseRecorder{
		cfgReq(t, h, "GET", "/api/profiles/default/config", ""),
		cfgReq(t, h, "PUT", "/api/profiles/default/config", `{"cpu":4}`),
	} {
		if rec.Code != 500 || strings.Contains(rec.Body.String(), "unclosed") || strings.Contains(rec.Body.String(), "line") {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
}

func TestConfigRealServiceTraversalNames(t *testing.T) {
	h, _ := realConfig(t, "cpu: 2\n", &mutexGate{}, true)
	for _, n := range []string{"..", "%2e%2e", "a%2Fb", "_templates", ".hidden", "ghost"} {
		for _, m := range []string{"GET", "PUT"} {
			body := ""
			if m == "PUT" {
				body = `{"cpu":3}`
			}
			if rec := cfgReq(t, h, m, "/api/profiles/"+n+"/config", body); rec.Code != 404 {
				t.Fatalf("%s %s: %d %s", m, n, rec.Code, rec.Body)
			}
		}
	}
}

func TestConfigRealServiceConcurrentPuts(t *testing.T) {
	h, file := realConfig(t, "cpu: 1\nmemory: 4\nruntime: docker\n", &mutexGate{}, true)
	const n = 12
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := cfgReq(t, h, "PUT", "/api/profiles/default/config", fmt.Sprintf(`{"cpu":%d}`, i+2))
			codes[i] = rec.Code
		}()
	}
	wg.Wait()
	ok := 0
	for i, c := range codes {
		switch c {
		case 200:
			ok++
		case 409:
		default:
			t.Fatalf("put %d: %d", i, c)
		}
	}
	if ok == 0 {
		t.Fatal("no PUT won")
	}
	b, _ := os.ReadFile(file)
	p, err := config.Parse(b)
	if err != nil || p.CPU == nil || *p.CPU < 2 || *p.CPU > n+1 || !strings.Contains(string(b), "runtime: docker") {
		t.Fatalf("file after race: %q %v", b, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(file))
	if len(entries) != 1 {
		t.Fatalf("stray files: %v", entries)
	}
}
