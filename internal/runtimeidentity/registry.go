// Package runtimeidentity registers SIMULATION / TEST runtime identities (devices rows) against
// existing business assets, and optionally binds an installed Equipment to the runtime
// (equipment_runtime_bindings) — R7. It is the controlled, deployment-side replacement for using
// the startup Inventory import to add runtimes to a live database: the Inventory import creates
// missing projects/assets silently, deletes the legacy Project-A seed, ignores asset status and
// writes no audit, so it is not used for that.
//
// Rules:
//   - only explicit, exact identities; never a wildcard, never a project or asset creation;
//   - sourceMode SIMULATION (id prefix SIM-) or TEST (prefix TEST-); REAL is refused — a REAL
//     runtime is a separate, reviewed provisioning step, and an existing identity is never changed
//     (a SIM identity can never become REAL through this tool or any API);
//   - the asset must exist in the named project and must not be RETIRED; one runtime per
//     (asset, sourceMode), as the device_asset_source index already enforces;
//   - an optional Equipment binding must name an active installation of that same asset that has
//     no active binding;
//   - plan is read-only; apply runs in one transaction under the Inventory import lock, re-plans,
//     requires the approved plan digest (bound to the target database), refuses any conflict or
//     invalid entry, creates only NEW rows and writes an audit row per runtime;
//   - verify reports whether every entry is registered exactly as requested.
package runtimeidentity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

const (
	StatusNew      = "NEW"
	StatusExisting = "EXISTING"
	StatusConflict = "CONFLICT"
	StatusInvalid  = "INVALID"
)

var (
	ErrRequest        = errors.New("invalid registration request")
	ErrDigestMismatch = errors.New("registration state changed since the approved plan; re-run plan and re-approve")
	ErrNotApplicable  = errors.New("plan has conflicting or invalid entries; nothing applied")
	ErrActorRequired  = errors.New("explicit administrator actor required")
)

// Entry is one requested runtime identity.
type Entry struct {
	RuntimeDeviceId         string  `json:"runtimeDeviceId" yaml:"runtimeDeviceId"`
	ProjectId               string  `json:"projectId" yaml:"projectId"`
	AssetDeviceId           string  `json:"assetDeviceId" yaml:"assetDeviceId"`
	SourceMode              string  `json:"sourceMode" yaml:"sourceMode"`
	EquipmentInstallationId *string `json:"equipmentInstallationId,omitempty" yaml:"equipmentInstallationId,omitempty"`
}

// Request is the explicit registration document.
type Request struct {
	TenantId string  `json:"tenantId" yaml:"tenantId"`
	Entries  []Entry `json:"entries" yaml:"entries"`
}

type Item struct {
	Entry
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Binding string `json:"binding"` // NONE | NEW | EXISTING
}

type Target struct {
	Database      string `json:"database"`
	Role          string `json:"role"`
	DatabaseOID   int64  `json:"databaseOid"`
	SchemaVersion int64  `json:"schemaVersion"`
}

type Plan struct {
	Target   Target `json:"target"`
	TenantId string `json:"tenantId" yaml:"tenantId"`
	Items    []Item `json:"items"`
	Digest   string `json:"digest"`
}

