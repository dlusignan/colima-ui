package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/dockerc/dockerctest"
	"github.com/dlusignan/colima-ui/internal/operations"
)

func deleteGuardServer(m JobsDeps, g ProfileGate, p dockerc.Provider) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithJobs(m, g, WithDeleteContents(p))), nil)
}

const guardDeleteBody = `{"confirm":"dev"}`

func TestDeleteRefusedWhenDockerHoldsAnything(t *testing.T) {
	for name, tc := range map[string]struct {
		fake *dockerctest.Fake
		want [3]float64
	}{
		"only a stopped container": {&dockerctest.Fake{ContainerList: []dockerc.Container{{ID: "a", Name: "synthetic-secret", State: "exited"}}}, [3]float64{1, 0, 0}},
		"only an image":            {&dockerctest.Fake{ImageList: []dockerc.Image{{ID: "i"}}}, [3]float64{0, 1, 0}},
		"only a volume":            {&dockerctest.Fake{VolumeList: []dockerc.Volume{{Name: "synthetic-secret"}}}, [3]float64{0, 0, 1}},
		"everything":               {fullFake(), [3]float64{3, 2, 1}},
	} {
		m, g := &fakeMgr{}, &fakeGate{status: "running"}
		rec := call(t, deleteGuardServer(m, g, &dockerctest.Provider{Client: tc.fake}), "POST", deletePath, guardDeleteBody)
		w := wantErr(t, rec, 409, "invalid_state")
		d := w.Error.Details
		if d["reason"] != "docker_not_empty" || d["status"] != "running" ||
			d["containers"] != tc.want[0] || d["images"] != tc.want[1] || d["volumes"] != tc.want[2] {
			t.Fatalf("%s: details %v", name, d)
		}
		if strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("%s: a name leaked", name)
		}
		if len(m.submitReq) != 0 {
			t.Fatalf("%s: submit called", name)
		}
	}
}

func TestDeleteAllowedWhenRunningDockerIsEmpty(t *testing.T) {
	m, g := &fakeMgr{}, &fakeGate{status: "Running"}
	rec := call(t, deleteGuardServer(m, g, &dockerctest.Provider{Client: &dockerctest.Fake{}}), "POST", deletePath, guardDeleteBody)
	if rec.Code != 202 || len(m.submitReq) != 1 || m.submitReq[0].Action != operations.ActionDelete {
		t.Fatalf("%d %s submit %v", rec.Code, rec.Body.String(), m.submitReq)
	}
}

