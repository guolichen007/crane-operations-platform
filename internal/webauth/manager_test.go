package webauth

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

const origin = "https://app.example.test"

func fixture(t *testing.T) (*Manager, *testsupport.OIDCProvider, http.Handler, *sql.DB) {
	t.Helper()
	db := testsupport.Database(t)
	provider := testsupport.NewOIDC(t)
	manager, err := New(context.Background(), db, Config{PublicURL: origin, Issuer: provider.Server.URL, ClientID: testsupport.OIDCClient, ClientSecret: []byte(testsupport.OIDCSecret), ServerSecret: []byte(strings.Repeat("anonymous-server-key-", 3)), AbsoluteTTL: time.Hour, IdleTTL: 5 * time.Minute, HTTPClient: provider.Server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "disabled"} {
		if err = identity.Bind(context.Background(), db, "user-"+id, provider.Server.URL, "subject-"+id, "Anonymous", id != "disabled", auditlog.Event{Actor: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	manager.Register(mux)
	return manager, provider, mux, db
}

func TestOIDCStateNoncePKCEIssuerAudienceAndIdentityCounterexamples(t *testing.T) {
	cases := []struct {
		name     string
		scenario testsupport.OIDCScenario
		mutate   func(*sql.DB, *http.Request)
	}{
		{"state_mismatch", testsupport.OIDCScenario{Subject: "subject-a"}, func(_ *sql.DB, r *http.Request) {
			q := r.URL.Query()
			q.Set("state", "different-state")
			r.URL.RawQuery = q.Encode()
		}},
		{"nonce_mismatch", testsupport.OIDCScenario{Subject: "subject-a", Nonce: "wrong-nonce"}, nil},
		{"pkce_failure", testsupport.OIDCScenario{Subject: "subject-a", RejectPKCE: true}, nil},
		{"issuer_mismatch", testsupport.OIDCScenario{Subject: "subject-a", Issuer: "https://wrong.example.test"}, nil},
		{"audience_mismatch", testsupport.OIDCScenario{Subject: "subject-a", Audience: "wrong-client"}, nil},
		{"authorized_party_mismatch", testsupport.OIDCScenario{Subject: "subject-a", AZP: "wrong-client"}, nil},
		{"unknown_sub", testsupport.OIDCScenario{Subject: "unknown"}, nil},
		{"disabled_user", testsupport.OIDCScenario{Subject: "subject-disabled"}, nil},
		{"expired_transaction", testsupport.OIDCScenario{Subject: "subject-a"}, func(db *sql.DB, _ *http.Request) {
			_, _ = db.Exec(`UPDATE oidc_login_transactions SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour'`)
		}},
		{"callback_uri_mismatch", testsupport.OIDCScenario{Subject: "subject-a"}, func(db *sql.DB, _ *http.Request) {
			_, _ = db.Exec(`UPDATE oidc_login_transactions SET redirect_uri='https://attacker.example.test/auth/callback'`)
		}},
		{"browser_binding_mismatch", testsupport.OIDCScenario{Subject: "subject-a"}, func(_ *sql.DB, r *http.Request) { r.Header.Set("Cookie", loginCookie+"=unrelated-browser") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, provider, handler, db := fixture(t)
			provider.Set(test.scenario)
			callback, cookies := testsupport.BeginLogin(t, handler, provider, origin)
			r := httptest.NewRequest("GET", callback, nil)
			for _, c := range cookies {
				r.AddCookie(c)
			}
			if test.mutate != nil {
				test.mutate(db, r)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatal("invalid flow accepted", w.Code, w.Body.String())
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM web_sessions`).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed login created session", count, err)
			}
			if err := db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='login_failure'`).Scan(&count); err != nil || count != 1 {
				t.Fatal("failed login missing audit", count, err)
			}
		})
	}
}

func TestServerSessionCookieHashExpiryFixationAndLogout(t *testing.T) {
	m, provider, handler, db := fixture(t)
	cookie, info := testsupport.Login(t, handler, provider, origin)
	if cookie.Name != CookieName || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" {
		t.Fatal("invalid production Cookie attributes")
	}
	if len(info.Projects) != 0 {
		t.Fatal("known unassigned identity received a project")
	}
	var sessionHex string
	if err := db.QueryRow(`SELECT encode(session_hash,'hex') FROM web_sessions`).Scan(&sessionHex); err != nil {
		t.Fatal(err)
	}
	if sessionHex == hex.EncodeToString([]byte(cookie.Value)) {
		t.Fatal("raw session stored")
	}
	for _, value := range []string{"random", strings.Repeat("a", 43), cookie.Value[:42] + "!"} {
		r := httptest.NewRequest("GET", origin+"/auth/session", nil)
		r.AddCookie(&http.Cookie{Name: CookieName, Value: value})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("random/tampered Cookie accepted", w.Code)
		}
	}
	// A new login rotates the opaque ID and invalidates the previously presented ID.
	callback, cookies := testsupport.BeginLogin(t, handler, provider, origin)
	r := httptest.NewRequest("GET", callback, nil)
	r.AddCookie(cookie)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	var rotated *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName {
			rotated = c
		}
	}
	if rotated == nil || rotated.Value == cookie.Value {
		t.Fatal("session fixation")
	}
	r = httptest.NewRequest("GET", origin+"/auth/session", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("old session still active")
	}
	for _, expiry := range []string{"absolute", "idle"} {
		t.Run(expiry, func(t *testing.T) {
			fresh, _ := testsupport.Login(t, handler, provider, origin)
			if expiry == "absolute" {
				m.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
			} else {
				m.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
			}
			r := httptest.NewRequest("GET", origin+"/auth/session", nil)
			r.AddCookie(fresh)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			m.now = time.Now
			if w.Code != 401 {
				t.Fatal("expired session accepted", expiry, w.Code)
			}
		})
	}
	logoutCookie, logoutInfo := testsupport.Login(t, handler, provider, origin)
	r = httptest.NewRequest("POST", origin+"/auth/logout", nil)
	r.AddCookie(logoutCookie)
	r.Header.Set("Origin", origin)
	r.Header.Set("X-CSRF-Token", logoutInfo.CsrfToken)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("logout failed", w.Code)
	}
	r = httptest.NewRequest("GET", origin+"/auth/session", nil)
	r.AddCookie(logoutCookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("logout did not revoke session")
	}
}

