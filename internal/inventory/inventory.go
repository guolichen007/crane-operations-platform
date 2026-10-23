package inventory

import (
	"context"
	"database/sql"
	"errors"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/fleet"
	"strings"
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
		_, err = tx.ExecContext(ctx, `INSERT INTO projects(id,tenant_id,name,lifecycle,province,city,county,location_label,admin_code,schedule_text,notes) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(id) DO UPDATE SET name=excluded.name,lifecycle=excluded.lifecycle,province=excluded.province,city=excluded.city,county=excluded.county,location_label=excluded.location_label,admin_code=excluded.admin_code,schedule_text=excluded.schedule_text,notes=excluded.notes,updated_at=now()`, p.Id, d.TenantId, p.Name, p.Lifecycle, p.Province, p.City, p.County, p.LocationLabel, p.AdminCode, p.ScheduleText, p.Notes)
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
		_, err = tx.ExecContext(ctx, `INSERT INTO asset_devices(id,project_id,display_name,crane_model,rated_capacity_t,span_m,work_class,grab_type,lifecycle) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,crane_model=excluded.crane_model,rated_capacity_t=excluded.rated_capacity_t,span_m=excluded.span_m,work_class=excluded.work_class,grab_type=excluded.grab_type,lifecycle=excluded.lifecycle,updated_at=now()`, a.AssetDeviceId, a.ProjectId, a.DisplayName, a.CraneModel, a.RatedCapacityT, a.SpanM, a.WorkClass, a.GrabType, a.Lifecycle)
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

func Projects(ctx context.Context, db *sql.DB) ([]contract.InventoryProject, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.id,p.name,p.lifecycle,p.province,p.city,p.county,p.location_label,p.admin_code,p.schedule_text,p.notes,
 (SELECT count(*) FROM asset_devices a WHERE a.project_id=p.id),
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
	rows, err := db.QueryContext(ctx, `SELECT a.id,a.project_id,a.display_name,a.crane_model,a.rated_capacity_t,a.span_m,a.work_class,a.grab_type,a.lifecycle,d.source_mode,d.id,s.payload_json#>>'{presence,connectivity}' FROM asset_devices a LEFT JOIN LATERAL(SELECT id,source_mode FROM devices WHERE asset_device_id=a.id ORDER BY CASE source_mode WHEN 'REAL' THEN 0 WHEN 'SIMULATION' THEN 1 ELSE 2 END,id LIMIT 1) d ON true LEFT JOIN device_shadow s ON s.device_id=d.id ORDER BY a.id`)
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
