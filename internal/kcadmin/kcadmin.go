// Package kcadmin is the cloud-api's only path to Keycloak user administration.
//
// Security model (P1 personnel management, 2026-11-18):
//   - bound at construction to ONE realm derived from the configured OIDC issuer; the realm can
//     never come from a request and "master" is refused;
//   - authenticates with that realm's own confidential service account (client credentials). No
//     master-realm / bootstrap credential is ever used by the application;
//   - the realm configuration (tools/provision-keycloak) limits the service account to
//     realm-management view-users/query-users/manage-users (and the client-scope mapping that
//     carries them), so it cannot change the realm, its clients or other realms; Keycloak itself
//     enforces that, and this package additionally refuses every path outside the user endpoints
//     it needs;
//   - users carrying the managed-by attribute are the only ones this package mutates; service
//     account users and accounts it did not create are refused (ErrNotManaged);
//   - no token, password or upstream body is logged or returned in errors.
package kcadmin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ManagedAttribute marks Keycloak users that the Crane personnel service created and may manage.
const ManagedAttribute = "crane_managed"

var (
	ErrUnavailable = errors.New("identity provider unavailable")
	ErrConflict    = errors.New("identity provider username already exists")
	ErrNotFound    = errors.New("identity provider user not found")
	ErrNotManaged  = errors.New("identity provider user is not managed by Crane")
	ErrRejected    = errors.New("identity provider rejected the request")
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidSubject reports whether s looks like a Keycloak user id (UUID).
func ValidSubject(s string) bool { return uuidPattern.MatchString(s) }

// Client is the realm-bound admin client.
type Client struct {
	base, realm, clientID string
	secret                []byte
	http                  *http.Client
	mu                    sync.Mutex
	token                 string
	expires               time.Time
}

// New derives the base URL and realm from the issuer (".../realms/<realm>"). base may override the
// network address used to reach Keycloak (an internal URL such as http://keycloak:8080) while the
// realm still comes from the issuer.
func New(issuer, internalBase, clientID string, secret []byte, client *http.Client) (*Client, error) {
	u, err := url.Parse(issuer)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid account service issuer")
	}
	i := strings.Index(u.Path, "/realms/")
	if i < 0 {
		return nil, errors.New("invalid account service issuer")
	}
	realm := u.Path[i+len("/realms/"):]
	if realm == "" || realm == "master" || strings.ContainsAny(realm, "/?#%") {
		return nil, errors.New("account service realm refused")
	}
	if clientID == "" || len(secret) < 32 {
		return nil, errors.New("account service credential required")
	}
	base := u.Scheme + "://" + u.Host + u.Path[:i]
	if internalBase != "" {
		b, err := url.Parse(internalBase)
		if err != nil || b.User != nil || b.RawQuery != "" || b.Fragment != "" || b.Path != "" ||
			!(b.Scheme == "https" || b.Scheme == "http" && (b.Hostname() == "keycloak" || b.Hostname() == "127.0.0.1" || b.Hostname() == "localhost")) {
			return nil, errors.New("invalid internal Keycloak address")
		}
		base = internalBase
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: base, realm: realm, clientID: clientID, secret: secret, http: &c}, nil
}

// Realm is the fixed realm name (for audit/diagnostics only).
func (c *Client) Realm() string { return c.realm }

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.clientID}, "client_secret": {string(c.secret)}}
	r, err := http.NewRequestWithContext(ctx, "POST", c.base+"/realms/"+url.PathEscape(c.realm)+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", ErrUnavailable
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.http.Do(r)
	if err != nil {
		return "", ErrUnavailable
	}
	defer response.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body) != nil || body.AccessToken == "" {
		return "", ErrUnavailable
	}
	c.token = body.AccessToken
	c.expires = time.Now().Add(time.Duration(max(body.ExpiresIn-30, 10)) * time.Second)
	return c.token, nil
}

func (c *Client) dropToken() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
}

// allowedPath is the closed list of admin endpoints this package may call (relative to the realm).
var allowedPath = regexp.MustCompile(`^/users(\?[^/]*|/[0-9a-f-]{36}(/(reset-password|logout))?)?$`)

