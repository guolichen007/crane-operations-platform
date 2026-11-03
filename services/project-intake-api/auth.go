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
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
)

type pendingLogin struct {
	Nonce, Verifier, Binding string
	Until                    time.Time
}
type App struct {
	db       *pgxpool.Pool
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
		a.oauth = oauth2.Config{ClientID: c.ClientID, ClientSecret: c.Secret, Endpoint: p.Endpoint(), RedirectURL: c.Origin + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "profile"}}
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
	if a.cfg.Env == "production" {
		return "__Host-baobei_session"
	}
	return "baobei_dev_session"
}
func (a *App) cookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: a.cfg.Env == "production", SameSite: http.SameSiteLaxMode, MaxAge: age})
}
func (a *App) user(r *http.Request) (User, error) {
	var u User
	c, e := r.Cookie(a.cookieName())
	if e != nil {
		return u, e
	}
	e = a.db.QueryRow(r.Context(), `SELECT u.subject,u.display_name,u.role,coalesce(u.team_id::text,''),s.csrf FROM app_sessions s JOIN app_users u ON u.subject=s.subject WHERE s.token_hash=$1 AND s.expires_at>now() AND u.active`, hash(c.Value)).Scan(&u.Subject, &u.Name, &u.Role, &u.TeamID, &u.CSRF)
	return u, e
}
func (a *App) session(w http.ResponseWriter, r *http.Request, subject string) error {
	token, csrf := randomToken(), randomToken()
	tx, e := a.db.Begin(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback(r.Context())
	if old, e := r.Cookie(a.cookieName()); e == nil {
		if _, e = tx.Exec(r.Context(), `DELETE FROM app_sessions WHERE token_hash=$1`, hash(old.Value)); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(r.Context(), `DELETE FROM app_sessions WHERE expires_at<now()`); e != nil {
		return e
	}
	if _, e = tx.Exec(r.Context(), `INSERT INTO app_sessions VALUES($1,$2,$3,now()+interval '8 hours')`, hash(token), subject, csrf); e != nil {
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
		if !contains([]string{"synthetic-a", "synthetic-b", "synthetic-manager", "synthetic-director", "synthetic-c", "synthetic-manager-other"}, b.Subject) {
			fail(w, 403, "FORBIDDEN", "测试身份不允许")
			return
		}
		if e := a.session(w, r, b.Subject); e != nil {
			fail(w, 503, "SESSION_UNAVAILABLE", "登录暂时不可用")
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	case "/auth/login":
		if r.Method != "GET" || a.verifier == nil {
			fail(w, 503, "OIDC_UNAVAILABLE", "企业认证未配置，请联系管理员")
			return
		}
		state, nonce, binding, verifier := randomToken(), randomToken(), randomToken(), oauth2.GenerateVerifier()
		if e := a.storePending(r.Context(), hash(state), pendingLogin{nonce, verifier, hash(binding), time.Now().Add(5 * time.Minute)}); e != nil {
			if errors.Is(e, errAuthCapacity) {
				fail(w, 429, "AUTH_BUSY", "登录繁忙，请稍后重试")
			} else {
				fail(w, 503, "AUTH_UNAVAILABLE", "登录暂时不可用，请重试")
			}
			return
		}
		loginCookie := "baobei_login"
		if a.cfg.Env == "production" {
			loginCookie = "__Host-baobei_login"
		}
		a.cookie(w, loginCookie, binding, 300)
		http.Redirect(w, r, a.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
	case "/auth/callback":
		if r.Method != "GET" || a.verifier == nil {
			fail(w, 400, "OIDC_INVALID", "登录回调无效")
			return
		}
		state := r.URL.Query().Get("state")
		p, pendingErr := a.consumePending(r.Context(), hash(state))
		name := "baobei_login"
		if a.cfg.Env == "production" {
			name = "__Host-baobei_login"
		}
		binding, e := r.Cookie(name)
		a.cookie(w, name, "", -1)
		if pendingErr != nil || p.Until.Before(time.Now()) || e != nil || subtle.ConstantTimeCompare([]byte(p.Binding), []byte(hash(binding.Value))) != 1 {
			fail(w, 400, "OIDC_INVALID", "登录已失效，请重新登录")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		token, e := a.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(p.Verifier))
		if e != nil {
			fail(w, 401, "OIDC_INVALID", "企业登录失败")
			return
		}
		raw, ok := token.Extra("id_token").(string)
		if !ok {
			fail(w, 401, "OIDC_INVALID", "企业登录凭证无效")
			return
		}
		id, e := a.verifier.Verify(ctx, raw)
		if e != nil || id.Nonce != p.Nonce {
			fail(w, 401, "OIDC_INVALID", "企业登录凭证验证失败")
			return
		}
		var claims struct {
			Realm struct {
				Roles []string `json:"roles"`
			} `json:"realm_access"`
			Resource map[string]struct {
				Roles []string `json:"roles"`
			} `json:"resource_access"`
		}
		if e = id.Claims(&claims); e != nil {
			fail(w, 401, "OIDC_INVALID", "角色声明无效")
			return
		}
		roles := claims.Resource[a.cfg.ClientID].Roles
		var role string
		e = a.db.QueryRow(ctx, `SELECT role FROM app_users WHERE subject=$1 AND active`, id.Subject).Scan(&role)
		if e != nil || !contains(roles, role) {
			fail(w, 403, "FORBIDDEN", "报备账号未授权，请联系管理员")
			return
		}
		if e = a.session(w, r, id.Subject); e != nil {
			fail(w, 503, "SESSION_UNAVAILABLE", "登录暂时不可用")
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	case "/auth/logout":
		u, e := a.user(r)
		if e != nil {
			fail(w, 401, "UNAUTHENTICATED", "请先登录")
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
