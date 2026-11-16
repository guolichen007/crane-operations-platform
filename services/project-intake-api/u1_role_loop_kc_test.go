package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestU1RealKeycloakRoleLoop (G5-C-U1): the person/role loop against a real
// Keycloak 26.8.1 dedicated realm and real PostgreSQL, through the real OIDC login:
//
//	existing (legacy) sales re-verified → login account → role sync → own login
//	ACTIVE → promoted to manager → manager role sync → own login again → the old
//	session and the old permission are gone.
//
// Negative controls: HTTP 200 of the PATCH alone grants nothing; a wrong Keycloak
// role cannot activate; no reauthentication → no manager role; an unverified
// member cannot be promoted; repeated and retried requests are idempotent.
// Skipped unless the loopback Keycloak fixture is present (~/kc/with-kc.sh).
func TestU1RealKeycloakRoleLoop(t *testing.T) {
	base, svc, web := os.Getenv("BAOBEI_KEYCLOAK_URL"), os.Getenv("BAOBEI_KEYCLOAK_SERVICE_SECRET"), os.Getenv("BAOBEI_KC_BAOBEI_WEB_SECRET")
	if base == "" || svc == "" || web == "" {
		t.Skip("NOT_RUN: real Keycloak fixture required")
	}
	ctx := context.Background()
	db := b2Database(t)
	issuer := base + "/realms/baobei"
	trustIssuer(t, db, issuer)
	k, err := newKeycloakAdmin(issuer, "baobei-web", "baobei-account-service", svc, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The real API (OIDC against the dedicated realm) and an administrator API
	// sharing the database (synthetic administrator session, same issuer).
	var app *App
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { app.handler().ServeHTTP(w, r) }))
	defer server.Close()
	if app, err = newApp(ctx, db, Config{Env: "development", Issuer: issuer, ClientID: "baobei-web", Secret: web, Origin: server.URL}); err != nil {
		t.Fatal(err)
	}
	app.oauth.RedirectURL = server.URL + "/auth/callback"
	app.accounts, app.linkedOnly = k, true
	adminApp := &App{db: db, cfg: Config{Env: "development", Dev: true, Issuer: issuer}, accounts: k, linkedOnly: true}
	adminServer := httptest.NewServer(adminApp.handler())
	defer adminServer.Close()
	adminApp.cfg.Origin = adminServer.URL
	f := &accountFixture{t: t, app: adminApp, server: adminServer, admin: login(t, adminServer, "synthetic-admin")}
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
	member := func(id string) staffMember { return f.member(id) }
	patch := func(m staffMember, role, reason string, verify bool) (int, []byte) {
		return f.admin.call("PATCH", "/api/v1/staff/"+m.ID, staffProfileInput{Name: m.Name, Phone: m.Phone, Email: m.Email,
			TeamID: m.TeamID, Note: m.Note, Role: role, Reason: reason, Version: m.Version, VerifyIdentity: verify})
	}
	kcRoles := func(staffID string) []string {
		var ext string
		_ = db.QueryRow(ctx, `SELECT l.external_subject FROM identity_links l JOIN app_users u ON u.subject=l.subject WHERE u.staff_id=$1 AND l.issuer=$2`, staffID, issuer).Scan(&ext)
		r, err := k.roles(ctx, ext)
		if err != nil {
			t.Fatal(err)
		}
		return baobeiRoles(r)
	}
	signIn := func(user, pass, next string) (*http.Client, int, string) {
		t.Helper()
		return u1KeycloakSignIn(t, server, user, pass, next)
	}

	// 0. An existing sales member with shared-realm history (business subject =
	//    old Keycloak id, ACTIVE, never re-verified under staff governance).
	legacy := newUUID()
	phone := "135" + randDigits(8)
	var staffID string
	if err := db.QueryRow(ctx, `INSERT INTO app_users(subject,display_name,role,team_id,work_phone,staff_managed)
 VALUES($1,'合成U1历史销售','baobei_sales',$2,$3,true) RETURNING staff_id::text`, legacy, syntheticTeam, "+86"+phone).Scan(&staffID); err != nil {
		t.Fatal(err)
	}
	m := member(staffID)
	if m.Status != staffActive || m.IdentityVerified || !m.IdentityLinked {
		t.Fatalf("fixture: %+v", m)
	}
	// An unverified member cannot be promoted (reauthenticated or not).
	reauth(true)
	if s, b := patch(m, managerRole, "晋升", false); s != 409 || !strings.Contains(string(b), "STAFF_IDENTITY_NOT_VERIFIED") {
		t.Fatal("unverified member promoted", s, string(b))
	}

	// 1. Re-verify: HTTP 200 only opens the identity process.
	if s, b := patch(m, salesRole, "迁移到独立登录前重新核验", true); s != 200 {
		t.Fatal("verify", s, string(b))
	}
	m = member(staffID)
	if m.Status != staffPending || m.IdentityVerified {
		t.Fatalf("verify must not activate or verify by itself: %+v", m)
	}
	// 2. Login account in the dedicated realm (one-time password).
	s, res, b := f.account(f.admin, staffID, "create", "u1-create-"+randDigits(10), map[string]string{})
	expect(t, s, 200, b)
	cred := res.Credential
	m = member(staffID)
	t.Logf("U1_AFTER_VERIFY_AND_CREATE status=%s account=%s verified=%v", m.Status, m.AccountState, m.IdentityVerified)
	newPass := "U1-" + randomToken()[:12]
	if _, s, code := signIn(cred.Login, cred.TemporaryPassword, newPass); s == 200 {
		t.Fatal("a re-verification awaiting identity processing signed in")
	} else {
		t.Logf("U1_LOGIN_BEFORE_SYNC refused=%s", code)
	}
	if got := member(staffID); got.Status != staffPending {
		t.Fatalf("login attempt changed the state: %+v", got)
	}
	// 3. Role sync (sales: no reauthentication needed): exact role, then 待本人激活.
	syncKey := "u1-sync-" + randDigits(10)
	s, res, b = f.account(f.admin, staffID, "sync-role", syncKey, map[string]string{})
	expect(t, s, 200, b)
	if res.Member.Status != staffActivation || res.Member.AccountState != accountReady || res.Member.IdentityVerified {
		t.Fatalf("after sync: %+v", res.Member)
	}
	if r := kcRoles(staffID); !exactClientRole(r, salesRole) {
		t.Fatal("keycloak role after sync", r)
	}
	// Same key again (double click / timeout retry): replay, nothing repeated.
	s, again, b := f.account(f.admin, staffID, "sync-role", syncKey, map[string]string{})
	expect(t, s, 200, b)
	if !again.Replayed {
		t.Fatal("sync replay", string(b))
	}
	// 4. Own login: ACTIVE, identity verified (exact subject + exact client role).
	sales, s, code := signIn(cred.Login, newPass, newPass)
	if s != 200 {
		// The temporary password may still be required (first login of this user).
		sales, s, code = signIn(cred.Login, cred.TemporaryPassword, newPass)
	}
	if s != 200 {
		t.Fatal("own login after sync", s, code)
	}
	m = member(staffID)
	if m.Status != staffActive || !m.IdentityVerified || m.Role != salesRole {
		t.Fatalf("after own login: %+v", m)
	}
	if me := u1Me(t, sales, server.URL); me.Role != salesRole {
		t.Fatal("session role", me.Role)
	}

	// 5. Promote to manager: needs a fresh password check; 200 grants nothing yet.
	reauth(false)
	if s, b = patch(m, managerRole, "晋升销售主管", false); s != 403 || !strings.Contains(string(b), "STAFF_REAUTH_REQUIRED") {
		t.Fatal("promotion without reauthentication", s, string(b))
	}
	reauth(true)
	if s, b = patch(m, managerRole, "晋升销售主管", false); s != 200 {
		t.Fatal("promote", s, string(b))
	}
	if r, _ := sales.Get(server.URL + "/api/v1/me"); r.StatusCode != 401 {
		t.Fatal("old sales session survived the promotion", r.StatusCode)
	}
	m = member(staffID)
	if m.Status != staffPending || m.Role != managerRole {
		t.Fatalf("after promote: %+v", m)
	}
	if r := kcRoles(staffID); !exactClientRole(r, salesRole) {
		t.Fatal("promotion must not touch the login role by itself", r)
	}
	// The member logs in again before the sync: the old role cannot get back in.
	if _, s, code = signIn(cred.Login, newPass, newPass); s == 200 {
		t.Fatal("old sales role signed in after the promotion")
	} else {
		t.Logf("U1_LOGIN_AFTER_PROMOTE_BEFORE_SYNC refused=%s", code)
	}
	// 6. Manager role sync: a manager role needs a fresh password check too.
	reauth(false)
	if s, _, b = f.account(f.admin, staffID, "sync-role", "u1-sync-lead-"+randDigits(8), map[string]string{}); s != 403 || !strings.Contains(string(b), "STAFF_REAUTH_REQUIRED") {
		t.Fatal("manager role sync without reauthentication", s, string(b))
	}
	if r := kcRoles(staffID); !exactClientRole(r, salesRole) {
		t.Fatal("refused sync reached Keycloak", r)
	}
	reauth(true)
	s, res, b = f.account(f.admin, staffID, "sync-role", "u1-sync-lead-"+randDigits(8), map[string]string{})
	expect(t, s, 200, b)
	if res.Member.Status != staffActivation || !exactClientRole(kcRoles(staffID), managerRole) {
		t.Fatalf("after manager sync: %+v %v", res.Member, kcRoles(staffID))
	}
	// Wrong role at the provider (director): activation refused, state unchanged.
	var ext string
	_ = db.QueryRow(ctx, `SELECT l.external_subject FROM identity_links l JOIN app_users u ON u.subject=l.subject WHERE u.staff_id=$1 AND l.issuer=$2`, staffID, issuer).Scan(&ext)
	if err = k.setExactRole(ctx, ext, directorRole); err != nil {
		t.Fatal(err)
	}
	if _, s, code = signIn(cred.Login, newPass, newPass); s == 200 || code != string(loginRoleMissing) {
		t.Fatal("wrong role activated", s, code)
	}
	if got := member(staffID); got.Status != staffActivation {
		t.Fatalf("wrong-role login changed the state: %+v", got)
	}
	if err = k.setExactRole(ctx, ext, managerRole); err != nil {
		t.Fatal(err)
	}
	// 7. Manager signs in again: ACTIVE manager; the old sales client is still out.
	lead, s, code := signIn(cred.Login, newPass, newPass)
	if s != 200 {
		t.Fatal("manager login", s, code)
	}
	m = member(staffID)
	if m.Status != staffActive || m.Role != managerRole || !m.IdentityVerified {
		t.Fatalf("after manager login: %+v", m)
	}
	if me := u1Me(t, lead, server.URL); me.Role != managerRole {
		t.Fatal("manager session role", me.Role)
	}
	if r, _ := sales.Get(server.URL + "/api/v1/projects"); r.StatusCode != 401 {
		t.Fatal("old sales session used the old permission", r.StatusCode)
	}
	out, _ := json.Marshal(map[string]any{"staffStatus": m.Status, "role": m.Role, "identityVerified": m.IdentityVerified})
	t.Logf("U1_REAL_KEYCLOAK_ROLE_LOOP=PASS %s", out)
}

