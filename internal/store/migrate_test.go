package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
	"time"
)

func version(t *testing.T, s *Store) int {
	t.Helper()
	v, err := userVersion(context.Background(), s.rdb)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrationsFreshCreatesC4Schema(t *testing.T) {
	s := openTemp(t, newClock(t0), nil)
	if v := version(t, s); v != 1 {
		t.Fatalf("user_version = %d, want 1", v)
	}
	cols := func(table string) []string {
		rows, err := s.rdb.Query("SELECT name FROM pragma_table_info(?) ORDER BY cid", table)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var n string
			_ = rows.Scan(&n)
			out = append(out, n)
		}
		return out
	}
	want := map[string][]string{
		"metric_sample":    {"id", "profile", "taken_at", "scope", "cpu_percent", "memory_bytes", "disk_bytes"},
		"metric_sample_1m": {"profile", "scope", "bucket_start", "cpu_percent", "memory_bytes", "disk_bytes", "samples"},
		"action_log":       {"id", "profile", "started_at", "action", "outcome", "detail"},
	}
	for table, w := range want {
		if got := cols(table); !reflect.DeepEqual(got, w) {
			t.Errorf("%s columns = %v, want %v", table, got, w)
		}
	}
	idx := map[string][]string{
		"metric_sample_lookup": {"profile", "scope", "taken_at"},
		"action_log_recent":    {"profile", "started_at", "id"},
	}
	for name, w := range idx {
		rows, err := s.rdb.Query("SELECT name FROM pragma_index_info(?) ORDER BY seqno", name)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var n string
			_ = rows.Scan(&n)
			got = append(got, n)
		}
		rows.Close()
		if !reflect.DeepEqual(got, w) {
			t.Errorf("index %s = %v, want %v", name, got, w)
		}
	}
	var without int
	if err := s.rdb.QueryRow("SELECT wr FROM pragma_table_list('metric_sample_1m')").Scan(&without); err != nil || without != 1 {
		t.Errorf("metric_sample_1m WITHOUT ROWID = %d, %v", without, err)
	}
}

func TestPragmasApplied(t *testing.T) {
	s := openTemp(t, newClock(t0), nil)
	for name, want := range map[string]string{
		"journal_mode": "wal", "auto_vacuum": "2", "foreign_keys": "1", "busy_timeout": "5000", "synchronous": "1",
	} {
		for label, db := range map[string]*sql.DB{"writer": s.wdb, "reader": s.rdb} {
			var got string
			if err := db.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("%s %s = %q, want %q", label, name, got, want)
			}
		}
	}
	if got := s.rdb.Stats().MaxOpenConnections; got != DefaultReaderConns {
		t.Errorf("reader pool = %d", got)
	}
	if got := s.wdb.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("writer pool = %d", got)
	}
}

func TestReopenAtCurrentVersionChangesNothing(t *testing.T) {
	path := dbPath(t)
	clk := newClock(t0)
	s := openAt(t, path, clk, nil)
	mustAppend(t, s, smp("default", "vm", t0, 1, 2))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	s2 := openAt(t, path, clk, nil)
	if v := version(t, s2); v != 1 {
		t.Fatalf("version = %d", v)
	}
	if n := countRows(t, s2, "metric_sample"); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	// SQLite bumps the 100 byte header change counter on every checkpointing
	// close; everything after the header (schema and data pages) must be identical.
	if len(before) != len(after) || string(before[100:]) != string(after[100:]) {
		t.Fatal("database pages changed on a no-op reopen")
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	path := dbPath(t)
	s := openAt(t, path, newClock(t0), nil)
	if _, err := s.wdb.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	_, err := Open(context.Background(), Options{Path: path})
	var tooNew ErrSchemaTooNew
	if !errors.As(err, &tooNew) || tooNew.Have != 99 || tooNew.Want != 1 {
		t.Fatalf("err = %v, want ErrSchemaTooNew{99,1}", err)
	}
}

func TestCorruptFileRefused(t *testing.T) {
	path := dbPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	junk := make([]byte, 4096)
	for i := range junk {
		junk[i] = byte(i*7 + 3)
	}
	if err := os.WriteFile(path, junk, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), Options{Path: path})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}
}

func TestEmptyFileIsFresh(t *testing.T) {
	path := dbPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := openAt(t, path, newClock(t0), nil)
	if v := version(t, s); v != 1 {
		t.Fatalf("version = %d", v)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ms := []migration{
		{1, "0001_a.sql", "CREATE TABLE a (x INTEGER);"},
		{2, "0002_b.sql", "CREATE TABLE b (x INTEGER); INSERT INTO nonexistent VALUES (1);"},
	}
	if err := migrate(context.Background(), db, ms); err == nil {
		t.Fatal("expected failure")
	}
	v, _ := userVersion(context.Background(), db)
	if v != 1 {
		t.Fatalf("version = %d, want 1 (previous version intact)", v)
	}
	var n int
	_ = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 'b'").Scan(&n)
	if n != 0 {
		t.Fatal("table b survived a failed migration")
	}
	// A fixed migration 2 then applies cleanly on top.
	ms[1].sql = "CREATE TABLE b (x INTEGER);"
	if err := migrate(context.Background(), db, ms); err != nil {
		t.Fatal(err)
	}
	if v, _ := userVersion(context.Background(), db); v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
}

func TestLoadMigrations(t *testing.T) {
	ok := fstest.MapFS{"m/0001_a.sql": {Data: []byte("x")}, "m/0002_b.sql": {Data: []byte("y")}}
	ms, err := loadMigrations(ok, "m")
	if err != nil || len(ms) != 2 || ms[1].version != 2 {
		t.Fatalf("ms=%v err=%v", ms, err)
	}
	gap := fstest.MapFS{"m/0001_a.sql": {Data: []byte("x")}, "m/0003_c.sql": {Data: []byte("y")}}
	if _, err := loadMigrations(gap, "m"); err == nil {
		t.Fatal("gap accepted")
	}
	bad := fstest.MapFS{"m/abc.sql": {Data: []byte("x")}}
	if _, err := loadMigrations(bad, "m"); err == nil {
		t.Fatal("bad name accepted")
	}
	if ms, err := loadMigrations(migrationFS, "migrations"); err != nil || len(ms) < 1 {
		t.Fatalf("embedded: %v %v", ms, err)
	}
}

func TestMemoryDatabase(t *testing.T) {
	s := openAt(t, MemoryPath, newClock(t0), nil)
	if s.wdb != s.rdb {
		t.Fatal("memory database must use one pool")
	}
	mustAppend(t, s, smp("p", "vm", t0, 1, 1))
	if got := query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-time.Hour)}); len(got) != 1 {
		t.Fatalf("got %d points", len(got))
	}
	// Force the disk path too: it must see the same (single) connection.
	s.ringMu.Lock()
	s.rings = map[scopeKey]*ring{}
	s.ringMu.Unlock()
	if got := query(t, s, MetricQuery{Profile: "p", Scope: "vm", From: t0.Add(-time.Hour)}); len(got) != 1 {
		t.Fatalf("disk path got %d points", len(got))
	}
}
