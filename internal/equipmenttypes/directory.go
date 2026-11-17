// Package equipmenttypes reconciles the database equipment type directory
// (equipment_types, the FK target of every Equipment catalog item and
// installation) with the code-owned registry (equipment.Types, which also gates
// spec validation).
//
// R6-BIZ-R1 F-02: no migration seeds equipment_types, so a freshly migrated
// schema-12 database has an empty directory and no Equipment is installable. This
// package is the only controlled way to initialize it:
//
//   - Plan is read-only and classifies every type in scope;
//   - Apply inserts only missing registry types, in one transaction, under a table
//     lock, and only when the caller presents the digest of the plan it approved
//     (any state change since the approval refuses the apply);
//   - Verify reports drift between the database and the registry.
//
// Codes always come from the registry: a code that is not registered is refused,
// never inserted. Existing rows are never updated: a DISABLED type stays DISABLED,
// a conflicting row refuses the apply, and existing installations and history are
// never touched.
package equipmenttypes

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/equipment"
)

// Classification of one type in scope.
const (
	StatusToCreate     = "TO_CREATE"    // registered in code, absent from the database
	StatusExisting     = "EXISTING"     // present, ACTIVE, identical to the registry
	StatusDisabled     = "DISABLED"     // present, DISABLED, identical otherwise — never re-enabled
	StatusConflict     = "CONFLICT"     // present but name / domain / spec version differ from the registry
	StatusUnregistered = "UNREGISTERED" // present in the database, unknown to the registry (not installable)
)

var (
	ErrUnknownType    = errors.New("type code is not in the code registry")
	ErrEmptyScope     = errors.New("explicit scope required (type codes or all registered types)")
	ErrDigestMismatch = errors.New("directory state changed since the approved plan; re-run plan and re-approve")
	ErrConflict       = errors.New("conflicting types in scope; resolve them or narrow the scope")
	ErrActorRequired  = errors.New("explicit administrator actor required")
)

// Item is the classification of one type code.
type Item struct {
	TypeCode string `json:"typeCode"`
	Status   string `json:"status"`
	// Expected is the registry definition (absent for UNREGISTERED rows).
	Expected *Definition `json:"expected,omitempty"`
	// Actual is the database row (absent for TO_CREATE).
	Actual *Definition `json:"actual,omitempty"`
}

// Definition is the comparable part of a type row.
type Definition struct {
	DisplayName       string `json:"displayName"`
	Domain            string `json:"domain"`
	SpecSchemaVersion string `json:"specSchemaVersion"`
	Status            string `json:"status"`
}

// Target identifies the database a plan was computed against, without any secret: database
// name, role, database OID and applied schema version. It is part of the digest, so a plan
// approved on one database can never be applied to another.
type Target struct {
	Database      string `json:"database"`
	Role          string `json:"role"`
	DatabaseOID   int64  `json:"databaseOid"`
	SchemaVersion int64  `json:"schemaVersion"`
}

// Plan is the read-only classification of a scope.
type Plan struct {
	Target       Target   `json:"target"`
	Scope        []string `json:"scope"`
	Items        []Item   `json:"items"`
	ToCreate     []string `json:"toCreate"`
	Existing     []string `json:"existing"`
	Disabled     []string `json:"disabled"`
	Conflicts    []string `json:"conflicts"`
	Unregistered []string `json:"unregistered"`
	// Digest identifies this exact plan (scope + every row's state). Apply refuses
	// unless the caller presents the digest of the plan that was approved.
	Digest string `json:"digest"`
}

// Record is the complete, credential-free record of one Apply.
type Record struct {
	Target    Target    `json:"target"`
	RequestID string    `json:"requestId"`
	Actor     string    `json:"actor"`
	AppliedAt time.Time `json:"appliedAt"`
	Digest    string    `json:"approvedDigest"`
	Scope     []string  `json:"scope"`
	Created   []string  `json:"created"`
	Existing  []string  `json:"existing"`
	Disabled  []string  `json:"disabled"`
	// Result is "success" when rows were created, "noop" when the approved plan had nothing to
	// create (the directory was already initialized; only the audit row is written).
	Result string `json:"result"`
}

