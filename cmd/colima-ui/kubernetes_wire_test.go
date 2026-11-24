package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dlusignan/colima-ui/internal/colima"
)

const k8sStatus = `{"display_name":"default","runtime":"docker","kubernetes":true}`
const noK8sStatus = `{"display_name":"default","runtime":"docker","kubernetes":false}`

func TestAppKubernetesFlow(t *testing.T) {
	r := &fakeRunner{list: listJSON, status: k8sStatus, script: []string{"working"}, exit: 0}
	a := testApp(t, r)
	ts := serve(t, a)

	// Probe route.
	if code, body := ts.do(t, "GET", "/api/profiles/default/kubernetes", ""); code != 200 || strings.TrimSpace(body) != `{"enabled":true}` {
		t.Fatalf("probe: %d %s", code, body)
	}
	if code, _ := ts.do(t, "GET", "/api/profiles/dev/kubernetes", ""); code != 409 {
		t.Fatalf("probe stopped: %d", code)
	}

	// Refusals start nothing.
	for _, c := range []struct {
		path, body string
		want       int
	}{
		{"/api/profiles/ghost/kubernetes/start", "", 404},
		{"/api/profiles/-rf/kubernetes/start", "", 404},
		{"/api/profiles/dev/kubernetes/start", "", 409},
		{"/api/profiles/default/kubernetes/reset", "", 422},
		{"/api/profiles/default/kubernetes/delete", `{"confirm":"reset-kubernetes"}`, 422},
		{"/api/profiles/default/kubernetes/delete", `{"confirm":`, 400},
	} {
		if code, body := ts.do(t, "POST", c.path, c.body); code != c.want {
			t.Fatalf("%s %q: %d %s", c.path, c.body, code, body)
		}
	}
	if r.streamCount() != 0 {
		t.Fatalf("a refused request started colima: %v", r.streams)
	}

	// A reset runs the fixed vector and writes a row naming the verb.
	if code, body := ts.do(t, "POST", "/api/profiles/default/kubernetes/reset", `{"confirm":"reset-kubernetes"}`); code != 202 {
		t.Fatalf("reset: %d %s", code, body)
	}
	eventually(t, "reset row", func() bool { return len(ts.actions(t, "default")) == 1 })
	rows := ts.actions(t, "default")
	if rows[0].Action != "kubernetes-reset" || rows[0].Outcome != "success" || rows[0].Detail == nil || *rows[0].Detail != "kubernetes reset" {
		t.Fatalf("row: %#v", rows[0])
	}
	if got := strings.Join(r.streams[0], " "); got != "kubernetes reset -p default" {
		t.Fatalf("argv = %q", got)
	}
}

func TestAppKubernetesDisabledAndRecheck(t *testing.T) {
	r := &fakeRunner{list: listJSON, status: noK8sStatus}
	a := testApp(t, r)
	ts := serve(t, a)
	for _, verb := range []string{"start", "stop"} {
		code, body := ts.do(t, "POST", "/api/profiles/default/kubernetes/"+verb, "")
		if code != 409 || !strings.Contains(body, "kubernetes_not_enabled") {
			t.Fatalf("%s: %d %s", verb, code, body)
		}
	}
	if r.streamCount() != 0 {
		t.Fatal("colima started for a profile without kubernetes")
	}

	// The in-slot recheck: the launcher sees a stopped VM even if the route
	// did not.
	r2 := &fakeRunner{list: listJSON, statusErr: &colima.ExitError{ExitCode: 1}}
	a2 := testApp(t, r2)
	ts2 := serve(t, a2)
	if code, body := ts2.do(t, "POST", "/api/profiles/default/kubernetes/stop", ""); code != 409 || !strings.Contains(body, "invalid_state") {
		t.Fatalf("refused status: %d %s", code, body)
	}
	if r2.streamCount() != 0 {
		t.Fatal("colima started although status was refused")
	}
}

func TestAppKubernetesCancelRecordsPartialWarning(t *testing.T) {
	r := &fakeRunner{list: listJSON, status: k8sStatus} // no script: the process waits to be cancelled
	a := testApp(t, r)
	ts := serve(t, a)

	code, body := ts.do(t, "POST", "/api/profiles/default/kubernetes/delete", `{"confirm":"delete-kubernetes"}`)
	if code != 202 {
		t.Fatalf("delete: %d %s", code, body)
	}
	var ref struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal([]byte(body), &ref); err != nil || ref.JobID == "" {
		t.Fatalf("job ref: %v %s", err, body)
	}
	// While it runs, the slot is held.
	if code, body := ts.do(t, "POST", "/api/profiles/default/kubernetes/stop", ""); code != 409 || !strings.Contains(body, "profile_busy") {
		t.Fatalf("busy: %d %s", code, body)
	}
	if code, body := ts.do(t, "POST", "/api/jobs/"+ref.JobID+"/cancel", ""); code != 200 || !strings.Contains(body, `"cancelled"`) {
		t.Fatalf("cancel: %d %s", code, body)
	}
	eventually(t, "cancelled row", func() bool { return len(ts.actions(t, "default")) == 1 })
	row := ts.actions(t, "default")[0]
	want := "kubernetes delete: cancelled before it finished; the cluster may be partly changed"
	if row.Action != "kubernetes-delete" || row.Outcome != "cancelled" || row.Detail == nil || *row.Detail != want {
		t.Fatalf("row: %#v", row)
	}
}
