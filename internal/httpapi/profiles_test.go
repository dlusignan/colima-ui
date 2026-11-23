package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/profiles"
)

type fakeProfiles struct {
	list    []profiles.Profile
	listErr error
	get     func(string) (profiles.Profile, error)
	calls   []string
}

func (f *fakeProfiles) List(context.Context) ([]profiles.Profile, error) {
	f.calls = append(f.calls, "list")
	return f.list, f.listErr
}

func (f *fakeProfiles) Get(_ context.Context, name string) (profiles.Profile, error) {
	f.calls = append(f.calls, "get:"+name)
	return f.get(name)
}

func profilesServer(s ProfilesDeps) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithProfiles(s)), nil)
}

func sampleProfile(name string) profiles.Profile {
	return profiles.Profile{Name: name, Status: "running", Runtime: "docker", CPUs: 2,
		MemoryBytes: 4 << 30, DiskBytes: 60 << 30, ColimaVersion: "0.8.1", Source: "both"}
}

func TestProfileRoutesNeedToken(t *testing.T) {
	f := &fakeProfiles{}
	h := profilesServer(f)
	for _, target := range []string{"/api/profiles", "/api/profiles/default"} {
		req := httptest.NewRequest("GET", target, nil)
		req.Host = goodHost
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		wantErr(t, rec, 401, "unauthorized")
	}
	if len(f.calls) != 0 {
		t.Fatalf("handlers ran without token: %v", f.calls)
	}
}

func TestListProfiles(t *testing.T) {
	id := "j_1"
	p2 := sampleProfile("dev")
	p2.ActiveJobID = &id
	p2.Warnings = []profiles.Warning{{Code: profiles.WarnConfigInvalid, Message: "x"}}
	f := &fakeProfiles{list: []profiles.Profile{sampleProfile("default"), p2}}
	rec := call(t, profilesServer(f), "GET", "/api/profiles", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var body struct {
		Profiles []map[string]any `json:"profiles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Profiles) != 2 {
		t.Fatalf("got %d profiles", len(body.Profiles))
	}
	a, b := body.Profiles[0], body.Profiles[1]
	if a["name"] != "default" || a["memoryBytes"] != float64(4<<30) || a["source"] != "both" {
		t.Fatalf("first profile %v", a)
	}
	if v, ok := a["activeJobId"]; !ok || v != nil {
		t.Fatalf("activeJobId should be present and null: %v", a)
	}
	if _, ok := a["warnings"]; ok {
		t.Fatalf("warnings must be omitted when empty: %v", a)
	}
	if b["activeJobId"] != "j_1" {
		t.Fatalf("activeJobId %v", b["activeJobId"])
	}
	w, _ := b["warnings"].([]any)
	if len(w) != 1 || w[0].(map[string]any)["code"] != "config_invalid" {
		t.Fatalf("warnings %v", b["warnings"])
	}
}

func TestListEmptyIsArray(t *testing.T) {
	for _, l := range [][]profiles.Profile{nil, {}} {
		rec := call(t, profilesServer(&fakeProfiles{list: l}), "GET", "/api/profiles", "")
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"profiles":[]}` {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestListErrors(t *testing.T) {
	f := &fakeProfiles{listErr: &profiles.ColimaError{Err: errors.New("/home/user/secret boom")}}
	rec := call(t, profilesServer(f), "GET", "/api/profiles", "")
	wantErr(t, rec, 502, "colima_failed")
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("leaked detail: %s", rec.Body.String())
	}
	f = &fakeProfiles{listErr: errors.New("other")}
	wantErr(t, call(t, profilesServer(f), "GET", "/api/profiles", ""), 500, "internal_error")
	f = &fakeProfiles{listErr: context.DeadlineExceeded}
	wantErr(t, call(t, profilesServer(f), "GET", "/api/profiles", ""), 500, "internal_error")
}

func TestGetProfile(t *testing.T) {
	f := &fakeProfiles{get: func(n string) (profiles.Profile, error) { return sampleProfile(n), nil }}
	rec := call(t, profilesServer(f), "GET", "/api/profiles/dev-1", "")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["name"] != "dev-1" || m["cpus"] != float64(2) {
		t.Fatalf("body %v", m)
	}
	if _, wrapped := m["profiles"]; wrapped {
		t.Fatal("profile must not be wrapped")
	}
	if v, ok := m["activeJobId"]; !ok || v != nil {
		t.Fatalf("activeJobId %v", m)
	}
}

func TestGetProfileNotFound(t *testing.T) {
	f := &fakeProfiles{get: func(string) (profiles.Profile, error) { return profiles.Profile{}, profiles.ErrNotFound }}
	h := profilesServer(f)
	w := wantErr(t, call(t, h, "GET", "/api/profiles/ghost", ""), 404, "profile_not_found")
	if w.Error.Details["name"] != "ghost" {
		t.Fatalf("details %v", w.Error.Details)
	}
	for _, bad := range []string{"%3Cscript%3E", "a%20b", "..%2Fx", strings.Repeat("a", 65)} {
		w := wantErr(t, call(t, h, "GET", "/api/profiles/"+bad, ""), 404, "profile_not_found")
		if len(w.Error.Details) != 0 {
			t.Fatalf("%q echoed: %v", bad, w.Error.Details)
		}
	}
}

func TestGetProfileColimaFailed(t *testing.T) {
	f := &fakeProfiles{get: func(string) (profiles.Profile, error) {
		return profiles.Profile{}, &profiles.ColimaError{Err: errors.New("boom")}
	}}
	wantErr(t, call(t, profilesServer(f), "GET", "/api/profiles/default", ""), 502, "colima_failed")
	f = &fakeProfiles{get: func(string) (profiles.Profile, error) { return profiles.Profile{}, errors.New("x") }}
	wantErr(t, call(t, profilesServer(f), "GET", "/api/profiles/default", ""), 500, "internal_error")
}

func TestProfileRoutesMethods(t *testing.T) {
	h := profilesServer(&fakeProfiles{})
	rec := call(t, h, "DELETE", "/api/profiles", "")
	wantErr(t, rec, 405, "method_not_allowed")
	if rec.Header().Get("Allow") == "" {
		t.Fatal("no Allow header")
	}
}
