package dockerc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type seen struct {
	method, path, rawQuery, ctype, body string
}

// recordingDocker answers every request with status and body and records the
// last request.
func recordingDocker(t *testing.T, status int, reply string) (*httpClient, *seen, *atomic.Int32) {
	t.Helper()
	var last seen
	var n atomic.Int32
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		last = seen{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Content-Type"), string(b)}
		n.Add(1)
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	return newHTTPClient(sock), &last, &n
}

func TestPostSendsJSONAndDecodesAnswer(t *testing.T) {
	c, last, _ := recordingDocker(t, 201, `{"Name":"synthetic-vol"}`)
	var out struct{ Name string }
	q := map[string][]string{"force": {"1"}}
	err := c.post(context.Background(), time.Second, fixedTarget("/volumes/create"), q, map[string]string{"Name": "synthetic-vol"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "synthetic-vol" {
		t.Fatalf("out = %+v", out)
	}
	if last.method != "POST" || last.path != "/v1.41/volumes/create" || last.rawQuery != "force=1" ||
		last.ctype != "application/json" || last.body != `{"Name":"synthetic-vol"}` {
		t.Fatalf("request = %+v", *last)
	}
}

func TestPostWithoutBodyHasNoContentType(t *testing.T) {
	c, last, _ := recordingDocker(t, 204, "")
	if err := c.post(context.Background(), time.Second, fixedTarget("/images/prune"), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if last.ctype != "" || last.body != "" {
		t.Fatalf("request = %+v", *last)
	}
}

func TestDeleteSendsDeleteWithEscapedPath(t *testing.T) {
	c, last, _ := recordingDocker(t, 204, "")
	p, err := objectTarget("/volumes", "synthetic.vol_1-x")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.delete(context.Background(), time.Second, p, map[string][]string{"force": {"true"}}, nil); err != nil {
		t.Fatal(err)
	}
	if last.method != "DELETE" || last.path != "/v1.41/volumes/synthetic.vol_1-x" || last.rawQuery != "force=true" {
		t.Fatalf("request = %+v", *last)
	}
}

func TestWriteStatusMapping(t *testing.T) {
	tests := []struct {
		status int
		ok     []int
		want   error // nil means success
	}{
		{200, nil, nil},
		{201, nil, nil},
		{204, nil, nil},
		{304, []int{304}, nil},
		{304, nil, ErrDaemon},
		{204, []int{200}, ErrDaemon},
		{404, nil, ErrObjectNotFound},
		{409, nil, ErrConflict},
		{403, nil, ErrConflict},
		{400, nil, ErrInvalidRequest},
		{500, nil, ErrDaemon},
		{503, nil, ErrDaemon},
		{418, nil, ErrDaemon},
		{301, nil, ErrDaemon},
	}
	for _, tc := range tests {
		for _, method := range []string{"post", "delete"} {
			c, _, _ := recordingDocker(t, tc.status, "Error: secret /home/user/.colima/synthetic")
			var err error
			if method == "post" {
				err = c.post(context.Background(), time.Second, fixedTarget("/networks/create"), nil, nil, nil, tc.ok...)
			} else {
				err = c.delete(context.Background(), time.Second, fixedTarget("/networks/x"), nil, nil, tc.ok...)
			}
			if tc.want == nil {
				if err != nil {
					t.Errorf("%s %d: err = %v", method, tc.status, err)
				}
				continue
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("%s %d: err = %v, want %v", method, tc.status, err, tc.want)
				continue
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/home") {
				t.Errorf("%s %d: error carries daemon text: %q", method, tc.status, err)
			}
		}
	}
}

func TestSendRedirectIsNotFollowed(t *testing.T) {
	// A 3xx must come back as a daemon error, never as a second request.
	var n atomic.Int32
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	}))
	c := newHTTPClient(sock)
	err := c.delete(context.Background(), time.Second, fixedTarget("/volumes/x"), nil, nil)
	if !errors.Is(err, ErrDaemon) && !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if n.Load() > 1 {
		t.Fatalf("redirect followed: %d requests", n.Load())
	}
}

func TestSendTimeoutAndCancel(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	c := newHTTPClient(sock)

	err := c.post(context.Background(), 50*time.Millisecond, fixedTarget("/volumes/create"), nil, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("timeout err = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	err = c.delete(ctx, 5*time.Second, fixedTarget("/volumes/x"), nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err = %v", err)
	}
}

func TestSendDefaultTimeoutWhenZero(t *testing.T) {
	c, _, _ := recordingDocker(t, 204, "")
	if err := c.post(context.Background(), 0, fixedTarget("/volumes/create"), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSendSocketGoneHidesPath(t *testing.T) {
	sock := startFakeDocker(t, http.NotFoundHandler())
	closeSockListener(t, sock)
	c := newHTTPClient(sock)
	err := c.post(context.Background(), time.Second, fixedTarget("/volumes/create"), nil, nil, nil)
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), sock) || strings.Contains(err.Error(), "/") {
		t.Fatalf("error leaks the path: %q", err)
	}
}

func TestSendAnswerLimits(t *testing.T) {
	c, _, _ := recordingDocker(t, 200, strings.Repeat("a", 100))
	c.maxBody = 10
	var out map[string]any
	if err := c.post(context.Background(), time.Second, fixedTarget("/x"), nil, nil, &out); !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("oversize err = %v", err)
	}
	c, _, _ = recordingDocker(t, 200, `not json`)
	if err := c.post(context.Background(), time.Second, fixedTarget("/x"), nil, nil, &out); !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("malformed err = %v", err)
	}
}

