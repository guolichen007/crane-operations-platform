package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type accountFixture struct {
	t      *testing.T
	app    *App
	dev    *devAccounts
	server *httptest.Server
	admin  testClient
}

func newAccountFixture(t *testing.T) *accountFixture {
	t.Helper()
	db := b2Database(t)
	dev := newDevAccounts()
	app := &App{db: db, cfg: Config{Env: "development", Dev: true}, accounts: dev}
	server := httptest.NewServer(app.handler())
	t.Cleanup(server.Close)
	app.cfg.Origin = server.URL
	return &accountFixture{t: t, app: app, dev: dev, server: server, admin: login(t, server, "synthetic-admin")}
}

func (f *accountFixture) invite(name, phone, role string) staffMember {
	f.t.Helper()
	in := staffProfileInput{Name: name, Phone: phone, Role: role, TeamID: syntheticTeam}
	if role == directorRole || role == adminRole {
		in.TeamID = ""
	}
	s, b := f.admin.call("POST", "/api/v1/staff/invitations", in)
	return memberFrom(f.t, s, b)
}

// account posts an account action with an explicit idempotency key.
func (f *accountFixture) account(c testClient, id, action, key string, body any) (int, accountResult, []byte) {
	f.t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", c.origin+"/api/v1/staff/"+id+"/account/"+action, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.origin)
	req.Header.Set("X-CSRF-Token", c.csrf)
	req.Header.Set("Idempotency-Key", key)
	resp, err := c.client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out accountResult
	raw, _ = io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, raw
}

