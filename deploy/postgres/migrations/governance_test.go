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
	if v := currentVersion(t, db); v != migrations.Latest() {
		t.Fatalf("version = %d, want %d", v, migrations.Latest())
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
	if v := currentVersion(t, db); v != migrations.Latest() {
		t.Fatalf("version after refused down = %d, want %d (record must stay consistent with schema)", v, migrations.Latest())
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
	if v := currentVersion(t, db); v != migrations.Latest() {
		t.Fatalf("version = %d, want %d", v, migrations.Latest())
	}
}

// P1: a populated schema-12 database (OIDC users, project + portfolio grants, sessions, audit) is
// migrated to 13 without losing or changing anything; existing identities stay enabled and
// unmanaged (no username, idp_state SYNCED, no system role), new tables start empty, and the new
// constraints accept the existing rows.
func TestMigration12To13PreservesPopulatedData(t *testing.T) {
	db := openIsolated(t)
	setupGoose(t)
	ctx := context.Background()
	if err := goose.UpToContext(ctx, db, ".", 12); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO tenants(id,name) VALUES('t1','t1') ON CONFLICT DO NOTHING`,
		`INSERT INTO projects(id,tenant_id,name) VALUES('P-M','t1','迁移项目')`,
		`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS-M','t1','PS-M','组合')`,
		`INSERT INTO users(id,subject,issuer,display_name,enabled) VALUES('owner','00000000-0000-4000-8000-0000000000aa','https://crane.example/realms/crane','OWNER',true),('old','00000000-0000-4000-8000-0000000000bb','https://crane.example/realms/crane','Old',false)`,
		`INSERT INTO project_memberships(user_id,project_id,role) VALUES('owner','P-M','platform_admin')`,
		`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('owner','PS-M','portfolio_manager')`,
		`INSERT INTO web_sessions(session_hash,user_id,csrf_hash,created_at,last_seen_at,expires_at,idle_expires_at) VALUES(decode(repeat('ab',32),'hex'),'owner',decode(repeat('cd',32),'hex'),now(),now(),now()+interval '1 hour',now()+interval '10 minutes')`,
		`INSERT INTO audit_logs(user_id,project_id,action,result,request_id) VALUES('owner','P-M','login_success','success','r-1')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(stmt, err)
		}
	}
	fingerprint := func() string {
		var s string
		if err := db.QueryRow(`SELECT md5(
		  (SELECT string_agg(id||'|'||subject||'|'||coalesce(issuer,'')||'|'||display_name||'|'||enabled::text, ',' ORDER BY id) FROM users)||'#'||
		  (SELECT string_agg(user_id||'|'||project_id||'|'||role, ',' ORDER BY user_id) FROM project_memberships)||'#'||
		  (SELECT string_agg(user_id||'|'||portfolio_scope_id||'|'||role, ',' ORDER BY user_id) FROM portfolio_memberships)||'#'||
		  (SELECT count(*)::text FROM web_sessions)||'#'||
		  (SELECT string_agg(user_id||'|'||action||'|'||result||'|'||request_id, ',' ORDER BY id) FROM audit_logs))`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := fingerprint()
	if err := goose.UpToContext(ctx, db, ".", 13); err != nil {
		t.Fatalf("12 -> 13 on populated data: %v", err)
	}
	if v := currentVersion(t, db); v != 13 {
		t.Fatalf("version = %d", v)
	}
	if after := fingerprint(); after != before {
		t.Fatal("existing users / grants / sessions / audit changed by 00013")
	}
	var unmanaged, roles, provisions, departments int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM users WHERE id IN ('owner','old') AND username IS NULL AND idp_state='SYNCED' AND version=1 AND sessions_revoked_at IS NULL),
	  (SELECT count(*) FROM system_memberships),(SELECT count(*) FROM account_provisions),(SELECT count(*) FROM departments)`).Scan(&unmanaged, &roles, &provisions, &departments); err != nil {
		t.Fatal(err)
	}
	if unmanaged != 2 || roles != 0 || provisions != 0 || departments != 0 {
		t.Fatalf("post-migration defaults: unmanaged=%d roles=%d provisions=%d departments=%d", unmanaged, roles, provisions, departments)
	}
	// Forward-only: Down refuses and the version stays 13.
	if err := goose.DownContext(ctx, db, "."); err == nil {
		t.Fatal("00013 down must refuse")
	}
	if v := currentVersion(t, db); v != 13 {
		t.Fatalf("version after refused down = %d", v)
	}
}

