package app

// R8 zero-business start, through the real chain (real PostgreSQL schema 12, real Mosquitto mTLS,
// the production runtime loop and BFF with OIDC):
//
//   explicit empty Inventory + explicit empty Fleet → Cloud starts, MQTT connects with 0 topics
//   → every business list is empty → an unknown runtime publishing on the broker creates nothing
//   → first project from the web flow (intake → review → planning → approve → grant → Inventory
//     registration) → resource + schedule → location + asset + Equipment → first SIM registered
//     (runtime-identity) → Fleet/ACL regenerated → real MQTT, WATCH, LIVE, non-empty alarms
//   → Cloud restart: data kept, startup with the empty Inventory never re-seeds a demo project
//   → crane-dev reset → empty again → startup still OK; a non-empty Inventory is now refused.
//
// Negative: an empty Fleet is refused by the simulator / Edge Agent runner.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/edge/agent/runner"
	"github.com/guolichen007/crane-operations-platform/internal/devreset"
	"github.com/guolichen007/crane-operations-platform/internal/equipmenttypes"
	"github.com/guolichen007/crane-operations-platform/internal/fleet"
	"github.com/guolichen007/crane-operations-platform/internal/inventory"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/internal/runtimeidentity"
)

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestR8EmptyDocumentsValidation(t *testing.T) {
	empty := contract.FleetManifest{TenantId: "demo", Devices: []contract.RuntimeDescriptor{}}
	if err := fleet.Validate(empty, false); err != nil {
		t.Fatal("cloud must accept an explicit empty Fleet:", err)
	}
	if err := fleet.Validate(empty, true); err == nil {
		t.Fatal("simulator must refuse an empty Fleet")
	}
	if subs := fleet.Subscriptions(empty); len(subs) != 0 {
		t.Fatalf("empty Fleet subscribes %v", subs)
	}
	acl, err := fleet.ACL(empty)
	if err != nil {
		t.Fatal(err)
	}
	rest := strings.SplitN(acl, "\n", 2)[1] // after the comment line
	if strings.Contains(acl, "topic") || strings.ContainsAny(rest, "#+") || strings.Count(acl, "user ") != 1 {
		t.Fatalf("empty ACL must grant nothing (only the cloud-api user, no topic, no wildcard): %q", acl)
	}
	if fleet.Allows(empty, contract.Envelope{TenantId: "demo", ProjectId: "P", DeviceId: "SIM-X"}) {
		t.Fatal("empty Fleet allowed a device")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet.json")
	writeJSONFile(t, path, empty)
	if _, err := fleet.Load(path, 20, false, false); err != nil {
		t.Fatal("cloud load of explicit empty Fleet:", err)
	}
	// The simulator / Edge Agent runner refuses an empty Fleet before connecting anywhere.
	if err := runner.RunFleet(context.Background(), platform.Config{FleetFile: path, Count: 20, MQTTURL: "tls://127.0.0.1:1", CertDir: dir, DataDir: dir, Scenario: "normal", TTL: 300, Grace: 45, WillDelay: 20}); err == nil || !strings.Contains(err.Error(), "1..100") {
		t.Fatalf("empty simulator Fleet: %v", err)
	}
	// Missing / malformed files are still errors (no legacy fallback through a missing file).
	for _, bad := range []string{filepath.Join(dir, "missing.json"), writeTemp(t, dir, "bad.json", `{"tenantId":"demo","devices":[],"extra":1}`), writeTemp(t, dir, "nodev.json", `{"tenantId":"demo"}`)} {
		if _, err := fleet.Load(bad, 20, false, false); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	inv := contract.InventoryDocument{TenantId: "demo", Projects: []contract.InventoryProjectInput{}, AssetDevices: []contract.InventoryAssetInput{}, RuntimeDevices: []contract.RuntimeDescriptor{}}
	if err := inventory.Validate(inv); err != nil {
		t.Fatal("explicit empty Inventory:", err)
	}
	for _, bad := range []string{filepath.Join(dir, "missing-inv.json"), writeTemp(t, dir, "inv-bad.json", `{"tenantId":"demo","projects":[]}`)} {
		if _, err := inventory.Load(bad); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}

func writeTemp(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// startEmpty: real Mosquitto with the ACL of an empty Fleet, cloud runtime with the empty Fleet.
func TestR8ZeroBusinessStartToFirstSimulatedCrane(t *testing.T) {
	simEnabled(t)
	e := newSimEnv(t, 30*time.Second, 5*time.Second, nil)
	ctx := e.ctx
	dir := t.TempDir()
	emptyInv := filepath.Join(dir, "inventory.json")
	writeJSONFile(t, emptyInv, contract.InventoryDocument{TenantId: "demo", Projects: []contract.InventoryProjectInput{}, AssetDevices: []contract.InventoryAssetInput{}, RuntimeDevices: []contract.RuntimeDescriptor{}})

	// 1. Startup step with the explicit empty Inventory: nothing imported, the 00001 seed removed.
	if imported, err := inventory.Startup(ctx, e.db, emptyInv); err != nil || imported {
		t.Fatalf("empty inventory startup = %v %v", imported, err)
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	for _, table := range []string{"projects", "asset_devices", "devices", "alarms", "resources", "schedule_items", "asset_locations"} {
		if n := count(`SELECT count(*) FROM ` + table); n != 0 {
			t.Fatalf("zero start: %s has %d rows", table, n)
		}
	}
	// 2. Cloud runtime with 0 subscriptions is connected (newSimEnv waited for mqttUp).
	if len(e.manifest.Devices) != 0 || !e.cloud.mqttUp.Load() {
		t.Fatalf("empty Fleet cloud not up: %+v", e.manifest)
	}
	// Foundation: portfolio scope + roles, 43 types, one manager and one planner (real OIDC login).
	scope, err := equipmenttypes.Scope(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	tp, err := equipmenttypes.PlanTypes(ctx, e.db, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = equipmenttypes.Apply(ctx, e.db, scope, tp.Digest, "bootstrap", "r8-zero"); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM equipment_types WHERE status='ACTIVE'`); n != len(scope) || n < 40 {
		t.Fatalf("type directory = %d", n)
	}
	if _, err := e.db.Exec(`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS-ZERO','demo','PS-ZERO','零起点组合')`); err != nil {
		t.Fatal(err)
	}
	pm := e.user("zero-pm", "", "")
	planner := e.user("zero-planner", "", "")
	if _, err := e.db.Exec(`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES($1,'PS-ZERO','portfolio_manager'),($2,'PS-ZERO','portfolio_planner')`, pm.id, planner.id); err != nil {
		t.Fatal(err)
	}
	// 3. Empty states through the BFF (lists are 200 + [] — not errors).
	for _, path := range []string{"/api/v1/projects", "/api/v1/assets/devices", "/api/v1/devices", "/api/v1/alarms", "/api/v1/portfolio/intakes", "/api/v1/portfolio/resources", "/api/v1/portfolio/schedule/items", "/api/v1/portfolio/conflicts", "/api/v1/portfolio/projects"} {
		var out []json.RawMessage
		if code := e.call(pm, "GET", path, &out); code != 200 || len(out) != 0 {
			t.Fatalf("empty state %s = %d %d items", path, code, len(out))
		}
	}
	var sum contract.OperationsSummary
	if code := e.call(pm, "GET", "/api/v1/operations/summary", &sum); code != 200 || sum.Metrics.Bound.SIMULATION != 0 || sum.Metrics.Real.Connected != 0 {
		t.Fatalf("operations summary empty = %d %+v", code, sum.Metrics)
	}

	// 4. An unknown runtime with a CA-valid certificate cannot create anything: the empty ACL
	// grants no topic (not authorized), and the Fleet admits no device.
	e.pki.issue(t, e.pki.dir, "SIM-GHOST", false)
	ghost, err := e.raw("SIM-GHOST", e.pki.dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(envelopeFor("SIM-GHOST", "P-GHOST", "BIRTH", "g", 1, 1, contract.BirthPayload{}))
	topic, _ := contract.Topic("demo", "P-GHOST", "SIM-GHOST", "shadow/reported")
	if rc, _ := ghost.publish(ctx, topic, b); rc != 0x87 {
		t.Fatalf("unknown runtime publish rc=0x%x, want 0x87", rc)
	}
	alarmTopic, _ := contract.Topic("demo", "P-GHOST", "SIM-GHOST", "alarm")
	ab, _ := json.Marshal(envelopeFor("SIM-GHOST", "P-GHOST", "ALARM", "g", 1, 2, contract.AlarmPayload{Code: "GHOST", Text: "ghost"}))
	if rc, _ := ghost.publish(ctx, alarmTopic, ab); rc != 0x87 {
		t.Fatalf("unknown runtime alarm rc=0x%x, want 0x87", rc)
	}
	ghost.close()
	time.Sleep(500 * time.Millisecond)
	if n := count(`SELECT count(*) FROM device_shadow`) + count(`SELECT count(*) FROM alarms`) + count(`SELECT count(*) FROM devices`) + count(`SELECT count(*) FROM asset_devices`); n != 0 {
		t.Fatalf("unknown runtime created %d rows", n)
	}

	// 5. First project from zero: intake → review → planning → approve (manager) → grant → register.
	intake := `{"portfolioScopeId":"PS-ZERO","intakeCode":"Z-001","projectName":"零起点首个项目","customerName":null,"projectType":"NEW_INSTALLATION","priority":"HIGH","riskLevel":"MEDIUM","scopeSummary":"从零开始","siteReference":null,"requestedStart":null,"requestedHandover":null,"plannedStart":"2027-03-01T00:00:00Z","plannedEnd":"2027-06-01T00:00:00Z","projectManagerUserId":null,"technicalLeadUserId":null,"siteLeadUserId":null}`
	var created struct{ Id, Status string }
	if code := e.callBody(planner, "POST", "/api/v1/portfolio/intakes", intake, &created); code != 201 {
		t.Fatalf("intake = %d", code)
	}
	for _, to := range []string{"REVIEW", "PLANNING"} {
		if code := e.callBody(planner, "POST", "/api/v1/portfolio/intakes/"+created.Id+"/transitions", `{"status":"`+to+`","reason":"推进"}`, nil); code != 200 {
			t.Fatalf("transition %s = %d", to, code)
		}
	}
	if code := e.callBody(planner, "POST", "/api/v1/portfolio/intakes/"+created.Id+"/approve", `{}`, nil); code != 403 {
		t.Fatalf("planner approve = %d", code)
	}
	var approved struct{ ProjectId string }
	if code := e.callBody(pm, "POST", "/api/v1/portfolio/intakes/"+created.Id+"/approve", `{}`, &approved); code != 200 || approved.ProjectId == "" {
		t.Fatalf("approve = %d %+v", code, approved)
	}
	project := approved.ProjectId
	// The manager grants a project administrator (self-grant is refused by design).
	if code := e.callBody(pm, "POST", "/api/v1/portfolio/projects/"+project+"/members", fmt.Sprintf(`{"userId":%q,"role":"platform_admin","expectedRole":null}`, pm.id), nil); code != 422 {
		t.Fatalf("self grant = %d, want 422", code)
	}
	admin := e.user("zero-admin", "", "")
	if code := e.callBody(pm, "POST", "/api/v1/portfolio/projects/"+project+"/members", fmt.Sprintf(`{"userId":%q,"role":"platform_admin","expectedRole":null}`, admin.id), nil); code != 200 {
		t.Fatalf("grant = %d", code)
	}
	admin = e.user("zero-admin", "", "") // fresh session carries the new ProjectGrant
	if code := e.callBody(admin, "POST", "/api/v1/projects/"+project+"/inventory-registration", `{"lifecycle":"PLANNED","adminCode":330481,"locationLabel":"","scheduleText":"","notes":""}`, nil); code != 200 && code != 201 {
		t.Fatalf("inventory registration = %d", code)
	}

	// 6. Resource + schedule.
	var res contract.Resource
	if code := e.callBody(planner, "POST", "/api/v1/portfolio/resources", `{"portfolioScopeId":"PS-ZERO","resourceType":"TEAM","name":"调试一班","capacity":1,"exclusive":true,"overrideAllowed":true}`, &res); code != 201 {
		t.Fatalf("resource = %d", code)
	}
	var item contract.ScheduleItem
	if code := e.callBody(planner, "POST", "/api/v1/portfolio/schedule/items", `{"portfolioScopeId":"PS-ZERO","sourceType":"COMMISSIONING","name":"首台调试","startAt":"2027-03-01T01:00:00Z","endAt":"2027-03-01T09:00:00Z"}`, &item); code != 201 {
		t.Fatalf("schedule item = %d", code)
	}
	if code := e.callBody(planner, "POST", "/api/v1/portfolio/schedule/items/"+item.Id+"/reservations", `{"resourceId":"`+res.Id+`","startAt":"2027-03-01T01:00:00Z","endAt":"2027-03-01T09:00:00Z","quantity":1}`, nil); code != 201 {
		t.Fatalf("reservation = %d", code)
	}
	if code := e.callBody(pm, "POST", "/api/v1/portfolio/schedule/approve", `{"itemIds":["`+item.Id+`"]}`, nil); code != 200 {
		t.Fatalf("schedule approve = %d", code)
	}

	// 7. Location, asset and Equipment.
	var site contract.AssetLocation
	if code := e.callBody(admin, "POST", "/api/v1/projects/"+project+"/locations", `{"kind":"SITE","name":"一号场地","code":"Z-S1"}`, &site); code != 201 {
		t.Fatalf("location = %d", code)
	}
	var crane contract.AssetMasterData
	if code := e.callBody(admin, "POST", "/api/v1/assets", `{"projectId":"`+project+`","assetType":"CRANE","assetCode":"Z-CRN-01","displayName":"零起点一号行车"}`, &crane); code != 201 {
		t.Fatalf("asset = %d", code)
	}
	var eq contract.EquipmentInstallation
	if code := e.callBody(admin, "POST", "/api/v1/assets/"+crane.Id+"/equipment", `{"equipmentCode":"Z-PLC-01","typeCode":"PLC","criticality":"HIGH","administrativeState":"IN_SERVICE","spec":{}}`, &eq); code != 201 {
		t.Fatalf("equipment = %d", code)
	}

	// 8. First SIM: controlled registration → Fleet/ACL regenerated → certificate → reload broker
	// ACL + recreate the cloud runtime with the new Fleet.
	req := runtimeidentity.Request{TenantId: "demo", Entries: []runtimeidentity.Entry{{RuntimeDeviceId: "SIM-Z-0001", ProjectId: project, AssetDeviceId: crane.Id, SourceMode: "SIMULATION", EquipmentInstallationId: &eq.Id}}}
	plan, err := runtimeidentity.PlanRegistration(ctx, e.db, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtimeidentity.Apply(ctx, e.db, req, plan.Digest, "zero-admin", "r8-zero-sim"); err != nil {
		t.Fatal(err)
	}
	merged, err := runtimeidentity.MergeFleet(e.manifest, req)
	if err != nil {
		t.Fatal(err)
	}
	e.reloadFleet(merged)
	e.pki.issue(t, e.pki.dir, "SIM-Z-0001", false)
	e.startDevice("SIM-Z-0001", "alarm-storm")
	e.until("first SIM connected", 30*time.Second, func() bool { return e.connected("SIM-Z-0001") })
	var lease contract.LiveSession
	if code := e.call(admin, "POST", "/api/v1/devices/SIM-Z-0001/live-sessions", &lease); code != 201 {
		t.Fatalf("watch = %d", code)
	}
	e.until("WATCH acked, upstream LIVE, live samples stored", 40*time.Second, func() bool {
		st, _ := e.leases.Status(ctx, "SIM-Z-0001")
		return st == "LIVE" && count(`SELECT count(*) FROM device_commands WHERE device_id='SIM-Z-0001' AND kind='WATCH_START' AND status='APPLIED'`) == 1 &&
			count(`SELECT count(*) FROM telemetry_samples WHERE device_id='SIM-Z-0001'`) > 0
	})
	e.until("alarms stored", 30*time.Second, func() bool { return count(`SELECT count(*) FROM alarms WHERE device_id='SIM-Z-0001'`) > 0 })
	var alarms []json.RawMessage
	if code := e.call(admin, "GET", "/api/v1/alarms", &alarms); code != 200 || len(alarms) == 0 {
		t.Fatalf("alarms page = %d %d", code, len(alarms))
	}
	e.stopDevice("SIM-Z-0001")

	// 9. Restart: the Cloud startup with the empty Inventory never re-seeds; data stays.
	e.stopCloud()
	if imported, err := inventory.Startup(ctx, e.db, emptyInv); err != nil || imported {
		t.Fatalf("restart startup = %v %v", imported, err)
	}
	e.startCloud()
	if n := count(`SELECT count(*) FROM projects`); n != 1 {
		t.Fatalf("after restart projects = %d (no demo re-seed, first project kept)", n)
	}
	if n := count(`SELECT count(*) FROM projects WHERE id='Project-A' OR id LIKE 'P00%'`); n != 0 {
		t.Fatalf("demo project resurrected: %d", n)
	}

	// 10. crane-dev reset → empty again; startup still OK; a demo Inventory is now refused.
	var dbName string
	_ = e.db.QueryRow(`SELECT current_database()`).Scan(&dbName)
	target := devreset.Guard{Environment: "crane-dev", PublicURL: "https://crane.youdaozd.com", Database: dbName}
	rp, err := devreset.PlanReset(ctx, e.db, devreset.Guard{Environment: "crane-dev", PublicURL: "https://crane.youdaozd.com"}, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = devreset.Apply(ctx, e.db, devreset.Guard{Environment: "crane-dev", PublicURL: "https://crane.youdaozd.com"}, target, rp.Digest, "zero-admin", "r8-reset"); err != nil {
		t.Fatal(err)
	}
	e.reloadFleet(contract.FleetManifest{TenantId: "demo", Devices: []contract.RuntimeDescriptor{}})
	for _, table := range []string{"projects", "asset_devices", "devices", "alarms", "resources", "schedule_items", "asset_locations", "project_intakes"} {
		if n := count(`SELECT count(*) FROM ` + table); n != 0 {
			t.Fatalf("after reset %s = %d", table, n)
		}
	}
	if n := count(`SELECT count(*) FROM portfolio_memberships WHERE portfolio_scope_id='PS-ZERO'`) + count(`SELECT count(*) FROM equipment_types`); n < 42 {
		t.Fatalf("foundation lost after reset: %d", n)
	}
	if imported, err := inventory.Startup(ctx, e.db, emptyInv); err != nil || imported {
		t.Fatalf("startup after reset = %v %v", imported, err)
	}
	demo := os.Getenv("A0_INVENTORY_EXAMPLE")
	if demo == "" {
		demo = filepath.Join(repoRoot(t), "deploy", "inventory", "inventory.example.yaml")
	}
	if _, err := inventory.Startup(ctx, e.db, demo); !errors.Is(err, inventory.ErrImportAfterReset) {
		t.Fatalf("demo inventory after reset must be refused: %v", err)
	}
	if n := count(`SELECT count(*) FROM projects`); n != 0 {
		t.Fatalf("refused import wrote %d projects", n)
	}
	var after []json.RawMessage
	if code := e.call(admin, "GET", "/api/v1/projects", &after); code != 200 || len(after) != 0 {
		t.Fatalf("projects after reset = %d %d", code, len(after))
	}
	simRecord(t, "ZERO_BUSINESS_START", map[string]any{"firstProject": project, "sim": "SIM-Z-0001", "alarms": len(alarms), "reset": rp.Delete})
}

// callBody is call() with a JSON request body.
func (e *simEnv) callBody(u *simUser, method, path, body string, out any) int {
	e.t.Helper()
	r, _ := http.NewRequest(method, e.server.URL+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(u.cookie)
	r.Header.Set("Origin", e.server.URL)
	r.Header.Set("X-CSRF-Token", u.csrf)
	w := newRecorder()
	e.handler.ServeHTTP(w, r)
	if out != nil && w.Code < 300 {
		_ = json.Unmarshal(w.Body.Bytes(), out)
	}
	if w.Code >= 300 {
		e.t.Logf("%s %s → %d %s", method, path, w.Code, strings.TrimSpace(w.Body.String()))
	}
	return w.Code
}

// reloadFleet applies a new Fleet the way the deployment does: regenerate the ACL, reload the
// broker ACL (SIGHUP), restart the cloud runtime with the new Fleet.
func (e *simEnv) reloadFleet(m contract.FleetManifest) {
	e.t.Helper()
	acl, err := fleet.ACL(m)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.root, "acl"), []byte(acl), 0o600); err != nil {
		e.t.Fatal(err)
	}
	e.reloadBrokerACL()
	e.stopCloud()
	e.manifest = m
	e.startCloud()
}

// The real Cloud entry point (Run) with explicit empty Inventory + Fleet passes every startup step
// up to MQTT (missing client certificates stop it there, before serving), imports nothing and
// removes only the empty 00001 seed; after a recorded reset a demo Inventory is refused at startup.
func TestR8RunStartsWithExplicitEmptyInventoryAndFleet(t *testing.T) {
	db, source := provenanceDB(t)
	dir := t.TempDir()
	inv := filepath.Join(dir, "inventory.json")
	fl := filepath.Join(dir, "fleet.json")
	writeJSONFile(t, inv, contract.InventoryDocument{TenantId: "demo", Projects: []contract.InventoryProjectInput{}, AssetDevices: []contract.InventoryAssetInput{}, RuntimeDevices: []contract.RuntimeDescriptor{}})
	writeJSONFile(t, fl, contract.FleetManifest{TenantId: "demo", Devices: []contract.RuntimeDescriptor{}})
	env := map[string]string{"APP_CONFIG": "", "APP_ENV": "development", "INVENTORY_FILE": inv, "FLEET_FILE": fl,
		"DATABASE_URL": source, "DEV_API_TOKEN": strings.Repeat("x", 32), "HTTP_ADDR": "127.0.0.1:0", "CERT_DIR": t.TempDir()}
	for k, v := range env {
		t.Setenv(k, v)
	}
	// MQTT TLS material is absent on purpose: Run must get past Inventory + Fleet and stop at MQTT.
	if err := Run(nil); err == nil || !strings.Contains(err.Error(), "ca.crt") {
		t.Fatalf("Run with empty documents must reach the MQTT step: %v", err)
	}
	var projects int
	if err := db.QueryRow(`SELECT count(*) FROM projects`).Scan(&projects); err != nil || projects != 0 {
		t.Fatalf("empty startup left %d projects (%v)", projects, err)
	}
	// A missing Inventory / Fleet file is not an empty one.
	for _, k := range []string{"INVENTORY_FILE", "FLEET_FILE"} {
		t.Setenv(k, filepath.Join(dir, "missing.json"))
		if err := Run(nil); err == nil || strings.Contains(err.Error(), "ca.crt") {
			t.Fatalf("%s missing must be refused before MQTT: %v", k, err)
		}
		t.Setenv(k, env[k])
	}
	// After a recorded business reset the demo Inventory is refused at startup (no re-seed).
	if _, err := db.Exec(`INSERT INTO audit_logs(user_id,action,result,request_id) VALUES('admin','business_data_reset','success','r')`); err != nil {
		t.Fatal(err)
	}
	demo := os.Getenv("A0_INVENTORY_EXAMPLE") // set inside the integration container (no checkout there)
	if demo == "" {
		demo = filepath.Join(repoRoot(t), "deploy", "inventory", "inventory.example.yaml")
	}
	t.Setenv("INVENTORY_FILE", demo)
	if err := Run(nil); !errors.Is(err, inventory.ErrImportAfterReset) {
		t.Fatalf("demo Inventory after reset: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM projects`).Scan(&projects); err != nil || projects != 0 {
		t.Fatalf("refused import wrote %d projects", projects)
	}
}