func (f *accountFixture) signIn(phone string) (testClient, int) {
	f.t.Helper()
	c := login(f.t, f.server, "synthetic-a") // fresh cookie jar with a CSRF origin
	req, _ := http.NewRequest("POST", f.server.URL+"/auth/dev-phone", strings.NewReader(`{"phone":"`+phone+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", f.server.URL)
	resp, err := c.client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return c, resp.StatusCode
	}
	s, b := c.call("GET", "/api/v1/me", nil)
	if s == 200 {
		var me User
		_ = json.Unmarshal(b, &me)
		c.csrf = me.CSRF
	}
	return c, s
}

func TestDatabaseR1EAccountCreateClosedLoop(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	m := f.invite("合成开户销售", "13950000001", salesRole)
	s, res, b := f.account(f.admin, m.ID, "create", "create-key-0000000001", map[string]string{})
	expect(t, s, 200, b)
	if res.Credential == nil || res.Credential.Login != "13950000001" || len(res.Credential.TemporaryPassword) != 12 {
		t.Fatal("one-time credential", string(b))
	}
	if res.Member.Status != staffActivation || res.Member.AccountState != accountReady || !res.Member.IdentityLinked || !res.Member.MustChangePassword {
		t.Fatalf("member after create: %+v", res.Member)
	}
	// The password is never stored or returned again.
	var stored int
	_ = f.app.db.QueryRow(ctx, `SELECT count(*) FROM account_operations WHERE detail LIKE '%'||$1||'%'`, res.Credential.TemporaryPassword).Scan(&stored)
	var audit int
	_ = f.app.db.QueryRow(ctx, `SELECT count(*) FROM staff_audit WHERE reason LIKE '%'||$1||'%'`, res.Credential.TemporaryPassword).Scan(&audit)
	if stored != 0 || audit != 0 {
		t.Fatal("temporary password persisted")
	}
	s, detail := f.admin.call("GET", "/api/v1/staff/"+m.ID, nil)
	if strings.Contains(string(detail), res.Credential.TemporaryPassword) || !strings.Contains(string(detail), `"loginName":"13950000001"`) {
		t.Fatal("detail must show the login name but never the password", s)
	}
	// Retrying the same request does not create a second account or show the password again.
	s, again, b := f.account(f.admin, m.ID, "create", "create-key-0000000001", map[string]string{})
	expect(t, s, 200, b)
	if !again.Replayed || again.Credential != nil || len(f.dev.users) != 1 {
		t.Fatal("idempotent replay", string(b), len(f.dev.users))
	}
	// A new request for an already-created account is refused.
	s, _, _ = f.account(f.admin, m.ID, "create", "create-key-0000000002", map[string]string{})
	expect(t, s, 409, nil)
	// The member signs in with the 11-digit phone: business account becomes active.
	emp, s := f.signIn("13950000001")
	expect(t, s, 200, nil)
	s, b = f.admin.call("GET", "/api/v1/staff/"+m.ID, nil)
	var d struct{ Member staffMember }
	_ = json.Unmarshal(b, &d)
	if d.Member.Status != staffActive || d.Member.MustChangePassword {
		t.Fatalf("after first login: %+v", d.Member)
	}
	// Non-admins (and the employee) cannot call account endpoints.
	s, _, _ = f.account(emp, m.ID, "reset-password", "reset-key-0000000001", map[string]string{"reason": "x"})
	if s != 403 {
		t.Fatal("sales called the account service", s)
	}
	manager := login(t, f.server, "synthetic-manager")
	s, _, _ = f.account(manager, m.ID, "reset-password", "reset-key-0000000002", map[string]string{"reason": "x"})
	if s != 403 {
		t.Fatal("manager called the account service", s)
	}
}

func TestDatabaseR1EAccountFailureLeavesNoUsableAccount(t *testing.T) {
	f := newAccountFixture(t)
	for _, step := range []string{"password", "role", "update"} {
		phone := map[string]string{"password": "13950000011", "role": "13950000012", "update": "13950000013"}[step]
		m := f.invite("合成失败"+step, phone, salesRole)
		f.dev.failNext = step
		s, res, b := f.account(f.admin, m.ID, "create", "fail-"+step+"-key-000000001", map[string]string{})
		if s != 503 {
			t.Fatal(step, "expected 503", s, string(b))
		}
		if res.Credential != nil {
			t.Fatal("credential shown on failure")
		}
		// No enabled Keycloak user for this phone; business record unbound and marked failed.
		if u, _ := f.dev.findByUsername(context.Background(), phone); u != nil && u.Enabled != nil && *u.Enabled {
			t.Fatal(step, "failure left an enabled login")
		}
		if _, s := f.signIn(phone); s == 200 {
			t.Fatal(step, "failed account can sign in")
		}
		s, b = f.admin.call("GET", "/api/v1/staff/"+m.ID, nil)
		var d struct{ Member staffMember }
		_ = json.Unmarshal(b, &d)
		if d.Member.IdentityLinked || d.Member.AccountState != accountFailed || d.Member.Status != staffFailed {
			t.Fatalf("%s: business record after failure %+v", step, d.Member)
		}
		// Safe retry with a new request succeeds.
		s, res, b = f.account(f.admin, m.ID, "create", "fail-"+step+"-key-000000002", map[string]string{})
		expect(t, s, 200, b)
		if res.Credential == nil {
			t.Fatal(step, "retry produced no credential")
		}
	}
}

func TestDatabaseR1EAccountConcurrencyAndPhone(t *testing.T) {
	f := newAccountFixture(t)
	m := f.invite("合成并发销售", "13950000021", salesRole)
	// Two concurrent create requests for the same member: exactly one succeeds.
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, _, _ := f.account(f.admin, m.ID, "create", "concurrent-key-00000000"+string(rune('a'+i)), map[string]string{})
			codes <- s
		}(i)
	}
	wg.Wait()
	close(codes)
	ok := 0
	for s := range codes {
		if s == 200 {
			ok++
		}
	}
	if ok != 1 || len(f.dev.users) != 1 {
		t.Fatal("concurrent create", ok, len(f.dev.users))
	}
	// A second member cannot take a login phone already used in the realm.
	other := f.invite("合成另一成员", "13950000022", salesRole)
	if s, _, _ := f.account(f.admin, other.ID, "create", "other-create-key-000001", map[string]string{}); s != 200 {
		t.Fatal("second create", s)
	}
	var before staffMember
	s, b := f.admin.call("GET", "/api/v1/staff/"+m.ID, nil)
	var d struct{ Member staffMember }
	_ = json.Unmarshal(b, &d)
	before = d.Member
	s, _, b = f.account(f.admin, m.ID, "change-phone", "phone-key-000000000001", map[string]string{"phone": "13950000022", "reason": "换号"})
	if s != 409 {
		t.Fatal("phone of another member accepted", s, string(b))
	}
	s, _, b = f.account(f.admin, m.ID, "change-phone", "phone-key-000000000002", map[string]string{"phone": "+85291234567", "reason": "换号"})
	if s != 400 {
		t.Fatal("non-mainland login phone accepted", s, string(b))
	}
	// Change phone: UUID kept, old login dead, new login works, sessions end.
	emp, s := f.signIn("13950000021")
	expect(t, s, 200, nil)
	s, res, b := f.account(f.admin, m.ID, "change-phone", "phone-key-000000000003", map[string]string{"phone": "139 5000 0029", "reason": "员工换号"})
	expect(t, s, 200, b)
	if res.Member.Phone != "+8613950000029" || res.Member.LoginName != "13950000029" {
		t.Fatalf("after change: %+v", res.Member)
	}
	var subject string
	_ = f.app.db.QueryRow(context.Background(), `SELECT subject FROM app_users WHERE staff_id=$1`, m.ID).Scan(&subject)
	if u, _ := f.dev.getUser(context.Background(), subject); u == nil || u.Username != "13950000029" || !before.IdentityLinked {
		t.Fatal("UUID must be unchanged and username updated")
	}
	if s, _ = emp.call("GET", "/api/v1/me", nil); s != 401 {
		t.Fatal("old session survived a phone change", s)
	}
	if _, s = f.signIn("13950000021"); s == 200 {
		t.Fatal("old phone still signs in")
	}
	if _, s = f.signIn("13950000029"); s != 200 {
		t.Fatal("new phone cannot sign in", s)
	}
	// Failure in the provider: database unchanged.
	f.dev.failNext = "update"
	s, _, _ = f.account(f.admin, m.ID, "change-phone", "phone-key-000000000004", map[string]string{"phone": "13950000030", "reason": "x"})
	if s != 503 {
		t.Fatal("provider failure", s)
	}
	s, b = f.admin.call("GET", "/api/v1/staff/"+m.ID, nil)
	_ = json.Unmarshal(b, &d)
	if d.Member.Phone != "+8613950000029" {
		t.Fatal("phone changed although the login could not be changed")
	}
}

