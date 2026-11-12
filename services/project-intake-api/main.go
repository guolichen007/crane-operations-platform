package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Env, Addr, Origin, Issuer, ClientID, Secret string
	Dev                                         bool
	// Realm: the approved realm name selected for a deployed environment.
	Realm string
	// Account service (dedicated realm only): client credentials for the Baobei
	// realm's admin API. Never set for the shared realm.
	AccountClientID, AccountSecret string
}

func config() (Config, error) {
	c := Config{Env: os.Getenv("APP_ENV"), Addr: os.Getenv("LISTEN_ADDR"), Origin: os.Getenv("PUBLIC_ORIGIN"), Issuer: os.Getenv("OIDC_ISSUER"),
		ClientID: os.Getenv("OIDC_CLIENT_ID"), Secret: envSecret("OIDC_CLIENT_SECRET"), Dev: os.Getenv("DEV_AUTH") == "1",
		Realm: os.Getenv("BAOBEI_IDENTITY_REALM"), AccountClientID: os.Getenv("BAOBEI_ACCOUNT_CLIENT_ID"), AccountSecret: envSecret("BAOBEI_ACCOUNT_CLIENT_SECRET")}
	if c.Addr == "" {
		c.Addr = "127.0.0.1:8088"
	}
	if c.Origin == "" {
		c.Origin = "http://127.0.0.1:5188"
	}
	if !knownEnv(c.Env) {
		return c, fmt.Errorf("explicit APP_ENV required")
	}
	if c.Dev {
		host, _, e := net.SplitHostPort(c.Addr)
		if e != nil || c.Env != "development" || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return c, fmt.Errorf("dev auth requires development and loopback listen")
		}
	}
	// Each deployed environment accepts exactly its own origin and one approved
	// realm (issuer + client) chosen by BAOBEI_IDENTITY_REALM. OIDC_ISSUER and
	// OIDC_CLIENT_ID must repeat that realm's exact values, so changing only the
	// Compose issuer (or only the realm name) refuses to start.
	if p, ok := deployedProfiles[c.Env]; ok {
		r, err := selectRealm(c.Env, c.Realm)
		if err != nil {
			return c, err
		}
		if c.Origin != p.Origin || c.Issuer != r.Issuer || c.ClientID != r.ClientID || c.Secret == "" {
			return c, fmt.Errorf("%s OIDC configuration required", c.Env)
		}
		if r.AccountService != (c.AccountClientID != "") || r.AccountService && (c.AccountClientID != accountServiceClientID || c.AccountSecret == "") {
			return c, fmt.Errorf("%s account service configuration does not match realm %q", c.Env, c.Realm)
		}
	}
	return c, nil
}

const accountServiceClientID = "baobei-account-service"

