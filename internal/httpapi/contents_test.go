package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/dockerc/dockerctest"
)

const contentsURL = "/api/profiles/synthetic-a/contents"

func contentsServer(g ProfileGate, p dockerc.Provider) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithContents(g, p)), nil)
}

// fullFake holds containers of every state, images and volumes, with names a
// response must never echo.
func fullFake() *dockerctest.Fake {
	return &dockerctest.Fake{
		ContainerList: []dockerc.Container{
			{ID: "aaaaaaaaaaaa", Name: "synthetic-secret-run", State: "running"},
			{ID: "bbbbbbbbbbbb", Name: "synthetic-secret-exit", State: "exited"},
			{ID: "cccccccccccc", Name: "synthetic-secret-made", State: "created"},
		},
		ImageList:  []dockerc.Image{{ID: "i1", Tags: []string{"synthetic-secret/img:1"}}, {ID: "i2"}},
		VolumeList: []dockerc.Volume{{Name: "synthetic-secret-vol"}},
	}
}

func TestContentsNeedsToken(t *testing.T) {
	g, p := &fakeGate{status: "running"}, &dockerctest.Provider{Client: fullFake()}
	req := httptest.NewRequest("GET", contentsURL, nil)
	req.Host = goodHost
	rec := httptest.NewRecorder()
	contentsServer(g, p).ServeHTTP(rec, req)
	wantErr(t, rec, 401, "unauthorized")
	if len(g.names) != 0 || len(p.Profiles()) != 0 {
		t.Fatal("handler ran without token")
	}
}

func TestContentsCountsForARunningDockerProfile(t *testing.T) {
	fake := fullFake()
	p := &dockerctest.Provider{Client: fake}
	rec := call(t, contentsServer(&fakeGate{status: "Running"}, p), "GET", contentsURL, "")
	want := `{"applicable":true,"containers":3,"images":2,"volumes":1}` + "\n"
	if rec.Code != 200 || rec.Body.String() != want {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatal("a name leaked")
	}
	if got := p.Profiles(); len(got) != 1 || got[0] != "synthetic-a" {
		t.Fatalf("profiles %v", got)
	}
	// Only the three list reads, nothing that writes.
	for _, c := range fake.Calls() {
		if c.Method != "Containers" && c.Method != "Images" && c.Method != "Volumes" {
			t.Fatalf("unexpected call %s", c.Method)
		}
	}
}