func TestDeleteAllowedWhenRunningRuntimeIsNotDocker(t *testing.T) {
	m, g := &fakeMgr{}, &fakeGate{status: "running"}
	rec := call(t, deleteGuardServer(m, g, &dockerctest.Provider{Err: dockerc.ErrRuntimeNotDocker}), "POST", deletePath, guardDeleteBody)
	if rec.Code != 202 || len(m.submitReq) != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteStoppedCannotBeVerified(t *testing.T) {
	for _, st := range []string{"stopped", "Stopped"} {
		m, g, p := &fakeMgr{}, &fakeGate{status: st}, &dockerctest.Provider{Client: fullFake()}
		w := wantErr(t, call(t, deleteGuardServer(m, g, p), "POST", deletePath, guardDeleteBody), 409, "invalid_state")
		if w.Error.Details["reason"] != "cannot_verify" || w.Error.Details["status"] != "stopped" {
			t.Fatalf("details %v", w.Error.Details)
		}
		if len(m.submitReq) != 0 || len(p.Profiles()) != 0 {
			t.Fatal("submit or docker reached for a stopped profile")
		}
	}
}

func TestDeleteBrokenIsAllowedWithoutLookingAtDocker(t *testing.T) {
	m, g, p := &fakeMgr{}, &fakeGate{status: "broken"}, &dockerctest.Provider{Client: fullFake()}
	if rec := call(t, deleteGuardServer(m, g, p), "POST", deletePath, guardDeleteBody); rec.Code != 202 {
		t.Fatalf("%d", rec.Code)
	}
	if len(p.Profiles()) != 0 {
		t.Fatal("docker read for a broken profile")
	}
}

func TestDeleteRefusedWhenDockerCannotBeRead(t *testing.T) {
	failing := func(method string, err error) *dockerctest.Provider {
		f := fullFake()
		f.FailWith(method, err)
		return &dockerctest.Provider{Client: f}
	}
	for name, p := range map[string]*dockerctest.Provider{
		"socket":   {Err: dockerc.ErrDockerUnavailable},
		"resolver": {Err: errors.New("exec /home/user/colima: boom")},
		"list":     failing("Containers", errors.New("daemon text /var/lib/x")),
		"images":   failing("Images", dockerc.ErrDaemon),
		"volumes":  failing("Volumes", dockerc.ErrDockerUnavailable),
	} {
		m, g := &fakeMgr{}, &fakeGate{status: "running"}
		rec := call(t, deleteGuardServer(m, g, p), "POST", deletePath, guardDeleteBody)
		wantErr(t, rec, 502, "docker_unavailable")
		if len(m.submitReq) != 0 {
			t.Fatalf("%s: submit called", name)
		}
		if strings.Contains(rec.Body.String(), "/home") || strings.Contains(rec.Body.String(), "/var") {
			t.Fatalf("%s: leak %s", name, rec.Body.String())
		}
	}
}

func TestDeleteRunningVMThatStoppedMeanwhileIsRefused(t *testing.T) {
	m, g := &fakeMgr{}, &fakeGate{status: "running"}
	w := wantErr(t, call(t, deleteGuardServer(m, g, &dockerctest.Provider{Err: dockerc.ErrVMNotRunning}), "POST", deletePath, guardDeleteBody), 409, "invalid_state")
	if w.Error.Details["status"] != "stopped" || len(m.submitReq) != 0 {
		t.Fatalf("details %v submit %v", w.Error.Details, m.submitReq)
	}
}

// Busy, the body and the confirmation come before any Docker read.
func TestDeleteChecksComeBeforeTheDockerRead(t *testing.T) {
	p := &dockerctest.Provider{Client: fullFake()}
	m := &fakeMgr{busy: func(string) (string, bool) { return "j_1", true }}
	wantErr(t, call(t, deleteGuardServer(m, &fakeGate{status: "running"}, p), "POST", deletePath, guardDeleteBody), 409, "profile_busy")
	wantErr(t, call(t, deleteGuardServer(&fakeMgr{}, &fakeGate{status: "running"}, p), "POST", deletePath, `{"confirm":"other"}`), 422, "confirmation_required")
	wantErr(t, call(t, deleteGuardServer(&fakeMgr{}, &fakeGate{status: "running"}, p), "POST", deletePath, `{"confirm":"dev","force":true}`), 400, "bad_request")
	wantErr(t, call(t, deleteGuardServer(&fakeMgr{}, &fakeGate{err: ErrProfileNotFound}, p), "POST", deletePath, guardDeleteBody), 404, "profile_not_found")
	wantErr(t, call(t, deleteGuardServer(&fakeMgr{}, &fakeGate{err: ErrInvalidProfileName}, p), "POST", "/api/profiles/..%2Fx/delete", `{"confirm":"..%2Fx"}`), 404, "profile_not_found")
	if len(p.Profiles()) != 0 {
		t.Fatalf("docker reached: %v", p.Profiles())
	}
}

// Starting and unknown states keep the plain refusal, with no Docker read.
func TestDeleteOtherStatesNeverReadDocker(t *testing.T) {
	for _, st := range []string{"starting", "stopping", "unknown", ""} {
		p := &dockerctest.Provider{Client: &dockerctest.Fake{}}
		m := &fakeMgr{}
		wantErr(t, call(t, deleteGuardServer(m, &fakeGate{status: st}, p), "POST", deletePath, guardDeleteBody), 409, "invalid_state")
		if len(p.Profiles()) != 0 || len(m.submitReq) != 0 {
			t.Fatalf("%q: docker or submit reached", st)
		}
	}
}
