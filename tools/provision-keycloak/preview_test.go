package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeKeycloak records every request. The only permitted non-GET request is the
// admin token POST; any other POST/PUT/PATCH/DELETE (a realm or client
// modification) is recorded as a violation and answered 405.
type fakeKeycloak struct {
	issuer      string
	realmStatus int
	clients     []map[string]any
	mu          sync.Mutex
	requests    []string
	violations  []string
}

const tokenPath = "/realms/master/protocol/openid-connect/token"

func (k *fakeKeycloak) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k.mu.Lock()
		k.requests = append(k.requests, r.Method+" "+r.URL.Path)
		write := r.Method != http.MethodGet && !(r.Method == http.MethodPost && r.URL.Path == tokenPath)
		if write {
			k.violations = append(k.violations, r.Method+" "+r.URL.Path)
		}
		k.mu.Unlock()
		if write {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch {
		case r.URL.Path == tokenPath:
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "fake-token"})
		case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": k.issuer})
		case r.URL.Path == "/admin/realms/"+previewRealm:
			w.WriteHeader(k.realmStatus)
		case r.URL.Path == "/admin/realms/"+previewRealm+"/clients":
			_ = json.NewEncoder(w).Encode(k.clients)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

const previewOrigin = "https://" + previewHost

// goodClient mirrors the production client contract with Preview URLs.
func goodClient() map[string]any {
	return map[string]any{
		"clientId": previewClient, "enabled": true, "publicClient": false, "bearerOnly": false,
		"standardFlowEnabled": true, "implicitFlowEnabled": false, "directAccessGrantsEnabled": false, "serviceAccountsEnabled": false,
		"redirectUris": []string{previewOrigin + "/auth/callback"}, "webOrigins": []string{previewOrigin},
		"attributes": map[string]string{"pkce.code.challenge.method": "S256"},
	}
}

func with(c map[string]any, key string, value any) map[string]any {
	out := map[string]any{}
	for k, v := range c {
		out[k] = v
	}
	out[key] = value
	return out
}

func goodKeycloak(clients ...map[string]any) *fakeKeycloak {
	if clients == nil {
		clients = []map[string]any{goodClient()}
	}
	return &fakeKeycloak{issuer: previewOrigin + "/realms/" + previewRealm, realmStatus: 200, clients: clients}
}

func runFake(t *testing.T, k *fakeKeycloak) error {
	t.Helper()
	server := httptest.NewServer(k.handler())
	defer server.Close()
	err := runPreviewPrecheck(context.Background(), server.Client(), server.URL, previewOrigin, previewRealm, previewClient, []byte(strings.Repeat("a", 32)))
	if len(k.violations) != 0 {
		t.Fatalf("precheck attempted to modify Keycloak: %v", k.violations)
	}
	return err
}

func TestPreviewPrecheckAcceptsExactContract(t *testing.T) {
	k := goodKeycloak()
	for i := 0; i < 2; i++ { // idempotent: identical result, still no writes
		if err := runFake(t, k); err != nil {
			t.Fatalf("run %d: exact preview contract rejected: %v", i, err)
		}
	}
	// Only the token POST and GETs were issued.
	for _, r := range k.requests {
		if !strings.HasPrefix(r, "GET ") && r != "POST "+tokenPath {
			t.Fatalf("unexpected request %q", r)
		}
	}
}

