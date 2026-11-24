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

func imagesServer(g ProfileGate, p dockerc.Provider) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithImages(g, p, fakeBusy{}, nil)), nil)
}

const imagesURL = "/api/profiles/synthetic-a/images"

func TestImagesShapeSortAndCounts(t *testing.T) {
	created := time.Date(2026, 5, 2, 9, 41, 7, 0, time.UTC)
	fc := &fakeDockerClient{images: []dockerc.Image{
		{ID: "ccc", Tags: nil, SizeBytes: 5, Created: created, Containers: 1},
		{ID: "bbb", Tags: []string{"redis:7", "redis:latest"}, SizeBytes: 10, Created: created, Containers: 0},
		{ID: "aaa", Tags: []string{"nginx:1.27"}, SizeBytes: 192000000, Created: created, Containers: 2},
		{ID: "abc", Tags: []string{}, SizeBytes: 1, Created: created},
	}}
	g := &fakeGate{status: "Running"}
	rec := call(t, imagesServer(g, &fakeProvider{client: fc}), "GET", imagesURL, "")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Images []imageJSON `json:"images"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := body.Images
	order := []string{"aaa", "bbb", "abc", "ccc"}
	if len(got) != 4 {
		t.Fatalf("len %d", len(got))
	}
	for i, id := range order {
		if got[i].ID != id {
			t.Fatalf("order %d: got %s want %s (%+v)", i, got[i].ID, id, got)
		}
	}
	if got[0].Containers != 2 || got[0].SizeBytes != 192000000 || got[0].CreatedAt != "2026-05-02T09:41:07Z" {
		t.Fatalf("nginx: %+v", got[0])
	}
	if got[1].Containers != 0 || len(got[1].Tags) != 2 {
		t.Fatalf("shared tags: %+v", got[1])
	}
	if got[3].Containers != 1 {
		t.Fatalf("stopped-container count: %+v", got[3])
	}
	// Untagged images serialize as [], never null.
	if !strings.Contains(rec.Body.String(), `"id":"abc","tags":[]`) || !strings.Contains(rec.Body.String(), `"id":"ccc","tags":[]`) {
		t.Fatalf("untagged must be []: %s", rec.Body.String())
	}
	if len(g.names) != 1 || g.names[0] != "synthetic-a" {
		t.Fatalf("gate names %v", g.names)
	}
}

func TestImagesEmptyAndNonDocker(t *testing.T) {
	rec := call(t, imagesServer(&fakeGate{status: "running"}, &fakeProvider{client: &fakeDockerClient{}}), "GET", imagesURL, "")
	if rec.Code != 200 || rec.Body.String() != "{\"images\":[]}\n" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	rec = call(t, imagesServer(&fakeGate{status: "running"}, &fakeProvider{err: dockerc.ErrRuntimeNotDocker}), "GET", imagesURL, "")
	if rec.Code != 200 || rec.Body.String() != "{\"images\":[]}\n" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestImagesStoppedGate(t *testing.T) {
	p := &fakeProvider{err: errors.New("must not be called")}
	rec := call(t, imagesServer(&fakeGate{status: "stopped"}, p), "GET", imagesURL, "")
	w := wantErr(t, rec, 409, "invalid_state")
	if w.Error.Details["status"] != "stopped" {
		t.Fatalf("details %v", w.Error.Details)
	}
}

func TestImagesProviderErrors(t *testing.T) {
	cases := []struct {
		name   string
		p      dockerc.Provider
		status int
		code   string
	}{
		{"vm stopped", &fakeProvider{err: dockerc.ErrVMNotRunning}, 409, "invalid_state"},
		{"docker unavailable", &fakeProvider{err: dockerc.ErrDockerUnavailable}, 502, "docker_unavailable"},
		{"list unavailable", &fakeProvider{client: &fakeDockerClient{imageErr: dockerc.ErrDockerUnavailable}}, 502, "docker_unavailable"},
		{"other provider error", &fakeProvider{err: errors.New("/home/user/secret path")}, 500, "internal_error"},
		{"other list error", &fakeProvider{client: &fakeDockerClient{imageErr: errors.New("/home/user/x")}}, 500, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, imagesServer(&fakeGate{status: "running"}, tc.p), "GET", imagesURL, "")
			wantErr(t, rec, tc.status, tc.code)
			if got := rec.Body.String(); strings.Contains(got, "/home/user") || strings.Contains(got, "secret") {
				t.Fatalf("cause leaked: %s", got)
			}
		})
	}
}

func TestImagesGateErrors(t *testing.T) {
	p := &fakeProvider{err: errors.New("must not be called")}
	wantErr(t, call(t, imagesServer(&fakeGate{err: ErrProfileNotFound}, p), "GET", "/api/profiles/nope/images", ""), 404, "profile_not_found")
	wantErr(t, call(t, imagesServer(&fakeGate{err: ErrInvalidProfileName}, p), "GET", "/api/profiles/bad/images", ""), 404, "profile_not_found")
	wantErr(t, call(t, imagesServer(&fakeGate{err: errors.New("boom")}, p), "GET", "/api/profiles/default/images", ""), 502, "colima_failed")
}

func TestImagesNeedsToken(t *testing.T) {
	g := &fakeGate{status: "running"}
	req := httptest.NewRequest("GET", imagesURL, nil)
	req.Host = goodHost
	rec := httptest.NewRecorder()
	imagesServer(g, &fakeProvider{client: &fakeDockerClient{}}).ServeHTTP(rec, req)
	wantErr(t, rec, 401, "unauthorized")
	if len(g.names) != 0 {
		t.Fatal("gate reached without token")
	}
}
