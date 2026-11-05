package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
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
	staff := open("baobei_staff_api")
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
	const second = "00002_auth_and_governance.sql"
	body, err = migrations.ReadFile("migrations/" + second)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(migrator, string(body))
	mustExec(migrator, "INSERT INTO baobei_migrations VALUES($1,$2)", second, fmt.Sprintf("%x", sha256.Sum256(body)))
	mustExec(migrator, "INSERT INTO app_users(subject,display_name,role) VALUES('synthetic-legacy','旧销售','baobei_sales')")
	mustExec(migrator, "INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,document) VALUES('Legacy','legacy','synthetic-legacy','synthetic-legacy','{}')")
	mustExec(migrator, "GRANT SELECT ON baobei_migrations TO baobei_runtime")
	if err := prepareDatabase(ctx, runtime, "production", ""); err == nil {
		t.Fatal("runtime accepted an outdated schema")
	}
	for range 2 {
		if err := prepareDatabase(ctx, migrator, "production", "migrate"); err != nil {
			t.Fatal("production migration", err)
		}
	}
	if err := seed(ctx, migrator); err != nil {
		t.Fatal(err)
	}
	if err := verifyDatabaseRole(ctx, staff, "baobei_staff_api", false); err != nil {
		t.Fatal(err)
	}
	var legacy int
	if err := runtime.QueryRow(ctx, `SELECT staff_permissions FROM app_users u JOIN intake_projects p ON p.created_by_subject=u.subject
      WHERE u.subject='synthetic-legacy' AND u.active AND p.owner_subject=u.subject`).Scan(&legacy); err != nil || legacy != 7 {
		t.Fatal("legacy permissions or history", err)
	}
	for _, test := range []struct {
		pool    *pgxpool.Pool
		command string
	}{{runtime, ""}, {runtime, "protection-sweep"}, {maintenance, "identity-apply"}, {maintenance, "staff-provision"}} {
		if err := prepareDatabase(ctx, test.pool, "production", test.command); err != nil {
			t.Fatal("production readiness", err)
		}
	}
	for _, test := range []struct {
		pool    *pgxpool.Pool
		command string
	}{{runtime, "migrate"}, {runtime, "identity-apply"}, {runtime, "staff-provision"}, {staff, "staff-provision"}, {migrator, ""}, {maintenance, ""}, {maintenance, "migrate"}, {open("baobei_bootstrap"), ""}} {
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
		"UPDATE app_users SET staff_permissions = 0",
		"SELECT work_email FROM app_users",
		"SELECT token_hash FROM staff_sessions",
		"INSERT INTO staff_sessions(token_hash,subject) VALUES ('forged','synthetic-manager')",
		"SET ROLE baobei_staff_api",
		"INSERT INTO teams(name) VALUES ('forbidden')",
		"UPDATE project_submissions SET evidence_state='COMPLETE'",
		"DELETE FROM project_participations",
		"UPDATE project_create_requests SET request_hash=repeat('a',64)",
		"UPDATE intake_projects SET created_by_subject='synthetic-a'",
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
	for _, sql := range []string{"UPDATE app_users SET subject='forbidden'", "UPDATE intake_projects SET document='{}'", "UPDATE intake_projects SET created_by_subject='forbidden'", "CREATE TABLE forbidden(id int)", "UPDATE project_submissions SET evidence_state='COMPLETE'", "SELECT document FROM project_submissions"} {
		deny(staff, sql)
	}
	app := &App{db: runtime, staffDB: staff, cfg: Config{Env: "development", Dev: true}}
	server := httptest.NewServer(app.handler())
	defer server.Close()
	app.cfg.Origin = server.URL
	sales := login(t, server, "synthetic-a")
	manager := login(t, server, "synthetic-manager")
	director := login(t, server, "synthetic-director")
	statusStaff, bodyStaff := manager.call("POST", "/api/v1/staff/invitations", staffProfileInput{Name: "合成隔离邀请", Email: "role-fixture@example.test", Role: salesRole, TeamID: syntheticTeam, Reason: "角色隔离验证"})
	invitation := memberFrom(t, statusStaff, bodyStaff)
	receipt := provisionReceipt{StaffID: invitation.ID, Version: invitation.Version, Operation: "approve",
		Subject: "99999999-9999-4999-8999-999999999999", Email: invitation.Email, IdentityVerified: true, UniqueEmail: true,
		ApprovalReference: "SYNTHETIC-STAFF-PRIVILEGE", Reason: "合成验证受限维护角色"}
	if outcome, err := applyProvisionReceipt(ctx, maintenance, mustJSON(receipt), "synthetic-operator", false); err != nil || outcome != staffActivation {
		t.Fatal(outcome, err)
	}
	mustExec(runtime, `INSERT INTO app_sessions VALUES($1,'synthetic-manager','synthetic',now()+interval '1 hour')`, hash("synthetic-forged-session"))
	request := httptest.NewRequest("GET", server.URL+"/api/v1/staff", nil)
	request.AddCookie(&http.Cookie{Name: app.cookieName(), Value: "synthetic-forged-session"})
	recorded := httptest.NewRecorder()
	app.handler().ServeHTTP(recorded, request)
	if recorded.Code != 403 {
		t.Fatal("ordinary DB forged a staff session", recorded.Code)
	}
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
	expect(t, status, 403, response)
	status, response = director.call("POST", path+"/approve", map[string]any{"version": project.Version, "reason": "独立总监核验移交后的报备"})
	project = projectFrom(t, status, response)
	if project.IntakeStatus != "APPROVED" {
		t.Fatal("restricted role approval failed")
	}
	var sourceStaff, targetStaff string
	var sourceVersion int
	if err := staff.QueryRow(ctx, `SELECT staff_id::text,staff_version FROM app_users WHERE subject='synthetic-b'`).Scan(&sourceStaff, &sourceVersion); err != nil {
		t.Fatal(err)
	}
	if err := staff.QueryRow(ctx, `SELECT staff_id::text FROM app_users WHERE subject='synthetic-a'`).Scan(&targetStaff); err != nil {
		t.Fatal(err)
	}
	status, response = manager.call("POST", "/api/v1/staff/"+sourceStaff+"/ownership-transfer-batch", map[string]any{
		"version": sourceVersion, "targetId": targetStaff, "reason": "合成分权批量交接",
		"projects": []map[string]any{{"id": project.ID, "version": project.Version}},
	})
	expect(t, status, 200, response)
	status, response = sales.call("GET", path, nil)
	project = projectFrom(t, status, response)
	if project.OwnerSubject != "synthetic-a" || project.CreatedBySubject != "synthetic-a" || project.IntakeStatus != "APPROVED" {
		t.Fatal("restricted staff batch transfer failed")
	}

	// G2-0 paths under least privilege: receipt lookup, display names, list
	// hints with review facts and admin handoff through the staff connection.
	admin := login(t, server, "synthetic-admin")
	receiptKey := "g2-privilege-" + strings.Repeat("k", 20)
	status, response = admin.keyed(receiptKey, map[string]any{"projectName": "合成分权回执", "version": 1})
	draft := projectFrom(t, status, response)
	status, response = admin.call("GET", "/api/v1/project-create-receipts/"+receiptKey, nil)
	expect(t, status, 200, response)
	status, response = admin.call("GET", "/api/v1/projects/"+draft.ID, nil)
	if d := projectFrom(t, status, response); d.CreatedByName != "示例管理员" {
		t.Fatal("restricted display name", d.CreatedByName)
	}
	status, response = director.call("GET", "/api/v1/projects?pageSize=50", nil)
	expect(t, status, 200, response)
	for _, c := range []testClient{admin, director, manager, sales} {
		status, response = c.call("GET", "/api/v1/workspace-summary", nil)
		expect(t, status, 200, response)
	}
	if err := staff.QueryRow(ctx, `SELECT staff_version FROM app_users WHERE subject='synthetic-a'`).Scan(&sourceVersion); err != nil {
		t.Fatal(err)
	}
	status, response = admin.call("POST", "/api/v1/staff/"+targetStaff+"/ownership-transfer-batch", map[string]any{
		"version": sourceVersion, "targetId": sourceStaff, "reason": "合成管理员分权交接",
		"projects": []map[string]any{{"id": project.ID, "version": project.Version}},
	})
	expect(t, status, 200, response)
	if !strings.Contains(string(response), "TRANSFERRED") {
		t.Fatal("restricted admin handoff", string(response))
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
