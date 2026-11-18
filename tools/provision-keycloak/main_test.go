package main

import (
	"encoding/json"
	"testing"
)

func TestAdminEndpointTrustBoundary(t *testing.T) {
	for value, allowed := range map[string]bool{"https://auth.example.test": true, "http://keycloak:8080": true, "http://127.0.0.1:18082": true, "http://untrusted.example": false, "https://user:password@auth.example": false, "https://auth.example/?redirect=x": false, "file:///tmp/token": false} {
		if endpoint(value) != allowed {
			t.Fatalf("endpoint trust mismatch for %s", value)
		}
	}
}

// Keycloak returns set-valued fields (client scopes, event types) in varying order on PostgreSQL;
// the protected-configuration digest must not report that as a change, but must detect any value change.
func TestCanonicalDigestIgnoresSetOrderOnly(t *testing.T) {
	a := map[string]any{"optionalClientScopes": []any{"address", "phone", "organization"}, "redirectUris": []any{"https://x/cb"}, "attributes": map[string]any{"pkce": "S256"}}
	b := map[string]any{"optionalClientScopes": []any{"organization", "address", "phone"}, "redirectUris": []any{"https://x/cb"}, "attributes": map[string]any{"pkce": "S256"}}
	c := map[string]any{"optionalClientScopes": []any{"organization", "address", "phone"}, "redirectUris": []any{"https://evil/cb"}, "attributes": map[string]any{"pkce": "S256"}}
	d := map[string]any{"optionalClientScopes": []any{"organization", "address"}, "redirectUris": []any{"https://x/cb"}, "attributes": map[string]any{"pkce": "S256"}}
	enc := func(v any) string { b, _ := json.Marshal(canonical(v)); return string(b) }
	if enc(a) != enc(b) {
		t.Fatal("pure reordering reported as a change")
	}
	if enc(a) == enc(c) || enc(a) == enc(d) {
		t.Fatal("value change not detected")
	}
}
