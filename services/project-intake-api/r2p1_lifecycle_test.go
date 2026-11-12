package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// G4-S5-R1-F2 and R2-P1, against real PostgreSQL 18.

// --- R1-F2-1: an identity link is written, or already exactly equal; never
// silently ignored, overwritten or deleted when it points to somebody else.

func TestDatabaseR1F2IdentityLinkConflict(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	issuer := f.app.sessionIssuer()

	t.Run("direct: equal row is accepted, different subject refused, nothing changed", func(t *testing.T) {
		a := f.invite("合成映射甲", "13951000001", salesRole)
		b := f.invite("合成映射乙", "13951000002", salesRole)
		ext := "aaaaaaaa-1111-4111-8111-000000000001"
		var subA, subB string
		_ = f.app.db.QueryRow(ctx, `SELECT subject FROM app_users WHERE staff_id=$1`, a.ID).Scan(&subA)
		_ = f.app.db.QueryRow(ctx, `SELECT subject FROM app_users WHERE staff_id=$1`, b.ID).Scan(&subB)
		tx, err := f.app.db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err = bindIdentityLink(ctx, tx, issuer, ext, subA); err != nil {
			t.Fatal("first bind", err)
		}
		if err = bindIdentityLink(ctx, tx, issuer, ext, subA); err != nil {
			t.Fatal("identical bind must be accepted", err)
		}
		if err = bindIdentityLink(ctx, tx, issuer, ext, subB); !errors.Is(err, accountIdentityConflict) {
			t.Fatal("bind to another member must be refused", err)
		}
		if err = bindIdentityLink(ctx, tx, issuer, "aaaaaaaa-1111-4111-8111-000000000002", subA); !errors.Is(err, accountIdentityConflict) {
			t.Fatal("second external id for the same member must be refused", err)
		}
		var owner string
		_ = tx.QueryRow(ctx, `SELECT subject FROM identity_links WHERE issuer=$1 AND external_subject=$2`, issuer, ext).Scan(&owner)
		if owner != subA {
			t.Fatal("existing mapping changed", owner)
		}
		// An existing link cannot be rewritten in place either (trigger BB003).
		_, err = tx.Exec(ctx, `UPDATE identity_links SET subject=$1 WHERE external_subject=$2`, subB, ext)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "BB003" {
			t.Fatal("identity link rewritten", err)
		}
	})

	t.Run("compensation: Keycloak created the user but the mapping conflicts", func(t *testing.T) {
		victim := f.invite("合成已映射成员", "13951000011", salesRole)
		var victimSubject string
		_ = f.app.db.QueryRow(ctx, `SELECT subject FROM app_users WHERE staff_id=$1`, victim.ID).Scan(&victimSubject)
		m := f.invite("合成冲突成员", "13951000012", salesRole)
		// Keycloak creates the user; a mapping of that id to another member already
		// exists (for example left by a migration import).
		orig := f.dev
		f.app.accounts = preMapped{devAccounts: orig, run: func(id string) {
			if _, err := f.app.db.Exec(ctx, `INSERT INTO identity_links(issuer,external_subject,subject) VALUES($1,$2,$3)`, issuer, id, victimSubject); err != nil {
				t.Error(err)
			}
		}}
		s, _, b := f.account(f.admin, m.ID, "create", "r1f2-conflict-00000001", map[string]string{})
		f.app.accounts = orig
		if s != 409 || !strings.Contains(string(b), "ACCOUNT_IDENTITY_CONFLICT") {
			t.Fatal("conflicting mapping must be refused", s, string(b))
		}
		// The other member's link is untouched (not deleted, not rewritten).
		var n int
		_ = f.app.db.QueryRow(ctx, `SELECT count(*) FROM identity_links WHERE subject=$1`, victimSubject).Scan(&n)
		if n != 1 {
			t.Fatal("other member's mapping changed or deleted", n)
		}
		// The new member is not enabled, holds no link, can retry; the Keycloak user
		// created by this attempt was removed.
		got := f.member(m.ID)
		if got.AccountState != accountFailed || got.IdentityLinked || got.Status != staffFailed {
			t.Fatalf("conflicting member: %+v", got)
		}
		if got.AccountOperation == nil || got.AccountOperation.Detail != "ACCOUNT_IDENTITY_CONFLICT" {
			t.Fatalf("cause must be visible: %+v", got.AccountOperation)
		}
		if n := f.dev.enabledFor("13951000012"); n != 0 {
			t.Fatal("enabled login left for the conflicting member", n)
		}
		if _, st := f.signIn("13951000012"); st == 200 {
			t.Fatal("conflicting member signed in")
		}
		if ops := f.ops(m.ID); ops["STARTED"] != 0 || ops["REVIEW"] != 0 {
			t.Fatal("operation left unresolved", ops)
		}
		// Retry after the stale mapping is cleared works.
		_, _ = f.app.db.Exec(ctx, `DELETE FROM identity_links WHERE subject=$1`, victimSubject)
		s, res, b := f.account(f.admin, m.ID, "create", "r1f2-conflict-00000002", map[string]string{})
		expect(t, s, 200, b)
		if res.Member.AccountState != accountReady {
			t.Fatal("retry", string(b))
		}
	})

	t.Run("race: two members bind the same external identity concurrently", func(t *testing.T) {
		a := f.invite("合成竞争甲", "13951000021", salesRole)
		b := f.invite("合成竞争乙", "13951000022", salesRole)
		ext := "aaaaaaaa-2222-4222-8222-000000000001"
		subjects := map[string]string{}
		for _, m := range []staffMember{a, b} {
			var s string
			_ = f.app.db.QueryRow(ctx, `SELECT subject FROM app_users WHERE staff_id=$1`, m.ID).Scan(&s)
			subjects[m.ID] = s
		}
		for round := 0; round < 10; round++ {
			_, _ = f.app.db.Exec(ctx, `DELETE FROM identity_links WHERE external_subject=$1`, ext)
			var wg sync.WaitGroup
			results := make([]error, 2)
			for i, m := range []staffMember{a, b} {
				wg.Add(1)
				go func(i int, subject string) {
					defer wg.Done()
					results[i] = writeMember(ctx, f.app.db, func(tx pgx.Tx) error { return bindIdentityLink(ctx, tx, issuer, ext, subject) })
				}(i, subjects[m.ID])
			}
			wg.Wait()
			ok := 0
			for _, e := range results {
				if e == nil {
					ok++
				} else if !errors.Is(e, accountIdentityConflict) {
					t.Fatal("unexpected error", e)
				}
			}
			var n int
			_ = f.app.db.QueryRow(ctx, `SELECT count(*) FROM identity_links WHERE external_subject=$1`, ext).Scan(&n)
			if ok != 1 || n != 1 {
				t.Fatal("exactly one member may own the identity", ok, n)
			}
		}
	})
}

