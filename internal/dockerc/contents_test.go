package dockerc

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
)

// contentsHandler answers the three list endpoints and records every request.
type contentsHandler struct {
	mu       sync.Mutex
	requests []string
	fail     string // path suffix that answers 500
}

func (h *contentsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.requests = append(h.requests, r.Method+" "+r.URL.RequestURI())
	h.mu.Unlock()
	if h.fail != "" && r.URL.Path == "/v1.41"+h.fail {
		http.Error(w, "boom /secret/path", http.StatusInternalServerError)
		return
	}
	switch r.URL.Path {
	case "/v1.41/containers/json":
		w.Write([]byte(`[{"Id":"aaaaaaaaaaaa","Names":["/synthetic-a"],"State":"exited","ImageID":"sha256:i1"},{"Id":"bbbbbbbbbbbb","Names":["/synthetic-b"],"State":"running","ImageID":"sha256:i1"}]`))
	case "/v1.41/images/json":
		w.Write([]byte(`[{"Id":"sha256:i1","RepoTags":["synthetic/a:1"]},{"Id":"sha256:i2","RepoTags":null}]`))
	case "/v1.41/system/df":
		w.Write([]byte(`{"Volumes":[{"Name":"synthetic-v","Driver":"local"}]}`))
	default:
		http.NotFound(w, r)
	}
}

func TestCountContentsAgainstTheRealClient(t *testing.T) {
	h := &contentsHandler{}
	c := newHTTPClient(startFakeDocker(t, h))
	got, err := CountContents(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Contents{Containers: 2, Images: 2, Volumes: 1}); got != want || got.Empty() {
		t.Fatalf("got %+v", got)
	}
	// Stopped containers must count: the container list asks for every state.
	if h.requests[0] != "GET /v1.41/containers/json?all=1" {
		t.Fatalf("requests = %v", h.requests)
	}
	for _, r := range h.requests {
		if r[:3] != "GET" {
			t.Fatalf("non-read request %q", r)
		}
	}
}

func TestCountContentsFailsClosed(t *testing.T) {
	for _, path := range []string{"/containers/json", "/images/json", "/system/df"} {
		h := &contentsHandler{fail: path}
		c := newHTTPClient(startFakeDocker(t, h))
		got, err := CountContents(context.Background(), c)
		if err == nil || !got.Empty() {
			t.Fatalf("%s: got %+v err %v", path, got, err)
		}
	}
}

func TestProfileContentsOutcomes(t *testing.T) {
	sock := startFakeDocker(t, &contentsHandler{})
	ok := &fakeColima{list: listFor("Running", "docker"), status: statusFor("docker", "unix://"+sock)}
	c, applicable, err := ProfileContents(context.Background(), NewProvider(NewColimaResolver(ok)), "synthetic-a")
	if err != nil || !applicable || c.Containers != 2 {
		t.Fatalf("%+v %v %v", c, applicable, err)
	}

	// Another runtime: not applicable, no error, and no status call.
	other := &fakeColima{list: listFor("Running", "containerd")}
	_, applicable, err = ProfileContents(context.Background(), NewProvider(NewColimaResolver(other)), "synthetic-a")
	if err != nil || applicable {
		t.Fatalf("containerd: applicable=%v err=%v", applicable, err)
	}

	for name, tc := range map[string]struct {
		f    *fakeColima
		want error
	}{
		"stopped":   {&fakeColima{list: listFor("Stopped", "docker")}, ErrVMNotRunning},
		"no socket": {&fakeColima{list: listFor("Running", "docker"), status: statusFor("docker", "unix:///nonexistent/d.sock")}, ErrDockerUnavailable},
	} {
		_, applicable, err := ProfileContents(context.Background(), NewProvider(NewColimaResolver(tc.f)), "synthetic-a")
		if !errors.Is(err, tc.want) || applicable {
			t.Fatalf("%s: applicable=%v err=%v", name, applicable, err)
		}
	}
}

// The runtime name is compared ignoring letter case, in the list and in status.
func TestResolverRuntimeIgnoresCase(t *testing.T) {
	sock := startFakeDocker(t, &contentsHandler{})
	f := &fakeColima{list: listFor("Running", "Docker"), status: statusFor("DOCKER", "unix://"+sock)}
	if _, err := NewProvider(NewColimaResolver(f)).For(context.Background(), "synthetic-a"); err != nil {
		t.Fatal(err)
	}
}

// A hostile profile name never reaches colima or the socket.
func TestProfileContentsHostileNameMakesNoCall(t *testing.T) {
	h := &contentsHandler{}
	f := &fakeColima{list: listFor("Running", "docker"), status: statusFor("docker", "unix://"+startFakeDocker(t, h))}
	for _, name := range []string{"../x", "a b", "-p", "a;rm", "", "a\x00b", "A/../b"} {
		if _, _, err := ProfileContents(context.Background(), NewProvider(NewColimaResolver(f)), name); err == nil {
			t.Fatalf("%q accepted", name)
		}
	}
	if len(f.calls) != 0 || len(h.requests) != 0 {
		t.Fatalf("colima calls %v socket requests %v", f.calls, h.requests)
	}
}