func main() {
	command := ""
	if len(os.Args) > 1 {
		switch {
		case len(os.Args) >= 3 && os.Args[1] == "maintenance" && os.Args[2] == "identity-apply":
			command = "identity-apply"
		case len(os.Args) >= 3 && os.Args[1] == "maintenance" && os.Args[2] == "staff-provision":
			command = "staff-provision"
		case len(os.Args) >= 3 && os.Args[1] == "maintenance" && os.Args[2] == "identity-switch":
			command = "identity-switch"
		case len(os.Args) == 3 && os.Args[1] == "maintenance" && (os.Args[2] == "migrate" || os.Args[2] == "protection-sweep"):
			command = os.Args[2]
		default:
			log.Fatal("unknown maintenance command")
		}
	}
	c := Config{Env: os.Getenv("APP_ENV")}
	if !knownEnv(c.Env) {
		log.Fatal("explicit APP_ENV required")
	}
	var e error
	if command == "" {
		c, e = config()
		if e != nil {
			log.Fatal(e)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dsn := envSecret("DATABASE_URL")
	if dsn == "" {
		log.Fatal("explicit DATABASE_URL required")
	}
	database, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		log.Fatal("database configuration failed")
	}
	if deployed(c.Env) {
		database.ConnConfig.RuntimeParams["search_path"] = "public"
	}
	// Serving in a deployed environment needs the separate staff credentials;
	// refuse before touching the database or the identity provider.
	var staffConfig *pgxpool.Config
	if deployed(c.Env) && command == "" {
		if staffConfig, e = parseStaffConfig(envSecret("STAFF_DATABASE_URL"), database); e != nil {
			log.Fatal(e)
		}
	}
	pool, e := pgxpool.NewWithConfig(ctx, database)
	if e != nil {
		log.Fatal("database configuration failed")
	}
	defer pool.Close()
	check, cancel := context.WithTimeout(ctx, time.Minute)
	e = prepareDatabase(check, pool, c.Env, command)
	cancel()
	if e != nil {
		log.Fatal("database role, migration or schema verification failed")
	}
	switch command {
	case "migrate":
		fmt.Println("database schema verified")
		return
	case "identity-apply":
		if e = identityCommand(ctx, pool, os.Args[3:], os.Stdout); e != nil {
			log.Fatal(e)
		}
		return
	case "staff-provision":
		if e = provisionCommand(ctx, pool, os.Args[3:], os.Stdout); e != nil {
			log.Fatal(e)
		}
		return
	case "identity-switch":
		if e = identitySwitchCommand(ctx, pool, c.Env, os.Args[3:], os.Stdout); e != nil {
			log.Fatal(e)
		}
		return
	case "protection-sweep":
		n, e := (&App{db: pool, cfg: c}).protectionSweep(ctx, time.Now().UTC())
		if e != nil {
			log.Fatal("protection sweep failed")
		}
		fmt.Printf("protection sweep updated=%d\n", n)
		return
	}
	// Fail closed: the database must trust exactly this API's issuer (set by the
	// controlled identity-switch command); otherwise refuse to serve.
	issuer := c.Issuer
	if issuer == "" {
		issuer = devIssuer
	}
	verifyEpoch, cancelEpoch := context.WithTimeout(ctx, 10*time.Second)
	e = verifyIdentityEpoch(verifyEpoch, pool, c.Env, issuer)
	cancelEpoch()
	if e != nil {
		log.Fatal("identity epoch rejected: the database trusts another issuer")
	}
	a, e := newApp(ctx, pool, c)
	if e != nil {
		log.Fatal("authentication initialization failed")
	}
	if r, err := selectRealm(c.Env, c.Realm); err == nil && r.AccountService {
		if a.accounts, e = newKeycloakAdmin(c.Issuer, c.ClientID, c.AccountClientID, c.AccountSecret, nil); e != nil {
			log.Fatal("account service configuration rejected")
		}
		a.linkedOnly = true
	}
	if deployed(c.Env) {
		var err error
		a.staffDB, err = pgxpool.NewWithConfig(ctx, staffConfig)
		if err != nil {
			log.Fatal("staff database unavailable")
		}
		defer a.staffDB.Close()
		verify, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = verifyDatabaseRole(verify, a.staffDB, "baobei_staff_api", false)
		if err != nil {
			cancel()
			log.Fatal("staff database role rejected")
		}
		err = verifyDatabaseEnvironment(verify, a.staffDB, c.Env)
		cancel()
		if err != nil {
			log.Fatal("staff database environment rejected")
		}
	}
	if c.Dev {
		a.accounts = newDevAccounts()
		if e = seed(ctx, pool); e != nil {
			log.Fatal("synthetic identity seed failed")
		}
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		// Account operations left by a previous process are reconciled with
		// Keycloak as soon as their claims expire (checked at start and each minute).
		_, _ = a.recoverAccountOps(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = a.cleanupAuth(ctx)
				if _, err := a.recoverAccountOps(ctx); err != nil {
					log.Print("account recovery: database unavailable, will retry")
				}
			}
		}
	}()
	server := &http.Server{Addr: c.Addr, Handler: a.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Print("baobei API listening (request logging disabled)")
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal("server stopped")
	}
}
func seed(ctx context.Context, p *pgxpool.Pool) error {
	_, e := p.Exec(ctx, `INSERT INTO teams(id,name) VALUES('11111111-1111-4111-8111-111111111111','Synthetic Team') ON CONFLICT DO NOTHING;
 INSERT INTO app_users(subject,display_name,role,team_id) VALUES
 ('synthetic-a','示例销售 A','baobei_sales','11111111-1111-4111-8111-111111111111'),
 ('synthetic-b','示例销售 B','baobei_sales','11111111-1111-4111-8111-111111111111'),
 ('synthetic-manager','示例主管','baobei_manager','11111111-1111-4111-8111-111111111111') ON CONFLICT DO NOTHING;
 INSERT INTO teams(id,name) VALUES('22222222-2222-4222-8222-222222222222','Synthetic Other Team') ON CONFLICT DO NOTHING;
 INSERT INTO app_users(subject,display_name,role,team_id) VALUES
 ('synthetic-c','示例销售 C','baobei_sales','22222222-2222-4222-8222-222222222222'),
 ('synthetic-manager-other','示例另一组主管','baobei_manager','22222222-2222-4222-8222-222222222222'),
 ('synthetic-director','示例总监','baobei_director',NULL),
 ('synthetic-admin','示例管理员','baobei_admin',NULL),
 ('synthetic-staff-desktop','交接销售桌面','baobei_sales','11111111-1111-4111-8111-111111111111'),
 ('synthetic-staff-mobile','交接销售手机','baobei_sales','11111111-1111-4111-8111-111111111111') ON CONFLICT DO NOTHING;
 UPDATE app_users SET work_email=subject||'@example.test' WHERE subject IN ('synthetic-a','synthetic-b','synthetic-c','synthetic-staff-desktop','synthetic-staff-mobile') AND work_email IS NULL`)
	return e
}

// File secrets are read only at startup; never logged or copied into responses.
func envSecret(key string) string {
	if path := os.Getenv(key + "_FILE"); path != "" {
		b, e := os.ReadFile(path)
		if e != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	return os.Getenv(key)
}