func TestSessionBoundCSRFAndOriginDefense(t *testing.T) {
	_, provider, handler, _ := fixture(t)
	cookie, session := testsupport.Login(t, handler, provider, origin)
	_, otherSession := testsupport.Login(t, handler, provider, origin)
	for _, test := range []struct{ name, token, origin string }{
		{"missing", "", origin}, {"wrong", strings.Repeat("a", 43), origin}, {"other_session", otherSession.CsrfToken, origin},
		{"cross_origin", session.CsrfToken, "https://evil.example.test"}, {"null_origin", session.CsrfToken, "null"}, {"missing_origin", session.CsrfToken, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", origin+"/auth/logout", nil)
			r.AddCookie(cookie)
			r.Header.Set("X-CSRF-Token", test.token)
			if test.origin != "" {
				r.Header.Set("Origin", test.origin)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatal("CSRF accepted", w.Code)
			}
		})
	}
	r := httptest.NewRequest("POST", origin+"/auth/logout", nil)
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", session.CsrfToken)
	r.Header.Set("Referer", origin+"/page")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("valid same-origin Referer rejected", w.Code)
	}
}

func TestTrustedProxyAndRequestID(t *testing.T) {
	m := &Manager{config: Config{PublicURL: origin}, proxies: []netip.Prefix{netip.MustParsePrefix("10.20.0.0/24")}}
	for _, test := range []struct{ peer, xff, want string }{
		{"198.51.100.1:1234", "203.0.113.2", "198.51.100.1"},
		{"10.20.0.2:1234", "198.51.100.3, 10.20.0.3", "198.51.100.3"},
		{"10.20.0.2:1234", "invalid", "10.20.0.2"},
	} {
		r := httptest.NewRequest("GET", origin, nil)
		r.RemoteAddr = test.peer
		r.Header.Set("X-Forwarded-For", test.xff)
		r.Header.Set("X-Request-ID", "untrusted-client-request")
		meta := m.Metadata(r)
		if meta.SourceIP != test.want || meta.RequestID == "untrusted-client-request" || meta.RequestID == "" {
			t.Fatal("untrusted request metadata", meta)
		}
	}
	for _, value := range []string{"null", "https://evil.example.test", origin + "/path"} {
		r := httptest.NewRequest("POST", origin, nil)
		r.Header.Set("Origin", value)
		r.Header.Set("Referer", origin)
		if m.SameOrigin(r, true) {
			t.Fatal("invalid Origin rescued by Referer")
		}
	}
	if _, err := url.Parse(origin); err != nil {
		t.Fatal(err)
	}
	var captured string
	h := m.Transport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = m.Metadata(r).RequestID
		_ = json.NewEncoder(w).Encode(map[string]string{"request": captured})
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", origin, nil))
	if captured != w.Header().Get("X-Request-ID") {
		t.Fatal("request trace differs from audit metadata")
	}
}
