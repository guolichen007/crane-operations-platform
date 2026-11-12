package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// keycloakAdmin is the Baobei account service's only path to Keycloak. It is bound
// at construction to one realm (derived from the approved issuer, never from a
// request) and authenticates with the realm's own service account (client
// credentials). It holds no master-realm credential; the realm's role and scope
// configuration (verified by tests/baobei/keycloak-realm-probe.py) limits it to
// user management inside that realm.
type keycloakAdmin struct {
	base, realm, clientID, secret, webClientID string
	http                                       *http.Client
	mu                                         sync.Mutex
	token                                      string
	expires                                    time.Time
	webClientUUID                              string
}

var errAccountProvider = errors.New("account provider unavailable")

// accountProblem is a Keycloak answer the user can act on (409 duplicate etc.).
type accountProblem struct{ status int }

func (e accountProblem) Error() string { return fmt.Sprintf("keycloak status %d", e.status) }

func newKeycloakAdmin(issuer, webClientID, clientID, secret string, client *http.Client) (*keycloakAdmin, error) {
	i := strings.Index(issuer, "/realms/")
	if i < 0 || clientID == "" || secret == "" {
		return nil, errors.New("account service configuration")
	}
	realm := issuer[i+len("/realms/"):]
	if realm == "" || strings.ContainsAny(realm, "/?#") || realm == "master" {
		return nil, errors.New("account service realm")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &keycloakAdmin{base: issuer[:i], realm: realm, clientID: clientID, secret: secret, webClientID: webClientID, http: client}, nil
}

func (k *keycloakAdmin) accessToken(ctx context.Context) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.token != "" && time.Now().Before(k.expires) {
		return k.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {k.clientID}, "client_secret": {k.secret}}
	req, _ := http.NewRequestWithContext(ctx, "POST", k.base+"/realms/"+url.PathEscape(k.realm)+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := k.http.Do(req)
	if err != nil {
		return "", errAccountProvider
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) != nil || body.AccessToken == "" {
		return "", errAccountProvider
	}
	k.token = body.AccessToken
	k.expires = time.Now().Add(time.Duration(max(body.ExpiresIn-30, 10)) * time.Second)
	return k.token, nil
}

// call performs one admin REST call under /admin/realms/<fixed realm>. path is
// always relative to that realm; it cannot address another realm.
func (k *keycloakAdmin) call(ctx context.Context, method, path string, in, out any) (int, http.Header, error) {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return 0, nil, errors.New("invalid admin path")
	}
	tok, err := k.accessToken(ctx)
	if err != nil {
		return 0, nil, err
	}
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, k.base+"/admin/realms/"+url.PathEscape(k.realm)+path, body)
	req.Header.Set("Authorization", "Bearer "+tok)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := k.http.Do(req)
	if err != nil {
		return 0, nil, errAccountProvider
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return resp.StatusCode, resp.Header, errAccountProvider
	}
	if out != nil && resp.StatusCode < 300 {
		if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
			return resp.StatusCode, resp.Header, errAccountProvider
		}
	}
	return resp.StatusCode, resp.Header, nil
}

type kcUser struct {
	ID              string              `json:"id,omitempty"`
	Username        string              `json:"username,omitempty"`
	Enabled         *bool               `json:"enabled,omitempty"`
	Attributes      map[string][]string `json:"attributes,omitempty"`
	RequiredActions []string            `json:"requiredActions,omitempty"`
}

type kcRole struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func boolp(v bool) *bool { return &v }

// findByUsername returns the exact-match user (Keycloak search is prefix based).
func (k *keycloakAdmin) findByUsername(ctx context.Context, username string) (*kcUser, error) {
	var users []kcUser
	s, _, err := k.call(ctx, "GET", "/users?exact=true&briefRepresentation=false&username="+url.QueryEscape(username), nil, &users)
	if err != nil || s != 200 {
		return nil, errAccountProvider
	}
	for i := range users {
		if users[i].Username == username {
			return &users[i], nil
		}
	}
	return nil, nil
}

func (k *keycloakAdmin) getUser(ctx context.Context, id string) (*kcUser, error) {
	var u kcUser
	s, _, err := k.call(ctx, "GET", "/users/"+url.PathEscape(id), nil, &u)
	if err != nil {
		return nil, err
	}
	if s == 404 {
		return nil, nil
	}
	if s != 200 {
		return nil, accountProblem{s}
	}
	return &u, nil
}

