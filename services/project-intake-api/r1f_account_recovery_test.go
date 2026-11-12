package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// G4-S5-R1-F: account operations never leave a member stuck, never replay an
// operation with an unknown outcome, never leave a usable wrong account, and
// never persist a temporary password. Faults are injected at every point named
// in the order: crashes after a Keycloak call, Keycloak timeouts that actually
// succeeded, database commit failures, lost responses, failing compensation and
// repeated submission after a restart.

// crashAt makes the process "die" at the named point, once.
func (f *accountFixture) crashAt(point string) {
	once := true
	f.app.fault = func(p string) error {
		if once && p == point {
			once = false
			return crashError{point}
		}
		return nil
	}
}

// failDBAt makes the named database step fail (commit not performed), once.
func (f *accountFixture) failDBAt(point string) {
	once := true
	f.app.fault = func(p string) error {
		if once && p == point {
			once = false
			return errDBInjected
		}
		return nil
	}
}

type injectedDB struct{}

func (injectedDB) Error() string { return "injected database failure" }

var errDBInjected = injectedDB{}

// restart: a new process. Expired claims are recovered (the test moves time on
// by expiring the lease instead of waiting two minutes).
func (f *accountFixture) restart() int {
	f.t.Helper()
	f.app.fault = nil
	ctx := context.Background()
	if _, err := f.app.db.Exec(ctx, `UPDATE account_operations SET lease_expires_at=now()-interval '1 second' WHERE state='STARTED'`); err != nil {
		f.t.Fatal(err)
	}
	n, err := f.app.recoverAccountOps(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *accountFixture) member(id string) staffMember {
	f.t.Helper()
	s, b := f.admin.call("GET", "/api/v1/staff/"+id, nil)
	expect(f.t, s, 200, b)
	var d struct{ Member staffMember }
	if err := json.Unmarshal(b, &d); err != nil {
		f.t.Fatal(err)
	}
	return d.Member
}

func (f *accountFixture) ops(staffID string) map[string]int {
	f.t.Helper()
	rows, err := f.app.db.Query(context.Background(), `SELECT state,count(*) FROM account_operations WHERE staff_id=$1 GROUP BY state`, staffID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		_ = rows.Scan(&s, &n)
		out[s] = n
	}
	return out
}

// noPasswordStored: no temporary password value anywhere in the database.
func (f *accountFixture) noPasswordStored(passwords ...string) {
	f.t.Helper()
	for _, p := range passwords {
		if p == "" {
			continue
		}
		var n int
		err := f.app.db.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM account_operations o WHERE row_to_json(o)::text LIKE '%'||$1||'%')+
 (SELECT count(*) FROM staff_audit a WHERE row_to_json(a)::text LIKE '%'||$1||'%')+
 (SELECT count(*) FROM audit_events e WHERE row_to_json(e)::text LIKE '%'||$1||'%')`, p).Scan(&n)
		if err != nil || n != 0 {
			f.t.Fatal("temporary password persisted", n, err)
		}
	}
}

// settledNoLogin: the member is not stuck, has no usable login, and can retry.
func (f *accountFixture) settledNoLogin(m staffMember, phone string) {
	f.t.Helper()
	got := f.member(m.ID)
	if got.AccountState != accountFailed || got.Status != staffFailed || got.IdentityLinked {
		f.t.Fatalf("expected 开通失败 with retry, got %+v", got)
	}
	if got.AccountOperation == nil || got.AccountOperation.State == "STARTED" || got.AccountOperation.Detail == "" {
		f.t.Fatalf("failure cause must be visible: %+v", got.AccountOperation)
	}
	if n := f.dev.enabledFor(phone[len(phone)-11:]); n != 0 {
		f.t.Fatal("an enabled login was left behind", n)
	}
	if _, s := f.signIn(phone[len(phone)-11:]); s == 200 {
		f.t.Fatal("failed creation can sign in")
	}
	if ops := f.ops(m.ID); ops["STARTED"] != 0 || ops["REVIEW"] != 0 {
		f.t.Fatal("operation left unresolved", ops)
	}
}

func TestDatabaseR1FCreateCrashRecovery(t *testing.T) {
	for i, point := range []string{"crash:after-create", "crash:after-enable", "db:step:ROLE", "db:bind", "db:final"} {
		t.Run(point, func(t *testing.T) {
			f := newAccountFixture(t)
			phone := "1395100000" + string(rune('0'+i))
			m := f.invite("合成崩溃"+point, phone, salesRole)
			if strings.HasPrefix(point, "crash:") {
				f.crashAt(point)
			} else {
				f.failDBAt(point)
			}
			s, res, b := f.account(f.admin, m.ID, "create", "crash-create-key-0000"+string(rune('a'+i)), map[string]string{})
			if s == 200 || res.Credential != nil {
				t.Fatal("success reported although the result was not stored", s, string(b))
			}
			// While unresolved: no sign-in, no second operation, a visible state.
			if _, s := f.signIn(phone); s == 200 {
				t.Fatal("half-created account can sign in")
			}
			got := f.member(m.ID)
			if got.AccountState == accountReady {
				t.Fatalf("shown as 已开通 while unresolved: %+v", got)
			}
			if s, _, _ := f.account(f.admin, m.ID, "create", "crash-create-second-"+string(rune('a'+i)), map[string]string{}); s == 200 {
				t.Fatal("a second creation ran while the first was unresolved")
			}
			// The same request after the restart is not replayed blindly.
			creates := f.dev.callCount("create")
			if n := f.restart(); n != 1 {
				t.Fatal("recovery did not take the expired operation", n)
			}
			if f.dev.callCount("create") != creates {
				t.Fatal("recovery repeated the creation")
			}
			f.settledNoLogin(m, phone)
			s, _, _ = f.account(f.admin, m.ID, "create", "crash-create-key-0000"+string(rune('a'+i)), map[string]string{})
			if s != 409 {
				t.Fatal("the same key replayed a failed creation", s)
			}
			// Safe retry: a new request opens the account and shows a new password.
			s, res, b = f.account(f.admin, m.ID, "create", "crash-create-retry-00"+string(rune('a'+i)), map[string]string{})
			expect(t, s, 200, b)
			if res.Credential == nil || f.dev.enabledFor(phone) != 1 || f.dev.count() != 1 {
				t.Fatal("retry", string(b), f.dev.enabledFor(phone), f.dev.count())
			}
			f.noPasswordStored(res.Credential.TemporaryPassword)
			if _, s := f.signIn(phone); s != 200 {
				t.Fatal("retried account cannot sign in", s)
			}
		})
	}
}

func TestDatabaseR1FCreateTimeoutButCreated(t *testing.T) {
	f := newAccountFixture(t)
	m := f.invite("合成超时开户", "13951000011", salesRole)
	// Keycloak created the user, but the response was lost.
	f.dev.lostNext = "create"
	s, res, b := f.account(f.admin, m.ID, "create", "timeout-create-key-0001", map[string]string{})
	expect(t, s, 200, b)
	if res.Credential == nil || f.dev.count() != 1 || f.dev.enabledFor("13951000011") != 1 {
		t.Fatal("timeout after creation must adopt this member's own user exactly once", f.dev.count())
	}
	// Same, but the lookup also fails: the outcome is unknown → REVIEW, login paused.
	m2 := f.invite("合成超时未知", "13951000012", salesRole)
	f.dev.lostNext = "create"
	f.app.fault = func(p string) error {
		if p == "db:step:KC_CREATE" {
			f.dev.failNext = "find"
		}
		return nil
	}
	s, _, b = f.account(f.admin, m2.ID, "create", "timeout-create-key-0002", map[string]string{})
	if s != 409 || !strings.Contains(string(b), "ACCOUNT_REVIEW_REQUIRED") {
		t.Fatal("unknown creation outcome must be parked for review", s, string(b))
	}
	got := f.member(m2.ID)
	if got.AccountState != accountReview || got.AccountOperation == nil || got.AccountOperation.State != "REVIEW" {
		t.Fatalf("待核对 not shown: %+v", got)
	}
	if _, s := f.signIn("13951000012"); s == 200 {
		t.Fatal("REVIEW account can sign in")
	}
	if s, _, _ := f.account(f.admin, m2.ID, "reset-password", "timeout-reset-key-0002", map[string]string{"reason": "x"}); s != 409 {
		t.Fatal("other operations allowed while 待核对", s)
	}
	// The administrator reconciles: Keycloak shows a disabled user of this member
	// created by the attempt; it is removed and the member can be opened again.
	f.app.fault = nil
	s, _, b = f.account(f.admin, m2.ID, "reconcile", "", map[string]string{})
	expect(t, s, 200, b)
	f.settledNoLogin(m2, "13951000012")
	s, res, b = f.account(f.admin, m2.ID, "create", "timeout-create-key-0003", map[string]string{})
	expect(t, s, 200, b)
	if f.dev.enabledFor("13951000012") != 1 {
		t.Fatal("reopen after review")
	}
}

func TestDatabaseR1FCompensationFailure(t *testing.T) {
	f := newAccountFixture(t)
	m := f.invite("合成补偿失败", "13951000021", salesRole)
	// Role assignment fails; compensation (delete) works: settled, retry possible.
	f.dev.failNext = "role"
	s, _, b := f.account(f.admin, m.ID, "create", "comp-fail-key-00000001", map[string]string{})
	if s != 503 {
		t.Fatal("role failure", s, string(b))
	}
	f.settledNoLogin(m, "13951000021")
	// Role assignment fails and the compensation itself fails: the user stays
	// disabled, the member is parked as 待核对 and cannot sign in.
	f.app.accounts = &compensationBreaker{devAccounts: f.dev}
	f.dev.failNext = "role"
	s, _, b = f.account(f.admin, m.ID, "create", "comp-fail-key-00000002", map[string]string{})
	if s != 409 || !strings.Contains(string(b), "ACCOUNT_REVIEW_REQUIRED") {
		t.Fatal("failed compensation must be parked for review", s, string(b))
	}
	if f.dev.enabledFor("13951000021") != 0 {
		t.Fatal("failed compensation left an enabled login")
	}
	if _, s := f.signIn("13951000021"); s == 200 {
		t.Fatal("REVIEW member can sign in")
	}
	if got := f.member(m.ID); got.AccountState != accountReview {
		t.Fatalf("待核对: %+v", got)
	}
	// Reconcile while the provider still fails: stays REVIEW (never "已开通").
	s, _, b = f.account(f.admin, m.ID, "reconcile", "", map[string]string{})
	expect(t, s, 200, b)
	if got := f.member(m.ID); got.AccountState != accountReview {
		t.Fatalf("still unconfirmed must stay 待核对: %+v", got)
	}
	// Reconcile once the provider works again: removed, member can retry.
	f.app.accounts = f.dev
	s, _, b = f.account(f.admin, m.ID, "reconcile", "", map[string]string{})
	expect(t, s, 200, b)
	f.settledNoLogin(m, "13951000021")
	s, _, b = f.account(f.admin, m.ID, "create", "comp-fail-key-00000003", map[string]string{})
	expect(t, s, 200, b)
}

// compensationBreaker: delete always fails (the compensation step itself fails).
type compensationBreaker struct{ *devAccounts }

func (c *compensationBreaker) delete(context.Context, string) error { return errAccountProvider }

func TestDatabaseR1FResetPasswordLostAndCrash(t *testing.T) {
	f := newAccountFixture(t)
	m := f.invite("合成重置丢失", "13951000031", salesRole)
	s, created, b := f.account(f.admin, m.ID, "create", "lost-reset-create-0001", map[string]string{})
	expect(t, s, 200, b)
	emp, s := f.signIn("13951000031")
	expect(t, s, 200, nil)
	// Keycloak set the password but the response was lost: never shown, never
	// repeated; sessions end and a new password is required.
	f.dev.lostNext = "password"
	resets := f.dev.callCount("password")
	s, res, b := f.account(f.admin, m.ID, "reset-password", "lost-reset-key-000001", map[string]string{"reason": "忘记密码"})
	if s != 503 || res.Credential != nil {
		t.Fatal("lost reset must not report success or a password", s, string(b))
	}
	if f.dev.callCount("password") != resets+1 {
		t.Fatal("reset repeated")
	}
	if s, _ = emp.call("GET", "/api/v1/me", nil); s != 401 {
		t.Fatal("sessions must end after a reset with a lost result", s)
	}
	got := f.member(m.ID)
	if !got.MustChangePassword || got.AccountOperation == nil || got.AccountOperation.Detail != "RESULT_LOST" {
		t.Fatalf("visible failure + must change password: %+v", got)
	}
	// Same key again: not replayed.
	if s, _, _ := f.account(f.admin, m.ID, "reset-password", "lost-reset-key-000001", map[string]string{"reason": "忘记密码"}); s != 409 {
		t.Fatal("same key replayed a reset", s)
	}
	// Crash right after Keycloak changed the password; restart recovers without
	// resetting again.
	f.crashAt("crash:after-password")
	s, _, _ = f.account(f.admin, m.ID, "reset-password", "lost-reset-key-000002", map[string]string{"reason": "忘记密码"})
	if s == 200 {
		t.Fatal("crash reported success")
	}
	resets = f.dev.callCount("password")
	f.restart()
	if f.dev.callCount("password") != resets {
		t.Fatal("recovery repeated the password reset")
	}
	if ops := f.ops(m.ID); ops["STARTED"] != 0 {
		t.Fatal("reset left STARTED", ops)
	}
	// A new reset works and returns a fresh password once.
	s, res, b = f.account(f.admin, m.ID, "reset-password", "lost-reset-key-000003", map[string]string{"reason": "重新重置"})
	expect(t, s, 200, b)
	if res.Credential == nil || res.Credential.TemporaryPassword == created.Credential.TemporaryPassword {
		t.Fatal("fresh password")
	}
	f.noPasswordStored(created.Credential.TemporaryPassword, res.Credential.TemporaryPassword)
}

func TestDatabaseR1FChangePhoneDBFailure(t *testing.T) {
	f := newAccountFixture(t)
	m := f.invite("合成换号失败", "13951000041", salesRole)
	s, _, b := f.account(f.admin, m.ID, "create", "phone-db-create-000001", map[string]string{})
	expect(t, s, 200, b)
	// Keycloak changed the username, then the database update failed.
	f.failDBAt("db:final")
	s, _, b = f.account(f.admin, m.ID, "change-phone", "phone-db-key-000000001", map[string]string{"phone": "13951000042", "reason": "换号"})
	if s != 503 || !strings.Contains(string(b), "ACCOUNT_RESULT_PENDING") {
		t.Fatal("database failure after the Keycloak change must be reported as pending", s, string(b))
	}
	// Recovery reads Keycloak (new username) and completes the database side.
	f.restart()
	got := f.member(m.ID)
	if got.Phone != "+8613951000042" || got.AccountState != accountReady {
		t.Fatalf("recovery must complete the phone change: %+v", got)
	}
	if _, s := f.signIn("13951000041"); s == 200 {
		t.Fatal("old phone signs in")
	}
	if _, s := f.signIn("13951000042"); s != 200 {
		t.Fatal("new phone cannot sign in", s)
	}
	// Crash before the Keycloak call made progress: recovery finds the old
	// username and closes the operation without changing anything.
	f.app.fault = func(p string) error {
		if p == "db:step:KC_USERNAME" {
			return crashError{p}
		}
		return nil
	}
	_, _, _ = f.account(f.admin, m.ID, "change-phone", "phone-db-key-000000002", map[string]string{"phone": "13951000043", "reason": "换号"})
	f.restart()
	got = f.member(m.ID)
	if got.Phone != "+8613951000042" || f.ops(m.ID)["STARTED"] != 0 {
		t.Fatalf("crash before the provider call must change nothing: %+v", got)
	}
	// Business record refuses the new phone (taken meanwhile): username restored.
	other := f.invite("合成号码占用", "13951000049", salesRole)
	_ = other
	if _, err := f.app.db.Exec(context.Background(), `UPDATE app_users SET work_phone='+8613951000044' WHERE staff_id=$1`, other.ID); err != nil {
		t.Fatal(err)
	}
	f.app.fault = func(p string) error {
		if p == "db:step:KC_USERNAME" {
			// Another member takes the phone while Keycloak is being changed.
			_, _ = f.app.db.Exec(context.Background(), `UPDATE app_users SET work_phone='+8613951000045' WHERE staff_id=$1`, other.ID)
		}
		return nil
	}
	s, _, b = f.account(f.admin, m.ID, "change-phone", "phone-db-key-000000003", map[string]string{"phone": "13951000045", "reason": "换号"})
	if s != 409 {
		t.Fatal("conflicting phone", s, string(b))
	}
	if u, _ := f.dev.findByUsername(context.Background(), "13951000042"); u == nil {
		t.Fatal("Keycloak username not restored")
	}
}

func TestDatabaseR1FEnableAfterDBFailure(t *testing.T) {
	f := newAccountFixture(t)
	m := f.invite("合成恢复失败", "13951000051", salesRole)
	s, _, b := f.account(f.admin, m.ID, "create", "enable-db-create-00001", map[string]string{})
	expect(t, s, 200, b)
	s, _, b = f.account(f.admin, m.ID, "disable", "enable-db-disable-0001", map[string]string{"reason": "停用"})
	expect(t, s, 200, b)
	// Keycloak enabled the login, then the business update failed: the member is
	// still suspended in Baobei (cannot sign in), and recovery completes it after
	// checking Keycloak shows exactly the intended role.
	f.failDBAt("db:final")
	s, _, b = f.account(f.admin, m.ID, "enable", "enable-db-key-00000001", map[string]string{"reason": "恢复"})
	if s != 503 {
		t.Fatal(s, string(b))
	}
	if _, s := f.signIn("13951000051"); s == 200 {
		t.Fatal("suspended member signs in before the business state is stored")
	}
	f.restart()
	got := f.member(m.ID)
	if got.Status != staffActivation || got.AccountState != accountReady {
		t.Fatalf("recovery must complete the restore: %+v", got)
	}
	// Disable with Keycloak unconfirmed: Baobei refuses at once; REVIEW.
	f.dev.down = true
	s, _, b = f.account(f.admin, m.ID, "disable", "enable-db-disable-0002", map[string]string{"reason": "停用"})
	if s != 409 || !strings.Contains(string(b), "ACCOUNT_REVIEW_REQUIRED") {
		t.Fatal(s, string(b))
	}
	got = f.member(m.ID)
	if got.Status != staffSuspended || got.AccountState != accountReview {
		t.Fatalf("fail closed: %+v", got)
	}
	f.dev.down = false
	s, _, b = f.account(f.admin, m.ID, "reconcile", "", map[string]string{})
	expect(t, s, 200, b)
	got = f.member(m.ID)
	if got.AccountState != accountDisabled || f.dev.enabledFor("13951000051") != 0 {
		t.Fatalf("reconcile completes the disable: %+v", got)
	}
}

func TestDatabaseR1FIdempotencyAndConcurrency(t *testing.T) {
	f := newAccountFixture(t)
	a := f.invite("合成幂等甲", "13951000061", salesRole)
	b := f.invite("合成幂等乙", "13951000062", salesRole)
	s, _, raw := f.account(f.admin, a.ID, "create", "idem-shared-key-000001", map[string]string{})
	expect(t, s, 200, raw)
	// Same key for another member or another operation: refused, nothing done.
	if s, _, raw := f.account(f.admin, b.ID, "create", "idem-shared-key-000001", map[string]string{}); s != 409 || !strings.Contains(string(raw), "ACCOUNT_IDEMPOTENCY_MISMATCH") {
		t.Fatal("same key, other member", s, string(raw))
	}
	if s, _, raw := f.account(f.admin, a.ID, "reset-password", "idem-shared-key-000001", map[string]string{"reason": "x"}); s != 409 || !strings.Contains(string(raw), "ACCOUNT_IDEMPOTENCY_MISMATCH") {
		t.Fatal("same key, other operation", s, string(raw))
	}
	if s, _, raw := f.account(f.admin, a.ID, "disable", "idem-disable-key-00001", map[string]string{"reason": "甲"}); s != 200 {
		t.Fatal(s, string(raw))
	}
	if s, _, raw := f.account(f.admin, a.ID, "disable", "idem-disable-key-00001", map[string]string{"reason": "乙"}); s != 409 || !strings.Contains(string(raw), "ACCOUNT_IDEMPOTENCY_MISMATCH") {
		t.Fatal("same key, other reason", s, string(raw))
	}
	// Two administrators open accounts for two members at once: both succeed,
	// each with exactly one login.
	c := f.invite("合成并发丙", "13951000071", salesRole)
	d := f.invite("合成并发丁", "13951000072", salesRole)
	var wg sync.WaitGroup
	codes := make(chan int, 4)
	for i, m := range []staffMember{c, d, c, d} {
		wg.Add(1)
		go func(i int, m staffMember) {
			defer wg.Done()
			s, _, _ := f.account(f.admin, m.ID, "create", "idem-race-key-0000000"+string(rune('a'+i)), map[string]string{})
			codes <- s
		}(i, m)
	}
	wg.Wait()
	close(codes)
	ok := 0
	for s := range codes {
		if s == 200 {
			ok++
		}
	}
	if ok != 2 || f.dev.enabledFor("13951000071") != 1 || f.dev.enabledFor("13951000072") != 1 {
		t.Fatal("exactly one account per member", ok)
	}
	// A login that already belongs to another member (stale import) is never
	// adopted: the creation is refused and no identity is bound.
	g := f.invite("合成同号己", "13951000082", salesRole)
	if _, err := f.dev.createDisabled(context.Background(), "13951000082", c.ID); err != nil {
		t.Fatal(err)
	}
	s, _, raw = f.account(f.admin, g.ID, "create", "idem-same-phone-00002", map[string]string{})
	if s != 409 || !strings.Contains(string(raw), "ACCOUNT_PHONE_TAKEN") {
		t.Fatal("a login owned by another member must not be adopted", s, string(raw))
	}
	if got := f.member(g.ID); got.IdentityLinked || got.AccountState != accountFailed {
		t.Fatalf("no identity of another member: %+v", got)
	}
	// Two concurrent operations on one login phone: the database admits one.
	ctx := context.Background()
	_, err := f.app.db.Exec(ctx, `INSERT INTO account_operations(idempotency_key,staff_id,operation,actor_subject,lease_token,lease_expires_at,target_login)
 VALUES('idem-login-lock-00001',$1,'CREATE','synthetic-admin',gen_random_uuid(),now()+interval '1 minute','13951000099')`, c.ID)
	if err == nil {
		_, err = f.app.db.Exec(ctx, `INSERT INTO account_operations(idempotency_key,staff_id,operation,actor_subject,lease_token,lease_expires_at,target_login)
 VALUES('idem-login-lock-00002',$1,'CREATE','synthetic-admin',gen_random_uuid(),now()+interval '1 minute','13951000099')`, d.ID)
		if err == nil || !strings.Contains(err.Error(), "account_operations_one_login") {
			t.Fatal("two open operations on one login", err)
		}
	} else {
		t.Fatal(err)
	}
}

func mustFindID(t *testing.T, d *devAccounts, username string) string {
	t.Helper()
	u, _ := d.findByUsername(context.Background(), username)
	if u == nil {
		t.Fatal("no user", username)
	}
	return u.ID
}

func TestDatabaseR1FLegacyStartedRowRecovered(t *testing.T) {
	// A row left STARTED by the R1-E code (no steps, no claim) is recovered.
	f := newAccountFixture(t)
	m := f.invite("合成旧版遗留", "13951000091", salesRole)
	ctx := context.Background()
	if _, err := f.app.db.Exec(ctx, `INSERT INTO account_operations(idempotency_key,staff_id,operation,actor_subject,lease_expires_at)
 VALUES('legacy-started-key-0001',$1,'CREATE','synthetic-admin',now()-interval '1 minute')`, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.db.Exec(ctx, `UPDATE app_users SET account_state='CREATING' WHERE staff_id=$1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.restart(); n != 1 {
		t.Fatal("legacy row not recovered", n)
	}
	got := f.member(m.ID)
	if got.AccountState != accountFailed || f.ops(m.ID)["STARTED"] != 0 {
		t.Fatalf("legacy row: %+v", got)
	}
	s, _, b := f.account(f.admin, m.ID, "create", "legacy-started-key-0002", map[string]string{})
	expect(t, s, 200, b)
}

func TestDatabaseR1FProfileEditCannotDesyncLogin(t *testing.T) {
	f := newAccountFixture(t)
	m := f.invite("合成资料改号", "13951000101", salesRole)
	s, _, b := f.account(f.admin, m.ID, "create", "edit-phone-create-0001", map[string]string{})
	expect(t, s, 200, b)
	got := f.member(m.ID)
	s, b = f.admin.call("PATCH", "/api/v1/staff/"+m.ID, staffProfileInput{Name: got.Name, Phone: "13951000102", Role: got.Role, TeamID: got.TeamID, Version: got.Version})
	if s != 409 || !strings.Contains(string(b), "STAFF_PHONE_ACCOUNT_BOUND") {
		t.Fatal("profile edit changed the login phone outside the account service", s, string(b))
	}
}
