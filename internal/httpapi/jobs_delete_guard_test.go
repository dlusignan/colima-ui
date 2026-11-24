package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/colima"
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

// A broken VM is always deletable and its Docker is never read.
func TestDeleteBrokenIsAlwaysAllowedWithoutReadingDocker(t *testing.T) {
	for name, p := range map[string]*dockerctest.Provider{
		"holds things": {Client: fullFake()},
		"unreadable":   {Err: dockerc.ErrVMNotRunning},
	} {
		m, g := &fakeMgr{}, &fakeGate{status: "broken"}
		if rec := call(t, deleteGuardServer(m, g, p), "POST", deletePath, guardDeleteBody); rec.Code != 202 || len(m.submitReq) != 1 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
		if len(p.Profiles()) != 0 {
			t.Fatalf("%s: docker read for a broken profile", name)
		}
	}
	m := &fakeMgr{}
	if rec := call(t, deleteGuardServer(m, &fakeGate{status: "broken"}, nil), "POST", deletePath, guardDeleteBody); rec.Code != 202 {
		t.Fatalf("nil provider: %d", rec.Code)
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

// A VM the resolver no longer finds running is refused as unavailable, never
// reported as stopped.
func TestDeleteRunningVMThatStoppedMeanwhileIsRefused(t *testing.T) {
	m, g := &fakeMgr{}, &fakeGate{status: "running"}
	wantErr(t, call(t, deleteGuardServer(m, g, &dockerctest.Provider{Err: dockerc.ErrVMNotRunning}), "POST", deletePath, guardDeleteBody), 502, "docker_unavailable")
	if len(m.submitReq) != 0 {
		t.Fatal("submit called")
	}
}

// The route asks the manager before the slow reads; no status or Docker read
// happens when a task already runs.
type taskMgr struct {
	fakeMgr
	task string
}

func (m *taskMgr) TaskRunning(string) (string, bool) { return m.task, m.task != "" }

func TestDeleteRefusedEarlyWhileATaskRuns(t *testing.T) {
	m, g, p := &taskMgr{task: "j_pull"}, &fakeGate{status: "running"}, &dockerctest.Provider{Client: &dockerctest.Fake{}}
	w := wantErr(t, call(t, deleteGuardServer(m, g, p), "POST", deletePath, guardDeleteBody), 409, "invalid_state")
	if w.Error.Details["reason"] != "task_running" || w.Error.Details["jobId"] != "j_pull" {
		t.Fatalf("details %v", w.Error.Details)
	}
	if len(g.names) != 0 || len(p.Profiles()) != 0 || len(m.submitReq) != 0 {
		t.Fatal("slow reads or submit happened")
	}
	// No task: the delete goes on.
	m2 := &taskMgr{}
	if rec := call(t, deleteGuardServer(m2, &fakeGate{status: "broken"}, p), "POST", deletePath, guardDeleteBody); rec.Code != 202 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestDeleteRefusedWhileATaskRuns(t *testing.T) {
	m := &fakeMgr{submit: func(operations.Request) (operations.JobSummary, error) {
		return operations.JobSummary{}, &operations.TaskRunningError{Profile: "dev", JobID: "j_pull"}
	}}
	w := wantErr(t, call(t, deleteGuardServer(m, &fakeGate{status: "running"}, &dockerctest.Provider{Client: &dockerctest.Fake{}}), "POST", deletePath, guardDeleteBody), 409, "invalid_state")
	if w.Error.Details["reason"] != "task_running" || w.Error.Details["jobId"] != "j_pull" {
		t.Fatalf("details %v", w.Error.Details)
	}
}

// socketRuntimeProvider is the production resolver over a fake colima that
// reports the given runtimes, with a real Docker socket behind it.
type runtimeColima struct{ list, status string }

func (r runtimeColima) Run(_ context.Context, args []string) (colima.Result, error) {
	if args[0] == "list" {
		return colima.Result{Stdout: []byte(r.list)}, nil
	}
	return colima.Result{Stdout: []byte(r.status)}, nil
}

func runtimeProvider(t *testing.T, listRuntime, statusRuntime string, h http.Handler) dockerc.Provider {
	t.Helper()
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return dockerc.NewProvider(dockerc.NewColimaResolver(runtimeColima{
		list:   `{"name":"dev","status":"Running","runtime":"` + listRuntime + `"}`,
		status: `{"runtime":"` + statusRuntime + `","docker_socket":"unix://` + sock + `"}`,
	}))
}

// dockerWith answers the three list reads with one container, one image and no volume.
var dockerWith = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1.41/containers/json", "/v1.41/images/json":
		w.Write([]byte(`[{"Id":"aaaaaaaaaaaa"}]`))
	case "/v1.41/volumes":
		w.Write([]byte(`{"Volumes":null}`))
	default:
		http.NotFound(w, r)
	}
})

// Only an explicit docker (with or without +k3s) is counted, containerd and
// incus are "not applicable", and everything else fails closed, in the contents
// route and in the delete route.
func TestRuntimeClassificationInTheRoutes(t *testing.T) {
	for _, tc := range []struct {
		name, list, status string
		contents           string // body of a 200, or "" for a 502
		deleteCode         int
	}{
		{"docker", "docker", "docker", `{"applicable":true,"containers":1,"images":1,"volumes":0}`, 409},
		{"docker+k3s", "docker+k3s", "docker+k3s", `{"applicable":true,"containers":1,"images":1,"volumes":0}`, 409},
		{"Docker upper case", "Docker", "DOCKER", `{"applicable":true,"containers":1,"images":1,"volumes":0}`, 409},
		{"containerd", "containerd", "containerd", `{"applicable":false}`, 202},
		{"incus", "incus", "incus", `{"applicable":false}`, 202},
		{"empty", "", "", "", 502},
		{"unknown", "podman", "podman", "", 502},
		{"disagree", "docker", "containerd", "", 502},
		{"list empty only", "", "docker", "", 502},
		{"status empty only", "docker", "", "", 502},
	} {
		p := runtimeProvider(t, tc.list, tc.status, dockerWith)
		rec := call(t, NewHandler(testPort, testToken, NewAPI(WithContents(&fakeGate{status: "running"}, p)), nil), "GET", "/api/profiles/dev/contents", "")
		if tc.contents == "" {
			wantErr(t, rec, 502, "docker_unavailable")
		} else if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != tc.contents {
			t.Fatalf("%s contents: %d %s", tc.name, rec.Code, rec.Body.String())
		}
		m := &fakeMgr{}
		rec = call(t, deleteGuardServer(m, &fakeGate{status: "running"}, p), "POST", deletePath, guardDeleteBody)
		if rec.Code != tc.deleteCode {
			t.Fatalf("%s delete: %d %s", tc.name, rec.Code, rec.Body.String())
		}
		if tc.deleteCode != 202 && len(m.submitReq) != 0 {
			t.Fatalf("%s: submit called", tc.name)
		}
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
