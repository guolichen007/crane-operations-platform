package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// signingIdP is a minimal OIDC provider with its own signing key, standing in for
// one Keycloak realm. Two instances model the shared `crane` realm and the
// dedicated `baobei` realm: different issuers, different keys, same user UUIDs.
type signingIdP struct {
	server  *httptest.Server
	issuer  string
	mu      sync.Mutex
	subject string
	codes   map[string][2]string
}

func newSigningIdP(t *testing.T) *signingIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	p := &signingIdP{codes: map[string][2]string{}}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			respond(w, 200, map[string]any{"issuer": p.issuer, "authorization_endpoint": p.issuer + "/authorize",
				"token_endpoint": p.issuer + "/token", "jwks_uri": p.issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			respond(w, 200, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Use: "sig", Algorithm: "RS256"}}})
		case "/authorize":
			q := r.URL.Query()
			code := randomToken()
			p.codes[code] = [2]string{q.Get("nonce"), q.Get("code_challenge")}
			http.Redirect(w, r, q.Get("redirect_uri")+"?"+url.Values{"state": {q.Get("state")}, "code": {code}}.Encode(), http.StatusFound)
		case "/token":
			_ = r.ParseForm()
			item, ok := p.codes[r.Form.Get("code")]
			delete(p.codes, r.Form.Get("code"))
			digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || base64.RawURLEncoding.EncodeToString(digest[:]) != item[1] {
				respond(w, 400, map[string]string{"error": "invalid_grant"})
				return
			}
			claims, _ := json.Marshal(map[string]any{"iss": p.issuer, "aud": "baobei-web", "sub": p.subject, "nonce": item[0],
				"iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "auth_time": time.Now().Unix(),
				"email": "", "email_verified": false,
				"resource_access": map[string]any{"baobei-web": map[string]any{"roles": []string{salesRole}}}})
			obj, _ := signer.Sign(claims)
			jwt, _ := obj.CompactSerialize()
			respond(w, 200, map[string]string{"id_token": jwt, "access_token": "synthetic-only", "token_type": "Bearer"})
		default:
			w.WriteHeader(404)
		}
	}))
	p.issuer = p.server.URL
	t.Cleanup(p.server.Close)
	return p
}

