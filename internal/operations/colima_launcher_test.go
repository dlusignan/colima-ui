package operations

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/dlusignan/colima-ui/internal/colima"
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
	if _, err := l.Launch(context.Background(), Request{Action: "delete", Profile: "dev"}); !errors.Is(err, ErrUnknownAction) {
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

// statusRunner is a colima.Runner fake that answers `colima status --json`.
type statusRunner struct {
	out   string
	err   error
	calls [][]string
}

func (r *statusRunner) Run(_ context.Context, args []string) (colima.Result, error) {
	r.calls = append(r.calls, args)
	return colima.Result{Stdout: []byte(r.out)}, r.err
}

const statusK8s = `{"runtime":"docker","kubernetes":true}`
const statusNoK8s = `{"runtime":"docker","kubernetes":false}`

func TestColimaLauncherKubernetesArgv(t *testing.T) {
	s := &argsStreamer{}
	l := ColimaLauncher{S: s, R: &statusRunner{out: statusK8s}}
	for _, a := range []Action{ActionKubernetesStart, ActionKubernetesStop, ActionKubernetesReset, ActionKubernetesDelete} {
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

func TestColimaLauncherKubernetesRechecksInsideTheSlot(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    *statusRunner
		want error
	}{
		{"stopped", &statusRunner{err: &colima.ExitError{ExitCode: 1}}, ErrProfileNotRunning},
		{"disabled", &statusRunner{out: statusNoK8s}, ErrKubernetesNotEnabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &argsStreamer{}
			l := ColimaLauncher{S: s, R: tc.r}
			if _, err := l.Launch(context.Background(), Request{Action: ActionKubernetesReset, Profile: "dev"}); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if len(s.args) != 0 {
				t.Fatalf("streamer called: %v", s.args)
			}
		})
	}
	// An unexpected failure is not read as "not running".
	l := ColimaLauncher{S: &argsStreamer{}, R: &statusRunner{err: errors.New("boom")}}
	_, err := l.Launch(context.Background(), Request{Action: ActionKubernetesStop, Profile: "dev"})
	if err == nil || errors.Is(err, ErrProfileNotRunning) {
		t.Fatalf("err = %v", err)
	}
}

func TestColimaLauncherKubernetesRejectsBadProfileBeforeAnyProcess(t *testing.T) {
	s, r := &argsStreamer{}, &statusRunner{out: statusK8s}
	l := ColimaLauncher{S: s, R: r}
	for _, a := range []Action{ActionKubernetesStart, ActionKubernetesStop, ActionKubernetesReset, ActionKubernetesDelete} {
		if _, err := l.Launch(context.Background(), Request{Action: a, Profile: "--all"}); err == nil {
			t.Fatalf("%s: want invalid profile error", a)
		}
	}
	if len(s.args) != 0 || len(r.calls) != 0 {
		t.Fatalf("process started: %v %v", s.args, r.calls)
	}
}
