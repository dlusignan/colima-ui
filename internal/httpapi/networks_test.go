package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

func networksServer(g ProfileGate, p dockerc.Provider) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithNetworks(g, p)), nil)
}

const networksURL = "/api/profiles/synthetic-a/networks"

func TestNetworksShapeAndSort(t *testing.T) {
	fc := &fakeDockerClient{networks: []dockerc.Network{
		{ID: "bbb", Name: "host", Driver: "host", Scope: "local"},
		{ID: "ccc", Name: "bridge", Driver: "bridge", Scope: "local", Containers: 3},
		{ID: "aaa", Name: "bridge", Driver: "bridge", Scope: "local", Internal: true},
		{ID: "ddd", Name: "app", Driver: "overlay", Scope: "swarm"},
	}}
	rec := call(t, networksServer(&fakeGate{status: "Running"}, &fakeProvider{client: fc}), "GET", networksURL, "")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Networks []networkJSON `json:"networks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := body.Networks
	order := []string{"ddd", "aaa", "ccc", "bbb"}
	if len(got) != 4 {
		t.Fatalf("len %d", len(got))
	}
	for i, id := range order {
		if got[i].ID != id {
			t.Fatalf("order %d: got %s want %s", i, got[i].ID, id)
		}
	}
	if got[0].Scope != "swarm" || !got[1].Internal || got[2].Containers != 3 || got[2].Driver != "bridge" {
		t.Fatalf("fields: %+v", got)
	}
	noLeadingSlash(t, rec.Body.Bytes())
}

func TestNetworksEmptyAndNonDocker(t *testing.T) {
	rec := call(t, networksServer(&fakeGate{status: "running"}, &fakeProvider{client: &fakeDockerClient{}}), "GET", networksURL, "")
	if rec.Code != 200 || rec.Body.String() != "{\"networks\":[]}\n" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	rec = call(t, networksServer(&fakeGate{status: "running"}, &fakeProvider{err: dockerc.ErrRuntimeNotDocker}), "GET", networksURL, "")
	if rec.Code != 200 || rec.Body.String() != "{\"networks\":[]}\n" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestNetworksStoppedGate(t *testing.T) {
	p := &fakeProvider{err: errors.New("must not be called")}
	rec := call(t, networksServer(&fakeGate{status: "stopped"}, p), "GET", networksURL, "")
	w := wantErr(t, rec, 409, "invalid_state")
	if w.Error.Details["status"] != "stopped" {
		t.Fatalf("details %v", w.Error.Details)
	}
}

func TestNetworksProviderErrors(t *testing.T) {
	cases := []struct {
		name   string
		p      dockerc.Provider
		status int
		code   string
	}{
		{"vm stopped", &fakeProvider{err: dockerc.ErrVMNotRunning}, 409, "invalid_state"},
		{"docker unavailable", &fakeProvider{err: dockerc.ErrDockerUnavailable}, 502, "docker_unavailable"},
		{"list unavailable", &fakeProvider{client: &fakeDockerClient{netErr: dockerc.ErrDockerUnavailable}}, 502, "docker_unavailable"},
		{"other list error", &fakeProvider{client: &fakeDockerClient{netErr: errors.New("/home/user/x")}}, 500, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, networksServer(&fakeGate{status: "running"}, tc.p), "GET", networksURL, "")
			wantErr(t, rec, tc.status, tc.code)
			if strings.Contains(rec.Body.String(), "/home/user") {
				t.Fatalf("cause leaked: %s", rec.Body.String())
			}
		})
	}
}

func TestNetworksGateErrors(t *testing.T) {
	p := &fakeProvider{err: errors.New("must not be called")}
	wantErr(t, call(t, networksServer(&fakeGate{err: ErrProfileNotFound}, p), "GET", "/api/profiles/nope/networks", ""), 404, "profile_not_found")
	wantErr(t, call(t, networksServer(&fakeGate{err: ErrInvalidProfileName}, p), "GET", "/api/profiles/bad/networks", ""), 404, "profile_not_found")
	wantErr(t, call(t, networksServer(&fakeGate{err: errors.New("boom")}, p), "GET", "/api/profiles/default/networks", ""), 502, "colima_failed")
}

func TestNetworksNeedsToken(t *testing.T) {
	g := &fakeGate{status: "running"}
	req := httptest.NewRequest("GET", networksURL, nil)
	req.Host = goodHost
	rec := httptest.NewRecorder()
	networksServer(g, &fakeProvider{client: &fakeDockerClient{}}).ServeHTTP(rec, req)
	wantErr(t, rec, 401, "unauthorized")
	if len(g.names) != 0 {
		t.Fatal("gate reached without token")
	}
}
