// Preview-only read-only precheck. The production provisioner (main.go) hardcodes
// realm "crane" / client "crane-bff" and must never run against Preview. This
// precheck is the opposite: it accepts only the exact Preview contract and only
// reads, never writes. It is idempotent and fails closed on any cross-environment
// (production) target.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

const (
	previewRealm  = "crane-preview"
	previewClient = "crane-b4-preview"
	previewHost   = "b4.crane.youdaozd.com"
	// The Preview Keycloak admin endpoint verified read-only by the server CC:
	// the loopback port the Preview base publishes for Keycloak. The admin
	// password is never sent anywhere else.
	previewAdminURL = "http://127.0.0.1:18085"
)

// validatePreviewAdminURL locks the admin target to the exact verified Preview
// Keycloak loopback endpoint. Unlike the production endpoint() check it does not
// accept arbitrary HTTPS hosts, other ports, other loopback names, paths,
// userinfo or the production domain.
func validatePreviewAdminURL(raw string) error {
	if raw != previewAdminURL {
		return errors.New("preview precheck refuses admin URL \"" + raw + "\" (must be exactly " + previewAdminURL + ")")
	}
	return nil
}

// validatePreviewTarget is fail-closed: only the exact Preview realm/client/
// public-origin pass. A production realm, production client, unknown realm, or
// wrong host is rejected before any HTTP request is issued.
func validatePreviewTarget(publicURL, realm, client string) error {
	if realm != previewRealm {
		return errors.New("preview precheck refuses realm \"" + realm + "\" (must be " + previewRealm + ")")
	}
	if client != previewClient {
		return errors.New("preview precheck refuses client \"" + client + "\" (must be " + previewClient + ")")
	}
	u, err := url.Parse(publicURL)
	if err != nil {
		return errors.New("preview precheck invalid public URL")
	}
	if u.Scheme != "https" || u.Hostname() != previewHost {
		return errors.New("preview precheck refuses public URL \"" + publicURL + "\" (must be https://" + previewHost + ")")
	}
	return nil
}

// previewPrecheck verifies the existing Preview realm/client read-only. It never
// writes, never prints secrets, and is idempotent (re-runs are identical).
func previewPrecheck(ctx context.Context) error {
	base, public := os.Getenv("KEYCLOAK_ADMIN_URL"), os.Getenv("PUBLIC_URL")
	realm, clientID := os.Getenv("PREVIEW_REALM"), os.Getenv("PREVIEW_CLIENT")
	if realm == "" {
		realm = previewRealm
	}
	if clientID == "" {
		clientID = previewClient
	}
	// Both locks run before the admin password file is even read.
	if err := validatePreviewAdminURL(base); err != nil {
		return err
	}
	if !platform.SecureURL(public, false) {
		return errors.New("explicit public HTTPS origin required")
	}
	if err := validatePreviewTarget(public, realm, clientID); err != nil {
		return err
	}

	admin, err := platform.PrivateSecret(os.Getenv("KEYCLOAK_BOOTSTRAP_PASSWORD_FILE"), 32)
	if err != nil {
		return err
	}
	h := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return runPreviewPrecheck(ctx, h, base, public, realm, clientID, admin)
}

