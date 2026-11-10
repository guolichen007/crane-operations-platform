package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

const tenant = "uat"

// ids returns the exact identifiers this tool owns. Cleanup never uses LIKE patterns: a project,
// user, asset or runtime that is not in these lists is never deleted, whatever its name.
func ids() (projectIDs, userIDs, assetIDs, runtimeIDs []string) {
	for _, p := range projects {
		projectIDs = append(projectIDs, p.ID)
	}
	for _, u := range users {
		userIDs = append(userIDs, u.ID)
	}
	for _, a := range assets() {
		assetIDs = append(assetIDs, a.ID)
	}
	for _, r := range runtimes() {
		runtimeIDs = append(runtimeIDs, r.ID)
	}
	return
}

// ErrOwnership: something in (or attached to) the UAT namespace was not created by this tool.
// Cleanup refuses as a whole instead of deleting it.
var ErrOwnership = errors.New("refusing: UAT namespace contains data not owned by uatdata")

// ownership is checked inside the cleanup transaction (rows locked) before any delete:
//   - every existing UAT project belongs to tenant 'uat' and carries the dataset note;
//   - every asset / runtime in a UAT project is one of the tool's exact ids;
//   - no non-UAT account holds a grant on a UAT project (real accounts keep their permissions);
//   - no UAT account holds a grant on a non-UAT project (nothing outside UAT changes);
//   - every existing UAT user is bound to the expected issuer.
func ownership(ctx context.Context, tx *sql.Tx, issuer string) error {
	p, u, a, r := ids()
	checks := []struct {
		name, sql string
		args      []any
	}{
		{"foreign project", `SELECT count(*) FROM (SELECT 1 FROM projects WHERE id = ANY($1) AND (tenant_id <> 'uat' OR position('H3 合成验收数据' in notes) <> 1) FOR UPDATE) x`, []any{p}},
		{"foreign asset", `SELECT count(*) FROM asset_devices WHERE project_id = ANY($1) AND NOT (id = ANY($2))`, []any{p, a}},
		{"foreign runtime", `SELECT count(*) FROM devices WHERE project_id = ANY($1) AND NOT (id = ANY($2))`, []any{p, r}},
		{"foreign location/catalog", `SELECT (SELECT count(*) FROM asset_locations WHERE project_id = ANY($1)) + (SELECT count(*) FROM equipment_catalog_items WHERE project_id = ANY($1)) + (SELECT count(*) FROM manufacturers WHERE project_id = ANY($1))`, []any{p}},
		{"non-UAT account granted on UAT project", `SELECT count(*) FROM project_memberships WHERE project_id = ANY($1) AND NOT (user_id = ANY($2))`, []any{p, u}},
		{"UAT account granted outside UAT", `SELECT count(*) FROM project_memberships WHERE user_id = ANY($1) AND NOT (project_id = ANY($2))`, []any{u, p}},
		{"UAT account with portfolio/legacy role", `SELECT (SELECT count(*) FROM portfolio_memberships WHERE user_id = ANY($1)) + (SELECT count(*) FROM user_roles WHERE user_id = ANY($1))`, []any{u}},
		{"UAT account bound to another issuer", `SELECT count(*) FROM (SELECT 1 FROM users WHERE id = ANY($1) AND issuer IS DISTINCT FROM $2 FOR UPDATE) x`, []any{u, issuer}},
	}
	for _, c := range checks {
		var n int
		if err := tx.QueryRowContext(ctx, c.sql, c.args...).Scan(&n); err != nil {
			return fmt.Errorf("ownership check %s: %w", c.name, err)
		}
		if n > 0 {
			return fmt.Errorf("%w (%s: %d)", ErrOwnership, c.name, n)
		}
	}
	return nil
}

