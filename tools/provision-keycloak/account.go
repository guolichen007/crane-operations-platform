package main

// P1 personnel: the realm-scoped account service client for cloud-api.
//
// Creates / converges (idempotently) in realm "crane":
//   - confidential client crane-account-service: service account only (no browser flow, no
//     direct grants, no implicit), fullScopeAllowed=false;
//   - its service-account user holds exactly realm-management view-users, query-users,
//     manage-users (nothing else — no manage-realm, manage-clients, realm-admin, impersonation);
//   - the client's scope mapping carries exactly those three roles (fullScopeAllowed=false means a
//     token only contains roles that are ALSO in the scope mapping);
//   - the realm user profile declares the admin-only attributes crane_managed / crane_user_id
//     (Keycloak 26 silently drops undeclared attributes).
//
// The secret is read from ACCOUNT_SERVICE_SECRET_FILE (written by the operator, never printed).
// Any extra role found on the service account is REMOVED, so a manual widening is reverted.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

const accountClientID = "crane-account-service"

var accountRoles = []string{"manage-users", "query-users", "view-users"}

type role struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContainerID string `json:"containerId,omitempty"`
	ClientRole  bool   `json:"clientRole,omitempty"`
}

func provisionAccountService(ctx context.Context, c client, accountOnly bool) error {
	path := os.Getenv("ACCOUNT_SERVICE_SECRET_FILE")
	if path == "" {
		return nil // optional: personnel account operations stay disabled until configured
	}
	secret, err := platform.PrivateSecret(path, 32)
	if err != nil {
		return err
	}
	body := map[string]any{
		"clientId": accountClientID, "enabled": true, "protocol": "openid-connect", "publicClient": false, "secret": string(secret),
		"serviceAccountsEnabled": true, "standardFlowEnabled": false, "implicitFlowEnabled": false, "directAccessGrantsEnabled": false,
		"frontchannelLogout": false, "fullScopeAllowed": false, "redirectUris": []string{}, "webOrigins": []string{},
		"attributes": map[string]string{"oauth2.device.authorization.grant.enabled": "false", "oidc.ciba.grant.enabled": "false"},
	}
	var existing []struct {
		ID string `json:"id"`
	}
	status, err := c.request(ctx, "GET", "/admin/realms/crane/clients?clientId="+accountClientID, nil, &existing)
	if err != nil || status != 200 || len(existing) > 1 {
		return errors.New("account client lookup rejected")
	}
	if len(existing) == 0 {
		if status, err = c.request(ctx, "POST", "/admin/realms/crane/clients", body, nil); err != nil || status != 201 {
			return errors.New("account client creation rejected")
		}
		if status, err = c.request(ctx, "GET", "/admin/realms/crane/clients?clientId="+accountClientID, nil, &existing); err != nil || status != 200 || len(existing) != 1 {
			return errors.New("account client lookup rejected")
		}
	} else if status, err = c.request(ctx, "PUT", "/admin/realms/crane/clients/"+url.PathEscape(existing[0].ID), body, nil); err != nil || status != 204 {
		return errors.New("account client configuration rejected")
	}
	clientUUID := existing[0].ID
	var management []struct {
		ID string `json:"id"`
	}
	if status, err = c.request(ctx, "GET", "/admin/realms/crane/clients?clientId=realm-management", nil, &management); err != nil || status != 200 || len(management) != 1 {
		return errors.New("realm-management lookup rejected")
	}
	rm := management[0].ID
	var all []role
	if status, err = c.request(ctx, "GET", "/admin/realms/crane/clients/"+url.PathEscape(rm)+"/roles", nil, &all); err != nil || status != 200 {
		return errors.New("realm-management roles rejected")
	}
	want := []role{}
	for _, r := range all {
		for _, name := range accountRoles {
			if r.Name == name {
				want = append(want, role{ID: r.ID, Name: r.Name})
			}
		}
	}
	if len(want) != len(accountRoles) {
		return errors.New("realm-management roles incomplete")
	}
	var sa struct {
		ID string `json:"id"`
	}
	if status, err = c.request(ctx, "GET", "/admin/realms/crane/clients/"+url.PathEscape(clientUUID)+"/service-account-user", nil, &sa); err != nil || status != 200 || sa.ID == "" {
		return errors.New("service account user rejected")
	}
	converge := func(base string) error {
		var current []role
		if status, err := c.request(ctx, "GET", base, nil, &current); err != nil || status != 200 {
			return errors.New("role mapping lookup rejected")
		}
		extra, missing := []role{}, []role{}
		has := map[string]bool{}
		for _, r := range current {
			has[r.Name] = true
			keep := false
			for _, w := range accountRoles {
				keep = keep || r.Name == w
			}
			if !keep {
				extra = append(extra, role{ID: r.ID, Name: r.Name})
			}
		}
		for _, w := range want {
			if !has[w.Name] {
				missing = append(missing, w)
			}
		}
		if len(extra) > 0 {
			if status, err := c.request(ctx, "DELETE", base, extra, nil); err != nil || status != 204 {
				return errors.New("role mapping narrowing rejected")
			}
		}
		if len(missing) > 0 {
			if status, err := c.request(ctx, "POST", base, missing, nil); err != nil || status != 204 {
				return errors.New("role mapping rejected")
			}
		}
		return nil
	}
	if err = converge("/admin/realms/crane/users/" + url.PathEscape(sa.ID) + "/role-mappings/clients/" + url.PathEscape(rm)); err != nil {
		return err
	}
	if err = converge("/admin/realms/crane/clients/" + url.PathEscape(clientUUID) + "/scope-mappings/clients/" + url.PathEscape(rm)); err != nil {
		return err
	}
	return declareProfileAttributes(ctx, c, accountOnly)
}

