package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/fleet"
)

func Load(path string) (contract.InventoryDocument, error) {
	var d contract.InventoryDocument
	if err := fleet.Read(path, &d); err != nil {
		return d, err
	}
	return d, Validate(d)
}
func Validate(d contract.InventoryDocument) error {
	if err := contract.ValidateDocument("InventoryDocument", d); err != nil {
		return err
	}
	if len(d.Projects) < 1 || len(d.Projects) > 1000 || len(d.AssetDevices) > 10000 || len(d.RuntimeDevices) > 10000 {
		return errors.New("inventory size outside bounds")
	}
	projects := map[string]contract.InventoryProjectInput{}
	assets := map[string]contract.InventoryAssetInput{}
	runtimes := map[string]bool{}
	sources := map[string]bool{}
	for _, p := range d.Projects {
		if _, ok := projects[p.Id]; ok {
			return errors.New("duplicate project id")
		}
		projects[p.Id] = p
	}
	for _, a := range d.AssetDevices {
		if _, ok := assets[a.AssetDeviceId]; ok {
			return errors.New("duplicate asset id")
		}
		p, ok := projects[a.ProjectId]
		if !ok || a.Lifecycle != p.Lifecycle {
			return errors.New("asset project/lifecycle mismatch")
		}
		assets[a.AssetDeviceId] = a
	}
	for _, r := range d.RuntimeDevices {
		if runtimes[r.RuntimeDeviceId] {
			return errors.New("duplicate runtime id")
		}
		runtimes[r.RuntimeDeviceId] = true
		if _, ok := projects[r.ProjectId]; !ok {
			return errors.New("runtime references unknown project")
		}
		if r.AssetDeviceId == nil {
			return errors.New("inventory runtime requires a known asset")
		}
		a, ok := assets[*r.AssetDeviceId]
		if !ok || a.ProjectId != r.ProjectId {
			return errors.New("runtime asset/project mismatch")
		}
		source := *r.AssetDeviceId + "/" + r.SourceMode
		if sources[source] {
			return errors.New("duplicate asset source")
		}
		sources[source] = true
		if r.SourceMode == "SIMULATION" && !strings.HasPrefix(r.RuntimeDeviceId, "SIM-") || r.SourceMode == "REAL" && !strings.HasPrefix(r.RuntimeDeviceId, "EDGE-") {
			return errors.New("SIM and REAL identities must be distinct")
		}
	}
	return nil
}
func Fleet(d contract.InventoryDocument) contract.FleetManifest {
	m := contract.FleetManifest{TenantId: d.TenantId, Devices: []contract.RuntimeDescriptor{}}
	for _, r := range d.RuntimeDevices {
		if r.SourceMode == "SIMULATION" {
			m.Devices = append(m.Devices, r)
		}
	}
	return m
}

