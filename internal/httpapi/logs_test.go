package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

func logsServer(g ProfileGate, p dockerc.Provider) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithLogs(g, p)), nil)
}

const (
	logsCID = "aaaaaaaaaaaa"
	logsURL = "/api/profiles/synthetic-a/containers/" + logsCID + "/logs"
)

func logsOK(fc *fakeDockerClient) http.Handler {
	return logsServer(&fakeGate{status: "running"}, &fakeProvider{client: fc})
}

func decodeLogsBody(t *testing.T, rec *httptest.ResponseRecorder) logsJSON {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out logsJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return out
}

func TestLogsShape(t *testing.T) {
	fc := &fakeDockerClient{logs: dockerc.LogResult{Lines: []string{"2026-05-02T09:41:07.000000001Z server started", "2026-05-02T09:41:08.000000001Z ready"}}}
	rec := call(t, logsOK(fc), "GET", logsURL, "")
	out := decodeLogsBody(t, rec)
	if len(out.Lines) != 2 || out.Truncated || out.Lines[1] != "2026-05-02T09:41:08.000000001Z ready" {
		t.Fatalf("%+v", out)
	}
	want := `{"lines":["2026-05-02T09:41:07.000000001Z server started","2026-05-02T09:41:08.000000001Z ready"],"truncated":false}` + "\n"
	if rec.Body.String() != want {
		t.Fatalf("body %q", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	if len(fc.logsArgs) != 1 || fc.logsArgs[0] != (logsCall{logsCID, 200}) {
		t.Fatalf("args %+v", fc.logsArgs)
	}
}

func TestLogsEmptyIsArrayNotNull(t *testing.T) {
	for _, res := range []dockerc.LogResult{{}, {Lines: []string{}}} {
		rec := call(t, logsOK(&fakeDockerClient{logs: res}), "GET", logsURL, "")
		if rec.Body.String() != "{\"lines\":[],\"truncated\":false}\n" {
			t.Fatalf("%q", rec.Body.String())
		}
	}
}

func TestLogsTailParsing(t *testing.T) {
	ok := map[string]int{
		"":                                 200,
		"?tail=1":                          1,
		"?tail=200":                        200,
		"?tail=1000":                       1000,
		"?tail=1001":                       1000,
		"?tail=999999":                     1000,
		"?tail=99999999999999999999999999": 1000,
		"?tail=0005":                       5,
		"?other=1":                         200,
	}
	for q, want := range ok {
		fc := &fakeDockerClient{}
		rec := call(t, logsOK(fc), "GET", logsURL+q, "")
		if rec.Code != 200 || len(fc.logsArgs) != 1 || fc.logsArgs[0].tail != want {
			t.Fatalf("%q: %d %+v", q, rec.Code, fc.logsArgs)
		}
	}
	bad := []string{
		"?tail=0", "?tail=-1", "?tail=abc", "?tail=NaN", "?tail=1.5", "?tail=", "?tail=+5", "?tail=%205",
		"?tail=5%20", "?tail=0x10", "?tail=1e3", "?tail=1&tail=2", "?tail=1&tail=1", "?tail=%zz", "?tail=%E0%A4%A",
		"?tail=000", "?tail=%FF", "?tail=1;tail=2", "?tail=%0a5", "?tail=٣",
	}
	for _, q := range bad {
		fc := &fakeDockerClient{}
		rec := call(t, logsOK(fc), "GET", logsURL+q, "")
		w := wantErr(t, rec, 400, "invalid_range")
		if w.Error.Details["parameter"] != "tail" {
			t.Fatalf("%q: details %v", q, w.Error.Details)
		}
		if len(fc.logsArgs) != 0 {
			t.Fatalf("%q reached docker", q)
		}
		if raw, _ := url.QueryUnescape(strings.TrimPrefix(q, "?tail=")); len(raw) > 2 && strings.Contains(rec.Body.String(), raw) {
			t.Fatalf("%q echoed", q)
		}
	}
}

func TestLogsIDValidationAfterGateNoEcho(t *testing.T) {
	ids := []string{
		"abc", "AAAAAAAAAAAA", "synthetic-web", strings.Repeat("a", 65), "aaaaaaaaaaaz",
		"..", "%2e%2e", "aaaaaaaaaaaa%2fjson", "aaaaaaaaaaaa%3fx", "aaaaaaaaaaaa%0a", "aaaaaaaaaaaa%00",
	}
	for _, id := range ids {
		fc := &fakeDockerClient{}
		p := &fakeProvider{client: fc}
		rec := call(t, logsServer(&fakeGate{status: "running"}, p), "GET", "/api/profiles/synthetic-a/containers/"+id+"/logs", "")
		if rec.Code == 200 {
			t.Fatalf("%q accepted", id)
		}
		if rec.Code == 404 {
			w := wantErr(t, rec, 404, "container_not_found")
			if len(w.Error.Details) != 0 || strings.Contains(rec.Body.String(), "synthetic-web") || strings.Contains(rec.Body.String(), "zzz") {
				t.Fatalf("%q: %s", id, rec.Body.String())
			}
		}
		if len(fc.logsArgs) != 0 {
			t.Fatalf("%q reached docker", id)
		}
	}
	// Valid lengths and the lowercase hex alphabet pass.
	for _, id := range []string{"0123456789ab", strings.Repeat("f", 64)} {
		fc := &fakeDockerClient{}
		rec := call(t, logsOK(fc), "GET", "/api/profiles/synthetic-a/containers/"+id+"/logs", "")
		if rec.Code != 200 || fc.logsArgs[0].id != id {
			t.Fatalf("%q: %d", id, rec.Code)
		}
	}
	// A missing profile wins over a bad id, and a stopped VM too.
	wantErr(t, call(t, logsServer(&fakeGate{err: ErrProfileNotFound}, &fakeProvider{}), "GET", "/api/profiles/nope/containers/zzz/logs", ""), 404, "profile_not_found")
	wantErr(t, call(t, logsServer(&fakeGate{status: "stopped"}, &fakeProvider{}), "GET", "/api/profiles/synthetic-a/containers/zzz/logs", ""), 409, "invalid_state")
	// A bad id wins over a bad tail.
	wantErr(t, call(t, logsOK(&fakeDockerClient{}), "GET", "/api/profiles/synthetic-a/containers/zzz/logs?tail=0", ""), 404, "container_not_found")
}

func TestLogsErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		g      *fakeGate
		p      dockerc.Provider
		status int
		code   string
	}{
		{"unknown container", &fakeGate{status: "running"}, &fakeProvider{client: &fakeDockerClient{logsErr: dockerc.ErrContainerNotFound}}, 404, "container_not_found"},
		{"non docker runtime", &fakeGate{status: "running"}, &fakeProvider{err: dockerc.ErrRuntimeNotDocker}, 404, "container_not_found"},
		{"vm stopped", &fakeGate{status: "running"}, &fakeProvider{err: dockerc.ErrVMNotRunning}, 409, "invalid_state"},
		{"gate says stopped", &fakeGate{status: "Stopped"}, &fakeProvider{}, 409, "invalid_state"},
		{"socket gone", &fakeGate{status: "running"}, &fakeProvider{err: dockerc.ErrDockerUnavailable}, 502, "docker_unavailable"},
		{"logs unavailable error", &fakeGate{status: "running"}, &fakeProvider{client: &fakeDockerClient{logsErr: dockerc.ErrDockerUnavailable}}, 502, "docker_unavailable"},
		{"other error", &fakeGate{status: "running"}, &fakeProvider{client: &fakeDockerClient{logsErr: errors.New("/home/user/SECRET")}}, 500, "internal_error"},
		{"gate failure", &fakeGate{err: errors.New("boom")}, &fakeProvider{}, 502, "colima_failed"},
		{"profile missing", &fakeGate{err: ErrProfileNotFound}, &fakeProvider{}, 404, "profile_not_found"},
		{"invalid profile name", &fakeGate{err: ErrInvalidProfileName}, &fakeProvider{}, 404, "profile_not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, logsServer(tc.g, tc.p), "GET", logsURL, "")
			wantErr(t, rec, tc.status, tc.code)
			if got := rec.Body.String(); strings.Contains(got, "/home/user") || strings.Contains(got, "SECRET") {
				t.Fatalf("cause leaked: %s", got)
			}
		})
	}
}

