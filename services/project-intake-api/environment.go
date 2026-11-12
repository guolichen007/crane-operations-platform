package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// G4-S1A: deployed environments. Preview behaves exactly like production
// (Secure __Host- cookies, least-privilege role checks, separate staff
// connection, public search_path, no dev login, no automatic migration);
// only its OIDC identity and database marker differ. Each deployed
// environment has its own exact allow-list and the lists never overlap.
type deployedProfile struct {
	Origin string
	// Realms the environment may be switched between. Exactly one is active at a
	// time (BAOBEI_IDENTITY_REALM), chosen from this fixed list: an arbitrary
	// issuer URL is never accepted. Each realm has its own issuer and client.
	Realms map[string]realmProfile
}

type realmProfile struct {
	Issuer, ClientID string
	// AccountService: the dedicated realm has a Baobei account service client;
	// the shared realm does not (no admin API access there, ever).
	AccountService bool
}

var deployedProfiles = map[string]deployedProfile{
	"production": {Origin: "https://baobei.youdaozd.com", Realms: map[string]realmProfile{
		// G4-S5-R1-E: dedicated Baobei realm (target) and the shared crane realm
		// (current, and the fallback for a controlled switch back).
		"baobei": {Issuer: "https://crane.youdaozd.com/realms/baobei", ClientID: "baobei-web", AccountService: true},
		"crane":  {Issuer: "https://crane.youdaozd.com/realms/crane", ClientID: "baobei-web"},
	}},
	"preview": {Origin: "https://baobei-preview.youdaozd.com", Realms: map[string]realmProfile{
		// Preview keeps its isolated realm without the account service until an
		// approved preview deployment provides one.
		"baobei-preview": {Issuer: "https://crane.youdaozd.com/realms/baobei-preview", ClientID: "baobei-preview-web"},
	}},
}

// selectRealm returns the single realm profile an environment trusts.
func selectRealm(env, realm string) (realmProfile, error) {
	p, ok := deployedProfiles[env]
	if !ok {
		return realmProfile{}, fmt.Errorf("unknown deployed environment")
	}
	r, ok := p.Realms[realm]
	if !ok {
		return realmProfile{}, fmt.Errorf("%s: BAOBEI_IDENTITY_REALM must be one of the approved realms", env)
	}
	return r, nil
}

// deployed reports whether env must use the hardened (production-equivalent) path.
func deployed(env string) bool {
	_, ok := deployedProfiles[env]
	return ok
}

func knownEnv(env string) bool { return env == "development" || deployed(env) }

// Database environment marker. A preview database carries a one-row table
// written by its own provisioning script (deploy/baobei/db/preview-environment.sql).
// Production is not required to have the marker (no forced migration on the
// production database); but a production API refuses a database that is
// marked as anything else, and a preview API refuses any database that is not
// marked exactly "preview". The check runs before migration or schema
// verification, so a misconfigured DSN never reaches a migration.
const environmentMarkerSQL = `SELECT name FROM baobei_environment WHERE singleton`

var errEnvironmentMismatch = errors.New("database environment marker mismatch")

// parseStaffConfig: deployed environments need a separate staff DSN on the
// same server and database as the runtime DSN. Missing or mismatched → error
// (the API refuses to start; there is no fallback to the runtime pool).
func parseStaffConfig(dsn string, runtime *pgxpool.Config) (*pgxpool.Config, error) {
	staffConfig, err := pgxpool.ParseConfig(dsn)
	if dsn == "" || err != nil || staffConfig.ConnConfig.Host != runtime.ConnConfig.Host ||
		staffConfig.ConnConfig.Port != runtime.ConnConfig.Port ||
		staffConfig.ConnConfig.Database != runtime.ConnConfig.Database {
		return nil, errors.New("separate staff database credentials required")
	}
	staffConfig.ConnConfig.RuntimeParams["search_path"] = "public"
	return staffConfig, nil
}

func verifyDatabaseEnvironment(ctx context.Context, pool *pgxpool.Pool, env string) error {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.baobei_environment') IS NOT NULL`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if env == "preview" {
			return fmt.Errorf("%w: preview requires marker", errEnvironmentMismatch)
		}
		return nil
	}
	var names []string
	rows, err := pool.Query(ctx, environmentMarkerSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(names) != 1 || names[0] != env {
		return fmt.Errorf("%w: expected %q", errEnvironmentMismatch, env)
	}
	return nil
}
