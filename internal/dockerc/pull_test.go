package dockerc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

const (
	pullL1     = "aaaaaaaaaaaa"
	pullL2     = "bbbbbbbbbbbb"
	pullDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// pullDocker is a fake engine whose /images/create handler is h. It records the
// requests it saw.
type pullDocker struct {
	mu   sync.Mutex
	reqs []*http.Request
	h    http.HandlerFunc
}

func (d *pullDocker) count() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.reqs) }
func (d *pullDocker) last() *http.Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.reqs[len(d.reqs)-1]
}

func newPullClient(t *testing.T, h http.HandlerFunc) (*httpClient, *pullDocker) {
	t.Helper()
	d := &pullDocker{h: h}
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.reqs = append(d.reqs, r)
		d.mu.Unlock()
		d.h(w, r)
	}))
	return newHTTPClient(sock), d
}

func streamLines(lines ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		for _, l := range lines {
			io.WriteString(w, l+"\n")
		}
	}
}

func collect() (func(string), func() []string) {
	var mu sync.Mutex
	var out []string
	return func(s string) { mu.Lock(); out = append(out, s); mu.Unlock() },
		func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), out...) }
}

func normalPull() []string {
	return []string{
		`{"status":"Pulling from library/postgres","id":"18-alpine"}`,
		`{"status":"Pulling fs layer","progressDetail":{},"id":"` + pullL1 + `"}`,
		`{"status":"Pulling fs layer","progressDetail":{},"id":"` + pullL2 + `"}`,
		`{"status":"Already exists","progressDetail":{},"id":"cccccccccccc"}`,
		`{"status":"Downloading","progressDetail":{"current":1000,"total":4000},"progress":"[=>   ]","id":"` + pullL1 + `"}`,
		`{"status":"Downloading","progressDetail":{"current":2100,"total":4000},"progress":"[==>  ]","id":"` + pullL1 + `"}`,
		`{"status":"Downloading","progressDetail":{"current":3900,"total":4000},"progress":"[===> ]","id":"` + pullL1 + `"}`,
		`{"status":"Verifying Checksum","progressDetail":{},"id":"` + pullL1 + `"}`,
		`{"status":"Download complete","progressDetail":{},"id":"` + pullL1 + `"}`,
		`{"status":"Extracting","progressDetail":{"current":4000,"total":4000},"id":"` + pullL1 + `"}`,
		`{"status":"Pull complete","progressDetail":{},"id":"` + pullL1 + `"}`,
		`{"status":"Digest: ` + pullDigest + `"}`,
		`{"status":"Status: Downloaded newer image for postgres:18-alpine"}`,
	}
}

func TestPullImageNormalStream(t *testing.T) {
	c, d := newPullClient(t, streamLines(normalPull()...))
	emit, got := collect()
	if err := c.PullImage(context.Background(), "postgres:18-alpine", emit); err != nil {
		t.Fatal(err)
	}
	r := d.last()
	if r.Method != "POST" || r.URL.EscapedPath() != "/v1.41/images/create" {
		t.Fatalf("request = %s %s", r.Method, r.URL.EscapedPath())
	}
	q := r.URL.Query()
	if len(q) != 2 || q.Get("fromImage") != "postgres" || q.Get("tag") != "18-alpine" {
		t.Fatalf("query = %v", q)
	}
	for _, h := range []string{"X-Registry-Auth", "Authorization", "Content-Type"} {
		if r.Header.Get(h) != "" {
			t.Errorf("header %s sent", h)
		}
	}
	if b, _ := io.ReadAll(r.Body); len(b) != 0 {
		t.Errorf("body sent: %q", b)
	}
	want := []string{
		"Pulling from library/postgres",
		pullL1 + ": waiting",
		pullL2 + ": waiting",
		"cccccccccccc: already exists",
		pullL1 + ": downloading (4.0 kB)",
		pullL1 + ": downloading 25%",
		pullL1 + ": downloading 50%",
		pullL1 + ": downloading 75%",
		pullL1 + ": download complete",
		pullL1 + ": extracting",
		pullL1 + ": pull complete",
		"Digest: " + pullDigest,
		"Status: downloaded newer image",
	}
	if g := got(); strings.Join(g, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines:\n%s\nwant:\n%s", strings.Join(g, "\n"), strings.Join(want, "\n"))
	}
}

