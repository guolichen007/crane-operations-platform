// uatdata seeds, refreshes, verifies and removes the H3 Preview synthetic acceptance dataset.
//
// It writes ONLY to the UAT namespace (projects/assets/runtimes "UAT-*", users "uat-*") and only
// after three independent Preview guards pass:
//
//  1. --issuer must be exactly the Preview realm issuer;
//  2. every OIDC-bound user already in the database must carry that same issuer
//     (a production or mixed database has production-issuer users and is refused);
//  3. --expect-system-identifier must equal pg_control_system().system_identifier of the
//     connected cluster (recorded read-only during G1; the production cluster has a different one).
//
// Usage (on the Preview host, inside the Preview compose network; never against production):
//
//	DATABASE_URL=postgres://crane_app@postgres:5432/crane_app?sslmode=disable \
//	DATABASE_PASSWORD_FILE=/run/secrets/app_db_password \
//	uatdata <plan|seed|refresh|verify|cleanup> \
//	  --issuer https://b4.crane.youdaozd.com/realms/crane-preview \
//	  --expect-system-identifier <id> [--subjects-file subjects.json] [--confirm-cleanup]
//
// subjects.json maps each UAT user id to the Keycloak `sub` of a user that already exists in the
// Preview realm (`sub` is not a secret). No credentials are read from or printed by this tool
// beyond the database password file.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/platform"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const previewIssuer = "https://b4.crane.youdaozd.com/realms/crane-preview"

type options struct {
	issuer, subjectsFile, systemID string
	confirmCleanup                 bool
}

func guard(ctx context.Context, db *sql.DB, o options) error {
	if o.issuer != previewIssuer {
		return fmt.Errorf("refusing: --issuer must be exactly %s", previewIssuer)
	}
	if _, err := strconv.ParseUint(o.systemID, 10, 64); err != nil {
		return errors.New("refusing: --expect-system-identifier (numeric, from G1) is required")
	}
	var sysid string
	if err := db.QueryRowContext(ctx, `SELECT system_identifier::text FROM pg_control_system()`).Scan(&sysid); err != nil {
		return fmt.Errorf("refusing: cannot read cluster identifier: %w", err)
	}
	if sysid != o.systemID {
		return errors.New("refusing: connected cluster is not the expected Preview cluster")
	}
	var foreign int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE issuer IS NOT NULL AND issuer <> $1`, previewIssuer).Scan(&foreign); err != nil {
		return err
	}
	if foreign > 0 {
		return fmt.Errorf("refusing: %d user(s) bound to a non-Preview issuer — this is not an isolated Preview database", foreign)
	}
	return nil
}

func plan(w io.Writer) error {
	now := time.Now().UTC()
	return json.NewEncoder(w).Encode(map[string]any{
		"projects": len(projects), "users": len(users), "assets": len(assets()), "runtimes": len(runtimes()), "alarms": len(alarms(now)),
		"expectations": len(expectations()), "namespace": []string{projectPrefix + "*", userPrefix + "*"},
	})
}

func open() (*sql.DB, error) {
	source := os.Getenv("DATABASE_URL")
	path := os.Getenv("DATABASE_PASSWORD_FILE")
	if source == "" || path == "" {
		return nil, errors.New("DATABASE_URL and DATABASE_PASSWORD_FILE are required")
	}
	password, err := platform.PrivateSecret(path, 20)
	if err != nil {
		return nil, err
	}
	if source, err = platform.DatabaseWithPassword(source, password); err != nil {
		return nil, err
	}
	return sql.Open("pgx", source)
}

func readSubjects(path string) (map[string]string, error) {
	if path == "" {
		return nil, errors.New("--subjects-file is required for seed")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	return m, json.Unmarshal(b, &m)
}

// refresh shifts every time-relative UAT fact (shadow lastSeenAt, alarm observed_at, the seed
// anchor) forward to "now", so a long UAT session keeps the same classification without
// re-binding users or invalidating their web sessions.
func refresh(ctx context.Context, tx *sql.Tx, now time.Time, anchor time.Time) error {
	delta := now.Sub(anchor)
	if _, err := tx.ExecContext(ctx, `UPDATE alarms SET observed_at = observed_at + make_interval(secs => $1)
WHERE device_id IN (SELECT id FROM devices WHERE project_id LIKE 'UAT-%')`, delta.Seconds()); err != nil {
		return err
	}
	for _, r := range runtimes() {
		if _, err := tx.ExecContext(ctx, `UPDATE device_shadow SET payload_json=$2, updated_at=$3 WHERE device_id=$1`, r.ID, shadowJSON(r, now), now); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE projects SET notes = regexp_replace(notes, $1 || '.*$', $1 || $2) WHERE id='UAT-P001'`,
		seedMarker, now.Format(time.RFC3339Nano))
	return err
}

func run(args []string, w io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: uatdata plan|seed|refresh|verify|cleanup [flags]")
	}
	fs := flag.NewFlagSet("uatdata", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o options
	fs.StringVar(&o.issuer, "issuer", "", "exact Preview OIDC issuer")
	fs.StringVar(&o.subjectsFile, "subjects-file", "", "JSON map uat user id → Keycloak sub")
	fs.StringVar(&o.systemID, "expect-system-identifier", "", "Preview PostgreSQL system_identifier recorded in G1")
	fs.BoolVar(&o.confirmCleanup, "confirm-cleanup", false, "required for cleanup")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return errors.New("invalid uatdata arguments")
	}
	if args[0] == "plan" {
		return plan(w)
	}
	db, err := open()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err = guard(ctx, db, o); err != nil {
		return err
	}
	return execute(ctx, db, args[0], o, w)
}

// execute runs one guarded operation. Kept separate from run so the real-PostgreSQL test can
// exercise it without the Preview-only guard.
func execute(ctx context.Context, db *sql.DB, op string, o options, w io.Writer) error {
	enc := json.NewEncoder(w)
	switch op {
	case "seed":
		subjects, err := readSubjects(o.subjectsFile)
		if err != nil {
			return err
		}
		return seedAll(ctx, db, subjects, o.issuer, time.Now().UTC(), w)
	case "refresh":
		anchor, err := seedAnchor(ctx, db)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = refresh(ctx, tx, time.Now().UTC(), anchor); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return verifyAndReport(ctx, db, w)
	case "verify":
		return verifyAndReport(ctx, db, w)
	case "cleanup":
		if !o.confirmCleanup {
			return errors.New("cleanup requires --confirm-cleanup")
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		removed, err := cleanup(ctx, tx)
		if err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return enc.Encode(map[string]any{"UAT_CLEANUP": "DONE", "removed": removed})
	}
	return errors.New("unknown uatdata operation")
}

func seedAll(ctx context.Context, db *sql.DB, subjects map[string]string, issuer string, now time.Time, w io.Writer) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = cleanup(ctx, tx); err != nil {
		return err
	}
	if err = seedData(ctx, tx, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = bindUsers(ctx, db, issuer, subjects); err != nil {
		return err
	}
	return verifyAndReport(ctx, db, w)
}

func verifyAndReport(ctx context.Context, db *sql.DB, w io.Writer) error {
	res, err := verify(ctx, db)
	if err != nil {
		return err
	}
	status := "PASS"
	if len(res.Failures) > 0 {
		status = "FAIL"
	}
	if err = json.NewEncoder(w).Encode(map[string]any{"UAT_DATA_VERIFY": status, "result": res}); err != nil {
		return err
	}
	if status != "PASS" {
		return errors.New("UAT dataset does not match the expected server-side numbers")
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "uatdata:", err)
		os.Exit(1)
	}
}
