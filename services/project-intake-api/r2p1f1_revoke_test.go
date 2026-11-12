package main

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// G4-S5-R2-P1-F1: revoking an unused account never touches another member's
// Keycloak user and never releases a phone while its login name is still held.
// Real PostgreSQL 18.4; Keycloak is the in-memory provider with fault injection.

// guardedAccounts records every mutating call per user id and can make the next
// delete fail (refused, or acted-then-timeout via devAccounts.lostNext).
type guardedAccounts struct {
	*devAccounts
	mu       sync.Mutex
	mutated  map[string][]string
	failDel  bool
	failFind bool
}

func newGuarded(d *devAccounts) *guardedAccounts {
	return &guardedAccounts{devAccounts: d, mutated: map[string][]string{}}
}
func (g *guardedAccounts) note(id, what string) {
	g.mu.Lock()
	g.mutated[id] = append(g.mutated[id], what)
	g.mu.Unlock()
}
func (g *guardedAccounts) touched(id string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.mutated[id]...)
}
func (g *guardedAccounts) delete(ctx context.Context, id string) error {
	g.note(id, "delete")
	if g.failDel {
		return errAccountProvider
	}
	return g.devAccounts.delete(ctx, id)
}
func (g *guardedAccounts) update(ctx context.Context, id string, u kcUser) error {
	g.note(id, "update")
	return g.devAccounts.update(ctx, id, u)
}
func (g *guardedAccounts) setExactRole(ctx context.Context, id, role string) error {
	g.note(id, "role")
	return g.devAccounts.setExactRole(ctx, id, role)
}
func (g *guardedAccounts) setTemporaryPassword(ctx context.Context, id, p string) error {
	g.note(id, "password")
	return g.devAccounts.setTemporaryPassword(ctx, id, p)
}
func (g *guardedAccounts) findByUsername(ctx context.Context, name string) (*kcUser, error) {
	if g.failFind {
		return nil, errAccountProvider
	}
	return g.devAccounts.findByUsername(ctx, name)
}

// openedMember invites and opens a login for a member (never used).
func (f *accountFixture) openedMember(name, phone, key string) staffMember {
	f.t.Helper()
	m := f.invite(name, phone, salesRole)
	s, _, b := f.account(f.admin, m.ID, "create", key, map[string]string{})
	expect(f.t, s, 200, b)
	return f.member(m.ID)
}