// R9 A1: a populated schema-13 database (people, roles, provisions, audit) migrates to 14 without
// changing anything; nobody is archived or protected by the migration; the new CHECKs accept every
// existing row; an archived person can never be enabled again; forward-only.
func TestMigration13To14PersonnelArchive(t *testing.T) {
	db := openIsolated(t)
	setupGoose(t)
	ctx := context.Background()
	if err := goose.UpToContext(ctx, db, ".", 13); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO tenants(id,name) VALUES('t1','t1') ON CONFLICT DO NOTHING`,
		`INSERT INTO users(id,subject,issuer,display_name,enabled,username) VALUES('owner','00000000-0000-4000-8000-0000000000aa','https://crane.example/realms/crane','OWNER',true,'owner'),('off','00000000-0000-4000-8000-0000000000bb','https://crane.example/realms/crane','Off',false,'off')`,
		`INSERT INTO system_memberships(user_id,role,granted_by) VALUES('owner','system_admin','host:ops')`,
		`INSERT INTO account_provisions(id,actor,idempotency_key,payload_sha256,username,request,state) VALUES('p1','owner','key-0000001','x','off','{}','COMPLETED')`,
		`INSERT INTO audit_logs(user_id,action,result,request_id,target_user_id) VALUES('owner','account_disabled','success','r-1','off')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(stmt, err)
		}
	}
	fingerprint := func() string {
		var s string
		if err := db.QueryRow(`SELECT md5((SELECT string_agg(id||'|'||enabled::text||'|'||coalesce(username,'')||'|'||version::text, ',' ORDER BY id) FROM users)||'#'||
		  (SELECT string_agg(user_id||role, ',') FROM system_memberships)||'#'||(SELECT string_agg(id||state, ',') FROM account_provisions)||'#'||(SELECT count(*)::text FROM audit_logs))`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := fingerprint()
	if err := goose.UpToContext(ctx, db, ".", 14); err != nil {
		t.Fatalf("13 -> 14: %v", err)
	}
	if fingerprint() != before {
		t.Fatal("00014 changed existing people / roles / provisions / audit")
	}
	var archived, protected int
	if err := db.QueryRow(`SELECT count(*) FILTER (WHERE archived_at IS NOT NULL), count(*) FILTER (WHERE protected_reason IS NOT NULL) FROM users`).Scan(&archived, &protected); err != nil || archived != 0 || protected != 0 {
		t.Fatalf("migration archived/protected people: %d/%d %v", archived, protected, err)
	}
	if _, err := db.Exec(`UPDATE users SET archived_at=now() WHERE id='owner'`); err == nil {
		t.Fatal("an enabled person was archived (CHECK users_archived_disabled missing)")
	}
	if _, err := db.Exec(`UPDATE users SET archived_at=now() WHERE id='off'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET enabled=true WHERE id='off'`); err == nil {
		t.Fatal("an archived person was re-enabled")
	}
	if _, err := db.Exec(`UPDATE users SET protected_reason='OWNER' WHERE id='off'`); err == nil {
		t.Fatal("an archived person was marked protected")
	}
	if _, err := db.Exec(`UPDATE account_provisions SET state='PURGED' WHERE id='p1'`); err != nil {
		t.Fatal("PURGED provision state refused:", err)
	}
	if err := goose.DownContext(ctx, db, "."); err == nil {
		t.Fatal("00014 down must refuse")
	}
}
