package main

import (
	"crypto/sha256"
	"fmt"

	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
)

// confirmProfile confirms the caller's current name (or name) through the API.
func confirmProfile(t *testing.T, c testClient, name string) ownProfile {
	t.Helper()
	s, b := c.call("GET", "/api/v1/me/profile", nil)
	expect(t, s, 200, b)
	var p ownProfile
	_ = json.Unmarshal(b, &p)
	if name == "" {
		name = p.Name
	}
	s, b = c.call("PATCH", "/api/v1/me/profile", map[string]any{"name": name, "reason": "", "version": p.Version})
	expect(t, s, 200, b)
	_ = json.Unmarshal(b, &p)
	return p
}

// V1.1 R3 (AC-16, AC-17): first-login name confirmation.
func TestDatabaseV11ProfileConfirmation(t *testing.T) {
	f := g1FixtureFor(t)
	ctx := context.Background()
	g1Exec(t, f.db, `UPDATE app_users SET profile_completed_at=NULL,profile_name_source='UNCONFIRMED',display_name='占位-UAT-07' WHERE subject='synthetic-b'`)
	b := login(t, f.server, "synthetic-b")
	// Session works; business API is gated with a stable code.
	s, body := b.call("GET", "/api/v1/me", nil)
	expect(t, s, 200, body)
	if !strings.Contains(string(body), `"profileCompleted":false`) {
		t.Fatal(string(body))
	}
	for _, req := range [][2]string{{"GET", "/api/v1/projects"}, {"GET", "/api/v1/workspace-summary"}, {"POST", "/api/v1/projects"}, {"GET", "/api/v1/analytics/overview"}} {
		s, body = b.call(req[0], req[1], sample("未确认姓名"))
		if s != 403 || !strings.Contains(string(body), "PROFILE_INCOMPLETE") {
			t.Fatal(req, s, string(body))
		}
	}
	// Sign-out stays available.
	s, body = b.call("POST", "/auth/logout", map[string]any{})
	if s >= 400 {
		t.Fatal("logout gated", s, string(body))
	}
	b = login(t, f.server, "synthetic-b")
	// Only the name may be sent: role/team/subject fields are rejected.
	s, body = b.call("GET", "/api/v1/me/profile", nil)
	expect(t, s, 200, body)
	var p ownProfile
	_ = json.Unmarshal(body, &p)
	if p.Completed || p.Source != "UNCONFIRMED" || p.Name != "占位-UAT-07" {
		t.Fatalf("%+v", p)
	}
	for _, extra := range []map[string]any{{"role": "baobei_admin"}, {"teamId": "22222222-2222-4222-8222-222222222222"}, {"subject": "synthetic-admin"}, {"phone": "13900000000"}} {
		in := map[string]any{"name": "王小二", "reason": "", "version": p.Version}
		for k, v := range extra {
			in[k] = v
		}
		s, body = b.call("PATCH", "/api/v1/me/profile", in)
		expect(t, s, 400, body)
	}
	for _, bad := range []string{"", "   ", "13900000000", strings.Repeat("名", 65)} {
		s, body = b.call("PATCH", "/api/v1/me/profile", map[string]any{"name": bad, "reason": "", "version": p.Version})
		if s != 400 || !strings.Contains(string(body), "PROFILE_NAME_INVALID") {
			t.Fatal(bad, s, string(body))
		}
	}
	s, body = b.call("PATCH", "/api/v1/me/profile", map[string]any{"name": "王小二", "reason": "", "version": p.Version + 5})
	expect(t, s, 409, body)
	before := countRows(t, f.db, `SELECT count(*) FROM profile_audit`)
	p = confirmProfile(t, b, "王小二")
	// History: the fixture's direct rename (recorded by the 00010 trigger as a
	// staff edit by the database role) and the member's confirmation.
	if !p.Completed || p.Source != "SELF_CONFIRMED" || p.Name != "王小二" || len(p.History) != 2 || p.History[0].OldName != "占位-UAT-07" || p.History[0].Action != "profile.confirm" || p.History[1].Action != "profile.staff-edit" {
		t.Fatalf("%+v", p)
	}
	var role, team, subject string
	_ = f.db.QueryRow(ctx, `SELECT role,team_id::text,subject FROM app_users WHERE display_name='王小二'`).Scan(&role, &team, &subject)
	if role != salesRole || team != syntheticTeam || subject != "synthetic-b" {
		t.Fatal("identity changed", role, team, subject)
	}
	// The new name is what others see; the business API is open now.
	s, body = b.call("GET", "/api/v1/me", nil)
	if !strings.Contains(string(body), `"name":"王小二"`) {
		t.Fatal(string(body))
	}
	pr := g1Create(t, b, "V11改名后项目")
	if v11Get(t, f.manager, pr.ID).OwnerName != "王小二" {
		t.Fatal("owner name not updated")
	}
	// A later correction is a rename with its own audit row; history is immutable.
	p = confirmProfile(t, b, "王晓二")
	if len(p.History) != 3 || p.History[0].Action != "profile.rename" || countRows(t, f.db, `SELECT count(*) FROM profile_audit`) != before+2 {
		t.Fatalf("%+v", p.History)
	}
	if _, err := f.db.Exec(ctx, `UPDATE profile_audit SET new_name='x'`); !isCode(err, "BB002") {
		t.Fatal("profile audit mutable", err)
	}
	// The definer function only acts for a live session: a forged token hash or
	// a subject string is refused (no caller-chosen target).
	if _, err := f.db.Exec(ctx, `SELECT confirm_own_profile('synthetic-a','伪造','',1)`); !isCode(err, "BB011") {
		t.Fatal("confirm_own_profile accepted a non-session argument", err)
	}
	// A name change through staff management is audited with old/new and actor.
	var staffVersion int
	_ = f.db.QueryRow(ctx, `SELECT staff_version FROM app_users WHERE subject='synthetic-b'`).Scan(&staffVersion)
	s, body = f.manager.call("PATCH", "/api/v1/staff/"+staffIDOf(t, f.db, "synthetic-b"), map[string]any{"name": "王晓二（主管更正）", "phone": "", "email": "synthetic-b@example.test", "teamId": syntheticTeam, "role": salesRole, "reason": "主管更正姓名", "version": staffVersion})
	expect(t, s, 200, body)
	var actor, oldName, newName string
	if err := f.db.QueryRow(ctx, `SELECT actor_subject,old_name,new_name FROM profile_audit WHERE action='profile.staff-edit' ORDER BY at DESC LIMIT 1`).Scan(&actor, &oldName, &newName); err != nil || actor != "synthetic-manager" || oldName != "王晓二" || newName != "王晓二（主管更正）" {
		t.Fatal("staff rename audit", actor, oldName, newName, err)
	}
	// Someone else's profile cannot be touched: the endpoint has no target.
	s, body = f.manager.call("PATCH", "/api/v1/me/profile", map[string]any{"name": "示例主管", "reason": "", "version": 1, "staffId": staffIDOf(t, f.db, "synthetic-b")})
	expect(t, s, 400, body)
}

