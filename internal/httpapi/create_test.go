package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/dlusignan/colima-ui/internal/operations"
)

type fakeCreator struct {
	mu    sync.Mutex
	calls []NewProfile
	id    string
	err   error
}

func (f *fakeCreator) Create(_ context.Context, p NewProfile) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, p)
	if f.err != nil {
		return "", f.err
	}
	return f.id, nil
}

func createAPI(c ProfileCreator) http.Handler { return NewAPI(WithCreate(c)) }

func TestCreateAcceptsEveryField(t *testing.T) {
	f := &fakeCreator{id: "3.4"}
	body := `{"name":"dev","cpus":4,"memoryGiB":8.5,"diskGiB":100,"rootDiskGiB":20,"runtime":"docker",
	"vmType":"vz","arch":"aarch64","mountType":"virtiofs","kubernetes":true,"kubernetesVersion":"v1.31.2+k3s1"}`
	rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", body)
	if rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	m := decodeMap(t, rec)
	if m["jobId"] != "3.4" || m["profile"] != "dev" || len(m) != 2 {
		t.Fatalf("body %v", m)
	}
	p := f.calls[0]
	if p.Name != "dev" || *p.CPUs != 4 || *p.MemoryGiB != 8.5 || *p.DiskGiB != 100 || *p.RootDiskGiB != 20 ||
		p.Runtime != "docker" || p.VMType != "vz" || p.Arch != "aarch64" || p.MountType != "virtiofs" ||
		!p.Kubernetes || p.KubernetesVersion != "v1.31.2+k3s1" {
		t.Fatalf("decoded %+v", p)
	}
}

func TestCreateNameOnlyLeavesDefaults(t *testing.T) {
	for _, body := range []string{`{"name":"dev"}`, `{"name":"dev","cpus":null,"kubernetes":null,"runtime":null}`} {
		f := &fakeCreator{id: "1"}
		if rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", body); rec.Code != 202 {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
		p := f.calls[0]
		if p.CPUs != nil || p.MemoryGiB != nil || p.DiskGiB != nil || p.RootDiskGiB != nil || p.Runtime != "" ||
			p.VMType != "" || p.Arch != "" || p.MountType != "" || p.Kubernetes || p.KubernetesVersion != "" ||
			!reflect.DeepEqual(p, NewProfile{Name: "dev"}) {
			t.Fatalf("%s: %+v", body, p)
		}
	}
}

func TestCreateBadBodyNeverReachesCreator(t *testing.T) {
	big := `{"name":"dev","kubernetesVersion":"` + strings.Repeat("a", int(createBodyLimit)+1) + `"}`
	bodies := map[string]string{
		"unknown key":       `{"name":"dev","bogus":"x"}`,
		"cpu not cpus":      `{"name":"dev","cpu":2}`,
		"memory not gib":    `{"name":"dev","memory":2}`,
		"upper case key":    `{"Name":"dev"}`,
		"mixed case key":    `{"name":"dev","CPUs":2}`,
		"repeated key":      `{"name":"a","name":"b"}`,
		"string cpus":       `{"name":"dev","cpus":"2"}`,
		"fractional cpus":   `{"name":"dev","cpus":2.5}`,
		"fractional disk":   `{"name":"dev","diskGiB":10.5}`,
		"string memory":     `{"name":"dev","memoryGiB":"4"}`,
		"huge memory":       `{"name":"dev","memoryGiB":1e999}`,
		"overflow int":      `{"name":"dev","cpus":99999999999999999999}`,
		"number name":       `{"name":5}`,
		"array name":        `{"name":["dev"]}`,
		"object name":       `{"name":{"a":1}}`,
		"string kubernetes": `{"name":"dev","kubernetes":"true"}`,
		"number runtime":    `{"name":"dev","runtime":1}`,
		"trailing data":     `{"name":"dev"}{"name":"x"}`,
		"trailing garbage":  `{"name":"dev"} x`,
		"array body":        `[{"name":"dev"}]`,
		"null body":         `null`,
		"string body":       `"dev"`,
		"malformed":         `{"name":`,
		"empty body":        ``,
		"not json":          `name=dev`,
		"over the body cap": big,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			f := &fakeCreator{id: "1"}
			rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", body)
			if rec.Code != 400 || errCode(t, rec) != "bad_request" {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if len(f.calls) != 0 {
				t.Fatal("creator reached with a bad body")
			}
			if strings.Contains(rec.Body.String(), "bogus") {
				t.Fatalf("request text echoed: %s", rec.Body)
			}
		})
	}
}

