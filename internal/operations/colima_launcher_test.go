package operations

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/dlusignan/colima-ui/internal/colima"
)

// argsStreamer is a colima.Streamer fake: no binary is ever run.
type argsStreamer struct{ args [][]string }

func (s *argsStreamer) Stream(_ context.Context, args []string) (colima.Process, error) {
	s.args = append(s.args, args)
	return newFakeProc(), nil
}

func TestColimaLauncherMapsActionsToAdapterCalls(t *testing.T) {
	s := &argsStreamer{}
	l := ColimaLauncher{S: s}
	for _, a := range []Action{ActionStart, ActionStop, ActionRestart} {
		if _, err := l.Launch(context.Background(), Request{Action: a, Profile: "dev"}); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	want := [][]string{
		{"start", "-p", "dev"},
		{"stop", "-p", "dev"},
		{"restart", "-p", "dev"},
	}
	if !reflect.DeepEqual(s.args, want) {
		t.Fatalf("args = %v, want %v", s.args, want)
	}
}

func TestColimaLauncherRejectsUnknownActionAndBadProfile(t *testing.T) {
	s := &argsStreamer{}
	l := ColimaLauncher{S: s}
	if _, err := l.Launch(context.Background(), Request{Action: "delete", Profile: "dev"}); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("err = %v", err)
	}
	if _, err := l.Launch(context.Background(), Request{Action: ActionStart, Profile: "../x"}); err == nil {
		t.Fatal("want invalid profile error")
	}
	if len(s.args) != 0 {
		t.Fatalf("streamer called: %v", s.args)
	}
}
