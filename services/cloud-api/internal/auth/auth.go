package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

type Identity struct{ ID, Role string }

func Allowed(role, action string) bool {
	switch action {
	case "read":
		return role == "viewer" || role == "operator" || role == "maintainer" || role == "platform_admin"
	case "watch":
		return role == "operator" || role == "maintainer" || role == "platform_admin"
	case "remote":
		return role == "maintainer" || role == "platform_admin"
	}
	return false
}
func Authenticate(r *http.Request, expected string) (Identity, bool) {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if cookie, e := r.Cookie("crane_dev"); e == nil && got == "" {
		got = cookie.Value
	}
	if len(expected) < 32 || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
		return Identity{}, false
	}
	return Identity{ID: "dev-admin", Role: "platform_admin"}, true
}
