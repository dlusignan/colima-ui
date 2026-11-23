package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads NNNN_name.sql files from dir and checks that versions
// run 1..n without gaps.
func loadMigrations(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		numPart, _, ok := strings.Cut(name, "_")
		v, err := strconv.Atoi(numPart)
		if !ok || err != nil || v < 1 {
			return nil, fmt.Errorf("store: bad migration file name %q", name)
		}
		body, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return nil, fmt.Errorf("store: read migration %q: %w", name, err)
		}
		ms = append(ms, migration{version: v, name: name, sql: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("store: migration versions are not contiguous at %q", m.name)
		}
	}
	return ms, nil
}

func userVersion(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int, error) {
	var v int
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// migrate brings the database to the newest version. A database already at the
// newest version is not written to. Each step runs in its own BEGIN IMMEDIATE
// transaction together with its version bump, so a failure leaves the previous
// version intact. A newer database is refused with ErrSchemaTooNew.
func migrate(ctx context.Context, db *sql.DB, ms []migration) error {
	want := len(ms)
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	have, err := userVersion(ctx, conn)
	if err != nil {
		return err
	}
	if have > want {
		return ErrSchemaTooNew{Have: have, Want: want}
	}
	for _, m := range ms {
		if m.version <= have {
			continue
		}
		if err := applyMigration(ctx, conn, m); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, conn *sql.Conn, m migration) (err error) {
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
	}()
	// Re-check under the write lock: another process may have migrated.
	cur, err := userVersion(ctx, conn)
	if err != nil {
		return err
	}
	if cur >= m.version {
		return nil // the deferred rollback ends the empty transaction
	}
	if cur != m.version-1 {
		return fmt.Errorf("store: unexpected schema version %d before migration %d", cur, m.version)
	}
	if _, err = conn.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}
	if _, err = conn.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(m.version)); err != nil {
		return fmt.Errorf("store: migration %s: set version: %w", m.name, err)
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

