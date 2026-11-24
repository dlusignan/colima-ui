package dockerc

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	synthID64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	synthID12 = "0123456789ab"
)

func TestValidContainerID(t *testing.T) {
	good := []string{synthID12, synthID64, "abcdefabcdef", "000000000000", synthID64[:40]}
	for _, id := range good {
		if !ValidContainerID(id) {
			t.Errorf("%q refused", id)
		}
	}
	bad := map[string]string{
		"empty":        "",
		"name":         "synthetic-web",
		"hex name":     "synthetic-web1",
		"short prefix": "0123456789a",
		"too long":     synthID64 + "0",
		"uppercase":    "0123456789AB",
		"mixed case":   "0123456789aB",
		"non hex":      "0123456789ag",
		"slash":        "0123456789a/",
		"dotdot":       "0123456789../abcd",
		"percent":      "0123456789%2e",
		"query":        "0123456789a?",
		"fragment":     "0123456789a#",
		"backslash":    "0123456789a\\",
		"space":        "0123456789a ",
		"colon":        "sha256:0123456789ab",
		"newline":      synthID12 + "\n",
		"nul":          "0123456789a\x00",
		"tab":          "0123456789a\t",
		"del":          "0123456789a\x7f",
		"zero width":   "0123456789a​",
		"bidi":         "0123456789a‮",
		"fullwidth":    "０１２３４５６７８９ａｂ",
		"leading sp":   " " + synthID12,
	}
	for name, id := range bad {
		if ValidContainerID(id) {
			t.Errorf("%s: %q accepted", name, id)
		}
	}
}

type containerCall struct {
	name string
	run  func(c *httpClient, id string) (bool, error)
	path string
	// ok is the status that means "did it".
}

var containerCalls = []containerCall{
	{"start", func(c *httpClient, id string) (bool, error) { return c.StartContainer(context.Background(), id) }, "/start"},
	{"stop", func(c *httpClient, id string) (bool, error) { return c.StopContainer(context.Background(), id) }, "/stop"},
	{"restart", func(c *httpClient, id string) (bool, error) { return c.RestartContainer(context.Background(), id) }, "/restart"},
}

func TestContainerActionsRefuseHostileIDsBeforeTheSocket(t *testing.T) {
	var n atomic.Int32
	sock := startFakeDocker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(204)
	}))
	c := newHTTPClient(sock)
	hostile := []string{
		"", "synthetic-web", "0123456789a", synthID64 + "0", "0123456789AB", "0123456789ab/stop",
		"../../volumes/x", "0123456789a?force=1", "0123456789a#", "0123456789%2fab", "0123456789a\n",
		"0123456789a\x00", "0123456789a​", "0123456789a‮", "0123456789a b", "0123456789a\\",
		synthID12 + "/../..",
	}
	for _, call := range containerCalls {
		for _, id := range hostile {
			changed, err := call.run(c, id)
			if !errors.Is(err, ErrBadName) || changed {
				t.Errorf("%s %q: changed=%v err=%v", call.name, id, changed, err)
			}
		}
	}
	if n.Load() != 0 {
		t.Fatalf("%d requests reached the socket", n.Load())
	}
}

func TestContainerActionRequestShape(t *testing.T) {
	for _, call := range containerCalls {
		for _, id := range []string{synthID12, synthID64} {
			c, last, _ := recordingDocker(t, 204, "")
			changed, err := call.run(c, id)
			if err != nil || !changed {
				t.Fatalf("%s: changed=%v err=%v", call.name, changed, err)
			}
			// No query (so no timeout parameter) and no body.
			if last.method != "POST" || last.path != "/v1.41/containers/"+id+call.path || last.rawQuery != "" ||
				last.body != "" || last.ctype != "" {
				t.Fatalf("%s: request = %+v", call.name, *last)
			}
		}
	}
}

func TestContainerActionStatusMapping(t *testing.T) {
	tests := []struct {
		status  int
		changed bool
		want    error // nil means success
	}{
		{204, true, nil},
		{404, false, ErrObjectNotFound},
		{400, false, ErrObjectNotFound}, // ambiguous prefix: no single container
		{409, false, ErrConflict},       // paused, restarting, ...
		{403, false, ErrConflict},
		{500, false, ErrDaemon},
		{503, false, ErrDaemon},
		{200, false, ErrDaemon}, // not what Docker documents: no guessing
		{301, false, ErrDaemon},
	}
	for _, call := range containerCalls {
		for _, tc := range tests {
			c, _, _ := recordingDocker(t, tc.status, "Error: secret /home/user/.colima/synthetic")
			changed, err := call.run(c, synthID12)
			if tc.want == nil {
				if err != nil || changed != tc.changed {
					t.Errorf("%s %d: changed=%v err=%v", call.name, tc.status, changed, err)
				}
				continue
			}
			if !errors.Is(err, tc.want) || changed {
				t.Errorf("%s %d: changed=%v err=%v, want %v", call.name, tc.status, changed, err, tc.want)
				continue
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/home") {
				t.Errorf("%s %d: error carries daemon text: %q", call.name, tc.status, err)
			}
		}
	}
}

func TestContainerAction304IsUnchangedForStartAndStopOnly(t *testing.T) {
	for _, call := range containerCalls {
		c, _, _ := recordingDocker(t, 304, "")
		changed, err := call.run(c, synthID12)
		if call.name == "restart" {
			// Docker never answers 304 to a restart: it is not a success here.
			if !errors.Is(err, ErrDaemon) || changed {
				t.Errorf("restart 304: changed=%v err=%v", changed, err)
			}
			continue
		}
		if err != nil || changed {
			t.Errorf("%s 304: changed=%v err=%v", call.name, changed, err)
		}
	}
}

func TestContainerActionUnreachableDocker(t *testing.T) {
	c := newHTTPClient(t.TempDir() + "/missing.sock")
	for _, call := range containerCalls {
		changed, err := call.run(c, synthID12)
		if !errors.Is(err, ErrDockerUnavailable) || changed {
			t.Errorf("%s: changed=%v err=%v", call.name, changed, err)
		}
	}
}