func TestCreateBodyLimitIsExactlyTheCap(t *testing.T) {
	pad := func(n int) string { // a body of exactly n bytes
		prefix, suffix := `{"name":"dev","kubernetesVersion":"`, `"}`
		return prefix + strings.Repeat("a", n-len(prefix)-len(suffix)) + suffix
	}
	f := &fakeCreator{id: "1"}
	if rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", pad(int(createBodyLimit))); rec.Code != 202 {
		t.Fatalf("at the cap: %d %s", rec.Code, rec.Body)
	}
	if rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", pad(int(createBodyLimit)+1)); rec.Code != 400 {
		t.Fatalf("over the cap: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"validation", &ValidationError{Fields: map[string]string{"name": "is required", "cpus": "must be between 1 and 8"}}, 422, "validation_failed"},
		{"exists", ErrProfileExists, 409, "profile_exists"},
		{"busy", &operations.BusyError{Profile: "dev", JobID: "3.4"}, 409, "profile_busy"},
		{"closed", operations.ErrClosed, 503, "shutting_down"},
		{"id exhausted", operations.ErrIDExhausted, 500, "internal_error"},
		{"exists check", fmt.Errorf("%w: list profiles: exit 1 at /home/user/.colima", ErrExistsCheckFailed), 502, "colima_failed"},
		{"colima", errors.New("list profiles: exit 1 at /home/user/.colima"), 502, "colima_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := cfgReq(t, createAPI(&fakeCreator{err: tc.err}), "POST", "/api/profiles", `{"name":"dev"}`)
			if rec.Code != tc.status || errCode(t, rec) != tc.code {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if strings.Contains(rec.Body.String(), "/home/user") {
				t.Fatalf("error text leaked: %s", rec.Body)
			}
		})
	}
}

func TestCreateValidationFieldsAndBusyJobID(t *testing.T) {
	err := &ValidationError{Fields: map[string]string{"name": "is required", "cpus": "must be between 1 and 8"}}
	rec := cfgReq(t, createAPI(&fakeCreator{err: err}), "POST", "/api/profiles", `{}`)
	fields := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any)
	if len(fields) != 2 || fields["name"] != "is required" || fields["cpus"] == nil {
		t.Fatalf("fields %v", fields)
	}
	rec = cfgReq(t, createAPI(&fakeCreator{err: &operations.BusyError{Profile: "dev", JobID: "3.4"}}), "POST", "/api/profiles", `{"name":"dev"}`)
	if d := decodeMap(t, rec)["error"].(map[string]any)["details"].(map[string]any); d["jobId"] != "3.4" {
		t.Fatalf("details %v", d)
	}
}

func TestCreateRouteShape(t *testing.T) {
	h := createAPI(&fakeCreator{id: "1"})
	for _, m := range []string{"GET", "PUT", "DELETE", "PATCH"} {
		if rec := cfgReq(t, h, m, "/api/profiles", `{"name":"dev"}`); rec.Code != 405 && rec.Code != 404 {
			t.Fatalf("%s: %d", m, rec.Code)
		}
	}
}

func TestCreateAcceptsEveryAdvancedField(t *testing.T) {
	f := &fakeCreator{id: "3.4"}
	body := `{"name":"dev","hostname":"vm-1","dns":["1.1.1.1","2001:db8::1"],
	"dnsHosts":[{"name":"example.com","address":"192.0.2.10"},{"address":"192.0.2.11","name":"b.test"}],
	"sshAgent":true,"vzRosetta":true,"portForwarder":"grpc","mountInotify":true,"networkAddress":true,
	"networkMode":"bridged","networkInterface":"en0","cpuType":"max"}`
	if rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", body); rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	want := NewProfile{Name: "dev", Hostname: "vm-1", DNS: []string{"1.1.1.1", "2001:db8::1"},
		DNSHosts: []DNSHost{{Name: "example.com", Address: "192.0.2.10"}, {Name: "b.test", Address: "192.0.2.11"}},
		SSHAgent: true, VZRosetta: true, PortForwarder: "grpc", MountInotify: true, NetworkAddress: true,
		NetworkMode: "bridged", NetworkInterface: "en0", CPUType: "max"}
	if !reflect.DeepEqual(f.calls[0], want) {
		t.Fatalf("decoded %+v", f.calls[0])
	}
}

