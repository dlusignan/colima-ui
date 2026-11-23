package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/store"
)

type fakeActions struct {
	entries []store.ActionEntry
	err     error
	calls   int
	profile string
	limit   int
}

func (f *fakeActions) RecentActions(_ context.Context, profile string, limit int) ([]store.ActionEntry, error) {
	f.calls++
	f.profile, f.limit = profile, limit
	return f.entries, f.err
}

func actionsServer(r ActionReader, g ProfileGate) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithActions(r, g)), nil)
}

func TestActionsNeedToken(t *testing.T) {
	f, g := &fakeActions{}, &fakeGate{status: "running"}
	req := httptest.NewRequest("GET", "/api/profiles/default/actions", nil)
	req.Host = goodHost
	rec := httptest.NewRecorder()
	actionsServer(f, g).ServeHTTP(rec, req)
	wantErr(t, rec, 401, "unauthorized")
	if f.calls != 0 || len(g.names) != 0 {
		t.Fatal("handler ran without token")
	}
}

func TestActionsEmpty(t *testing.T) {
	f := &fakeActions{entries: []store.ActionEntry{}}
	rec := call(t, actionsServer(f, &fakeGate{status: "running"}), "GET", "/api/profiles/default/actions", "")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"entries":[]}` {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	if f.limit != 20 || f.profile != "default" {
		t.Fatalf("limit %d profile %q", f.limit, f.profile)
	}
	f.entries = nil
	rec = call(t, actionsServer(f, &fakeGate{}), "GET", "/api/profiles/default/actions", "")
	var got struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Entries == nil {
		t.Fatalf("nil entries must encode as []: %q", rec.Body.String())
	}
}

func TestActionsFullLog(t *testing.T) {
	detail := "colima exited with status 1"
	f := &fakeActions{entries: []store.ActionEntry{
		{Profile: "default", StartedAt: time.Date(2026, 11, 23, 1, 10, 0, 0, time.FixedZone("x", 3600)), Action: "start", Outcome: "failed", Detail: &detail},
		{Profile: "default", StartedAt: time.Date(2026, 11, 22, 22, 3, 11, 0, time.UTC), Action: "config_change", Outcome: "success"},
	}}
	rec := call(t, actionsServer(f, &fakeGate{}), "GET", "/api/profiles/default/actions?limit=2", "")
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	want := `{"entries":[{"startedAt":"2026-11-23T00:10:00Z","action":"start","outcome":"failed","detail":"colima exited with status 1"},` +
		`{"startedAt":"2026-11-22T22:03:11Z","action":"config_change","outcome":"success","detail":null}]}`
	var a, b any
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(want), &b)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("got %s\nwant %s", ja, jb)
	}
	if f.limit != 2 {
		t.Fatalf("limit %d", f.limit)
	}
}

func TestActionsLimit(t *testing.T) {
	for q, want := range map[string]int{"": 20, "?limit=1": 1, "?limit=100": 100, "?limit=101": 100, "?limit=100000": 100} {
		f := &fakeActions{}
		rec := call(t, actionsServer(f, &fakeGate{}), "GET", "/api/profiles/default/actions"+q, "")
		if rec.Code != 200 || f.limit != want {
			t.Fatalf("%q: status %d limit %d, want %d", q, rec.Code, f.limit, want)
		}
	}
	for _, q := range []string{"?limit=", "?limit=0", "?limit=-3", "?limit=abc", "?limit=1.5", "?limit=99999999999999999999"} {
		f := &fakeActions{}
		rec := call(t, actionsServer(f, &fakeGate{}), "GET", "/api/profiles/default/actions"+q, "")
		w := wantErr(t, rec, 400, "invalid_range")
		if w.Error.Details["parameter"] != "limit" || f.calls != 0 {
			t.Fatalf("%q: details %v calls %d", q, w.Error.Details, f.calls)
		}
	}
}

func TestActionsGate(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{ErrProfileNotFound, 404, "profile_not_found"},
		{ErrInvalidProfileName, 404, "profile_not_found"},
		{errors.New("boom"), 502, "colima_failed"},
	}
	for _, c := range cases {
		f := &fakeActions{}
		// A bad limit must not mask the 404.
		rec := call(t, actionsServer(f, &fakeGate{err: c.err}), "GET", "/api/profiles/nope/actions?limit=0", "")
		wantErr(t, rec, c.status, c.code)
		if f.calls != 0 {
			t.Fatal("store reached despite gate failure")
		}
	}
}

func TestActionsStoreErrors(t *testing.T) {
	rec := call(t, actionsServer(&fakeActions{err: store.ErrClosed}, &fakeGate{}), "GET", "/api/profiles/default/actions", "")
	wantErr(t, rec, 503, "shutting_down")

	rec = call(t, actionsServer(&fakeActions{err: errors.New("secret /home/user/db")}, &fakeGate{}), "GET", "/api/profiles/default/actions", "")
	wantErr(t, rec, 500, "internal_error")
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("error text leaked: %s", rec.Body.String())
	}
}
