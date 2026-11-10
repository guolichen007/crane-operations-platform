package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
)

type pendingLogin struct {
	Nonce, Verifier, Binding string
	Until                    time.Time
}
type App struct {
	db       *pgxpool.Pool
	staffDB  *pgxpool.Pool
	cfg      Config
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
}

func newApp(ctx context.Context, db *pgxpool.Pool, c Config) (*App, error) {
	a := &App{db: db, cfg: c}
	if c.Issuer != "" {
		p, e := oidc.NewProvider(ctx, c.Issuer)
		if e != nil {
			return nil, e
		}
		a.verifier = p.Verifier(&oidc.Config{ClientID: c.ClientID})
		a.oauth = oauth2.Config{ClientID: c.ClientID, ClientSecret: c.Secret, Endpoint: p.Endpoint(), RedirectURL: c.Origin + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}
	}
	return a, nil
}
func randomToken() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic("random unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func (a *App) cookieName() string {
	if deployed(a.cfg.Env) {
		return "__Host-baobei_session"
	}
	return "baobei_dev_session"
}
func (a *App) cookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: deployed(a.cfg.Env), SameSite: http.SameSiteLaxMode, MaxAge: age})
}
func (a *App) user(r *http.Request) (User, error) {
	var u User
	c, e := r.Cookie(a.cookieName())
	if e != nil {
		return u, e
	}
	u.sessionHash = hash(c.Value)
	var mask int
	e = a.db.QueryRow(r.Context(), `SELECT u.subject,u.display_name,u.role,coalesce(u.team_id::text,''),s.csrf,u.staff_permissions FROM app_sessions s JOIN app_users u ON u.subject=s.subject WHERE s.token_hash=$1 AND s.expires_at>now() AND u.active`, u.sessionHash).Scan(&u.Subject, &u.Name, &u.Role, &u.TeamID, &u.CSRF, &mask)
	u.Permissions = capabilityList(mask)
	return u, e
}
func (a *App) session(w http.ResponseWriter, r *http.Request, subject string, roles ...string) error {
	return a.sessionAt(w, r, subject, nil, nil, roles...)
}

type loginEmail struct {
	address  string
	verified bool
}