func (c *Client) call(ctx context.Context, method, path string, in, out any) (int, http.Header, error) {
	if !allowedPath.MatchString(path) {
		return 0, nil, fmt.Errorf("%w: admin path not allowed", ErrRejected)
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.accessToken(ctx)
		if err != nil {
			return 0, nil, err
		}
		var body io.Reader
		if in != nil {
			b, err := json.Marshal(in)
			if err != nil {
				return 0, nil, err
			}
			body = bytes.NewReader(b)
		}
		r, err := http.NewRequestWithContext(ctx, method, c.base+"/admin/realms/"+url.PathEscape(c.realm)+path, body)
		if err != nil {
			return 0, nil, ErrUnavailable
		}
		r.Header.Set("Authorization", "Bearer "+token)
		if in != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		response, err := c.http.Do(r)
		if err != nil {
			return 0, nil, ErrUnavailable
		}
		if response.StatusCode == 401 && attempt == 0 {
			response.Body.Close()
			c.dropToken()
			continue
		}
		defer response.Body.Close()
		if response.StatusCode >= 500 || response.StatusCode == 401 || response.StatusCode == 403 {
			return response.StatusCode, response.Header, ErrUnavailable
		}
		if out != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
			if err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(out); err != nil {
				return response.StatusCode, response.Header, ErrUnavailable
			}
		}
		return response.StatusCode, response.Header, nil
	}
	return 0, nil, ErrUnavailable
}

// User is the subset of the Keycloak user representation the personnel service uses.
type User struct {
	ID              string              `json:"id,omitempty"`
	Username        string              `json:"username,omitempty"`
	Enabled         *bool               `json:"enabled,omitempty"`
	FirstName       string              `json:"firstName,omitempty"`
	LastName        string              `json:"lastName,omitempty"`
	Email           string              `json:"email,omitempty"`
	EmailVerified   *bool               `json:"emailVerified,omitempty"`
	Attributes      map[string][]string `json:"attributes,omitempty"`
	RequiredActions []string            `json:"requiredActions,omitempty"`
	ServiceAccount  string              `json:"serviceAccountClientId,omitempty"`
}

// Managed reports whether the user was created by (and belongs to) the Crane personnel service.
func (u *User) Managed() bool {
	if u == nil || u.ServiceAccount != "" || strings.HasPrefix(u.Username, "service-account-") {
		return false
	}
	v := u.Attributes[ManagedAttribute]
	return len(v) == 1 && v[0] == "true"
}

// Profile is what Crane writes to Keycloak for a person (no password).
type Profile struct {
	Username, DisplayName, Email, LocalUserID string
}

func boolp(v bool) *bool { return &v }

func randomHex() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func decodeSegment(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

func (p Profile) representation() User {
	u := User{Username: p.Username, FirstName: p.DisplayName, LastName: "", Attributes: map[string][]string{
		ManagedAttribute: {"true"}, "crane_user_id": {p.LocalUserID},
	}}
	if p.Email != "" {
		u.Email, u.EmailVerified = p.Email, boolp(false)
	}
	return u
}

// FindByUsername returns the exact-match user or nil (Keycloak's search is prefix based).
func (c *Client) FindByUsername(ctx context.Context, username string) (*User, error) {
	var users []User
	status, _, err := c.call(ctx, "GET", "/users?exact=true&briefRepresentation=false&username="+url.QueryEscape(username), nil, &users)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, ErrUnavailable
	}
	for i := range users {
		if users[i].Username == username {
			return &users[i], nil
		}
	}
	return nil, nil
}

// Get loads one user by Keycloak id; nil when it does not exist.
func (c *Client) Get(ctx context.Context, id string) (*User, error) {
	if !ValidSubject(id) {
		return nil, ErrNotFound
	}
	var u User
	status, _, err := c.call(ctx, "GET", "/users/"+id, nil, &u)
	if err != nil {
		return nil, err
	}
	if status == 404 {
		return nil, nil
	}
	if status != 200 {
		return nil, ErrUnavailable
	}
	return &u, nil
}

// CreateDisabled creates a DISABLED managed user and returns its id. 409 = username taken.
func (c *Client) CreateDisabled(ctx context.Context, p Profile) (string, error) {
	u := p.representation()
	u.Enabled = boolp(false)
	status, header, err := c.call(ctx, "POST", "/users", u, nil)
	if err != nil {
		return "", err
	}
	switch status {
	case 201:
	case 409:
		return "", ErrConflict
	default:
		return "", fmt.Errorf("%w: create status %d", ErrRejected, status)
	}
	location := header.Get("Location")
	id := location[strings.LastIndex(location, "/")+1:]
	if !ValidSubject(id) {
		return "", ErrUnavailable
	}
	return id, nil
}

