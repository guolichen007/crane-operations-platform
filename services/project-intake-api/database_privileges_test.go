package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The runner starts a dedicated, disposable PostgreSQL with the production initializer.
func TestDatabaseProductionPrivileges(t *testing.T) {
	port, err := strconv.ParseUint(os.Getenv("BAOBEI_PRIVILEGE_TEST_PORT"), 10, 16)
	if os.Getenv("BAOBEI_PRIVILEGE_TEST_PORT") == "" {
		t.Skip("NOT_RUN: disposable role fixture required")
	}
	if err != nil || port == 0 {
		t.Fatal("invalid fixture port")
	}
	ctx := context.Background()
	open := func(role string) *pgxpool.Pool {
		pool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s@127.0.0.1:%d/baobei?sslmode=disable", role, port))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	migrator := open("baobei_migrator")
	runtime := open("baobei_runtime")
	maintenance := open("baobei_maintenance")
	mustExec := func(pool *pgxpool.Pool, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	deny := func(pool *pgxpool.Pool, sql string) {
		t.Helper()
		_, err := pool.Exec(ctx, sql)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("expected insufficient_privilege for %q: %v", sql, err)
		}
	}

	if err := prepareDatabase(ctx, runtime, "production", ""); err == nil {
		t.Fatal("runtime accepted an empty schema")
	}
	// Materialize the unchanged first migration to verify an actual additive upgrade.
	const first = "00001_init.sql"
	body, err := migrations.ReadFile("migrations/" + first)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(migrator, "CREATE TABLE baobei_migrations(name text PRIMARY KEY, checksum text NOT NULL)")
	mustExec(migrator, string(body))
	mustExec(migrator, "INSERT INTO baobei_migrations VALUES($1,$2)", first, fmt.Sprintf("%x", sha256.Sum256(body)))
	if err := seed(ctx, migrator); err != nil {
		t.Fatal(err)
	}
	mustExec(migrator, "GRANT SELECT ON baobei_migrations TO baobei_runtime")
	if err := prepareDatabase(ctx, runtime, "production", ""); err == nil {
		t.Fatal("runtime accepted an outdated schema")
	}
	for range 2 {
		if err := prepareDatabase(ctx, migrator, "production", "migrate"); err != nil {
			t.Fatal("production migration", err)
		}
	}
	for _, test := range []struct {
		pool    *pgxpool.Pool
		command string
	}{{runtime, ""}, {runtime, "protection-sweep"}, {maintenance, "identity-apply"}} {
		if err := prepareDatabase(ctx, test.pool, "production", test.command); err != nil {
			t.Fatal("production readiness", err)
		}
	}
	for _, test := range []struct {
		pool    *pgxpool.Pool
		command string
	}{{runtime, "migrate"}, {runtime, "identity-apply"}, {migrator, ""}, {maintenance, ""}, {maintenance, "migrate"}, {open("baobei_bootstrap"), ""}} {
		if err := prepareDatabase(ctx, test.pool, "production", test.command); err == nil {
			t.Fatal("cross-role command accepted", test.command)
		}
	}
	mustExec(migrator, "INSERT INTO baobei_migrations VALUES ('99999_unexpected.sql', 'synthetic')")
	if err := prepareDatabase(ctx, runtime, "production", ""); err == nil {
		t.Fatal("runtime accepted unexpected migration")
	}
	mustExec(migrator, "DELETE FROM baobei_migrations WHERE name = '99999_unexpected.sql'")

	for _, sql := range []string{
		"CREATE TABLE public.forbidden(id integer)",
		"CREATE TEMP TABLE forbidden(id integer)",
		"CREATE SCHEMA forbidden",
		"ALTER TABLE intake_projects ADD COLUMN forbidden text",
		"DROP TABLE intake_projects",
		"SET ROLE baobei_migrator",
		"UPDATE baobei_migrations SET checksum = 'forbidden'",
		"UPDATE app_users SET role = 'baobei_admin'",
		"INSERT INTO teams(name) VALUES ('forbidden')",
		"DELETE FROM audit_events",
	} {
		deny(runtime, sql)
	}
	for _, sql := range []string{
		"CREATE TABLE public.forbidden(id integer)",
		"SET ROLE baobei_runtime",
		"UPDATE intake_projects SET version = 2",
		"DELETE FROM app_users",
		"SELECT token_hash FROM app_sessions",
	} {
		deny(maintenance, sql)
	}

	// Drive the real HTTP handlers against a restricted login using synthetic actors.
	app := &App{db: runtime, cfg: Config{Env: "development", Dev: true}}
	server := httptest.NewServer(app.handler())
	defer server.Close()
	app.cfg.Origin = server.URL
	sales := login(t, server, "synthetic-a")
	manager := login(t, server, "synthetic-manager")
	status, response := sales.call("POST", "/api/v1/projects", sample("Synthetic restricted role"))
	project := projectFrom(t, status, response)
	path := "/api/v1/projects/" + project.ID
	project.Input.Version = project.Version
	status, response = sales.call("PATCH", path, project.Input)
	project = projectFrom(t, status, response)
	status, response = sales.call("POST", path+"/submit", map[string]int{"version": project.Version})
	project = projectFrom(t, status, response)
	status, response = manager.call("POST", path+"/ownership-transfer", map[string]any{
		"version": project.Version, "targetSubject": "synthetic-b", "reason": "Synthetic restricted transfer",
	})
	project = projectFrom(t, status, response)
	if project.OwnerSubject != "synthetic-b" {
		t.Fatal("restricted role transfer failed")
	}
	status, response = manager.call("POST", path+"/approve", map[string]any{
		"version": project.Version, "reason": "Synthetic restricted approval",
	})
	project = projectFrom(t, status, response)
	if project.IntakeStatus != "APPROVED" {
		t.Fatal("restricted role approval failed")
	}

	summary, err := applyIdentity(ctx, maintenance, identityFixture(), "synthetic-operator", false)
	if err != nil || summary.UsersChanged != 1 || summary.TeamsChanged != 1 {
		t.Fatal("maintenance could not provision approved identity", summary, err)
	}
	var role string
	if err := runtime.QueryRow(ctx, "SELECT role FROM app_users WHERE subject=$1", "77777777-7777-4777-8777-777777777777").Scan(&role); err != nil || role != "baobei_sales" {
		t.Fatal("provisioned identity unreadable", role, err)
	}
}
