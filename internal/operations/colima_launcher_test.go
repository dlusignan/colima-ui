package operations

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/dockerc"
)

// argsStreamer is a colima.Streamer fake: no binary is ever run.
type argsStreamer struct{ args [][]string }

func (s *argsStreamer) Stream(_ context.Context, args []string) (colima.Process, error) {
	s.args = append(s.args, args)
	return newFakeProc(), nil
}

func TestColimaLauncherMapsActionsToAdapterCalls(t *testing.T) {
	s := &argsStreamer{}
	l := ColimaLauncher{S: s}
	for _, a := range []Action{ActionStart, ActionStop, ActionRestart, ActionUpdate} {
		if _, err := l.Launch(context.Background(), Request{Action: a, Profile: "dev"}); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	want := [][]string{
		{"start", "-p", "dev"},
		{"stop", "-p", "dev"},
		{"restart", "-p", "dev"},
		{"update", "-p", "dev"},
	}
	if !reflect.DeepEqual(s.args, want) {
		t.Fatalf("args = %v, want %v", s.args, want)
	}
}

func TestColimaLauncherRejectsUnknownActionAndBadProfile(t *testing.T) {
	s := &argsStreamer{}
	l := ColimaLauncher{S: s}
	if _, err := l.Launch(context.Background(), Request{Action: "bogus", Profile: "dev"}); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("err = %v", err)
	}
	if _, err := l.Launch(context.Background(), Request{Action: ActionStart, Profile: "../x"}); err == nil {
		t.Fatal("want invalid profile error")
	}
	if len(s.args) != 0 {
		t.Fatalf("streamer called: %v", s.args)
	}
}

func TestColimaLauncherCreateRendersEveryOption(t *testing.T) {
	s := &argsStreamer{}
	l := ColimaLauncher{S: s, Limits: colima.Limits{MaxCPU: 8, MaxMemoryGiB: 32, MaxDiskGiB: 500}}
	cpus, mem, disk, root := 4, 8.5, 100, 20
	opts := colima.StartOptions{
		CPUs: &cpus, MemoryGiB: &mem, DiskGiB: &disk, RootDiskGiB: &root,
		Runtime: colima.RuntimeDocker, VMType: colima.VMTypeVZ, Arch: colima.ArchAarch64,
		MountType: colima.MountVirtiofs, Kubernetes: true, KubernetesVersion: "v1.31.2+k3s1",
	}
	if _, err := l.Launch(context.Background(), Request{Action: ActionCreate, Profile: "dev", Options: opts}); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"start", "-p", "dev", "--cpus", "4", "--memory", "8.5", "--disk", "100", "--root-disk", "20",
		"--runtime", "docker", "--vm-type", "vz", "--arch", "aarch64", "--mount-type", "virtiofs",
		"--kubernetes", "--kubernetes-version", "v1.31.2+k3s1"}}
	if !reflect.DeepEqual(s.args, want) {
		t.Fatalf("args = %v, want %v", s.args, want)
	}
}

func TestColimaLauncherCreateWithoutOptionsOmitsFlags(t *testing.T) {
	s := &argsStreamer{}
	l := ColimaLauncher{S: s}
	if _, err := l.Launch(context.Background(), Request{Action: ActionCreate, Profile: "dev"}); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"start", "-p", "dev"}}; !reflect.DeepEqual(s.args, want) {
		t.Fatalf("args = %v, want %v", s.args, want)
	}
}

func TestColimaLauncherCreateBackstopRejectsBadOptions(t *testing.T) {
	cpus := 99
	bigDisk := 1 << 40
	cases := map[string]colima.StartOptions{
		"cpus over limit":   {CPUs: &cpus},
		"disk over limit":   {DiskGiB: &bigDisk},
		"flag-like runtime": {Runtime: "--edit"},
		"flag-like version": {KubernetesVersion: "--edit"},
	}
	for name, o := range cases {
		s := &argsStreamer{}
		l := ColimaLauncher{S: s, Limits: colima.Limits{MaxCPU: 8, MaxMemoryGiB: 32, MaxDiskGiB: 500}}
		if _, err := l.Launch(context.Background(), Request{Action: ActionCreate, Profile: "dev", Options: o}); err == nil {
			t.Errorf("%s: want error", name)
		}
		if len(s.args) != 0 {
			t.Errorf("%s: streamer called: %v", name, s.args)
		}
	}
	// A zero Limits (the old wiring) permits no resource option.
	s := &argsStreamer{}
	one := 1
	if _, err := (ColimaLauncher{S: s}).Launch(context.Background(), Request{Action: ActionCreate, Profile: "dev", Options: colima.StartOptions{CPUs: &one}}); err == nil {
		t.Error("zero limits must reject a resource flag")
	}
}