// Import is atomic. Runtime provenance is immutable even when descriptive metadata changes.
func Import(ctx context.Context, db *sql.DB, d contract.InventoryDocument) error {
	if err := Validate(d); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize inventory imports so concurrent upserts cannot reassign global IDs.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(7160003)`); err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, `INSERT INTO tenants(id,name) VALUES($1,$1) ON CONFLICT DO NOTHING`, d.TenantId); err != nil {
		return err
	}
	for _, p := range d.Projects {
		var tenant string
		err = tx.QueryRowContext(ctx, `SELECT tenant_id FROM projects WHERE id=$1 FOR UPDATE`, p.Id).Scan(&tenant)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && tenant != d.TenantId {
			return errors.New("project tenant is immutable")
		}
		// First import seeds BUSINESS_OWNED fields; a later import must not
		// overwrite them (existing objects are only subject to the immutable
		// tenant/provenance checks above).
		_, err = tx.ExecContext(ctx, `INSERT INTO projects(id,tenant_id,name,lifecycle,province,city,county,location_label,admin_code,schedule_text,notes) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(id) DO NOTHING`, p.Id, d.TenantId, p.Name, p.Lifecycle, p.Province, p.City, p.County, p.LocationLabel, p.AdminCode, p.ScheduleText, p.Notes)
		if err != nil {
			return err
		}
	}
	for _, a := range d.AssetDevices {
		var project string
		err = tx.QueryRowContext(ctx, `SELECT project_id FROM asset_devices WHERE id=$1 FOR UPDATE`, a.AssetDeviceId).Scan(&project)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && project != a.ProjectId {
			return errors.New("asset project is immutable")
		}
		// BUSINESS_OWNED fields (display_name, model, capacity, span, work_class,
		// grab_type, lifecycle) are seeded on first import only; a later import
		// must not overwrite them. Runtime identity/provenance stays immutable.
		_, err = tx.ExecContext(ctx, `INSERT INTO asset_devices(id,project_id,display_name,crane_model,rated_capacity_t,span_m,work_class,grab_type,lifecycle) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(id) DO NOTHING`, a.AssetDeviceId, a.ProjectId, a.DisplayName, a.CraneModel, a.RatedCapacityT, a.SpanM, a.WorkClass, a.GrabType, a.Lifecycle)
		if err != nil {
			return err
		}
	}
	for _, r := range d.RuntimeDevices {
		var project, source string
		var asset *string
		err = tx.QueryRowContext(ctx, `SELECT project_id,source_mode,asset_device_id FROM devices WHERE id=$1 FOR UPDATE`, r.RuntimeDeviceId).Scan(&project, &source, &asset)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && (project != r.ProjectId || source != r.SourceMode || asset == nil || *asset != *r.AssetDeviceId) {
			return errors.New("runtime provenance is immutable; create a new identity")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, r.RuntimeDeviceId, r.ProjectId, r.AssetDeviceId, r.SourceMode)
		if err != nil {
			return err
		}
	}
	// Remove only the empty development seed, never an existing customer project.
	_, err = tx.ExecContext(ctx, `DELETE FROM projects WHERE id='Project-A' AND tenant_id='demo' AND lifecycle IS NULL AND NOT EXISTS(SELECT 1 FROM devices WHERE project_id='Project-A') AND NOT EXISTS(SELECT 1 FROM asset_devices WHERE project_id='Project-A')`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Inventory operating scope (H2-1A, docs/04-前端与可视化/Homepage-H2-0-运营数据契约.md §2.6):
// an Inventory asset is an asset_devices row that belongs to a registered
// Inventory project (projects.lifecycle IS NOT NULL) and is not RETIRED. DRAFT,
// ACTIVE and OUT_OF_SERVICE stay registered. RETIRED rows are excluded here only;
// the formal asset center (/api/v1/assets) keeps serving them as history.
// Projects().assetDeviceCount and Assets() must use the same predicate so the
// per-project counts always sum to the asset list.
const inventoryAssetStatus = `a.asset_status <> 'RETIRED'`

func Projects(ctx context.Context, db *sql.DB) ([]contract.InventoryProject, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.id,p.name,p.lifecycle,p.province,p.city,p.county,p.location_label,p.admin_code,p.schedule_text,p.notes,
 (SELECT count(*) FROM asset_devices a WHERE a.project_id=p.id AND `+inventoryAssetStatus+`),
 (SELECT count(*) FROM devices d WHERE d.project_id=p.id),
 (SELECT count(*) FROM devices d JOIN device_shadow s ON s.device_id=d.id WHERE d.project_id=p.id AND d.source_mode='REAL' AND s.payload_json#>>'{presence,connectivity}'='MQTT_CONNECTED'),
 (SELECT count(*) FROM devices d WHERE d.project_id=p.id AND d.source_mode='SIMULATION')
 FROM projects p WHERE p.lifecycle IS NOT NULL ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []contract.InventoryProject{}
	for rows.Next() {
		var p contract.InventoryProject
		err = rows.Scan(&p.Id, &p.Name, &p.Lifecycle, &p.Province, &p.City, &p.County, &p.LocationLabel, &p.AdminCode, &p.ScheduleText, &p.Notes, &p.AssetDeviceCount, &p.RuntimeDeviceCount, &p.RealConnectedCount, &p.SimulatedCount)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
func Assets(ctx context.Context, db *sql.DB) ([]contract.AssetDevice, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.id,a.project_id,a.display_name,a.crane_model,a.rated_capacity_t,a.span_m,a.work_class,a.grab_type,a.lifecycle,d.source_mode,d.id,s.payload_json#>>'{presence,connectivity}' FROM asset_devices a JOIN projects p ON p.id=a.project_id AND p.lifecycle IS NOT NULL LEFT JOIN LATERAL(SELECT id,source_mode FROM devices WHERE asset_device_id=a.id ORDER BY CASE source_mode WHEN 'REAL' THEN 0 WHEN 'SIMULATION' THEN 1 ELSE 2 END,id LIMIT 1) d ON true LEFT JOIN device_shadow s ON s.device_id=d.id WHERE `+inventoryAssetStatus+` ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []contract.AssetDevice{}
	for rows.Next() {
		var a contract.AssetDevice
		err = rows.Scan(&a.AssetDeviceId, &a.ProjectId, &a.DisplayName, &a.CraneModel, &a.RatedCapacityT, &a.SpanM, &a.WorkClass, &a.GrabType, &a.Lifecycle, &a.RuntimeSource, &a.RuntimeDeviceId, &a.Connectivity)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

// ResetLegacy deletes only the exact original 20 development SIM identities.
func ResetLegacy(ctx context.Context, db *sql.DB, env string) error {
	if env != "development" {
		return errors.New("legacy reset requires APP_ENV=development")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var unsafe int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM devices WHERE id ~ '^SIM-(00[1-9]|01[0-9]|020)$' AND (source_mode<>'SIMULATION' OR asset_device_id IS NOT NULL)`).Scan(&unsafe); err != nil {
		return err
	}
	if unsafe > 0 {
		return errors.New("legacy ids include non-legacy provenance; reset refused")
	}
	for _, table := range []string{"device_commands", "device_live_state", "live_sessions", "remote_sessions", "alarms", "events", "telemetry_samples", "device_heartbeats", "device_shadow", "devices"} {
		// Table names are compile-time constants; IDs/source are fixed, not user SQL.
		_, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+map[bool]string{true: "id", false: "device_id"}[table == "devices"]+` IN (SELECT id FROM devices WHERE id ~ '^SIM-(00[1-9]|01[0-9]|020)$' AND source_mode='SIMULATION' AND asset_device_id IS NULL)`)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------------------------