// createDisabled creates the user disabled; 409 means the username is taken.
func (k *keycloakAdmin) createDisabled(ctx context.Context, username, staffID string) (string, error) {
	s, h, err := k.call(ctx, "POST", "/users", kcUser{Username: username, Enabled: boolp(false),
		Attributes: map[string][]string{"baobei_staff_id": {staffID}}}, nil)
	if err != nil {
		return "", err
	}
	if s != 201 {
		return "", accountProblem{s}
	}
	loc := h.Get("Location")
	id := loc[strings.LastIndex(loc, "/")+1:]
	if !validIdentityUUID(id) {
		return "", errAccountProvider
	}
	return id, nil
}

func (k *keycloakAdmin) update(ctx context.Context, id string, u kcUser) error {
	s, _, err := k.call(ctx, "PUT", "/users/"+url.PathEscape(id), u, nil)
	if err != nil {
		return err
	}
	if s != 204 {
		return accountProblem{s}
	}
	return nil
}

// setTemporaryPassword: Keycloak marks the user with UPDATE_PASSWORD.
func (k *keycloakAdmin) setTemporaryPassword(ctx context.Context, id, password string) error {
	s, _, err := k.call(ctx, "PUT", "/users/"+url.PathEscape(id)+"/reset-password",
		map[string]any{"type": "password", "value": password, "temporary": true}, nil)
	if err != nil {
		return err
	}
	if s != 204 {
		return accountProblem{s}
	}
	return nil
}

func (k *keycloakAdmin) logout(ctx context.Context, id string) error {
	s, _, err := k.call(ctx, "POST", "/users/"+url.PathEscape(id)+"/logout", nil, nil)
	if err == nil && s != 204 {
		err = accountProblem{s}
	}
	return err
}

func (k *keycloakAdmin) delete(ctx context.Context, id string) error {
	s, _, err := k.call(ctx, "DELETE", "/users/"+url.PathEscape(id), nil, nil)
	if err == nil && s != 204 && s != 404 {
		err = accountProblem{s}
	}
	return err
}

func (k *keycloakAdmin) webClient(ctx context.Context) (string, error) {
	k.mu.Lock()
	cached := k.webClientUUID
	k.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	var clients []struct {
		ID       string `json:"id"`
		ClientID string `json:"clientId"`
	}
	s, _, err := k.call(ctx, "GET", "/clients?clientId="+url.QueryEscape(k.webClientID), nil, &clients)
	if err != nil || s != 200 || len(clients) != 1 || clients[0].ClientID != k.webClientID {
		return "", errAccountProvider
	}
	k.mu.Lock()
	k.webClientUUID = clients[0].ID
	k.mu.Unlock()
	return clients[0].ID, nil
}

// setExactRole makes the user hold exactly one Baobei client role (the four
// baobei_* roles of the web client only; other roles are never touched).
func (k *keycloakAdmin) setExactRole(ctx context.Context, id, role string) error {
	if !contains([]string{salesRole, managerRole, directorRole, adminRole}, role) {
		return errors.New("role not allowed")
	}
	web, err := k.webClient(ctx)
	if err != nil {
		return err
	}
	base := "/users/" + url.PathEscape(id) + "/role-mappings/clients/" + url.PathEscape(web)
	var current []kcRole
	if s, _, err := k.call(ctx, "GET", base, nil, &current); err != nil || s != 200 {
		return errAccountProvider
	}
	var remove []kcRole
	has := false
	for _, r := range current {
		if r.Name == role {
			has = true
		} else if strings.HasPrefix(r.Name, "baobei_") {
			remove = append(remove, r)
		}
	}
	if len(remove) > 0 {
		if s, _, err := k.call(ctx, "DELETE", base, remove, nil); err != nil || s != 204 {
			return errAccountProvider
		}
	}
	if !has {
		var target kcRole
		if s, _, err := k.call(ctx, "GET", "/clients/"+url.PathEscape(web)+"/roles/"+url.PathEscape(role), nil, &target); err != nil || s != 200 {
			return errAccountProvider
		}
		if s, _, err := k.call(ctx, "POST", base, []kcRole{target}, nil); err != nil || s != 204 {
			return errAccountProvider
		}
	}
	return nil
}

// roles returns the user's Baobei client roles (for verification).
func (k *keycloakAdmin) roles(ctx context.Context, id string) ([]string, error) {
	web, err := k.webClient(ctx)
	if err != nil {
		return nil, err
	}
	var current []kcRole
	if s, _, err := k.call(ctx, "GET", "/users/"+url.PathEscape(id)+"/role-mappings/clients/"+url.PathEscape(web), nil, &current); err != nil || s != 200 {
		return nil, errAccountProvider
	}
	names := make([]string, 0, len(current))
	for _, r := range current {
		names = append(names, r.Name)
	}
	return names, nil
}
