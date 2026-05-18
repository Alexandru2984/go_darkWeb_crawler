package database

import (
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	pgdriver "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/lib/pq"
)

// TestMigrationsEmbeddedSource is a unit test (no DB needed): verifies the
// SQL files are bundled into the binary under the expected names so the
// golang-migrate iofs source can discover them. Catches filename typos
// (e.g. missing `.up.sql` suffix) before they reach prod.
func TestMigrationsEmbeddedSource(t *testing.T) {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("iofs.New: %v", err)
	}
	defer src.Close()

	first, err := src.First()
	if err != nil {
		t.Fatalf("First(): %v", err)
	}
	if first != 1 {
		t.Errorf("first version = %d, want 1", first)
	}

	r, _, err := src.ReadUp(1)
	if err != nil {
		t.Fatalf("ReadUp(1): %v", err)
	}
	r.Close()
	r, _, err = src.ReadDown(1)
	if err != nil {
		t.Fatalf("ReadDown(1): %v", err)
	}
	r.Close()
}

// TestMigrationsApplyAndIdempotent is an integration test: needs a throwaway
// Postgres reachable at $TEST_DATABASE_URL. Skipped otherwise so the default
// `go test ./...` stays hermetic.
//
// What it proves:
//  1. The initial migration runs cleanly against an empty database.
//  2. Running runMigrations a second time is a no-op (idempotent).
//  3. schema_migrations records version 1, dirty=false.
//  4. The Down migration tears everything back down without errors.
func TestMigrationsApplyAndIdempotent(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping postgres integration test")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("db.Ping: %v", err)
	}

	// First apply — empty DB, should create everything.
	if err := runMigrations(db); err != nil {
		t.Fatalf("first runMigrations: %v", err)
	}

	// Second apply — should be a no-op via migrate.ErrNoChange.
	if err := runMigrations(db); err != nil {
		t.Fatalf("second runMigrations (should be no-op): %v", err)
	}

	// schema_migrations should record version 1, not dirty.
	var v int
	var dirty bool
	if err := db.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&v, &dirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if v != 1 || dirty {
		t.Errorf("schema_migrations = (version=%d, dirty=%v), want (1, false)", v, dirty)
	}

	// Expected tables exist.
	for _, tbl := range []string{"users", "nodes", "edges", "auth_audit", "blacklist"} {
		if !tableExists(t, db, tbl) {
			t.Errorf("expected table %q to exist after migration", tbl)
		}
	}

	// Now exercise Down: should drop everything cleanly.
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("iofs.New: %v", err)
	}
	driver, err := pgdriver.WithInstance(db, &pgdriver.Config{})
	if err != nil {
		t.Fatalf("pgdriver.WithInstance: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		t.Fatalf("NewWithInstance: %v", err)
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("Down(): %v", err)
	}

	// After Down, the domain tables should be gone. (schema_migrations stays —
	// it's owned by the migrator, not our migration.)
	for _, tbl := range []string{"users", "nodes", "edges", "auth_audit", "blacklist"} {
		if tableExists(t, db, tbl) {
			t.Errorf("expected table %q to be dropped after Down()", tbl)
		}
	}
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=$1)`,
		name).Scan(&exists)
	if err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return exists
}