// Q3: formal project → Inventory registration.
//
// `projects` is the single entity for formal and Inventory projects (one id). A formal project
// (created by ApproveIntake: portfolio_scope_id set, approved intake, lifecycle NULL) becomes an
// Inventory project by setting lifecycle + the administrative fields ONCE. Province / city /
// county are DERIVED from the administrative index (never typed by the user), so the level and
// the display names cannot contradict each other; no coordinates are stored or invented — the
// map places the project at administrative-region precision only. The caller has already proved
// location_write on this project (ProjectGrant); this function re-checks nothing about the user
// but locks the row and refuses anything that is not an approved, unregistered formal project.

var (
	// ErrNotFormalProject: unknown id, legacy/Inventory-only project or not from an approved intake.
	ErrNotFormalProject = errors.New("not an approved formal project")
	// ErrAlreadyRegistered: lifecycle is already set (first registration only; HTTP 409).
	ErrAlreadyRegistered = errors.New("project is already registered in Inventory")
	// ErrInvalidRegistration: contract / region violation (HTTP 422).
	ErrInvalidRegistration = errors.New("invalid inventory registration")
)

// Region is the administrative resolution of a code: names per level, from the index.
type Region struct {
	Province, City, County string
	Level                  string
}

// RegionResolver resolves an administrative code (internal/geo). ok=false = unknown code.
type RegionResolver func(code int) (Region, bool)

