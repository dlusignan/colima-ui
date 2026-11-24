package config

import (
	"errors"
	"fmt"
	"path"
	"reflect"
	"strings"
	"testing"
)

// fakeFS is an in-memory host: dirs are directories, files are plain files,
// links map a path to its target. Nothing touches the real disk or home.
type fakeFS struct {
	home    string
	homeErr error
	dirs    map[string]bool
	files   map[string]bool
	links   map[string]string
	probed  []string
}

func newFakeFS(dirs ...string) *fakeFS {
	f := &fakeFS{home: "/home/user", dirs: map[string]bool{"/": true}, files: map[string]bool{}, links: map[string]string{}}
	for _, d := range dirs {
		for p := d; p != "/"; p = path.Dir(p) {
			f.dirs[p] = true
		}
	}
	return f
}

func (f *fakeFS) Home() (string, error) { return f.home, f.homeErr }

func (f *fakeFS) EvalSymlinks(p string) (string, error) {
	f.probed = append(f.probed, p)
	for i := 0; i < 20; i++ {
		t, ok := f.links[p]
		if !ok {
			break
		}
		p = t
	}
	if !f.dirs[p] && !f.files[p] {
		return "", errors.New("synthetic: no such file " + p)
	}
	return p, nil
}

func (f *fakeFS) IsDir(p string) (bool, error) {
	if f.dirs[p] {
		return true, nil
	}
	if f.files[p] {
		return false, nil
	}
	return false, errors.New("synthetic: no such file " + p)
}

func resolve(t *testing.T, fs *fakeFS, ms ...NewMount) ([]NewMount, map[string]string) {
	t.Helper()
	out, errs := ResolveMounts(ms, fs)
	m := map[string]string{}
	for _, e := range errs {
		m[e.Field] = e.Message
	}
	return out, m
}

func TestResolveMountsAcceptsExistingDirectories(t *testing.T) {
	fs := newFakeFS("/home/user/src", "/srv/data", "/home/user/cache")
	out, errs := resolve(t, fs, NewMount{"/home/user/src", true}, NewMount{"/srv/data", false})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if want := []NewMount{{"/home/user/src", true}, {"/srv/data", false}}; !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v", out)
	}
	if out, errs := resolve(t, fs); out != nil || len(errs) != 0 {
		t.Fatalf("empty list: %v %v", out, errs)
	}
}

func TestResolveMountsRejections(t *testing.T) {
	fs := newFakeFS("/home/user/src", "/Users/other", "/srv/data")
	fs.files["/srv/file"] = true
	fs.links["/srv/to-root"] = "/"
	fs.links["/srv/to-home"] = "/home/user"
	fs.links["/srv/to-home-parent"] = "/home"
	fs.links["/srv/src-link"] = "/home/user/src"
	fs.links["/srv/comma"] = "/srv/data,x" // a link whose target the adapter would refuse
	fs.dirs["/srv/data,x"] = true
	fs.links["/srv/dangling"] = "/srv/gone"
	cases := []struct {
		name string
		ms   []NewMount
		want map[string]string // field -> substring
	}{
		{"missing", []NewMount{{"/nope", false}}, map[string]string{"mounts[0].path": "existing directory"}},
		{"file", []NewMount{{"/srv/file", false}}, map[string]string{"mounts[0].path": "existing directory"}},
		{"dangling link", []NewMount{{"/srv/dangling", false}}, map[string]string{"mounts[0].path": "existing directory"}},
		{"link target the adapter refuses", []NewMount{{"/srv/comma", false}}, map[string]string{"mounts[0].path": "existing directory"}},
		{"writable root", []NewMount{{"/", true}}, map[string]string{"mounts[0].path": "too broad"}},
		{"writable home", []NewMount{{"/home/user", true}}, map[string]string{"mounts[0].path": "too broad"}},
		{"writable above home", []NewMount{{"/home", true}}, map[string]string{"mounts[0].path": "too broad"}},
		{"writable link to root", []NewMount{{"/srv/to-root", true}}, map[string]string{"mounts[0].path": "too broad"}},
		{"writable link to home", []NewMount{{"/srv/to-home", true}}, map[string]string{"mounts[0].path": "too broad"}},
		{"writable link above home", []NewMount{{"/srv/to-home-parent", true}}, map[string]string{"mounts[0].path": "too broad"}},
		{"two names for one folder", []NewMount{{"/home/user/src", false}, {"/srv/src-link", false}}, map[string]string{"mounts[1].path": "repeats"}},
		{"second of three is bad", []NewMount{{"/srv/data", false}, {"/nope", false}, {"/srv/data", false}},
			map[string]string{"mounts[1].path": "existing directory", "mounts[2].path": "repeats"}},
		{"syntactically bad is not probed", []NewMount{{"../x", false}}, map[string]string{"mounts[0].path": "acceptable"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, errs := resolve(t, fs, c.ms...)
			if out != nil {
				t.Fatalf("returned mounts with errors: %v", out)
			}
			if len(errs) != len(c.want) {
				t.Fatalf("got %v want %v", errs, c.want)
			}
			for f, sub := range c.want {
				if !strings.Contains(errs[f], sub) {
					t.Errorf("%s: %q lacks %q", f, errs[f], sub)
				}
			}
		})
	}
}

