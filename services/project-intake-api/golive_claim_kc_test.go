package main

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestGoLiveClaimRealKeycloak: the first-time claim against a real Keycloak
// 26.8.1 (fixture prepared by tests/baobei/keycloak-realm-probe.py). The
// temporary login name is the Keycloak username until the claim; the claim
// renames the user to the phone (same UUID, same role) and the temporary name
// no longer exists. Skipped without the fixture.
func TestGoLiveClaimRealKeycloak(t *testing.T) {
	base, secret := os.Getenv("BAOBEI_KEYCLOAK_URL"), os.Getenv("BAOBEI_KEYCLOAK_SERVICE_SECRET")
	if base == "" || secret == "" {
		t.Skip("NOT_RUN: real Keycloak fixture required")
	}
	kc, err := newKeycloakAdmin(base+"/realms/baobei", "baobei-web", "baobei-account-service", secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := b2Database(t)
	app := &App{db: db, cfg: Config{Env: "development", Dev: true}, accounts: kc}
	server := httptest.NewServer(app.handler())
	defer server.Close()
	app.cfg.Origin = server.URL
	f := &accountFixture{t: t, app: app, server: server, admin: login(t, server, "synthetic-admin")}
	temp := "kc" + strings.ToLower(randDigits(10))
	m := f.inviteClaim("真实认领", temp, salesRole)
	s, res, b := f.account(f.admin, m.ID, "create", "kc-claim-create-"+randDigits(10), map[string]string{})
	expect(t, s, 200, b)
	if res.Credential == nil || res.Credential.Login != temp {
		t.Fatal("temporary credential", string(b))
	}
	var subject string
	_ = db.QueryRow(ctx, `SELECT subject FROM app_users WHERE staff_id=$1`, m.ID).Scan(&subject)
	u, err := kc.getUser(ctx, subject)
	if err != nil || u == nil || u.Username != temp || !enabled(u) || len(u.RequiredActions) != 1 || u.RequiredActions[0] != "UPDATE_PASSWORD" {
		t.Fatalf("keycloak user with temporary name: %+v %v", u, err)
	}
	// The member's session (the browser login itself is covered by the
	// real-login check); the claim request is the same as from the page.
	if _, err := db.Exec(ctx, `UPDATE app_users SET staff_status='ACTIVE',active=true WHERE staff_id=$1`, m.ID); err != nil {
		t.Fatal(err)
	}
	emp := login(t, server, "synthetic-a")
	if _, err := db.Exec(ctx, `UPDATE app_sessions SET subject=$1 WHERE token_hash IN (SELECT token_hash FROM app_sessions WHERE subject='synthetic-a' ORDER BY expires_at DESC LIMIT 1)`, subject); err != nil {
		t.Fatal(err)
	}
	s2, b2 := emp.call("GET", "/api/v1/me", nil)
	if s2 != 200 || !strings.Contains(string(b2), `"claimRequired":true`) {
		t.Fatal("claim gate", s2, string(b2))
	}
	var me User
	_ = jsonUnmarshal(b2, &me)
	emp.csrf = me.CSRF
	phone := "139" + randDigits(8)
	s, out, body := f.claim(emp, "kc-claim-"+randDigits(12), "真实姓名", phone)
	if s != 200 || out.Login != phone {
		t.Fatal("claim", s, body)
	}
	u, err = kc.getUser(ctx, subject)
	if err != nil || u == nil || u.ID != subject || u.Username != phone || !enabled(u) {
		t.Fatalf("keycloak user after claim: %+v %v", u, err)
	}
	if old, _ := kc.findByUsername(ctx, temp); old != nil {
		t.Fatal("temporary login name still exists in Keycloak")
	}
	roles, _ := kc.roles(ctx, subject)
	if !exactClientRole(baobeiRoles(roles), salesRole) {
		t.Fatal("role changed by the claim", roles)
	}
	t.Logf("REAL_KEYCLOAK_CLAIM=PASS")
}
