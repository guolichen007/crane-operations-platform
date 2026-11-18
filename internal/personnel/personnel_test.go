package personnel

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/personnel/personneltest"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

const issuer = "https://crane.test/realms/crane"

type fixture struct {
	db   *sql.DB
	idp  *personneltest.Fake
	svc  *Service
	host Actor
	live map[string]int
}

func setup(t *testing.T) *fixture {
	t.Helper()
	db := testsupport.Database(t)
	f := &fixture{db: db, idp: personneltest.NewFake(), live: map[string]int{}}
	f.svc = &Service{DB: db, IdP: f.idp, Issuer: issuer, RevokeLive: func(_ context.Context, u string) error { f.live[u]++; return nil }}
	f.host = Actor{ID: "ops", Host: true, Event: auditlog.Event{RequestID: "host-req"}}
	for _, stmt := range []string{
		`INSERT INTO tenants(id,name) VALUES('t1','t1') ON CONFLICT DO NOTHING`,
		`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS1','t1','PS1','组合一'),('PS2','t1','PS2','组合二')`,
		`INSERT INTO departments(id,name) VALUES('ops','运维部'),('eng','工程部')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// admin bootstraps an enabled system administrator through the host recovery path.
func (f *fixture) admin(t *testing.T, username string) Actor {
	t.Helper()
	f.idp.AddExternal(username)
	p, err := f.svc.AdoptAdministrator(context.Background(), f.host, username, "管理员 "+username)
	if err != nil {
		t.Fatal(err)
	}
	return Actor{ID: p.ID, Event: auditlog.Event{RequestID: platform.ID()}}
}

func (f *fixture) open(t *testing.T, a Actor, username string, roles ...string) ProvisionResult {
	t.Helper()
	r, err := f.svc.ProvisionAccount(context.Background(), a, "key-"+username+"-0001", ProvisionRequest{Username: username,
		Profile: Profile{DisplayName: "员工 " + username, DepartmentID: "ops", JobTitle: "工程师"}, SystemRoles: roles})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestProvisionHappyPathEnablesOnlyAfterGrants(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	a := f.admin(t, "root-admin")
	r, err := f.svc.ProvisionAccount(ctx, a, "key-new-0001", ProvisionRequest{Username: "Zhang-San", Profile: Profile{DisplayName: "张三", DepartmentID: "ops", JobTitle: "调度员", Phone: "13800000000"},
		PortfolioGrants: []PortfolioGrantInput{{ScopeID: "PS1", Role: "portfolio_planner"}}})
	if err != nil {
		t.Fatal(err)
	}
	if r.State != "COMPLETED" || len(r.TemporaryPassword) != 16 || !r.PasswordDelivered || r.Person.Username != "zhang-san" || !r.Person.Enabled {
		t.Fatalf("unexpected result %+v", r)
	}
	u, _ := f.idp.FindByUsername(ctx, "zhang-san")
	if u == nil || !*u.Enabled || u.RequiredActions[0] != "UPDATE_PASSWORD" || f.idp.Passwords[u.ID] != r.TemporaryPassword {
		t.Fatal("keycloak not enabled with a temporary password")
	}
	if n := count(t, f.db, `SELECT count(*) FROM portfolio_memberships WHERE user_id='zhang-san' AND role='portfolio_planner'`); n != 1 {
		t.Fatal("portfolio grant missing")
	}
	// No secret in the database: not in provisions, audit or users.
	for _, q := range []string{`SELECT count(*) FROM account_provisions WHERE request::text LIKE '%'||$1||'%'`, `SELECT count(*) FROM audit_logs WHERE change::text LIKE '%'||$1||'%' OR result LIKE '%'||$1||'%'`} {
		if n := count(t, f.db, q, r.TemporaryPassword); n != 0 {
			t.Fatal("temporary password persisted")
		}
	}
	// Replay with the same key: same person, no password.
	again, err := f.svc.ProvisionAccount(ctx, a, "key-new-0001", ProvisionRequest{Username: "zhang-san", Profile: Profile{DisplayName: "张三", DepartmentID: "ops", JobTitle: "调度员", Phone: "13800000000"},
		PortfolioGrants: []PortfolioGrantInput{{ScopeID: "PS1", Role: "portfolio_planner"}}})
	if err != nil || !again.Replayed || again.TemporaryPassword != "" || again.Person.ID != "zhang-san" {
		t.Fatalf("replay %+v %v", again, err)
	}
	// Same key, different body → 422; new key same username → 409.
	if _, err = f.svc.ProvisionAccount(ctx, a, "key-new-0001", ProvisionRequest{Username: "zhang-san", Profile: Profile{DisplayName: "李四"}}); !errors.Is(err, ErrIdempotencyKey) {
		t.Fatal("key reuse with another body must fail", err)
	}
	if _, err = f.svc.ProvisionAccount(ctx, a, "key-new-0002", ProvisionRequest{Username: "zhang-san", Profile: Profile{DisplayName: "张三"}}); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate username must be 409", err)
	}
	if n := count(t, f.db, `SELECT count(*) FROM audit_logs WHERE action='personnel_created' AND target_user_id='zhang-san' AND user_id=$1`, a.ID); n != 1 {
		t.Fatal("audit missing", n)
	}
}

func TestProvisionRecoversFromEveryCrashPointWithoutUnauthorizedLogin(t *testing.T) {
	for _, step := range []string{"after_idp_create", "before_bind_commit", "after_password", "after_idp_enable"} {
		t.Run(step, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			a := f.admin(t, "root-admin")
			crash := true
			f.svc.Fault = func(s string) error {
				if s == step && crash {
					crash = false
					return errors.New("simulated crash at " + s)
				}
				return nil
			}
			req := ProvisionRequest{Username: "crash-user", Profile: Profile{DisplayName: "崩溃测试"}, PortfolioGrants: []PortfolioGrantInput{{ScopeID: "PS2", Role: "portfolio_manager"}}}
			if _, err := f.svc.ProvisionAccount(ctx, a, "key-crash-0001", req); err == nil {
				t.Fatal("expected simulated failure")
			}
			// Invariant: never able to log in without grants. Login requires Keycloak enabled AND
			// users.enabled; check that at no crash point both are true unless grants exist.
			u, _ := f.idp.FindByUsername(ctx, "crash-user")
			kcEnabled := u != nil && u.Enabled != nil && *u.Enabled
			var localEnabled bool
			_ = f.db.QueryRow(`SELECT enabled FROM users WHERE id='crash-user'`).Scan(&localEnabled)
			grants := count(t, f.db, `SELECT count(*) FROM portfolio_memberships WHERE user_id='crash-user'`)
			if kcEnabled && localEnabled && grants == 0 {
				t.Fatal("login possible without approved grants")
			}
			if localEnabled {
				t.Fatal("local identity enabled before the provision completed")
			}
			// Retry with the same key completes and returns a fresh password.
			r, err := f.svc.ProvisionAccount(ctx, a, "key-crash-0001", req)
			if err != nil || r.State != "COMPLETED" || r.TemporaryPassword == "" || !r.Person.Enabled {
				t.Fatalf("retry %+v %v", r, err)
			}
			if n := count(t, f.db, `SELECT count(*) FROM users WHERE username='crash-user'`); n != 1 {
				t.Fatal("duplicate local user")
			}
			if len(f.idp.Users) != 2 { // admin + crash-user, never a second Keycloak account
				t.Fatal("orphan / duplicate Keycloak user", len(f.idp.Users))
			}
		})
	}
}

func TestProvisionLostCreateResponseAndCancelLeavesNoOrphan(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	a := f.admin(t, "root-admin")
	f.idp.LoseCreateResponse = true
	req := ProvisionRequest{Username: "lost-resp", Profile: Profile{DisplayName: "响应丢失"}}
	if _, err := f.svc.ProvisionAccount(ctx, a, "key-lost-0001", req); !errors.Is(err, ErrIdentityProvider) {
		t.Fatal("expected provider error", err)
	}
	// Keycloak has the (disabled) user, DB has nothing bound.
	if u, _ := f.idp.FindByUsername(ctx, "lost-resp"); u == nil || *u.Enabled {
		t.Fatal("expected the disabled orphan candidate")
	}
	var id string
	if err := f.db.QueryRow(`SELECT id FROM account_provisions WHERE username='lost-resp'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.CancelProvision(ctx, a, id); err != nil {
		t.Fatal(err)
	}
	if u, _ := f.idp.FindByUsername(ctx, "lost-resp"); u != nil {
		t.Fatal("cancel must remove the unbound Keycloak user")
	}
	// Username released: a new opening succeeds.
	if _, err := f.svc.ProvisionAccount(ctx, a, "key-lost-0002", req); err != nil {
		t.Fatal(err)
	}
	// A retry of a lost response (no cancel) adopts the user it created instead of 409.
	f.idp.LoseCreateResponse = true
	req2 := ProvisionRequest{Username: "lost-two", Profile: Profile{DisplayName: "重试"}}
	_, _ = f.svc.ProvisionAccount(ctx, a, "key-lost-0003", req2)
	if r, err := f.svc.ProvisionAccount(ctx, a, "key-lost-0003", req2); err != nil || r.State != "COMPLETED" {
		t.Fatal("retry after lost response must complete", err)
	}
}

func TestProvisionRefusesExistingForeignKeycloakUsername(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	a := f.admin(t, "root-admin")
	f.idp.AddExternal("someone")
	if _, err := f.svc.ProvisionAccount(ctx, a, "key-foreign-001", ProvisionRequest{Username: "someone", Profile: Profile{DisplayName: "x"}}); !errors.Is(err, ErrConflict) {
		t.Fatal("must refuse to take over a foreign Keycloak user", err)
	}
	u, _ := f.idp.FindByUsername(ctx, "someone")
	if u.Managed() {
		t.Fatal("foreign user must stay unmanaged")
	}
	for _, name := range []string{"admin", "service-account-crane-bff", "bootstrap-crane", "A", "x y"} {
		if _, err := f.svc.ProvisionAccount(ctx, a, "key-reserved-"+platform.ID(), ProvisionRequest{Username: name, Profile: Profile{DisplayName: "x"}}); !errors.Is(err, ErrInvalid) {
			t.Fatal("reserved/invalid username accepted", name)
		}
	}
}

func TestSystemRBACAndLastAdminProtection(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	mgr := f.open(t, root, "acct-mgr", "account_manager")
	staff := f.open(t, root, "staff-one")
	mgrA := Actor{ID: mgr.Person.ID}
	staffA := Actor{ID: staff.Person.ID}

	// Plain staff: no system capability at all.
	if _, err := f.svc.List(ctx, staffA, ListFilter{}); !errors.Is(err, ErrForbidden) {
		t.Fatal("staff listed personnel")
	}
	// A project or portfolio role gives no system rights.
	if err := identity.ChangeGrant(ctx, f.db, "staff-one", "P-none", "platform_admin", false, auditlog.Event{Actor: "x"}); err == nil {
		t.Log("grant to unknown project unexpectedly ok")
	}
	if _, err := f.db.Exec(`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('staff-one','PS1','portfolio_manager')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.List(ctx, staffA, ListFilter{}); !errors.Is(err, ErrForbidden) {
		t.Fatal("portfolio manager gained system rights")
	}
	// Account manager: may manage plain staff, may not grant roles or touch admins.
	if _, err := f.svc.ProvisionAccount(ctx, mgrA, "key-mgr-escalate", ProvisionRequest{Username: "esc", Profile: Profile{DisplayName: "x"}, SystemRoles: []string{"system_admin"}}); !errors.Is(err, ErrForbidden) {
		t.Fatal("account manager granted a system role", err)
	}
	if _, err := f.svc.SetSystemRoles(ctx, mgrA, "staff-one", []string{"system_admin"}, []string{}); !errors.Is(err, ErrForbidden) {
		t.Fatal("account manager escalated staff")
	}
	if _, err := f.svc.SetSystemRoles(ctx, mgrA, "acct-mgr", []string{"system_admin", "account_manager"}, []string{"account_manager"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("self escalation")
	}
	if _, err := f.svc.SetEnabled(ctx, mgrA, root.ID, 0, false); !errors.Is(err, ErrProtected) {
		t.Fatal("account manager disabled an administrator", err)
	}
	if _, err := f.svc.ResetPassword(ctx, mgrA, root.ID); !errors.Is(err, ErrProtected) {
		t.Fatal("account manager reset an administrator password", err)
	}
	if _, err := f.svc.SetPortfolioRole(ctx, mgrA, "staff-one", "PS2", "portfolio_planner", ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("account manager granted portfolio role")
	}
	if _, err := f.svc.ResetPassword(ctx, mgrA, "staff-one"); err != nil {
		t.Fatal("account manager must reset staff password", err)
	}
	// Last admin: cannot be demoted, disabled, or self-changed.
	if _, err := f.svc.SetSystemRoles(ctx, root, root.ID, []string{}, []string{"system_admin"}); !errors.Is(err, ErrSelf) {
		t.Fatal("self demotion must be refused", err)
	}
	if _, err := f.svc.SetEnabled(ctx, root, root.ID, 0, false); !errors.Is(err, ErrSelf) {
		t.Fatal("self disable must be refused", err)
	}
	if _, err := f.svc.SetEnabled(ctx, f.host, root.ID, 0, false); !errors.Is(err, ErrLastAdmin) {
		t.Fatal("host disabling the last admin must be refused", err)
	}
	if _, err := f.svc.SetSystemRoles(ctx, f.host, root.ID, []string{}, []string{"system_admin"}); !errors.Is(err, ErrLastAdmin) {
		t.Fatal("host demoting the last admin must be refused", err)
	}
	// With a second admin the first can be disabled — and the second then cannot.
	second := f.open(t, root, "second-admin", "system_admin")
	if _, err := f.svc.SetEnabled(ctx, Actor{ID: second.Person.ID}, root.ID, 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetEnabled(ctx, f.host, second.Person.ID, 0, false); !errors.Is(err, ErrLastAdmin) {
		t.Fatal("the remaining admin must be protected", err)
	}
	// A disabled admin has no effective system rights.
	if _, err := f.svc.List(ctx, root, ListFilter{}); !errors.Is(err, ErrForbidden) {
		t.Fatal("disabled admin still authorized")
	}
}

func TestConcurrentAdminRemovalKeepsOneAdmin(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	a := f.admin(t, "admin-a")
	b := f.open(t, a, "admin-b", "system_admin")
	bA := Actor{ID: b.Person.ID}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, errs[0] = f.svc.SetEnabled(ctx, a, b.Person.ID, 0, false) }()
	go func() { defer wg.Done(); _, errs[1] = f.svc.SetEnabled(ctx, bA, a.ID, 0, false) }()
	wg.Wait()
	if n := count(t, f.db, `SELECT count(*) FROM system_memberships m JOIN users u ON u.id=m.user_id WHERE m.role='system_admin' AND u.enabled`); n != 1 {
		t.Fatalf("enabled admins after concurrent mutual disable = %d (errs %v)", n, errs)
	}
}

func TestDisableRevokesSessionsAndLiveAndEnableDoesNotRestoreRevokedGrants(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	r := f.open(t, root, "worker2")
	if _, err := f.db.Exec(`INSERT INTO web_sessions(session_hash,user_id,csrf_hash,created_at,last_seen_at,expires_at,idle_expires_at)
VALUES(decode(repeat('ab',32),'hex'),'worker2',decode(repeat('cd',32),'hex'),now(),now(),now()+interval '1 hour',now()+interval '30 minutes')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('worker2','PS1','portfolio_planner'),('worker2','PS2','portfolio_planner')`); err != nil {
		t.Fatal(err)
	}
	out, err := f.svc.SetEnabled(ctx, root, "worker2", r.Person.Version, false)
	if err != nil || out.SessionsRevoked != 1 || out.IdentitySync != "SYNCED" || f.live["worker2"] != 1 {
		t.Fatalf("disable %+v %v live=%d", out, err, f.live["worker2"])
	}
	kc, _ := f.idp.FindByUsername(ctx, "worker2")
	if *kc.Enabled {
		t.Fatal("keycloak still enabled")
	}
	if n := count(t, f.db, `SELECT count(*) FROM web_sessions WHERE user_id='worker2'`); n != 0 {
		t.Fatal("web session survived")
	}
	// The project/portfolio lookups honour users.enabled (no access while disabled).
	if _, err := identity.PortfolioRole(ctx, f.db, "worker2", "PS1"); !errors.Is(err, identity.ErrDenied) {
		t.Fatal("disabled user still has portfolio access")
	}
	// Revoke one grant while disabled; enabling must not bring it back.
	if _, err := f.svc.SetPortfolioRole(ctx, root, "worker2", "PS2", "", "portfolio_planner"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetEnabled(ctx, root, "worker2", 0, true); err != nil {
		t.Fatal(err)
	}
	if n := count(t, f.db, `SELECT count(*) FROM portfolio_memberships WHERE user_id='worker2'`); n != 1 {
		t.Fatal("revoked grant restored on enable")
	}
	// History: created_by, ownership of business records is untouched (users row kept).
	if n := count(t, f.db, `SELECT count(*) FROM audit_logs WHERE target_user_id='worker2' AND (action IN ('account_disabled','portfolio_role_changed') OR (action='account_enabled' AND change->>'via' IS NULL))`); n != 3 {
		t.Fatal("audit trail incomplete", n)
	}
}

func TestDisableWithIdentityProviderDownStillBlocksAndIsRetryable(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	f.open(t, root, "flaky")
	f.idp.Down = true
	out, err := f.svc.SetEnabled(ctx, root, "flaky", 0, false)
	if err != nil || out.IdentitySync != "PENDING" {
		t.Fatalf("local disable must succeed and report pending sync: %+v %v", out, err)
	}
	var enabled bool
	_ = f.db.QueryRow(`SELECT enabled FROM users WHERE id='flaky'`).Scan(&enabled)
	if enabled {
		t.Fatal("local identity must be disabled even if Keycloak is down")
	}
	// Enable is refused while Keycloak is down (it must be enabled there first).
	if _, err = f.svc.SetEnabled(ctx, root, "flaky", 0, true); !errors.Is(err, ErrIdentityProvider) {
		t.Fatal("enable must need the provider", err)
	}
}

func TestResetPasswordRevokesSessionsAndReturnsOnce(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	r := f.open(t, root, "pw-user")
	kc, _ := f.idp.FindByUsername(ctx, "pw-user")
	old := f.idp.Passwords[kc.ID]
	f.idp.Sessions[kc.ID] = 3
	out, err := f.svc.ResetPassword(ctx, root, "pw-user")
	if err != nil || out.TemporaryPassword == "" || out.TemporaryPassword == old || f.idp.Sessions[kc.ID] != 0 {
		t.Fatalf("reset %+v %v", out, err)
	}
	if f.idp.Passwords[kc.ID] != out.TemporaryPassword || r.Person.ID != "pw-user" {
		t.Fatal("password not replaced")
	}
	if n := count(t, f.db, `SELECT count(*) FROM audit_logs WHERE action='password_reset' AND change::text LIKE '%'||$1||'%'`, out.TemporaryPassword); n != 0 {
		t.Fatal("password in audit")
	}
	// Legacy, unmanaged identity cannot be reset from the web.
	if err = identity.Bind(ctx, f.db, "legacy", issuer, "legacy-subject", "Legacy", true, auditlog.Event{Actor: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ResetPassword(ctx, root, "legacy"); !errors.Is(err, ErrInvalid) {
		t.Fatal("unmanaged reset must be refused", err)
	}
}

func TestCandidatesAreMinimalScopedAndBounded(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	f.open(t, root, "picker-a")
	f.open(t, root, "picker-b")
	if _, err := f.svc.SetEnabled(ctx, root, "picker-b", 0, false); err != nil {
		t.Fatal(err)
	}
	out, err := Candidates(ctx, f.db, issuer, "picker", 20)
	if err != nil || len(out) != 1 || out[0].UserID != "picker-a" {
		t.Fatalf("candidates %+v %v", out, err)
	}
	if _, err = Candidates(ctx, f.db, issuer, "", 20); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty query must be refused (no directory enumeration)")
	}
	if out, _ = Candidates(ctx, f.db, "https://other/realms/x", "picker", 20); len(out) != 0 {
		t.Fatal("other issuer leaked")
	}
}

func TestHostAdoptKeepsExistingIdentityAndGrants(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	sub := f.idp.AddExternal("owner")
	if err := identity.Bind(ctx, f.db, "owner-local", issuer, sub, "Owner", true, auditlog.Event{Actor: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('owner-local','PS1','portfolio_manager')`); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.AdoptAdministrator(ctx, f.host, "owner", "")
	if err != nil || p.ID != "owner-local" || p.Username != "owner" || len(p.SystemRoles) != 1 || p.PortfolioCount != 1 {
		t.Fatalf("adopt %+v %v", p, err)
	}
	if _, err = f.svc.AdoptAdministrator(ctx, Actor{ID: "owner-local"}, "owner", ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("adopt is host-only")
	}
}