func TestSendUnmarshalableBody(t *testing.T) {
	c, _, n := recordingDocker(t, 204, "")
	err := c.post(context.Background(), time.Second, fixedTarget("/x"), nil, make(chan int), nil)
	if !errors.Is(err, ErrInvalidRequest) || n.Load() != 0 {
		t.Fatalf("err = %v, requests = %d", err, n.Load())
	}
}

func TestSendRefusesUnsafePathsBeforeAnyRequest(t *testing.T) {
	c, _, n := recordingDocker(t, 204, "")
	for _, p := range []string{
		"", "volumes/x", "/", "/volumes/", "/volumes//x", "/volumes/..", "/volumes/.",
		"/volumes/%2e%2e", "/volumes/%2E", "/a/../b", "/volumes/x?force=1", "/volumes/x#y",
		"/volumes/x%3Fy", "/volumes/x%23y", "/volumes/a%0Ab", "/volumes/a\nb", "/volumes/%00",
		"/volumes/a%5Cb", "/volumes/%zz", "/volumes/a b\x7f",
	} {
		if err := c.delete(context.Background(), time.Second, fixedTarget(fixedPath(p)), nil, nil); !errors.Is(err, ErrBadName) {
			t.Errorf("delete %q: err = %v", p, err)
		}
		if err := c.post(context.Background(), time.Second, fixedTarget(fixedPath(p)), nil, nil, nil); !errors.Is(err, ErrBadName) {
			t.Errorf("post %q: err = %v", p, err)
		}
	}
	if n.Load() != 0 {
		t.Fatalf("%d requests reached the socket", n.Load())
	}
}

func TestSendBodyIsValidJSON(t *testing.T) {
	c, last, _ := recordingDocker(t, 204, "")
	body := map[string]any{"Name": "a\"b\n", "Labels": map[string]string{"k": "v"}}
	if err := c.post(context.Background(), time.Second, fixedTarget("/volumes/create"), nil, body, nil); err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(last.body), &back); err != nil || back["Name"] != "a\"b\n" {
		t.Fatalf("body %q: %v", last.body, err)
	}
}

// TestTargetsRefuseHostileNamesOnTheRealClient builds every target kind from
// hostile names and sends the survivors through the real httpClient: none may
// reach the socket.
func TestTargetsRefuseHostileNamesOnTheRealClient(t *testing.T) {
	c, _, n := recordingDocker(t, 204, "")
	hostile := []string{
		"x/disconnect", "x/../y", "../x", "..", "%2e%2e", "a%2Fb", "a\nb", "a\x00b", "x?force=1", "x#f",
		strings.Repeat("a", 129), "", " x", "x ", "x/", "/x",
	}
	for _, h := range hostile {
		if tg, err := objectTarget("/networks", h, "/disconnect"); err == nil {
			t.Errorf("objectTarget accepted %q: %q", h, tg.path)
		}
	}
	for _, h := range []string{"x/../y", "x//y", "A/b", "%2e%2e", "a\nb", "x?y", "x#y", "", "x/./y"} {
		tg, err := refTarget("/images", h, "/tag")
		if err != nil {
			continue
		}
		// Whatever refTarget accepts must be a grammatical reference, so the
		// only slashes are reference separators, never a traversal.
		if h != "A/b" {
			t.Errorf("refTarget accepted %q: %q", h, tg.path)
		}
	}
	// A hostile name never yields a target to send, so run the helpers with
	// the zero target a careless caller would use after ignoring the error.
	if err := c.delete(context.Background(), time.Second, target{}, nil, nil); !errors.Is(err, ErrBadName) {
		t.Errorf("zero target: %v", err)
	}
	if err := c.post(context.Background(), time.Second, target{}, nil, nil, nil); !errors.Is(err, ErrBadName) {
		t.Errorf("zero target: %v", err)
	}
	if n.Load() != 0 {
		t.Fatalf("%d requests reached the socket", n.Load())
	}
	// A valid name adds exactly one segment and the constant suffix.
	c, last, _ := recordingDocker(t, 204, "")
	tg, err := objectTarget("/networks", "net-1", "/disconnect")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.post(context.Background(), time.Second, tg, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if last.path != "/v1.41/networks/net-1/disconnect" {
		t.Fatalf("path = %q", last.path)
	}
}
