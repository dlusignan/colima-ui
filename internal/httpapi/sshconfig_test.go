package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type fakeSSHSource struct {
	mu    sync.Mutex
	names []string
	doc   SSHConfigDoc
	err   error
}

func (f *fakeSSHSource) SSHConfig(_ context.Context, name string) (SSHConfigDoc, error) {
	f.mu.Lock()
	f.names = append(f.names, name)
	f.mu.Unlock()
	return f.doc, f.err
}

func sshServer(g ProfileGate, s SSHConfigSource) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithSSHConfig(g, s)), nil)
}

const sshURL = "/api/profiles/synthetic-a/ssh-config"

func TestSSHConfigShape(t *testing.T) {
	src := &fakeSSHSource{doc: SSHConfigDoc{
		Raw:     "Host h\n  Port 1\n",
		Entries: []SSHConfigEntry{{"Host", "h"}, {"Port", "1"}},
	}}
	rec := call(t, sshServer(&fakeGate{status: "Running"}, src), "GET", sshURL, "")
	want := `{"raw":"Host h\n  Port 1\n","entries":[{"key":"Host","value":"h"},{"key":"Port","value":"1"}]}` + "\n"
	if rec.Code != 200 || rec.Body.String() != want {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("response must not be cached")
	}
}

func TestSSHConfigNeverNullEntries(t *testing.T) {
	rec := call(t, sshServer(&fakeGate{status: "running"}, &fakeSSHSource{}), "GET", sshURL, "")
	var got map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || string(got["entries"]) != "[]" {
		t.Fatalf("%v %s", err, rec.Body.String())
	}
}

func TestSSHConfigUnknownAndHostileProfileNeverReachColima(t *testing.T) {
	src := &fakeSSHSource{}
	h := sshServer(&fakeGate{err: ErrProfileNotFound}, src)
	wantErr(t, call(t, h, "GET", sshURL, ""), 404, "profile_not_found")
	h = sshServer(&fakeGate{err: ErrInvalidProfileName}, src)
	wantErr(t, call(t, h, "GET", "/api/profiles/-p/ssh-config", ""), 404, "profile_not_found")
	if len(src.names) != 0 {
		t.Fatalf("source called: %v", src.names)
	}
}

func TestSSHConfigStoppedProfileRefusedIs409(t *testing.T) {
	src := &fakeSSHSource{err: ErrSSHConfigRefused}
	w := wantErr(t, call(t, sshServer(&fakeGate{status: "Stopped"}, src), "GET", sshURL, ""), 409, "profile_not_running")
	if w.Error.Details["status"] != "stopped" {
		t.Errorf("details %v", w.Error.Details)
	}
}

func TestSSHConfigStoppedProfileThatAnswersIs200(t *testing.T) {
	src := &fakeSSHSource{doc: SSHConfigDoc{Raw: "Host h\n", Entries: []SSHConfigEntry{{"Host", "h"}}}}
	if rec := call(t, sshServer(&fakeGate{status: "stopped"}, src), "GET", sshURL, ""); rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestSSHConfigFailuresAreOpaque(t *testing.T) {
	for _, e := range []error{ErrSSHConfigRefused, ErrSSHConfigUnparseable, errors.New("/home/user/secret boom")} {
		rec := call(t, sshServer(&fakeGate{status: "running"}, &fakeSSHSource{err: e}), "GET", sshURL, "")
		wantErr(t, rec, 502, "colima_failed")
		if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "/home") {
			t.Errorf("leak: %s", rec.Body.String())
		}
	}
	wantErr(t, call(t, sshServer(&fakeGate{err: errors.New("x")}, &fakeSSHSource{}), "GET", sshURL, ""), 502, "colima_failed")
}

func TestSSHConfigNeedsTokenAndGetOnly(t *testing.T) {
	h := sshServer(&fakeGate{status: "running"}, &fakeSSHSource{})
	if rec := call(t, h, "POST", sshURL, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST %d", rec.Code)
	}
}
