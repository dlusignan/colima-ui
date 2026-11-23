package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dlusignan/colima-ui/internal/store"
)

type fakeMetrics struct {
	pts   []store.Point
	err   error
	calls int
	q     store.MetricQuery
}

func (f *fakeMetrics) QueryMetrics(_ context.Context, q store.MetricQuery) ([]store.Point, error) {
	f.calls++
	f.q = q
	return f.pts, f.err
}

func metricsServer(r store.MetricReader, g ProfileGate) http.Handler {
	return NewHandler(testPort, testToken, NewAPI(WithMetrics(r, g)), nil)
}

const metricsPath = "/api/profiles/default/metrics"

func TestMetricsNeedToken(t *testing.T) {
	f, g := &fakeMetrics{}, &fakeGate{status: "running"}
	req := httptest.NewRequest("GET", metricsPath, nil)
	req.Host = goodHost
	rec := httptest.NewRecorder()
	metricsServer(f, g).ServeHTTP(rec, req)
	wantErr(t, rec, 401, "unauthorized")
	if f.calls != 0 || len(g.names) != 0 {
		t.Fatal("handler ran without token")
	}
}

func TestMetricsDefaults(t *testing.T) {
	f := &fakeMetrics{}
	before := time.Now()
	rec := call(t, metricsServer(f, &fakeGate{status: "stopped"}), "GET", metricsPath, "")
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if f.q.Profile != "default" || f.q.Scope != "vm" || f.q.MaxPoints != 300 {
		t.Fatalf("query %+v", f.q)
	}
	if f.q.To.Before(before.Add(-2*time.Second)) || f.q.To.After(time.Now().Add(time.Second)) {
		t.Fatalf("to %v not now", f.q.To)
	}
	if f.q.To.Sub(f.q.From) != time.Hour {
		t.Fatalf("range %v", f.q.To.Sub(f.q.From))
	}
	var got struct {
		Profile, Scope, From, To string
		Points                   []json.RawMessage
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Profile != "default" || got.Scope != "vm" || got.Points == nil || len(got.Points) != 0 {
		t.Fatalf("%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"points":[]`) {
		t.Fatalf("empty points must be []: %s", rec.Body.String())
	}
}

func TestMetricsPointsMapping(t *testing.T) {
	cpu, mem, disk := 12.5, int64(2147483648), int64(21474836480)
	f := &fakeMetrics{pts: []store.Point{
		{TakenAt: time.Date(2026, 11, 23, 1, 10, 0, 0, time.FixedZone("x", 3600)), CPUPercent: &cpu, MemoryBytes: &mem, DiskBytes: &disk},
		{TakenAt: time.Date(2026, 11, 23, 0, 10, 30, 0, time.UTC), CPUPercent: &cpu, MemoryBytes: &mem},
		{TakenAt: time.Date(2026, 11, 23, 0, 11, 0, 0, time.UTC)},
	}}
	target := metricsPath + "?scope=abc123&from=2026-11-23T01:10:00%2B01:00&to=2026-11-23T02:10:00%2B01:00&maxPoints=50"
	rec := call(t, metricsServer(f, &fakeGate{}), "GET", target, "")
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	want := `{"profile":"default","scope":"abc123","from":"2026-11-23T00:10:00Z","to":"2026-11-23T01:10:00Z","points":[` +
		`{"takenAt":"2026-11-23T00:10:00Z","cpuPercent":12.5,"memoryBytes":2147483648,"diskBytes":21474836480},` +
		`{"takenAt":"2026-11-23T00:10:30Z","cpuPercent":12.5,"memoryBytes":2147483648,"diskBytes":null},` +
		`{"takenAt":"2026-11-23T00:11:00Z","cpuPercent":null,"memoryBytes":null,"diskBytes":null}]}`
	if strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("got  %s\nwant %s", rec.Body.String(), want)
	}
	if f.q.Scope != "abc123" || f.q.MaxPoints != 50 ||
		!f.q.From.Equal(time.Date(2026, 11, 23, 0, 10, 0, 0, time.UTC)) ||
		!f.q.To.Equal(time.Date(2026, 11, 23, 1, 10, 0, 0, time.UTC)) {
		t.Fatalf("query %+v", f.q)
	}
}

func TestMetricsMaxPointsClamp(t *testing.T) {
	for q, want := range map[string]int{
		"?maxPoints=1": 1, "?maxPoints=2000": 2000, "?maxPoints=2001": 2000,
		"?maxPoints=99999999999999999999": 2000, "?maxPoints=%2B7": 7,
	} {
		f := &fakeMetrics{}
		rec := call(t, metricsServer(f, &fakeGate{}), "GET", metricsPath+q, "")
		if rec.Code != 200 || f.q.MaxPoints != want {
			t.Fatalf("%q: status %d maxPoints %d, want %d", q, rec.Code, f.q.MaxPoints, want)
		}
	}
}

func TestMetricsInvalidParameters(t *testing.T) {
	cases := []struct{ query, param string }{
		{"maxPoints=0", "maxPoints"},
		{"maxPoints=-1", "maxPoints"},
		{"maxPoints=-99999999999999999999", "maxPoints"},
		{"maxPoints=", "maxPoints"},
		{"maxPoints=NaN", "maxPoints"},
		{"maxPoints=1.5", "maxPoints"},
		{"maxPoints=1e3", "maxPoints"},
		{"maxPoints=abc", "maxPoints"},
		{"maxPoints=1&maxPoints=2", "maxPoints"},
		{"from=NaN", "from"},
		{"from=yesterday", "from"},
		{"from=2026-11-23", "from"},
		{"from=2026-11-23T00:10:00", "from"},
		{"from=1700000000", "from"},
		{"from=", "from"},
		{"from=0001-01-01T00:00:00Z", "from"},
		{"from=1969-12-31T00:00:00Z", "from"},
		{"to=2026-13-45T00:00:00Z", "to"},
		{"to=", "to"},
		{"to=2026-11-23T00:00:00Z&to=2026-11-23T01:00:00Z", "to"},
		{"from=2026-11-23T02:00:00Z&from=2026-11-23T01:00:00Z", "from"},
		{"from=2026-11-23T02:00:00Z&to=2026-11-23T01:00:00Z", "from"},
		{"from=2999-01-01T00:00:00Z", "from"},
		{"scope=", "scope"},
		{"scope=a%20b", "scope"},
		{"scope=a%2Fb", "scope"},
		{"scope=%27%3B%20DROP%20TABLE%20x%3B--", "scope"},
		{"scope=a%00b", "scope"},
		{"scope=" + strings.Repeat("a", 129), "scope"},
		{"scope=vm&scope=abc", "scope"},
	}
	for _, c := range cases {
		f := &fakeMetrics{}
		rec := call(t, metricsServer(f, &fakeGate{}), "GET", metricsPath+"?"+c.query, "")
		w := wantErr(t, rec, 400, "invalid_range")
		if w.Error.Details["parameter"] != c.param || f.calls != 0 {
			t.Fatalf("%q: details %v calls %d", c.query, w.Error.Details, f.calls)
		}
		if strings.Contains(rec.Body.String(), "DROP") || strings.Contains(rec.Body.String(), "aaaa") {
			t.Fatalf("%q: input echoed: %s", c.query, rec.Body.String())
		}
	}
}

func TestMetricsAcceptsLongScopeAndOffsets(t *testing.T) {
	f := &fakeMetrics{}
	v := url.Values{}
	v.Set("scope", strings.Repeat("a", 128))
	v.Set("from", "2026-11-23T00:00:00.5Z")
	v.Set("to", "2026-11-23T00:00:00.5Z")
	rec := call(t, metricsServer(f, &fakeGate{}), "GET", metricsPath+"?"+v.Encode(), "")
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
}

func TestMetricsGate(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{ErrProfileNotFound, 404, "profile_not_found"},
		{ErrInvalidProfileName, 404, "profile_not_found"},
		{errors.New("boom"), 502, "colima_failed"},
	}
	for _, c := range cases {
		f := &fakeMetrics{}
		// Bad parameters must not mask the 404.
		rec := call(t, metricsServer(f, &fakeGate{err: c.err}), "GET", "/api/profiles/nope/metrics?maxPoints=0", "")
		wantErr(t, rec, c.status, c.code)
		if f.calls != 0 {
			t.Fatal("store reached despite gate failure")
		}
	}
}

func TestMetricsStoreErrors(t *testing.T) {
	rec := call(t, metricsServer(&fakeMetrics{err: store.ErrClosed}, &fakeGate{}), "GET", metricsPath, "")
	wantErr(t, rec, 503, "shutting_down")

	rec = call(t, metricsServer(&fakeMetrics{err: store.ErrInvalidQuery}, &fakeGate{}), "GET", metricsPath, "")
	wantErr(t, rec, 400, "invalid_range")

	rec = call(t, metricsServer(&fakeMetrics{err: errors.New("secret /home/user/db")}, &fakeGate{}), "GET", metricsPath, "")
	wantErr(t, rec, 500, "internal_error")
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("error text leaked: %s", rec.Body.String())
	}
}