func TestPullImageQueryShapes(t *testing.T) {
	long := "sha256:" + strings.Repeat("ab", 32)
	tests := []struct{ ref, from, tag string }{
		{"postgres:18-alpine", "postgres", "18-alpine"},
		{"library/postgres:18", "library/postgres", "18"},
		{"localhost:5000/team/app:v1.2", "localhost:5000/team/app", "v1.2"},
		{"ghcr.io/owner/app:latest", "ghcr.io/owner/app", "latest"},
		{"postgres@" + long, "postgres@" + long, ""},
		{"localhost:5000/app@" + long, "localhost:5000/app@" + long, ""},
		{"ghcr.io/o/app:v1@" + long, "ghcr.io/o/app:v1@" + long, ""},
	}
	for _, tc := range tests {
		c, d := newPullClient(t, streamLines(`{"status":"Status: Image is up to date for x"}`))
		if err := c.PullImage(context.Background(), tc.ref, nil); err != nil {
			t.Fatalf("%s: %v", tc.ref, err)
		}
		q := d.last().URL.Query()
		if q.Get("fromImage") != tc.from || q.Get("tag") != tc.tag || len(q) != map[bool]int{true: 2, false: 1}[tc.tag != ""] {
			t.Errorf("%s: query = %v", tc.ref, q)
		}
	}
}

func TestPullImageUpToDate(t *testing.T) {
	c, _ := newPullClient(t, streamLines(
		`{"status":"Pulling from library/postgres","id":"18-alpine"}`,
		`{"status":"Digest: `+pullDigest+`"}`,
		`{"status":"Status: Image is up to date for postgres:18-alpine"}`))
	emit, got := collect()
	if err := c.PullImage(context.Background(), "postgres:18-alpine", emit); err != nil {
		t.Fatal(err)
	}
	g := got()
	if len(g) != 3 || g[2] != "Status: image is up to date, nothing was downloaded" {
		t.Fatalf("lines = %q", g)
	}
}

func TestPullImageErrorInsideA200Stream(t *testing.T) {
	c, _ := newPullClient(t, streamLines(
		`{"status":"Pulling from library/nope","id":"1"}`,
		`{"errorDetail":{"message":"pull access denied for nope, repository does not exist or may require 'docker login'"},"error":"pull access denied for nope, repository does not exist or may require 'docker login'"}`,
		`{"status":"Status: Downloaded newer image for nope:1"}`))
	emit, got := collect()
	err := c.PullImage(context.Background(), "nope:1", emit)
	var pe *PullError
	if !errors.As(err, &pe) || !strings.HasPrefix(pe.Message, "pull access denied for nope") {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, ErrPullFailed) || !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("kind = %v", err)
	}
	if strings.Contains(err.Error(), "denied") {
		t.Errorf("Error() carries registry text: %q", err.Error())
	}
	if g := got(); len(g) != 1 {
		t.Errorf("lines after the error object were processed: %q", g)
	}
}

func TestPullImageErrorMidStreamAfterProgress(t *testing.T) {
	lines := append(normalPull()[:5:5], `{"error":"unauthorized: authentication required"}`)
	c, _ := newPullClient(t, streamLines(lines...))
	err := c.PullImage(context.Background(), "postgres:18-alpine", func(string) {})
	var pe *PullError
	if !errors.As(err, &pe) || pe.Message != "unauthorized: authentication required" {
		t.Fatalf("err = %v", err)
	}
}