func TestColimaLauncherDeleteArgv(t *testing.T) {
	s := &argsStreamer{}
	l := ColimaLauncher{S: s}
	for _, data := range []bool{false, true} {
		if _, err := l.Launch(context.Background(), Request{Action: ActionDelete, Profile: "dev", DeleteData: data}); err != nil {
			t.Fatal(err)
		}
	}
	want := [][]string{
		{"delete", "-p", "dev", "--force"},
		{"delete", "-p", "dev", "--force", "--data"},
	}
	if !reflect.DeepEqual(s.args, want) {
		t.Fatalf("args = %v, want %v", s.args, want)
	}
	if _, err := l.Launch(context.Background(), Request{Action: ActionDelete, Profile: "../x"}); err == nil {
		t.Fatal("want invalid profile error")
	}
	if len(s.args) != 2 {
		t.Fatalf("streamer called for a hostile name: %v", s.args)
	}
}

func emptyDocker(context.Context, string) (dockerc.Contents, bool, error) {
	return dockerc.Contents{}, true, nil
}

func TestColimaLauncherDeleteRechecksStatusInsideTheSlot(t *testing.T) {
	for _, c := range []struct {
		status string
		runs   bool
		reason string
	}{
		{"Broken", true, ""}, {"broken", true, ""}, {"running", true, ""}, {"Running", true, ""},
		{"stopped", false, NotRunCannotVerify}, {"Stopped", false, NotRunCannotVerify},
		{"starting", false, NotRunState}, {"unknown", false, NotRunState}, {"", false, NotRunState},
	} {
		s := &argsStreamer{}
		l := ColimaLauncher{S: s, DockerContents: emptyDocker,
			VMStatus: func(context.Context, string) (string, error) { return c.status, nil }}
		p, err := l.Launch(context.Background(), Request{Action: ActionDelete, Profile: "dev", DeleteData: true})
		if err != nil {
			t.Fatalf("%q: %v", c.status, err)
		}
		if (len(s.args) == 1) != c.runs {
			t.Fatalf("%q: streamer calls %v, want run=%v", c.status, s.args, c.runs)
		}
		if !c.runs {
			refusedLines(t, c.status, p, c.reason)
		}
	}
}

// refusedLines drains a refused process and checks it printed one line, failed,
// carries the reason token and says it never ran colima.
func refusedLines(t *testing.T, label string, p colima.Process, reason string) {
	t.Helper()
	var lines []string
	for l := range p.Lines() {
		lines = append(lines, l)
	}
	exit, werr := p.Wait()
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "Not deleted") || exit.ExitCode != 1 || werr != nil {
		t.Fatalf("%q: lines %v exit %+v %v", label, lines, exit, werr)
	}
	nr, ok := p.(interface {
		NotRun() bool
		NotRunReason() string
	})
	if !ok || !nr.NotRun() || nr.NotRunReason() != reason {
		t.Fatalf("%q: not-run marker %v reason %q, want %q", label, ok, nr.NotRunReason(), reason)
	}
	p.Cancel()
}