func (f *accountFixture) kcIDOf(staffID string) string {
	f.t.Helper()
	var id string
	if err := f.app.db.QueryRow(context.Background(), `SELECT l.external_subject FROM identity_links l JOIN app_users u ON u.subject=l.subject WHERE u.staff_id=$1`, staffID).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// phoneState: business phone kept, link kept, REVIEW, no completed revocation.
func (f *accountFixture) stillReserved(m staffMember, phone, cause string) {
	f.t.Helper()
	got := f.member(m.ID)
	if got.Status == staffRevoked || got.Phone != "+86"+phone || !got.IdentityLinked {
		f.t.Fatalf("revocation must not complete: %+v", got)
	}
	if got.AccountState != accountReview || got.AccountOperation == nil || got.AccountOperation.State != "REVIEW" || got.AccountOperation.Detail != cause {
		f.t.Fatalf("expected REVIEW %s, got %+v %+v", cause, got.AccountState, got.AccountOperation)
	}
	// The phone is not released: a new member cannot take it.
	s, b := f.admin.call("POST", "/api/v1/staff/invitations", staffProfileInput{Name: "合成抢号", Phone: phone, Role: salesRole, TeamID: syntheticTeam, Reason: "x"})
	if s != 409 || !strings.Contains(string(b), "STAFF_PHONE_DUPLICATE") {
		f.t.Fatal("phone released while the revocation is unconfirmed", s, string(b))
	}
	if _, st := f.signIn(phone); st == 200 {
		f.t.Fatal("member under review signed in")
	}
}

func TestDatabaseR2P1F1RevokeSafety(t *testing.T) {
	ctx := context.Background()

	t.Run("1 user owned by another member: never deleted, disabled or changed", func(t *testing.T) {
		f := newAccountFixture(t)
		g := newGuarded(f.dev)
		f.app.accounts = g
		victim := f.openedMember("合成他人", "13960000001", "f1-victim-0000000001")
		m := f.openedMember("合成撤销目标", "13960000002", "f1-target-0000000001")
		victimID := f.kcIDOf(victim.ID)
		// The recorded Keycloak user of m turns out to carry the victim's staff id
		// (corrupted attribute / mis-routed id): simulate by rewriting the attribute.
		mID := f.kcIDOf(m.ID)
		f.dev.mu.Lock()
		f.dev.users[mID].user.Attributes["baobei_staff_id"] = []string{victim.ID}
		f.dev.mu.Unlock()
		before := len(g.touched(mID))
		s, _, b := f.account(f.admin, m.ID, "revoke", "f1-owner-mismatch-001", map[string]string{"reason": "撤销"})
		if s != 409 || !strings.Contains(string(b), "ACCOUNT_REVIEW_REQUIRED") {
			t.Fatal("owner mismatch must park for review", s, string(b))
		}
		if got := g.touched(mID); len(got) != before {
			t.Fatal("foreign user was mutated", got[before:])
		}
		if u, _ := f.dev.getUser(ctx, mID); u == nil || !enabled(u) {
			t.Fatal("foreign user deleted or disabled")
		}
		victimBefore := g.touched(victimID)
		if u, _ := f.dev.getUser(ctx, victimID); u == nil || !enabled(u) {
			t.Fatal("other member's own user affected")
		}
		f.stillReserved(m, "13960000002", "OWNER_MISMATCH")
		// Reconcile re-checks and refuses again; the link is untouched.
		s, b = f.admin.call("POST", "/api/v1/staff/"+m.ID+"/account/reconcile", map[string]any{})
		expect(t, s, 200, b)
		if got := g.touched(mID); len(got) != before {
			t.Fatal("reconcile mutated the foreign user", got[before:])
		}
		f.stillReserved(m, "13960000002", "OWNER_MISMATCH")
		if f.kcIDOf(m.ID) != mID {
			t.Fatal("identity link changed")
		}
		if len(g.touched(victimID)) != len(victimBefore) {
			t.Fatal("other member's user mutated")
		}
	})

	t.Run("2 delete succeeds: REVOKED, phone released, new member opens a real account", func(t *testing.T) {
		f := newAccountFixture(t)
		m := f.openedMember("合成正常撤销", "13960000011", "f1-ok-000000000001")
		kc := f.kcIDOf(m.ID)
		s, _, b := f.account(f.admin, m.ID, "revoke", "f1-ok-revoke-00001", map[string]string{"reason": "信息错误"})
		expect(t, s, 200, b)
		got := f.member(m.ID)
		if got.Status != staffRevoked || got.Phone != "" || got.IdentityLinked {
			t.Fatalf("revoked %+v", got)
		}
		if u, _ := f.dev.getUser(ctx, kc); u != nil {
			t.Fatal("Keycloak user left")
		}
		n := f.invite("合成新员工", "13960000011", salesRole)
		s, res, b := f.account(f.admin, n.ID, "create", "f1-ok-reopen-00001", map[string]string{})
		expect(t, s, 200, b)
		if res.Credential == nil || res.Credential.Login != "13960000011" {
			t.Fatal("reopen with released phone", string(b))
		}
		if _, st := f.signIn("13960000011"); st != 200 {
			t.Fatal("new member cannot sign in", st)
		}
	})

	t.Run("3 delete fails, disable works: phone NOT released, REVIEW, retry completes", func(t *testing.T) {
		f := newAccountFixture(t)
		g := newGuarded(f.dev)
		f.app.accounts = g
		m := f.openedMember("合成删除失败", "13960000021", "f1-delfail-000001")
		kc := f.kcIDOf(m.ID)
		g.failDel = true
		s, _, b := f.account(f.admin, m.ID, "revoke", "f1-delfail-revoke1", map[string]string{"reason": "撤销"})
		if s != 409 || !strings.Contains(string(b), "ACCOUNT_REVIEW_REQUIRED") {
			t.Fatal("delete failure must not report success", s, string(b))
		}
		if u, _ := f.dev.getUser(ctx, kc); u == nil || enabled(u) {
			t.Fatal("own unused login must stay present and disabled")
		}
		f.stillReserved(m, "13960000021", "REVOKE_DELETE_FAILED")
		// Reconcile while Keycloak still refuses: unchanged.
		s, b = f.admin.call("POST", "/api/v1/staff/"+m.ID+"/account/reconcile", map[string]any{})
		expect(t, s, 200, b)
		f.stillReserved(m, "13960000021", "REVOKE_DELETE_FAILED")
		// Keycloak recovers: reconcile completes the original revocation.
		g.failDel = false
		s, b = f.admin.call("POST", "/api/v1/staff/"+m.ID+"/account/reconcile", map[string]any{})
		expect(t, s, 200, b)
		if got := f.member(m.ID); got.Status != staffRevoked || got.Phone != "" {
			t.Fatalf("after reconcile %+v", got)
		}
		if f.dev.count() != 0 {
			t.Fatal("Keycloak user left", f.dev.count())
		}
	})

	t.Run("4 delete times out but happened: recovery reads Keycloak and completes", func(t *testing.T) {
		f := newAccountFixture(t)
		m := f.openedMember("合成删除超时", "13960000031", "f1-timeout-000001")
		f.dev.lostNext = "delete"
		s, _, b := f.account(f.admin, m.ID, "revoke", "f1-timeout-revoke1", map[string]string{"reason": "撤销"})
		// The second read shows the user is gone and the name is free: complete.
		expect(t, s, 200, b)
		if got := f.member(m.ID); got.Status != staffRevoked || got.Phone != "" {
			t.Fatalf("timeout-but-deleted %+v", got)
		}
		// Same, but the process dies right after the lost delete: restart recovers.
		m2 := f.openedMember("合成删除超时崩溃", "13960000032", "f1-timeout-000002")
		f.dev.lostNext = "delete"
		f.crashAt("crash:after-revoke")
		func() {
			defer func() { _ = recover() }()
			f.account(f.admin, m2.ID, "revoke", "f1-timeout-revoke2", map[string]string{"reason": "撤销"})
		}()
		if got := f.member(m2.ID); got.Status == staffRevoked {
			t.Fatal("crashed request committed")
		}
		if n := f.restart(); n != 1 {
			t.Fatal("recovered", n)
		}
		if got := f.member(m2.ID); got.Status != staffRevoked || got.Phone != "" || got.AccountOperation != nil {
			t.Fatalf("after restart %+v %+v", got, got.AccountOperation)
		}
	})

	t.Run("5 delete done, database commit fails: restart is idempotent", func(t *testing.T) {
		f := newAccountFixture(t)
		m := f.openedMember("合成提交失败", "13960000041", "f1-dbfail-000001")
		f.failDBAt("db:final")
		s, _, b := f.account(f.admin, m.ID, "revoke", "f1-dbfail-revoke1", map[string]string{"reason": "撤销"})
		if s != 503 || !strings.Contains(string(b), "ACCOUNT_RESULT_PENDING") {
			t.Fatal("lost commit must be pending", s, string(b))
		}
		if got := f.member(m.ID); got.Status == staffRevoked || got.Phone == "" {
			t.Fatal("phone released without a committed revocation", got)
		}
		for i := 0; i < 2; i++ {
			f.restart()
		}
		got := f.member(m.ID)
		if got.Status != staffRevoked || got.Phone != "" || got.IdentityLinked {
			t.Fatalf("after restart %+v", got)
		}
		var done, audits int
		_ = f.app.db.QueryRow(ctx, `SELECT count(*) FROM account_operations WHERE staff_id=$1 AND operation='REVOKE' AND state='DONE'`, m.ID).Scan(&done)
		_ = f.app.db.QueryRow(ctx, `SELECT count(*) FROM staff_audit WHERE target_id=$1 AND action='staff.revoke'`, m.ID).Scan(&audits)
		if done != 1 || audits != 1 {
			t.Fatal("revocation recorded more than once", done, audits)
		}
	})

	t.Run("6 two admins / same key replay: at most one revocation", func(t *testing.T) {
		f := newAccountFixture(t)
		m := f.openedMember("合成并发撤销", "13960000051", "f1-race-0000000001")
		admin2 := login(t, f.server, "synthetic-admin")
		var wg sync.WaitGroup
		codes := make([]int, 4)
		for i := range 4 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c := f.admin
				key := "f1-race-revoke-0001" // same key twice
				if i >= 2 {
					c, key = admin2, "f1-race-revoke-0002" // second admin, own key
				}
				codes[i], _, _ = f.account(c, m.ID, "revoke", key, map[string]string{"reason": "撤销"})
			}(i)
		}
		wg.Wait()
		var done, audits int
		_ = f.app.db.QueryRow(ctx, `SELECT count(*) FROM account_operations WHERE staff_id=$1 AND operation='REVOKE' AND state='DONE'`, m.ID).Scan(&done)
		_ = f.app.db.QueryRow(ctx, `SELECT count(*) FROM staff_audit WHERE target_id=$1 AND action='staff.revoke'`, m.ID).Scan(&audits)
		if done != 1 || audits != 1 || f.member(m.ID).Status != staffRevoked {
			t.Fatal("revocation not exactly once", done, audits, codes)
		}
		// Replaying the winning key afterwards does not act again.
		s, res, _ := f.account(f.admin, m.ID, "revoke", "f1-race-revoke-0001", map[string]string{"reason": "撤销"})
		if s == 200 && !res.Replayed {
			t.Fatal("replay acted again")
		}
	})

	t.Run("7 member with sign-in or project history: still refused", func(t *testing.T) {
		f := newAccountFixture(t)
		m := f.openedMember("合成已登录", "13960000061", "f1-hist-0000000001")
		if _, st := f.signIn("13960000061"); st != 200 {
			t.Fatal("sign in", st)
		}
		s, _, b := f.account(f.admin, m.ID, "revoke", "f1-hist-revoke-001", map[string]string{"reason": "撤销"})
		if s != 409 || !(strings.Contains(string(b), "STAFF_NOT_REVOCABLE") || strings.Contains(string(b), "STAFF_HAS_HISTORY")) {
			t.Fatal("signed-in member revoked", s, string(b))
		}
		p := f.openedMember("合成有项目", "13960000062", "f1-hist-0000000002")
		if _, err := f.app.db.Exec(ctx, `INSERT INTO audit_events(subject,action,revision) SELECT subject,'synthetic',1 FROM app_users WHERE staff_id=$1`, p.ID); err != nil {
			t.Fatal(err)
		}
		s, _, b = f.account(f.admin, p.ID, "revoke", "f1-hist-revoke-002", map[string]string{"reason": "撤销"})
		if s != 409 || !strings.Contains(string(b), "STAFF_HAS_HISTORY") {
			t.Fatal("member with history revoked", s, string(b))
		}
		if f.dev.count() != 2 {
			t.Fatal("Keycloak users touched", f.dev.count())
		}
	})

	t.Run("8 login name still held / provider unreadable: REVIEW, shown with a cause", func(t *testing.T) {
		f := newAccountFixture(t)
		g := newGuarded(f.dev)
		f.app.accounts = g
		m := f.openedMember("合成查询失败", "13960000071", "f1-find-0000000001")
		g.failFind = true
		s, _, b := f.account(f.admin, m.ID, "revoke", "f1-find-revoke-001", map[string]string{"reason": "撤销"})
		if s != 409 || !strings.Contains(string(b), "ACCOUNT_REVIEW_REQUIRED") {
			t.Fatal("unknown login state must not complete", s, string(b))
		}
		f.stillReserved(m, "13960000071", "PROVIDER_UNAVAILABLE")
		g.failFind = false
		s, b = f.admin.call("POST", "/api/v1/staff/"+m.ID+"/account/reconcile", map[string]any{})
		expect(t, s, 200, b)
		if got := f.member(m.ID); got.Status != staffRevoked {
			t.Fatalf("reconcile after recovery %+v", got)
		}
		// Another Keycloak user holding the name blocks the release.
		m2 := f.openedMember("合成名被占", "13960000072", "f1-find-0000000002")
		kc2 := f.kcIDOf(m2.ID)
		f.dev.lostNext = "" // normal delete
		// Plant a second user with the same name after deletion (dev provider allows
		// it only by direct insert): created by someone else in the realm.
		g.failDel = false
		orig := g.devAccounts
		f.app.accounts = plantAfterDelete{guardedAccounts: g, name: "13960000072", dev: orig}
		s, _, b = f.account(f.admin, m2.ID, "revoke", "f1-find-revoke-002", map[string]string{"reason": "撤销"})
		if s != 409 {
			t.Fatal("occupied name must not complete", s, string(b))
		}
		f.stillReserved(m2, "13960000072", "LOGIN_STILL_OCCUPIED")
		_ = kc2
	})
}

// plantAfterDelete: after deleting the member's user, another (foreign) user with
// the same username appears in the realm.
type plantAfterDelete struct {
	*guardedAccounts
	name string
	dev  *devAccounts
}

func (p plantAfterDelete) delete(ctx context.Context, id string) error {
	if err := p.guardedAccounts.delete(ctx, id); err != nil {
		return err
	}
	p.dev.mu.Lock()
	defer p.dev.mu.Unlock()
	nid := newUUID()
	p.dev.users[nid] = &devAccount{user: kcUser{ID: nid, Username: p.name, Enabled: boolp(true), Attributes: map[string][]string{"baobei_staff_id": {newUUID()}}}}
	return nil
}
