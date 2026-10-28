// Package webauth implements the same-origin OIDC BFF in the existing Cloud process.
package webauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"golang.org/x/oauth2"
)

const CookieName = "__Host-crane_session"
const loginCookie = "__Host-crane_login"

var ErrUnauthenticated = errors.New("valid enabled server session required")

type Config struct {
	PublicURL, Issuer, ClientID string
	ClientSecret, ServerSecret  []byte
	AbsoluteTTL, IdleTTL        time.Duration
	TrustedProxies              []string
	HTTPClient                  *http.Client // Tests can supply a client trusting their ephemeral TLS CA.
}

type Manager struct {
	DB         *sql.DB
	config     Config
	key        [32]byte
	aead       cipher.AEAD
	oauth      oauth2.Config
	verifier   *oidc.IDTokenVerifier
	proxies    []netip.Prefix
	client     *http.Client
	now        func() time.Time
	endSession string
}

type Session struct {
	User                     identity.User
	Hash, CSRFHash           []byte
	ExpiresAt, IdleExpiresAt time.Time
}

func New(ctx context.Context, db *sql.DB, config Config) (*Manager, error) {
	if !platform.SecureURL(config.PublicURL, false) || !platform.SecureURL(config.Issuer, true) || config.ClientID == "" ||
		len(config.ClientSecret) < 32 || len(config.ServerSecret) < 32 || config.AbsoluteTTL < time.Minute ||
		config.AbsoluteTTL > 24*time.Hour || config.IdleTTL < 30*time.Second || config.IdleTTL > config.AbsoluteTTL {
		return nil, errors.New("invalid production BFF configuration")
	}
	m := &Manager{DB: db, config: config, key: sha256.Sum256(config.ServerSecret), now: time.Now, client: config.HTTPClient}
	if m.client == nil {
		m.client = &http.Client{Timeout: 10 * time.Second}
	}
	client := *m.client
	redirectPolicy := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" || request.URL.User != nil || len(via) >= 10 {
			return errors.New("OIDC redirect rejected")
		}
		if redirectPolicy != nil {
			return redirectPolicy(request, via)
		}
		return nil
	}
	m.client = &client
	for _, value := range config.TrustedProxies {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil || prefix.Bits() == 0 {
			return nil, errors.New("invalid trusted proxy CIDR")
		}
		m.proxies = append(m.proxies, prefix)
	}
	block, err := aes.NewCipher(m.key[:])
	if err != nil {
		return nil, err
	}
	m.aead, err = cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, m.client), config.Issuer)
	if err != nil {
		return nil, errors.New("OIDC discovery or issuer validation failed")
	}
	endpoint := provider.Endpoint()
	var metadata struct {
		EndSession string `json:"end_session_endpoint"`
		JWKS       string `json:"jwks_uri"`
	}
	if provider.Claims(&metadata) != nil {
		return nil, errors.New("OIDC provider metadata invalid")
	}
	if metadata.EndSession != "" {
		u, err := url.Parse(metadata.EndSession)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return nil, errors.New("OIDC logout endpoint requires HTTPS")
		}
		m.endSession = metadata.EndSession
	}
	for _, value := range []string{endpoint.AuthURL, endpoint.TokenURL, metadata.JWKS} {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return nil, errors.New("OIDC endpoints require HTTPS")
		}
	}
	m.oauth = oauth2.Config{ClientID: config.ClientID, ClientSecret: string(config.ClientSecret), Endpoint: endpoint,
		RedirectURL: config.PublicURL + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "profile"}}
	m.verifier = provider.Verifier(&oidc.Config{ClientID: config.ClientID, SupportedSigningAlgs: []string{oidc.RS256}})
	return m, nil
}

func (m *Manager) digest(domain, value string) []byte {
	hash := hmac.New(sha256.New, m.key[:])
	_, _ = hash.Write([]byte(domain + "\x00" + value))
	return hash.Sum(nil)
}

func randomID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (m *Manager) seal(value string) ([]byte, error) {
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return m.aead.Seal(nonce, nonce, []byte(value), []byte("crane/oidc/pkce/v1")), nil
}

func (m *Manager) unseal(value []byte) (string, error) {
	n := m.aead.NonceSize()
	if len(value) < n {
		return "", errors.New("invalid login transaction")
	}
	b, err := m.aead.Open(nil, value[:n], value[n:], []byte("crane/oidc/pkce/v1"))
	return string(b), err
}

