package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/operations"
)

const deletePath = "/api/profiles/dev/delete"

func TestDeleteNeedsToken(t *testing.T) {
	m, g := &fakeMgr{}, &fakeGate{status: "stopped"}
	req := httptest.NewRequest("POST", deletePath, strings.NewReader(`{"confirm":"dev"}`))
	req.Host = goodHost
	rec := httptest.NewRecorder()
	jobsServer(m, g).ServeHTTP(rec, req)
	wantErr(t, rec, 401, "unauthorized")
	if len(m.calls) != 0 || len(g.names) != 0 {
		t.Fatalf("handler ran without token: %v %v", m.calls, g.names)
	}
}

func TestDeleteAccepted(t *testing.T) {
	for _, c := range []struct {
		body   string
		status string
		data   bool
	}{
		{`{"confirm":"dev"}`, "stopped", false},
		{`{"confirm":"dev","deleteData":false}`, "stopped", false},
		{`{"confirm":"dev","deleteData":null}`, "stopped", false},
		{`{"confirm":"dev","deleteData":true}`, "stopped", true},
		{`{"deleteData":true,"confirm":"dev"}`, "broken", true},
	} {
		m, g := &fakeMgr{}, &fakeGate{status: c.status}
		rec := call(t, jobsServer(m, g), "POST", deletePath, c.body)
		if rec.Code != 202 || rec.Body.String() != `{"jobId":"j_abc123"}`+"\n" {
			t.Fatalf("%s: %d %s", c.body, rec.Code, rec.Body.String())
		}
		want := operations.Request{Action: operations.ActionDelete, Profile: "dev", DeleteData: c.data}
		if len(m.submitReq) != 1 || !reflect.DeepEqual(m.submitReq[0], want) {
			t.Fatalf("%s: submit %+v", c.body, m.submitReq)
		}
	}
}

func TestDeleteConfirmationRequired(t *testing.T) {
	for _, b := range []string{
		``, `{}`, ` {} `, `{"confirm":null}`, `{"confirm":""}`, `{"confirm":"Dev"}`, `{"confirm":"dev "}`,
		`{"confirm":"other"}`, `{"deleteData":true}`, `{"confirm":"dev/../x"}`,
	} {
		m, g := &fakeMgr{}, &fakeGate{status: "stopped"}
		w := wantErr(t, call(t, jobsServer(m, g), "POST", deletePath, b), 422, "confirmation_required")
		if strings.Contains(w.Error.Message, "dev/") {
			t.Fatalf("%q echoed", b)
		}
		if len(m.submitReq) != 0 {
			t.Fatalf("%q: submit called", b)
		}
	}
}

func TestDeleteBadBodies(t *testing.T) {
	for _, b := range []string{
		`[]`, `x`, `null`, `"dev"`, `{"confirm":1}`, `{"confirm":"dev","deleteData":"yes"}`,
		`{"confirm":"dev","force":true}`, `{"confirm":"dev","data":true}`, `{"Confirm":"dev"}`,
		`{"confirm":"dev","confirm":"dev"}`, `{"confirm":"dev"} {}`, `{"confirm":"dev"}x`,
		`{"confirm":"` + strings.Repeat("a", 2000) + `"}`,
	} {
		m, g := &fakeMgr{}, &fakeGate{status: "stopped"}
		wantErr(t, call(t, jobsServer(m, g), "POST", deletePath, b), 400, "bad_request")
		if len(g.names) != 0 || len(m.calls) != 0 {
			t.Fatalf("body %.20q: gate or manager reached", b)
		}
	}
}

func TestDeleteRunningProfileIsRefused(t *testing.T) {
	m, g := &fakeMgr{}, &fakeGate{status: "Running"}
	w := wantErr(t, call(t, jobsServer(m, g), "POST", deletePath, `{"confirm":"dev","deleteData":true}`), 409, "invalid_state")
	if w.Error.Details["status"] != "running" {
		t.Fatalf("details %v", w.Error.Details)
	}
	if len(m.submitReq) != 0 {
		t.Fatal("submit called for a running profile")
	}
}

func TestDeleteBusyAndUnknown(t *testing.T) {
	m, g := &fakeMgr{busy: func(string) (string, bool) { return "j_1", true }}, &fakeGate{status: "stopped"}
	w := wantErr(t, call(t, jobsServer(m, g), "POST", deletePath, `{"confirm":"dev"}`), 409, "profile_busy")
	if w.Error.Details["jobId"] != "j_1" || len(m.submitReq) != 0 {
		t.Fatalf("details %v submit %v", w.Error.Details, m.submitReq)
	}

	m, g = &fakeMgr{}, &fakeGate{err: ErrProfileNotFound}
	wantErr(t, call(t, jobsServer(m, g), "POST", deletePath, `{"confirm":"dev"}`), 404, "profile_not_found")
	m, g = &fakeMgr{}, &fakeGate{err: ErrInvalidProfileName}
	w = wantErr(t, call(t, jobsServer(m, g), "POST", "/api/profiles/..%2Fx/delete", `{"confirm":"..%2Fx"}`), 404, "profile_not_found")
	if _, ok := w.Error.Details["name"]; ok {
		t.Fatal("hostile name echoed")
	}
	// Busy is a read-only lookup that runs before the gate; nothing is submitted.
	if len(m.submitReq) != 0 {
		t.Fatalf("manager reached: %v", m.calls)
	}
}

