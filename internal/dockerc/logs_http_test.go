package dockerc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

const logID = "aaaaaaaaaaaa"

// logsStub serves inspect and logs for one container and records requests.
type logsStub struct {
	mu       sync.Mutex
	requests []string
	tty      bool
	body     []byte
	logsCode int
	logs     func(w http.ResponseWriter, r *http.Request)
}

func (s *logsStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, r.Method+" "+r.URL.RequestURI())
	s.mu.Unlock()
	switch {
	case r.URL.Path == "/v1.41/containers/"+logID+"/json":
		fmt.Fprintf(w, `{"Id":"%s","Config":{"Tty":%v}}`, logID, s.tty)
	case r.URL.Path == "/v1.41/containers/"+logID+"/logs":
		if s.logs != nil {
			s.logs(w, r)
			return
		}
		if s.logsCode != 0 {
			w.WriteHeader(s.logsCode)
			fmt.Fprint(w, `{"message":"SECRET detail"}`)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
		w.Write(s.body)
	default:
		w.WriteHeader(404)
		fmt.Fprint(w, `{"message":"no such container"}`)
	}
}

func (s *logsStub) reqs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func logsClient(t *testing.T, s *logsStub) *httpClient {
	return newHTTPClient(startFakeDocker(t, s))
}

func TestLogsMultiplexedSendsFixedQuery(t *testing.T) {
	s := &logsStub{body: mux(frame(1, "t1 a\n"), frame(2, "t2 b\n"), frame(1, "t3 c\n"))}
	res, err := logsClient(t, s).Logs(context.Background(), logID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(res.Lines) != "[t2 b t3 c]" || !res.Older {
		t.Fatalf("%+v", res)
	}
	reqs := s.reqs()
	if len(reqs) != 2 || reqs[0] != "GET /v1.41/containers/"+logID+"/json" ||
		reqs[1] != "GET /v1.41/containers/"+logID+"/logs?stderr=1&stdout=1&tail=3&timestamps=1" {
		t.Fatalf("requests %q", reqs)
	}
}

func TestLogsTTYUsesInspectNotSniffing(t *testing.T) {
	// A raw body that looks like a mux header is still text for a TTY.
	s := &logsStub{tty: true, body: []byte("\x01\x00\x00\x00 not a frame\nline two\n")}
	res, err := logsClient(t, s).Logs(context.Background(), logID, 10)
	if err != nil || len(res.Lines) != 2 || res.Lines[1] != "line two" {
		t.Fatalf("%v %q", err, res.Lines)
	}
	// The same bytes without a TTY are a malformed stream.
	s.tty = false
	if _, err = logsClient(t, s).Logs(context.Background(), logID, 10); !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("%v", err)
	}
}

func TestLogsTailClampedAndNeverZero(t *testing.T) {
	for in, want := range map[int]string{-5: "tail=2", 0: "tail=2", 1: "tail=2", 1000: "tail=1001", 5000: "tail=1001"} {
		s := &logsStub{}
		if _, err := logsClient(t, s).Logs(context.Background(), logID, in); err != nil {
			t.Fatal(err)
		}
		reqs := s.reqs()
		if !strings.Contains(reqs[1], want+"&") && !strings.HasSuffix(reqs[1], want) {
			t.Fatalf("tail %d: %q want %s", in, reqs[1], want)
		}
		if strings.Contains(reqs[1], "tail=0") {
			t.Fatalf("tail=0 sent: %q", reqs[1])
		}
	}
}

func TestLogsBadIDNeverReachesDocker(t *testing.T) {
	for _, id := range []string{"", "abc", "AAAAAAAAAAAA", "../json", "aaaaaaaaaaaa/../..", "aaaaaaaaaaaa?x=1", "aaaaaaaaaaaa\n", "synthetic-web", strings.Repeat("a", 65), "aaaaaaaaaaaz"} {
		s := &logsStub{}
		_, err := logsClient(t, s).Logs(context.Background(), id, 5)
		if !errors.Is(err, ErrContainerNotFound) || len(s.reqs()) != 0 {
			t.Fatalf("%q: %v %v", id, err, s.reqs())
		}
	}
}

func TestLogsErrorMapping(t *testing.T) {
	ctx := context.Background()
	// Unknown id: the inspect gives 404.
	s := &logsStub{}
	if _, err := logsClient(t, s).Logs(ctx, "bbbbbbbbbbbb", 5); !errors.Is(err, ErrContainerNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if len(s.reqs()) != 1 {
		t.Fatalf("logs call after failed inspect: %q", s.reqs())
	}
	// Logs call 404 is a missing container too, never remapped.
	s = &logsStub{logsCode: 404}
	if _, err := logsClient(t, s).Logs(ctx, logID, 5); !errors.Is(err, ErrContainerNotFound) {
		t.Fatalf("logs 404: %v", err)
	}
	// 501 is an unreadable log driver.
	s = &logsStub{logsCode: 501}
	if _, err := logsClient(t, s).Logs(ctx, logID, 5); !errors.Is(err, ErrLogsUnavailable) || errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("501: %v", err)
	}
	// Other statuses are 502 and carry no engine text.
	for _, code := range []int{400, 500, 503} {
		s = &logsStub{logsCode: code}
		_, err := logsClient(t, s).Logs(ctx, logID, 5)
		if !errors.Is(err, ErrDockerUnavailable) || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("%d: %v", code, err)
		}
	}
}

func TestLogsMalformedStreamIsUnavailable(t *testing.T) {
	for name, body := range map[string][]byte{
		"bad type":     {9, 0, 0, 0, 0, 0, 0, 1, 'x'},
		"eof in frame": frame(1, "hello\n")[:10],
	} {
		s := &logsStub{body: body}
		_, err := logsClient(t, s).Logs(context.Background(), logID, 5)
		if !errors.Is(err, ErrDockerUnavailable) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestLogsInspectGarbageIsUnavailable(t *testing.T) {
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) }))
	if _, err := newHTTPClient(sock).Logs(context.Background(), logID, 5); !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("%v", err)
	}
}

func TestLogsSlowChunkedStreamStillDecodes(t *testing.T) {
	body := mux(frame(1, "t1 one\n"), frame(2, "t2 two\n"))
	s := &logsStub{logs: func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		for i := 0; i < len(body); i += 3 {
			w.Write(body[i:min(i+3, len(body))])
			fl.Flush()
		}
	}}
	res, err := logsClient(t, s).Logs(context.Background(), logID, 5)
	if err != nil || fmt.Sprint(res.Lines) != "[t1 one t2 two]" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestLogsStalledStreamTimesOut(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s := &logsStub{logs: func(w http.ResponseWriter, r *http.Request) {
		w.Write(frame(1, "t1 first\n"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}}
	c := logsClient(t, s)
	c.logsTimeout = 150 * time.Millisecond
	start := time.Now()
	_, err := c.Logs(context.Background(), logID, 5)
	// A deadline is a 502 for the route, never an unclassified error.
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("%v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("did not time out promptly")
	}
}

func TestLogsCallerCancelStopsRead(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s := &logsStub{logs: func(w http.ResponseWriter, r *http.Request) {
		w.Write(frame(1, "t1 first\n"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	_, err := logsClient(t, s).Logs(ctx, logID, 5)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

func TestLogsUnreachableSocket(t *testing.T) {
	c := newHTTPClient("/nonexistent/synthetic.sock")
	if _, err := c.Logs(context.Background(), logID, 5); !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("%v", err)
	}
}
