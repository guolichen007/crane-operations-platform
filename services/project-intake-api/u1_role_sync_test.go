package main

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// TestDatabaseU1RoleSyncLoop (G5-C-U1, CI): the person/role loop with the
// development account service (same contract as the Keycloak adapter; the real
// Keycloak run is TestU1RealKeycloakRoleLoop). Promotion → role sync → own login,
// with the negative controls: unverified member, missing reauthentication, wrong
// provider role, old session, double submit and timeout retry.
func TestDatabaseU1RoleSyncLoop(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	db := f.app.db
	reauth := func(fresh bool) {
		t.Helper()
		q := `UPDATE staff_sessions SET reauthenticated_at=now()`
		if !fresh {
			q = `UPDATE staff_sessions SET reauthenticated_at=now()-interval '10 minutes'`
		}
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	patch := func(m staffMember, role, reason string, verify bool) (int, []byte) {
		return f.admin.call("PATCH", "/api/v1/staff/"+m.ID, staffProfileInput{Name: m.Name, Phone: m.Phone, Email: m.Email,
			TeamID: m.TeamID, Note: m.Note, Role: role, Reason: reason, Version: m.Version, VerifyIdentity: verify})
	}
	devRole := func(staffID string) string {
		var subject string
		_ = db.QueryRow(ctx, `SELECT subject FROM app_users WHERE staff_id=$1`, staffID).Scan(&subject)
		r, _ := f.dev.roles(ctx, subject)
		return strings.Join(r, ",")
	}
	ops := func(staffID, op string) int {
		var n int
		_ = db.QueryRow(ctx, `SELECT count(*) FROM account_operations WHERE staff_id=$1 AND operation=$2`, staffID, op).Scan(&n)
		return n
	}

	phone := "13960000101"
	m := f.invite("合成U1晋升销售", phone, salesRole)
	s, _, b := f.account(f.admin, m.ID, "create", "u1-create-0000000001", map[string]string{})
	expect(t, s, 200, b)
	// Not yet verified (never signed in): promotion refused.
	reauth(true)
	m = f.member(m.ID)
	if s, b = patch(m, managerRole, "晋升", false); s != 409 || !strings.Contains(string(b), "STAFF_IDENTITY_NOT_VERIFIED") {
		t.Fatal("unverified member promoted", s, string(b))
	}
	sales, s := f.signIn(phone)
	expect(t, s, 200, nil)
	m = f.member(m.ID)
	if m.Status != staffActive || !m.IdentityVerified {
		t.Fatalf("after first login: %+v", m)
	}

	// Promotion without a fresh password check: refused, nothing changes.
	reauth(false)
	if s, b = patch(m, managerRole, "晋升销售主管", false); s != 403 || !strings.Contains(string(b), "STAFF_REAUTH_REQUIRED") {
		t.Fatal("promotion without reauthentication", s, string(b))
	}
	reauth(true)
	if s, b = patch(m, managerRole, "晋升销售主管", false); s != 200 {
		t.Fatal("promote", s, string(b))
	}
	// HTTP 200 = submitted only: pending identity processing, old session gone,
	// the login still carries the old role and cannot sign in.
	m = f.member(m.ID)
	if m.Status != staffPending || m.Role != managerRole || m.IdentityVerified || devRole(m.ID) != salesRole {
		t.Fatalf("after promote: %+v role=%s", m, devRole(m.ID))
	}
	if s, _ = sales.call("GET", "/api/v1/projects", nil); s != 401 {
		t.Fatal("old sales session kept its permission", s)
	}
	if _, s = f.signIn(phone); s == 200 {
		t.Fatal("signed in before the role sync")
	}

	// Manager role sync without a fresh password check: refused before the provider.
	reauth(false)
	calls := f.dev.callCount("role")
	if s, _, b = f.account(f.admin, m.ID, "sync-role", "u1-sync-stale-000001", map[string]string{}); s != 403 || !strings.Contains(string(b), "STAFF_REAUTH_REQUIRED") {
		t.Fatal("manager sync without reauthentication", s, string(b))
	}
	if f.dev.callCount("role") != calls || devRole(m.ID) != salesRole {
		t.Fatal("refused sync reached the provider")
	}
	reauth(true)
	// Double submit with the same key: one execution; the other is busy or a replay.
	key := "u1-sync-lead-0000001"
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _, _ = f.account(f.admin, m.ID, "sync-role", key, map[string]string{})
		}(i)
	}
	wg.Wait()
	if ops(m.ID, "SET_ROLE") != 1 || codes[0] != 200 && codes[1] != 200 {
		t.Fatal("double submit", codes, ops(m.ID, "SET_ROLE"))
	}
	// Timeout retry (same key): replayed, not executed again.
	s, res, b := f.account(f.admin, m.ID, "sync-role", key, map[string]string{})
	expect(t, s, 200, b)
	if !res.Replayed || ops(m.ID, "SET_ROLE") != 1 {
		t.Fatal("retry repeated the operation", string(b))
	}
	m = f.member(m.ID)
	if m.Status != staffActivation || m.AccountState != accountReady || devRole(m.ID) != managerRole || m.IdentityVerified {
		t.Fatalf("after sync: %+v role=%s", m, devRole(m.ID))
	}

	// Wrong role at the provider: activation refused, state unchanged.
	var subject string
	_ = db.QueryRow(ctx, `SELECT subject FROM app_users WHERE staff_id=$1`, m.ID).Scan(&subject)
	f.dev.mu.Lock()
	f.dev.users[subject].role = directorRole
	f.dev.mu.Unlock()
	if _, s = f.signIn(phone); s == 200 {
		t.Fatal("wrong provider role activated the member")
	}
	if got := f.member(m.ID); got.Status != staffActivation {
		t.Fatalf("wrong-role login changed the state: %+v", got)
	}
	f.dev.mu.Lock()
	f.dev.users[subject].role = managerRole
	f.dev.mu.Unlock()

	// Own login: manager, active, verified. The old sales session stays dead.
	lead, s := f.signIn(phone)
	expect(t, s, 200, nil)
	m = f.member(m.ID)
	if m.Status != staffActive || m.Role != managerRole || !m.IdentityVerified {
		t.Fatalf("after manager login: %+v", m)
	}
	if s, b = lead.call("GET", "/api/v1/me", nil); s != 200 || !strings.Contains(string(b), `"role":"baobei_manager"`) {
		t.Fatal("manager session", s, string(b))
	}
	if s, _ = sales.call("GET", "/api/v1/me", nil); s != 401 {
		t.Fatal("old sales session revived", s)
	}
	// Only administrators may sync roles.
	if s, _, _ = f.account(lead, m.ID, "sync-role", "u1-sync-by-lead-0001", map[string]string{}); s != 403 {
		t.Fatal("manager called the role sync", s)
	}
}