func TestContentsEmptyEngineReportsZeros(t *testing.T) {
	p := &dockerctest.Provider{Client: &dockerctest.Fake{}}
	rec := call(t, contentsServer(&fakeGate{status: "running"}, p), "GET", contentsURL, "")
	want := `{"applicable":true,"containers":0,"images":0,"volumes":0}` + "\n"
	if rec.Code != 200 || rec.Body.String() != want {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestContentsNotApplicableForAnotherRuntime(t *testing.T) {
	p := &dockerctest.Provider{Err: dockerc.ErrRuntimeNotDocker}
	rec := call(t, contentsServer(&fakeGate{status: "running"}, p), "GET", contentsURL, "")
	if rec.Code != 200 || rec.Body.String() != `{"applicable":false}`+"\n" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestContentsRefusesAProfileThatIsNotRunning(t *testing.T) {
	for _, st := range []string{"stopped", "Stopped", "broken", "starting"} {
		g, p := &fakeGate{status: st}, &dockerctest.Provider{Client: fullFake()}
		w := wantErr(t, call(t, contentsServer(g, p), "GET", contentsURL, ""), 409, "invalid_state")
		if w.Error.Details["status"] != strings.ToLower(st) || len(p.Profiles()) != 0 {
			t.Fatalf("%q: %v %v", st, w.Error.Details, p.Profiles())
		}
	}
}

// The status read said running but the resolver does not find the VM running
// (a stop in between, or a name that differs from the listed one in letter
// case): never answered as stopped, because that could be false.
func TestContentsNeverReportsARunningVMAsStopped(t *testing.T) {
	p := &dockerctest.Provider{Err: dockerc.ErrVMNotRunning}
	wantErr(t, call(t, contentsServer(&fakeGate{status: "running"}, p), "GET", contentsURL, ""), 502, "docker_unavailable")
}

// caseGate answers like the production delete gate: a listed profile that
// differs only in letter case reports its own status.
type caseGate struct{ fakeGate }

func (g *caseGate) ProfileStatusForDelete(context.Context, string) (string, error) {
	return "running", nil
}

func TestContentsUsesTheCaseTolerantGate(t *testing.T) {
	g := &caseGate{fakeGate{status: "stopped"}}
	p := &dockerctest.Provider{Err: dockerc.ErrVMNotRunning}
	rec := call(t, contentsServer(g, p), "GET", "/api/profiles/DEV/contents", "")
	// The plain gate said stopped; the tolerant one said running, so the answer
	// is not a 409 "stopped".
	wantErr(t, rec, 502, "docker_unavailable")
	if len(g.names) != 0 {
		t.Fatal("plain gate used")
	}
}

func TestContentsDockerFailuresAre502WithoutDaemonText(t *testing.T) {
	for name, mk := range map[string]func() *dockerctest.Provider{
		"socket": func() *dockerctest.Provider { return &dockerctest.Provider{Err: dockerc.ErrDockerUnavailable} },
		"resolver": func() *dockerctest.Provider {
			return &dockerctest.Provider{Err: errors.New("exec /home/user/colima: boom")}
		},
		"containers": func() *dockerctest.Provider {
			f := fullFake()
			f.FailWith("Containers", errors.New("daemon says /var/lib/docker/secret"))
			return &dockerctest.Provider{Client: f}
		},
		"images": func() *dockerctest.Provider {
			f := fullFake()
			f.FailWith("Images", dockerc.ErrDaemon)
			return &dockerctest.Provider{Client: f}
		},
		"volumes": func() *dockerctest.Provider {
			f := fullFake()
			f.FailWith("Volumes", dockerc.ErrDockerUnavailable)
			return &dockerctest.Provider{Client: f}
		},
	} {
		rec := call(t, contentsServer(&fakeGate{status: "running"}, mk()), "GET", contentsURL, "")
		wantErr(t, rec, 502, "docker_unavailable")
		if strings.Contains(rec.Body.String(), "/") && strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "/home") {
			t.Fatalf("%s: leak %s", name, rec.Body.String())
		}
	}
}

func TestContentsNilProviderFailsClosed(t *testing.T) {
	wantErr(t, call(t, contentsServer(&fakeGate{status: "running"}, nil), "GET", contentsURL, ""), 502, "docker_unavailable")
}

func TestContentsHostileAndUnknownNames(t *testing.T) {
	for _, target := range []string{
		"/api/profiles/..%2Fx/contents", "/api/profiles/a%20b/contents", "/api/profiles/-p/contents", "/api/profiles/%00/contents",
	} {
		g, p := &fakeGate{err: ErrInvalidProfileName}, &dockerctest.Provider{Client: fullFake()}
		w := wantErr(t, call(t, contentsServer(g, p), "GET", target, ""), 404, "profile_not_found")
		if _, ok := w.Error.Details["name"]; ok || len(p.Profiles()) != 0 {
			t.Fatalf("%s: name echoed or docker reached", target)
		}
	}
	g, p := &fakeGate{err: ErrProfileNotFound}, &dockerctest.Provider{Client: fullFake()}
	wantErr(t, call(t, contentsServer(g, p), "GET", contentsURL, ""), 404, "profile_not_found")
	if len(p.Profiles()) != 0 {
		t.Fatal("docker reached for an unknown profile")
	}
	wantErr(t, call(t, contentsServer(&fakeGate{err: errors.New("exec /x: boom")}, p), "GET", contentsURL, ""), 502, "colima_failed")
}

// The route is a read: it takes no job manager, so it cannot check busy or
// write an audit row. Only GET is routed.
func TestContentsOnlyAnswersGet(t *testing.T) {
	p := &dockerctest.Provider{Client: fullFake()}
	for _, m := range []string{"POST", "PUT", "DELETE"} {
		rec := call(t, contentsServer(&fakeGate{status: "running"}, p), m, contentsURL, "")
		if rec.Code == 200 || len(p.Profiles()) != 0 {
			t.Fatalf("%s answered %d", m, rec.Code)
		}
	}
}
