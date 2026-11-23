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
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

func engineServer(g ProfileGate, p dockerc.Provider) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithEngine(g, p)), nil)
}

const engineURL = "/api/profiles/synthetic-a/engine"

func TestEngineShape(t *testing.T) {
	fc := &fakeDockerClient{
		info: dockerc.Engine{
			Version: "27.3.1", APIVersion: "1.47", OS: "linux", Arch: "aarch64",
			CPUs: 4, MemoryBytes: 8350000000,
			Containers: dockerc.ContainerCounts{Total: 5, Running: 3, Paused: 0, Stopped: 2},
			Images:     12,
		},
		disk: dockerc.DiskUsage{ImagesBytes: 1, ContainersBytes: 2, VolumesBytes: 3, BuildCacheBytes: 4, LayersBytes: 5},
	}
	rec := call(t, engineServer(&fakeGate{status: "Running"}, &fakeProvider{client: fc}), "GET", engineURL, "")
	want := `{"version":"27.3.1","apiVersion":"1.47","os":"linux","arch":"aarch64","cpus":4,"memoryBytes":8350000000,` +
		`"containers":{"total":5,"running":3,"paused":0,"stopped":2},"images":12,` +
		`"diskUsage":{"imagesBytes":1,"containersBytes":2,"volumesBytes":3,"buildCacheBytes":4,"layersBytes":5}}` + "\n"
	if rec.Code != 200 || rec.Body.String() != want {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

// socketProvider serves h on a real Unix socket through dockerc.NewProvider,
// so the route is tested against the real /info decoding.
func socketProvider(t *testing.T, h http.Handler) dockerc.Provider {
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
	return dockerc.NewProvider(func(context.Context, string) (string, error) { return sock, nil })
}

func TestEngineDoesNotLeakHostNameOrDataRoot(t *testing.T) {
	p := socketProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.41/info":
			w.Write([]byte(`{"ServerVersion":"27.3.1","OSType":"linux","OperatingSystem":"Synthetic Linux",
			 "Architecture":"aarch64","NCPU":2,"MemTotal":4000000000,"Containers":1,"ContainersRunning":1,
			 "ContainersPaused":0,"ContainersStopped":0,"Images":3,
			 "Name":"synthetic-host-name","DockerRootDir":"/synthetic/data-root",
			 "RegistryConfig":{"IndexConfigs":{"synthetic.example":{}}}}`))
		case "/v1.41/version":
			w.Write([]byte(`{"ApiVersion":"1.47"}`))
		case "/v1.41/system/df":
			w.Write([]byte(`{"LayersSize":10,"Images":[],"Containers":[],"Volumes":[],"BuildCache":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	rec := call(t, engineServer(&fakeGate{status: "running"}, p), "GET", engineURL, "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, bad := range []string{"synthetic-host-name", "synthetic/data-root", "DockerRootDir", "synthetic.example", "Synthetic Linux"} {
		if strings.Contains(body, bad) {
			t.Fatalf("%q leaked: %s", bad, body)
		}
	}
	if !strings.Contains(body, `"apiVersion":"1.47"`) || !strings.Contains(body, `"os":"linux"`) || !strings.Contains(body, `"layersBytes":10`) {
		t.Fatalf("body: %s", body)
	}
}

func TestEngineCallsRunConcurrently(t *testing.T) {
	infoStarted, diskStarted := make(chan struct{}), make(chan struct{})
	fc := &fakeDockerClient{
		infoFn: func(ctx context.Context) (dockerc.Engine, error) {
			close(infoStarted)
			select {
			case <-diskStarted:
				return dockerc.Engine{Version: "1"}, nil
			case <-time.After(3 * time.Second):
				return dockerc.Engine{}, errors.New("disk never started")
			}
		},
		diskFn: func(ctx context.Context) (dockerc.DiskUsage, error) {
			close(diskStarted)
			select {
			case <-infoStarted:
				return dockerc.DiskUsage{}, nil
			case <-time.After(3 * time.Second):
				return dockerc.DiskUsage{}, errors.New("info never started")
			}
		},
	}
	rec := call(t, engineServer(&fakeGate{status: "running"}, &fakeProvider{client: fc}), "GET", engineURL, "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestEngineEitherFailureIs502AndCancelsTheOther(t *testing.T) {
	blockUntilCancel := func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
			return errors.New("not cancelled")
		}
	}
	cases := map[string]*fakeDockerClient{
		"info fails": {
			infoErr: dockerc.ErrDockerUnavailable,
			diskFn: func(ctx context.Context) (dockerc.DiskUsage, error) {
				return dockerc.DiskUsage{}, blockUntilCancel(ctx)
			},
		},
		"disk fails": {
			diskErr: dockerc.ErrDockerUnavailable,
			infoFn: func(ctx context.Context) (dockerc.Engine, error) {
				return dockerc.Engine{}, blockUntilCancel(ctx)
			},
		},
	}
	for name, fc := range cases {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			rec := call(t, engineServer(&fakeGate{status: "running"}, &fakeProvider{client: fc}), "GET", engineURL, "")
			wantErr(t, rec, 502, "docker_unavailable")
			if time.Since(start) > 2*time.Second {
				t.Fatal("the other call was not cancelled")
			}
		})
	}
}

func TestEngineUnexpectedErrorIs500WithoutDetail(t *testing.T) {
	fc := &fakeDockerClient{infoErr: errors.New("/home/user/secret")}
	rec := call(t, engineServer(&fakeGate{status: "running"}, &fakeProvider{client: fc}), "GET", engineURL, "")
	wantErr(t, rec, 500, "internal_error")
	if strings.Contains(rec.Body.String(), "/home/user") {
		t.Fatalf("cause leaked: %s", rec.Body.String())
	}
}

func TestEngineStoppedAndNonDocker(t *testing.T) {
	p := &fakeProvider{err: errors.New("must not be called")}
	w := wantErr(t, call(t, engineServer(&fakeGate{status: "stopped"}, p), "GET", engineURL, ""), 409, "invalid_state")
	if w.Error.Details["status"] != "stopped" {
		t.Fatalf("details %v", w.Error.Details)
	}
	w = wantErr(t, call(t, engineServer(&fakeGate{status: "running"}, &fakeProvider{err: dockerc.ErrVMNotRunning}), "GET", engineURL, ""), 409, "invalid_state")
	if w.Error.Details["status"] != "stopped" {
		t.Fatalf("details %v", w.Error.Details)
	}
	w = wantErr(t, call(t, engineServer(&fakeGate{status: "running"}, &fakeProvider{err: dockerc.ErrRuntimeNotDocker}), "GET", engineURL, ""), 409, "invalid_state")
	if w.Error.Details["runtime"] != "not_docker" || w.Error.Details["status"] != nil ||
		!strings.Contains(w.Error.Message, "Docker runtime") {
		t.Fatalf("non-docker: %+v", w.Error)
	}
}

func TestEngineProviderAndGateErrors(t *testing.T) {
	wantErr(t, call(t, engineServer(&fakeGate{status: "running"}, &fakeProvider{err: dockerc.ErrDockerUnavailable}), "GET", engineURL, ""), 502, "docker_unavailable")
	p := &fakeProvider{err: errors.New("must not be called")}
	wantErr(t, call(t, engineServer(&fakeGate{err: ErrProfileNotFound}, p), "GET", "/api/profiles/nope/engine", ""), 404, "profile_not_found")
	wantErr(t, call(t, engineServer(&fakeGate{err: ErrInvalidProfileName}, p), "GET", "/api/profiles/bad/engine", ""), 404, "profile_not_found")
	wantErr(t, call(t, engineServer(&fakeGate{err: errors.New("boom")}, p), "GET", "/api/profiles/default/engine", ""), 502, "colima_failed")
}

func TestEngineNeedsToken(t *testing.T) {
	g := &fakeGate{status: "running"}
	req := httptest.NewRequest("GET", engineURL, nil)
	req.Host = goodHost
	rec := httptest.NewRecorder()
	engineServer(g, &fakeProvider{client: &fakeDockerClient{}}).ServeHTTP(rec, req)
	wantErr(t, rec, 401, "unauthorized")
	if len(g.names) != 0 {
		t.Fatal("gate reached without token")
	}
}
