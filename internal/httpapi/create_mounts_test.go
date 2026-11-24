package httpapi

import (
	"reflect"
	"strings"
	"testing"
)

func TestCreateAcceptsMountsAndEnv(t *testing.T) {
	f := &fakeCreator{id: "1"}
	body := `{"name":"dev","mounts":[{"path":"/srv/a","writable":true},{"path":"/srv/b"},{"writable":false,"path":"/srv/c"}],
	"env":{"B":"2","A":"a=b;c\nd","a":""}}`
	if rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", body); rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	p := f.calls[0]
	if want := (MountList{{"/srv/a", true}, {"/srv/b", false}, {"/srv/c", false}}); !reflect.DeepEqual(p.Mounts, want) {
		t.Fatalf("mounts %+v", p.Mounts)
	}
	// Keys that differ only in case are different variables, in body order.
	if want := (EnvVars{{"B", "2"}, {"A", "a=b;c\nd"}, {"a", ""}}); !reflect.DeepEqual(p.Env, want) {
		t.Fatalf("env %+v", p.Env)
	}
}

func TestCreateMountsEnvNullsAndEmptiesCountAsAbsent(t *testing.T) {
	for _, body := range []string{`{"name":"dev","mounts":null,"env":null}`, `{"name":"dev","mounts":[],"env":{}}`} {
		f := &fakeCreator{id: "1"}
		if rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", body); rec.Code != 202 {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
		if p := f.calls[0]; len(p.Mounts) != 0 || len(p.Env) != 0 {
			t.Fatalf("%s: %+v", body, p)
		}
	}
}