// The 00010 backfill marks only trusted, signed-in members; names unchanged.
func TestDatabaseV11ProfileBackfill(t *testing.T) {
	db := b2Database(t)
	ctx := context.Background()
	old := "v11_p_" + hash(randomToken())[:12]
	g1Exec(t, db, `CREATE SCHEMA `+old)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DROP SCHEMA `+old+` CASCADE`) })
	cfg := db.Config().Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = old + ",public"
	pool := mustPool(t, cfg)
	g1Exec(t, pool, `CREATE TABLE baobei_migrations(name text PRIMARY KEY, checksum text NOT NULL)`)
	applyUpTo(t, pool, "00010")
	for _, sql := range []string{
		`INSERT INTO teams(id,name) VALUES('44444444-4444-4444-8444-444444444444','旧团队')`,
		// activated + signed in (staff managed): trusted
		`INSERT INTO app_users(staff_id,subject,display_name,role,team_id,staff_managed,staff_status,active,last_login_at) VALUES('aaaaaaaa-0000-4000-8000-0000000000a1','s-trusted','张三','baobei_sales','44444444-4444-4444-8444-444444444444',true,'ACTIVE',true,now())`,
		`INSERT INTO staff_audit(target_id,actor,action,reason,version) VALUES('aaaaaaaa-0000-4000-8000-0000000000a1','admin','staff.activate','x',1)`,
		// staff managed, signed in, never activated: confirm
		`INSERT INTO app_users(staff_id,subject,display_name,role,team_id,staff_managed,staff_status,active,last_login_at) VALUES('aaaaaaaa-0000-4000-8000-0000000000a2','s-noact','UAT 测试员','baobei_sales','44444444-4444-4444-8444-444444444444',true,'ACTIVE',true,now())`,
		// manifest import, signed in: trusted
		`INSERT INTO app_users(staff_id,subject,display_name,role,team_id,staff_managed,staff_status,active,last_login_at) VALUES('aaaaaaaa-0000-4000-8000-0000000000a3','s-manifest','李四','baobei_manager','44444444-4444-4444-8444-444444444444',false,'ACTIVE',true,now())`,
		// manifest import, never signed in: confirm
		`INSERT INTO app_users(staff_id,subject,display_name,role,team_id,staff_managed,staff_status,active) VALUES('aaaaaaaa-0000-4000-8000-0000000000a4','s-never','王五','baobei_sales','44444444-4444-4444-8444-444444444444',false,'ACTIVE',true)`,
		// suspended: confirm when back
		`INSERT INTO app_users(staff_id,subject,display_name,role,team_id,staff_managed,staff_status,active,last_login_at) VALUES('aaaaaaaa-0000-4000-8000-0000000000a5','s-susp','赵六','baobei_sales','44444444-4444-4444-8444-444444444444',true,'SUSPENDED',false,now())`,
	} {
		g1Exec(t, pool, sql)
	}
	var names string
	_ = pool.QueryRow(ctx, `SELECT string_agg(subject||'='||display_name,',' ORDER BY subject) FROM app_users`).Scan(&names)
	if err := migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var after, done string
	_ = pool.QueryRow(ctx, `SELECT string_agg(subject||'='||display_name,',' ORDER BY subject) FROM app_users`).Scan(&after)
	_ = pool.QueryRow(ctx, `SELECT string_agg(subject,',' ORDER BY subject) FROM app_users WHERE profile_completed_at IS NOT NULL`).Scan(&done)
	if after != names || done != "s-manifest,s-trusted" {
		t.Fatal("backfill", done, after)
	}
	if countRows(t, pool, `SELECT count(*) FROM profile_audit WHERE action='profile.backfill'`) != 2 {
		t.Fatal("backfill audit")
	}
}

func mustPool(t *testing.T, cfg *pgxpool.Config) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// applyUpTo applies the embedded migrations below `before` (e.g. "00010").
func applyUpTo(t *testing.T, pool *pgxpool.Pool, before string) {
	t.Helper()
	entries, _ := migrations.ReadDir("migrations")
	for _, e := range entries {
		if e.Name() >= before {
			break
		}
		body, _ := migrations.ReadFile("migrations/" + e.Name())
		g1Exec(t, pool, string(body))
		g1Exec(t, pool, `INSERT INTO baobei_migrations VALUES($1,$2)`, e.Name(), fmt.Sprintf("%x", sha256.Sum256(body)))
	}
}