func TestPullImageStatusErrors(t *testing.T) {
	tests := []struct {
		status int
		body   string
		kind   error
		msg    string
	}{
		{404, `{"message":"manifest for postgres:nope not found: manifest unknown: manifest unknown"}`, ErrObjectNotFound, "manifest for postgres:nope not found: manifest unknown: manifest unknown"},
		{404, `not json at all`, ErrObjectNotFound, "The image was not found, or it needs credentials."},
		{400, `{"message":"invalid reference format"}`, ErrInvalidRequest, "invalid reference format"},
		{500, `{"message":"Get \"https://registry-1.docker.io/v2/\": dial tcp: lookup registry-1.docker.io: no such host"}`, ErrDaemon, `Get "https://registry-1.docker.io/v2/": dial tcp: lookup registry-1.docker.io: no such host`},
		{500, `{"message":"open /home/user/.colima/synthetic/docker.sock: permission denied"}`, ErrDaemon, "open [path]: permission denied"},
		{500, "\x1b[31m{\"message\":\"x\"}", ErrDaemon, "Docker answered with an error."},
		{500, `{"message":"{\"raw\":true}"}`, ErrDaemon, "Docker answered with an error."},
	}
	for i, tc := range tests {
		c, _ := newPullClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) })
		err := c.PullImage(context.Background(), "postgres:18", nil)
		var pe *PullError
		if !errors.Is(err, tc.kind) || !errors.As(err, &pe) || pe.Message != tc.msg {
			t.Errorf("%d: err = %v, msg = %q", i, err, func() string {
				if pe != nil {
					return pe.Message
				}
				return ""
			}())
		}
	}
}

func TestPullImageCancelStopsTheStream(t *testing.T) {
	disconnected := make(chan struct{})
	started := make(chan struct{})
	c, _ := newPullClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, `{"status":"Pulling from library/postgres","id":"18"}`+"\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(disconnected)
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.PullImage(ctx, "postgres:18", func(string) {}) }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PullImage did not return after cancel")
	}
	select {
	case <-disconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("the HTTP connection was not closed")
	}
}

func TestPullImageIdleTimeout(t *testing.T) {
	c, _ := newPullClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, `{"status":"Pulling from library/postgres","id":"18"}`+"\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	c.pullIdle = 100 * time.Millisecond
	start := time.Now()
	err := c.PullImage(context.Background(), "postgres:18", func(string) {})
	var pe *PullError
	if !errors.As(err, &pe) || !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("idle timeout took too long")
	}
}

func TestPullImageOverallTimeout(t *testing.T) {
	c, _ := newPullClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		for r.Context().Err() == nil {
			io.WriteString(w, `{"status":"Downloading","progressDetail":{"current":1,"total":2},"id":"`+pullL1+`"}`+"\n")
			w.(http.Flusher).Flush()
			time.Sleep(10 * time.Millisecond)
		}
	})
	c.pullTimeout = 200 * time.Millisecond
	err := c.PullImage(context.Background(), "postgres:18", func(string) {})
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestPullImageStreamWithoutResultFails(t *testing.T) {
	c, _ := newPullClient(t, streamLines(normalPull()[:6]...))
	err := c.PullImage(context.Background(), "postgres:18-alpine", func(string) {})
	var pe *PullError
	if !errors.As(err, &pe) || !errors.Is(err, ErrPullFailed) {
		t.Fatalf("err = %v", err)
	}
}

func TestPullImageThrottlesByteTicks(t *testing.T) {
	var lines []string
	lines = append(lines, `{"status":"Pulling fs layer","id":"`+pullL1+`"}`)
	for i := 0; i <= 20000; i++ {
		lines = append(lines, fmt.Sprintf(`{"status":"Downloading","progressDetail":{"current":%d,"total":20000},"progress":"x","id":"%s"}`, i, pullL1))
	}
	lines = append(lines, `{"status":"Status: Downloaded newer image for x:1"}`)
	c, _ := newPullClient(t, streamLines(lines...))
	emit, got := collect()
	if err := c.PullImage(context.Background(), "x:1", emit); err != nil {
		t.Fatal(err)
	}
	if n := len(got()); n > 8 {
		t.Fatalf("%d lines for 20000 ticks: %q", n, got())
	}
}