func TestPreviewPrecheckRejectsDrift(t *testing.T) {
	base := goodClient()
	cases := map[string]*fakeKeycloak{
		"production issuer":         {issuer: "https://crane.youdaozd.com/realms/crane", realmStatus: 200, clients: []map[string]any{base}},
		"missing realm":             {issuer: previewOrigin + "/realms/" + previewRealm, realmStatus: 404, clients: []map[string]any{base}},
		"no client":                 goodKeycloak([]map[string]any{}...),
		"duplicate client":          goodKeycloak(base, base),
		"clientId mismatch":         goodKeycloak(with(base, "clientId", previewClient+"-other")),
		"production clientId":       goodKeycloak(with(base, "clientId", "crane-bff")),
		"wildcard redirect":         goodKeycloak(with(base, "redirectUris", []string{"*"})),
		"extra production redirect": goodKeycloak(with(base, "redirectUris", []string{previewOrigin + "/auth/callback", "https://crane.youdaozd.com/auth/callback"})),
		"wildcard web origin":       goodKeycloak(with(base, "webOrigins", []string{"+"})),
		"disabled client":           goodKeycloak(with(base, "enabled", false)),
		"public client":             goodKeycloak(with(base, "publicClient", true)),
		"bearer-only client":        goodKeycloak(with(base, "bearerOnly", true)),
		"no code flow":              goodKeycloak(with(base, "standardFlowEnabled", false)),
		"implicit flow":             goodKeycloak(with(base, "implicitFlowEnabled", true)),
		"password grant":            goodKeycloak(with(base, "directAccessGrantsEnabled", true)),
		"service accounts":          goodKeycloak(with(base, "serviceAccountsEnabled", true)),
		"no PKCE":                   goodKeycloak(with(base, "attributes", map[string]string{})),
		"plain PKCE":                goodKeycloak(with(base, "attributes", map[string]string{"pkce.code.challenge.method": "plain"})),
	}
	for name, k := range cases {
		t.Run(name, func(t *testing.T) {
			if err := runFake(t, k); err == nil {
				t.Fatalf("%s: expected rejection", name)
			}
		})
	}
}

// The admin URL lock runs before the admin password is read: every non-Preview
// admin target is refused even when the password file is missing.
func TestPreviewAdminURLLocked(t *testing.T) {
	if err := validatePreviewAdminURL(previewAdminURL); err != nil {
		t.Fatalf("verified Preview admin URL rejected: %v", err)
	}
	for _, raw := range []string{
		"", "https://crane.youdaozd.com", "https://b4.crane.youdaozd.com", "https://keycloak.example.com",
		"http://keycloak:8080", "http://localhost:18085", "http://127.0.0.1:18082", "http://127.0.0.1:18085/",
		"http://127.0.0.1:18085/admin", "http://user:pw@127.0.0.1:18085", "https://127.0.0.1:18085",
	} {
		if err := validatePreviewAdminURL(raw); err == nil {
			t.Fatalf("admin URL %q accepted, must be refused", raw)
		}
	}

	// End-to-end: with a non-Preview admin URL and a valid password file, the
	// precheck fails on the URL lock and never contacts the server.
	k := goodKeycloak()
	server := httptest.NewServer(k.handler())
	defer server.Close()
	pw := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pw, []byte(strings.Repeat("a", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KEYCLOAK_ADMIN_URL", server.URL) // a real, reachable — but non-Preview — admin endpoint
	t.Setenv("PUBLIC_URL", previewOrigin)
	t.Setenv("KEYCLOAK_BOOTSTRAP_PASSWORD_FILE", pw)
	if err := previewPrecheck(context.Background()); err == nil || !strings.Contains(err.Error(), "admin URL") {
		t.Fatalf("precheck with non-Preview admin URL = %v, want admin URL refusal", err)
	}
	if len(k.requests) != 0 {
		t.Fatalf("precheck contacted a non-Preview admin endpoint: %v", k.requests)
	}
}

// The preview precheck contract must fail closed: any production realm/client/
// host, an unknown realm, an HTTP origin, or an invalid URL is rejected before
// any request. Re-running the validation is idempotent.
func TestValidatePreviewTarget(t *testing.T) {
	if err := validatePreviewTarget("https://"+previewHost, previewRealm, previewClient); err != nil {
		t.Fatalf("valid preview target rejected: %v", err)
	}
	reject := func(name, publicURL, realm, client string) {
		t.Helper()
		if err := validatePreviewTarget(publicURL, realm, client); err == nil {
			t.Fatalf("%s: expected rejection, got nil", name)
		}
	}
	reject("production host", "https://crane.youdaozd.com", previewRealm, previewClient)
	reject("production realm", "https://"+previewHost, "crane", previewClient)
	reject("production client", "https://"+previewHost, previewRealm, "crane-bff")
	reject("unknown realm", "https://"+previewHost, "unknown-realm", previewClient)
	reject("http scheme", "http://"+previewHost, previewRealm, previewClient)
	reject("invalid url", "://bad", previewRealm, previewClient)
}

func TestValidatePreviewTargetIdempotent(t *testing.T) {
	for i := 0; i < 3; i++ {
		if err := validatePreviewTarget("https://"+previewHost, previewRealm, previewClient); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
}