// RegisterFormalProject sets the Inventory fields of one formal project in a single
// transaction together with its audit event. Repeated / concurrent requests: the row lock +
// `lifecycle IS NULL` check make exactly one succeed; later ones get ErrAlreadyRegistered and
// write nothing (no overwrite of an imported or already registered project).
func RegisterFormalProject(ctx context.Context, db *sql.DB, project string, in contract.ProjectInventoryRegistration, resolve RegionResolver, event auditlog.Event) (contract.InventoryProject, error) {
	var out contract.InventoryProject
	if err := contract.ValidateDocument("ProjectInventoryRegistration", in); err != nil {
		return out, fmt.Errorf("%w: %v", ErrInvalidRegistration, err)
	}
	region, ok := resolve(in.AdminCode)
	if !ok || region.Level == "country" {
		return out, fmt.Errorf("%w: adminCode %d is not a province, prefecture or county in the administrative index", ErrInvalidRegistration, in.AdminCode)
	}
	label := strings.TrimSpace(in.LocationLabel)
	if label == "" {
		label = strings.Join(nonEmpty(region.Province, region.City, region.County), " ")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var lifecycle sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT p.lifecycle FROM projects p JOIN project_intakes pi ON pi.formal_project_id=p.id AND pi.status='APPROVED'
 AND pi.portfolio_scope_id=p.portfolio_scope_id AND pi.tenant_id=p.tenant_id WHERE p.id=$1 FOR UPDATE OF p`, project).Scan(&lifecycle)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFormalProject
	}
	if err != nil {
		return out, err
	}
	if lifecycle.Valid {
		return out, ErrAlreadyRegistered
	}
	res, err := tx.ExecContext(ctx, `UPDATE projects SET lifecycle=$2,province=$3,city=$4,county=$5,location_label=$6,admin_code=$7,schedule_text=$8,notes=$9,updated_at=now()
 WHERE id=$1 AND lifecycle IS NULL`, project, in.Lifecycle, region.Province, region.City, region.County, label, in.AdminCode, strings.TrimSpace(in.ScheduleText), strings.TrimSpace(in.Notes))
	if err != nil {
		return out, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return out, ErrAlreadyRegistered
	}
	event.Project, event.Action, event.Result = project, "project_inventory_registered", fmt.Sprintf("success:%s:%d", in.Lifecycle, in.AdminCode)
	if err = auditlog.Write(ctx, tx, event); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	projects, err := Projects(ctx, db)
	if err != nil {
		return out, err
	}
	for _, p := range projects {
		if p.Id == project {
			return p, nil
		}
	}
	return out, errors.New("registered project not readable")
}

func nonEmpty(values ...string) []string {
	out := []string{}
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// PendingFormalProject is an approved formal project, not yet in Inventory, on which the user
// holds a project grant.
type PendingFormalProject struct {
	ID, Name, PortfolioScopeID, Role string
	Phase                            sql.NullString
	AssetCount                       int
}

// PendingFormalProjects lists the caller's granted, approved, unregistered formal projects.
func PendingFormalProjects(ctx context.Context, db *sql.DB, user string) ([]PendingFormalProject, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.id,p.name,p.portfolio_scope_id,p.project_phase,m.role,
 (SELECT count(*) FROM asset_devices a WHERE a.project_id=p.id AND `+inventoryAssetStatus+`)
FROM projects p JOIN project_intakes pi ON pi.formal_project_id=p.id AND pi.status='APPROVED' AND pi.portfolio_scope_id=p.portfolio_scope_id AND pi.tenant_id=p.tenant_id
JOIN project_memberships m ON m.project_id=p.id AND m.user_id=$1 JOIN users u ON u.id=m.user_id AND u.enabled
WHERE p.lifecycle IS NULL ORDER BY p.id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingFormalProject{}
	for rows.Next() {
		var v PendingFormalProject
		if err = rows.Scan(&v.ID, &v.Name, &v.PortfolioScopeID, &v.Phase, &v.Role, &v.AssetCount); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