func TestLogsDriverUnavailableIs409WithReason(t *testing.T) {
	fc := &fakeDockerClient{logsErr: dockerc.ErrLogsUnavailable}
	w := wantErr(t, call(t, logsOK(fc), "GET", logsURL, ""), 409, "invalid_state")
	if w.Error.Details["reason"] != "logs_unavailable" || len(w.Error.Details) != 1 {
		t.Fatalf("details %v", w.Error.Details)
	}
	// Wrapped too.
	fc.logsErr = errors.Join(errors.New("ctx"), dockerc.ErrLogsUnavailable)
	wantErr(t, call(t, logsOK(fc), "GET", logsURL, ""), 409, "invalid_state")
}

func TestLogsTruncatedFlags(t *testing.T) {
	for name, res := range map[string]dockerc.LogResult{
		"older": {Lines: []string{"a"}, Older: true},
		"cut":   {Lines: []string{"a"}, Cut: true},
	} {
		out := decodeLogsBody(t, call(t, logsOK(&fakeDockerClient{logs: res}), "GET", logsURL, ""))
		if !out.Truncated || len(out.Lines) != 1 {
			t.Fatalf("%s: %+v", name, out)
		}
	}
}

func TestLogsBodyCapDropsOldestFirst(t *testing.T) {
	line := strings.Repeat("x", 3990)
	var lines []string
	for i := 0; i < 400; i++ { // about 1.6 MB, over the cap
		lines = append(lines, strings.Repeat(string(rune('a'+i%26)), 10)+line[10:])
	}
	lines = lines[:400]
	rec := call(t, logsOK(&fakeDockerClient{logs: dockerc.LogResult{Lines: lines}}), "GET", logsURL+"?tail=1000", "")
	out := decodeLogsBody(t, rec)
	if !out.Truncated {
		t.Fatal("not truncated")
	}
	if rec.Body.Len() > 512<<10 {
		t.Fatalf("body %d bytes", rec.Body.Len())
	}
	if len(out.Lines) < 100 || len(out.Lines) >= 400 {
		t.Fatalf("kept %d", len(out.Lines))
	}
	if out.Lines[len(out.Lines)-1] != lines[399] {
		t.Fatal("newest line was dropped")
	}
	if out.Lines[0] != lines[400-len(out.Lines)] {
		t.Fatal("kept lines are not the newest contiguous run")
	}
}

