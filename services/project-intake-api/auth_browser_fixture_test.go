package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAuthBrowserFixture(t *testing.T) {
	dist := os.Getenv("BAOBEI_AUTH_FIXTURE_DIST")
	if dist == "" {
		t.Skip("browser fixture only")
	}
	db := b2Database(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `UPDATE app_users SET staff_managed=true WHERE subject='synthetic-a';
 UPDATE app_users SET active=false WHERE subject='synthetic-b'`); err != nil {
		t.Fatal(err)
	}
	staffSubjects := map[string]string{
		"role-desktop": "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		"role-mobile":  "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
	}
	for name, subject := range staffSubjects {
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		in := staffProfileInput{Name: "合成主管授权 " + name, Email: name + "@example.test", Phone: map[string]string{"role-desktop": "13900000101", "role-mobile": "13900000102"}[name], Role: salesRole, TeamID: syntheticTeam, Reason: "合成角色授权验收"}
		result, err := inviteStaff(ctx, tx, User{Subject: "synthetic-admin", Role: adminRole}, nil, in)
		if err != nil {
			tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		m := result.(staffMember)
		receipt := provisionReceipt{StaffID: m.ID, Version: m.Version, Operation: "approve",
			Subject: subject, Email: m.Email, IdentityVerified: true, UniqueEmail: true,
			ApprovalReference: "SYNTHETIC-BROWSER", Reason: "合成销售已核对"}
		if outcome, err := applyProvisionReceipt(ctx, db, mustJSON(receipt), "synthetic-operator", false); err != nil || outcome != staffActivation {
			t.Fatal(outcome, err)
		}
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "browser-test"))
	if err != nil {
		t.Fatal(err)
	}
	closedPool, err := pgxpool.NewWithConfig(ctx, db.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	closedPool.Close()

	var mode atomic.Value
	mode.Store("success")
	var dbOutage atomic.Bool
	var exchanges, ssoLogouts atomic.Int64
	var lastPrompt atomic.Value
	lastPrompt.Store("")
	var mu sync.Mutex
	type attempt struct{ mode, nonce, challenge string }
	attempts := make(map[string]attempt)
	var appURL, issuer string
	authorizePage := template.Must(template.New("authorize").Parse(`<!doctype html><title>隔离企业认证</title>
<h1>隔离企业认证</h1><a href="{{.Allow}}">继续企业登录</a> <a href="{{.Cancel}}">取消企业登录</a>`))
	idp := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			respond(w, 200, map[string]any{
				"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
				"jwks_uri": issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"},
				"end_session_endpoint": issuer + "/logout",
			})
		case "/logout":
			// Keycloak RP-initiated logout: confirm, then return to post_logout_redirect_uri.
			q := r.URL.Query()
			if q.Get("client_id") != "baobei-web" || q.Has("redirect_uri") ||
				q.Get("post_logout_redirect_uri") != appURL+"/login?signedOut=sso" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			ssoLogouts.Add(1)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><title>企业退出</title><h1>确认退出企业账号</h1><a href="`+
				template.HTMLEscapeString(q.Get("post_logout_redirect_uri"))+`">确认退出</a>`)
		case "/keys":
			respond(w, 200, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
				{Key: &key.PublicKey, KeyID: "browser-test", Use: "sig", Algorithm: "RS256"},
			}})
		case "/authorize":
			q := r.URL.Query()
			state, nonce, challenge := q.Get("state"), q.Get("nonce"), q.Get("code_challenge")
			if q.Get("redirect_uri") != appURL+"/auth/callback" || q.Get("client_id") != "baobei-web" ||
				state == "" || nonce == "" || challenge == "" || q.Get("code_challenge_method") != "S256" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			code := randomToken()
			selected := mode.Load().(string)
			lastPrompt.Store(q.Get("prompt") + "|" + q.Get("max_age"))
			mu.Lock()
			attempts[code] = attempt{selected, nonce, challenge}
			mu.Unlock()
			callback := appURL + "/auth/callback?"
			allow := url.Values{"state": {state}, "code": {code}}
			if selected == "oauth-error" {
				allow = url.Values{"state": {state}, "error": {"invalid_request"},
					"error_description": {"sensitive-fixture-description"}, "next": {"https://example.test"}}
			}
			cancel := url.Values{"state": {state}, "error": {"access_denied"}, "error_description": {"sensitive-fixture-description"}}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Referrer-Policy", "no-referrer")
			_ = authorizePage.Execute(w, map[string]string{"Allow": callback + allow.Encode(), "Cancel": callback + cancel.Encode()})
		case "/token":
			_ = r.ParseForm()
			mu.Lock()
			item, ok := attempts[r.Form.Get("code")]
			delete(attempts, r.Form.Get("code"))
			mu.Unlock()
			exchanges.Add(1)
			digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || item.challenge != base64.RawURLEncoding.EncodeToString(digest[:]) || item.mode == "exchange" {
				respond(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
				return
			}
			if item.mode == "unavailable" {
				respond(w, http.StatusServiceUnavailable, map[string]string{"error": "temporarily_unavailable"})
				return
			}
			if item.mode == "network" {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
			subject, audience, email := "synthetic-a", "baobei-web", "synthetic-a@example.test"
			roles := map[string]any{"baobei-web": map[string]any{"roles": []string{salesRole}}}
			switch item.mode {
			case "nonce":
				item.nonce = "incorrect-fixture-nonce"
			case "unopened":
				subject = "synthetic-not-provisioned"
			case "role":
				roles = map[string]any{"other-client": map[string]any{"roles": []string{salesRole}}}
			case "disabled":
				subject = "synthetic-b"
			case "email":
				email = "different@example.test"
			case "token":
				audience = "other-client"
			case "sales-b":
				subject, email = "synthetic-c", "synthetic-c@example.test"
			case "staff-admin":
				subject = "synthetic-admin"
				roles = map[string]any{"baobei-web": map[string]any{"roles": []string{adminRole}}}
			case "role-desktop", "role-mobile":
				subject, email = staffSubjects[item.mode], item.mode+"@example.test"
			case "role-desktop-manager", "role-mobile-manager":
				salesMode := strings.TrimSuffix(item.mode, "-manager")
				subject, email = staffSubjects[salesMode], salesMode+"@example.test"
				roles = map[string]any{"baobei-web": map[string]any{"roles": []string{managerRole}}}
			}
			claims, _ := json.Marshal(map[string]any{
				"iss": issuer, "aud": audience, "sub": subject, "nonce": item.nonce,
				"iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(),
				"auth_time": time.Now().Unix(),
				"email":     email, "email_verified": item.mode != "unverified", "resource_access": roles,
				"realm_access": map[string]any{"roles": []string{salesRole}},
			})
			signed, err := signer.Sign(claims)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			jwt, _ := signed.CompactSerialize()
			respond(w, 200, map[string]string{"id_token": jwt, "access_token": "fixture-access", "token_type": "Bearer"})
		default:
			http.NotFound(w, r)
		}
	}))
	issuer = "https://" + strings.Replace(idp.Listener.Addr().String(), "127.0.0.1", "localhost", 1)
	idp.Config.ErrorLog = log.New(io.Discard, "", 0)
	idp.StartTLS()
	defer idp.Close()
	client := idp.Client()
	client.Transport.(*http.Transport).TLSClientConfig.ServerName = "127.0.0.1"

	app, err := newApp(oidc.ClientContext(ctx, client), db, Config{Env: "production", Issuer: issuer, ClientID: "baobei-web"})
	if err != nil {
		t.Fatal(err)
	}
	app.staffDB = db
	offline := *app
	offline.db = closedPool
	onlineHandler, offlineHandler := app.handler(), offline.handler()
	files := http.FileServer(http.Dir(dist))
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture/role-receipt":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			subject := staffSubjects[r.URL.Query().Get("member")]
			operation := r.URL.Query().Get("operation")
			if subject == "" || operation != "fail" && operation != "approve" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			receipt := provisionReceipt{Operation: operation, ApprovalReference: "SYNTHETIC-ROLE",
				Reason: "合成身份同步失败"}
			err := db.QueryRow(r.Context(), `SELECT staff_id::text,staff_version,work_email FROM app_users
 WHERE subject=$1 AND role='baobei_manager' AND staff_status='PENDING_PROVISION'`, subject).
				Scan(&receipt.StaffID, &receipt.Version, &receipt.Email)
			if err == nil {
				if operation == "approve" {
					receipt.Subject, receipt.IdentityVerified, receipt.UniqueEmail = subject, true, true
					receipt.Reason, receipt.ClientRoles = "合成运维核对", []string{managerRole}
				}
				_, err = applyProvisionReceipt(r.Context(), db, mustJSON(receipt), "synthetic-operator", false)
			}
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case "/fixture/stale-reauth":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			// An administrator whose last enterprise password entry is older than five minutes.
			if _, err := db.Exec(r.Context(), `UPDATE staff_sessions SET reauthenticated_at=now()-interval '10 minutes'`); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case "/fixture/mode", "/fixture/expire-state", "/fixture/expire-session", "/fixture/db-outage", "/fixture/db-online":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			var err error
			switch r.URL.Path {
			case "/fixture/mode":
				mode.Store(r.URL.Query().Get("value"))
			case "/fixture/expire-state":
				_, err = db.Exec(r.Context(), `UPDATE oidc_pending_logins SET expires_at=now()-interval '1 second'`)
			case "/fixture/expire-session":
				_, err = db.Exec(r.Context(), `UPDATE app_sessions SET expires_at=now()-interval '1 second'`)
			default:
				dbOutage.Store(r.URL.Path == "/fixture/db-outage")
			}
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case "/fixture/metrics":
			var sessions int
			if err := db.QueryRow(r.Context(), `SELECT count(*) FROM app_sessions WHERE expires_at>now()`).Scan(&sessions); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			respond(w, 200, map[string]any{"exchanges": exchanges.Load(), "sessions": sessions,
				"ssoLogouts": ssoLogouts.Load(), "prompt": lastPrompt.Load()})
		default:
			switch {
			case strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/auth/"):
				r = r.WithContext(oidc.ClientContext(r.Context(), client))
				if dbOutage.Load() {
					offlineHandler.ServeHTTP(w, r)
				} else {
					onlineHandler.ServeHTTP(w, r)
				}
			case strings.HasPrefix(r.URL.Path, "/assets/"):
				files.ServeHTTP(w, r)
			default:
				http.ServeFile(w, r, filepath.Join(dist, "index.html"))
			}
		}
	}))
	appURL = "https://" + server.Listener.Addr().String()
	app.cfg.Origin = appURL
	app.oauth.RedirectURL = appURL + "/auth/callback"
	offline.cfg.Origin = appURL
	offline.oauth.RedirectURL = app.oauth.RedirectURL
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	fmt.Println("AUTH_FIXTURE_URL=" + appURL)
	_, _ = io.Copy(io.Discard, os.Stdin)
}