// preMapped: the provider creates the user, then run() plants a conflicting
// mapping for the new id before Baobei binds it.
type preMapped struct {
	*devAccounts
	run func(id string)
}

func (p preMapped) createDisabled(ctx context.Context, username, staffID string) (string, error) {
	id, err := p.devAccounts.createDisabled(ctx, username, staffID)
	if err == nil {
		p.run(id)
	}
	return id, err
}

// --- R1-F2-2: a subject with business history never moves back.

func TestDatabaseR1F2SubjectHistoryGuard(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	newBound := func(name, phone string) (staffMember, string) {
		m := f.invite(name, phone, salesRole)
		ext := newUUID()
		if _, err := f.app.db.Exec(ctx, `UPDATE app_users SET account_state='CREATING' WHERE staff_id=$1`, m.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.app.db.Exec(ctx, `UPDATE app_users SET subject=$2 WHERE staff_id=$1`, m.ID, ext); err != nil {
			t.Fatal("bind", err)
		}
		return m, ext
	}
	back := func(m staffMember) error {
		_, err := f.app.db.Exec(ctx, `UPDATE app_users SET subject='pending:'||staff_id::text WHERE staff_id=$1`, m.ID)
		return err
	}
	bb002 := func(err error) bool {
		var pgErr *pgconn.PgError
		return errors.As(err, &pgErr) && pgErr.Code == "BB002"
	}
	// No history, never signed in: the unfinished binding may be undone.
	m, _ := newBound("合成无历史", "13952000001")
	if err := back(m); err != nil {
		t.Fatal("clean rollback refused", err)
	}
	// Each kind of history blocks the rollback, even with last_login_at NULL.
	project := Project{}
	for i, plant := range []string{
		`INSERT INTO audit_events(subject,action,revision) VALUES($1,'synthetic',1)`,
		`INSERT INTO staff_audit(target_id,actor,action,reason,version) VALUES(gen_random_uuid(),$1,'staff.edit','x',1)`,
		`INSERT INTO ownership_history(to_subject,reason,actor_subject) VALUES('synthetic-a','x',$1)`,
		`INSERT INTO account_operations(idempotency_key,staff_id,operation,actor_subject,state,request_hash) VALUES('r1f2-history-'||md5($1),(SELECT staff_id FROM app_users WHERE subject='synthetic-admin'),'DISABLE',$1,'DONE',repeat('0',64))`,
		`UPDATE intake_projects SET draft_custodian_subject=$1 WHERE id=(SELECT id FROM intake_projects LIMIT 1)`,
	} {
		m, ext := newBound("合成历史"+string(rune('A'+i)), "1395200001"+string(rune('0'+i)))
		if strings.Contains(plant, "intake_projects") {
			s, b := f.admin.call("POST", "/api/v1/projects", sample("合成历史项目"))
			project = projectFrom(t, s, b)
		}
		if _, err := f.app.db.Exec(ctx, plant, ext); err != nil {
			t.Fatal(i, err)
		}
		if err := back(m); !bb002(err) {
			t.Fatal("history did not protect the subject", i, err)
		}
	}
	_ = project
	// Through the account service: a compensation that would roll back a subject
	// with history parks the member in REVIEW instead and keeps the subject.
	m2 := f.invite("合成补偿历史", "13952000021", salesRole)
	f.app.fault = func(p string) error {
		if p == "db:step:ENABLE" {
			var sub string
			_ = f.app.db.QueryRow(ctx, `SELECT subject FROM app_users WHERE staff_id=$1`, m2.ID).Scan(&sub)
			if _, err := f.app.db.Exec(ctx, `INSERT INTO audit_events(subject,action,revision) VALUES($1,'synthetic.history',1)`, sub); err != nil {
				return err
			}
			f.dev.failNext = "update"
		}
		return nil
	}
	s, _, b := f.account(f.admin, m2.ID, "create", "r1f2-history-comp-0001", map[string]string{})
	f.app.fault = nil
	if s != 409 || !strings.Contains(string(b), "ACCOUNT_REVIEW_REQUIRED") {
		t.Fatal("expected REVIEW", s, string(b))
	}
	got := f.member(m2.ID)
	if got.AccountState != accountReview || !got.IdentityLinked || got.AccountOperation == nil || got.AccountOperation.Detail != "SUBJECT_HAS_HISTORY" {
		t.Fatalf("subject with history must stay bound and be reviewed: %+v %+v", got, got.AccountOperation)
	}
	if _, st := f.signIn("13952000021"); st == 200 {
		t.Fatal("REVIEW member signed in")
	}
}

// --- R2-P1-A: revoke an unfinished invitation.

func TestDatabaseR2P1RevokeInvitation(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	revoke := func(c testClient, m staffMember, reason string) (int, []byte) {
		return c.call("POST", "/api/v1/staff/"+m.ID+"/revoke", map[string]any{"version": m.Version, "reason": reason})
	}
	// 1. No account yet: revoke directly; phone released; re-invite works.
	m := f.invite("合成撤销甲", "13953000001", salesRole)
	s, b := revoke(f.admin, m, "")
	expect(t, s, 400, b)
	s, b = f.admin.call("POST", "/api/v1/staff/"+m.ID+"/revoke", map[string]any{"version": m.Version + 1, "reason": "信息错误"})
	expect(t, s, 409, b) // stale version
	managerOther := login(t, f.server, "synthetic-manager-other")
	s, b = revoke(managerOther, m, "越权")
	if s != 404 && s != 403 {
		t.Fatal("other team manager revoked", s, string(b))
	}
	sales := login(t, f.server, "synthetic-a")
	s, b = revoke(sales, m, "越权")
	expect(t, s, 403, b)
	s, b = revoke(f.admin, m, "手机号填错")
	expect(t, s, 200, b)
	var got staffMember
	_ = json.Unmarshal(b, &got)
	if got.Status != staffRevoked || got.Phone != "" || got.CanManage {
		t.Fatalf("after revoke: %+v", got)
	}
	s, b = revoke(f.admin, got, "再次撤销")
	expect(t, s, 403, b) // revoked rows are read-only
	again := f.invite("合成撤销甲", "13953000001", salesRole)
	if again.ID == m.ID || again.Status != staffPending {
		t.Fatal("re-invite with the released phone", again)
	}
	s, b = f.admin.call("GET", "/api/v1/staff/"+m.ID+"/audit", nil)
	if s != 200 || !strings.Contains(string(b), "staff.revoke") || !strings.Contains(string(b), "****") {
		t.Fatal("revocation audit", string(b))
	}
	s, b = f.admin.call("GET", "/api/v1/staff?status=REVOKED", nil)
	if s != 200 || !strings.Contains(string(b), m.ID) {
		t.Fatal("revoked filter", string(b))
	}

	// 2. A manager may revoke an unfinished sales invitation of the own team.
	mgr := login(t, f.server, "synthetic-manager")
	s, b = mgr.call("POST", "/api/v1/staff/invitations", staffProfileInput{Name: "合成主管邀请", Phone: "13953000002", Role: salesRole, TeamID: syntheticTeam, Reason: "x"})
	mm := memberFrom(t, s, b)
	s, b = revoke(mgr, mm, "重复添加")
	expect(t, s, 200, b)

	// 3. Account created but never used: the plain route refuses, the account
	// service deletes the Keycloak user and revokes in one controlled operation.
	c := f.invite("合成撤销已开户", "13953000003", salesRole)
	s, _, b = f.account(f.admin, c.ID, "create", "r2p1-revoke-create-001", map[string]string{})
	expect(t, s, 200, b)
	c = f.member(c.ID)
	s, b = revoke(f.admin, c, "撤销")
	if s != 409 || !strings.Contains(string(b), "STAFF_REVOKE_ACCOUNT_EXISTS") {
		t.Fatal("plain revoke must refuse a member with a login", s, string(b))
	}
	// Keycloak failure: nothing half-done, login not usable, member reviewable.
	f.dev.down = true
	s, _, b = f.account(f.admin, c.ID, "revoke", "r2p1-revoke-kc-down-01", map[string]string{"reason": "撤销"})
	f.dev.down = false
	if s != 409 || !strings.Contains(string(b), "ACCOUNT_REVIEW_REQUIRED") {
		t.Fatal("unconfirmed removal parks for review", s, string(b))
	}
	if _, st := f.signIn("13953000003"); st == 200 {
		t.Fatal("member in REVIEW signed in")
	}
	// Reconcile completes the revocation once Keycloak answers.
	s, b = f.admin.call("POST", "/api/v1/staff/"+c.ID+"/account/reconcile", map[string]any{})
	expect(t, s, 200, b)
	got = f.member(c.ID)
	if got.Status != staffRevoked || got.IdentityLinked || got.AccountState != accountNone {
		t.Fatalf("after reconcile: %+v", got)
	}
	if n := f.dev.enabledFor("13953000003"); n != 0 {
		t.Fatal("Keycloak user left", n)
	}
	var links int
	_ = f.app.db.QueryRow(ctx, `SELECT count(*) FROM identity_links l JOIN app_users u ON u.subject=l.subject WHERE u.staff_id=$1`, c.ID).Scan(&links)
	if links != 0 {
		t.Fatal("identity link left", links)
	}
	if ops := f.ops(c.ID); ops["STARTED"] != 0 || ops["REVIEW"] != 0 {
		t.Fatal("operation unresolved", ops)
	}
	// Replay of the same idempotency key: no second action.
	again2 := f.invite("合成撤销已开户", "13953000003", salesRole)
	if again2.Status != staffPending {
		t.Fatal("phone not released", again2)
	}

	// 4. History: a member who signed in, or is named by a project, cannot be revoked.
	h := f.invite("合成有历史", "13953000004", salesRole)
	s, _, b = f.account(f.admin, h.ID, "create", "r2p1-revoke-history-01", map[string]string{})
	expect(t, s, 200, b)
	if _, st := f.signIn("13953000004"); st != 200 {
		t.Fatal("sign in", st)
	}
	h = f.member(h.ID)
	s, _, b = f.account(f.admin, h.ID, "revoke", "r2p1-revoke-history-02", map[string]string{"reason": "撤销"})
	if s != 409 || !strings.Contains(string(b), "STAFF_NOT_REVOCABLE") && !strings.Contains(string(b), "STAFF_HAS_HISTORY") {
		t.Fatal("member with history revoked", s, string(b))
	}
	p := f.invite("合成项目引用", "13953000005", salesRole)
	if _, err := f.app.db.Exec(ctx, `INSERT INTO audit_events(subject,action,revision) SELECT subject,'synthetic',1 FROM app_users WHERE staff_id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	s, b = revoke(f.admin, p, "撤销")
	if s != 409 || !strings.Contains(string(b), "STAFF_HAS_HISTORY") {
		t.Fatal("referenced member revoked", s, string(b))
	}
}

// --- R2-P1-B: team archive and restore.

func TestDatabaseR2P1TeamArchive(t *testing.T) {
	f := newAccountFixture(t)
	call := func(c testClient, method, path string, body any) (int, staffTeam, []byte) {
		s, b := c.call(method, path, body)
		var tv staffTeam
		_ = json.Unmarshal(b, &tv)
		return s, tv, b
	}
	s, tm, b := call(f.admin, "POST", "/api/v1/teams", map[string]any{"name": "合成归档团队", "reason": "新建"})
	expect(t, s, 200, b)
	if tm.Status != "ACTIVE" || tm.Members != 0 {
		t.Fatalf("new team %+v", tm)
	}
	s, tm, b = call(f.admin, "PATCH", "/api/v1/teams/"+tm.ID, map[string]any{"name": "合成归档团队二", "reason": "改名", "version": tm.Version})
	expect(t, s, 200, b)
	m := f.invite("合成团队成员", "13954000001", salesRole)
	s, b = f.admin.call("PATCH", "/api/v1/staff/"+m.ID, staffProfileInput{Name: m.Name, Phone: m.Phone, Role: salesRole, TeamID: tm.ID, Reason: "调入", Version: m.Version})
	expect(t, s, 200, b)
	m = f.member(m.ID)
	// Members still attached: refused with the count, nothing changed.
	s, _, b = call(f.admin, "POST", "/api/v1/teams/"+tm.ID+"/archive", map[string]any{"version": tm.Version, "reason": "合并"})
	if s != 409 || !strings.Contains(string(b), "TEAM_HAS_MEMBERS") {
		t.Fatal("archive with members", s, string(b))
	}
	// Non-admins cannot archive; stale versions conflict.
	mgr := login(t, f.server, "synthetic-manager")
	s, _, b = call(mgr, "POST", "/api/v1/teams/"+tm.ID+"/archive", map[string]any{"version": tm.Version, "reason": "x"})
	expect(t, s, 403, b)
	s, b = f.admin.call("POST", "/api/v1/staff/"+m.ID+"/revoke", map[string]any{"version": m.Version, "reason": "离开团队"})
	expect(t, s, 200, b)
	s, _, b = call(f.admin, "POST", "/api/v1/teams/"+tm.ID+"/archive", map[string]any{"version": tm.Version - 1, "reason": "合并"})
	expect(t, s, 409, b)
	s, archived, b := call(f.admin, "POST", "/api/v1/teams/"+tm.ID+"/archive", map[string]any{"version": tm.Version, "reason": "合并"})
	expect(t, s, 200, b)
	if archived.Status != "ARCHIVED" || archived.ArchivedAt == nil {
		t.Fatalf("archived %+v", archived)
	}
	s, _, b = call(f.admin, "POST", "/api/v1/teams/"+tm.ID+"/archive", map[string]any{"version": archived.Version, "reason": "再次"})
	expect(t, s, 409, b)
	// Archived: not selectable, not renamable; the name stays reserved.
	s, b = f.admin.call("POST", "/api/v1/staff/invitations", staffProfileInput{Name: "合成新成员", Phone: "13954000002", Role: salesRole, TeamID: tm.ID, Reason: "x"})
	if s != 409 || !strings.Contains(string(b), "TEAM_ARCHIVED") {
		t.Fatal("archived team selectable", s, string(b))
	}
	s, _, b = call(f.admin, "PATCH", "/api/v1/teams/"+tm.ID, map[string]any{"name": "改", "reason": "x", "version": archived.Version})
	expect(t, s, 409, b)
	s, _, b = call(f.admin, "POST", "/api/v1/teams", map[string]any{"name": "合成归档团队二", "reason": "同名"})
	if s != 409 || !strings.Contains(string(b), "TEAM_NAME_DUPLICATE") {
		t.Fatal("duplicate name", s, string(b))
	}
	// History visible; restore makes it usable again.
	s, b = f.admin.call("GET", "/api/v1/teams/"+tm.ID, nil)
	if s != 200 || !strings.Contains(string(b), "team.archive") || !strings.Contains(string(b), "team.edit") {
		t.Fatal("team history", string(b))
	}
	s, restored, b := call(f.admin, "POST", "/api/v1/teams/"+tm.ID+"/restore", map[string]any{"version": archived.Version, "reason": "恢复使用"})
	expect(t, s, 200, b)
	if restored.Status != "ACTIVE" || restored.ArchivedAt != nil {
		t.Fatalf("restored %+v", restored)
	}
	s, b = f.admin.call("POST", "/api/v1/staff/invitations", staffProfileInput{Name: "合成新成员", Phone: "13954000002", Role: salesRole, TeamID: tm.ID, Reason: "x"})
	expect(t, s, 200, b)
	// Open projects block archiving too (Synthetic Team owns synthetic-a's projects).
	ca := login(t, f.server, "synthetic-a")
	s, b = ca.call("POST", "/api/v1/projects", sample("合成团队在办项目"))
	pr := projectFrom(t, s, b)
	s, b = ca.call("POST", "/api/v1/projects/"+pr.ID+"/submit", map[string]int{"version": pr.Version})
	expect(t, s, 200, b)
	var team staffTeam
	_ = f.app.db.QueryRow(context.Background(), `SELECT staff_version FROM teams WHERE id=$1`, syntheticTeam).Scan(&team.Version)
	s, _, b = call(f.admin, "POST", "/api/v1/teams/"+syntheticTeam+"/archive", map[string]any{"version": team.Version, "reason": "x"})
	if s != 409 || !strings.Contains(string(b), "TEAM_HAS_") {
		t.Fatal("team with members/projects archived", s, string(b))
	}
}

// --- R2-P1-C: draft discard and restore.

func TestDatabaseR2P1DraftDiscardRestore(t *testing.T) {
	f := newAccountFixture(t)
	ca, cb := login(t, f.server, "synthetic-a"), login(t, f.server, "synthetic-b")
	mgr := login(t, f.server, "synthetic-manager")
	in := sample("合成废弃草稿")
	in.RequirementNotes = "保留的需求说明"
	s, b := ca.call("POST", "/api/v1/projects", in)
	p := projectFrom(t, s, b)
	if p.Actions["discard"].Allowed != true || p.Actions["restore"].Allowed {
		t.Fatalf("hints %+v", p.Actions)
	}
	before := p
	// Permission negatives: other sales and manager cannot discard.
	s, b = cb.call("POST", "/api/v1/projects/"+p.ID+"/discard", map[string]any{"version": p.Version, "reason": "合成废弃"})
	expect(t, s, 404, b)
	s, b = mgr.call("POST", "/api/v1/projects/"+p.ID+"/discard", map[string]any{"version": p.Version, "reason": "合成废弃"})
	if s != 403 && s != 404 {
		t.Fatal("manager discarded", s, string(b))
	}
	s, b = ca.call("POST", "/api/v1/projects/"+p.ID+"/discard", map[string]any{"version": p.Version - 1, "reason": "合成废弃"})
	expect(t, s, 409, b)
	// Concurrent double click: exactly one succeeds.
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = ca.call("POST", "/api/v1/projects/"+p.ID+"/discard", map[string]any{"version": p.Version, "reason": "客户取消"})
		}(i)
	}
	wg.Wait()
	if codes[0]+codes[1] != 200+409 {
		t.Fatal("double submit", codes)
	}
	s, b = ca.call("GET", "/api/v1/projects/"+p.ID, nil)
	d := projectFrom(t, s, b)
	if d.IntakeStatus != "CANCELLED" || d.DiscardedAt == nil || !d.Actions["restore"].Allowed || d.Actions["edit"].Allowed {
		t.Fatalf("discarded %+v %+v", d.IntakeStatus, d.Actions)
	}
	// Not in lists, search, pipeline counts or home; present in the bin.
	s, b = ca.call("GET", "/api/v1/projects?q=合成废弃草稿", nil)
	if s != 200 || strings.Contains(string(b), p.ID) {
		t.Fatal("discarded draft in search", string(b))
	}
	s, b = ca.call("GET", "/api/v1/projects?bin=discarded", nil)
	if s != 200 || !strings.Contains(string(b), p.ID) {
		t.Fatal("recycle bin", string(b))
	}
	s, b = ca.call("GET", "/api/v1/projects?bin=x", nil)
	expect(t, s, 400, b)
	s, b = ca.call("GET", "/api/v1/workspace-summary", nil)
	if s != 200 || strings.Contains(string(b), p.ID) {
		t.Fatal("home shows discarded draft", string(b))
	}
	// Editing or submitting a discarded draft is refused.
	s, b = ca.call("POST", "/api/v1/projects/"+p.ID+"/submit", map[string]int{"version": d.Version})
	expect(t, s, 409, b)
	// Restore: content, equipment, customer and ownership unchanged.
	s, b = cb.call("POST", "/api/v1/projects/"+p.ID+"/restore", map[string]any{"version": d.Version})
	expect(t, s, 404, b)
	s, b = ca.call("POST", "/api/v1/projects/"+p.ID+"/restore", map[string]any{"version": d.Version, "reason": "客户恢复"})
	r := projectFrom(t, s, b)
	if r.IntakeStatus != "DRAFT" || r.DiscardedAt != nil || r.Customer != before.Customer || r.RequirementNotes != "保留的需求说明" ||
		len(r.PlannedEquipment) != len(before.PlannedEquipment) || r.OwnerSubject != before.OwnerSubject || r.TeamID != before.TeamID || r.DocumentHash != before.DocumentHash {
		t.Fatalf("restore changed content: %+v", r)
	}
	// Continue editing and submit.
	r.Input.NextAction = "继续跟进"
	r.Input.Version = r.Version
	s, b = ca.call("PATCH", "/api/v1/projects/"+p.ID, r.Input)
	r = projectFrom(t, s, b)
	s, b = ca.call("POST", "/api/v1/projects/"+p.ID+"/submit", map[string]int{"version": r.Version})
	sub := projectFrom(t, s, b)
	// Submitted projects cannot be discarded (and never hard-deleted).
	s, b = ca.call("POST", "/api/v1/projects/"+p.ID+"/discard", map[string]any{"version": sub.Version, "reason": "合成废弃"})
	expect(t, s, 409, b)
	s, b = f.admin.call("GET", "/api/v1/projects/"+p.ID, nil)
	if s != 200 || !strings.Contains(string(b), "project.discard") || !strings.Contains(string(b), "project.restore") {
		t.Fatal("audit trail", string(b))
	}
	// Admin can discard an unassigned draft and restore it.
	s, b = f.admin.call("POST", "/api/v1/projects", sample("合成管理员草稿"))
	ad := projectFrom(t, s, b)
	s, b = f.admin.call("POST", "/api/v1/projects/"+ad.ID+"/discard", map[string]any{"version": ad.Version, "reason": "合成废弃"})
	ad = projectFrom(t, s, b)
	s, b = f.admin.call("POST", "/api/v1/projects/"+ad.ID+"/restore", map[string]any{"version": ad.Version})
	expect(t, s, 200, b)
}