// managed loads the user and refuses anything Crane does not own.
func (c *Client) managed(ctx context.Context, id string) (*User, error) {
	u, err := c.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrNotFound
	}
	if !u.Managed() {
		return nil, ErrNotManaged
	}
	return u, nil
}

// UpdateProfile writes display name / email for a managed user (username is immutable).
func (c *Client) UpdateProfile(ctx context.Context, id string, p Profile) error {
	u, err := c.managed(ctx, id)
	if err != nil {
		return err
	}
	next := p.representation()
	next.Username = u.Username
	next.Enabled = u.Enabled
	next.RequiredActions = u.RequiredActions
	for k, v := range u.Attributes {
		if _, set := next.Attributes[k]; !set {
			next.Attributes[k] = v
		}
	}
	if p.Email == "" {
		next.Email, next.EmailVerified = "", nil
	}
	return c.put(ctx, id, next)
}

func (c *Client) put(ctx context.Context, id string, u User) error {
	status, _, err := c.call(ctx, "PUT", "/users/"+id, u, nil)
	if err != nil {
		return err
	}
	if status == 404 {
		return ErrNotFound
	}
	if status != 204 {
		return fmt.Errorf("%w: update status %d", ErrRejected, status)
	}
	return nil
}

// SetEnabled enables or disables a managed user.
func (c *Client) SetEnabled(ctx context.Context, id string, enabled bool) error {
	u, err := c.managed(ctx, id)
	if err != nil {
		return err
	}
	u.Enabled = boolp(enabled)
	u.ServiceAccount = ""
	return c.put(ctx, id, *u)
}

// SetTemporaryPassword sets a one-time password; Keycloak then requires UPDATE_PASSWORD at the
// next login. The password is sent once and never stored or logged.
func (c *Client) SetTemporaryPassword(ctx context.Context, id string, password []byte) error {
	if _, err := c.managed(ctx, id); err != nil {
		return err
	}
	status, _, err := c.call(ctx, "PUT", "/users/"+id+"/reset-password", map[string]any{"type": "password", "value": string(password), "temporary": true}, nil)
	if err != nil {
		return err
	}
	if status == 400 {
		return fmt.Errorf("%w: password policy", ErrRejected)
	}
	if status != 204 {
		return fmt.Errorf("%w: reset status %d", ErrRejected, status)
	}
	return nil
}

// Logout removes every Keycloak (SSO) session of a managed user.
func (c *Client) Logout(ctx context.Context, id string) error {
	if _, err := c.managed(ctx, id); err != nil {
		return err
	}
	status, _, err := c.call(ctx, "POST", "/users/"+id+"/logout", nil, nil)
	if err != nil {
		return err
	}
	if status != 204 {
		return fmt.Errorf("%w: logout status %d", ErrRejected, status)
	}
	return nil
}

// Adopt marks an existing human user of the realm as Crane-managed (host recovery tool only:
// e.g. the owner's pre-existing account becoming the first system administrator). Service-account
// users are refused.
func (c *Client) Adopt(ctx context.Context, id, localUserID string) (*User, error) {
	u, err := c.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrNotFound
	}
	if u.ServiceAccount != "" || strings.HasPrefix(u.Username, "service-account-") {
		return nil, ErrNotManaged
	}
	if u.Attributes == nil {
		u.Attributes = map[string][]string{}
	}
	if owner := u.Attributes["crane_user_id"]; len(owner) == 1 && owner[0] != localUserID {
		return nil, ErrNotManaged
	}
	u.Attributes[ManagedAttribute] = []string{"true"}
	u.Attributes["crane_user_id"] = []string{localUserID}
	if err = c.put(ctx, id, *u); err != nil {
		return nil, err
	}
	return u, nil
}

// DeleteUnbound removes a managed user that was never bound locally (provision rollback).
func (c *Client) DeleteUnbound(ctx context.Context, id string) error {
	u, err := c.Get(ctx, id)
	if err != nil {
		return err
	}
	if u == nil {
		return nil
	}
	if !u.Managed() || (u.Enabled != nil && *u.Enabled) {
		return ErrNotManaged
	}
	status, _, err := c.call(ctx, "DELETE", "/users/"+id, nil, nil)
	if err != nil {
		return err
	}
	if status != 204 && status != 404 {
		return fmt.Errorf("%w: delete status %d", ErrRejected, status)
	}
	return nil
}
