// Package devreset removes ALL Crane business data of the crane-dev environment in one transaction,
// keeping the platform foundation: tenants, users/roles (accounts), departments, system roles and
// account provisioning records (Schema 13), portfolio scopes and their
// memberships, the equipment type directory, global (project-less) manufacturers and catalog items,
// the audit log, web sessions and the schema version. It is a one-off, explicitly approved
// operation (OWNER 2026-11-18: historical Crane test data may be deleted); normal Fleet updates
// still refuse to remove identities.
//
// Guards (all must hold, checked again inside the locked transaction):
//   - environment sentinel CRANE_DEPLOYMENT_ENVIRONMENT=crane-dev and PUBLIC_URL of crane-dev;
//   - the database is the Crane application database (crane_app);
//   - no REAL runtime identity exists (real hardware is never touched);
//   - the approved plan digest equals the digest recomputed in the transaction (binds the plan to
//     this database and these exact row counts; after the reset the digest changes, so a digest
//     can be used only once).
package devreset

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
)

var (
	ErrGuard          = errors.New("reset guard")
	ErrDigestMismatch = errors.New("plan digest does not match the database state")
)

// Guard is what the target must be. Production code uses CraneDev; tests use their own database.
type Guard struct {
	Environment, PublicURL, Database string
}

// CraneDev is the only target the CLI accepts.
var CraneDev = Guard{Environment: "crane-dev", PublicURL: "https://crane.youdaozd.com", Database: "crane_app"}

// Deleted tables in dependency order (children first). Partial deletes keep global master data.
var steps = []struct{ table, where string }{
	{"alarms", ""}, {"events", ""}, {"telemetry_samples", ""}, {"device_shadow", ""}, {"device_heartbeats", ""},
	{"device_live_state", ""}, {"device_commands", ""}, {"live_sessions", ""}, {"remote_sessions", ""},
	{"equipment_runtime_bindings", ""}, {"equipment_relations", ""}, {"asset_equipment_installations", ""},
	{"asset_installations", ""}, {"asset_timeline_events", ""}, {"entity_revisions", ""},
	{"devices", ""}, {"asset_devices", ""}, {"asset_locations", ""},
	{"equipment_catalog_items", "project_id IS NOT NULL OR manufacturer_id IN (SELECT id FROM manufacturers WHERE project_id IS NOT NULL)"}, {"manufacturers", "project_id IS NOT NULL"},
	{"schedule_conflict_overrides", ""}, {"schedule_conflicts", ""}, {"schedule_dependencies", ""},
	{"resource_reservations", ""}, {"schedule_baselines", ""}, {"schedule_items", ""},
	{"resource_calendar_exceptions", ""}, {"resource_calendar_rules", ""}, {"resource_calendars", ""}, {"resources", ""},
	{"project_activities", ""}, {"project_milestones", ""}, {"project_memberships", ""},
	{"project_intake_transitions", ""}, {"project_intakes", ""}, {"projects", ""}, {"idempotency_keys", ""},
}

// Kept tables reported in the plan (never written).
// Schema 13: personnel (users incl. profile, departments, system roles, account provisioning records)
// and the account audit trail are part of the kept foundation — after the reset a legitimate system
// administrator can still log in, open accounts and manage people.
var kept = []string{"tenants", "users", "roles", "user_roles", "departments", "system_memberships", "account_provisions", "portfolio_scopes", "portfolio_memberships", "equipment_types", "manufacturers", "equipment_catalog_items", "audit_logs"}

