package main

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// G4-S1A / G4-S5-R1-E: each deployed environment accepts its own origin and one
// approved realm, chosen by BAOBEI_IDENTITY_REALM from a fixed list. Preview and
// production lists never overlap.
func setDeployedEnv(t *testing.T, env, realm string) {
	t.Helper()
	p := deployedProfiles[env]
	r := p.Realms[realm]
	t.Setenv("APP_ENV", env)
	t.Setenv("DEV_AUTH", "0")
	t.Setenv("LISTEN_ADDR", "0.0.0.0:8088")
	t.Setenv("PUBLIC_ORIGIN", p.Origin)
	t.Setenv("BAOBEI_IDENTITY_REALM", realm)
	t.Setenv("OIDC_ISSUER", r.Issuer)
	t.Setenv("OIDC_CLIENT_ID", r.ClientID)
	t.Setenv("OIDC_CLIENT_SECRET", "synthetic-unit-only")
	t.Setenv("OIDC_CLIENT_SECRET_FILE", "")
	t.Setenv("BAOBEI_ACCOUNT_CLIENT_SECRET_FILE", "")
	if r.AccountService {
		t.Setenv("BAOBEI_ACCOUNT_CLIENT_ID", accountServiceClientID)
		t.Setenv("BAOBEI_ACCOUNT_CLIENT_SECRET", "synthetic-unit-only")
	} else {
		t.Setenv("BAOBEI_ACCOUNT_CLIENT_ID", "")
		t.Setenv("BAOBEI_ACCOUNT_CLIENT_SECRET", "")
	}
}

func rejects(t *testing.T, what string, pairs ...[2]string) {
	t.Helper()
	for _, pair := range pairs {
		old := os.Getenv(pair[0])
		t.Setenv(pair[0], pair[1])
		if _, e := config(); e == nil {
			t.Fatalf("%s accepted %s=%q", what, pair[0], pair[1])
		}
		t.Setenv(pair[0], old)
	}
}

func TestPreviewConfigGate(t *testing.T) {
	prod, prev := deployedProfiles["production"], deployedProfiles["preview"]
	if prod.Origin == prev.Origin {
		t.Fatal("preview and production origins overlap")
	}
	for name, pr := range prod.Realms {
		for name2, pv := range prev.Realms {
			if name == name2 || pr.Issuer == pv.Issuer || pr.ClientID == pv.ClientID {
				t.Fatal("preview and production realms overlap")
			}
		}
	}
	pv := prev.Realms["baobei-preview"]
	setDeployedEnv(t, "preview", "baobei-preview")
	if _, e := config(); e != nil {
		t.Fatal("valid preview config rejected:", e)
	}
	rejects(t, "preview",
		[2]string{"DEV_AUTH", "1"},
		[2]string{"PUBLIC_ORIGIN", prod.Origin}, [2]string{"OIDC_ISSUER", prod.Realms["crane"].Issuer}, [2]string{"OIDC_CLIENT_ID", "baobei-web"},
		[2]string{"PUBLIC_ORIGIN", "http://baobei-preview.youdaozd.com"}, [2]string{"PUBLIC_ORIGIN", prev.Origin + "/"},
		[2]string{"OIDC_ISSUER", pv.Issuer + "/"}, [2]string{"OIDC_CLIENT_ID", ""}, [2]string{"OIDC_CLIENT_SECRET", ""},
		[2]string{"BAOBEI_IDENTITY_REALM", "baobei"}, [2]string{"BAOBEI_IDENTITY_REALM", ""})
	for _, realm := range []string{"crane", "baobei"} {
		setDeployedEnv(t, "production", realm)
		if _, e := config(); e != nil {
			t.Fatal("valid production config rejected:", realm, e)
		}
		rejects(t, "production/"+realm,
			[2]string{"PUBLIC_ORIGIN", prev.Origin}, [2]string{"OIDC_ISSUER", pv.Issuer}, [2]string{"OIDC_CLIENT_ID", pv.ClientID},
			[2]string{"BAOBEI_IDENTITY_REALM", "baobei-preview"}, [2]string{"BAOBEI_IDENTITY_REALM", "master"},
			[2]string{"BAOBEI_IDENTITY_REALM", ""}, [2]string{"OIDC_ISSUER", "https://evil.example/realms/" + realm})
	}
	// Changing only the Compose issuer (realm still "crane") is refused, and the reverse.
	setDeployedEnv(t, "production", "crane")
	rejects(t, "issuer-only switch", [2]string{"OIDC_ISSUER", prod.Realms["baobei"].Issuer})
	setDeployedEnv(t, "production", "baobei")
	rejects(t, "realm-only switch", [2]string{"OIDC_ISSUER", prod.Realms["crane"].Issuer})
	// Account service credentials exist only for the dedicated realm, with the fixed client id.
	rejects(t, "dedicated realm without account service", [2]string{"BAOBEI_ACCOUNT_CLIENT_ID", ""}, [2]string{"BAOBEI_ACCOUNT_CLIENT_SECRET", ""},
		[2]string{"BAOBEI_ACCOUNT_CLIENT_ID", "admin-cli"})
	setDeployedEnv(t, "production", "crane")
	rejects(t, "shared realm with account service", [2]string{"BAOBEI_ACCOUNT_CLIENT_ID", accountServiceClientID})
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
