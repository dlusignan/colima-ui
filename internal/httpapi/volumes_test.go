package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

func volumesServer(g ProfileGate, p dockerc.Provider) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithVolumes(g, p)), nil)
}

const volumesURL = "/api/profiles/synthetic-a/volumes"

// noLeadingSlash fails on any string value in the decoded JSON that starts
// with "/", which is how a host path would show up.
func noLeadingSlash(t *testing.T, raw []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(x any) {
		switch x := x.(type) {
		case string:
			if strings.HasPrefix(x, "/") {
				t.Errorf("string value %q starts with /", x)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
}

func TestVolumesShapeSortAndNulls(t *testing.T) {
	created := time.Date(2026, 4, 11, 18, 2, 55, 0, time.UTC)
	size, zero, refs := int64(73400320), int64(0), 1
	none := 0
	fc := &fakeDockerClient{volumes: []dockerc.Volume{
		{Name: "zeta", Driver: "local", Created: created, SizeBytes: &zero, Containers: &none},
		{Name: "pgdata", Driver: "local", Created: created, SizeBytes: &size, Containers: &refs},
		{Name: "alpha", Driver: "nfs"},
	}}
	rec := call(t, volumesServer(&fakeGate{status: "Running"}, &fakeProvider{client: fc}), "GET", volumesURL, "")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Volumes []volumeJSON `json:"volumes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := body.Volumes
	if len(got) != 3 || got[0].Name != "alpha" || got[1].Name != "pgdata" || got[2].Name != "zeta" {
		t.Fatalf("order: %+v", got)
	}
	if got[1].SizeBytes == nil || *got[1].SizeBytes != 73400320 || *got[1].Containers != 1 ||
		got[1].CreatedAt != "2026-04-11T18:02:55Z" || got[1].Driver != "local" {
		t.Fatalf("pgdata: %+v", got[1])
	}
	if got[2].SizeBytes == nil || *got[2].SizeBytes != 0 || got[2].Containers == nil || *got[2].Containers != 0 {
		t.Fatalf("zero must stay zero: %+v", got[2])
	}
	if !strings.Contains(rec.Body.String(), `{"name":"alpha","driver":"nfs","createdAt":"","sizeBytes":null,"containers":null}`) {
		t.Fatalf("unknown must be null: %s", rec.Body.String())
	}
	noLeadingSlash(t, rec.Body.Bytes())
}

func TestVolumesEmptyAndNonDocker(t *testing.T) {
	rec := call(t, volumesServer(&fakeGate{status: "running"}, &fakeProvider{client: &fakeDockerClient{}}), "GET", volumesURL, "")
	if rec.Code != 200 || rec.Body.String() != "{\"volumes\":[]}\n" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	rec = call(t, volumesServer(&fakeGate{status: "running"}, &fakeProvider{err: dockerc.ErrRuntimeNotDocker}), "GET", volumesURL, "")
	if rec.Code != 200 || rec.Body.String() != "{\"volumes\":[]}\n" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestVolumesStoppedGate(t *testing.T) {
	p := &fakeProvider{err: errors.New("must not be called")}
	rec := call(t, volumesServer(&fakeGate{status: "stopped"}, p), "GET", volumesURL, "")
	w := wantErr(t, rec, 409, "invalid_state")
	if w.Error.Details["status"] != "stopped" {
		t.Fatalf("details %v", w.Error.Details)
	}
}

func TestVolumesProviderErrors(t *testing.T) {
	cases := []struct {
		name   string
		p      dockerc.Provider
		status int
		code   string
	}{
		{"vm stopped", &fakeProvider{err: dockerc.ErrVMNotRunning}, 409, "invalid_state"},
		{"docker unavailable", &fakeProvider{err: dockerc.ErrDockerUnavailable}, 502, "docker_unavailable"},
		{"list unavailable", &fakeProvider{client: &fakeDockerClient{volErr: dockerc.ErrDockerUnavailable}}, 502, "docker_unavailable"},
		{"other list error", &fakeProvider{client: &fakeDockerClient{volErr: errors.New("/home/user/x")}}, 500, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, volumesServer(&fakeGate{status: "running"}, tc.p), "GET", volumesURL, "")
			wantErr(t, rec, tc.status, tc.code)
			if strings.Contains(rec.Body.String(), "/home/user") {
				t.Fatalf("cause leaked: %s", rec.Body.String())
			}
		})
	}
}

func TestVolumesGateErrors(t *testing.T) {
	p := &fakeProvider{err: errors.New("must not be called")}
	wantErr(t, call(t, volumesServer(&fakeGate{err: ErrProfileNotFound}, p), "GET", "/api/profiles/nope/volumes", ""), 404, "profile_not_found")
	wantErr(t, call(t, volumesServer(&fakeGate{err: ErrInvalidProfileName}, p), "GET", "/api/profiles/bad/volumes", ""), 404, "profile_not_found")
	wantErr(t, call(t, volumesServer(&fakeGate{err: errors.New("boom")}, p), "GET", "/api/profiles/default/volumes", ""), 502, "colima_failed")
}

func TestVolumesNeedsToken(t *testing.T) {
	g := &fakeGate{status: "running"}
	req := httptest.NewRequest("GET", volumesURL, nil)
	req.Host = goodHost
	rec := httptest.NewRecorder()
	volumesServer(g, &fakeProvider{client: &fakeDockerClient{}}).ServeHTTP(rec, req)
	wantErr(t, rec, 401, "unauthorized")
	if len(g.names) != 0 {
		t.Fatal("gate reached without token")
	}
}
