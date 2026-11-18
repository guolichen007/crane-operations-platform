package personnel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
)

// gate blocks the FIRST fake Keycloak call named op on id until released, and reports when the
// blocked operation has entered its Keycloak phase. This holds one account operation in the middle
// of its non-atomic sequence (database committed, Keycloak not yet changed) while a competing
// operation runs — the interleavings that sequential tests never exercise.
type gate struct {
	entered, release chan struct{}
	mu               sync.Mutex
	used             bool
}

func (f *fixture) block(op, id string) *gate {
	g := &gate{entered: make(chan struct{}), release: make(chan struct{})}
	f.idp.Hook = func(o, i string) {
		if o != op || i != id {
			return
		}
		g.mu.Lock()
		first := !g.used
		g.used = true
		g.mu.Unlock()
		if first { // only the first caller is held; a competing call passes straight through
			close(g.entered)
			<-g.release
		}
	}
	return g
}

func (g *gate) wait(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("operation never reached its Keycloak phase")
	}
}

func subjectOf(t *testing.T, f *fixture, id string) string {
	t.Helper()
	var s string
	if err := f.db.QueryRow(`SELECT subject FROM users WHERE id=$1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func shortLocks(t *testing.T) {
	old := lockWait
	lockWait = 400 * time.Millisecond
	t.Cleanup(func() { lockWait = old })
}

// Disable is held after its database commit, before Keycloak is disabled. An enable of the same
// person must not interleave (it would leave Keycloak and the database inverted); it waits / is
// refused, and the final state of BOTH sides is consistent.
func TestRaceDisableVersusEnable(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	shortLocks(t)
	root := f.admin(t, "root-admin")
	other := f.open(t, root, "admin-two", "system_admin")
	f.open(t, root, "worker")
	sub := subjectOf(t, f, "worker")
	g := f.block("SetEnabled", sub)
	done := make(chan error, 1)
	go func() { _, err := f.svc.SetEnabled(ctx, root, "worker", 0, false); done <- err }()
	g.wait(t)
	_, err := f.svc.SetEnabled(ctx, Actor{ID: other.Person.ID}, "worker", 0, true)
	if !errors.Is(err, ErrBusy) {
		t.Fatal("enable interleaved with an in-flight disable:", err)
	}
	close(g.release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	var enabled bool
	var state string
	_ = f.db.QueryRow(`SELECT enabled, idp_state FROM users WHERE id='worker'`).Scan(&enabled, &state)
	if enabled || f.idp.Enabled(sub) || state != "SYNCED" {
		t.Fatalf("inconsistent after race: db enabled=%v state=%s keycloak enabled=%v", enabled, state, f.idp.Enabled(sub))
	}
	// Afterwards the enable succeeds normally and both sides agree.
	f.idp.Hook = nil
	if _, err = f.svc.SetEnabled(ctx, Actor{ID: other.Person.ID}, "worker", 0, true); err != nil || !f.idp.Enabled(sub) {
		t.Fatal("enable after the race", err)
	}
}

// Two administrators reset the same password at the same time: they are serialized; the password
// that is finally set in Keycloak is exactly the one returned by the LAST successful reset (no
// administrator holds a password that does not work).
func TestRaceTwoAdministratorsResetPassword(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	other := f.open(t, root, "admin-two", "system_admin")
	f.open(t, root, "worker")
	sub := subjectOf(t, f, "worker")
	// Reset A is held after Keycloak accepted its password, before it returns it.
	g := f.block("Logout", sub)
	type res struct {
		pw  string
		err error
	}
	a, b := make(chan res, 1), make(chan res, 1)
	go func() { out, err := f.svc.ResetPassword(ctx, root, "worker"); a <- res{out.TemporaryPassword, err} }()
	g.wait(t)
	// Reset B starts while A is in flight: it must wait for A (not overwrite A's password and let A
	// hand out a password that no longer works).
	go func() {
		out, err := f.svc.ResetPassword(ctx, Actor{ID: other.Person.ID}, "worker")
		b <- res{out.TemporaryPassword, err}
	}()
	time.Sleep(300 * time.Millisecond)
	select {
	case r := <-b:
		t.Fatalf("second reset completed while the first was in flight (err=%v)", r.err)
	default:
	}
	close(g.release)
	ra, rb := <-a, <-b
	if ra.err != nil || rb.err != nil {
		t.Fatal(ra.err, rb.err)
	}
	if ra.pw == rb.pw || f.idp.Password(sub) != rb.pw {
		t.Fatal("serialized resets: the password in Keycloak must be the one returned by the later reset")
	}
	if n := count(t, f.db, `SELECT count(*) FROM audit_logs WHERE target_user_id='worker' AND action='password_reset' AND result='success'`); n != 2 {
		t.Fatal("both resets must be audited", n)
	}
}

// An account manager starts disabling a plain user; meanwhile a system administrator tries to make
// that user a system administrator. The role change cannot slip in during the operation (it would
// let an account manager act on a system-role holder); once the account manager's operation is
// done, the grant is refused because the user is now disabled, and a later account-manager action
// on the (re-enabled, promoted) user is refused.
func TestRaceTargetGainsSystemRoleDuringAccountManagerOperation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	shortLocks(t)
	root := f.admin(t, "root-admin")
	mgr := f.open(t, root, "acct-mgr", "account_manager")
	f.open(t, root, "worker")
	sub := subjectOf(t, f, "worker")
	g := f.block("SetEnabled", sub)
	done := make(chan error, 1)
	go func() { _, err := f.svc.SetEnabled(ctx, Actor{ID: mgr.Person.ID}, "worker", 0, false); done <- err }()
	g.wait(t)
	if _, err := f.svc.SetSystemRoles(ctx, root, "worker", []string{"system_admin"}, []string{}); !errors.Is(err, ErrBusy) {
		t.Fatal("system role granted while an account manager operation was in flight:", err)
	}
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f.idp.Hook = nil
	if _, err := f.svc.SetEnabled(ctx, root, "worker", 0, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetSystemRoles(ctx, root, "worker", []string{"system_admin"}, []string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ResetPassword(ctx, Actor{ID: mgr.Person.ID}, "worker"); !errors.Is(err, ErrProtected) {
		t.Fatal("account manager acted on a system role holder:", err)
	}
}

// An administrator's permission is removed while one of their operations is held in its Keycloak
// phase: the removal waits for that operation (no half-authorized operation), and every operation
// started after the removal is refused.
func TestRaceActorLosesPermissionMidOperation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	shortLocks(t)
	root := f.admin(t, "root-admin")
	mgr := f.open(t, root, "acct-mgr", "account_manager")
	mgrA := Actor{ID: mgr.Person.ID}
	f.open(t, root, "worker")
	f.open(t, root, "worker-b")
	sub := subjectOf(t, f, "worker")
	g := f.block("SetTemporaryPassword", sub)
	done := make(chan error, 1)
	go func() { _, err := f.svc.ResetPassword(ctx, mgrA, "worker"); done <- err }()
	g.wait(t)
	if _, err := f.svc.SetSystemRoles(ctx, root, mgr.Person.ID, []string{}, []string{"account_manager"}); !errors.Is(err, ErrBusy) {
		t.Fatal("actor's role removed while their operation was in flight:", err)
	}
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f.idp.Hook = nil
	if _, err := f.svc.SetSystemRoles(ctx, root, mgr.Person.ID, []string{}, []string{"account_manager"}); err != nil {
		t.Fatal(err)
	}
	for name, op := range map[string]func() error{
		"reset":   func() error { _, err := f.svc.ResetPassword(ctx, mgrA, "worker-b"); return err },
		"disable": func() error { _, err := f.svc.SetEnabled(ctx, mgrA, "worker-b", 0, false); return err },
		"revoke":  func() error { _, err := f.svc.RevokeSessions(ctx, mgrA, "worker-b"); return err },
		"provision": func() error {
			_, err := f.svc.ProvisionAccount(ctx, mgrA, "key-after-revoke-01", ProvisionRequest{Username: "late-user", Profile: Profile{DisplayName: "x"}})
			return err
		},
	} {
		if err := op(); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s after the actor lost the permission: %v", name, err)
		}
	}
}

// Session fence: a reset / revoke moves users.sessions_revoked_at. Any login whose authentication
// happened at or before it (Keycloak SSO reuse, a callback already in flight) is refused by the
// BFF; see internal/webauth TestCallbackRefusesAuthenticationBeforeSessionRevocation. Here: the
// fence moves on every reset / revoke / disable and again after Keycloak confirmed the change.
func TestSessionFenceMovesOnResetRevokeDisable(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	f.open(t, root, "worker")
	fenceAt := func() time.Time {
		var at time.Time
		if err := f.db.QueryRow(`SELECT COALESCE(sessions_revoked_at, 'epoch') FROM users WHERE id='worker'`).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	prev := fenceAt()
	for name, op := range map[string]func() error{
		"reset":   func() error { _, err := f.svc.ResetPassword(ctx, root, "worker"); return err },
		"revoke":  func() error { _, err := f.svc.RevokeSessions(ctx, root, "worker"); return err },
		"disable": func() error { _, err := f.svc.SetEnabled(ctx, root, "worker", 0, false); return err },
	} {
		time.Sleep(5 * time.Millisecond)
		if err := op(); err != nil {
			t.Fatal(name, err)
		}
		now := fenceAt()
		if !now.After(prev) {
			t.Fatalf("%s did not move the session fence", name)
		}
		prev = now
	}
	// The second fence (after Keycloak) is later than the local commit: a login created between the
	// two is deleted. Simulate one: insert a session during the Keycloak phase of a reset.
	_, _ = f.svc.SetEnabled(ctx, root, "worker", 0, true)
	sub := subjectOf(t, f, "worker")
	g := f.block("Logout", sub)
	done := make(chan error, 1)
	go func() { _, err := f.svc.ResetPassword(ctx, root, "worker"); done <- err }()
	g.wait(t)
	if _, err := f.db.Exec(`INSERT INTO web_sessions(session_hash,user_id,csrf_hash,created_at,last_seen_at,expires_at,idle_expires_at)
VALUES(decode(repeat('ef',32),'hex'),'worker',decode(repeat('cd',32),'hex'),now(),now(),now()+interval '1 hour',now()+interval '30 minutes')`); err != nil {
		t.Fatal(err)
	}
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := count(t, f.db, `SELECT count(*) FROM web_sessions WHERE user_id='worker'`); n != 0 {
		t.Fatal("a session created during the reset survived it")
	}
}

// Provision state guard: an account of an unfinished opening (BOUND: local row + grants exist,
// both sides still disabled) cannot be enabled, reset or re-roled through the ordinary endpoints;
// it can only be resumed or (before BOUND) cancelled through the provisioning flow.
func TestProvisionIncompleteCannotBeBypassed(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	crash := true
	f.svc.Fault = func(step string) error {
		if step == "after_password" && crash {
			crash = false
			return errors.New("simulated crash")
		}
		return nil
	}
	req := ProvisionRequest{Username: "half-open", Profile: Profile{DisplayName: "半开户"}, PortfolioGrants: []PortfolioGrantInput{{ScopeID: "PS1", Role: "portfolio_planner"}}}
	if _, err := f.svc.ProvisionAccount(ctx, root, "key-half-0001", req); err == nil {
		t.Fatal("expected crash")
	}
	for name, op := range map[string]func() error{
		"enable": func() error { _, err := f.svc.SetEnabled(ctx, root, "half-open", 0, true); return err },
		"reset":  func() error { _, err := f.svc.ResetPassword(ctx, root, "half-open"); return err },
		"sysrole": func() error {
			_, err := f.svc.SetSystemRoles(ctx, root, "half-open", []string{"system_admin"}, []string{})
			return err
		},
		"portfolio": func() error {
			_, err := f.svc.SetPortfolioRole(ctx, root, "half-open", "PS2", "portfolio_manager", "")
			return err
		},
	} {
		if err := op(); !errors.Is(err, ErrProvisionIncomplete) && !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s bypassed the unfinished opening: %v", name, err)
		}
	}
	var enabled bool
	_ = f.db.QueryRow(`SELECT enabled FROM users WHERE id='half-open'`).Scan(&enabled)
	if enabled {
		t.Fatal("unfinished account became enabled")
	}
	var id string
	_ = f.db.QueryRow(`SELECT id FROM account_provisions WHERE username='half-open'`).Scan(&id)
	if err := f.svc.CancelProvision(ctx, root, id); !errors.Is(err, ErrConflict) {
		t.Fatal("a BOUND opening must be resumed, not cancelled:", err)
	}
	r, err := f.svc.ResumeProvision(ctx, root, id)
	if err != nil || r.State != "COMPLETED" || r.TemporaryPassword == "" {
		t.Fatal("resume", err)
	}
	if _, err = f.svc.SetEnabled(ctx, root, "half-open", 0, false); err != nil {
		t.Fatal("after completion ordinary operations work:", err)
	}
}

// A high-privilege opening (system role) left unfinished can only be resumed or cancelled by an
// administrator who may grant that role now; an account manager is refused, and so is the original
// administrator after losing system_admin.
func TestUnfinishedHighPrivilegeProvisionNeedsMatchingCapability(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	other := f.open(t, root, "admin-two", "system_admin")
	mgr := f.open(t, root, "acct-mgr", "account_manager")
	f.idp.LoseCreateResponse = true
	req := ProvisionRequest{Username: "new-admin", Profile: Profile{DisplayName: "新管理员"}, SystemRoles: []string{"system_admin"}}
	if _, err := f.svc.ProvisionAccount(ctx, root, "key-hp-00001", req); err == nil {
		t.Fatal("expected lost response")
	}
	var id string
	_ = f.db.QueryRow(`SELECT id FROM account_provisions WHERE username='new-admin'`).Scan(&id)
	mgrA := Actor{ID: mgr.Person.ID}
	if _, err := f.svc.ResumeProvision(ctx, mgrA, id); !errors.Is(err, ErrForbidden) {
		t.Fatal("account manager resumed a system_admin opening:", err)
	}
	if err := f.svc.CancelProvision(ctx, mgrA, id); !errors.Is(err, ErrForbidden) {
		t.Fatal("account manager cancelled a system_admin opening:", err)
	}
	// The original administrator loses system_admin → can no longer continue it either.
	if _, err := f.svc.SetSystemRoles(ctx, Actor{ID: other.Person.ID}, root.ID, []string{"account_manager"}, []string{"system_admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ProvisionAccount(ctx, root, "key-hp-00001", req); !errors.Is(err, ErrForbidden) {
		t.Fatal("demoted administrator continued a system_admin opening:", err)
	}
	r, err := f.svc.ResumeProvision(ctx, Actor{ID: other.Person.ID}, id)
	if err != nil || r.State != "COMPLETED" || len(r.Person.SystemRoles) != 1 {
		t.Fatal("system administrator resume", err)
	}
	if n := count(t, f.db, `SELECT count(*) FROM audit_logs WHERE action='personnel_created' AND target_user_id='new-admin' AND user_id=$1`, other.Person.ID); n != 1 {
		t.Fatal("the completing administrator must be the audited actor", n)
	}
}

// Resume of an opening races with a role change on the person being opened: serialized, the role
// change is refused while the opening is unfinished and works after completion.
func TestRaceResumeVersusRoleChange(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	shortLocks(t)
	root := f.admin(t, "root-admin")
	f.idp.LoseCreateResponse = true
	req := ProvisionRequest{Username: "racer", Profile: Profile{DisplayName: "竞争"}}
	_, _ = f.svc.ProvisionAccount(ctx, root, "key-race-0001", req)
	var id string
	_ = f.db.QueryRow(`SELECT id FROM account_provisions WHERE username='racer'`).Scan(&id)
	kc, _ := f.idp.FindByUsername(ctx, "racer")
	g := f.block("SetTemporaryPassword", kc.ID)
	done := make(chan error, 1)
	go func() { _, err := f.svc.ResumeProvision(ctx, root, id); done <- err }()
	g.wait(t)
	if _, err := f.svc.SetSystemRoles(ctx, Actor{ID: "host-x", Host: true, Event: auditlog.Event{}}, "racer", []string{"account_manager"}, []string{}); !errors.Is(err, ErrBusy) {
		t.Fatal("role change interleaved with the resumed opening:", err)
	}
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f.idp.Hook = nil
	if _, err := f.svc.SetSystemRoles(ctx, root, "racer", []string{"account_manager"}, []string{}); err != nil {
		t.Fatal(err)
	}
}