func TestPullImageHostileStream(t *testing.T) {
	huge := `{"status":"Pulling from library/` + strings.Repeat("a", 200<<10) + `","id":"x"}`
	lines := []string{
		"not json", "", "{", "[]", `"str"`, "null", `{"status":5}`, `{"id":{"a":1}}`,
		huge,
		`{"status":"Pulling from library/\u001b[31mred\u001b[0m\u202e\u0007name","id":"x"}`,
		`{"status":"Downloading","id":"zzzz","progressDetail":{"current":-5,"total":-1}}`,
		`{"status":"Downloading","id":"` + pullL1 + `","progressDetail":{"current":99999999999,"total":1}}`,
		`{"status":"Downloading","id":"` + pullL2 + `","progressDetail":{"current":1,"total":0}}`,
		`{"status":"Digest: sha256:not-hex"}`,
		`{"status":"Digest: ` + pullDigest + `\n{\"fake\":1}"}`,
		`{"status":"Status: Downloaded newer image for \u001b[2J\u001b]0;pwn\u0007x"}`,
	}
	c, _ := newPullClient(t, streamLines(lines...))
	emit, got := collect()
	if err := c.PullImage(context.Background(), "x:1", emit); err != nil {
		t.Fatal(err)
	}
	for _, l := range got() {
		if len(l) > 400 || !utf8.ValidString(l) {
			t.Errorf("bad line %q", l)
		}
		for _, r := range l {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x202e {
				t.Errorf("control character in %q", l)
			}
		}
		if strings.Contains(l, "{") || strings.Contains(l, "fake") || strings.Contains(l, "pwn") {
			t.Errorf("raw stream text echoed: %q", l)
		}
	}
}

func TestPullImageCapsLayersAndLines(t *testing.T) {
	var lines []string
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("%012x", i+1)
		lines = append(lines,
			`{"status":"Pulling fs layer","id":"`+id+`"}`,
			`{"status":"Pull complete","id":"`+id+`"}`)
	}
	lines = append(lines, `{"status":"Status: Downloaded newer image for x:1"}`)
	c, _ := newPullClient(t, streamLines(lines...))
	emit, got := collect()
	if err := c.PullImage(context.Background(), "x:1", emit); err != nil {
		t.Fatal(err)
	}
	if n := len(got()); n > maxPullLayers*2+2 {
		t.Fatalf("%d lines for 1000 layers", n)
	}
}

func TestPullImageRefusesBadReferencesBeforeAnyRequest(t *testing.T) {
	c, d := newPullClient(t, streamLines(`{"status":"Status: Image is up to date"}`))
	hex64 := strings.Repeat("a", 64)
	bad := []string{
		"", "postgres", "postgres:", ":18", "localhost:5000/app", "Postgres:18", "post gres:18", "postgres:18 ", "postgres:1 8",
		"postgres:18;rm", "postgres:$(x)", "postgres:`x`", "postgres:18&fromSrc=http://x", "postgres:18?x=1", "postgres:18#x",
		"postgres:18\n", "postgres:18\x00", "postgres:18\r\nX-Registry-Auth: x", "--rm", "-postgres:18", "postgres:--18", "a/../b:1",
		"/abs:1", "a//b:1", "a/:1", "a:b:c", "postgres:18@sha256:zz", "postgres@sha256:zz", "postgres@", "@" + pullDigest,
		hex64 + ":1", hex64 + "@" + pullDigest, "sha256:" + hex64, "sha256@" + pullDigest, "sha256:18",
		strings.Repeat("a", 256) + ":1", "postgres:" + strings.Repeat("a", 129), "postgres:18\u202e", "postgres:1\u200b8", "pöstgres:18",
		"%2e%2e/x:1", "a%2Fb:1", "postgres:%0a", "http://evil/x:1", "unix:///var/run/docker.sock:1", "host:99999999/app:1",
		"a\\b:1", "[::1]:5000/app:1", "postgres:18,tag=x",
	}
	for _, ref := range bad {
		if ValidPullRef(ref) {
			t.Errorf("ValidPullRef(%q) = true", ref)
		}
		if err := c.PullImage(context.Background(), ref, func(string) { t.Errorf("emit for %q", ref) }); !errors.Is(err, ErrBadName) {
			t.Errorf("PullImage(%q) = %v", ref, err)
		}
	}
	if n := d.count(); n != 0 {
		t.Fatalf("%d requests reached the socket", n)
	}
}

func TestValidPullRefAccepts(t *testing.T) {
	long := "sha256:" + strings.Repeat("ab", 32)
	for _, ref := range []string{
		"postgres:18-alpine", "library/postgres:18", "localhost:5000/app:v1", "Registry.Example.com:5000/a/b:1.2.3_x",
		"ghcr.io/owner/app:latest", "postgres@" + long, "ghcr.io/o/app:v1@" + long, "a__b/c-d.e:T", "postgres:" + strings.Repeat("a", 128),
		strings.Repeat("a", 63) + ":1", strings.Repeat("a", 65) + ":1",
	} {
		if !ValidPullRef(ref) {
			t.Errorf("ValidPullRef(%q) = false", ref)
		}
	}
}