// declareProfileAttributes adds admin-only crane_managed / crane_user_id to the user profile.
func declareProfileAttributes(ctx context.Context, c client, addOnly bool) error {
	var profile map[string]any
	status, err := c.request(ctx, "GET", "/admin/realms/crane/users/profile", nil, &profile)
	if err != nil || status != 200 {
		return errors.New("user profile lookup rejected")
	}
	attrs, _ := profile["attributes"].([]any)
	present := map[string]bool{}
	changed := false
	for _, a := range attrs {
		if m, ok := a.(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				present[n] = true
				// Personnel are opened by administrators with name + username only: email / first /
				// last name must not be required at first login (no extra "update profile" step), and
				// people may not edit them themselves (the administrator owns the record).
				if !addOnly && (n == "email" || n == "firstName" || n == "lastName") {
					if _, req := m["required"]; req {
						delete(m, "required")
						changed = true
					}
					perms, _ := m["permissions"].(map[string]any)
					if edit, _ := perms["edit"].([]any); len(edit) != 1 || edit[0] != "admin" {
						m["permissions"] = map[string]any{"view": []string{"admin", "user"}, "edit": []string{"admin"}}
						changed = true
					}
				}
			}
		}
	}
	names := []string{"crane_managed", "crane_user_id"}
	sort.Strings(names)
	for _, n := range names {
		if !present[n] {
			attrs = append(attrs, map[string]any{"name": n, "displayName": n, "multivalued": false,
				"permissions": map[string]any{"view": []string{"admin"}, "edit": []string{"admin"}}, "validations": map[string]any{}})
			changed = true
		}
	}
	if !changed {
		return nil
	}
	profile["attributes"] = attrs
	if status, err = c.request(ctx, "PUT", "/admin/realms/crane/users/profile", profile, nil); err != nil || status != 200 {
		return errors.New("user profile update rejected")
	}
	return nil
}

// protectedDigest hashes the Keycloak objects the account-service-only mode must never change:
// realm settings (incl. password policy, brute force, required actions, SSO lifetimes), the
// crane-bff client, the realm's required-action list, and the set of non-service users with their
// enabled flag and credential types. Volatile fields (attribute order) are normalized.
func protectedDigest(ctx context.Context, c client) (map[string]string, error) {
	out := map[string]string{}
	fetch := func(path string) (any, error) {
		var v any
		status, err := c.request(ctx, "GET", path, nil, &v)
		if err != nil || status != 200 {
			return nil, errors.New("protected object lookup rejected")
		}
		return v, nil
	}
	sum := func(v any) string {
		b, _ := json.Marshal(v) // encoding/json sorts map keys: stable
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:])
	}
	realm, err := fetch("/admin/realms/crane")
	if err != nil {
		return nil, err
	}
	out["realm"] = sum(realm)
	var bff []map[string]any
	if status, err := c.request(ctx, "GET", "/admin/realms/crane/clients?clientId=crane-bff", nil, &bff); err != nil || status != 200 || len(bff) != 1 {
		return nil, errors.New("crane-bff lookup rejected")
	}
	delete(bff[0], "secret") // the secret is not returned in full anyway; compared by presence below
	out["client:crane-bff"] = sum(bff[0])
	actions, err := fetch("/admin/realms/crane/authentication/required-actions")
	if err != nil {
		return nil, err
	}
	out["required-actions"] = sum(actions)
	var users []map[string]any
	if status, err := c.request(ctx, "GET", "/admin/realms/crane/users?briefRepresentation=true&max=10000", nil, &users); err != nil || status != 200 {
		return nil, errors.New("user lookup rejected")
	}
	type u struct {
		ID, Username string
		Enabled      bool
		Creds        []string
	}
	list := []u{}
	for _, x := range users {
		name, _ := x["username"].(string)
		if strings.HasPrefix(name, "service-account-") {
			continue
		}
		id, _ := x["id"].(string)
		en, _ := x["enabled"].(bool)
		var creds []map[string]any
		if status, err := c.request(ctx, "GET", "/admin/realms/crane/users/"+url.PathEscape(id)+"/credentials", nil, &creds); err != nil || status != 200 {
			return nil, errors.New("credential lookup rejected")
		}
		types := []string{}
		for _, cr := range creds {
			t, _ := cr["type"].(string)
			created, _ := cr["createdDate"].(float64)
			types = append(types, t+"@"+strconv.FormatInt(int64(created), 10))
		}
		sort.Strings(types)
		list = append(list, u{id, name, en, types})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	out["users+credentials"] = sum(list)
	return out, nil
}