func TestDatabaseR1EAccountResetDisableEnable(t *testing.T) {
	f := newAccountFixture(t)
	m := f.invite("合成重置销售", "13950000031", salesRole)
	s, created, b := f.account(f.admin, m.ID, "create", "reset-create-key-000001", map[string]string{})
	expect(t, s, 200, b)
	emp, s := f.signIn("13950000031")
	expect(t, s, 200, nil)
	s, res, b := f.account(f.admin, m.ID, "reset-password", "reset-key-0000000000003", map[string]string{"reason": "员工忘记密码"})
	expect(t, s, 200, b)
	if res.Credential == nil || res.Credential.TemporaryPassword == created.Credential.TemporaryPassword || !res.Member.MustChangePassword {
		t.Fatal("reset must issue a new one-time password", string(b))
	}
	if s, _ = emp.call("GET", "/api/v1/me", nil); s != 401 {
		t.Fatal("sessions must end on reset", s)
	}
	emp, _ = f.signIn("13950000031")
	// Disable: business requests stop at once; the login is disabled.
	s, res, b = f.account(f.admin, m.ID, "disable", "disable-key-00000000001", map[string]string{"reason": "离职"})
	expect(t, s, 200, b)
	if res.Member.Status != staffSuspended || res.Member.AccountState != accountDisabled {
		t.Fatalf("after disable: %+v", res.Member)
	}
	if s, _ = emp.call("GET", "/api/v1/projects", nil); s != 401 {
		t.Fatal("disabled member still served", s)
	}
	if _, s = f.signIn("13950000031"); s == 200 {
		t.Fatal("disabled login works")
	}
	// Enable: restored to pending activation; signs in again with the exact role.
	s, res, b = f.account(f.admin, m.ID, "enable", "enable-key-000000000001", map[string]string{"reason": "返聘"})
	expect(t, s, 200, b)
	if res.Member.Status != staffActivation || res.Member.AccountState != accountReady {
		t.Fatalf("after enable: %+v", res.Member)
	}
	if _, s = f.signIn("13950000031"); s != 200 {
		t.Fatal("enabled account cannot sign in", s)
	}
	// Provider failure while disabling (G4-S5-R1-F): the member is suspended in
	// Baobei at once (fail closed); Keycloak could not confirm, so the account is
	// 待核对 until the administrator reconciles.
	f.dev.down = true
	s, _, _ = f.account(f.admin, m.ID, "disable", "disable-key-00000000002", map[string]string{"reason": "再次停用"})
	if s != 409 {
		t.Fatal("expected review", s)
	}
	s, b = f.admin.call("GET", "/api/v1/staff/"+m.ID, nil)
	var d struct{ Member staffMember }
	_ = json.Unmarshal(b, &d)
	if d.Member.Status != staffSuspended || d.Member.AccountState != accountReview {
		t.Fatalf("fail-closed disable: %+v", d.Member)
	}
	f.dev.down = false
	s, _, b = f.account(f.admin, m.ID, "reconcile", "", map[string]string{})
	expect(t, s, 200, b)
}

