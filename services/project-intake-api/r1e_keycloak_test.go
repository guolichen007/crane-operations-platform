package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestR1EKeycloakAdapter runs the account service against a real Keycloak 26.8.1
// prepared by tests/baobei/keycloak-realm-probe.py (realms `baobei` and `crane`).
// It is skipped unless BAOBEI_KEYCLOAK_URL and BAOBEI_KEYCLOAK_SERVICE_SECRET are
// set; the CI fixture job runs it with a disposable Keycloak.
func TestR1EKeycloakAdapter(t *testing.T) {
	base, secret := os.Getenv("BAOBEI_KEYCLOAK_URL"), os.Getenv("BAOBEI_KEYCLOAK_SERVICE_SECRET")
	if base == "" || secret == "" {
		t.Skip("NOT_RUN: real Keycloak fixture required")
	}
	kc, err := newKeycloakAdmin(base+"/realms/baobei", "baobei-web", "baobei-account-service", secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	db := b2Database(t)
	app := &App{db: db, cfg: Config{Env: "development", Dev: true}, accounts: kc}
	server := httptest.NewServer(app.handler())
	defer server.Close()
	app.cfg.Origin = server.URL
	f := &accountFixture{t: t, app: app, server: server, admin: login(t, server, "synthetic-admin")}
	phone := "139" + randDigits(8)
	m := f.invite("真实 Keycloak 开户", phone, salesRole)
	s, res, b := f.account(f.admin, m.ID, "create", "kc-create-"+randDigits(12), map[string]string{})
	expect(t, s, 200, b)
	if res.Credential == nil || res.Credential.Login != phone {
		t.Fatal(string(b))
	}
	var subject string
	_ = db.QueryRow(context.Background(), `SELECT subject FROM app_users WHERE staff_id=$1`, m.ID).Scan(&subject)
	u, err := kc.getUser(context.Background(), subject)
	if err != nil || u == nil || u.Username != res.Credential.Login || u.Enabled == nil || !*u.Enabled ||
		len(u.RequiredActions) != 1 || u.RequiredActions[0] != "UPDATE_PASSWORD" {
		t.Fatalf("keycloak user after create: %+v %v", u, err)
	}
	roles, _ := kc.roles(context.Background(), subject)
	if !exactClientRole(baobeiRoles(roles), salesRole) {
		t.Fatal("exact role", roles)
	}
	// Duplicate phone at the provider: a second member with the same phone cannot be created
	// (the business database refuses it first; the provider refuses it too).
	if _, err := kc.createDisabled(context.Background(), res.Credential.Login, "00000000-0000-4000-8000-000000000009"); err == nil {
		t.Fatal("provider accepted a duplicate username")
	}
	// Change phone keeps the UUID.
	phone2 := "139" + randDigits(8)
	s, res, b = f.account(f.admin, m.ID, "change-phone", "kc-phone-"+randDigits(12), map[string]string{"phone": phone2, "reason": "换号"})
	expect(t, s, 200, b)
	u, _ = kc.getUser(context.Background(), subject)
	if u == nil || u.ID != subject || u.Username != phone2 {
		t.Fatalf("after change-phone: %+v", u)
	}
	// Reset and disable/enable.
	s, res, b = f.account(f.admin, m.ID, "reset-password", "kc-reset-"+randDigits(12), map[string]string{"reason": "重置"})
	expect(t, s, 200, b)
	s, _, b = f.account(f.admin, m.ID, "disable", "kc-disable-"+randDigits(12), map[string]string{"reason": "停用"})
	expect(t, s, 200, b)
	u, _ = kc.getUser(context.Background(), subject)
	if u == nil || u.Enabled == nil || *u.Enabled {
		t.Fatal("keycloak user still enabled after disable")
	}
	s, _, b = f.account(f.admin, m.ID, "enable", "kc-enable-"+randDigits(12), map[string]string{"reason": "恢复"})
	expect(t, s, 200, b)
	// The service cannot reach another realm through any path.
	for _, p := range []string{"/../crane/users", "/users/../../crane/users"} {
		if _, _, err := kc.call(context.Background(), "GET", p, nil, nil); err == nil {
			t.Fatal("path accepted", p)
		}
	}
	cross := &keycloakAdmin{base: kc.base, realm: "crane", clientID: kc.clientID, secret: kc.secret, http: http.DefaultClient}
	if _, err := cross.accessToken(context.Background()); err == nil {
		t.Fatal("baobei service credentials accepted by the crane realm")
	}
	out, _ := json.Marshal(map[string]any{"subject": subject, "login": phone2})
	t.Logf("REAL_KEYCLOAK_ACCOUNT_LOOP=PASS %s", out)
	if path := os.Getenv("BAOBEI_KEYCLOAK_RESULT"); path != "" && res.Credential != nil {
		// Hand-off to the real-login browser check (loopback fixture only).
		_ = os.WriteFile(path, []byte(phone2+"\n"+res.Credential.TemporaryPassword+"\n"), 0o600)
	}
}

func randDigits(n int) string {
	t := randomToken()
	out := make([]byte, 0, n)
	for i := 0; len(out) < n; i++ {
		out = append(out, '0'+t[i%len(t)]%10)
	}
	return string(out)
}