func TestDeleteSubmitErrorsAndGateFailure(t *testing.T) {
	m := &fakeMgr{submit: func(operations.Request) (operations.JobSummary, error) {
		return operations.JobSummary{}, &operations.BusyError{Profile: "dev", JobID: "j_9"}
	}}
	wantErr(t, call(t, jobsServer(m, &fakeGate{status: "stopped"}), "POST", deletePath, `{"confirm":"dev"}`), 409, "profile_busy")

	m = &fakeMgr{submit: func(operations.Request) (operations.JobSummary, error) {
		return operations.JobSummary{}, operations.ErrClosed
	}}
	wantErr(t, call(t, jobsServer(m, &fakeGate{status: "stopped"}), "POST", deletePath, `{"confirm":"dev"}`), 503, "shutting_down")

	m = &fakeMgr{}
	wantErr(t, call(t, jobsServer(m, &fakeGate{err: errors.New("exec /usr/local/bin/colima: boom")}), "POST", deletePath, `{"confirm":"dev"}`), 502, "colima_failed")
	if len(m.submitReq) != 0 {
		t.Fatal("submit called after gate failure")
	}
}

// busyGate flips what the manager reports while the status read is running, as a
// start that began during a slow `colima list` would.
func TestDeleteSeesAStartThatBeganDuringTheStatusRead(t *testing.T) {
	var busy bool
	m := &fakeMgr{busy: func(string) (string, bool) { return "j_7", busy }}
	g := &fakeGate{status: "stopped", onCall: func() { busy = true }}
	// Busy is read before the gate, so the flip happens too late for the first
	// check; Submit stays authoritative and answers busy.
	m.submit = func(operations.Request) (operations.JobSummary, error) {
		return operations.JobSummary{}, &operations.BusyError{Profile: "dev", JobID: "j_7"}
	}
	wantErr(t, call(t, jobsServer(m, g), "POST", deletePath, `{"confirm":"dev"}`), 409, "profile_busy")

	// A job already running before the call stops it before the gate is asked.
	busy = true
	g2 := &fakeGate{status: "stopped"}
	m2 := &fakeMgr{busy: func(string) (string, bool) { return "j_8", true }}
	wantErr(t, call(t, jobsServer(m2, g2), "POST", deletePath, `{"confirm":"dev"}`), 409, "profile_busy")
	if len(g2.names) != 0 {
		t.Fatalf("status read before the busy check: %v", g2.names)
	}
	if m2.calls[0] != "busy" {
		t.Fatalf("calls %v", m2.calls)
	}
}

func TestDeleteStatusAllowlist(t *testing.T) {
	for _, st := range []string{"running", "Running", "starting", "stopping", "unknown", "", "weird"} {
		m, g := &fakeMgr{}, &fakeGate{status: st}
		w := wantErr(t, call(t, jobsServer(m, g), "POST", deletePath, `{"confirm":"dev"}`), 409, "invalid_state")
		if w.Error.Details["status"] != strings.ToLower(st) || len(m.submitReq) != 0 {
			t.Fatalf("%q: %v %v", st, w.Error.Details, m.submitReq)
		}
	}
	for _, st := range []string{"stopped", "Stopped", "broken", "Broken"} {
		m, g := &fakeMgr{}, &fakeGate{status: st}
		if rec := call(t, jobsServer(m, g), "POST", deletePath, `{"confirm":"dev"}`); rec.Code != 202 {
			t.Fatalf("%q: %d", st, rec.Code)
		}
	}
}

type deleteStrictGate struct {
	fakeGate
	deleteCalls int
}

func (g *deleteStrictGate) ProfileStatusForDelete(context.Context, string) (string, error) {
	g.deleteCalls++
	return "", ErrProfileNotFound
}

func TestDeleteUsesTheStrictGateWhenThereIsOne(t *testing.T) {
	m, g := &fakeMgr{}, &deleteStrictGate{fakeGate: fakeGate{status: "stopped"}}
	wantErr(t, call(t, jobsServer(m, g), "POST", deletePath, `{"confirm":"dev"}`), 404, "profile_not_found")
	if g.deleteCalls != 1 || len(g.names) != 0 || len(m.submitReq) != 0 {
		t.Fatalf("strict %d plain %v submit %v", g.deleteCalls, g.names, m.submitReq)
	}
}