func TestResolveMountsAllowsBroadMountsReadOnly(t *testing.T) {
	fs := newFakeFS("/home/user")
	fs.links["/srv/to-home"] = "/home/user"
	out, errs := resolve(t, fs, NewMount{"/", false}, NewMount{"/home", false}, NewMount{"/home/user", false})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if len(out) != 3 || out[0].Path != "/" || out[2].Path != "/home/user" {
		t.Fatalf("got %v", out)
	}
	if _, errs := resolve(t, fs, NewMount{"/srv/to-home", false}); len(errs) != 0 {
		t.Fatal(errs)
	}
}

// The default macOS filesystem ignores case, and symlink resolution keeps the
// case as written, so a case variant of the home folder must not get through.
func TestResolveMountsHomeCheckIgnoresCase(t *testing.T) {
	fs := newFakeFS("/Users/Me/src")
	fs.home = "/Users/Me"
	fs.dirs["/users/me"] = true // what a case-insensitive disk would answer for
	fs.dirs["/USERS"] = true
	for _, p := range []string{"/users/me", "/USERS/ME", "/Users/me", "/USERS"} {
		fs.dirs[p] = true
		if _, errs := resolve(t, fs, NewMount{p, true}); errs["mounts[0].path"] == "" {
			t.Errorf("%q accepted writable", p)
		}
	}
	if _, errs := resolve(t, fs, NewMount{"/Users/Me/src", true}); len(errs) != 0 {
		t.Fatal(errs)
	}
}

func TestResolveMountsWritableBelowHomeIsFine(t *testing.T) {
	fs := newFakeFS("/home/user/a", "/home/username", "/home/user2")
	// Sibling names that share a prefix with the home folder are not ancestors.
	if _, errs := resolve(t, fs, NewMount{"/home/user/a", true}, NewMount{"/home/username", true}, NewMount{"/home/user2", true}); len(errs) != 0 {
		t.Fatal(errs)
	}
}

// The mount is handed to Colima as its resolved path.
func TestResolveMountsReturnsTheResolvedPath(t *testing.T) {
	fs := newFakeFS("/real/dir")
	fs.links["/srv/link"] = "/real/dir"
	out, errs := resolve(t, fs, NewMount{"/srv/link", true})
	if len(errs) != 0 || out[0] != (NewMount{"/real/dir", true}) {
		t.Fatalf("%v %v", out, errs)
	}
}