type Record struct {
	Target    Target   `json:"target"`
	RequestID string   `json:"requestId"`
	Actor     string   `json:"actor"`
	Digest    string   `json:"approvedDigest"`
	Created   []string `json:"created"`
	Bound     []string `json:"bound"`
	Existing  []string `json:"existing"`
	Result    string   `json:"result"` // success | noop
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// ValidateRequest checks the document shape before any database access.
func ValidateRequest(r Request) error {
	if !idPattern.MatchString(r.TenantId) {
		return fmt.Errorf("%w: tenantId", ErrRequest)
	}
	if len(r.Entries) < 1 || len(r.Entries) > 100 {
		return fmt.Errorf("%w: 1..100 entries", ErrRequest)
	}
	seen := map[string]bool{}
	for i, e := range r.Entries {
		for name, v := range map[string]string{"runtimeDeviceId": e.RuntimeDeviceId, "projectId": e.ProjectId, "assetDeviceId": e.AssetDeviceId} {
			if !idPattern.MatchString(v) {
				return fmt.Errorf("%w: entry %d %s", ErrRequest, i+1, name)
			}
		}
		if e.EquipmentInstallationId != nil && !idPattern.MatchString(*e.EquipmentInstallationId) {
			return fmt.Errorf("%w: entry %d equipmentInstallationId", ErrRequest, i+1)
		}
		switch {
		case e.SourceMode == "SIMULATION" && strings.HasPrefix(e.RuntimeDeviceId, "SIM-"):
		case e.SourceMode == "TEST" && strings.HasPrefix(e.RuntimeDeviceId, "TEST-"):
		case e.SourceMode == "REAL":
			return fmt.Errorf("%w: entry %d: REAL runtimes are not registered by this tool", ErrRequest, i+1)
		default:
			return fmt.Errorf("%w: entry %d: sourceMode must be SIMULATION (SIM-*) or TEST (TEST-*)", ErrRequest, i+1)
		}
		if e.RuntimeDeviceId == "cloud-api" || e.RuntimeDeviceId == "mosquitto" || seen[e.RuntimeDeviceId] {
			return fmt.Errorf("%w: entry %d: reserved or duplicate runtime id", ErrRequest, i+1)
		}
		seen[e.RuntimeDeviceId] = true
	}
	return nil
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func target(ctx context.Context, q queryer) (Target, error) {
	var t Target
	err := q.QueryRowContext(ctx, `SELECT current_database(), current_user, (SELECT oid::bigint FROM pg_database WHERE datname=current_database()), COALESCE((SELECT max(version_id) FROM goose_db_version WHERE is_applied),0)`).
		Scan(&t.Database, &t.Role, &t.DatabaseOID, &t.SchemaVersion)
	return t, err
}

func classify(ctx context.Context, q queryer, r Request) (Plan, error) {
	t, err := target(ctx, q)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Target: t, TenantId: r.TenantId, Items: []Item{}}
	entries := append([]Entry(nil), r.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].RuntimeDeviceId < entries[j].RuntimeDeviceId })
	for _, e := range entries {
		it := Item{Entry: e, Binding: "NONE"}
		it.Status, it.Reason, err = classifyEntry(ctx, q, r.TenantId, e, &it.Binding)
		if err != nil {
			return Plan{}, err
		}
		p.Items = append(p.Items, it)
	}
	b, err := json.Marshal(struct {
		Target   Target `json:"target"`
		TenantId string `json:"tenantId" yaml:"tenantId"`
		Items    []Item `json:"items"`
	}{p.Target, p.TenantId, p.Items})
	if err != nil {
		return Plan{}, err
	}
	sum := sha256.Sum256(b)
	p.Digest = hex.EncodeToString(sum[:])
	return p, nil
}

