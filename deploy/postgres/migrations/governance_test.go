package migrations_test

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"

	"github.com/guolichen007/crane-operations-platform/deploy/postgres/migrations"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Migration-version governance: the goose version record must stay consistent
// with the applied schema. 00012 adds spec_json and is forward-only, so its Down
// must refuse to run (not silently no-op) — a no-op Down would roll the version
// record back to 11 while leaving the spec_json column behind, making a later Up
// fail on a duplicate column.

func setupGoose(t *testing.T) {
	t.Helper()
	goose.SetLogger(goose.NopLogger())
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
}

func openIsolated(t *testing.T) *sql.DB {
	t.Helper()
	source := os.Getenv("A1_TEST_DATABASE_URL")
	if source == "" {
		t.Skip("requires isolated PostgreSQL (A1_TEST_DATABASE_URL)")
	}
	root, err := sql.Open("pgx", source)
	if err != nil {
		t.Fatal(err)
	}
	name := "crane_mig_gov_" + platform.ID()
	if _, err := root.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		root.Close()
		t.Fatalf("isolated test DB create failed: %v", err)
	}
	u, err := url.Parse(source)
	if err != nil {
		root.Close()
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		root.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = root.Exec(`DROP DATABASE "` + name + `" WITH (FORCE)`)
		root.Close()
	})
	return db
}

func specColumnPresent(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name='asset_equipment_installations' AND column_name='spec_json'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func currentVersion(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRow(`SELECT max(version_id) FROM goose_db_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrationUpFromEmptyAppliesSpec(t *testing.T) {
	db := openIsolated(t)
	setupGoose(t)
	if err := goose.UpContext(context.Background(), db, "."); err != nil {
		t.Fatal(err)
	}
	if !specColumnPresent(t, db) {
		t.Fatal("spec_json column missing after up from empty")
	}
	if v := currentVersion(t, db); v != 12 {
		t.Fatalf("version = %d, want 12", v)
	}
}

func TestMigrationDownRefusedKeepsVersionConsistent(t *testing.T) {
	db := openIsolated(t)
	setupGoose(t)
	if err := goose.UpContext(context.Background(), db, "."); err != nil {
		t.Fatal(err)
	}
	// A forward-only Down must refuse, not roll back the version record while
	// leaving the spec_json column behind.
	if err := goose.DownContext(context.Background(), db, "."); err == nil {
		t.Fatal("goose down succeeded, want forward-only refusal")
	}
	if !specColumnPresent(t, db) {
		t.Fatal("spec_json column lost after refused down")
	}
	if v := currentVersion(t, db); v != 12 {
		t.Fatalf("version after refused down = %d, want 12 (record must stay consistent with schema)", v)
	}
}

func TestMigrationUpFromOldDatabase(t *testing.T) {
	db := openIsolated(t)
	setupGoose(t)
	// An environment frozen at 00011 (pre-spec_json) must upgrade cleanly.
	if err := goose.UpToContext(context.Background(), db, ".", 11); err != nil {
		t.Fatal(err)
	}
	if specColumnPresent(t, db) {
		t.Fatal("spec_json column present before 00012, want absent")
	}
	if err := goose.UpContext(context.Background(), db, "."); err != nil {
		t.Fatalf("upgrade from 00011 to latest failed: %v", err)
	}
	if !specColumnPresent(t, db) {
		t.Fatal("spec_json column missing after upgrade")
	}
	if v := currentVersion(t, db); v != 12 {
		t.Fatalf("version = %d, want 12", v)
	}
}