func TestResolveMountsFailsClosedWithoutAHome(t *testing.T) {
	fs := newFakeFS("/srv/data")
	fs.homeErr = errors.New("synthetic: no home")
	if _, errs := resolve(t, fs, NewMount{"/srv/data", true}); errs["mounts[0].path"] == "" {
		t.Fatal("writable mount accepted without a known home")
	}
	if _, errs := resolve(t, fs, NewMount{"/srv/data", false}); len(errs) != 0 {
		t.Fatal(errs)
	}
	fs.homeErr, fs.home = nil, "relative/home"
	if _, errs := resolve(t, fs, NewMount{"/srv/data", true}); errs["mounts[0].path"] == "" {
		t.Fatal("writable mount accepted with a relative home")
	}
}

// Messages never carry a path or the filesystem's own error text.
func TestResolveMountsMessagesNameNoPath(t *testing.T) {
	const marker = "synthetic-marker-dir"
	fs := newFakeFS()
	fs.files["/"+marker+"-file"] = true
	for _, p := range []string{"/" + marker, "/" + marker + "-file", "/" + marker + ",x", "/../" + marker} {
		_, errs := ResolveMounts([]NewMount{{p, false}, {"/", true}}, fs)
		if len(errs) == 0 {
			t.Fatalf("%q accepted", p)
		}
		for _, e := range errs {
			if strings.Contains(e.Message, marker) || strings.Contains(e.Message, "synthetic") || strings.Contains(e.Field, marker) {
				t.Errorf("%q leaks into %+v", p, e)
			}
		}
	}
}

func TestResolveMountsTouchesOnlyWellFormedPaths(t *testing.T) {
	fs := newFakeFS("/srv/data")
	ResolveMounts([]NewMount{{"/srv/../etc", false}, {"relative", false}, {"/a\x00b", false}, {"/srv/data", false}}, fs)
	for _, p := range fs.probed {
		if strings.Contains(p, "..") || p == "relative" || strings.ContainsRune(p, 0) {
			t.Errorf("probed %q", p)
		}
	}
}

func syn(f *testing.T, a NewAdvanced) map[string]string { f.Helper(); return advFields(&Profile{}, a) }