func classifyEntry(ctx context.Context, q queryer, tenant string, e Entry, binding *string) (string, string, error) {
	var projectTenant string
	err := q.QueryRowContext(ctx, `SELECT tenant_id FROM projects WHERE id=$1`, e.ProjectId).Scan(&projectTenant)
	if errors.Is(err, sql.ErrNoRows) {
		return StatusInvalid, "project does not exist (never created by this tool)", nil
	}
	if err != nil {
		return "", "", err
	}
	if projectTenant != tenant {
		return StatusInvalid, "project belongs to another tenant", nil
	}
	var assetProject, assetStatus string
	err = q.QueryRowContext(ctx, `SELECT project_id, asset_status FROM asset_devices WHERE id=$1`, e.AssetDeviceId).Scan(&assetProject, &assetStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return StatusInvalid, "asset does not exist", nil
	}
	if err != nil {
		return "", "", err
	}
	if assetProject != e.ProjectId {
		return StatusInvalid, "asset belongs to another project", nil
	}
	var curProject, curSource string
	var curAsset sql.NullString
	err = q.QueryRowContext(ctx, `SELECT project_id, source_mode, asset_device_id FROM devices WHERE id=$1`, e.RuntimeDeviceId).Scan(&curProject, &curSource, &curAsset)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	if exists && (curProject != e.ProjectId || curSource != e.SourceMode || !curAsset.Valid || curAsset.String != e.AssetDeviceId) {
		return StatusConflict, "runtime id already registered with a different project, asset or source (identities are immutable)", nil
	}
	if !exists {
		if assetStatus == "RETIRED" {
			return StatusInvalid, "asset is RETIRED (terminal)", nil
		}
		var other string
		err = q.QueryRowContext(ctx, `SELECT id FROM devices WHERE asset_device_id=$1 AND source_mode=$2`, e.AssetDeviceId, e.SourceMode).Scan(&other)
		if err == nil {
			return StatusConflict, "asset already has a " + e.SourceMode + " runtime (" + other + ")", nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", "", err
		}
	}
	if e.EquipmentInstallationId != nil {
		var eqAsset string
		var removed sql.NullTime
		err = q.QueryRowContext(ctx, `SELECT asset_id, valid_to FROM asset_equipment_installations WHERE id=$1`, *e.EquipmentInstallationId).Scan(&eqAsset, &removed)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && eqAsset != e.AssetDeviceId) {
			return StatusInvalid, "equipment installation is not on this asset", nil
		}
		if err != nil {
			return "", "", err
		}
		var activeRef string
		err = q.QueryRowContext(ctx, `SELECT source_ref FROM equipment_runtime_bindings WHERE equipment_installation_id=$1 AND valid_to IS NULL`, *e.EquipmentInstallationId).Scan(&activeRef)
		switch {
		case err == nil && activeRef == e.RuntimeDeviceId:
			*binding = "EXISTING"
		case err == nil:
			return StatusConflict, "equipment installation is already bound to " + activeRef, nil
		case errors.Is(err, sql.ErrNoRows):
			if removed.Valid {
				return StatusInvalid, "equipment installation is removed (terminal)", nil
			}
			if assetStatus == "RETIRED" {
				return StatusInvalid, "asset is RETIRED (terminal)", nil
			}
			*binding = "NEW"
		default:
			return "", "", err
		}
	}
	if exists && *binding != "NEW" {
		return StatusExisting, "", nil
	}
	if exists {
		return StatusExisting, "runtime exists; binding will be added", nil
	}
	return StatusNew, "", nil
}

