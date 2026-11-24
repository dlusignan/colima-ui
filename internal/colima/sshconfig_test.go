package colima

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const sshFixture = `# synthetic fixture
Host colima-synthetic
  HostName 127.0.0.1
  Port 60022
  User synthetic-user
  ForwardAgent yes
  IdentityFile "/home/user/.colima/_lima/_config/user"
  IdentityFile /home/user/.ssh/id_synthetic
  StrictHostKeyChecking=no

  # trailing comment
  ProxyCommand = "ssh -W %h:%p jump"
`

func TestReadSSHConfigParsesFixture(t *testing.T) {
	f := &fakeRunner{result: ok([]byte(sshFixture))}
	got, err := ReadSSHConfig(context.Background(), f, "synthetic-a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls, [][]string{{"ssh-config", "-p", "synthetic-a"}}) {
		t.Errorf("args = %v", f.calls)
	}
	if got.Raw != sshFixture {
		t.Error("raw not kept")
	}
	if got.Host != "colima-synthetic" || got.HostName != "127.0.0.1" || got.Port != "60022" ||
		got.User != "synthetic-user" || got.IdentityFile != "/home/user/.colima/_lima/_config/user" {
		t.Errorf("typed fields: %+v", got)
	}
	wantKeys := []string{"Host", "HostName", "Port", "User", "ForwardAgent", "IdentityFile", "IdentityFile", "StrictHostKeyChecking", "ProxyCommand"}
	var keys []string
	for _, e := range got.Entries {
		keys = append(keys, e.Key)
	}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("keys = %v", keys)
	}
	last := got.Entries[len(got.Entries)-1]
	if last.Value != "ssh -W %h:%p jump" || got.Entries[7].Value != "no" {
		t.Errorf("values: %+v", got.Entries)
	}
}

func TestReadSSHConfigRejectsBadOutput(t *testing.T) {
	for name, out := range map[string]string{
		"empty":     "",
		"blank":     "  \n\n",
		"comments":  "# only\n# comments\n",
		"garbage":   "\x00\x01 not ssh",
		"no host":   "HostName 127.0.0.1\n",
		"no value":  "Host a\nPort\n",
		"empty val": "Host a\nPort =\n",
		"long line": "Host " + strings.Repeat("a", maxSSHConfigLine),
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeRunner{result: ok([]byte(out))}
			_, err := ReadSSHConfig(context.Background(), f, "synthetic-a")
			var pe *ParseError
			if !errors.As(err, &pe) || pe.Op != "ssh-config" {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), "not ssh") {
				t.Error("raw output leaked into error text")
			}
		})
	}
}

func TestReadSSHConfigSecondHostBlockStaysOutOfTypedFields(t *testing.T) {
	f := &fakeRunner{result: ok([]byte("Host a\nPort 1\nHost b\nPort 2\n"))}
	got, err := ReadSSHConfig(context.Background(), f, "synthetic-a")
	if err != nil || got.Host != "a" || got.Port != "1" || len(got.Entries) != 3 {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestReadSSHConfigHostileProfileNeverRuns(t *testing.T) {
	for _, name := range []string{"", "-p", "../x", "a b", "a;b", "$(x)", "a\nb"} {
		f := &fakeRunner{}
		if _, err := ReadSSHConfig(context.Background(), f, name); err == nil {
			t.Errorf("%q accepted", name)
		}
		if len(f.calls) != 0 {
			t.Errorf("%q reached the runner", name)
		}
	}
}

func TestReadSSHConfigExitError(t *testing.T) {
	f := &fakeRunner{result: Result{ExitCode: 1, Stderr: []byte("/home/user/secret")}}
	_, err := ReadSSHConfig(context.Background(), f, "synthetic-a")
	var ee *ExitError
	if !errors.As(err, &ee) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}
}