// Scope resolves an explicit scope: all registered types, or the given codes,
// each of which must be registered. An empty request is refused.
func Scope(all bool, codes []string) ([]string, error) {
	registered := map[string]bool{}
	var every []string
	for _, t := range equipment.Types() {
		registered[t.Code] = true
		every = append(every, t.Code)
	}
	if all {
		if len(codes) > 0 {
			return nil, errors.New("choose either all registered types or explicit codes, not both")
		}
		sort.Strings(every)
		return every, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range codes {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !registered[c] {
			return nil, fmt.Errorf("%w: %q", ErrUnknownType, c)
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, ErrEmptyScope
	}
	sort.Strings(out)
	return out, nil
}

func registryDefinitions() map[string]Definition {
	out := map[string]Definition{}
	for _, t := range equipment.Types() {
		out[t.Code] = Definition{DisplayName: t.NameZh, Domain: t.Domain, SpecSchemaVersion: equipment.SpecSchemaVersion, Status: "ACTIVE"}
	}
	return out
}

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// classify reads the whole directory (it is small and bounded by the registry)
// and classifies the scope. Unregistered rows are always reported, whatever the
// scope, because they are drift the operator must see.
func classify(ctx context.Context, q querier, scope []string) (Plan, error) {
	var target Target
	if err := q.QueryRowContext(ctx, `SELECT current_database(), current_user, (SELECT oid::bigint FROM pg_database WHERE datname=current_database()), COALESCE((SELECT max(version_id) FROM goose_db_version WHERE is_applied),0)`).
		Scan(&target.Database, &target.Role, &target.DatabaseOID, &target.SchemaVersion); err != nil {
		return Plan{}, err
	}
	rows, err := q.QueryContext(ctx, `SELECT type_code, display_name, domain, spec_schema_version, status FROM equipment_types ORDER BY type_code`)
	if err != nil {
		return Plan{}, err
	}
	defer rows.Close()
	db := map[string]Definition{}
	for rows.Next() {
		var code string
		var d Definition
		if err := rows.Scan(&code, &d.DisplayName, &d.Domain, &d.SpecSchemaVersion, &d.Status); err != nil {
			return Plan{}, err
		}
		db[code] = d
	}
	if err := rows.Err(); err != nil {
		return Plan{}, err
	}
	reg := registryDefinitions()
	p := Plan{Target: target, Scope: append([]string(nil), scope...), Items: []Item{}, ToCreate: []string{}, Existing: []string{}, Disabled: []string{}, Conflicts: []string{}, Unregistered: []string{}}
	for _, code := range scope {
		want := reg[code]
		item := Item{TypeCode: code, Expected: &want}
		got, ok := db[code]
		switch {
		case !ok:
			item.Status = StatusToCreate
			p.ToCreate = append(p.ToCreate, code)
		case got.DisplayName != want.DisplayName || got.Domain != want.Domain || got.SpecSchemaVersion != want.SpecSchemaVersion:
			item.Status, item.Actual = StatusConflict, &got
			p.Conflicts = append(p.Conflicts, code)
		case got.Status == "DISABLED":
			item.Status, item.Actual = StatusDisabled, &got
			p.Disabled = append(p.Disabled, code)
		default:
			item.Status, item.Actual = StatusExisting, &got
			p.Existing = append(p.Existing, code)
		}
		p.Items = append(p.Items, item)
	}
	var unregistered []string
	for code := range db {
		if _, ok := reg[code]; !ok {
			unregistered = append(unregistered, code)
		}
	}
	sort.Strings(unregistered)
	for _, code := range unregistered {
		got := db[code]
		p.Items = append(p.Items, Item{TypeCode: code, Status: StatusUnregistered, Actual: &got})
		p.Unregistered = append(p.Unregistered, code)
	}
	b, err := json.Marshal(struct {
		Target Target   `json:"target"`
		Scope  []string `json:"scope"`
		Items  []Item   `json:"items"`
	}{p.Target, p.Scope, p.Items})
	if err != nil {
		return Plan{}, err
	}
	sum := sha256.Sum256(b)
	p.Digest = hex.EncodeToString(sum[:])
	return p, nil
}

// PlanTypes is read-only: it never writes and runs in a read-only transaction.
func PlanTypes(ctx context.Context, db *sql.DB, scope []string) (Plan, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback()
	return classify(ctx, tx, scope)
}

// Apply creates the missing registry types of the approved scope in one
// transaction. The equipment_types table is locked against concurrent writers
// (installs, which only take row share locks, are not blocked), the plan is
// recomputed inside the lock and must match approvedDigest exactly, and any
// conflict in scope refuses the whole apply. Existing rows — ACTIVE, DISABLED or
// conflicting — are never updated. One audit row records the operation; the
// returned Record is the complete operation record for the operator's log.
func Apply(ctx context.Context, db *sql.DB, scope []string, approvedDigest, actor, requestID string) (Record, error) {
	if strings.TrimSpace(actor) == "" {
		return Record{}, ErrActorRequired
	}
	if len(scope) == 0 {
		return Record{}, ErrEmptyScope
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `LOCK TABLE equipment_types IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return Record{}, err
	}
	p, err := classify(ctx, tx, scope)
	if err != nil {
		return Record{}, err
	}
	if p.Digest != approvedDigest {
		return Record{}, ErrDigestMismatch
	}
	if len(p.Conflicts) > 0 {
		return Record{}, fmt.Errorf("%w: %s", ErrConflict, strings.Join(p.Conflicts, ","))
	}
	reg := registryDefinitions()
	for _, code := range p.ToCreate {
		d := reg[code]
		res, err := tx.ExecContext(ctx, `INSERT INTO equipment_types(type_code, display_name, domain, spec_schema_version, status) VALUES($1,$2,$3,$4,'ACTIVE') ON CONFLICT (type_code) DO NOTHING`,
			code, d.DisplayName, d.Domain, d.SpecSchemaVersion)
		if err != nil {
			return Record{}, err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return Record{}, ErrDigestMismatch
		}
	}
	result := "success"
	if len(p.ToCreate) == 0 {
		result = "noop"
	}
	actorID := "host:" + actor
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actorID, Action: "equipment_types_initialized", Result: result, RequestID: requestID}); err != nil {
		return Record{}, err
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT now()`).Scan(&now); err != nil {
		return Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return Record{}, err
	}
	return Record{Target: p.Target, RequestID: requestID, Actor: actorID, AppliedAt: now.UTC(), Digest: approvedDigest, Scope: p.Scope, Created: p.ToCreate, Existing: p.Existing, Disabled: p.Disabled, Result: result}, nil
}

// Report is the outcome of Verify.
type Report struct {
	Consistent bool `json:"consistent"`
	Plan       Plan `json:"plan"`
	// Installable lists the scope types an install request can use right now.
	Installable []string `json:"installable"`
}

// Verify compares the database with the registry for the scope. The directory is
// consistent when no scope type is missing or conflicting and no unregistered row
// exists. DISABLED types are consistent (a deliberate business decision) but are
// not installable.
func Verify(ctx context.Context, db *sql.DB, scope []string) (Report, error) {
	p, err := PlanTypes(ctx, db, scope)
	if err != nil {
		return Report{}, err
	}
	return Report{
		Consistent:  len(p.ToCreate) == 0 && len(p.Conflicts) == 0 && len(p.Unregistered) == 0,
		Plan:        p,
		Installable: append([]string{}, p.Existing...),
	}, nil
}