// u1KeycloakSignIn drives the real login form (incl. UPDATE_PASSWORD) through the
// Baobei OIDC callback. Returns the client, the /api/v1/me status and, when the
// callback refused the login, its reason code.
func u1KeycloakSignIn(t *testing.T, server *httptest.Server, username, password, newPassword string) (*http.Client, int, string) {
	t.Helper()
	c := &http.Client{Jar: &loopbackJar{store: map[string]map[string]*http.Cookie{}}}
	resp, err := c.Get(server.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	code := ""
	for i := range 5 {
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.HasPrefix(resp.Request.URL.String(), server.URL) {
			var e struct{ Code string }
			if json.Unmarshal(page, &e) == nil {
				code = e.Code
			}
			break
		}
		m := regexp.MustCompile(`action="([^"]+)"`).FindSubmatch(page)
		if m == nil {
			return c, 0, "NO_FORM"
		}
		action := strings.ReplaceAll(string(m[1]), "&amp;", "&")
		form := url.Values{"username": {username}, "password": {password}}
		if strings.Contains(string(page), "password-new") {
			form = url.Values{"password-new": {newPassword}, "password-confirm": {newPassword}}
		} else if i > 0 {
			// The login form came back after a submit: wrong credentials.
			return c, 0, "BAD_CREDENTIALS"
		}
		if resp, err = c.PostForm(action, form); err != nil {
			t.Fatal(err)
		}
	}
	me, err := c.Get(server.URL + "/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	me.Body.Close()
	return c, me.StatusCode, code
}

func u1Me(t *testing.T, c *http.Client, origin string) User {
	t.Helper()
	r, err := c.Get(origin + "/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var u User
	_ = json.NewDecoder(r.Body).Decode(&u)
	return u
}
