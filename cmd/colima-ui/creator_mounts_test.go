package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/httpapi"
)

// fakeHost is an in-memory filesystem with a synthetic home, so no test reads
// the real disk or the real home folder.
type fakeHost struct {
	home  string
	dirs  map[string]bool
	files map[string]bool
	links map[string]string
	calls int
}

func defaultFakeHost() *fakeHost {
	h := &fakeHost{home: "/home/user", dirs: map[string]bool{"/": true}, files: map[string]bool{"/srv/file": true}, links: map[string]string{}}
	for _, d := range []string{"/home/user/src", "/home/user/cache", "/srv/data", "/srv/other", "/real/dir"} {
		for p := d; p != "/"; p = path.Dir(p) {
			h.dirs[p] = true
		}
	}
	h.links["/srv/link"] = "/real/dir"
	h.links["/srv/to-home"] = "/home/user"
	return h
}

func (h *fakeHost) Home() (string, error) { return h.home, nil }
func (h *fakeHost) EvalSymlinks(p string) (string, error) {
	h.calls++
	if t, ok := h.links[p]; ok {
		p = t
	}
	if !h.dirs[p] && !h.files[p] {
		return "", errors.New("synthetic: missing " + p)
	}
	return p, nil
}
func (h *fakeHost) IsDir(p string) (bool, error) { return h.dirs[p], nil }
func (h *fakeHost) SameFile(a, b string) (bool, error) {
	if (!h.dirs[a] && !h.files[a]) || (!h.dirs[b] && !h.files[b]) {
		return false, errors.New("synthetic: missing")
	}
	return a == b, nil
}

func creatorWithHost(m *fakeMgr, h *fakeHost) profileCreator {
	c, _ := newCreator(m, false, nil)
	c.host = h
	return c
}

func TestCreatorSubmitsMountsAndSortedEnv(t *testing.T) {
	m := &fakeMgr{}
	c := creatorWithHost(m, defaultFakeHost())
	_, err := c.Create(t.Context(), httpapi.NewProfile{
		Name:   "dev",
		Mounts: []httpapi.Mount{{Path: "/home/user/src", Writable: true}, {Path: "/srv/link"}, {Path: "/", Writable: false}},
		Env:    httpapi.EnvVars{{Key: "b", Value: "2"}, {Key: "A", Value: "x;y"}, {Key: "B", Value: ""}},
	})
	if err != nil {
		t.Fatal(err)
	}
	o := m.reqs[0].Options
	// The symlink is submitted as the folder it resolves to.
	if want := []colima.Mount{{Path: "/home/user/src", Writable: true}, {Path: "/real/dir"}, {Path: "/"}}; !reflect.DeepEqual(o.Mounts, want) {
		t.Fatalf("mounts %+v", o.Mounts)
	}
	if want := []colima.EnvVar{{Key: "A", Value: "x;y"}, {Key: "B", Value: ""}, {Key: "b", Value: "2"}}; !reflect.DeepEqual(o.Env, want) {
		t.Fatalf("env %+v", o.Env)
	}
	if err := o.Validate(colimaLimits(testLimits)); err != nil {
		t.Fatalf("backstop: %v", err)
	}
	want := "--mount /home/user/src:w --mount /real/dir --mount / --env A=x;y --env B= --env b=2"
	if got := strings.Join(argsOf(o), " "); got != want {
		t.Fatalf("argv %s", got)
	}
}

// recordingStreamer records the argv it is asked to run and ends at once.
type recordingStreamer struct{ args []string }

func (r *recordingStreamer) Stream(_ context.Context, args []string) (colima.Process, error) {
	r.args = args
	return doneProc{}, nil
}

type doneProc struct{}

func (doneProc) Lines() <-chan string       { c := make(chan string); close(c); return c }
func (doneProc) Wait() (colima.Exit, error) { return colima.Exit{}, nil }
func (doneProc) Cancel()                    {}

// argsOf renders the flags through the adapter's real entry point.
func argsOf(o colima.StartOptions) []string {
	fs := &recordingStreamer{}
	if _, err := colima.Start(context.Background(), fs, "dev", o, colimaLimits(testLimits)); err != nil {
		panic(err)
	}
	return fs.args[3:]
}

func TestCreatorOmitsMountsAndEnvWhenEmpty(t *testing.T) {
	m := &fakeMgr{}
	c := creatorWithHost(m, defaultFakeHost())
	if _, err := c.Create(t.Context(), httpapi.NewProfile{Name: "dev", Mounts: []httpapi.Mount{}, Env: httpapi.EnvVars{}}); err != nil {
		t.Fatal(err)
	}
	if o := m.reqs[0].Options; len(o.Mounts) != 0 || len(o.Env) != 0 || len(argsOf(o)) != 0 {
		t.Fatalf("%+v", o)
	}
}

