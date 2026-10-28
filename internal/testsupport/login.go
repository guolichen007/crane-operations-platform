package testsupport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

func BeginLogin(t *testing.T, handler http.Handler, provider *OIDCProvider, origin string) (string, []*http.Cookie) {
	t.Helper()
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, httptest.NewRequest("GET", origin+"/auth/login", nil))
	if result.Code != 302 {
		t.Fatal("begin login failed", result.Code)
	}
	return provider.LoginResponse(t, result.Header().Get("Location")), result.Result().Cookies()
}

func Login(t *testing.T, handler http.Handler, provider *OIDCProvider, origin string) (*http.Cookie, contract.WebSession) {
	t.Helper()
	callback, cookies := BeginLogin(t, handler, provider, origin)
	request := httptest.NewRequest("GET", callback, nil)
	request.TLS = nil // Shared ingress terminates TLS; production Cookie must still be Secure.
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != 303 {
		t.Fatal("callback failed", result.Code, result.Body.String())
	}
	var cookie *http.Cookie
	for _, value := range result.Result().Cookies() {
		if value.Name == "__Host-crane_session" {
			cookie = value
		}
	}
	if cookie == nil {
		t.Fatal("session cookie missing")
	}
	request = httptest.NewRequest("GET", origin+"/auth/session", nil)
	request.AddCookie(cookie)
	result = httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	var session contract.WebSession
	if result.Code != 200 || json.Unmarshal(result.Body.Bytes(), &session) != nil {
		t.Fatal("current session failed", result.Code)
	}
	return cookie, session
}