func TestCreateAdvancedNullsCountAsAbsent(t *testing.T) {
	f := &fakeCreator{id: "1"}
	body := `{"name":"dev","hostname":null,"dns":null,"dnsHosts":null,"sshAgent":null,"networkMode":null}`
	if rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", body); rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !reflect.DeepEqual(f.calls[0], NewProfile{Name: "dev"}) {
		t.Fatalf("%+v", f.calls[0])
	}
}

func TestCreateAdvancedWrongShapesAre400(t *testing.T) {
	bodies := map[string]string{
		"hostname number":      `{"name":"dev","hostname":5}`,
		"hostname array":       `{"name":"dev","hostname":["a"]}`,
		"dns string":           `{"name":"dev","dns":"1.1.1.1"}`,
		"dns object":           `{"name":"dev","dns":{"a":"1.1.1.1"}}`,
		"dns number element":   `{"name":"dev","dns":[1]}`,
		"dns nested array":     `{"name":"dev","dns":[["1.1.1.1"]]}`,
		"dnsHosts string":      `{"name":"dev","dnsHosts":"a=1.1.1.1"}`,
		"dnsHosts string elem": `{"name":"dev","dnsHosts":["a=1.1.1.1"]}`,
		"dnsHosts object":      `{"name":"dev","dnsHosts":{"name":"a","address":"1.1.1.1"}}`,
		"dnsHosts null elem":   `{"name":"dev","dnsHosts":[null]}`,
		"dnsHosts unknown key": `{"name":"dev","dnsHosts":[{"name":"a","address":"1.1.1.1","x":1}]}`,
		"dnsHosts case key":    `{"name":"dev","dnsHosts":[{"Name":"a","address":"1.1.1.1"}]}`,
		"dnsHosts repeat key":  `{"name":"dev","dnsHosts":[{"name":"a","name":"b","address":"1.1.1.1"}]}`,
		"dnsHosts name num":    `{"name":"dev","dnsHosts":[{"name":1,"address":"1.1.1.1"}]}`,
		"dnsHosts name null":   `{"name":"dev","dnsHosts":[{"name":null,"address":"1.1.1.1"}]}`,
		"dnsHosts addr array":  `{"name":"dev","dnsHosts":[{"name":"a","address":["1.1.1.1"]}]}`,
		"dnsHosts array elem":  `{"name":"dev","dnsHosts":[["a","1.1.1.1"]]}`,
		"sshAgent string":      `{"name":"dev","sshAgent":"true"}`,
		"sshAgent number":      `{"name":"dev","sshAgent":1}`,
		"vzRosetta string":     `{"name":"dev","vzRosetta":"yes"}`,
		"mountInotify array":   `{"name":"dev","mountInotify":[true]}`,
		"networkAddress num":   `{"name":"dev","networkAddress":1}`,
		"portForwarder bool":   `{"name":"dev","portForwarder":true}`,
		"networkMode number":   `{"name":"dev","networkMode":1}`,
		"networkInterface obj": `{"name":"dev","networkInterface":{}}`,
		"cpuType array":        `{"name":"dev","cpuType":["max"]}`,
		"repeated dns":         `{"name":"dev","dns":["1.1.1.1"],"dns":["8.8.8.8"]}`,
		"case Hostname":        `{"name":"dev","Hostname":"x"}`,
		"snake case":           `{"name":"dev","ssh_agent":true}`,
		"flag name":            `{"name":"dev","ssh-agent":true}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			f := &fakeCreator{id: "1"}
			rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", body)
			if rec.Code != 400 || errCode(t, rec) != "bad_request" {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if len(f.calls) != 0 {
				t.Fatal("creator reached with a bad body")
			}
		})
	}
}

func TestCreateHugeArraysReachTheValidatorWithinTheBodyCap(t *testing.T) {
	// The count caps live in the creator; the route only bounds the body.
	f := &fakeCreator{id: "1"}
	body := `{"name":"dev","dns":[` + strings.TrimSuffix(strings.Repeat(`"1.1.1.1",`, 3000), ",") + `]}`
	if rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", body); rec.Code != 202 {
		t.Fatalf("%d", rec.Code)
	}
	if len(f.calls[0].DNS) != 3000 {
		t.Fatalf("dns %d", len(f.calls[0].DNS))
	}
}