func TestPullImageTransportErrorLeaksNoPath(t *testing.T) {
	c := newHTTPClient("/nonexistent/synthetic/docker.sock")
	err := c.PullImage(context.Background(), "postgres:18", func(string) {})
	if !errors.Is(err, ErrDockerUnavailable) || strings.Contains(err.Error(), "synthetic") {
		t.Fatalf("err = %v", err)
	}
}

func TestPullImageNoGoroutineLeakAfterCancel(t *testing.T) {
	var live atomic.Int32
	c, _ := newPullClient(t, func(w http.ResponseWriter, r *http.Request) {
		live.Add(1)
		defer live.Add(-1)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		err := c.PullImage(ctx, "postgres:18", func(string) {})
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v", err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for live.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if live.Load() != 0 {
		t.Fatal("server handlers still running: connections were not closed")
	}
}

func TestCleanPullText(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain message", "plain message"},
		{"  spaced \t out\n\nlines  ", "spaced out lines"},
		{"\x1b[31mred\x1b[0m text", "red text"},
		{"\x1b]0;title\x07after", "after"},
		{"a\x00b\x07c\x7fd", "abcd"},
		{"bidi \u202eevil\u202c \u200bzero", "bidi evil zero"},
		{"bad \xff\xfe utf8", "bad utf8"},
		{`{"message":"raw"}`, ""},
		{`[{"a":1}]`, ""},
		{"open /var/run/docker.sock: denied", "open [path]: denied"},
		{"listen unix:///home/user/.colima/default/docker.sock failed", "listen [path] failed"},
		{"read /home/user/.colima/x", "read [path]"},
		{"read ~/.colima/x/y", "read [path]"},
		{"C:\\Users\\user\\x denied", "[path] denied"},
		{"repo foo/bar not found", "repo foo/bar not found"},
		{`Get "https://registry-1.docker.io/v2/": no such host`, `Get "https://registry-1.docker.io/v2/": no such host`},
		{strings.Repeat("x", 1000), strings.Repeat("x", 299) + "…"},
	}
	for _, tc := range tests {
		got := CleanPullText(tc.in)
		if got != tc.want {
			t.Errorf("CleanPullText(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if again := CleanPullText(got); again != got {
			t.Errorf("not idempotent for %q: %q -> %q", tc.in, got, again)
		}
	}
	// Hostile bulk input stays bounded and valid.
	big := strings.Repeat("\x1b[31m/a/b ", 1<<16)
	if out := CleanPullText(big); len([]rune(out)) > 300 || !utf8.ValidString(out) {
		t.Errorf("bulk input: %d runes", len([]rune(out)))
	}
}

func TestReadBoundedLine(t *testing.T) {
	in := "short\n" + strings.Repeat("x", 100) + "\nlast"
	r := newTestReader(in)
	var got []string
	var long []bool
	for {
		l, tl, err := readBoundedLine(r, 10)
		got, long = append(got, string(l)), append(long, tl)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if fmt.Sprint(got) != "[short  last]" || fmt.Sprint(long) != "[false true false]" {
		t.Fatalf("got %q %v", got, long)
	}
}

func TestPullQueryNeverCarriesImportParameters(t *testing.T) {
	c, d := newPullClient(t, streamLines(`{"status":"Status: Image is up to date"}`))
	if err := c.PullImage(context.Background(), "postgres:18", nil); err != nil {
		t.Fatal(err)
	}
	q := d.last().URL.Query()
	for _, k := range []string{"fromSrc", "repo", "changes", "platform", "message"} {
		if _, ok := q[k]; ok {
			t.Errorf("query has %s", k)
		}
	}
	if _, err := url.ParseQuery(d.last().URL.RawQuery); err != nil {
		t.Fatal(err)
	}
}

func newTestReader(s string) *bufio.Reader { return bufio.NewReaderSize(strings.NewReader(s), 16) }