func TestCreatorMountRejectionsNameTheFieldAndLaunchNothing(t *testing.T) {
	cases := []struct {
		name  string
		ms    []httpapi.Mount
		field string
	}{
		{"relative", []httpapi.Mount{{Path: "src"}}, "mounts[0].path"},
		{"dotdot", []httpapi.Mount{{Path: "/srv/data/../other"}}, "mounts[0].path"},
		{"missing", []httpapi.Mount{{Path: "/srv/data"}, {Path: "/srv/missing"}}, "mounts[1].path"},
		{"file", []httpapi.Mount{{Path: "/srv/file"}}, "mounts[0].path"},
		{"duplicate", []httpapi.Mount{{Path: "/srv/data"}, {Path: "/srv/data"}}, "mounts[1].path"},
		{"duplicate by link", []httpapi.Mount{{Path: "/real/dir"}, {Path: "/srv/link"}}, "mounts[1].path"},
		{"writable root", []httpapi.Mount{{Path: "/", Writable: true}}, "mounts[0].path"},
		{"writable home", []httpapi.Mount{{Path: "/home/user", Writable: true}}, "mounts[0].path"},
		{"writable link to home", []httpapi.Mount{{Path: "/srv/to-home", Writable: true}}, "mounts[0].path"},
		{"comma", []httpapi.Mount{{Path: "/srv/data,/etc"}}, "mounts[0].path"},
		{"too many", func() []httpapi.Mount {
			ms := make([]httpapi.Mount, 17)
			for i := range ms {
				ms[i] = httpapi.Mount{Path: "/srv/data"}
			}
			return ms
		}(), "mounts"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &fakeMgr{}
			_, err := creatorWithHost(m, defaultFakeHost()).Create(t.Context(), httpapi.NewProfile{Name: "dev", Mounts: c.ms})
			var verr *httpapi.ValidationError
			if !errors.As(err, &verr) || verr.Fields[c.field] == "" {
				t.Fatalf("%v", err)
			}
			if len(m.reqs) != 0 {
				t.Fatal("launched")
			}
		})
	}
}

func TestCreatorMountRejectionsAreIndependentOfOtherFields(t *testing.T) {
	_, err := creatorWithHost(&fakeMgr{}, defaultFakeHost()).Create(t.Context(), httpapi.NewProfile{
		Name: "dev", Hostname: "-bad", Mounts: []httpapi.Mount{{Path: "/nope"}},
		Env: httpapi.EnvVars{{Key: "1x", Value: "v"}, {Key: "OK", Value: "a\x00"}},
	})
	var verr *httpapi.ValidationError
	if !errors.As(err, &verr) {
		t.Fatal(err)
	}
	// env[0] is the key "1x" in sorted order ("1x" sorts before "OK").
	for _, k := range []string{"hostname", "mounts[0].path", "env[0]", "env.OK"} {
		if verr.Fields[k] == "" {
			t.Errorf("missing %s in %v", k, verr.Fields)
		}
	}
}

// A hostile or oversized list never reaches the filesystem.
func TestCreatorDoesNotTouchTheFilesystemForMalformedMounts(t *testing.T) {
	h := defaultFakeHost()
	ms := make([]httpapi.Mount, 17)
	for i := range ms {
		ms[i] = httpapi.Mount{Path: "/srv/data"}
	}
	creatorWithHost(&fakeMgr{}, h).Create(t.Context(), httpapi.NewProfile{Name: "dev", Mounts: ms})
	creatorWithHost(&fakeMgr{}, h).Create(t.Context(), httpapi.NewProfile{Name: "dev", Mounts: []httpapi.Mount{{Path: "/srv/data"}, {Path: "../x"}}})
	if h.calls != 0 {
		t.Fatalf("filesystem touched %d times", h.calls)
	}
}

func TestCreatorEnvRejectionsNameTheField(t *testing.T) {
	cases := []struct {
		name  string
		env   httpapi.EnvVars
		field string
	}{
		{"bad key", httpapi.EnvVars{{Key: "A", Value: "1"}, {Key: "B-C", Value: "1"}}, "env[1]"}, // "B-C" sorts after "A"
		{"digit key", httpapi.EnvVars{{Key: "1A", Value: "1"}}, "env[0]"},
		{"flag key", httpapi.EnvVars{{Key: "--edit", Value: "1"}}, "env[0]"},
		{"nul value", httpapi.EnvVars{{Key: "K", Value: "a\x00"}}, "env.K"},
		{"long value", httpapi.EnvVars{{Key: "K", Value: strings.Repeat("v", 4097)}}, "env.K"},
		{"duplicate", httpapi.EnvVars{{Key: "K", Value: "1"}, {Key: "K", Value: "2"}}, "env[1]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &fakeMgr{}
			_, err := creatorWithHost(m, defaultFakeHost()).Create(t.Context(), httpapi.NewProfile{Name: "dev", Env: c.env})
			var verr *httpapi.ValidationError
			if !errors.As(err, &verr) || verr.Fields[c.field] == "" || len(verr.Fields) != 1 {
				t.Fatalf("%v", err)
			}
			if len(m.reqs) != 0 {
				t.Fatal("launched")
			}
		})
	}
	env := make(httpapi.EnvVars, 33)
	for i := range env {
		env[i] = httpapi.EnvVar{Key: "K" + strings.Repeat("X", i), Value: "v"}
	}
	_, err := creatorWithHost(&fakeMgr{}, defaultFakeHost()).Create(t.Context(), httpapi.NewProfile{Name: "dev", Env: env})
	var verr *httpapi.ValidationError
	if !errors.As(err, &verr) || verr.Fields["env"] == "" {
		t.Fatalf("%v", err)
	}
}

