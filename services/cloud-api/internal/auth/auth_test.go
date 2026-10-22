package auth

import (
	"net/http/httptest"
	"testing"
)

func TestRBAC(t *testing.T) {
	if Allowed("viewer", "watch") || Allowed("operator", "remote") || !Allowed("maintainer", "remote") {
		t.Fatal("RBAC")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer short")
	if _, ok := Authenticate(r, "short"); ok {
		t.Fatal("weak token")
	}
}