func TestValidateNewMountSyntax(t *testing.T) {
	cases := []struct {
		name string
		ms   []NewMount
		want map[string]string
	}{
		{"relative", []NewMount{{"a/b", false}}, map[string]string{"mounts[0].path": "absolute"}},
		{"dotdot", []NewMount{{"/a/../b", false}}, map[string]string{"mounts[0].path": ".."}},
		{"unclean", []NewMount{{"/a//b", false}}, map[string]string{"mounts[0].path": "clean"}},
		{"trailing slash", []NewMount{{"/a/", false}}, map[string]string{"mounts[0].path": "clean"}},
		{"dot segment", []NewMount{{"/a/./b", false}}, map[string]string{"mounts[0].path": "clean"}},
		{"empty", []NewMount{{"", false}}, map[string]string{"mounts[0].path": "required"}},
		{"comma", []NewMount{{"/a,b", false}}, map[string]string{"mounts[0].path": "comma"}},
		{"quote", []NewMount{{`/a"b`, false}}, map[string]string{"mounts[0].path": "comma"}},
		{"colon", []NewMount{{"/a:w", false}}, map[string]string{"mounts[0].path": "colon"}},
		{"newline", []NewMount{{"/a\nb", false}}, map[string]string{"mounts[0].path": "control"}},
		{"NUL", []NewMount{{"/a\x00b", false}}, map[string]string{"mounts[0].path": "control"}},
		{"long", []NewMount{{"/" + strings.Repeat("a", 4096), false}}, map[string]string{"mounts[0].path": "4096"}},
		{"writable root", []NewMount{{"/", true}}, map[string]string{"mounts[0].path": "root"}},
		{"duplicate", []NewMount{{"/a", false}, {"/b", false}, {"/a", true}}, map[string]string{"mounts[2].path": "repeats"}},
		{"index of second", []NewMount{{"/a", false}, {"x", false}}, map[string]string{"mounts[1].path": "absolute"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := syn(t, NewAdvanced{Mounts: c.ms})
			if len(got) != len(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for f, sub := range c.want {
				if !strings.Contains(got[f], sub) {
					t.Errorf("%s: %q lacks %q", f, got[f], sub)
				}
			}
		})
	}
	if got := syn(t, NewAdvanced{Mounts: []NewMount{{"/", false}, {"/a b", true}}}); len(got) != 0 {
		t.Fatalf("valid mounts rejected: %v", got)
	}
}

func TestValidateNewMountCountIsCheckedBeforeElements(t *testing.T) {
	ms := make([]NewMount, 17)
	for i := range ms {
		ms[i] = NewMount{Path: "bad\x00"} // every element is invalid
	}
	got := syn(t, NewAdvanced{Mounts: ms})
	if len(got) != 1 || !strings.Contains(got["mounts"], "16") {
		t.Fatalf("got %v", got)
	}
	ms = ms[:16]
	if got := syn(t, NewAdvanced{Mounts: ms}); len(got) != 16 {
		t.Fatalf("16 bad elements: %v", got)
	}
}

func TestValidateNewEnv(t *testing.T) {
	long := strings.Repeat("v", 4097)
	cases := []struct {
		name string
		env  []NewEnvVar
		want map[string]string
	}{
		{"fine", []NewEnvVar{{"A", "1"}, {"_b2", "x=y;z"}}, map[string]string{}},
		{"empty value", []NewEnvVar{{"A", ""}}, map[string]string{}},
		{"bad key by index", []NewEnvVar{{"A", "1"}, {"B-C", "1"}}, map[string]string{"env[1]": "name"}},
		{"digit key", []NewEnvVar{{"1A", "1"}}, map[string]string{"env[0]": "name"}},
		{"empty key", []NewEnvVar{{"", "1"}}, map[string]string{"env[0]": "name"}},
		{"duplicate", []NewEnvVar{{"A", "1"}, {"A", "2"}}, map[string]string{"env[1]": "repeats"}},
		{"long value named by key", []NewEnvVar{{"TOKEN", long}}, map[string]string{"env.TOKEN": "4096"}},
		{"NUL value named by key", []NewEnvVar{{"TOKEN", "a\x00"}}, map[string]string{"env.TOKEN": "NUL"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := syn(t, NewAdvanced{Env: c.env})
			if len(got) != len(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for f, sub := range c.want {
				if !strings.Contains(got[f], sub) {
					t.Errorf("%s: %q lacks %q", f, got[f], sub)
				}
			}
		})
	}
	env := make([]NewEnvVar, 33)
	for i := range env {
		env[i] = NewEnvVar{Key: "bad-" + fmt.Sprint(i), Value: "x\x00"}
	}
	if got := syn(t, NewAdvanced{Env: env}); len(got) != 1 || got["env"] == "" {
		t.Fatalf("count cap: %v", got)
	}
}

// No message, field name or error carries a hostile key's text or a value.
func TestValidateNewMountEnvMessagesNameNoRequestText(t *testing.T) {
	const marker = "synthetic-secret-marker"
	for _, a := range []NewAdvanced{
		{Mounts: []NewMount{{"/" + marker + "\x00", false}}},
		{Mounts: []NewMount{{marker, false}}},
		{Mounts: []NewMount{{"/" + marker, false}, {"/" + marker, false}}},
		{Env: []NewEnvVar{{marker + "-", marker}}},
		{Env: []NewEnvVar{{"K", marker + "\x00"}}},
		{Env: []NewEnvVar{{"K", strings.Repeat(marker, 400)}}},
		{Env: []NewEnvVar{{marker, "a"}, {marker, marker}}},
	} {
		errs := ValidateNew(&Profile{}, a, testLimits)
		if len(errs) == 0 {
			t.Fatalf("%+v accepted", a)
		}
		for _, e := range errs {
			if strings.Contains(e.Message, marker) {
				t.Errorf("message carries request text: %+v", e)
			}
			// Only a key that passed the name pattern may appear in a field.
			if strings.Contains(e.Field, marker) && !strings.HasPrefix(e.Field, "env.") {
				t.Errorf("field carries request text: %+v", e)
			}
		}
	}
}
