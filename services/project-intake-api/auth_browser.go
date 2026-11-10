package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
)

type loginFailure string

func (e loginFailure) Error() string { return string(e) }

const (
	loginCancelled           loginFailure = "LOGIN_CANCELLED"
	loginProviderRejected    loginFailure = "LOGIN_PROVIDER_REJECTED"
	loginProviderUnavailable loginFailure = "LOGIN_PROVIDER_UNAVAILABLE"
	loginExpired             loginFailure = "LOGIN_EXPIRED"
	loginStateInvalid        loginFailure = "LOGIN_STATE_INVALID"
	loginNonceInvalid        loginFailure = "LOGIN_NONCE_INVALID"
	loginExchangeFailed      loginFailure = "LOGIN_EXCHANGE_FAILED"
	loginTokenInvalid        loginFailure = "LOGIN_TOKEN_INVALID"
	loginNotProvisioned      loginFailure = "LOGIN_NOT_PROVISIONED"
	loginDisabled            loginFailure = "LOGIN_DISABLED"
	loginRoleMissing         loginFailure = "LOGIN_ROLE_MISSING"
	loginEmailMismatch       loginFailure = "LOGIN_EMAIL_MISMATCH"
	loginSessionUnavailable  loginFailure = "SESSION_UNAVAILABLE"
	loginBusy                loginFailure = "LOGIN_BUSY"
)

func recoverLogin(w http.ResponseWriter, r *http.Request, reason loginFailure, status int) {
	// Only interactive GET navigation redirects. Fetch/API callers retain a JSON response.
	if r.Method == http.MethodGet && (r.Header.Get("Sec-Fetch-Mode") == "navigate" ||
		strings.Contains(r.Header.Get("Accept"), "text/html")) {
		http.Redirect(w, r, "/login?error="+string(reason), http.StatusSeeOther)
		return
	}
	fail(w, status, string(reason), "登录未完成，请返回登录页重试或联系系统管理员")
}

func (a *App) loginCookieName() string {
	if deployed(a.cfg.Env) {
		return "__Host-baobei_login"
	}
	return "baobei_login"
}

func (a *App) startLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || a.verifier == nil {
		recoverLogin(w, r, loginProviderUnavailable, http.StatusServiceUnavailable)
		return
	}
	state, nonce, binding, verifier := randomToken(), randomToken(), randomToken(), oauth2.GenerateVerifier()
	if r.URL.Query().Get("reauth") == "1" {
		nonce = "reauth-" + nonce
	}
	if err := a.storePending(r.Context(), hash(state), pendingLogin{nonce, verifier, hash(binding), time.Now().Add(5 * time.Minute)}); err != nil {
		if errors.Is(err, errAuthCapacity) {
			recoverLogin(w, r, loginBusy, http.StatusTooManyRequests)
		} else {
			recoverLogin(w, r, loginSessionUnavailable, http.StatusServiceUnavailable)
		}
		return
	}
	a.cookie(w, a.loginCookieName(), binding, 300)
	options := []oauth2.AuthCodeOption{oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)}
	if strings.HasPrefix(nonce, "reauth-") {
		options = append(options, oauth2.SetAuthURLParam("prompt", "login"), oauth2.SetAuthURLParam("max_age", "0"))
	}
	http.Redirect(w, r, a.oauth.AuthCodeURL(state, options...), http.StatusFound)
}

func (a *App) finishLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, string(loginStateInvalid), "登录回调无效")
		return
	}
	if a.verifier == nil {
		recoverLogin(w, r, loginProviderUnavailable, http.StatusBadRequest)
		return
	}
	reason, status := a.completeLogin(w, r)
	if reason != "" {
		recoverLogin(w, r, reason, status)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) completeLogin(w http.ResponseWriter, r *http.Request) (loginFailure, int) {
	query := r.URL.Query()
	name := a.loginCookieName()
	binding, cookieErr := r.Cookie(name)
	a.cookie(w, name, "", -1)
	state := query.Get("state")
	if len(query["state"]) != 1 || state == "" {
		return loginStateInvalid, http.StatusBadRequest
	}
	p, err := a.consumePending(r.Context(), hash(state))
	if errors.Is(err, pgx.ErrNoRows) {
		return loginStateInvalid, http.StatusBadRequest
	}
	if err != nil {
		return loginSessionUnavailable, http.StatusServiceUnavailable
	}
	if !time.Now().Before(p.Until) {
		return loginExpired, http.StatusBadRequest
	}
	if cookieErr != nil || subtle.ConstantTimeCompare([]byte(p.Binding), []byte(hash(binding.Value))) != 1 {
		return loginStateInvalid, http.StatusBadRequest
	}
	if query.Has("error") {
		switch query.Get("error") {
		case "access_denied":
			return loginCancelled, http.StatusBadRequest
		case "server_error", "temporarily_unavailable":
			return loginProviderUnavailable, http.StatusServiceUnavailable
		default:
			return loginProviderRejected, http.StatusBadRequest
		}
	}
	if len(query["code"]) != 1 || query.Get("code") == "" {
		return loginExchangeFailed, http.StatusUnauthorized
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	token, err := a.oauth.Exchange(ctx, query.Get("code"), oauth2.VerifierOption(p.Verifier))
	if err != nil {
		var response *oauth2.RetrieveError
		var network *url.Error
		if errors.As(err, &network) || errors.Is(err, context.DeadlineExceeded) ||
			errors.As(err, &response) && response.Response != nil &&
				(response.Response.StatusCode >= 500 || response.Response.StatusCode == 429) {
			return loginProviderUnavailable, http.StatusServiceUnavailable
		}
		return loginExchangeFailed, http.StatusUnauthorized
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return loginTokenInvalid, http.StatusUnauthorized
	}
	id, err := a.verifier.Verify(ctx, raw)
	if err != nil || id.Subject == "" {
		return loginTokenInvalid, http.StatusUnauthorized
	}
	if id.Nonce != p.Nonce {
		return loginNonceInvalid, http.StatusUnauthorized
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		AuthTime      int64  `json:"auth_time"`
		Resource      map[string]struct {
			Roles []string `json:"roles"`
		} `json:"resource_access"`
	}
	if err = id.Claims(&claims); err != nil {
		return loginTokenInvalid, http.StatusUnauthorized
	}
	email := loginEmail{address: claims.Email, verified: claims.EmailVerified}
	roles := claims.Resource[a.cfg.ClientID].Roles
	err = a.sessionAt(w, r.WithContext(ctx), id.Subject, reauthenticationTime(p.Nonce, claims.AuthTime, p.Until), &email, roles...)
	if err != nil {
		var reason loginFailure
		if errors.As(err, &reason) {
			switch reason {
			case loginNotProvisioned, loginDisabled, loginRoleMissing, loginEmailMismatch:
				return reason, http.StatusForbidden
			}
		}
		return loginSessionUnavailable, http.StatusServiceUnavailable
	}
	return "", 0
}

func (a *App) sessionFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, http.ErrNoCookie):
		fail(w, http.StatusUnauthorized, "UNAUTHENTICATED", "请先登录")
	case errors.Is(err, pgx.ErrNoRows):
		fail(w, http.StatusUnauthorized, "SESSION_EXPIRED", "登录已失效，请重新登录")
	default:
		fail(w, http.StatusServiceUnavailable, string(loginSessionUnavailable), "会话服务暂时不可用，请重试")
	}
}
