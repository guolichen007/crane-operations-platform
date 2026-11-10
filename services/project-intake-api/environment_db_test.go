package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// G4-S1A: preview database identity against a disposable PostgreSQL that was
// initialised with the deployed initializers (initialize.sql, staff-role.sql).
// BAOBEI_PREVIEW_TEST_PORT points at such a fixture WITHOUT the marker;
// the test adds the marker itself (as preview-environment.sql does) part way.
func TestDatabasePreviewEnvironment(t *testing.T) {
	v := os.Getenv("BAOBEI_PREVIEW_TEST_PORT")
	if v == "" {
		t.Skip("NOT_RUN: disposable preview role fixture required")
	}
	port, err := strconv.ParseUint(v, 10, 16)
	if err != nil || port == 0 {
		t.Fatal("invalid fixture port")
	}
	ctx := context.Background()
	open := func(role string) *pgxpool.Pool {
		cfg, err := pgxpool.ParseConfig(fmt.Sprintf("postgres://%s@127.0.0.1:%d/baobei?sslmode=disable", role, port))
		if err != nil {
			t.Fatal(err)
		}
		cfg.ConnConfig.RuntimeParams["search_path"] = "public"
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	migrator, runtime, staff, bootstrap := open("baobei_migrator"), open("baobei_runtime"), open("baobei_staff_api"), open("baobei_bootstrap")
	migrated := func() bool {
		var ok bool
		_ = bootstrap.QueryRow(ctx, `SELECT to_regclass('public.baobei_migrations') IS NOT NULL`).Scan(&ok)
		return ok
	}

	// 1. Marker missing: preview refuses, and the migrator does not migrate.
	if err := prepareDatabase(ctx, migrator, "preview", "migrate"); !errors.Is(err, errEnvironmentMismatch) {
		t.Fatal("preview migrate without marker:", err)
	}
	if migrated() {
		t.Fatal("misconfigured preview migrated the database")
	}
	if err := verifyDatabaseEnvironment(ctx, staff, "preview"); !errors.Is(err, errEnvironmentMismatch) {
		t.Fatal("preview staff without marker:", err)
	}
	// Production on an unmarked database keeps working as before (no forced marker migration).
	if err := verifyDatabaseEnvironment(ctx, runtime, "production"); err != nil {
		t.Fatal("unmarked database rejected for production:", err)
	}

	// 2. Wrong role for a preview command: refused before any environment or migration step.
	for _, c := range []struct {
		pool    *pgxpool.Pool
		command string
	}{{runtime, "migrate"}, {migrator, ""}, {staff, ""}, {bootstrap, ""}, {bootstrap, "migrate"}} {
		if err := prepareDatabase(ctx, c.pool, "preview", c.command); err == nil {
			t.Fatal("wrong role accepted for preview", c.command)
		}
	}
	if migrated() {
		t.Fatal("wrong-role preview command migrated the database")
	}

	// 3. Marker present (applied the way deploy/baobei/db/preview-environment.sql does).
	body, err := os.ReadFile("../../deploy/baobei/db/preview-environment.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.Exec(ctx, string(body)); err != nil {
		t.Fatal(err)
	}
	if err := prepareDatabase(ctx, migrator, "preview", "migrate"); err != nil {
		t.Fatal("preview migrate with marker:", err)
	}
	if err := prepareDatabase(ctx, runtime, "preview", ""); err != nil {
		t.Fatal("preview runtime with marker:", err)
	}
	if err := verifyDatabaseRole(ctx, staff, "baobei_staff_api", false); err != nil {
		t.Fatal(err)
	}
	if err := verifyDatabaseEnvironment(ctx, staff, "preview"); err != nil {
		t.Fatal("preview staff with marker:", err)
	}

	// 4. Environment mismatch: a production API on the preview database refuses
	//    (runtime, staff and migrator alike), and does not migrate.
	for _, c := range []struct {
		pool    *pgxpool.Pool
		command string
	}{{runtime, ""}, {migrator, "migrate"}} {
		if err := prepareDatabase(ctx, c.pool, "production", c.command); !errors.Is(err, errEnvironmentMismatch) {
			t.Fatal("production accepted the preview database:", c.command, err)
		}
	}
	if err := verifyDatabaseEnvironment(ctx, staff, "production"); !errors.Is(err, errEnvironmentMismatch) {
		t.Fatal("production staff accepted the preview database:", err)
	}
	// Unknown environment names never take the deployed path.
	if err := prepareDatabase(ctx, runtime, "staging", ""); err == nil {
		t.Fatal("unknown environment accepted")
	}

	// 5. The marker is protected: runtime/staff cannot rewrite it; its CHECK
	//    rejects any value other than preview, and a second row is impossible.
	for _, sql := range []string{
		"UPDATE baobei_environment SET name='production'",
		"DELETE FROM baobei_environment",
		"DROP TABLE baobei_environment",
	} {
		for _, pool := range []*pgxpool.Pool{runtime, staff} {
			if _, err := pool.Exec(ctx, sql); err == nil {
				t.Fatal("marker writable:", sql)
			}
		}
	}
	for _, sql := range []string{
		"UPDATE baobei_environment SET name='production'",
		"INSERT INTO baobei_environment(singleton,name) VALUES (false,'preview')",
		"INSERT INTO baobei_environment(name) VALUES ('preview')",
	} {
		if _, err := bootstrap.Exec(ctx, sql); err == nil {
			t.Fatal("marker constraint missing:", sql)
		}
	}
}
