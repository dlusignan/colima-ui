package colima

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestKubernetesArgv(t *testing.T) {
	cases := []struct {
		name string
		call func(Streamer, string) (Process, error)
		want string
	}{
		{"start", func(s Streamer, p string) (Process, error) { return KubernetesStart(context.Background(), s, p) }, "kubernetes start -p synthetic-a"},
		{"stop", func(s Streamer, p string) (Process, error) { return KubernetesStop(context.Background(), s, p) }, "kubernetes stop -p synthetic-a"},
		{"reset", func(s Streamer, p string) (Process, error) { return KubernetesReset(context.Background(), s, p) }, "kubernetes reset -p synthetic-a"},
		{"delete", func(s Streamer, p string) (Process, error) { return KubernetesDelete(context.Background(), s, p) }, "kubernetes delete -p synthetic-a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeStreamer{proc: newFakeProcess(nil, Exit{}, nil)}
			if _, err := c.call(f, "synthetic-a"); err != nil {
				t.Fatal(err)
			}
			if got := f.argv(t); got != c.want {
				t.Fatalf("argv = %s, want %s", got, c.want)
			}
		})
	}
}

func TestKubernetesHostileNamesNeverStartAProcess(t *testing.T) {
	bad := []string{"", "-rf", "--edit", "a b", "a;b", "../x", strings.Repeat("a", 65), "a\n", "$(id)"}
	calls := []func(Streamer, string) (Process, error){
		func(s Streamer, p string) (Process, error) { return KubernetesStart(context.Background(), s, p) },
		func(s Streamer, p string) (Process, error) { return KubernetesStop(context.Background(), s, p) },
		func(s Streamer, p string) (Process, error) { return KubernetesReset(context.Background(), s, p) },
		func(s Streamer, p string) (Process, error) { return KubernetesDelete(context.Background(), s, p) },
	}
	for _, name := range bad {
		for i, call := range calls {
			f := &fakeStreamer{proc: newFakeProcess(nil, Exit{}, nil)}
			var ipe *InvalidProfileError
			if _, err := call(f, name); !errors.As(err, &ipe) {
				t.Errorf("verb %d name %q: got %v", i, name, err)
			}
			if len(f.calls) != 0 {
				t.Errorf("verb %d name %q: process started", i, name)
			}
		}
	}
}