func TestColimaLauncherDeleteCountsDockerInsideTheSlot(t *testing.T) {
	running := func(context.Context, string) (string, error) { return "running", nil }
	for _, c := range []struct {
		name   string
		hook   func(context.Context, string) (dockerc.Contents, bool, error)
		runs   bool
		reason string
	}{
		{"empty", emptyDocker, true, ""},
		{"not docker", func(context.Context, string) (dockerc.Contents, bool, error) { return dockerc.Contents{}, false, nil }, true, ""},
		{"container", func(context.Context, string) (dockerc.Contents, bool, error) {
			return dockerc.Contents{Containers: 1}, true, nil
		}, false, NotRunDockerNotEmpty},
		{"image", func(context.Context, string) (dockerc.Contents, bool, error) {
			return dockerc.Contents{Images: 1}, true, nil
		}, false, NotRunDockerNotEmpty},
		{"volume", func(context.Context, string) (dockerc.Contents, bool, error) {
			return dockerc.Contents{Volumes: 1}, true, nil
		}, false, NotRunDockerNotEmpty},
		{"read error", func(context.Context, string) (dockerc.Contents, bool, error) {
			return dockerc.Contents{}, false, errors.New("boom")
		}, false, NotRunDockerUnreadable},
		{"docker unavailable", func(context.Context, string) (dockerc.Contents, bool, error) {
			return dockerc.Contents{}, false, dockerc.ErrDockerUnavailable
		}, false, NotRunDockerUnreadable},
		{"vm stopped meanwhile", func(context.Context, string) (dockerc.Contents, bool, error) {
			return dockerc.Contents{}, false, dockerc.ErrVMNotRunning
		}, false, NotRunDockerUnreadable},
		{"no hook wired", nil, false, NotRunDockerUnreadable},
	} {
		s := &argsStreamer{}
		l := ColimaLauncher{S: s, VMStatus: running, DockerContents: c.hook}
		p, err := l.Launch(context.Background(), Request{Action: ActionDelete, Profile: "dev"})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if (len(s.args) == 1) != c.runs {
			t.Fatalf("%s: streamer calls %v, want run=%v", c.name, s.args, c.runs)
		}
		if !c.runs {
			refusedLines(t, c.name, p, c.reason)
		}
	}
}

// A broken VM is always deleted and Docker is never read.
func TestColimaLauncherDeleteBrokenNeverReadsDocker(t *testing.T) {
	s := &argsStreamer{}
	l := ColimaLauncher{S: s,
		VMStatus: func(context.Context, string) (string, error) { return "broken", nil },
		DockerContents: func(context.Context, string) (dockerc.Contents, bool, error) {
			t.Fatal("docker read for a broken profile")
			return dockerc.Contents{}, false, nil
		}}
	if _, err := l.Launch(context.Background(), Request{Action: ActionDelete, Profile: "dev"}); err != nil || len(s.args) != 1 {
		t.Fatalf("err %v args %v", err, s.args)
	}
}

// Unknown, empty and disagreeing runtimes fail closed in the slot, through the
// real resolver; docker+k3s is counted.
func TestColimaLauncherDeleteRuntimeClassificationInTheSlot(t *testing.T) {
	for _, c := range []struct {
		runtime string
		runs    bool
		reason  string
	}{
		{"docker", false, NotRunDockerNotEmpty},
		{"docker+k3s", false, NotRunDockerNotEmpty},
		{"containerd", true, ""},
		{"", false, NotRunDockerUnreadable},
		{"podman", false, NotRunDockerUnreadable},
	} {
		sock := startDockerSocket(t)
		rn := runtimeRunner{runtime: c.runtime, sock: sock}
		prov := dockerc.NewProvider(dockerc.NewColimaResolver(rn))
		s := &argsStreamer{}
		l := ColimaLauncher{S: s,
			VMStatus: func(context.Context, string) (string, error) { return "running", nil },
			DockerContents: func(ctx context.Context, profile string) (dockerc.Contents, bool, error) {
				return dockerc.ProfileContents(ctx, prov, profile)
			}}
		p, err := l.Launch(context.Background(), Request{Action: ActionDelete, Profile: "dev"})
		if err != nil || (len(s.args) == 1) != c.runs {
			t.Fatalf("%q: err %v args %v", c.runtime, err, s.args)
		}
		if !c.runs {
			refusedLines(t, c.runtime, p, c.reason)
		}
	}
}