func TestLogsBodyCapCountsEscapes(t *testing.T) {
	// Control characters and HTML escape to 6 bytes each, so the cap has to
	// use the encoded size, not the raw size.
	line := strings.Repeat("\x01<", 2000) // 4000 characters, 24000 encoded bytes
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = line
	}
	rec := call(t, logsOK(&fakeDockerClient{logs: dockerc.LogResult{Lines: lines}}), "GET", logsURL, "")
	out := decodeLogsBody(t, rec)
	if rec.Body.Len() > 512<<10 || !out.Truncated || len(out.Lines) > 22 || len(out.Lines) < 20 {
		t.Fatalf("body %d lines %d truncated %v", rec.Body.Len(), len(out.Lines), out.Truncated)
	}
}

func TestLogsClientReturningTooManyLinesIsTrimmed(t *testing.T) {
	fc := &fakeDockerClient{logs: dockerc.LogResult{Lines: []string{"1", "2", "3", "4"}}}
	out := decodeLogsBody(t, call(t, logsOK(fc), "GET", logsURL+"?tail=2", ""))
	if strings.Join(out.Lines, ",") != "3,4" || !out.Truncated {
		t.Fatalf("%+v", out)
	}
}

func TestLogsInvalidUTF8NeverBreaksJSON(t *testing.T) {
	// The client contract is valid UTF-8; if a fake breaks it, encoding/json
	// still emits valid JSON (replacement characters).
	fc := &fakeDockerClient{logs: dockerc.LogResult{Lines: []string{"bad \xff\xfe bytes", "ok   \x00 \x1b[31mred\x1b[0m"}}}
	rec := call(t, logsOK(fc), "GET", logsURL, "")
	out := decodeLogsBody(t, rec)
	for _, l := range out.Lines {
		if !utf8.ValidString(l) {
			t.Fatalf("%q", l)
		}
	}
}

func TestLogsNeedsToken(t *testing.T) {
	g := &fakeGate{status: "running"}
	fc := &fakeDockerClient{}
	req := httptest.NewRequest("GET", logsURL, nil)
	req.Host = goodHost
	rec := httptest.NewRecorder()
	logsServer(g, &fakeProvider{client: fc}).ServeHTTP(rec, req)
	wantErr(t, rec, 401, "unauthorized")
	if len(g.names) != 0 || len(fc.logsArgs) != 0 {
		t.Fatal("reached backend without token")
	}
}

func TestLogsOnlyGET(t *testing.T) {
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		rec := call(t, logsOK(&fakeDockerClient{}), m, logsURL, "")
		if rec.Code == 200 {
			t.Fatalf("%s accepted", m)
		}
	}
}