// G4-S5-R1-E task A: issuer and session isolation across a realm switch.
func TestDatabaseR1EIssuerIsolation(t *testing.T) {
	db := b2Database(t)
	ctx := context.Background()
	shared, dedicated := newSigningIdP(t), newSigningIdP(t)
	// One real staff member with a preserved Keycloak UUID, the same in both realms.
	const uuid = "a1a1a1a1-0000-4000-8000-000000000001"
	if _, err := db.Exec(ctx, `INSERT INTO app_users(subject,display_name,role,team_id,staff_managed) VALUES($1,'合成迁移销售','baobei_sales',$2,true)`, uuid, syntheticTeam); err != nil {
		t.Fatal(err)
	}
	shared.subject, dedicated.subject = uuid, uuid

	serve := func(p *signingIdP) (*App, *httptest.Server) {
		t.Helper()
		app, err := newApp(ctx, db, Config{Env: "development", Issuer: p.issuer, ClientID: "baobei-web"})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(app.handler())
		t.Cleanup(server.Close)
		app.cfg.Origin = server.URL
		app.oauth.RedirectURL = server.URL + "/auth/callback"
		return app, server
	}
	sharedApp, sharedServer := serve(shared)
	dedicatedApp, dedicatedServer := serve(dedicated)
	signIn := func(server *httptest.Server) (testClient, int) {
		t.Helper()
		jar, _ := cookiejar.New(nil)
		c := testClient{&http.Client{Jar: jar}, server.URL, "", t}
		r, err := c.client.Get(server.URL + "/auth/login")
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		s, b := c.call("GET", "/api/v1/me", nil)
		if s == 200 {
			var me User
			_ = json.Unmarshal(b, &me)
			c.csrf = me.CSRF
		}
		return c, s
	}
	moveCookie := func(from testClient, to *httptest.Server) testClient {
		jar, _ := cookiejar.New(nil)
		fu, _ := url.Parse(from.origin)
		tu, _ := url.Parse(to.URL)
		jar.SetCookies(tu, from.client.Jar.Cookies(fu))
		return testClient{&http.Client{Jar: jar}, to.URL, from.csrf, t}
	}
	countSessions := func() (n int) {
		_ = db.QueryRow(ctx, `SELECT count(*) FROM app_sessions`).Scan(&n)
		return
	}

	// Phase 1: database trusts the shared realm.
	trustIssuer(t, db, shared.issuer)
	old, s := signIn(sharedServer)
	expect(t, s, 200, nil)
	if verifyIdentityEpoch(ctx, db, "production", dedicated.issuer) == nil {
		t.Fatal("a deployed API with another issuer must refuse to start")
	}
	// An API configured for the dedicated realm (e.g. started early by mistake)
	// cannot create sessions while the database trusts the shared realm.
	if _, s = signIn(dedicatedServer); s == 200 {
		t.Fatal("login under an issuer the database does not trust")
	}
	// The shared-realm cookie is not accepted by the dedicated-realm API either:
	// the session row carries the shared issuer, and only one issuer is trusted.
	s, _ = moveCookie(old, dedicatedServer).call("GET", "/api/v1/me", nil)
	expect(t, s, 200, nil) // same database, same trusted issuer → still the shared session
	_ = dedicatedApp

	// Phase 2: controlled switch to the dedicated realm revokes every session.
	staffBefore := 0
	_ = db.QueryRow(ctx, `SELECT count(*) FROM staff_sessions`).Scan(&staffBefore)
	from, epoch, revoked, err := switchIdentity(ctx, db, dedicated.issuer, "synthetic-operator", "切换到独立 Baobei Realm")
	if err != nil || from != shared.issuer || revoked < 1 || countSessions() != 0 {
		t.Fatal("switch must revoke all sessions", from, epoch, revoked, err)
	}
	var staffAfter int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM staff_sessions`).Scan(&staffAfter)
	if staffAfter != 0 {
		t.Fatal("staff sessions must go with their sessions")
	}
	// Same UUID, same old cookie: dead on both APIs.
	s, _ = old.call("GET", "/api/v1/me", nil)
	expect(t, s, 401, nil)
	s, _ = moveCookie(old, dedicatedServer).call("GET", "/api/v1/me", nil)
	expect(t, s, 401, nil)
	// The shared-realm token can no longer create a session.
	if _, s = signIn(sharedServer); s == 200 {
		t.Fatal("shared-realm login accepted after the switch")
	}
	// The dedicated realm signs the same UUID in: a new session, new epoch.
	fresh, s := signIn(dedicatedServer)
	expect(t, s, 200, nil)
	var issuer string
	var sessionEpoch int
	_ = db.QueryRow(ctx, `SELECT issuer,identity_epoch FROM app_sessions`).Scan(&issuer, &sessionEpoch)
	if issuer != dedicated.issuer || sessionEpoch != epoch {
		t.Fatal("session not stamped with the new issuer/epoch", issuer, sessionEpoch)
	}

	// Fail closed even if a stale row were left behind: re-insert an old-epoch row.
	if _, err = db.Exec(ctx, `INSERT INTO app_sessions(token_hash,subject,csrf,expires_at,issuer,identity_epoch)
 VALUES($1,$2,'x',now()+interval '1 hour',$3,$4)`, hash("forged-left-over"), uuid, shared.issuer, epoch-1); err != nil {
		t.Fatal(err)
	}
	ghost := testClient{&http.Client{}, dedicatedServer.URL, "", t}
	req, _ := http.NewRequest("GET", dedicatedServer.URL+"/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: dedicatedApp.cookieName(), Value: "forged-left-over"})
	resp, err := ghost.client.Do(req)
	if err != nil || resp.StatusCode != 401 {
		t.Fatal("left-over session of an older epoch accepted")
	}
	resp.Body.Close()
	// Same issuer but an old epoch is also dead.
	if _, err = db.Exec(ctx, `UPDATE app_sessions SET issuer=$1 WHERE token_hash=$2`, dedicated.issuer, hash("forged-left-over")); err != nil {
		t.Fatal(err)
	}
	resp, _ = ghost.client.Do(req)
	if resp.StatusCode != 401 {
		t.Fatal("left-over session with the right issuer but an older epoch accepted")
	}
	resp.Body.Close()

	// Phase 3: switch back to the shared realm also revokes the dedicated sessions.
	if _, _, revoked, err = switchIdentity(ctx, db, shared.issuer, "synthetic-operator", "回切共享 Realm"); err != nil || revoked < 1 {
		t.Fatal(revoked, err)
	}
	s, _ = fresh.call("GET", "/api/v1/me", nil)
	expect(t, s, 401, nil)
	back, s := signIn(sharedServer)
	expect(t, s, 200, nil)
	s, _ = back.call("GET", "/api/v1/me", nil)
	expect(t, s, 200, nil)
	_ = sharedApp
	// Every switch is audited.
	var switches int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='IDENTITY_SWITCH'`).Scan(&switches)
	if switches < 3 {
		t.Fatal("identity switches not audited", switches)
	}
	// Invalid switch requests change nothing.
	if _, _, _, err = switchIdentity(ctx, db, "", "synthetic-operator", "x"); err == nil {
		t.Fatal("empty issuer accepted")
	}
	if _, _, _, err = switchIdentity(ctx, db, dedicated.issuer, "bad operator!", "x"); err == nil {
		t.Fatal("invalid operator accepted")
	}
	s, _ = back.call("GET", "/api/v1/me", nil)
	expect(t, s, 200, nil)
}

// The production identity-switch command never takes an issuer from the command
// line: only an approved realm name, resolved through the environment profile.
func TestR1EIdentitySwitchCommandRealmOnly(t *testing.T) {
	var out strings.Builder
	for _, realm := range []string{"", "master", "https://evil.example/realms/baobei", "baobei-preview"} {
		if err := identitySwitchCommand(context.Background(), nil, "production", []string{"--realm", realm, "--operator", "op-1", "--reason", "x"}, &out); err == nil {
			t.Fatalf("realm %q accepted", realm)
		}
	}
	if _, err := selectRealm("production", "baobei"); err != nil {
		t.Fatal(err)
	}
	if r, _ := selectRealm("production", "crane"); r.AccountService {
		t.Fatal("the shared realm must never get the account service")
	}
}
