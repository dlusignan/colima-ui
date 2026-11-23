package colima

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ok(b []byte) Result { return Result{Stdout: b} }

func TestListParsesJSONLines(t *testing.T) {
	f := &fakeRunner{result: ok(fixture(t, "list_multi.json"))}
	got, err := List(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls, [][]string{{"list", "--json"}}) {
		t.Errorf("args = %v", f.calls)
	}
	want := []Instance{
		{Name: "default", Status: StatusRunning, Arch: "aarch64", CPUs: 4, Memory: 8589934592, Disk: 107374182400, Runtime: "docker"},
		{Name: "synthetic-a", Status: StatusStopped, Arch: "aarch64", CPUs: 2, Memory: 4294967296, Disk: 64424509440, Runtime: "containerd"},
		{Name: "synthetic-b", Status: StatusRunning, Arch: "x86_64", CPUs: 6, Memory: 17179869184, Disk: 214748364800, Runtime: "docker", Address: "192.0.2.10"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestListAcceptsArrayAndEmpty(t *testing.T) {
	f := &fakeRunner{result: ok(fixture(t, "list_array.json"))}
	got, err := List(context.Background(), f)
	if err != nil || len(got) != 1 || got[0].Name != "default" {
		t.Fatalf("array: %v %+v", err, got)
	}
	f = &fakeRunner{result: ok(fixture(t, "list_empty.json"))}
	got, err = List(context.Background(), f)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty: %v %#v", err, got)
	}
}

func TestListToleratesUnknownStatusAndFields(t *testing.T) {
	f := &fakeRunner{result: ok([]byte(`{"name":"synthetic-a","status":"Broken","future":{"x":1}}`))}
	got, err := List(context.Background(), f)
	if err != nil || got[0].Status != "Broken" {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestListParseErrors(t *testing.T) {
	for name, in := range map[string]string{
		"garbage":        "not json",
		"truncated":      `{"name":"a","status":"Running"`,
		"missing name":   `{"status":"Running"}`,
		"missing status": `{"name":"a"}`,
		"wrong type":     `{"name":"a","status":"Running","cpus":"four"}`,
		"trailing junk":  `{"name":"a","status":"Running"} xyz`,
		"array of junk":  `[1,2]`,
	} {
		_, err := List(context.Background(), &fakeRunner{result: ok([]byte(in))})
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Errorf("%s: got %v, want ParseError", name, err)
			continue
		}
		if pe.Raw != in || pe.Op != "list" {
			t.Errorf("%s: raw/op not kept: %+v", name, pe)
		}
	}
}

func TestNonZeroExitIsExitError(t *testing.T) {
	r := &fakeRunner{result: Result{ExitCode: 1, Stderr: []byte(`level=fatal msg="boom"`)}}
	_, err := List(context.Background(), r)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.ExitCode != 1 || ee.Stderr != `level=fatal msg="boom"` {
		t.Fatalf("got %v", err)
	}
	var pe *ParseError
	var bnf *BinaryNotFoundError
	if errors.As(err, &pe) || errors.As(err, &bnf) {
		t.Error("error types must be distinct")
	}
}

func TestRunnerErrorsPassThrough(t *testing.T) {
	bnf := &BinaryNotFoundError{Err: errors.New("x")}
	for _, fn := range []func(Runner) error{
		func(r Runner) error { _, err := List(context.Background(), r); return err },
		func(r Runner) error { _, err := Status(context.Background(), r, "default"); return err },
		func(r Runner) error { _, err := Version(context.Background(), r); return err },
	} {
		err := fn(&fakeRunner{err: bnf})
		var got *BinaryNotFoundError
		if !errors.As(err, &got) {
			t.Errorf("got %v, want BinaryNotFoundError", err)
		}
	}
}

func TestFunctionsHonourCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeRunner{result: ok(fixture(t, "list_multi.json"))}
	if _, err := List(ctx, f); !errors.Is(err, context.Canceled) {
		t.Errorf("List: %v", err)
	}
	if _, err := Status(ctx, f, "default"); !errors.Is(err, context.Canceled) {
		t.Errorf("Status: %v", err)
	}
	if _, err := Version(ctx, f); !errors.Is(err, context.Canceled) {
		t.Errorf("Version: %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("runner called with cancelled context: %v", f.calls)
	}
}

func TestStatus(t *testing.T) {
	f := &fakeRunner{result: ok(fixture(t, "status_running.json"))}
	got, err := Status(context.Background(), f, "synthetic-a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls, [][]string{{"status", "--json", "-p", "synthetic-a"}}) {
		t.Errorf("args = %v", f.calls)
	}
	want := Details{
		DisplayName: "colima", Driver: "macOS Virtualization.Framework", Arch: "aarch64", Runtime: "docker",
		MountType: "virtiofs", DockerSocket: "unix:///home/user/.colima/default/docker.sock",
		ContainerdSocket: "unix:///home/user/.colima/default/containerd.sock",
		CPUs:             4, Memory: 8589934592, Disk: 107374182400,
	}
	if got != want {
		t.Errorf("got %+v", got)
	}
}

func TestStatusValidatesProfileBeforeRunning(t *testing.T) {
	f := &fakeRunner{}
	for _, bad := range []string{"", "-x", "a b", "--json"} {
		_, err := Status(context.Background(), f, bad)
		var ip *InvalidProfileError
		if !errors.As(err, &ip) {
			t.Errorf("%q: got %v", bad, err)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("runner called for invalid profile: %v", f.calls)
	}
}

func TestStatusParseErrorsAndExit(t *testing.T) {
	for name, in := range map[string]string{"empty": "", "garbage": "x", "array": "[]", "no runtime": `{"driver":"d"}`} {
		_, err := Status(context.Background(), &fakeRunner{result: ok([]byte(in))}, "default")
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Op != "status" {
			t.Errorf("%s: got %v", name, err)
		}
	}
	_, err := Status(context.Background(), &fakeRunner{result: Result{ExitCode: 1}}, "default")
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Errorf("got %v", err)
	}
}

func TestVersion(t *testing.T) {
	cases := []struct {
		file string
		want VersionInfo
	}{
		{"version.txt", VersionInfo{Number: "0.10.3", Major: 0, Minor: 10, Patch: 3, Semver: true}},
		{"version_prerelease.txt", VersionInfo{Number: "v0.11.0-rc.1+dev", Major: 0, Minor: 11, Patch: 0, Prerelease: "rc.1", Semver: true}},
		{"version_devbuild.txt", VersionInfo{Number: "HEAD-abcdef0"}},
	}
	for _, c := range cases {
		raw := fixture(t, c.file)
		f := &fakeRunner{result: ok(raw)}
		got, err := Version(context.Background(), f)
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		if !reflect.DeepEqual(f.calls, [][]string{{"version"}}) {
			t.Errorf("args = %v", f.calls)
		}
		c.want.Raw = string(raw)
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.file, got, c.want)
		}
	}
}

func TestVersionParseError(t *testing.T) {
	for _, in := range []string{"", "hello", "version 1.2.3", "\ncolima version 1.0.0"} {
		_, err := Version(context.Background(), &fakeRunner{result: ok([]byte(in))})
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Raw != in {
			t.Errorf("%q: got %v", in, err)
		}
	}
}
