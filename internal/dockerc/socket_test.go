package dockerc

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// fakeColima answers list and status from canned JSON and records calls.
type fakeColima struct {
	list, status string
	statusExit   int
	calls        [][]string
}

func (f *fakeColima) Run(_ context.Context, args []string) (colima.Result, error) {
	f.calls = append(f.calls, args)
	switch args[0] {
	case "list":
		return colima.Result{Stdout: []byte(f.list)}, nil
	case "status":
		return colima.Result{Stdout: []byte(f.status), ExitCode: f.statusExit}, nil
	}
	return colima.Result{ExitCode: 2}, nil
}

func listFor(status, runtime string) string {
	return `{"name":"synthetic-a","status":"` + status + `","runtime":"` + runtime + `","cpus":2,"memory":1,"disk":1}`
}

func statusFor(runtime, sock string) string {
	return `{"runtime":"` + runtime + `","docker_socket":"` + sock + `"}`
}

func TestResolverHappyPath(t *testing.T) {
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(listJSON)) }))
	f := &fakeColima{list: listFor("Running", "docker"), status: statusFor("docker", "unix://"+sock)}
	p := NewProvider(NewColimaResolver(f))
	c, err := p.For(context.Background(), "synthetic-a")
	if err != nil {
		t.Fatal(err)
	}
	cs, err := c.Containers(context.Background())
	if err != nil || len(cs) != 2 {
		t.Fatalf("cs=%v err=%v", cs, err)
	}
}

func TestResolverErrors(t *testing.T) {
	plainFile := t.TempDir() + "/file"
	if err := writeFile(plainFile); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		f    *fakeColima
		prof string
		want error
	}{
		{"stopped", &fakeColima{list: listFor("Stopped", "docker")}, "synthetic-a", ErrVMNotRunning},
		{"unknown profile", &fakeColima{list: listFor("Running", "docker")}, "synthetic-b", ErrVMNotRunning},
		{"empty list", &fakeColima{list: ""}, "synthetic-a", ErrVMNotRunning},
		{"containerd runtime", &fakeColima{list: listFor("Running", "containerd")}, "synthetic-a", ErrRuntimeNotDocker},
		{"incus runtime", &fakeColima{list: listFor("Running", "incus")}, "synthetic-a", ErrRuntimeNotDocker},
		{"status runtime differs", &fakeColima{list: listFor("Running", "docker"), status: statusFor("containerd", "")}, "synthetic-a", ErrRuntimeNotDocker},
		{"socket file missing", &fakeColima{list: listFor("Running", "docker"), status: statusFor("docker", "unix:///nonexistent/dir/docker.sock")}, "synthetic-a", ErrDockerUnavailable},
		{"not a socket", &fakeColima{list: listFor("Running", "docker"), status: statusFor("docker", "unix://"+plainFile)}, "synthetic-a", ErrDockerUnavailable},
		{"empty socket", &fakeColima{list: listFor("Running", "docker"), status: statusFor("docker", "")}, "synthetic-a", ErrDockerUnavailable},
		{"relative socket", &fakeColima{list: listFor("Running", "docker"), status: statusFor("docker", "unix://rel/docker.sock")}, "synthetic-a", ErrDockerUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewProvider(NewColimaResolver(tc.f)).For(context.Background(), tc.prof)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), "/nonexistent") || strings.Contains(err.Error(), plainFile) {
				t.Fatalf("error leaks a path: %q", err)
			}
		})
	}
}

func TestResolverRejectsBadProfileBeforeRunning(t *testing.T) {
	f := &fakeColima{list: listFor("Running", "docker")}
	_, err := NewProvider(NewColimaResolver(f)).For(context.Background(), "../x; rm")
	var ip *colima.InvalidProfileError
	if !errors.As(err, &ip) || len(f.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
}

func TestRunningProfileWithDeadSocketIsUnavailable(t *testing.T) {
	// Socket exists (resolver passes) but nobody listens: dial error on a
	// "Running" profile must be ErrDockerUnavailable, not a raw error.
	sock := startFakeDocker(t, http.NotFoundHandler())
	f := &fakeColima{list: listFor("Running", "docker"), status: statusFor("docker", "unix://"+sock)}
	c, err := NewProvider(NewColimaResolver(f)).For(context.Background(), "synthetic-a")
	if err != nil {
		t.Fatal(err)
	}
	closeSockListener(t, sock)
	if _, err := c.Containers(context.Background()); !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