// PlanRegistration is read-only.
func PlanRegistration(ctx context.Context, db *sql.DB, r Request) (Plan, error) {
	if err := ValidateRequest(r); err != nil {
		return Plan{}, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback()
	return classify(ctx, tx, r)
}

// Apply registers the NEW entries of the approved plan in one transaction.
func Apply(ctx context.Context, db *sql.DB, r Request, approvedDigest, actor, requestID string) (Record, error) {
	if strings.TrimSpace(actor) == "" {
		return Record{}, ErrActorRequired
	}
	if err := ValidateRequest(r); err != nil {
		return Record{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, err
	}
	defer tx.Rollback()
	// Same lock as the startup Inventory import: the two can never interleave.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(7160003)`); err != nil {
		return Record{}, err
	}
	// Terminal-state race protection: lock every referenced asset (sorted, the same parent-first
	// order as RetireAsset / RemoveEquipment), then every referenced installation, BEFORE the plan
	// is recomputed. A concurrent retire or removal either commits first (and the re-plan sees it
	// and refuses) or waits until this registration commits (and then refuses on the binding).
	assets := []string{}
	installs := []string{}
	for _, e := range r.Entries {
		assets = append(assets, e.AssetDeviceId)
		if e.EquipmentInstallationId != nil {
			installs = append(installs, *e.EquipmentInstallationId)
		}
	}
	sort.Strings(assets)
	sort.Strings(installs)
	for _, id := range assets {
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM asset_devices WHERE id=$1 FOR UPDATE`, id); err != nil {
			return Record{}, err
		}
	}
	for _, id := range installs {
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM asset_equipment_installations WHERE id=$1 FOR UPDATE`, id); err != nil {
			return Record{}, err
		}
	}
	p, err := classify(ctx, tx, r)
	if err != nil {
		return Record{}, err
	}
	if p.Digest != approvedDigest {
		return Record{}, ErrDigestMismatch
	}
	for _, it := range p.Items {
		if it.Status == StatusConflict || it.Status == StatusInvalid {
			return Record{}, fmt.Errorf("%w: %s %s", ErrNotApplicable, it.RuntimeDeviceId, it.Reason)
		}
	}
	rec := Record{Target: p.Target, RequestID: requestID, Actor: "host:" + actor, Digest: approvedDigest, Created: []string{}, Bound: []string{}, Existing: []string{}}
	for _, it := range p.Items {
		if it.Status == StatusNew {
			if _, err := tx.ExecContext(ctx, `INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES($1,$2,$3,$4)`, it.RuntimeDeviceId, it.ProjectId, it.AssetDeviceId, it.SourceMode); err != nil {
				return Record{}, err
			}
			rec.Created = append(rec.Created, it.RuntimeDeviceId)
			if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: rec.Actor, Project: it.ProjectId, Device: it.RuntimeDeviceId, Action: "runtime_identity_registered", Result: "success", RequestID: requestID}); err != nil {
				return Record{}, err
			}
		} else {
			rec.Existing = append(rec.Existing, it.RuntimeDeviceId)
		}
		if it.Binding == "NEW" {
			id := platform.ID()
			if _, err := tx.ExecContext(ctx, `INSERT INTO equipment_runtime_bindings(id,asset_id,equipment_installation_id,source_kind,source_ref,valid_from,created_by_deployment) VALUES($1,$2,$3,'RUNTIME_DEVICE',$4,now(),true)`, id, it.AssetDeviceId, *it.EquipmentInstallationId, it.RuntimeDeviceId); err != nil {
				return Record{}, err
			}
			snapshot, _ := json.Marshal(map[string]string{"id": id, "assetId": it.AssetDeviceId, "equipmentInstallationId": *it.EquipmentInstallationId, "sourceKind": "RUNTIME_DEVICE", "sourceRef": it.RuntimeDeviceId})
			sum := sha256.Sum256(snapshot)
			if _, err := tx.ExecContext(ctx, `INSERT INTO entity_revisions(project_id,asset_id,entity_type,entity_id,revision,recorded_at,effective_at,changed_by,request_id,changed_fields,snapshot_json,snapshot_sha256) VALUES($1,$2,'equipment_runtime_binding',$3,1,now(),now(),$4,$5,'{created}',$6,$7)`,
				it.ProjectId, it.AssetDeviceId, id, rec.Actor, requestID, snapshot, hex.EncodeToString(sum[:])); err != nil {
				return Record{}, err
			}
			if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: rec.Actor, Project: it.ProjectId, Device: it.RuntimeDeviceId, Action: "equipment_runtime_bound", Result: "success", RequestID: requestID}); err != nil {
				return Record{}, err
			}
			rec.Bound = append(rec.Bound, it.RuntimeDeviceId)
		}
	}
	rec.Result = "success"
	if len(rec.Created) == 0 && len(rec.Bound) == 0 {
		rec.Result = "noop"
		if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: rec.Actor, Action: "runtime_identity_registered", Result: "noop", RequestID: requestID}); err != nil {
			return Record{}, err
		}
	}
	return rec, tx.Commit()
}

// Report is the outcome of Verify.
type Report struct {
	Consistent bool `json:"consistent"`
	Plan       Plan `json:"plan"`
}

// Verify: every entry (and its binding, if requested) is registered exactly as requested.
func Verify(ctx context.Context, db *sql.DB, r Request) (Report, error) {
	p, err := PlanRegistration(ctx, db, r)
	if err != nil {
		return Report{}, err
	}
	ok := true
	for _, it := range p.Items {
		if it.Status != StatusExisting || it.Binding == "NEW" {
			ok = false
		}
	}
	return Report{Consistent: ok, Plan: p}, nil
}

// Fleet is the Fleet manifest describing exactly the requested runtimes (input for tools/fleet
// ACL generation, tools/certs device certificates and the simulator / cloud-api --fleet-file).
func Fleet(r Request) contract.FleetManifest {
	m := contract.FleetManifest{TenantId: r.TenantId, Devices: []contract.RuntimeDescriptor{}}
	for _, e := range r.Entries {
		asset := e.AssetDeviceId
		m.Devices = append(m.Devices, contract.RuntimeDescriptor{RuntimeDeviceId: e.RuntimeDeviceId, ProjectId: e.ProjectId, AssetDeviceId: &asset, SourceMode: e.SourceMode})
	}
	return m
}