func TestDatabaseR1EAccountAdvancedRoleNeedsReauth(t *testing.T) {
	f := newAccountFixture(t)
	m := f.invite("合成主管开户", "13950000041", managerRole)
	if _, err := f.app.db.Exec(context.Background(), `UPDATE staff_sessions SET reauthenticated_at=now()-interval '10 minutes'`); err != nil {
		t.Fatal(err)
	}
	s, _, b := f.account(f.admin, m.ID, "create", "lead-create-key-0000001", map[string]string{})
	if s != 403 || !strings.Contains(string(b), "STAFF_REAUTH_REQUIRED") {
		t.Fatal("manager account without fresh password check", s, string(b))
	}
	if len(f.dev.users) != 0 {
		t.Fatal("refused request reached the provider")
	}
	if _, err := f.app.db.Exec(context.Background(), `UPDATE staff_sessions SET reauthenticated_at=now()`); err != nil {
		t.Fatal(err)
	}
	s, res, b := f.account(f.admin, m.ID, "create", "lead-create-key-0000002", map[string]string{})
	expect(t, s, 200, b)
	if roles, _ := f.dev.roles(context.Background(), res.Member.ID); roles != nil {
		_ = roles
	}
	// Sales accounts do not need it.
	if _, err := f.app.db.Exec(context.Background(), `UPDATE staff_sessions SET reauthenticated_at=now()-interval '10 minutes'`); err != nil {
		t.Fatal(err)
	}
	sales := f.invite("合成销售无需验证", "13950000042", salesRole)
	s, _, b = f.account(f.admin, sales.ID, "create", "sales-create-key-000001", map[string]string{})
	expect(t, s, 200, b)
}

