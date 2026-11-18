package kcadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Check is one least-privilege probe result.
type Check struct {
	Name   string
	Pass   bool
	Detail string
}

// ProbeLeastPrivilege runs against a REAL Keycloak with the account service credential and proves
// what the service account can and cannot do. Every "must be refused" probe sends a request that
// would change the realm/clients/other realms if it were allowed, so a PASS means Keycloak itself
// refused it (403/401/404), not this package. Used by the local real-Keycloak run and the CI
// fixture (tests/integration/a1fixture probe-account-service).
// Escalation (optional) carries the realm-management client UUID and the full representations of
// realm-admin / manage-realm / manage-clients, looked up by the caller with an administrator
// credential, so the self-escalation probe sends VALID role mappings (a refusal then proves a
// permission check, not a malformed request). After the attempts a fresh token must still be
// unable to create clients.
type Escalation struct {
	RealmManagementID string
	Roles             []map[string]any
}

func ProbeLeastPrivilege(ctx context.Context, base, realm, clientID string, secret []byte, httpClient *http.Client, esc *Escalation) []Check {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	c, err := New(base+"/realms/"+realm, "", clientID, secret, httpClient)
	if err != nil {
		return []Check{{"client configuration", false, err.Error()}}
	}
	out := []Check{}
	add := func(name string, pass bool, detail string) { out = append(out, Check{name, pass, detail}) }
	token, err := c.accessToken(ctx)
	add("service account obtains a token (client credentials)", err == nil, "")
	if err != nil {
		return out
	}
	raw := func(method, path string, body any) int {
		var r *http.Request
		if body != nil {
			b, _ := json.Marshal(body)
			r, _ = http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(b))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r, _ = http.NewRequestWithContext(ctx, method, base+path, nil)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		resp, err := httpClient.Do(r)
		if err != nil {
			return 0
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	refused := func(s int) bool { return s == 401 || s == 403 || s == 404 }
	rp := "/admin/realms/" + url.PathEscape(realm)

	// Allowed: the user lifecycle used by the personnel service.
	username := "probe-" + strings.ReplaceAll(strings.ToLower(randomHex()), "_", "")
	id, err := c.CreateDisabled(ctx, Profile{Username: username, DisplayName: "Probe", LocalUserID: username})
	add("create disabled managed user", err == nil, errString(err))
	if err == nil {
		_, dup := c.CreateDisabled(ctx, Profile{Username: username, DisplayName: "Probe", LocalUserID: username})
		add("duplicate username refused with conflict (409)", dup == ErrConflict, errString(dup))
		u, _ := c.Get(ctx, id)
		add("managed attribute persisted by the user profile", u != nil && u.Managed() && u.Enabled != nil && !*u.Enabled, "")
		e := c.SetTemporaryPassword(ctx, id, []byte("Probe-"+randomHex()+"a1A"))
		add("set temporary password", e == nil, errString(e))
		u, _ = c.Get(ctx, id)
		add("temporary password requires UPDATE_PASSWORD", u != nil && len(u.RequiredActions) == 1 && u.RequiredActions[0] == "UPDATE_PASSWORD", "")
		add("enable", c.SetEnabled(ctx, id, true) == nil, "")
		add("logout all sessions", c.Logout(ctx, id) == nil, "")
		add("disable", c.SetEnabled(ctx, id, false) == nil, "")
		add("delete unbound disabled user", c.DeleteUnbound(ctx, id) == nil, "")
	}

	// Refused by Keycloak: realm, clients, roles, other realms, privilege escalation.
	add("cannot change realm settings", refused(raw("PUT", rp, map[string]any{"registrationAllowed": true})), "")
	add("cannot create clients", refused(raw("POST", rp+"/clients", map[string]any{"clientId": "probe-" + randomHex(), "publicClient": true})), "")
	var clients []struct {
		ID       string `json:"id"`
		ClientID string `json:"clientId"`
	}
	_ = getJSON(ctx, httpClient, token, base+rp+"/clients?clientId=crane-bff", &clients)
	if len(clients) == 1 {
		add("cannot modify the BFF OIDC client", refused(raw("PUT", rp+"/clients/"+clients[0].ID, map[string]any{"clientId": "crane-bff", "directAccessGrantsEnabled": true})), "")
	} else {
		add("BFF client not visible to the account service (cannot modify it)", true, "")
	}
	add("cannot create realm roles", refused(raw("POST", rp+"/roles", map[string]any{"name": "probe-" + randomHex()})), "")
	add("cannot read the master realm", refused(raw("GET", "/admin/realms/master/users", nil)), "")
	add("cannot create realms", refused(raw("POST", "/admin/realms", map[string]any{"realm": "probe-" + randomHex()})), "")
	add("cannot list or change identity providers", refused(raw("POST", rp+"/identity-provider/instances", map[string]any{"alias": "probe", "providerId": "oidc"})), "")

	// The service account's own user must not be manageable through this package.
	var self struct {
		Sub string `json:"sub"`
	}
	parts := strings.Split(token, ".")
	if len(parts) == 3 {
		if b, e := decodeSegment(parts[1]); e == nil {
			_ = json.Unmarshal(b, &self)
		}
	}
	if self.Sub != "" {
		e := c.SetEnabled(ctx, self.Sub, true)
		add("package refuses to manage the service-account user", e == ErrNotManaged, errString(e))
		if esc != nil && esc.RealmManagementID != "" && len(esc.Roles) > 0 {
			for _, r := range esc.Roles {
				st := raw("POST", rp+"/users/"+self.Sub+"/role-mappings/clients/"+esc.RealmManagementID, []map[string]any{r})
				add(fmt.Sprintf("cannot grant itself %v", r["name"]), st == 403, fmt.Sprint(st))
			}
			// Indirect escalation: create a user and try to make IT an administrator.
			if proxy, err := c.CreateDisabled(ctx, Profile{Username: "probe-esc-" + randomHex(), DisplayName: "Probe", LocalUserID: "probe"}); err == nil {
				for _, r := range esc.Roles {
					st := raw("POST", rp+"/users/"+proxy+"/role-mappings/clients/"+esc.RealmManagementID, []map[string]any{r})
					add(fmt.Sprintf("cannot grant %v to a user it created", r["name"]), st == 403, fmt.Sprint(st))
				}
				_ = c.DeleteUnbound(ctx, proxy)
			} else {
				add("indirect escalation probe user", false, errString(err))
			}
			c.dropToken()
			fresh, err := c.accessToken(ctx)
			if err == nil {
				token = fresh
			}
			add("after escalation attempts a fresh token still cannot create clients", err == nil && refused(raw("POST", rp+"/clients", map[string]any{"clientId": "probe-" + randomHex()})), "")
		} else {
			add("self-escalation probe needs administrator-supplied role representations", false, "escalation data missing")
		}
	}
	return out
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func getJSON(ctx context.Context, h *http.Client, token, u string, out any) error {
	r, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	resp, err := h.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ErrRejected
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