// logCapture swaps the default logger for one that writes to a buffer.
func logCapture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// Drives the real route and creator with a distinctive mount path and value.
// Neither may appear in any response body or log line, on a rejection, on a
// launch failure or on success.
func TestMountPathsAndEnvValuesNeverReachResponsesOrLogs(t *testing.T) {
	const pathMarker, valMarker, keyMarker = "synthetic-path-marker", "synthetic-value-marker", "SYNTHETIC_KEY_MARKER"
	h := defaultFakeHost()
	h.dirs["/srv/"+pathMarker] = true
	for _, c := range []struct {
		name string
		body string
		mgr  *fakeMgr
		code int
	}{
		{"accepted", `{"name":"dev","mounts":[{"path":"/srv/` + pathMarker + `"}],"env":{"K":"` + valMarker + `"}}`, &fakeMgr{}, 202},
		{"launch fails", `{"name":"dev","mounts":[{"path":"/srv/` + pathMarker + `"}],"env":{"K":"` + valMarker + `"}}`,
			&fakeMgr{err: errors.New("start colima: boom")}, 502},
		{"bad path", `{"name":"dev","mounts":[{"path":"/srv/` + pathMarker + `,x"}]}`, &fakeMgr{}, 422},
		{"missing path", `{"name":"dev","mounts":[{"path":"/missing/` + pathMarker + `"}]}`, &fakeMgr{}, 422},
		{"relative path", `{"name":"dev","mounts":[{"path":"` + pathMarker + `/../x"}]}`, &fakeMgr{}, 422},
		{"duplicate", `{"name":"dev","mounts":[{"path":"/srv/` + pathMarker + `"},{"path":"/srv/` + pathMarker + `"}]}`, &fakeMgr{}, 422},
		{"bad value", `{"name":"dev","env":{"K":"` + valMarker + `\u0000"}}`, &fakeMgr{}, 422},
		{"long value", `{"name":"dev","env":{"K":"` + strings.Repeat(valMarker, 200) + `"}}`, &fakeMgr{}, 422},
		{"bad key", `{"name":"dev","env":{"` + keyMarker + `-":"` + valMarker + `"}}`, &fakeMgr{}, 422},
		{"malformed", `{"name":"dev","env":{"K":"` + valMarker + `","K":"x"}}`, &fakeMgr{}, 400},
		{"wrong type", `{"name":"dev","mounts":[{"path":"/srv/` + pathMarker + `","writable":"` + valMarker + `"}]}`, &fakeMgr{}, 400},
		{"unknown key", `{"name":"dev","env":{"K":"` + valMarker + `"},"` + valMarker + `":1}`, &fakeMgr{}, 400},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := logCapture(t)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/profiles", strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			httpapi.NewAPI(httpapi.WithCreate(creatorWithHost(c.mgr, h))).ServeHTTP(rec, req)
			if rec.Code != c.code {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			for _, out := range []string{rec.Body.String(), logs.String()} {
				for _, marker := range []string{pathMarker, valMarker} {
					if strings.Contains(out, marker) {
						t.Errorf("%q leaked: %s", marker, out)
					}
				}
				if strings.Contains(out, keyMarker+"-") {
					t.Errorf("a rejected key leaked: %s", out)
				}
			}
		})
	}
}

// The same through the adapter: a launch refused by Start and a launch that
// fails at the process level log and return no mount path or value.
func TestStartNeverEchoesMountOrEnvInErrors(t *testing.T) {
	const marker = "synthetic-marker"
	for _, o := range []colima.StartOptions{
		{Mounts: []colima.Mount{{Path: "/srv/" + marker + ",x"}}},
		{Env: []colima.EnvVar{{Key: "K", Value: marker + "\x00"}}},
		{Env: []colima.EnvVar{{Key: marker + "-", Value: "v"}}},
	} {
		_, err := colima.Start(t.Context(), &recordingStreamer{}, "dev", o, colimaLimits(testLimits))
		if err == nil || strings.Contains(err.Error(), marker) {
			t.Errorf("%v", err)
		}
	}
}
