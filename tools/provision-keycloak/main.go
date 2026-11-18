// provision-keycloak is a one-shot host-administrator tool. It never runs in the BFF request path.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

type client struct {
	base, token string
	http        *http.Client
}

func endpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return false
	}
	return u.Scheme == "https" && u.Host != "" || u.Scheme == "http" && (u.Hostname() == "keycloak" || u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")
}

func (c client) request(ctx context.Context, method, path string, body any, output any) (int, error) {
	var data io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		data = bytes.NewReader(encoded)
	}
	r, err := http.NewRequestWithContext(ctx, method, c.base+path, data)
	if err != nil {
		return 0, errors.New("invalid Keycloak request")
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(r)
	if err != nil {
		return 0, errors.New("keycloak administration unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 && output != nil {
		if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
			return 0, errors.New("invalid Keycloak response")
		}
	}
	// Never include upstream response bodies: they can contain credentials or reflected input.
	return response.StatusCode, nil
}

func adminClient(ctx context.Context, base string) (client, error) {
	admin, err := platform.PrivateSecret(os.Getenv("KEYCLOAK_BOOTSTRAP_PASSWORD_FILE"), 32)
	if err != nil {
		return client{}, err
	}
	h := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	form := url.Values{"grant_type": {"password"}, "client_id": {"admin-cli"}, "username": {"bootstrap-crane"}, "password": {string(admin)}}
	r, err := http.NewRequestWithContext(ctx, "POST", base+"/realms/master/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return client{}, err
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := h.Do(r)
	if err != nil {
		return client{}, errors.New("keycloak admin authentication unavailable")
	}
	var authentication struct {
		AccessToken string `json:"access_token"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&authentication)
	_ = response.Body.Close()
	if response.StatusCode != 200 || decodeErr != nil || authentication.AccessToken == "" {
		return client{}, errors.New("keycloak admin authentication rejected")
	}
	return client{base: base, token: authentication.AccessToken, http: h}, nil
}

func execute(ctx context.Context) error {
	base, public := os.Getenv("KEYCLOAK_ADMIN_URL"), os.Getenv("PUBLIC_URL")
	if !endpoint(base) || !platform.SecureURL(public, false) {
		return errors.New("explicit admin endpoint and public HTTPS origin required")
	}
	secret, err := platform.PrivateSecret(os.Getenv("OIDC_CLIENT_SECRET_FILE"), 32)
	if err != nil {
		return err
	}
	c, err := adminClient(ctx, base)
	if err != nil {
		return err
	}
	template, err := os.ReadFile(os.Getenv("KEYCLOAK_REALM_TEMPLATE"))
	if err != nil {
		return errors.New("realm template unavailable")
	}
	var realm map[string]any
	if json.Unmarshal([]byte(strings.ReplaceAll(string(template), "${CRANE_PUBLIC_URL}", public)), &realm) != nil || realm["realm"] != "crane" {
		return errors.New("invalid fixed crane realm template")
	}
	clients, ok := realm["clients"].([]any)
	if !ok || len(clients) != 1 {
		return errors.New("one BFF client required")
	}
	bff, ok := clients[0].(map[string]any)
	if !ok || bff["clientId"] != "crane-bff" {
		return errors.New("invalid BFF client")
	}
	bff["secret"] = string(secret)
	status, err := c.request(ctx, "GET", "/admin/realms/crane", nil, nil)
	if err != nil {
		return err
	}
	if status == 404 {
		status, err = c.request(ctx, "POST", "/admin/realms", realm, nil)
		if err != nil || status != 201 {
			return errors.New("realm creation rejected")
		}
	} else if status == 200 {
		delete(realm, "clients")
		status, err = c.request(ctx, "PUT", "/admin/realms/crane", realm, nil)
		if err != nil || status != 204 {
			return errors.New("realm configuration rejected")
		}
		var existing []struct {
			ID string `json:"id"`
		}
		status, err = c.request(ctx, "GET", "/admin/realms/crane/clients?clientId=crane-bff", nil, &existing)
		if err != nil || status != 200 || len(existing) > 1 {
			return errors.New("BFF lookup rejected")
		}
		path, method := "/admin/realms/crane/clients", "POST"
		if len(existing) == 1 {
			path += "/" + url.PathEscape(existing[0].ID)
			method = "PUT"
		}
		status, err = c.request(ctx, method, path, bff, nil)
		if err != nil || status != 201 && status != 204 {
			return errors.New("BFF configuration rejected")
		}
	} else {
		return errors.New("realm lookup rejected")
	}
	return provisionAccountService(ctx, c, false)
}

// executeAccountServiceOnly is the deployment mode for an EXISTING crane realm: it touches only
// crane-account-service (client, its service-account role mappings and scope mappings) and adds the
// two admin-only user-profile attributes when missing. It never writes the realm settings, the
// crane-bff client, required actions, password policy or any user, and does not read the OIDC
// client secret. Before/after digests of those objects are compared and printed; any difference is
// an error.
func executeAccountServiceOnly(ctx context.Context) error {
	base := os.Getenv("KEYCLOAK_ADMIN_URL")
	if !endpoint(base) {
		return errors.New("explicit admin endpoint required")
	}
	c, err := adminClient(ctx, base)
	if err != nil {
		return err
	}
	if status, err := c.request(ctx, "GET", "/admin/realms/crane", nil, nil); err != nil || status != 200 {
		return errors.New("crane realm must already exist (account-service-only never creates it)")
	}
	before, err := protectedDigest(ctx, c)
	if err != nil {
		return err
	}
	if err = provisionAccountService(ctx, c, true); err != nil {
		return err
	}
	after, err := protectedDigest(ctx, c)
	if err != nil {
		return err
	}
	for k, v := range before {
		if after[k] != v {
			return errors.New("protected Keycloak configuration changed: " + k)
		}
	}
	keys := make([]string, 0, len(before))
	for k := range before {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = os.Stdout.WriteString("UNCHANGED " + k + " sha256=" + before[k][:16] + "\n")
	}
	return nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	precheck, accountOnly := false, false
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--preview-precheck":
			precheck = true
		case "--account-service-only":
			accountOnly = true
		}
	}
	var err error
	switch {
	case precheck:
		err = previewPrecheck(ctx)
	case accountOnly:
		err = executeAccountServiceOnly(ctx)
	default:
		err = execute(ctx)
	}
	if err != nil {
		_, _ = os.Stderr.WriteString("Keycloak provisioning/precheck rejected; no credential or upstream response printed.\n")
		os.Exit(1)
	}
	if precheck {
		_, _ = os.Stdout.WriteString("Keycloak preview realm/client precheck passed; read-only, no writes.\n")
	} else if accountOnly {
		_, _ = os.Stdout.WriteString("crane-account-service converged; realm, crane-bff, users and policies unchanged; no credential printed.\n")
	} else {
		_, _ = os.Stdout.WriteString("Keycloak realm/client provisioned; no users or project grants created.\n")
	}
}