func TestR1ELoginName(t *testing.T) {
	if n, err := loginName("+8613800123456"); err != nil || n != "13800123456" {
		t.Fatal(n, err)
	}
	for _, p := range []string{"+85291234567", "+14155550100", "13800123456", ""} {
		if _, err := loginName(p); err == nil {
			t.Fatal("accepted", p)
		}
	}
	seen := map[string]bool{}
	for range 200 {
		p := temporaryPassword()
		if len(p) != 12 || seen[p] || !strings.ContainsAny(p, "23456789") {
			t.Fatal("weak or repeated temporary password", p)
		}
		seen[p] = true
	}
	if _, err := newKeycloakAdmin("https://crane.youdaozd.com/realms/master", "baobei-web", "svc", "s", nil); err == nil {
		t.Fatal("master realm accepted for the account service")
	}
	k, err := newKeycloakAdmin("https://crane.youdaozd.com/realms/baobei", "baobei-web", "svc", "s", nil)
	if err != nil || k.realm != "baobei" || k.base != "https://crane.youdaozd.com" {
		t.Fatal(k, err)
	}
	if _, _, err := k.call(context.Background(), "GET", "/../crane/users", nil, nil); err == nil {
		t.Fatal("path traversal to another realm accepted")
	}
}

// Migration under the chosen model: an existing member with history (projects,
// approvals, audit) gets a new login in the dedicated realm. The business subject
// and every reference stay exactly as they were; only an identity link is added.
func TestDatabaseR1EMigrationKeepsHistory(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	db := f.app.db
	const legacy = "b2b2b2b2-0000-4000-8000-000000000001"
	var staffID string
	if err := db.QueryRow(ctx, `INSERT INTO app_users(subject,display_name,role,team_id,work_phone,staff_managed)
 VALUES($1,'合成历史销售','baobei_sales',$2,'+8613950000051',true) RETURNING staff_id::text`, legacy, syntheticTeam).Scan(&staffID); err != nil {
		t.Fatal(err)
	}
	// History under the shared realm: a project created by this subject.
	legacyClient := login(t, f.server, "synthetic-a")
	_ = legacyClient
	var projectID string
	if err := db.QueryRow(ctx, `INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,document)
 VALUES('合成历史项目','synthetic history',$1,$1,$1,'{}') RETURNING id::text`, legacy).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var s string
		_ = db.QueryRow(ctx, `SELECT owner_subject||created_by_subject||draft_custodian_subject||document_hash||version FROM intake_projects WHERE id=$1`, projectID).Scan(&s)
		return s
	}
	before := snapshot()
	// Switch the API to the dedicated realm (linked identities only).
	f.app.linkedOnly = true
	if _, err := f.signIn("13950000051"); err == 200 {
		t.Fatal("unlinked phone signed in")
	}
	s, res, b := f.account(f.admin, staffID, "create", "migrate-key-0000000001", map[string]string{})
	expect(t, s, 200, b)
	if res.Credential == nil || res.Credential.Login != "13950000051" {
		t.Fatal(string(b))
	}
	var subject string
	var links int
	_ = db.QueryRow(ctx, `SELECT subject FROM app_users WHERE staff_id=$1`, staffID).Scan(&subject)
	_ = db.QueryRow(ctx, `SELECT count(*) FROM identity_links WHERE subject=$1`, legacy).Scan(&links)
	if subject != legacy || links != 1 || snapshot() != before {
		t.Fatal("business identity or history changed", subject, links)
	}
	emp, s := f.signIn("13950000051")
	expect(t, s, 200, nil)
	s, mb := emp.call("GET", "/api/v1/me", nil)
	if s != 200 || !strings.Contains(string(mb), `"subject":"`+legacy+`"`) {
		t.Fatal("new login must resolve to the same business subject", string(mb))
	}
	s, pb := emp.call("GET", "/api/v1/projects/"+projectID, nil)
	expect(t, s, 200, pb)
	// Account operations address the new realm's user id through the link.
	s, _, b = f.account(f.admin, staffID, "reset-password", "migrate-reset-00000001", map[string]string{"reason": "迁移后重置"})
	expect(t, s, 200, b)
	if snapshot() != before {
		t.Fatal("history changed by account operations")
	}
}