func setCookie(w http.ResponseWriter, name, value string, expires time.Time, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Expires: expires.UTC(), MaxAge: age})
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func (m *Manager) Register(mux *http.ServeMux) {
	mux.Handle("GET /auth/login", m.Transport(http.HandlerFunc(m.login)))
	mux.Handle("GET /auth/callback", m.Transport(http.HandlerFunc(m.callback)))
	mux.Handle("GET /auth/session", m.Transport(http.HandlerFunc(m.current)))
	mux.Handle("POST /auth/logout", m.Transport(http.HandlerFunc(m.logout)))
}

func (m *Manager) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet || !m.SameOrigin(r, false) {
		status := http.StatusForbidden
		if err := auditlog.Write(r.Context(), m.DB, m.Metadata(r).Event("anonymous", "", "", "authorization_denied", "login_origin")); err != nil {
			status = 503
		}
		jsonResponse(w, status, map[string]string{"error": "login origin rejected"})
		return
	}
	state, err := randomID()
	if err != nil {
		jsonResponse(w, 503, map[string]string{"error": "login unavailable"})
		return
	}
	binding, err := randomID()
	if err != nil {
		jsonResponse(w, 503, map[string]string{"error": "login unavailable"})
		return
	}
	nonce, err := randomID()
	if err != nil {
		jsonResponse(w, 503, map[string]string{"error": "login unavailable"})
		return
	}
	verifier := oauth2.GenerateVerifier()
	encrypted, err := m.seal(verifier)
	if err != nil {
		jsonResponse(w, 503, map[string]string{"error": "login unavailable"})
		return
	}
	now := m.now().UTC()
	tx, err := m.DB.BeginTx(r.Context(), nil)
	if err != nil {
		jsonResponse(w, 503, map[string]string{"error": "login unavailable"})
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `SELECT pg_advisory_xact_lock(7160004)`); err == nil {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM oidc_login_transactions WHERE expires_at<=$1`, now)
	}
	var result sql.Result
	if err == nil {
		result, err = tx.ExecContext(r.Context(), `INSERT INTO oidc_login_transactions(state_hash,binding_hash,nonce_hash,pkce_cipher,redirect_uri,created_at,expires_at)
SELECT $1,$2,$3,$4,$5,$6,$7 WHERE (SELECT count(*) FROM oidc_login_transactions)<10000`, m.digest("state", state), m.digest("binding", binding), m.digest("nonce", nonce), encrypted, m.oauth.RedirectURL, now, now.Add(5*time.Minute))
	}
	if err == nil {
		if count, e := result.RowsAffected(); e != nil || count != 1 {
			err = errors.New("login transaction capacity reached")
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		jsonResponse(w, 503, map[string]string{"error": "login unavailable"})
		return
	}
	setCookie(w, loginCookie, binding, now.Add(5*time.Minute), 300)
	http.Redirect(w, r, m.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)), http.StatusFound)
}

func singleQuery(r *http.Request, name string) (string, bool) {
	values := r.URL.Query()[name]
	returnValue := ""
	if len(values) == 1 {
		returnValue = values[0]
	}
	return returnValue, len(values) == 1 && returnValue != "" && len(returnValue) <= 4096
}

func (m *Manager) failedLogin(w http.ResponseWriter, r *http.Request, reason string) {
	status := http.StatusUnauthorized
	if err := auditlog.Write(r.Context(), m.DB, m.Metadata(r).Event("anonymous", "", "", "login_failure", reason)); err != nil {
		status = http.StatusServiceUnavailable
	}
	jsonResponse(w, status, map[string]string{"error": "login rejected"})
}

func (m *Manager) callback(w http.ResponseWriter, r *http.Request) {
	setCookie(w, loginCookie, "", time.Unix(1, 0), -1)
	state, stateOK := singleQuery(r, "state")
	code, codeOK := singleQuery(r, "code")
	binding, err := r.Cookie(loginCookie)
	if r.Method != http.MethodGet || !stateOK || !codeOK || err != nil || r.URL.Query().Get("error") != "" {
		m.failedLogin(w, r, "invalid_callback")
		return
	}
	var nonceHash, encrypted []byte
	var redirect string
	now := m.now().UTC()
	err = m.DB.QueryRowContext(r.Context(), `DELETE FROM oidc_login_transactions
WHERE state_hash=$1 AND binding_hash=$2 AND expires_at>$3 RETURNING nonce_hash,pkce_cipher,redirect_uri`, m.digest("state", state), m.digest("binding", binding.Value), now).Scan(&nonceHash, &encrypted, &redirect)
	if err != nil || redirect != m.oauth.RedirectURL {
		m.failedLogin(w, r, "invalid_or_expired_state")
		return
	}
	verifier, err := m.unseal(encrypted)
	if err != nil {
		m.failedLogin(w, r, "invalid_transaction")
		return
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(r.Context(), m.client), 10*time.Second)
	defer cancel()
	tokens, err := m.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		m.failedLogin(w, r, "exchange_failed")
		return
	}
	rawID, ok := tokens.Extra("id_token").(string)
	if !ok {
		m.failedLogin(w, r, "missing_id_token")
		return
	}
	verified, err := m.verifier.Verify(ctx, rawID)
	if err != nil {
		m.failedLogin(w, r, "id_token_rejected")
		return
	}
	var claims struct {
		Nonce string `json:"nonce"`
		AZP   string `json:"azp"`
	}
	if verified.Claims(&claims) != nil || verified.Subject == "" || subtle.ConstantTimeCompare(nonceHash, m.digest("nonce", claims.Nonce)) != 1 ||
		(claims.AZP != "" && claims.AZP != m.config.ClientID) || (len(verified.Audience) > 1 && claims.AZP != m.config.ClientID) {
		m.failedLogin(w, r, "nonce_or_client_rejected")
		return
	}
	user, err := identity.Lookup(ctx, m.DB, verified.Issuer, verified.Subject)
	if err != nil {
		m.failedLogin(w, r, "unassigned_or_disabled_identity")
		return
	}
	// No access/refresh/ID token is persisted or returned to the browser.
	old := ""
	if cookie, err := r.Cookie(CookieName); err == nil {
		old = cookie.Value
	}
	raw, session, err := m.createSession(ctx, user, old, m.Metadata(r))
	if err != nil {
		jsonResponse(w, 503, map[string]string{"error": "session unavailable"})
		return
	}
	setCookie(w, CookieName, raw, session.ExpiresAt, int(m.config.AbsoluteTTL/time.Second))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, m.config.PublicURL+"/#/overview", http.StatusSeeOther)
}

func (m *Manager) csrf(raw string) string {
	return base64.RawURLEncoding.EncodeToString(m.digest("csrf", raw))
}

func (m *Manager) createSession(ctx context.Context, user identity.User, old string, meta Metadata) (string, Session, error) {
	raw, err := randomID()
	if err != nil {
		return "", Session{}, err
	}
	now := m.now().UTC()
	s := Session{User: user, Hash: m.digest("session", raw), CSRFHash: m.digest("csrf-hash", m.csrf(raw)),
		ExpiresAt: now.Add(m.config.AbsoluteTTL), IdleExpiresAt: now.Add(m.config.IdleTTL)}
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", s, err
	}
	defer tx.Rollback()
	if old != "" {
		if _, err = tx.ExecContext(ctx, `DELETE FROM web_sessions WHERE session_hash=$1`, m.digest("session", old)); err != nil {
			return "", s, err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM web_sessions WHERE expires_at<=$1 OR idle_expires_at<=$1`, now); err != nil {
		return "", s, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO web_sessions(session_hash,user_id,csrf_hash,created_at,last_seen_at,expires_at,idle_expires_at)
VALUES($1,$2,$3,$4,$4,$5,$6)`, s.Hash, user.ID, s.CSRFHash, now, s.ExpiresAt, s.IdleExpiresAt); err != nil {
		return "", s, err
	}
	if err = auditlog.Write(ctx, tx, meta.Event(user.ID, "", "", "login_success", "success")); err != nil {
		return "", s, err
	}
	return raw, s, tx.Commit()
}

// Session checks the authoritative DB on every call. WS frames use touch=false,
// so a passive stream cannot keep an idle web session alive indefinitely.
func (m *Manager) Session(r *http.Request, touch bool) (Session, error) {
	var s Session
	cookie, err := r.Cookie(CookieName)
	if err != nil || len(cookie.Value) != 43 {
		return s, ErrUnauthenticated
	}
	now := m.now().UTC()
	s.Hash = m.digest("session", cookie.Value)
	query := `SELECT u.id,u.display_name,s.csrf_hash,s.expires_at,s.idle_expires_at FROM web_sessions s
JOIN users u ON u.id=s.user_id WHERE s.session_hash=$1 AND u.enabled AND s.expires_at>$2 AND s.idle_expires_at>$2`
	err = m.DB.QueryRowContext(r.Context(), query, s.Hash, now).Scan(&s.User.ID, &s.User.DisplayName, &s.CSRFHash, &s.ExpiresAt, &s.IdleExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrUnauthenticated
	}
	if err != nil {
		return s, err
	}
	if touch {
		err = m.DB.QueryRowContext(r.Context(), `UPDATE web_sessions SET last_seen_at=$2,idle_expires_at=LEAST(expires_at,$3)
WHERE session_hash=$1 AND expires_at>$2 AND idle_expires_at>$2 RETURNING idle_expires_at`, s.Hash, now, now.Add(m.config.IdleTTL)).Scan(&s.IdleExpiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrUnauthenticated
		}
	}
	return s, err
}

func (m *Manager) CheckCSRF(r *http.Request, s Session) bool {
	if !m.SameOrigin(r, true) {
		return false
	}
	values := r.Header.Values("X-CSRF-Token")
	return len(values) == 1 && len(values[0]) == 43 && subtle.ConstantTimeCompare(s.CSRFHash, m.digest("csrf-hash", values[0])) == 1
}

func (m *Manager) current(w http.ResponseWriter, r *http.Request) {
	s, err := m.Session(r, r.Method == http.MethodGet)
	if err != nil {
		status := 503
		if errors.Is(err, ErrUnauthenticated) {
			status = 401
		}
		if auditlog.Write(r.Context(), m.DB, m.Metadata(r).Event("anonymous", "", "", "authorization_denied", "session_required")) != nil {
			status = 503
		}
		jsonResponse(w, status, map[string]string{"error": "session required"})
		return
	}
	grants, err := identity.Grants(r.Context(), m.DB, s.User.ID)
	if err != nil {
		jsonResponse(w, 503, map[string]string{"error": "grants unavailable"})
		return
	}
	cookie, _ := r.Cookie(CookieName)
	jsonResponse(w, 200, contract.WebSession{UserId: s.User.ID, DisplayName: s.User.DisplayName, Projects: grants,
		CsrfToken: m.csrf(cookie.Value), ExpiresAt: s.ExpiresAt, IdleExpiresAt: s.IdleExpiresAt})
}

func (m *Manager) logout(w http.ResponseWriter, r *http.Request) {
	s, err := m.Session(r, false)
	if err != nil {
		status := 401
		if auditlog.Write(r.Context(), m.DB, m.Metadata(r).Event("anonymous", "", "", "authorization_denied", "logout_session")) != nil {
			status = 503
		}
		jsonResponse(w, status, map[string]string{"error": "session required"})
		return
	}
	meta := m.Metadata(r)
	if !m.CheckCSRF(r, s) {
		status := 403
		if err = auditlog.Write(r.Context(), m.DB, meta.Event(s.User.ID, "", "", "authorization_denied", "csrf")); err != nil {
			status = 503
		}
		jsonResponse(w, status, map[string]string{"error": "request rejected"})
		return
	}
	tx, err := m.DB.BeginTx(r.Context(), nil)
	if err != nil {
		jsonResponse(w, 503, map[string]string{"error": "logout unavailable"})
		return
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(r.Context(), `DELETE FROM web_sessions WHERE session_hash=$1`, s.Hash)
	if err == nil {
		err = auditlog.Write(r.Context(), tx, meta.Event(s.User.ID, "", "", "logout", "success"))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		jsonResponse(w, 503, map[string]string{"error": "logout unavailable"})
		return
	}
	setCookie(w, CookieName, "", time.Unix(1, 0), -1)
	if m.endSession != "" {
		target, _ := url.Parse(m.endSession)
		values := target.Query()
		values.Set("client_id", m.config.ClientID)
		values.Set("post_logout_redirect_uri", m.config.PublicURL+"/")
		target.RawQuery = values.Encode()
		w.Header().Set("Location", target.String())
	}
	jsonResponse(w, 204, nil)
}
