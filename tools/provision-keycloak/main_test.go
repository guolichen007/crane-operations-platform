package main

import "testing"

func TestAdminEndpointTrustBoundary(t *testing.T) {
	for value, allowed := range map[string]bool{"https://auth.example.test": true, "http://keycloak:8080": true, "http://127.0.0.1:18082": true, "http://untrusted.example": false, "https://user:password@auth.example": false, "https://auth.example/?redirect=x": false, "file:///tmp/token": false} {
		if endpoint(value) != allowed {
			t.Fatalf("endpoint trust mismatch for %s", value)
		}
	}
}