// cleanup removes ONLY the tool's exact ids, in foreign-key order, in the caller's transaction,
// after ownership() passed. audit_logs are append-only security facts and are retained. Any other
// row still referencing a UAT id makes the delete fail on its foreign key and the whole
// transaction rolls back — nothing is cascaded or silently removed.
func cleanup(ctx context.Context, tx *sql.Tx, issuer string) (map[string]int64, error) {
	if err := ownership(ctx, tx, issuer); err != nil {
		return nil, err
	}
	p, u, a, r := ids()
	steps := []struct {
		name, sql string
		args      []any
	}{
		{"alarms", `DELETE FROM alarms WHERE device_id = ANY($1)`, []any{r}},
		{"events", `DELETE FROM events WHERE device_id = ANY($1)`, []any{r}},
		{"telemetry_samples", `DELETE FROM telemetry_samples WHERE device_id = ANY($1)`, []any{r}},
		{"device_heartbeats", `DELETE FROM device_heartbeats WHERE device_id = ANY($1)`, []any{r}},
		{"device_live_state", `DELETE FROM device_live_state WHERE device_id = ANY($1)`, []any{r}},
		{"device_commands", `DELETE FROM device_commands WHERE device_id = ANY($1)`, []any{r}},
		{"live_sessions", `DELETE FROM live_sessions WHERE device_id = ANY($1) OR user_id = ANY($2)`, []any{r, u}},
		{"remote_sessions", `DELETE FROM remote_sessions WHERE device_id = ANY($1) OR user_id = ANY($2)`, []any{r, u}},
		{"device_shadow", `DELETE FROM device_shadow WHERE device_id = ANY($1)`, []any{r}},
		{"equipment_runtime_bindings", `DELETE FROM equipment_runtime_bindings WHERE asset_id = ANY($1)`, []any{a}},
		{"equipment_relations", `DELETE FROM equipment_relations WHERE asset_id = ANY($1)`, []any{a}},
		{"asset_equipment_installations", `DELETE FROM asset_equipment_installations WHERE asset_id = ANY($1)`, []any{a}},
		{"asset_installations", `DELETE FROM asset_installations WHERE asset_id = ANY($1)`, []any{a}},
		{"asset_timeline_events", `DELETE FROM asset_timeline_events WHERE asset_id = ANY($1)`, []any{a}},
		{"entity_revisions", `DELETE FROM entity_revisions WHERE asset_id = ANY($1)`, []any{a}},
		{"devices", `DELETE FROM devices WHERE id = ANY($1) AND project_id = ANY($2)`, []any{r, p}},
		{"asset_devices", `DELETE FROM asset_devices WHERE id = ANY($1) AND project_id = ANY($2)`, []any{a, p}},
		{"project_memberships", `DELETE FROM project_memberships WHERE user_id = ANY($1) AND project_id = ANY($2)`, []any{u, p}},
		{"web_sessions", `DELETE FROM web_sessions WHERE user_id = ANY($1)`, []any{u}},
		{"users", `DELETE FROM users WHERE id = ANY($1)`, []any{u}},
		{"projects", `DELETE FROM projects WHERE id = ANY($1) AND tenant_id = 'uat'`, []any{p}},
		{"tenants", `DELETE FROM tenants WHERE id = 'uat' AND NOT EXISTS (SELECT 1 FROM projects WHERE tenant_id = 'uat')`, nil},
	}
	out := map[string]int64{}
	for _, s := range steps {
		res, err := tx.ExecContext(ctx, s.sql, s.args...)
		if err != nil {
			return nil, fmt.Errorf("cleanup %s: %w", s.name, err)
		}
		n, _ := res.RowsAffected()
		out[s.name] = n
	}
	return out, nil
}

func shadowJSON(r runtime, now time.Time) []byte {
	presence := map[string]any{"connectivity": r.Conn, "edgeHealth": "EDGE_HEALTHY", "controllerHealth": "CONTROLLER_HEALTHY",
		"sensorHealth": "HEALTHY", "bootId": "uat-boot", "bootSeq": 1, "sequence": 1, "disconnectedAt": "0001-01-01T00:00:00Z", "disconnectReason": "NONE"}
	if r.Conn == "OFFLINE" {
		presence["disconnectedAt"] = now.Add(-10 * time.Minute).Format(time.RFC3339)
		presence["disconnectReason"] = "UNEXPECTED"
	}
	if ls, ok := lastSeen(r.LastSeen, now); ok {
		presence["lastSeenAt"] = ls
	}
	b, _ := json.Marshal(map[string]any{"deviceId": r.ID, "tenantId": tenant, "projectId": r.Project, "presence": presence,
		"reported": map[string]any{"operatingState": r.Op, "mode": "AUTO", "alarmState": r.Alarm, "runtimeTotal": 0,
			"position": map[string]any{"x": 0, "y": 0, "z": 0}, "load": 0},
		"updatedAt": now.Format(time.RFC3339Nano), "version": 1, "upstreamStatus": "SILENT"})
	return b
}

