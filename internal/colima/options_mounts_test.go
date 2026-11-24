package colima

import (
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/config"
)

func TestMountEnvFlagTable(t *testing.T) {
	cases := []struct {
		name string
		opts StartOptions
		want []string
	}{
		{"read-only mount", StartOptions{Mounts: []Mount{{Path: "/srv/data"}}}, []string{"--mount", "/srv/data"}},
		{"writable mount", StartOptions{Mounts: []Mount{{Path: "/srv/data", Writable: true}}}, []string{"--mount", "/srv/data:w"}},
		{"mounts repeat in order", StartOptions{Mounts: []Mount{{Path: "/b"}, {Path: "/a", Writable: true}}},
			[]string{"--mount", "/b", "--mount", "/a:w"}},
		{"root read-only", StartOptions{Mounts: []Mount{{Path: "/"}}}, []string{"--mount", "/"}},
		{"path with space", StartOptions{Mounts: []Mount{{Path: "/srv/my data"}}}, []string{"--mount", "/srv/my data"}},
		{"env", StartOptions{Env: []EnvVar{{"A", "1"}}}, []string{"--env", "A=1"}},
		{"env repeat in order", StartOptions{Env: []EnvVar{{"B", "x"}, {"A", "y"}}}, []string{"--env", "B=x", "--env", "A=y"}},
		{"env empty value", StartOptions{Env: []EnvVar{{"A", ""}}}, []string{"--env", "A="}},
		{"empty lists add nothing", StartOptions{Mounts: []Mount{}, Env: []EnvVar{}}, nil},
		{"nil lists add nothing", StartOptions{}, nil},
		{"after the other flags", StartOptions{CPUType: "max", Mounts: []Mount{{Path: "/a"}}, Env: []EnvVar{{"K", "v"}}},
			[]string{"--cpu-type", "max", "--mount", "/a", "--env", "K=v"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.opts.Validate(testLimits); err != nil {
				t.Fatal(err)
			}
			if got := c.opts.args(); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

// Every hostile value stays exactly one argv element, right after its flag.
func TestEnvValuesStayOneArgvElement(t *testing.T) {
	vals := []string{
		"--edit", "--", "a;b", "a\nb", "a\r\nb", "x=y=z", "$(id)", "`id`", "a|b&c", "'; rm -rf /", `a"b`, "a,b=c", "a b",
		"-x", "=", "==", "\t", "ünï", "KEY2=1",
	}
	for _, v := range vals {
		o := StartOptions{Env: []EnvVar{{"K", v}}}
		if err := o.Validate(testLimits); err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if got, want := o.args(), []string{"--env", "K=" + v}; !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q", v, got)
		}
	}
}

func TestMountPathsStayOneArgvElement(t *testing.T) {
	// Accepted despite looking odd: they are one argv element and Colima sees
	// no comma, quote, colon or control character in them.
	for _, p := range []string{"/--edit", "/a;b", "/$(id)", "/`id`", "/a b", "/a|b", "/-x", "/a=b", "/ünï", `/a\b`, "/*", "/~"} {
		o := StartOptions{Mounts: []Mount{{Path: p, Writable: true}}}
		if err := o.Validate(testLimits); err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		if got, want := o.args(), []string{"--mount", p + ":w"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q", p, got)
		}
	}
}

var hostileMountPaths = []string{
	"", "a", "./a", "../a", "~", "~/a", "$HOME", "none", "-x", "--mount", "/a/../b", "/a/./b", "/..", "/a/..", "/.", "//a", "/a//b", "/a/",
	"/a,b", "/a\",\"b", `/a"b`, "/a:w", "/a:b", "/a\nb", "/a\rb", "/a\x00b", "/a\tb", "/a\x7fb", "/a\x1b[0m", "\xff/a", "/\xc3\x28",
	"/a,/etc", strings.Repeat("/a", 2049), "relative/path", " /a",
}

func TestMountPathsRejectHostileValues(t *testing.T) {
	for _, p := range hostileMountPaths {
		err := StartOptions{Mounts: []Mount{{Path: p}}}.Validate(testLimits)
		var ioe *InvalidOptionError
		if !errors.As(err, &ioe) || ioe.Field != "mount" {
			t.Errorf("%q: got %v", p, err)
			continue
		}
		if len(p) > 3 && strings.Contains(err.Error(), p) {
			t.Errorf("%q echoed in %v", p, err)
		}
	}
}

func TestMountEnvInvalidValues(t *testing.T) {
	longPath := "/" + strings.Repeat("a", 4095) // 4096 bytes: allowed
	tooLong := longPath + "a"
	cases := []struct {
		name  string
		opts  StartOptions
		field string
		ok    bool
	}{
		{"path at 4096 bytes", StartOptions{Mounts: []Mount{{Path: longPath}}}, "", true},
		{"path at 4097 bytes", StartOptions{Mounts: []Mount{{Path: tooLong}}}, "mount", false},
		{"writable root", StartOptions{Mounts: []Mount{{Path: "/", Writable: true}}}, "mount", false},
		{"duplicate", StartOptions{Mounts: []Mount{{Path: "/a"}, {Path: "/a"}}}, "mount", false},
		{"duplicate differing in writable", StartOptions{Mounts: []Mount{{Path: "/a"}, {Path: "/a", Writable: true}}}, "mount", false},
		{"paths differing in case are distinct", StartOptions{Mounts: []Mount{{Path: "/a"}, {Path: "/A"}}}, "", true},
		{"sixteen mounts", StartOptions{Mounts: mounts(16)}, "", true},
		{"seventeen mounts", StartOptions{Mounts: mounts(17)}, "mount", false},
		{"thirty-two vars", StartOptions{Env: envs(32)}, "", true},
		{"thirty-three vars", StartOptions{Env: envs(33)}, "env", false},
		{"duplicate key", StartOptions{Env: []EnvVar{{"A", "1"}, {"A", "2"}}}, "env", false},
		{"keys differing in case are distinct", StartOptions{Env: []EnvVar{{"A", "1"}, {"a", "2"}}}, "", true},
		{"empty key", StartOptions{Env: []EnvVar{{"", "1"}}}, "env", false},
		{"digit first", StartOptions{Env: []EnvVar{{"1A", "1"}}}, "env", false},
		{"underscore first", StartOptions{Env: []EnvVar{{"_A1", "1"}}}, "", true},
		{"dash in key", StartOptions{Env: []EnvVar{{"A-B", "1"}}}, "env", false},
		{"equals in key", StartOptions{Env: []EnvVar{{"A=B", "1"}}}, "env", false},
		{"space in key", StartOptions{Env: []EnvVar{{"A B", "1"}}}, "env", false},
		{"flag as key", StartOptions{Env: []EnvVar{{"--edit", "1"}}}, "env", false},
		{"unicode key", StartOptions{Env: []EnvVar{{"Ä", "1"}}}, "env", false},
		{"key at 128", StartOptions{Env: []EnvVar{{strings.Repeat("A", 128), "1"}}}, "", true},
		{"key at 129", StartOptions{Env: []EnvVar{{strings.Repeat("A", 129), "1"}}}, "env", false},
		{"value at 4096", StartOptions{Env: []EnvVar{{"A", strings.Repeat("v", 4096)}}}, "", true},
		{"value at 4097", StartOptions{Env: []EnvVar{{"A", strings.Repeat("v", 4097)}}}, "env", false},
		{"NUL in value", StartOptions{Env: []EnvVar{{"A", "a\x00b"}}}, "env", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.opts.Validate(testLimits)
			if c.ok {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var ioe *InvalidOptionError
			if !errors.As(err, &ioe) || ioe.Field != c.field {
				t.Fatalf("got %v, want field %s", err, c.field)
			}
		})
	}
}

func mounts(n int) []Mount {
	m := make([]Mount, n)
	for i := range m {
		m[i] = Mount{Path: "/m" + strings.Repeat("x", i)}
	}
	return m
}

func envs(n int) []EnvVar {
	e := make([]EnvVar, n)
	for i := range e {
		e[i] = EnvVar{Key: "K" + strings.Repeat("X", i), Value: "v"}
	}
	return e
}

// The error text never carries a path, key or value.
func TestMountEnvErrorsNameNoValue(t *testing.T) {
	const marker = "synthetic-secret-marker"
	for _, o := range []StartOptions{
		{Mounts: []Mount{{Path: "/" + marker + ",x"}}},
		{Mounts: []Mount{{Path: "/" + marker}, {Path: "/" + marker}}},
		{Env: []EnvVar{{marker + "-", "v"}}},
		{Env: []EnvVar{{"A", marker + "\x00"}}},
		{Env: []EnvVar{{marker, "v"}, {marker, "w"}}},
	} {
		err := o.Validate(testLimits)
		if err == nil {
			t.Fatalf("%+v accepted", o)
		}
		if strings.Contains(err.Error(), marker) {
			t.Errorf("%v carries the value", err)
		}
	}
}

func TestStartRefusesHostileMountEnv(t *testing.T) {
	for _, o := range []StartOptions{
		{Mounts: []Mount{{Path: "/a,/etc"}}}, {Mounts: []Mount{{Path: "../a"}}}, {Mounts: []Mount{{Path: "/", Writable: true}}},
		{Env: []EnvVar{{"A=B", "c"}}}, {Env: []EnvVar{{"--x", "c"}}}, {Env: []EnvVar{{"A", "x\x00"}}},
	} {
		fs := &fakeStreamer{}
		if _, err := Start(t.Context(), fs, "dev", o, testLimits); err == nil {
			t.Errorf("%+v accepted", o)
		}
		if len(fs.calls) != 0 {
			t.Errorf("%+v launched %v", o, fs.calls)
		}
	}
}

func TestStartBuildsMountEnvArgv(t *testing.T) {
	fs := &fakeStreamer{proc: newFakeProcess(nil, Exit{}, nil)}
	o := StartOptions{Mounts: []Mount{{Path: "/srv/a", Writable: true}}, Env: []EnvVar{{"K", "a;b"}}}
	if _, err := Start(t.Context(), fs, "dev", o, testLimits); err != nil {
		t.Fatal(err)
	}
	want := []string{"start", "-p", "dev", "--mount", "/srv/a:w", "--env", "K=a;b"}
	if len(fs.calls) != 1 || !reflect.DeepEqual(fs.calls[0], want) {
		t.Fatalf("calls %q", fs.calls)
	}
}

// mountEnvAgree compares the adapter's verdict with the Config Service's
// syntax check (ValidateNew) for one mount and one variable. The host checks
// (ResolveMounts) have no adapter counterpart and are not compared.
func mountEnvAgree(t *testing.T, p, key, val string) {
	t.Helper()
	cfg := func(a config.NewAdvanced) bool {
		return len(config.ValidateNew(&config.Profile{}, a, config.Limits{})) == 0
	}
	for _, w := range []bool{false, true} {
		ours := StartOptions{Mounts: []Mount{{Path: p, Writable: w}}}.Validate(testLimits) == nil
		theirs := cfg(config.NewAdvanced{Mounts: []config.NewMount{{Path: p, Writable: w}}})
		if ours != theirs {
			t.Errorf("mount %q writable=%v: adapter %v, config %v", p, w, ours, theirs)
		}
	}
	ours := StartOptions{Env: []EnvVar{{key, val}}}.Validate(testLimits) == nil
	theirs := cfg(config.NewAdvanced{Env: []config.NewEnvVar{{Key: key, Value: val}}})
	if ours != theirs {
		t.Errorf("env %q=%q: adapter %v, config %v", key, val, ours, theirs)
	}
}

var parityPaths = append([]string{
	"/", "/a", "/Users", "/Users/u", "/a b", "/a/b/c", "/tmp/x_y-z.w", "/ünï", "/" + strings.Repeat("a", 4095),
	"/" + strings.Repeat("a", 4096), "/a/b/", "/a:w", "/a:", "/a\\b", "/a#b", "/a$b", "/a%00",
}, hostileMountPaths...)

var parityKeys = []string{
	"A", "a", "_", "_a", "A1", "A_B", "PATH", "1A", "", "A-B", "A=B", "A B", "A.B", "A\x00", "--x", "-x", "Ä", "A,B", `A"B`,
	strings.Repeat("A", 128), strings.Repeat("A", 129),
}

var parityValues = []string{
	"", "v", "a=b", "a,b", `a"b`, "a\nb", "a\x00b", "-x", "--", strings.Repeat("v", 4096), strings.Repeat("v", 4097), "ünï", "\xff",
}

func TestMountEnvValidatorsAgreeWithConfig(t *testing.T) {
	for _, p := range parityPaths {
		mountEnvAgree(t, p, "A", "v")
	}
	for _, k := range parityKeys {
		for _, v := range parityValues {
			mountEnvAgree(t, "/a", k, v)
		}
	}
	// Counts, duplicates and the writable root across several entries.
	for _, n := range []int{0, 1, 16, 17, 100} {
		var cm []config.NewMount
		var m []Mount
		for _, x := range mounts(n) {
			m = append(m, x)
			cm = append(cm, config.NewMount{Path: x.Path})
		}
		ours := StartOptions{Mounts: m}.Validate(testLimits) == nil
		theirs := len(config.ValidateNew(&config.Profile{}, config.NewAdvanced{Mounts: cm}, config.Limits{})) == 0
		if ours != theirs {
			t.Errorf("mount count %d: adapter %v, config %v", n, ours, theirs)
		}
	}
	for _, n := range []int{0, 1, 32, 33, 100} {
		var ce []config.NewEnvVar
		var e []EnvVar
		for _, x := range envs(n) {
			e = append(e, x)
			ce = append(ce, config.NewEnvVar{Key: x.Key, Value: x.Value})
		}
		ours := StartOptions{Env: e}.Validate(testLimits) == nil
		theirs := len(config.ValidateNew(&config.Profile{}, config.NewAdvanced{Env: ce}, config.Limits{})) == 0
		if ours != theirs {
			t.Errorf("env count %d: adapter %v, config %v", n, ours, theirs)
		}
	}
	dup := func(a, b string, wa, wb bool) {
		ours := StartOptions{Mounts: []Mount{{a, wa}, {b, wb}}}.Validate(testLimits) == nil
		theirs := len(config.ValidateNew(&config.Profile{}, config.NewAdvanced{
			Mounts: []config.NewMount{{Path: a, Writable: wa}, {Path: b, Writable: wb}}}, config.Limits{})) == 0
		if ours != theirs {
			t.Errorf("duplicate %q %q: adapter %v, config %v", a, b, ours, theirs)
		}
	}
	dup("/a", "/a", false, false)
	dup("/a", "/a", false, true)
	dup("/a", "/b", false, true)
	dup("/a", "/A", false, false)
	ours := StartOptions{Env: []EnvVar{{"A", "1"}, {"A", "2"}}}.Validate(testLimits) == nil
	theirs := len(config.ValidateNew(&config.Profile{}, config.NewAdvanced{Env: []config.NewEnvVar{{Key: "A", Value: "1"}, {Key: "A", Value: "2"}}}, config.Limits{})) == 0
	if ours != theirs {
		t.Errorf("duplicate key: adapter %v, config %v", ours, theirs)
	}
}

// FuzzMountEnvParity runs its seeds under `go test` and explores under
// `go test -fuzz FuzzMountEnvParity ./internal/colima`.
func FuzzMountEnvParity(f *testing.F) {
	for _, p := range parityPaths {
		f.Add(p, "A", "v")
	}
	for _, k := range parityKeys {
		f.Add("/a", k, "v")
	}
	for _, v := range parityValues {
		f.Add("/a", "A", v)
	}
	f.Fuzz(func(t *testing.T, p, k, v string) {
		mountEnvAgree(t, p, k, v)
		// Whatever is accepted renders as exactly one element per flag.
		o := StartOptions{Mounts: []Mount{{Path: p}}, Env: []EnvVar{{k, v}}}
		if o.Validate(testLimits) == nil {
			a := o.args()
			if len(a) != 4 || a[0] != "--mount" || a[2] != "--env" || a[1] != p || a[3] != k+"="+v {
				t.Fatalf("argv %q", a)
			}
		}
	})
}

// A deterministic random sweep over a small alphabet of the troublesome bytes,
// so the parity check explores combinations without the fuzzer.
func TestMountEnvParityRandomSweep(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alpha := []string{"/", "/", "a", "A", "_", "1", ".", "..", ":", ",", `"`, "=", "\n", "\x00", " ", "-", "ü", "w", "\x7f"}
	gen := func() string {
		var b strings.Builder
		for i, n := 0, rng.Intn(8); i < n; i++ {
			b.WriteString(alpha[rng.Intn(len(alpha))])
		}
		return b.String()
	}
	for i := 0; i < 20000; i++ {
		mountEnvAgree(t, gen(), gen(), gen())
	}
}
