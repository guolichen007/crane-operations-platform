package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestR1ERealKeycloakBrowserFixture serves the built Web and the real API against
// a real Keycloak 26.8.1 dedicated realm (loopback), for the browser check in
// tests/baobei/keycloak-browser. It creates one sales member and its login
// through the account service, prints the one-time credential to the fixture's
// stdout (loopback test data only), then serves until stdin closes.
func TestR1ERealKeycloakBrowserFixture(t *testing.T) {
	dist, base, svc, web := os.Getenv("BAOBEI_AUTH_FIXTURE_DIST"), os.Getenv("BAOBEI_KEYCLOAK_URL"), os.Getenv("BAOBEI_KEYCLOAK_SERVICE_SECRET"), os.Getenv("BAOBEI_KC_BAOBEI_WEB_SECRET")
	if dist == "" || base == "" || svc == "" || web == "" {
		t.Skip("browser fixture only")
	}
	ctx := context.Background()
	db := b2Database(t)
	issuer := base + "/realms/baobei"
	trustIssuer(t, db, issuer)
	// Fixed loopback port so the Keycloak client's redirect URI matches.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + ln.Addr().String()
	app, err := newApp(ctx, db, Config{Env: "development", Issuer: issuer, ClientID: "baobei-web", Secret: web, Origin: origin})
	if err != nil {
		t.Fatal(err)
	}
	app.oauth.RedirectURL = origin + "/auth/callback"
	k, err := newKeycloakAdmin(issuer, "baobei-web", "baobei-account-service", svc, nil)
	if err != nil {
		t.Fatal(err)
	}
	app.accounts, app.linkedOnly = k, true
	files := http.FileServer(http.Dir(dist))
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/auth/") || strings.HasPrefix(r.URL.Path, "/health/"):
			app.handler().ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/assets/") || strings.HasSuffix(r.URL.Path, ".svg"):
			files.ServeHTTP(w, r)
		default:
			http.ServeFile(w, r, filepath.Join(dist, "index.html"))
		}
	}))
	server.Listener = ln
	server.Start()
	defer server.Close()
	// Admin creates the member and the login through the account service (dev admin session).
	admin := &App{db: db, cfg: Config{Env: "development", Dev: true, Issuer: issuer}, accounts: k, linkedOnly: true}
	adminServer := httptest.NewServer(admin.handler())
	defer adminServer.Close()
	admin.cfg.Origin = adminServer.URL
	f := &accountFixture{t: t, app: admin, server: adminServer, admin: login(t, adminServer, "synthetic-admin")}
	phone := "137" + randDigits(8)
	m := f.invite("真实浏览器销售", phone, salesRole)
	s, res, b := f.account(f.admin, m.ID, "create", "browser-create-"+randDigits(10), map[string]string{})
	if s != 200 || res.Credential == nil {
		t.Fatal(s, string(b))
	}
	fmt.Println("AUTH_FIXTURE_URL=" + origin)
	fmt.Println("BAOBEI_KC_PHONE=" + res.Credential.Login)
	fmt.Println("BAOBEI_KC_TEMP=" + res.Credential.TemporaryPassword)
	_, _ = io.Copy(io.Discard, os.Stdin)
}
