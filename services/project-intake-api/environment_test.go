package main

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// G4-S1A: APP_ENV=preview is production-equivalent; only its exact OIDC
// allow-list differs, and the two allow-lists never accept each other.
func setDeployedEnv(t *testing.T, env, origin, issuer, client string) {
	t.Helper()
	t.Setenv("APP_ENV", env)
	t.Setenv("DEV_AUTH", "0")
	t.Setenv("LISTEN_ADDR", "0.0.0.0:8088")
	t.Setenv("PUBLIC_ORIGIN", origin)
	t.Setenv("OIDC_ISSUER", issuer)
	t.Setenv("OIDC_CLIENT_ID", client)
	t.Setenv("OIDC_CLIENT_SECRET", "synthetic-unit-only")
	t.Setenv("OIDC_CLIENT_SECRET_FILE", "")
}

func TestPreviewConfigGate(t *testing.T) {
	prod, prev := deployedProfiles["production"], deployedProfiles["preview"]
	// Allow-lists are disjoint: no field is shared between the two environments.
	if prod.Origin == prev.Origin || prod.Issuer == prev.Issuer || prod.ClientID == prev.ClientID {
		t.Fatal("preview and production allow-lists overlap")
	}
	setDeployedEnv(t, "preview", prev.Origin, prev.Issuer, prev.ClientID)
	if _, e := config(); e != nil {
		t.Fatal("valid preview config rejected:", e)
	}
	for _, pair := range [][2]string{
		{"DEV_AUTH", "1"},
		{"PUBLIC_ORIGIN", prod.Origin}, {"OIDC_ISSUER", prod.Issuer}, {"OIDC_CLIENT_ID", prod.ClientID},
		{"PUBLIC_ORIGIN", "http://baobei-preview.youdaozd.com"}, {"PUBLIC_ORIGIN", prev.Origin + "/"},
		{"OIDC_ISSUER", prev.Issuer + "/"}, {"OIDC_CLIENT_ID", ""}, {"OIDC_CLIENT_SECRET", ""},
	} {
		old := os.Getenv(pair[0])
		t.Setenv(pair[0], pair[1])
		if _, e := config(); e == nil {
			t.Fatalf("preview accepted %s=%q", pair[0], pair[1])
		}
		t.Setenv(pair[0], old)
	}
	// Production must not accept any preview value either.
	setDeployedEnv(t, "production", prod.Origin, prod.Issuer, prod.ClientID)
	if _, e := config(); e != nil {
		t.Fatal("valid production config rejected:", e)
	}
	for _, pair := range [][2]string{{"PUBLIC_ORIGIN", prev.Origin}, {"OIDC_ISSUER", prev.Issuer}, {"OIDC_CLIENT_ID", prev.ClientID}} {
		old := os.Getenv(pair[0])
		t.Setenv(pair[0], pair[1])
		if _, e := config(); e == nil {
			t.Fatalf("production accepted preview %s", pair[0])
		}
		t.Setenv(pair[0], old)
	}
	// Unknown or near-miss environment names are not deployed environments.
	for _, env := range []string{"", "Preview", "staging", "prod", "preview "} {
		t.Setenv("APP_ENV", env)
		if _, e := config(); e == nil {
			t.Fatalf("APP_ENV=%q accepted", env)
		}
	}
}

func TestPreviewHardenedBehaviour(t *testing.T) {
	for _, env := range []string{"production", "preview"} {
		a := &App{cfg: Config{Env: env}}
		if a.cookieName() != "__Host-baobei_session" || a.loginCookieName() != "__Host-baobei_login" {
			t.Fatal(env, "cookie names")
		}
		rec := httptest.NewRecorder()
		a.cookie(rec, a.cookieName(), "synthetic-only", 60)
		h := rec.Header().Get("Set-Cookie")
		for _, want := range []string{"__Host-baobei_session=", "Path=/", "HttpOnly", "Secure", "SameSite=Lax"} {
			if !strings.Contains(h, want) {
				t.Fatal(env, "cookie lacks", want, h)
			}
		}
		if strings.Contains(h, "Domain=") {
			t.Fatal(env, "__Host- cookie must not set Domain")
		}
		// Fail closed: no dedicated staff connection → no staff pool, never the runtime pool.
		a.db = &pgxpool.Pool{}
		if a.staffPool() != nil {
			t.Fatal(env, "staffPool fell back to the runtime pool")
		}
	}
	for _, env := range []string{"", "staging", "Preview"} {
		a := &App{db: &pgxpool.Pool{}, cfg: Config{Env: env}}
		if a.staffPool() != nil {
			t.Fatalf("unknown env %q staffPool fell back to runtime", env)
		}
	}
	dev := &App{db: &pgxpool.Pool{}, cfg: Config{Env: "development"}}
	if dev.staffPool() != dev.db || dev.cookieName() != "baobei_dev_session" {
		t.Fatal("development behaviour changed")
	}
}

func TestPreviewStaffDSNRequired(t *testing.T) {
	runtime, err := pgxpool.ParseConfig("postgres://baobei_runtime@baobei-preview-postgres:5432/baobei?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseStaffConfig("postgres://baobei_staff_api@baobei-preview-postgres:5432/baobei?sslmode=disable", runtime); err != nil {
		t.Fatal("valid staff DSN rejected", err)
	}
	// Missing, unparsable, another server (the production host name), another
	// port and another database are all refused.
	for _, bad := range []string{
		"",
		"::not a dsn::",
		"postgres://baobei_staff_api@baobei-postgres:5432/baobei",
		"postgres://baobei_staff_api@baobei-preview-postgres:5433/baobei",
		"postgres://baobei_staff_api@baobei-preview-postgres:5432/other",
	} {
		if _, err := parseStaffConfig(bad, runtime); err == nil {
			t.Fatalf("staff DSN %q accepted", bad)
		}
	}
}
