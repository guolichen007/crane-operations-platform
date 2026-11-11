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

// G4-S4 A1: logout really ends the Baobei session, the old cookie cannot call the
// API, repeated logout is idempotent, "switch account" forces the Keycloak login
// form, and global SSO logout uses RP-initiated logout (client_id +
// post_logout_redirect_uri, never the deprecated redirect_uri) only on request.
func TestDatabaseG4S4SessionLogoutSwitch(t *testing.T) {
	db := b2Database(t)
	ctx := context.Background()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "g4s4"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var issuer string
	subject := "synthetic-a"
	codes := map[string][2]string{} // code -> nonce, challenge
	authorizeQueries := []url.Values{}
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			respond(w, 200, map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
				"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
				"end_session_endpoint": issuer + "/logout", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			respond(w, 200, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "g4s4", Use: "sig", Algorithm: "RS256"}}})
		case "/authorize":
			q := r.URL.Query()
			authorizeQueries = append(authorizeQueries, q)
			code := randomToken()
			codes[code] = [2]string{q.Get("nonce"), q.Get("code_challenge")}
			http.Redirect(w, r, q.Get("redirect_uri")+"?"+url.Values{"state": {q.Get("state")}, "code": {code}}.Encode(), http.StatusFound)
		case "/token":
			_ = r.ParseForm()
			item, ok := codes[r.Form.Get("code")]
			delete(codes, r.Form.Get("code"))
			digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || base64.RawURLEncoding.EncodeToString(digest[:]) != item[1] {
				respond(w, 400, map[string]string{"error": "invalid_grant"})
				return
			}
			role := salesRole
			claims, _ := json.Marshal(map[string]any{"iss": issuer, "aud": "baobei-web", "sub": subject, "nonce": item[0],
				"iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "auth_time": time.Now().Unix(),
				"email": subject + "@example.test", "email_verified": true,
				"resource_access": map[string]any{"baobei-web": map[string]any{"roles": []string{role}}}})
			obj, _ := signer.Sign(claims)
			jwt, _ := obj.CompactSerialize()
			respond(w, 200, map[string]string{"id_token": jwt, "access_token": "synthetic-only", "token_type": "Bearer"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer idp.Close()
	issuer = idp.URL
	if _, err = db.Exec(ctx, `UPDATE app_users SET active=true,staff_status='ACTIVE' WHERE subject IN ('synthetic-a','synthetic-b')`); err != nil {
		t.Fatal(err)
	}
	app, err := newApp(ctx, db, Config{Env: "development", Issuer: issuer, ClientID: "baobei-web"})
	if err != nil {
		t.Fatal(err)
	}
	if app.endSession != issuer+"/logout" {
		t.Fatal("end_session_endpoint not discovered: " + app.endSession)
	}
	server := httptest.NewServer(app.handler())
	defer server.Close()
	app.cfg.Origin = server.URL
	app.oauth.RedirectURL = server.URL + "/auth/callback"

	newBrowser := func() testClient {
		jar, _ := cookiejar.New(nil)
		return testClient{&http.Client{Jar: jar}, server.URL, "", t}
	}
	signIn := func(c *testClient, who, query string) {
		t.Helper()
		mu.Lock()
		subject = who
		mu.Unlock()
		r, err := c.client.Get(server.URL + "/auth/login" + query) // follows IdP + callback redirects
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		s, b := c.call("GET", "/api/v1/me", nil)
		expect(t, s, 200, b)
		var me User
		if err := json.Unmarshal(b, &me); err != nil || me.Subject != who {
			t.Fatalf("signed in as %s, want %s", me.Subject, who)
		}
		c.csrf = me.CSRF
	}
	cookieOf := func(c testClient) *http.Cookie {
		u, _ := url.Parse(server.URL)
		for _, ck := range c.client.Jar.Cookies(u) {
			if ck.Name == app.cookieName() {
				return ck
			}
		}
		return nil
	}

	t.Run("A_logout_then_B_old_cookie_dead", func(t *testing.T) {
		c := newBrowser()
		signIn(&c, "synthetic-a", "")
		old := cookieOf(c)
		s, b := c.call("POST", "/auth/logout", map[string]any{})
		expect(t, s, 200, b)
		if strings.Contains(string(b), "endSessionUrl") {
			t.Fatal("local logout must not end the shared SSO session")
		}
		s, b = c.call("GET", "/api/v1/me", nil)
		expect(t, s, 401, b)
		// Replaying A's old cookie in another client cannot call business APIs.
		replay := newBrowser()
		u, _ := url.Parse(server.URL)
		replay.client.Jar.SetCookies(u, []*http.Cookie{{Name: old.Name, Value: old.Value, Path: "/"}})
		s, b = replay.call("GET", "/api/v1/projects", nil)
		expect(t, s, 401, b)
		// Repeated logout is idempotent (already signed out).
		s, b = c.call("POST", "/auth/logout", map[string]any{})
		expect(t, s, 200, b)
		if !strings.Contains(string(b), "alreadySignedOut") {
			t.Fatal(string(b))
		}
		signIn(&c, "synthetic-b", "?account=switch")
	})

	t.Run("every_signin_forces_login_form_reauth_keeps_max_age", func(t *testing.T) {
		mu.Lock()
		authorizeQueries = nil
		mu.Unlock()
		c := newBrowser()
		signIn(&c, "synthetic-a", "")
		signIn(&c, "synthetic-b", "?account=switch")
		signIn(&c, "synthetic-b", "?reauth=1")
		mu.Lock()
		defer mu.Unlock()
		if len(authorizeQueries) != 3 {
			t.Fatal(len(authorizeQueries))
		}
		// G4-S5: every sign-in forces the password form; only reauth bounds age.
		for i := 0; i < 2; i++ {
			if authorizeQueries[i].Get("prompt") != "login" || authorizeQueries[i].Has("max_age") {
				t.Fatal("sign-in must force the login form", i, authorizeQueries[i])
			}
		}
		if authorizeQueries[2].Get("prompt") != "login" || authorizeQueries[2].Get("max_age") != "0" {
			t.Fatal("reauth parameters", authorizeQueries[2])
		}
	})

	t.Run("sso_logout_url_and_csrf", func(t *testing.T) {
		c := newBrowser()
		signIn(&c, "synthetic-a", "")
		csrf := c.csrf
		c.csrf = "forged"
		s, b := c.call("POST", "/auth/logout?scope=sso", map[string]any{})
		expect(t, s, 403, b)
		c.csrf = csrf
		s, b = c.call("POST", "/auth/logout?scope=everything", map[string]any{})
		expect(t, s, 400, b)
		s, b = c.call("POST", "/auth/logout?scope=sso", map[string]any{})
		expect(t, s, 200, b)
		var out struct {
			EndSessionURL string `json:"endSessionUrl"`
		}
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(out.EndSessionURL)
		if err != nil || u.Scheme+"://"+u.Host+u.Path != issuer+"/logout" {
			t.Fatal(out.EndSessionURL)
		}
		q := u.Query()
		if q.Get("client_id") != "baobei-web" || q.Get("post_logout_redirect_uri") != server.URL+"/login?signedOut=sso" ||
			q.Has("redirect_uri") || q.Has("id_token_hint") {
			t.Fatal("RP-initiated logout parameters", q)
		}
		s, b = c.call("GET", "/api/v1/me", nil)
		expect(t, s, 401, b)
	})

	t.Run("other_sessions_untouched", func(t *testing.T) {
		a1, a2 := newBrowser(), newBrowser()
		signIn(&a1, "synthetic-a", "")
		signIn(&a2, "synthetic-a", "")
		s, b := a1.call("POST", "/auth/logout", map[string]any{})
		expect(t, s, 200, b)
		// Logging out one browser ends only that Baobei session.
		s, b = a2.call("GET", "/api/v1/me", nil)
		expect(t, s, 200, b)
	})

	t.Run("logout_requires_post", func(t *testing.T) {
		c := newBrowser()
		s, b := c.call("GET", "/auth/logout", nil)
		expect(t, s, 405, b)
	})
}