// seedData inserts the whole dataset (after a cleanup in the same transaction, so a re-seed is
// a deterministic reset). issuer/subjects bind the UAT users to existing Preview Keycloak users.
func seedData(ctx context.Context, tx *sql.Tx, now time.Time) error {
	exec := func(q string, args ...any) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}
	if err := exec(`INSERT INTO tenants(id,name) VALUES($1,$1) ON CONFLICT DO NOTHING`, tenant); err != nil {
		return err
	}
	for i, p := range projects {
		notes := "H3 合成验收数据，非生产信息。" + p.Label
		if i == 0 {
			notes += "；" + seedMarker + now.Format(time.RFC3339Nano)
		}
		if err := exec(`INSERT INTO projects(id,tenant_id,name,lifecycle,admin_code,location_label,notes) VALUES($1,$2,$3,'PLANNED',$4,$5,$6)`,
			p.ID, tenant, p.Name, p.AdminCode, p.Label, notes); err != nil {
			return fmt.Errorf("project %s: %w", p.ID, err)
		}
	}
	for _, a := range assets() {
		if err := exec(`INSERT INTO asset_devices(id,project_id,display_name,lifecycle,asset_code,asset_type,asset_status) VALUES($1,$2,$3,'PLANNED',$1,'CRANE',$4)`,
			a.ID, a.Project, a.Name, a.Status); err != nil {
			return fmt.Errorf("asset %s: %w", a.ID, err)
		}
	}
	for _, r := range runtimes() {
		var asset any
		if r.Asset != "" {
			asset = r.Asset
		}
		if err := exec(`INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES($1,$2,$3,$4)`, r.ID, r.Project, asset, r.Source); err != nil {
			return fmt.Errorf("runtime %s: %w", r.ID, err)
		}
		if err := exec(`INSERT INTO device_shadow(device_id,payload_json,updated_at,version) VALUES($1,$2,$3,1)`, r.ID, shadowJSON(r, now), now); err != nil {
			return fmt.Errorf("shadow %s: %w", r.ID, err)
		}
	}
	for _, a := range alarms(now) {
		payload := fmt.Sprintf(`{"code":"UAT-ALARM","text":"H3 合成告警 %s"}`, a.ID)
		if err := exec(`INSERT INTO alarms(message_id,device_id,observed_at,payload_json) VALUES($1,$2,$3,$4)`, a.ID, a.Device, a.At, payload); err != nil {
			return fmt.Errorf("alarm %s: %w", a.ID, err)
		}
	}
	return nil
}

// bindUsers binds and grants the UAT identities through the same repository functions the
// identity tool uses (each call is its own audited transaction). Bind refuses to move an existing
// id to a different issuer/sub, so it runs after cleanup has removed stale UAT users.
func bindUsers(ctx context.Context, db *sql.DB, issuer string, subjects map[string]string) error {
	for _, u := range users {
		sub := strings.TrimSpace(subjects[u.ID])
		if sub == "" {
			return fmt.Errorf("missing Keycloak subject for %s", u.ID)
		}
		event := auditlog.Event{Actor: "host:uatdata", RequestID: platform.ID()}
		if err := identity.Bind(ctx, db, u.ID, issuer, sub, u.Display, true, event); err != nil {
			return fmt.Errorf("bind %s: %w", u.ID, err)
		}
		for project, role := range u.Grants {
			if err := identity.ChangeGrant(ctx, db, u.ID, project, role, false, auditlog.Event{Actor: "host:uatdata", RequestID: platform.ID()}); err != nil {
				return fmt.Errorf("grant %s %s: %w", u.ID, project, err)
			}
		}
	}
	return nil
}

// seedAnchor reads the seed time recorded on UAT-P001 so verify is deterministic.
func seedAnchor(ctx context.Context, db *sql.DB) (time.Time, error) {
	var notes string
	if err := db.QueryRowContext(ctx, `SELECT notes FROM projects WHERE id='UAT-P001'`).Scan(&notes); err != nil {
		return time.Time{}, errors.New("UAT dataset not seeded (UAT-P001 missing)")
	}
	i := strings.Index(notes, seedMarker)
	if i < 0 {
		return time.Time{}, errors.New("UAT-P001 has no seed marker")
	}
	return time.Parse(time.RFC3339Nano, notes[i+len(seedMarker):])
}
