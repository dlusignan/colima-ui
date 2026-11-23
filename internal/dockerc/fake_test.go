package dockerc

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// startFakeDocker serves h on a Unix socket in a short temp dir and returns
// the socket path.
func startFakeDocker(t *testing.T, h http.Handler) string {
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
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return sock
}

const listJSON = `[
 {"Id":"aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111","Names":["/synthetic-web"],"Image":"synthetic/web:1","State":"running"},
 {"Id":"bbbbbbbbbbbb2222","Names":["/synthetic-db"],"Image":"synthetic/db:2","State":"exited"}
]`

const statsV2JSON = `{
 "cpu_stats":{"cpu_usage":{"total_usage":2000},"system_cpu_usage":10000,"online_cpus":4},
 "precpu_stats":{"cpu_usage":{"total_usage":0},"system_cpu_usage":0},
 "memory_stats":{"usage":1000,"limit":4096,"stats":{"inactive_file":300,"file":500}}
}`

const dfJSON = `{
 "LayersSize": 900,
 "Images":[{"Size":500},{"Size":400}],
 "Containers":[{"SizeRw":10},{"SizeRw":-1}],
 "Volumes":[{"UsageData":{"Size":70}},{"UsageData":{"Size":-1}},{"UsageData":null}],
 "BuildCache":[{"Size":5},{"Size":6}]
}`

func writeFile(p string) error { return os.WriteFile(p, []byte("x"), 0o600) }

// closeSockListener makes the socket refuse connections while leaving the
// file in place, like a crashed daemon.
func closeSockListener(t *testing.T, sock string) {
	t.Helper()
	os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if ul, ok := l.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	l.Close()
}