// Plan is read-only.
type Plan struct {
	Database     string         `json:"database"`
	SystemID     string         `json:"databaseOid"`
	RealRuntimes int            `json:"realRuntimes"`
	Delete       map[string]int `json:"delete"`
	Keep         map[string]int `json:"keep"`
	Digest       string         `json:"digest"`
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func check(env Guard, want Guard) error {
	if env.Environment != want.Environment || strings.TrimRight(env.PublicURL, "/") != want.PublicURL {
		return fmt.Errorf("%w: environment %q / %q is not %s", ErrGuard, env.Environment, env.PublicURL, want.PublicURL)
	}
	return nil
}

func build(ctx context.Context, q queryer, want Guard) (Plan, error) {
	p := Plan{Delete: map[string]int{}, Keep: map[string]int{}}
	if err := q.QueryRowContext(ctx, `SELECT current_database(), (SELECT oid::text FROM pg_database WHERE datname=current_database())`).Scan(&p.Database, &p.SystemID); err != nil {
		return p, err
	}
	if p.Database != want.Database {
		return p, fmt.Errorf("%w: database %q is not %q", ErrGuard, p.Database, want.Database)
	}
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM devices WHERE source_mode='REAL'`).Scan(&p.RealRuntimes); err != nil {
		return p, err
	}
	for _, s := range steps {
		var n int
		query := `SELECT count(*) FROM ` + s.table
		if s.where != "" {
			query += ` WHERE ` + s.where
		}
		if err := q.QueryRowContext(ctx, query).Scan(&n); err != nil {
			return p, err
		}
		p.Delete[s.table] = n
	}
	for _, table := range kept {
		var n int
		query := `SELECT count(*) FROM ` + table
		switch table {
		case "manufacturers":
			query += ` WHERE project_id IS NULL`
		case "equipment_catalog_items":
			query += ` WHERE project_id IS NULL AND (manufacturer_id IS NULL OR manufacturer_id NOT IN (SELECT id FROM manufacturers WHERE project_id IS NOT NULL))`
		}
		if err := q.QueryRowContext(ctx, query).Scan(&n); err != nil {
			return p, err
		}
		p.Keep[table] = n
	}
	b, _ := json.Marshal(struct {
		Database, SystemID string
		Real               int
		Delete, Keep       map[string]int
	}{p.Database, p.SystemID, p.RealRuntimes, p.Delete, p.Keep})
	sum := sha256.Sum256(b)
	p.Digest = hex.EncodeToString(sum[:])
	if p.RealRuntimes != 0 {
		return p, fmt.Errorf("%w: %d REAL runtime identities exist; the reset never touches real hardware", ErrGuard, p.RealRuntimes)
	}
	return p, nil
}

// PlanReset is read-only: what would be deleted and kept, and the digest to approve.
func PlanReset(ctx context.Context, db *sql.DB, env, want Guard) (Plan, error) {
	if err := check(env, want); err != nil {
		return Plan{}, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback()
	return build(ctx, tx, want)
}

// Apply deletes the business data in one transaction (all-or-nothing) and records one audit event
// (action business_data_reset). Startup then refuses a non-empty Inventory (no demo re-seed).
func Apply(ctx context.Context, db *sql.DB, env, want Guard, approvedDigest, actor, requestID string) (Plan, error) {
	if err := check(env, want); err != nil {
		return Plan{}, err
	}
	if strings.TrimSpace(actor) == "" {
		return Plan{}, fmt.Errorf("%w: actor required", ErrGuard)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback()
	// Same lock as Inventory import and runtime-identity apply: no concurrent registration.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(7160003)`); err != nil {
		return Plan{}, err
	}
	locked := make([]string, 0, len(steps))
	for _, s := range steps {
		locked = append(locked, s.table)
	}
	if _, err = tx.ExecContext(ctx, `LOCK TABLE `+strings.Join(locked, ",")+` IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return Plan{}, err
	}
	p, err := build(ctx, tx, want)
	if err != nil {
		return p, err
	}
	if p.Digest != approvedDigest {
		return p, ErrDigestMismatch
	}
	for _, s := range steps {
		query := `DELETE FROM ` + s.table
		if s.where != "" {
			query += ` WHERE ` + s.where
		}
		if _, err = tx.ExecContext(ctx, query); err != nil {
			return p, fmt.Errorf("delete %s: %w", s.table, err)
		}
	}
	if err = auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Action: "business_data_reset", Result: "success", RequestID: requestID}); err != nil {
		return p, err
	}
	return p, tx.Commit()
}

// WasReset reports whether a business reset has been recorded in this database.
func WasReset(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit_logs WHERE action='business_data_reset' AND result='success'`).Scan(&n)
	return n > 0, err
}