func TestCreateMountsEnvWrongShapesAre400(t *testing.T) {
	bodies := map[string]string{
		"mounts string":         `{"name":"dev","mounts":"/srv/a"}`,
		"mounts object":         `{"name":"dev","mounts":{"path":"/srv/a"}}`,
		"mounts string elem":    `{"name":"dev","mounts":["/srv/a"]}`,
		"mounts null elem":      `{"name":"dev","mounts":[null]}`,
		"mounts nested array":   `{"name":"dev","mounts":[["/srv/a"]]}`,
		"mount unknown key":     `{"name":"dev","mounts":[{"path":"/a","readonly":true}]}`,
		"mount case Path":       `{"name":"dev","mounts":[{"Path":"/a"}]}`,
		"mount case PATH":       `{"name":"dev","mounts":[{"PATH":"/a"}]}`,
		"mount case Writable":   `{"name":"dev","mounts":[{"path":"/a","Writable":true}]}`,
		"mount repeated path":   `{"name":"dev","mounts":[{"path":"/a","path":"/b"}]}`,
		"mount escaped repeat":  `{"name":"dev","mounts":[{"path":"/a","path":"/b"}]}`,
		"mount repeated writ":   `{"name":"dev","mounts":[{"path":"/a","writable":true,"writable":false}]}`,
		"mount path number":     `{"name":"dev","mounts":[{"path":1}]}`,
		"mount path null":       `{"name":"dev","mounts":[{"path":null}]}`,
		"mount path array":      `{"name":"dev","mounts":[{"path":["/a"]}]}`,
		"mount writable string": `{"name":"dev","mounts":[{"path":"/a","writable":"true"}]}`,
		"mount writable number": `{"name":"dev","mounts":[{"path":"/a","writable":1}]}`,
		"mount writable null":   `{"name":"dev","mounts":[{"path":"/a","writable":null}]}`,
		"repeated mounts key":   `{"name":"dev","mounts":[],"mounts":[]}`,
		"case Mounts":           `{"name":"dev","Mounts":[]}`,
		"case ENV":              `{"name":"dev","ENV":{}}`,
		"environment":           `{"name":"dev","environment":{}}`,
		"env array":             `{"name":"dev","env":["A=1"]}`,
		"env array of objects":  `{"name":"dev","env":[{"key":"A","value":"1"}]}`,
		"env string":            `{"name":"dev","env":"A=1"}`,
		"env number value":      `{"name":"dev","env":{"A":1}}`,
		"env bool value":        `{"name":"dev","env":{"A":true}}`,
		"env null value":        `{"name":"dev","env":{"A":null}}`,
		"env array value":       `{"name":"dev","env":{"A":["1"]}}`,
		"env object value":      `{"name":"dev","env":{"A":{"x":"1"}}}`,
		"env repeated key":      `{"name":"dev","env":{"A":"1","A":"2"}}`,
		"env escaped repeat":    `{"name":"dev","env":{"A":"1","A":"2"}}`,
		"repeated env key":      `{"name":"dev","env":{},"env":{}}`,
		"unterminated env":      `{"name":"dev","env":{"A":"1"`,
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

// The largest legitimate body fits under the cap; one byte over is a 400.
func TestCreateBodyFitsMaxMountsAndEnv(t *testing.T) {
	var mounts, env []string
	for i := 0; i < 16; i++ {
		mounts = append(mounts, `{"path":"/`+strings.Repeat("a", 4095)+`","writable":true}`)
	}
	for i := 0; i < 32; i++ {
		env = append(env, `"`+strings.Repeat("K", 126)+string(rune('A'+i/26))+string(rune('A'+i%26))+`":"`+strings.Repeat("v", 4096)+`"`)
	}
	body := `{"name":"dev","mounts":[` + strings.Join(mounts, ",") + `],"env":{` + strings.Join(env, ",") + `}}`
	if int64(len(body)) >= createBodyLimit {
		t.Fatalf("legitimate maximum is %d bytes, cap %d", len(body), createBodyLimit)
	}
	f := &fakeCreator{id: "1"}
	if rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", body); rec.Code != 202 {
		t.Fatalf("%d %.200s", rec.Code, rec.Body)
	}
	if len(f.calls[0].Mounts) != 16 {
		t.Fatalf("mounts %d", len(f.calls[0].Mounts))
	}
}

// Counts over the caps still decode and reach the creator, which owns the caps.
func TestCreateHugeMountsAndEnvReachTheValidator(t *testing.T) {
	var mounts, env []string
	for i := 0; i < 500; i++ {
		mounts = append(mounts, `{"path":"/a"}`)
		env = append(env, `"K`+strings.Repeat("x", i%7)+string(rune('a'+i%26))+strings.Repeat("y", i/26)+`":"v"`)
	}
	f := &fakeCreator{id: "1"}
	body := `{"name":"dev","mounts":[` + strings.Join(mounts, ",") + `],"env":{` + strings.Join(env, ",") + `}}`
	if rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", body); rec.Code != 202 {
		t.Fatalf("%d %.200s", rec.Code, rec.Body)
	}
	// Decoding stops one past the caps; the validator answers "too many".
	if p := f.calls[0]; len(p.Mounts) != decodeMountsMax || len(p.Env) != decodeEnvMax {
		t.Fatalf("mounts %d env %d", len(p.Mounts), len(p.Env))
	}
}

// Past the cap an element or entry is only checked for JSON syntax.
func TestCreateElementsPastTheCapAreOnlySyntaxChecked(t *testing.T) {
	var mounts, env []string
	for i := 0; i < 17; i++ {
		mounts = append(mounts, `{"path":"/a"}`)
	}
	for i := 0; i < 33; i++ {
		env = append(env, `"K`+strings.Repeat("x", i)+`":"v"`)
	}
	f := &fakeCreator{id: "1"}
	ok := `{"name":"dev","mounts":[` + strings.Join(mounts, ",") + `,{"nope":[1,{"a":[]}]},"str",null,[[]]],"env":{` +
		strings.Join(env, ",") + `,"X":1}}`
	if rec := cfgReq(t, createAPI(f), "POST", "/api/profiles", ok); rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]string{
		"broken json past the mounts cap": `{"name":"dev","mounts":[` + strings.Join(mounts, ",") + `,{"a":}]}`,
		"unterminated past the cap":       `{"name":"dev","mounts":[` + strings.Join(mounts, ",") + `,[1,2}`,
	} {
		if rec := cfgReq(t, createAPI(&fakeCreator{id: "1"}), "POST", "/api/profiles", body); rec.Code != 400 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}
