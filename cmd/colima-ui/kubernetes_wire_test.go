package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The cluster's own state never decides: k8son has Kubernetes enabled in its
// configuration, k8soff has it disabled, default has no configuration file.
const k8sList = `{"name":"default","status":"Running","runtime":"docker"}
{"name":"k8son","status":"Running","runtime":"docker"}
{"name":"k8soff","status":"Running","runtime":"docker"}
{"name":"k8sbad","status":"Running","runtime":"docker"}
{"name":"k8sstopped","status":"Stopped","runtime":"docker"}
`

func TestAppKubernetesFlow(t *testing.T) {
	r := &fakeRunner{list: k8sList, script: []string{"working"}, exit: 0}
	a := testApp(t, r)
	ts := serve(t, a)

	// The probe route reads the configured flag.
	for name, want := range map[string]string{"k8son": `{"enabled":true}`, "k8soff": `{"enabled":false}`, "default": `{"enabled":false}`} {
		if code, body := ts.do(t, "GET", "/api/profiles/"+name+"/kubernetes", ""); code != 200 || strings.TrimSpace(body) != want {
			t.Fatalf("probe %s: %d %s", name, code, body)
		}
	}
	if code, _ := ts.do(t, "GET", "/api/profiles/k8sstopped/kubernetes", ""); code != 409 {
		t.Fatalf("probe stopped: %d", code)
	}

	// Refusals start nothing.
	for _, c := range []struct {
		path, body string
		want       int
	}{
		{"/api/profiles/ghost/kubernetes/start", "", 404},
		{"/api/profiles/-rf/kubernetes/start", "", 404},
		{"/api/profiles/k8sstopped/kubernetes/start", "", 409},
		{"/api/profiles/k8soff/kubernetes/start", "", 409},
		{"/api/profiles/default/kubernetes/stop", "", 409},
		{"/api/profiles/k8son/kubernetes/reset", "", 422},
		{"/api/profiles/k8son/kubernetes/delete", `{"confirm":"reset-kubernetes"}`, 422},
		{"/api/profiles/k8son/kubernetes/delete", `{"confirm":`, 400},
	} {
		if code, body := ts.do(t, "POST", c.path, c.body); code != c.want {
			t.Fatalf("%s %q: %d %s", c.path, c.body, code, body)
		}
	}
	if r.streamCount() != 0 {
		t.Fatalf("a refused request started colima: %v", r.streams)
	}

	// Start works although no cluster is running: only the configuration counts.
	if code, body := ts.do(t, "POST", "/api/profiles/k8son/kubernetes/start", ""); code != 202 {
		t.Fatalf("start: %d %s", code, body)
	}
	eventually(t, "start row", func() bool { return len(ts.actions(t, "k8son")) == 1 })
	if got := strings.Join(r.streams[0], " "); got != "kubernetes start -p k8son" {
		t.Fatalf("argv = %q", got)
	}

	// A reset runs the fixed vector and writes a row naming the verb.
	if code, body := ts.do(t, "POST", "/api/profiles/k8son/kubernetes/reset", `{"confirm":"reset-kubernetes"}`); code != 202 {
		t.Fatalf("reset: %d %s", code, body)
	}
	eventually(t, "reset row", func() bool { return len(ts.actions(t, "k8son")) == 2 })
	row := ts.actions(t, "k8son")[0]
	if row.Action != "kubernetes-reset" || row.Outcome != "success" || row.Detail == nil || *row.Detail != "kubernetes reset" {
		t.Fatalf("row: %#v", row)
	}
	if got := strings.Join(r.streams[1], " "); got != "kubernetes reset -p k8son" {
		t.Fatalf("argv = %q", got)
	}
}

func TestAppKubernetesCancelRecordsPartialWarning(t *testing.T) {
	r := &fakeRunner{list: k8sList} // no script: the process waits to be cancelled
	a := testApp(t, r)
	ts := serve(t, a)

	code, body := ts.do(t, "POST", "/api/profiles/k8son/kubernetes/delete", `{"confirm":"delete-kubernetes"}`)
	if code != 202 {
		t.Fatalf("delete: %d %s", code, body)
	}
	var ref struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal([]byte(body), &ref); err != nil || ref.JobID == "" {
		t.Fatalf("job ref: %v %s", err, body)
	}
	if code, body := ts.do(t, "POST", "/api/profiles/k8son/kubernetes/stop", ""); code != 409 || !strings.Contains(body, "profile_busy") {
		t.Fatalf("busy: %d %s", code, body)
	}
	if code, body := ts.do(t, "POST", "/api/jobs/"+ref.JobID+"/cancel", ""); code != 200 || !strings.Contains(body, `"cancelled"`) {
		t.Fatalf("cancel: %d %s", code, body)
	}
	eventually(t, "cancelled row", func() bool { return len(ts.actions(t, "k8son")) == 1 })
	row := ts.actions(t, "k8son")[0]
	want := "kubernetes delete: cancelled before it finished; the cluster may be partly changed"
	if row.Action != "kubernetes-delete" || row.Outcome != "cancelled" || row.Detail == nil || *row.Detail != want {
		t.Fatalf("row: %#v", row)
	}
}

func TestAppKubernetesUnreadableConfigFailsClosed(t *testing.T) {
	r := &fakeRunner{list: k8sList, script: []string{"x"}}
	a := testApp(t, r)
	ts := serve(t, a)
	if code, body := ts.do(t, "GET", "/api/profiles/k8sbad/kubernetes", ""); code != 500 {
		t.Fatalf("probe: %d %s", code, body)
	}
	for _, c := range []struct{ verb, body string }{
		{"start", ""}, {"stop", ""},
		{"reset", `{"confirm":"reset-kubernetes"}`}, {"delete", `{"confirm":"delete-kubernetes"}`},
	} {
		if code, body := ts.do(t, "POST", "/api/profiles/k8sbad/kubernetes/"+c.verb, c.body); code != 500 {
			t.Fatalf("%s: %d %s", c.verb, code, body)
		}
	}
	if r.streamCount() != 0 {
		t.Fatalf("colima started: %v", r.streams)
	}
}
