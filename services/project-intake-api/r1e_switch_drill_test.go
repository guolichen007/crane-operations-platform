package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// TestR1ERealSwitchDrill: forward switch to the dedicated realm and switch back,
// with real Keycloak 26.8.1 tokens through the real Baobei login path.
//
// Prepared by keycloak-realm-probe.py + keycloak-import-drill.py in the same
// Keycloak (realms crane and baobei, client baobei-web in both). Skipped unless
// BAOBEI_KEYCLOAK_URL is set.
func TestR1ERealSwitchDrill(t *testing.T) {
	base := os.Getenv("BAOBEI_KEYCLOAK_URL")
	secret := os.Getenv("BAOBEI_KEYCLOAK_SERVICE_SECRET")
	if base == "" || secret == "" {
		t.Skip("NOT_RUN: real Keycloak fixture required")
	}
	ctx := context.Background()
	db := b2Database(t)
	sharedIssuer, dedicatedIssuer := base+"/realms/crane", base+"/realms/baobei"
	// Client secrets of the two baobei-web clients (set by the fixture).
	sharedSecret, dedicatedSecret := os.Getenv("BAOBEI_KC_CRANE_WEB_SECRET"), os.Getenv("BAOBEI_KC_BAOBEI_WEB_SECRET")
	legacyUser, legacyPass, legacyUUID := os.Getenv("BAOBEI_KC_LEGACY_USER"), os.Getenv("BAOBEI_KC_LEGACY_PASS"), os.Getenv("BAOBEI_KC_LEGACY_UUID")
	if sharedSecret == "" || dedicatedSecret == "" || legacyUser == "" {
		t.Fatal("fixture variables missing")
	}
	// The legacy member: business subject = shared-realm UUID, with a project.
	var staffID string
	if err := db.QueryRow(ctx, `INSERT INTO app_users(subject,display_name,role,team_id,work_phone,staff_managed)
 VALUES($1,'合成迁移演练销售','baobei_sales',$2,$3,true) RETURNING staff_id::text`, legacyUUID, syntheticTeam, "+86"+os.Getenv("BAOBEI_KC_NEW_PHONE")).Scan(&staffID); err != nil {
		t.Fatal(err)
	}
	var projectID string
	if err := db.QueryRow(ctx, `INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,document)
 VALUES('合成演练项目','synthetic drill',$1,$1,$1,'{}') RETURNING id::text`, legacyUUID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	evidence := func() string {
		var s string
		_ = db.QueryRow(ctx, `SELECT owner_subject||created_by_subject||document_hash||version FROM intake_projects WHERE id=$1`, projectID).Scan(&s)
		return s
	}
	frozen := evidence()

	start := func(issuer, clientSecret string, dedicated bool) *httptest.Server {
		t.Helper()
		var app *App
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { app.handler().ServeHTTP(w, r) }))
		t.Cleanup(server.Close)
		a, err := newApp(ctx, db, Config{Env: "development", Issuer: issuer, ClientID: "baobei-web", Secret: clientSecret, Origin: server.URL})
		if err != nil {
			t.Fatal(err)
		}
		a.oauth.RedirectURL = server.URL + "/auth/callback"
		if dedicated {
			k, err := newKeycloakAdmin(issuer, "baobei-web", "baobei-account-service", secret, nil)
			if err != nil {
				t.Fatal(err)
			}
			a.accounts, a.linkedOnly = k, true
		}
		app = a
		return server
	}
	shared := start(sharedIssuer, sharedSecret, false)
	dedicated := start(dedicatedIssuer, dedicatedSecret, true)

	// signIn drives the real Keycloak login form, including UPDATE_PASSWORD.
	signIn := func(server *httptest.Server, username, password, newPassword string) (*http.Client, int) {
		t.Helper()
		c := &http.Client{Jar: &loopbackJar{store: map[string]map[string]*http.Cookie{}}}
		resp, err := c.Get(server.URL + "/auth/login")
		if err != nil {
			t.Fatal(err)
		}
		for range 4 {
			page, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if strings.HasPrefix(resp.Request.URL.String(), server.URL) {
				break
			}
			m := regexp.MustCompile(`action="([^"]+)"`).FindSubmatch(page)
			if m == nil {
				t.Logf("no form at %s (status %d): %s", resp.Request.URL, resp.StatusCode, regexp.MustCompile(`kc-error-message[^>]*>[\s\S]{0,200}`).FindString(string(page)))
				return c, 0
			}
			action := strings.ReplaceAll(string(m[1]), "&amp;", "&")
			form := url.Values{"username": {username}, "password": {password}}
			if strings.Contains(string(page), "password-new") {
				form = url.Values{"password-new": {newPassword}, "password-confirm": {newPassword}}
			}
			if resp, err = c.PostForm(action, form); err != nil {
				t.Fatal(err)
			}
		}
		me, err := c.Get(server.URL + "/api/v1/me")
		if err != nil {
			t.Fatal(err)
		}
		defer me.Body.Close()
		return c, me.StatusCode
	}

	// 1. Shared realm trusted: the legacy member signs in with the old account.
	trustIssuer(t, db, sharedIssuer)
	old, s := signIn(shared, legacyUser, legacyPass, "")
	if s != 200 {
		t.Fatal("shared-realm login before the switch", s)
	}
	// 2. Admin creates the member's login in the dedicated realm ahead of the switch
	//    (the account service works only once the dedicated issuer is trusted, so the
	//    switch comes first; before it, the dedicated API refuses logins).
	if _, s = signIn(dedicated, "nobody", "x", ""); s == 200 {
		t.Fatal("dedicated login while the shared realm is trusted")
	}
	_, _, revoked, err := switchIdentity(ctx, db, dedicatedIssuer, "drill-operator", "演练：切换到独立 Baobei Realm")
	if err != nil || revoked < 1 {
		t.Fatal("forward switch", revoked, err)
	}
	if r, _ := old.Get(shared.URL + "/api/v1/me"); r.StatusCode != 401 {
		t.Fatal("old session survived the switch")
	}
	// Old account and old token path are refused now.
	if _, s = signIn(shared, legacyUser, legacyPass, ""); s == 200 {
		t.Fatal("shared-realm login after the switch")
	}
	// 3. Admin (synthetic dev admin session on the dedicated API) creates the login.
	f := &accountFixture{t: t}
	adminApp := &App{db: db, cfg: Config{Env: "development", Dev: true, Issuer: dedicatedIssuer}}
	k, _ := newKeycloakAdmin(dedicatedIssuer, "baobei-web", "baobei-account-service", secret, nil)
	adminApp.accounts, adminApp.linkedOnly = k, true
	adminServer := httptest.NewServer(adminApp.handler())
	defer adminServer.Close()
	adminApp.cfg.Origin = adminServer.URL
	f.server, f.app = adminServer, adminApp
	f.admin = login(t, adminServer, "synthetic-admin")
	s, res, b := f.account(f.admin, staffID, "create", "drill-create-"+randDigits(10), map[string]string{})
	expect(t, s, 200, b)
	newPass := "Drill-" + randomToken()[:10]
	member, s := signIn(dedicated, res.Credential.Login, res.Credential.TemporaryPassword, newPass)
	if s != 200 {
		t.Fatal("new realm: phone + temporary password + forced change", s)
	}
	mr, _ := member.Get(dedicated.URL + "/api/v1/me")
	body, _ := io.ReadAll(mr.Body)
	mr.Body.Close()
	if !strings.Contains(string(body), `"subject":"`+legacyUUID+`"`) {
		t.Fatal("new login must map to the unchanged business subject", string(body))
	}
	if pr, _ := member.Get(dedicated.URL + "/api/v1/projects/" + projectID); pr.StatusCode != 200 {
		t.Fatal("history not reachable after migration")
	}
	if evidence() != frozen {
		t.Fatal("immutable evidence changed by the switch")
	}
	// 4. Switch back: dedicated sessions end; the shared realm works again unchanged.
	if _, _, revoked, err = switchIdentity(ctx, db, sharedIssuer, "drill-operator", "演练：回切共享 Realm"); err != nil || revoked < 1 {
		t.Fatal("switch back", revoked, err)
	}
	if r, _ := member.Get(dedicated.URL + "/api/v1/me"); r.StatusCode != 401 {
		t.Fatal("dedicated session survived the switch back")
	}
	if _, s = signIn(shared, legacyUser, legacyPass, ""); s != 200 {
		t.Fatal("shared realm after the switch back", s)
	}
	if _, s = signIn(dedicated, res.Credential.Login, newPass, ""); s == 200 {
		t.Fatal("dedicated realm accepted after the switch back")
	}
	if evidence() != frozen {
		t.Fatal("immutable evidence changed by the switch back")
	}
	var links int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM identity_links WHERE subject=$1`, legacyUUID).Scan(&links)
	out, _ := json.Marshal(map[string]any{"links": links, "subject": legacyUUID})
	fmt.Println("REAL_SWITCH_DRILL=PASS", string(out))
}

// loopbackJar keeps cookies per host:port like a browser treats loopback as a
// secure context (Keycloak marks its login cookies Secure; the standard jar
// drops them over plain http). Test only.
type loopbackJar struct {
	mu    sync.Mutex
	store map[string]map[string]*http.Cookie
}

func (j *loopbackJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	m := j.store[u.Host]
	if m == nil {
		m = map[string]*http.Cookie{}
		j.store[u.Host] = m
	}
	for _, c := range cookies {
		if c.MaxAge < 0 {
			delete(m, c.Name)
		} else {
			m[c.Name] = c
		}
	}
}

func (j *loopbackJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := []*http.Cookie{}
	for _, c := range j.store[u.Host] {
		out = append(out, &http.Cookie{Name: c.Name, Value: c.Value})
	}
	return out
}