func (a *App) sessionAt(w http.ResponseWriter, r *http.Request, subject string, at *time.Time, email *loginEmail, roles ...string) error {
	token, csrf := randomToken(), randomToken()
	var expectedRole string
	var pendingActivation bool
	pool := a.db
	lock := "pg_advisory_xact_lock_shared"
	if email == nil {
		if err := a.db.QueryRow(r.Context(), `SELECT role FROM app_users WHERE subject=$1 AND active`, subject).Scan(&expectedRole); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errIdentityDenied
			}
			return err
		}
		if staffRole(expectedRole) {
			pool = a.staffPool()
		}
	} else {
		pool = a.staffPool()
		if pool != nil {
			// Use an exclusive identity lock only when an activation may write the account.
			var status string
			err := pool.QueryRow(r.Context(), `SELECT staff_status FROM app_users WHERE subject=$1`, subject).Scan(&status)
			if errors.Is(err, pgx.ErrNoRows) {
				return loginNotProvisioned
			}
			if err != nil {
				return err
			}
			pendingActivation = status == staffActivation
			if pendingActivation {
				lock = "pg_advisory_xact_lock"
			}
		}
	}
	if pool == nil {
		return loginSessionUnavailable
	}
	tx, e := pool.Begin(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback(r.Context())
	// Serialize authorization/session creation with explicit identity changes.
	if _, e = tx.Exec(r.Context(), `SELECT `+lock+`($1)`, identityLock); e != nil {
		return e
	}
	var currentRole string
	if email != nil {
		currentRole, e = authorizeOIDCSession(r.Context(), tx, subject, *email, roles, pendingActivation)
		if e != nil {
			return e
		}
	} else {
		e = tx.QueryRow(r.Context(), `SELECT role FROM app_users WHERE subject=$1 AND active`, subject).Scan(&currentRole)
		if errors.Is(e, pgx.ErrNoRows) || e == nil && (currentRole != expectedRole || roles != nil && !contains(roles, currentRole)) {
			return errIdentityDenied
		}
		if e != nil {
			return e
		}
	}
	if old, e := r.Cookie(a.cookieName()); e == nil {
		if _, e = tx.Exec(r.Context(), `DELETE FROM app_sessions WHERE token_hash=$1`, hash(old.Value)); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(r.Context(), `DELETE FROM app_sessions WHERE expires_at<now()`); e != nil {
		return e
	}
	if _, e = tx.Exec(r.Context(), `INSERT INTO app_sessions(token_hash,subject,csrf,expires_at)
    VALUES($1,$2,$3,now()+interval '8 hours')`, hash(token), subject, csrf); e != nil {
		return e
	}
	if staffRole(currentRole) {
		if _, e = tx.Exec(r.Context(), `INSERT INTO staff_sessions(token_hash,subject,reauthenticated_at) VALUES($1,$2,$3)`, hash(token), subject, at); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(r.Context(), `UPDATE app_users SET last_login_at=now() WHERE subject=$1`, subject); e != nil {
		return e
	}
	if e = tx.Commit(r.Context()); e != nil {
		return e
	}
	a.cookie(w, a.cookieName(), token, 28800)
	return nil
}
func (a *App) auth(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/auth/config":
		respond(w, 200, map[string]any{"development": a.cfg.Dev, "oidc": a.verifier != nil})
	case "/auth/dev":
		if r.Method != "POST" || !a.cfg.Dev {
			fail(w, 404, "NOT_FOUND", "入口不存在")
			return
		}
		if r.Header.Get("Origin") != a.cfg.Origin {
			fail(w, 403, "CSRF_INVALID", "请求来源不允许")
			return
		}
		var b struct {
			Subject string `json:"subject"`
		}
		if !decode(w, r, &b) {
			return
		}
		if !contains([]string{"synthetic-a", "synthetic-b", "synthetic-manager", "synthetic-director", "synthetic-c", "synthetic-manager-other", "synthetic-admin", "synthetic-staff-desktop", "synthetic-staff-mobile"}, b.Subject) {
			fail(w, 403, "FORBIDDEN", "测试身份不允许")
			return
		}
		at := time.Now()
		if e := a.sessionAt(w, r, b.Subject, &at, nil); e != nil {
			if errors.Is(e, errIdentityDenied) {
				fail(w, 403, "FORBIDDEN", "测试身份未启用")
				return
			}
			fail(w, 503, "SESSION_UNAVAILABLE", "登录暂时不可用")
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	case "/auth/login":
		a.startLogin(w, r)
	case "/auth/callback":
		a.finishLogin(w, r)
	case "/auth/logout":
		u, e := a.user(r)
		if e != nil {
			a.sessionFailure(w, e)
			return
		}
		if r.Method != "POST" {
			fail(w, 405, "METHOD_NOT_ALLOWED", "请使用退出登录操作")
			return
		}
		if !a.csrf(w, r, u) {
			return
		}
		c, _ := r.Cookie(a.cookieName())
		if _, e = a.db.Exec(r.Context(), `DELETE FROM app_sessions WHERE token_hash=$1`, hash(c.Value)); e != nil {
			fail(w, 503, "SESSION_UNAVAILABLE", "退出失败，请重试")
			return
		}
		a.cookie(w, a.cookieName(), "", -1)
		respond(w, 200, map[string]bool{"ok": true})
	default:
		fail(w, 404, "NOT_FOUND", "入口不存在")
	}
}
func (a *App) csrf(w http.ResponseWriter, r *http.Request, u User) bool {
	if r.Header.Get("Origin") != a.cfg.Origin || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(u.CSRF)) != 1 {
		fail(w, 403, "CSRF_INVALID", "安全校验失败，请刷新页面后重试")
		return false
	}
	return true
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"code": code, "message": message, "requestId": w.Header().Get("X-Request-ID"), "details": map[string]any{}})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		fail(w, 415, "CONTENT_TYPE", "请使用 JSON 请求")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		fail(w, 400, "INVALID_INPUT", "请求字段不正确")
		return false
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		fail(w, 400, "INVALID_INPUT", "请求只能包含一个 JSON 对象")
		return false
	}
	return true
}
