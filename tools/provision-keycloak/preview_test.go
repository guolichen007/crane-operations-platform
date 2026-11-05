package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeKeycloak struct {
	issuer       string
	realmStatus  int
	clients      []map[string]any
	mu           sync.Mutex
	writeMethods []string
}

func (k *fakeKeycloak) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.URL.Path != "/realms/master/protocol/openid-connect/token" {
			k.mu.Lock()
			k.writeMethods = append(k.writeMethods, r.Method+" "+r.URL.Path)
			k.mu.Unlock()
		}
		switch {
		case r.URL.Path == "/realms/master/protocol/openid-connect/token":
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

func goodClient() map[string]any {
	return map[string]any{"redirectUris": []string{previewOrigin + "/auth/callback"}, "webOrigins": []string{previewOrigin}}
}

func runFake(t *testing.T, k *fakeKeycloak) error {
	t.Helper()
	server := httptest.NewServer(k.handler())
	defer server.Close()
	err := runPreviewPrecheck(context.Background(), server.Client(), server.URL, previewOrigin, previewRealm, previewClient, []byte(strings.Repeat("a", 32)))
	if len(k.writeMethods) != 0 {
		t.Fatalf("precheck issued write requests: %v", k.writeMethods)
	}
	return err
}

func TestPreviewPrecheckAcceptsExactContract(t *testing.T) {
	k := &fakeKeycloak{issuer: previewOrigin + "/realms/" + previewRealm, realmStatus: 200, clients: []map[string]any{goodClient()}}
	for i := 0; i < 2; i++ { // idempotent: identical result, still no writes
		if err := runFake(t, k); err != nil {
			t.Fatalf("run %d: exact preview contract rejected: %v", i, err)
		}
	}
}

func TestPreviewPrecheckRejectsDrift(t *testing.T) {
	cases := map[string]*fakeKeycloak{
		"production issuer": {issuer: "https://crane.youdaozd.com/realms/crane", realmStatus: 200, clients: []map[string]any{goodClient()}},
		"missing realm":     {issuer: previewOrigin + "/realms/" + previewRealm, realmStatus: 404, clients: []map[string]any{goodClient()}},
		"no client":         {issuer: previewOrigin + "/realms/" + previewRealm, realmStatus: 200, clients: []map[string]any{}},
		"duplicate client":  {issuer: previewOrigin + "/realms/" + previewRealm, realmStatus: 200, clients: []map[string]any{goodClient(), goodClient()}},
		"wildcard redirect": {issuer: previewOrigin + "/realms/" + previewRealm, realmStatus: 200, clients: []map[string]any{{"redirectUris": []string{"*"}, "webOrigins": []string{previewOrigin}}}},
		"extra production redirect": {issuer: previewOrigin + "/realms/" + previewRealm, realmStatus: 200, clients: []map[string]any{{
			"redirectUris": []string{previewOrigin + "/auth/callback", "https://crane.youdaozd.com/auth/callback"}, "webOrigins": []string{previewOrigin}}}},
		"wildcard web origin": {issuer: previewOrigin + "/realms/" + previewRealm, realmStatus: 200, clients: []map[string]any{{"redirectUris": []string{previewOrigin + "/auth/callback"}, "webOrigins": []string{"+"}}}},
	}
	for name, k := range cases {
		t.Run(name, func(t *testing.T) {
			if err := runFake(t, k); err == nil {
				t.Fatalf("%s: expected rejection", name)
			}
		})
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