// runPreviewPrecheck performs the read-only checks against an explicit admin
// endpoint. Apart from the admin token request it issues GET requests only.
func runPreviewPrecheck(ctx context.Context, h *http.Client, base, public, realm, clientID string, admin []byte) error {
	if err := validatePreviewTarget(public, realm, clientID); err != nil {
		return err
	}
	form := url.Values{"grant_type": {"password"}, "client_id": {"admin-cli"}, "username": {"bootstrap-crane"}, "password": {string(admin)}}
	r, err := http.NewRequestWithContext(ctx, "POST", base+"/realms/master/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := h.Do(r)
	if err != nil {
		return errors.New("keycloak admin authentication unavailable")
	}
	var authentication struct {
		AccessToken string `json:"access_token"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&authentication)
	_ = response.Body.Close()
	if response.StatusCode != 200 || decodeErr != nil || authentication.AccessToken == "" {
		return errors.New("keycloak admin authentication rejected")
	}
	c := client{base: base, token: authentication.AccessToken, http: h}

	// Discovery issuer must match the Preview origin exactly.
	var discovery struct {
		Issuer string `json:"issuer"`
	}
	status, err := c.request(ctx, "GET", "/realms/"+realm+"/.well-known/openid-configuration", nil, &discovery)
	if err != nil || status != 200 {
		return errors.New("preview realm discovery unavailable")
	}
	origin := strings.TrimSuffix(public, "/")
	if discovery.Issuer != origin+"/realms/"+realm {
		return errors.New("preview issuer mismatch")
	}
	// Realm must already exist (read-only; no creation here).
	status, err = c.request(ctx, "GET", "/admin/realms/"+realm, nil, nil)
	if err != nil || status != 200 {
		return errors.New("preview realm not found (precheck is read-only)")
	}
	// Exactly one client, whose redirect URIs are exactly the Preview callback and
	// whose web origins are exactly the Preview origin — no wildcard, no extra
	// (e.g. production) entries.
	var clients []previewClientRepresentation
	status, err = c.request(ctx, "GET", "/admin/realms/"+realm+"/clients?clientId="+url.QueryEscape(clientID), nil, &clients)
	if err != nil || status != 200 || len(clients) != 1 {
		return errors.New("preview client lookup rejected")
	}
	// Keycloak's clientId query is a search; re-verify the exact identity.
	if clients[0].ClientID != clientID {
		return errors.New("preview client lookup returned a different clientId")
	}
	if err := clients[0].secureContract(); err != nil {
		return err
	}
	if !exactly(clients[0].RedirectURIs, origin+"/auth/callback") {
		return errors.New("preview client redirect URIs must be exactly the Preview callback")
	}
	if !exactly(clients[0].WebOrigins, origin) {
		return errors.New("preview client web origins must be exactly the Preview origin")
	}
	return nil
}

// previewClientRepresentation is the subset of a Keycloak client read back by the
// precheck.
type previewClientRepresentation struct {
	ClientID                  string            `json:"clientId"`
	Enabled                   bool              `json:"enabled"`
	PublicClient              bool              `json:"publicClient"`
	BearerOnly                bool              `json:"bearerOnly"`
	StandardFlowEnabled       bool              `json:"standardFlowEnabled"`
	ImplicitFlowEnabled       bool              `json:"implicitFlowEnabled"`
	DirectAccessGrantsEnabled bool              `json:"directAccessGrantsEnabled"`
	ServiceAccountsEnabled    bool              `json:"serviceAccountsEnabled"`
	RedirectURIs              []string          `json:"redirectUris"`
	WebOrigins                []string          `json:"webOrigins"`
	Attributes                map[string]string `json:"attributes"`
}

// secureContract mirrors the production client contract
// (deploy/production/realm.template.json): an enabled confidential BFF client
// using only the authorization-code flow with PKCE S256.
func (c previewClientRepresentation) secureContract() error {
	switch {
	case !c.Enabled:
		return errors.New("preview client must be enabled")
	case c.PublicClient:
		return errors.New("preview client must be confidential (publicClient=false)")
	case c.BearerOnly:
		return errors.New("preview client must not be bearer-only")
	case !c.StandardFlowEnabled:
		return errors.New("preview client must enable the authorization-code flow")
	case c.ImplicitFlowEnabled:
		return errors.New("preview client must not enable the implicit flow")
	case c.DirectAccessGrantsEnabled:
		return errors.New("preview client must not enable direct access grants (password grant)")
	case c.ServiceAccountsEnabled:
		return errors.New("preview client must not enable service accounts")
	case c.Attributes["pkce.code.challenge.method"] != "S256":
		return errors.New("preview client must require PKCE S256")
	}
	return nil
}

func exactly(values []string, want string) bool {
	return len(values) == 1 && values[0] == want
}
