package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// G5-RC0, against real PostgreSQL 18.4. These tests prove why an upgrade across
// 00006 needs a maintenance window and that the identity tooling fails closed.

// oldAPISessionInsert is the session INSERT of the API before 00006 (253b4a8,
// auth.go): it does not know the issuer / identity_epoch columns.
const oldAPISessionInsert = `INSERT INTO app_sessions(token_hash,subject,csrf,expires_at) VALUES($1,$2,$3,now()+interval '8 hours')`

// schemaUpTo builds a fresh schema from migrations strictly before `stop`.
func schemaUpTo(t *testing.T, stop string) *pgxpool.Pool {
	t.Helper()
	db := b2Database(t) // full schema (for the cleanup and DSN); build another next to it
	ctx := context.Background()
	name := "rc0_" + hash(randomToken())[:12]
	if _, err := db.Exec(ctx, `CREATE SCHEMA `+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DROP SCHEMA `+name+` CASCADE`) })
	cfg := db.Config().Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = name + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err = pool.Exec(ctx, `CREATE TABLE baobei_migrations(name text PRIMARY KEY, checksum text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, _ := migrations.ReadDir("migrations")
	for _, e := range entries {
		if stop != "" && e.Name() >= stop {
			break
		}
		b, _ := migrations.ReadFile("migrations/" + e.Name())
		if _, err = pool.Exec(ctx, string(b)); err != nil {
			t.Fatal(e.Name(), err)
		}
		h := sha256.Sum256(b)
		if _, err = pool.Exec(ctx, `INSERT INTO baobei_migrations VALUES($1,$2)`, e.Name(), hex.EncodeToString(h[:])); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO app_users(subject,display_name,role) VALUES('synthetic-old','旧版销售','baobei_sales')`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestDatabaseRC0MixedVersionNeedsMaintenanceWindow(t *testing.T) {
	ctx := context.Background()
	// 1. Old API + old schema (00001–00005): sign-in works.
	old := schemaUpTo(t, "00006")
	if _, err := old.Exec(ctx, oldAPISessionInsert, "tok-old-"+randomToken(), "synthetic-old", "csrf"); err != nil {
		t.Fatal("old API on old schema must sign in", err)
	}
	// 2. Old API + new schema (after 00006): the session cannot be created.
	if err := migrate(ctx, old); err != nil {
		t.Fatal("upgrade", err)
	}
	_, err := old.Exec(ctx, oldAPISessionInsert, "tok-mixed-"+randomToken(), "synthetic-old", "csrf")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23502" {
		t.Fatal("old API on the new schema must fail (NOT NULL issuer/identity_epoch)", err)
	}
	// Sessions created by the old API before the upgrade are invalid afterwards:
	// they carry the 'legacy:unknown' issuer and epoch 0.
	var legacy int
	_ = old.QueryRow(ctx, `SELECT count(*) FROM app_sessions WHERE issuer='legacy:unknown' AND identity_epoch=0`).Scan(&legacy)
	if legacy != 1 {
		t.Fatal("pre-upgrade session not marked legacy", legacy)
	}
	// 3. New API without an identity epoch: refuses to start (deployed env).
	issuer := deployedProfiles["production"].Realms["crane"].Issuer
	if err = verifyIdentityEpoch(ctx, old, "production", issuer); !errors.Is(err, errIdentityEpochMismatch) {
		t.Fatal("new API must refuse without an identity epoch", err)
	}
	// 4. Matching epoch: the new API's session insert works.
	if _, _, _, err = switchIdentity(ctx, old, issuer, "rc0-operator", "维护窗口内建立身份纪元"); err != nil {
		t.Fatal(err)
	}
	if err = verifyIdentityEpoch(ctx, old, "production", issuer); err != nil {
		t.Fatal("matching issuer", err)
	}
	tag, err := old.Exec(ctx, `INSERT INTO app_sessions(token_hash,subject,csrf,expires_at,issuer,identity_epoch)
	 SELECT $1,'synthetic-old','csrf',now()+interval '8 hours',i.issuer,i.epoch FROM identity_epoch i WHERE i.issuer=$2`, "tok-new-"+randomToken(), issuer)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("new API sign-in", err)
	}
	// A new API configured for the other realm still refuses.
	if err = verifyIdentityEpoch(ctx, old, "production", deployedProfiles["production"].Realms["baobei"].Issuer); !errors.Is(err, errIdentityEpochMismatch) {
		t.Fatal("wrong issuer accepted", err)
	}
}

// identity-switch --dry-run must never report success on a schema it cannot read.
func TestDatabaseRC0IdentityDryRunFailsClosed(t *testing.T) {
	ctx := context.Background()
	old := schemaUpTo(t, "00006") // identity_epoch does not exist yet
	var out bytes.Buffer
	err := identitySwitchCommand(ctx, old, "production", []string{"--realm", "crane", "--dry-run"}, &out)
	if err == nil || strings.Contains(out.String(), `"dryRun":true`) {
		t.Fatal("dry-run on a schema without identity_epoch reported success:", out.String(), err)
	}
}

// Switching to the dedicated realm without one approved, linked administrator
// would lock everybody out: the switch must be refused.
func TestDatabaseRC0SwitchRefusedWithoutLinkedAdmin(t *testing.T) {
	ctx := context.Background()
	db := b2Database(t)
	shared := deployedProfiles["production"].Realms["crane"].Issuer
	dedicated := deployedProfiles["production"].Realms["baobei"].Issuer
	if _, _, _, err := switchIdentity(ctx, db, shared, "rc0-operator", "初始"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := identitySwitchCommand(ctx, db, "production", []string{"--realm", "baobei", "--operator", "rc0-operator", "--reason", "切换"}, &out)
	var current string
	_ = db.QueryRow(ctx, `SELECT issuer FROM identity_epoch`).Scan(&current)
	if err == nil || current != shared {
		t.Fatal("switch to a realm without a linked administrator was executed:", current, out.String(), err)
	}
	_ = dedicated
}

// Full identity-switch flow with an approved takeover administrator: forward
// and back, sessions revoked both ways, members who cannot sign in after the
// switch are reported (never "everyone switched").
func TestDatabaseRC0SwitchWithTakeoverAdminBothWays(t *testing.T) {
	ctx := context.Background()
	db := b2Database(t)
	dedicated := deployedProfiles["production"].Realms["baobei"].Issuer
	run := func(args ...string) (map[string]any, error) {
		var out bytes.Buffer
		err := identitySwitchCommand(ctx, db, "production", args, &out)
		var m map[string]any
		_ = json.Unmarshal(out.Bytes(), &m)
		return m, err
	}
	// Production starts on the shared realm (the fixture DB trusts the dev issuer).
	trustIssuer(t, db, deployedProfiles["production"].Realms["crane"].Issuer)
	// An approved administrator migrated to the dedicated realm (bound subject +
	// link), plus one shared-realm sales member who is not migrated.
	admin, sales := newUUID(), newUUID()
	var adminID string
	if err := db.QueryRow(ctx, `INSERT INTO app_users(subject,display_name,role,active,staff_status,staff_managed,work_phone,account_state)
	 VALUES($1,'接管管理员','baobei_admin',true,'ACTIVE',true,'+8613980000001','READY') RETURNING staff_id::text`, admin).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO app_users(subject,display_name,role,team_id,active,staff_status,staff_managed,work_phone)
	 VALUES($1,'未迁移销售','baobei_sales',$2,true,'ACTIVE',true,'+8613980000002')`, sales, syntheticTeam); err != nil {
		t.Fatal(err)
	}
	// Sessions that must be revoked by the switch.
	if _, err := db.Exec(ctx, `INSERT INTO app_sessions(token_hash,subject,csrf,expires_at,issuer,identity_epoch)
	 SELECT 'rc0-'||g,$1,'c',now()+interval '1 hour',i.issuer,i.epoch FROM identity_epoch i, generate_series(1,3) g`, admin); err != nil {
		t.Fatal(err)
	}
	// Without the link: refused (dry run too).
	if _, err := run("--realm", "baobei", "--takeover-admin", adminID, "--dry-run"); !errors.Is(err, errSwitchAdmin) {
		t.Fatal("dry-run without a linked admin", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO identity_links(issuer,external_subject,subject) VALUES($1,$2,$3)`, dedicated, newUUID(), admin); err != nil {
		t.Fatal(err)
	}
	// Wrong takeover (not an admin) and missing approval: refused.
	var salesID string
	_ = db.QueryRow(ctx, `SELECT staff_id::text FROM app_users WHERE subject=$1`, sales).Scan(&salesID)
	if _, err := run("--realm", "baobei", "--takeover-admin", salesID, "--dry-run"); !errors.Is(err, errSwitchAdmin) {
		t.Fatal("non-admin takeover accepted", err)
	}
	report, err := run("--realm", "baobei", "--takeover-admin", adminID, "--dry-run")
	if err != nil || report["precheck"] != "PASS" {
		t.Fatal("dry-run", report, err)
	}
	r := report["report"].(map[string]any)
	if r["cannotSignIn"].(float64) < 1 || r["sessionsToRevoke"].(float64) != 3 {
		t.Fatal("dry-run must report the unmigrated member and the sessions", r)
	}
	if _, err = run("--realm", "baobei", "--operator", "rc0-op", "--reason", "切换", "--takeover-admin", adminID); err == nil {
		t.Fatal("switch without an approval reference accepted")
	}
	res, err := run("--realm", "baobei", "--operator", "rc0-op", "--reason", "切换", "--takeover-admin", adminID, "--approval", "CHG-RC0-001")
	if err != nil || res["revokedSessions"].(float64) != 3 || res["cannotSignIn"].(float64) < 1 {
		t.Fatal("forward switch", res, err)
	}
	var cur string
	_ = db.QueryRow(ctx, `SELECT issuer FROM identity_epoch`).Scan(&cur)
	if cur != dedicated {
		t.Fatal("not switched", cur)
	}
	// A member opened in the dedicated realm (subject = its dedicated-realm id).
	opened := newUUID()
	if _, err = db.Exec(ctx, `INSERT INTO app_users(subject,display_name,role,team_id,active,staff_status,staff_managed,work_phone,account_state)
	 VALUES($1,'新 Realm 开户','baobei_sales',$2,true,'ACTIVE',true,'+8613980000003','READY')`, opened, syntheticTeam); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO identity_links(issuer,external_subject,subject) VALUES($1,$2,$2)`, dedicated, opened); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO app_sessions(token_hash,subject,csrf,expires_at,issuer,identity_epoch)
	 SELECT 'rc0-new',$1,'c',now()+interval '1 hour',i.issuer,i.epoch FROM identity_epoch i`, opened); err != nil {
		t.Fatal(err)
	}
	// Switch back (path A): the admin keeps the shared-realm subject; the member
	// opened in the dedicated realm cannot sign in there, and is reported.
	back, err := run("--realm", "crane", "--operator", "rc0-op", "--reason", "回切", "--takeover-admin", adminID, "--approval", "CHG-RC0-002")
	if err != nil || back["revokedSessions"].(float64) != 1 || back["cannotSignIn"].(float64) < 1 {
		t.Fatal("switch back must revoke sessions and report who cannot sign in", back, err)
	}
	var sessions int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM app_sessions`).Scan(&sessions)
	if sessions != 0 {
		t.Fatal("sessions survived a switch", sessions)
	}
	// History rows are untouched by switching.
	var audits int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='IDENTITY_SWITCH'`).Scan(&audits)
	if audits != 3 { // fixture + forward + back
		t.Fatal("switch audit", audits)
	}
}

// The real-Keycloak browser fixture hands its one-time credential over through
// a private file only (no stdout, no environment).
func TestRC0CredentialHandoffIsPrivate(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "credential.json")
	if err := checkPrivateHandoff("relative.json"); err == nil {
		t.Fatal("relative path accepted")
	}
	if err := checkPrivateHandoff(path); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateHandoff(path, map[string]string{"phone": "x", "temporaryPassword": "y"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatal("handoff file must be 0600", st.Mode(), err)
	}
	if err = checkPrivateHandoff(path); err == nil {
		t.Fatal("existing handoff file accepted (must be new)")
	}
	if err = writePrivateHandoff(path, map[string]string{}); err == nil {
		t.Fatal("handoff file overwritten")
	}
	open := t.TempDir()
	if err = os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = checkPrivateHandoff(filepath.Join(open, "c.json")); err == nil {
		t.Fatal("group/other-readable directory accepted")
	}
}

// Path B drill on an isolated PostgreSQL 18.4 instance: fresh backup of an
// 00005 database, upgrade to the current schema and dedicated realm, business
// writes after the backup, then restore of the backup. The backup is a
// file-level database clone (CREATE DATABASE … TEMPLATE); the pg_dump/pg_restore
// archive path is exercised by `task.mjs baobei restore-test` in CI.
func TestDatabaseRC0BackupUpgradeRestoreDrill(t *testing.T) {
	dsn := os.Getenv("BAOBEI_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NOT_RUN: test DB required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	suffix := hash(randomToken())[:10]
	live, backup := "rc0_live_"+suffix, "rc0_backup_"+suffix
	for _, name := range []string{live, backup} {
		n := name
		t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+n+` WITH (FORCE)`) })
	}
	if _, err = admin.Exec(ctx, `CREATE DATABASE `+live); err != nil {
		t.Fatal(err)
	}
	open := func(name string) *pgxpool.Pool {
		cfg, _ := pgxpool.ParseConfig(dsn)
		cfg.ConnConfig.Database = name
		p, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	db := open(live)
	if _, err = db.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_trgm; CREATE TABLE baobei_migrations(name text PRIMARY KEY, checksum text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, _ := migrations.ReadDir("migrations")
	for _, e := range entries {
		if e.Name() >= "00006" {
			break
		}
		b, _ := migrations.ReadFile("migrations/" + e.Name())
		if _, err = db.Exec(ctx, string(b)); err != nil {
			t.Fatal(e.Name(), err)
		}
		h := sha256.Sum256(b)
		_, _ = db.Exec(ctx, `INSERT INTO baobei_migrations VALUES($1,$2)`, e.Name(), hex.EncodeToString(h[:]))
	}
	// Pre-upgrade business history: a project with an approval, follow-up and audit.
	if _, err = db.Exec(ctx, `
	 INSERT INTO teams(id,name) VALUES('44444444-4444-4444-8444-444444444444','演练团队');
	 INSERT INTO app_users(subject,display_name,role,team_id) VALUES('aaaaaaaa-0000-4000-8000-0000000000d1','演练销售','baobei_sales','44444444-4444-4444-8444-444444444444');
	 INSERT INTO intake_projects(id,project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,team_id,document,intake_status,submission_revision,submitted_by_subject)
	  VALUES('55555555-5555-4555-8555-555555555555','演练项目','演练项目','aaaaaaaa-0000-4000-8000-0000000000d1','aaaaaaaa-0000-4000-8000-0000000000d1','aaaaaaaa-0000-4000-8000-0000000000d1','44444444-4444-4444-8444-444444444444','{}','APPROVED',1,'aaaaaaaa-0000-4000-8000-0000000000d1');
	 INSERT INTO approvals(project_id,subject,decision,reason) VALUES('55555555-5555-4555-8555-555555555555','aaaaaaaa-0000-4000-8000-0000000000d1','APPROVE','演练');
	 INSERT INTO project_activities(project_id,subject,type,occurred_at,summary,result,next_action) VALUES('55555555-5555-4555-8555-555555555555','aaaaaaaa-0000-4000-8000-0000000000d1','PHONE',now(),'演练跟进','','');
	 INSERT INTO audit_events(project_id,subject,action,revision) VALUES('55555555-5555-4555-8555-555555555555','aaaaaaaa-0000-4000-8000-0000000000d1','review.approve',1);`); err != nil {
		t.Fatal(err)
	}
	fingerprint := func(p *pgxpool.Pool) string {
		var s string
		if err := p.QueryRow(ctx, `SELECT md5(string_agg(x, '|' ORDER BY x)) FROM (
		 SELECT 'p:'||id||project_name||intake_status FROM intake_projects UNION ALL
		 SELECT 'a:'||project_id||decision||reason FROM approvals UNION ALL
		 SELECT 'f:'||project_id||summary FROM project_activities UNION ALL
		 SELECT 'e:'||project_id||action||revision FROM audit_events UNION ALL
		 SELECT 'm:'||name FROM baobei_migrations) t(x)`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := fingerprint(db)
	// FRESH_BACKUP_VERIFIED: clone (no other connection may be open).
	db.Close()
	if _, err = admin.Exec(ctx, `CREATE DATABASE `+backup+` TEMPLATE `+live); err != nil {
		t.Fatal(err)
	}
	bk := open(backup)
	if fingerprint(bk) != before {
		t.Fatal("backup does not match the source")
	}
	bk.Close()
	// SCHEMA_UPGRADE + IDENTITY_SWITCH on the live database, then writes.
	db = open(live)
	if err = migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = switchIdentity(ctx, db, deployedProfiles["production"].Realms["baobei"].Issuer, "drill-op", "演练切换"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO audit_events(project_id,subject,action,revision) VALUES('55555555-5555-4555-8555-555555555555','aaaaaaaa-0000-4000-8000-0000000000d1','activity.create',2)`); err != nil {
		t.Fatal(err)
	}
	after := fingerprint(db)
	if after == before {
		t.Fatal("upgrade had no effect")
	}
	// The pre-upgrade rows survive the upgrade unchanged.
	var kept int
	_ = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM approvals)+(SELECT count(*) FROM project_activities)+(SELECT count(*) FROM intake_projects)`).Scan(&kept)
	if kept != 3 {
		t.Fatal("history lost by the upgrade", kept)
	}
	// Writes made after the backup are what a path-B restore loses (RPO): they
	// must be exported before the restore. List them by time, never silently.
	var sinceBackup int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action IN ('activity.create','IDENTITY_SWITCH')`).Scan(&sinceBackup)
	if sinceBackup != 2 {
		t.Fatal("post-backup writes", sinceBackup)
	}
	db.Close()
	// RESTORE (path B): replace the live database with the backup clone.
	if _, err = admin.Exec(ctx, `DROP DATABASE `+live+` WITH (FORCE)`); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, `CREATE DATABASE `+live+` TEMPLATE `+backup); err != nil {
		t.Fatal(err)
	}
	db = open(live)
	defer db.Close()
	if fingerprint(db) != before {
		t.Fatal("restored database differs from the backup")
	}
	var hasEpoch bool
	_ = db.QueryRow(ctx, `SELECT to_regclass('identity_epoch') IS NOT NULL`).Scan(&hasEpoch)
	if hasEpoch {
		t.Fatal("restore left the new schema behind")
	}
	// The old API (pre-00006 session insert) works again on the restored schema.
	if _, err = db.Exec(ctx, oldAPISessionInsert, "tok-restored-"+randomToken(), "aaaaaaaa-0000-4000-8000-0000000000d1", "c"); err != nil {
		t.Fatal("old API after restore", err)
	}
}