// A running task job (an image pull) on the profile refuses a delete under the
// manager's own lock, and a task on another profile does not.
func TestDeleteRefusedWhileATaskRuns(t *testing.T) {
	m := New(Options{Launcher: ColimaLauncher{S: &argsStreamer{}}})
	release := make(chan struct{})
	task, err := m.SubmitTask(TaskRequest{Action: ActionImagePull, Profile: "Dev", Subject: "synthetic:1",
		Run: func(ctx context.Context, _ func(string)) error {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := m.TaskRunning("dev"); !ok || id != task.ID {
		t.Fatalf("TaskRunning = %q %v", id, ok)
	}
	var tr *TaskRunningError
	if _, err := m.Submit(Request{Action: ActionDelete, Profile: "dev"}); !errors.As(err, &tr) || tr.JobID != task.ID {
		t.Fatalf("err %v", err)
	}
	if _, ok := m.TaskRunning("other"); ok {
		t.Fatal("task seen on another profile")
	}
	close(release)
	deadline := time.After(5 * time.Second)
	for {
		if _, ok := m.TaskRunning("dev"); !ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("task never ended")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := m.Submit(Request{Action: ActionDelete, Profile: "dev"}); err != nil {
		t.Fatalf("delete after the task ended: %v", err)
	}
}

// The route saw an empty engine; a container appears before the slot count.
// Through the manager the job fails, colima never starts, and the summary says
// why.
func TestDeleteRefusedWhenDockerFlipsBetweenRouteAndSlot(t *testing.T) {
	s := &argsStreamer{}
	var reads int
	hook := func(context.Context, string) (dockerc.Contents, bool, error) {
		reads++
		if reads == 1 { // the route's read
			return dockerc.Contents{}, true, nil
		}
		return dockerc.Contents{Containers: 1}, true, nil
	}
	if c, ok, _ := hook(context.Background(), "dev"); !c.Empty() || !ok {
		t.Fatal("first read must be empty")
	}
	m := New(Options{Launcher: ColimaLauncher{S: s, DockerContents: hook,
		VMStatus: func(context.Context, string) (string, error) { return "running", nil }}})
	sum, err := m.Submit(Request{Action: ActionDelete, Profile: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		got, _ := m.Get(sum.ID)
		if got.State == StateFailed {
			if !got.NotRun || got.NotRunReason != NotRunDockerNotEmpty || len(s.args) != 0 {
				t.Fatalf("summary %+v args %v", got, s.args)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("state %s", got.State)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestColimaLauncherDeleteStatusErrorStartsNothing(t *testing.T) {
	s := &argsStreamer{}
	l := ColimaLauncher{S: s, VMStatus: func(context.Context, string) (string, error) { return "", errors.New("boom") }}
	if _, err := l.Launch(context.Background(), Request{Action: ActionDelete, Profile: "dev"}); err == nil {
		t.Fatal("want error")
	}
	if len(s.args) != 0 {
		t.Fatalf("streamer called: %v", s.args)
	}
}

// A refused delete ends as a failed job through the manager, and its slot is
// released, so the user can stop the profile and try again.
func TestRefusedDeleteIsAFailedJob(t *testing.T) {
	s := &argsStreamer{}
	m := New(Options{Launcher: ColimaLauncher{S: s, VMStatus: func(context.Context, string) (string, error) { return "stopped", nil }}})
	sum, err := m.Submit(Request{Action: ActionDelete, Profile: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		got, _ := m.Get(sum.ID)
		if got.State == StateFailed {
			if len(got.Output) != 1 || len(s.args) != 0 || !got.NotRun {
				t.Fatalf("output %v args %v", got.Output, s.args)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("state %s", got.State)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, running := m.Busy("dev"); running {
		t.Fatal("slot still held")
	}
}

func k8sLauncher(s colima.Streamer, status string, enabled bool) ColimaLauncher {
	return ColimaLauncher{
		S:                 s,
		VMStatus:          func(context.Context, string) (string, error) { return status, nil },
		KubernetesEnabled: func(string) (bool, error) { return enabled, nil },
	}
}

var k8sActions = []Action{ActionKubernetesStart, ActionKubernetesStop, ActionKubernetesReset, ActionKubernetesDelete}

func TestColimaLauncherKubernetesArgv(t *testing.T) {
	s := &argsStreamer{}
	l := k8sLauncher(s, "Running", true)
	for _, a := range k8sActions {
		if !a.IsKubernetes() {
			t.Fatalf("%s: IsKubernetes false", a)
		}
		if _, err := l.Launch(context.Background(), Request{Action: a, Profile: "dev"}); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	want := [][]string{
		{"kubernetes", "start", "-p", "dev"},
		{"kubernetes", "stop", "-p", "dev"},
		{"kubernetes", "reset", "-p", "dev"},
		{"kubernetes", "delete", "-p", "dev"},
	}
	if !reflect.DeepEqual(s.args, want) {
		t.Fatalf("args = %v, want %v", s.args, want)
	}
	if ActionStart.IsKubernetes() {
		t.Fatal("start is not a kubernetes action")
	}
}

// A stopped cluster is the normal state for Start: only the configuration
// decides, never whether k3s is running.
func TestColimaLauncherKubernetesRefusalsAreFailedJobsNotRuns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  string
		enabled bool
	}{
		{"stopped", "stopped", true},
		{"broken", "broken", true},
		{"empty", "", true},
		{"disabled", "running", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &argsStreamer{}
			p, err := k8sLauncher(s, tc.status, tc.enabled).Launch(context.Background(), Request{Action: ActionKubernetesReset, Profile: "dev"})
			if err != nil || p == nil {
				t.Fatalf("p=%v err=%v", p, err)
			}
			if nr, ok := p.(interface{ NotRun() bool }); !ok || !nr.NotRun() {
				t.Fatal("refusal must be marked NotRun")
			}
			if ex, _ := p.Wait(); ex.ExitCode == 0 {
				t.Fatal("refusal must fail")
			}
			if len(s.args) != 0 {
				t.Fatalf("streamer called: %v", s.args)
			}
		})
	}
}

func TestColimaLauncherKubernetesFailsClosed(t *testing.T) {
	boom := errors.New("boom")
	for _, l := range []ColimaLauncher{
		{S: &argsStreamer{}}, // no hooks at all
		{S: &argsStreamer{}, VMStatus: func(context.Context, string) (string, error) { return "running", nil }},
		{S: &argsStreamer{}, KubernetesEnabled: func(string) (bool, error) { return true, nil }},
		{S: &argsStreamer{}, VMStatus: func(context.Context, string) (string, error) { return "", boom },
			KubernetesEnabled: func(string) (bool, error) { return true, nil }},
		{S: &argsStreamer{}, VMStatus: func(context.Context, string) (string, error) { return "running", nil },
			KubernetesEnabled: func(string) (bool, error) { return false, boom }},
	} {
		s := &argsStreamer{}
		l.S = s
		if _, err := l.Launch(context.Background(), Request{Action: ActionKubernetesStop, Profile: "dev"}); err == nil {
			t.Fatal("want an error")
		}
		if len(s.args) != 0 {
			t.Fatalf("colima ran: %v", s.args)
		}
	}
}

func TestColimaLauncherKubernetesRejectsBadProfileBeforeAnyRead(t *testing.T) {
	s := &argsStreamer{}
	calls := 0
	l := ColimaLauncher{S: s,
		VMStatus:          func(context.Context, string) (string, error) { calls++; return "running", nil },
		KubernetesEnabled: func(string) (bool, error) { calls++; return true, nil }}
	for _, a := range k8sActions {
		if _, err := l.Launch(context.Background(), Request{Action: a, Profile: "--all"}); err == nil {
			t.Fatalf("%s: want invalid profile error", a)
		}
	}
	if len(s.args) != 0 || calls != 0 {
		t.Fatalf("process or read started: %v %d", s.args, calls)
	}
}

// runtimeRunner is a colima.Runner that reports one runtime in list and status,
// with a Docker socket behind it.
type runtimeRunner struct{ runtime, sock string }

func (r runtimeRunner) Run(_ context.Context, args []string) (colima.Result, error) {
	if args[0] == "list" {
		return colima.Result{Stdout: []byte(`{"name":"dev","status":"Running","runtime":"` + r.runtime + `"}`)}, nil
	}
	return colima.Result{Stdout: []byte(`{"runtime":"` + r.runtime + `","docker_socket":"unix://` + r.sock + `"}`)}, nil
}

// startDockerSocket serves one container, one image and no volume.
func startDockerSocket(t *testing.T) string {
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
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.41/containers/json", "/v1.41/images/json":
			w.Write([]byte(`[{"Id":"aaaaaaaaaaaa"}]`))
		case "/v1.41/volumes":
			w.Write([]byte(`{"Volumes":null}`))
		default:
			http.NotFound(w, r)
		}
	}))
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return sock
}
