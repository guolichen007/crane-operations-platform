package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/asset"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/equipmenttypes"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/operations"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
	"github.com/guolichen007/crane-operations-platform/internal/webauth"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/device"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/session"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// pgTool runs pg_dump / pg_restore. R7_PG_EXEC (e.g. "docker exec -i -e PGPASSWORD=… <container>")
// runs the server's own client inside the database container (CI: version-matched to the server);
// otherwise the binaries on PATH are used with the test database URL.
func pgTool(t *testing.T, tool, database string, stdin []byte, extra ...string) []byte {
	t.Helper()
	var args []string
	if prefix := strings.Fields(os.Getenv("R7_PG_EXEC")); len(prefix) > 0 {
		args = append(prefix, tool, "-h", "127.0.0.1", "-p", "5432", "-U", "a1test", "-d", database)
	} else {
		u, err := url.Parse(os.Getenv("A1_TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		u.Path = "/" + database
		args = []string{tool, "-d", u.String()}
	}
	args = append(args, extra...)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s failed: %v (%d bytes stderr withheld)", tool, err, errb.Len())
	}
	return out.Bytes()
}

// fingerprint is the business state compared before backup and after restore.
type drillFingerprint struct {
	Schema       int64
	Projects     int
	Assets       int
	Types        int
	ActiveTypes  int
	Users        int
	Memberships  int
	Audits       int
	AuditHash    string
	RevisionHash string
	InstallHash  string
	// Schema 13 (P1 personnel): people, departments, system roles, account provisions.
	PersonnelHash string
}

func fingerprintDB(t *testing.T, db *sql.DB) drillFingerprint {
	t.Helper()
	var f drillFingerprint
	err := db.QueryRow(`SELECT (SELECT max(version_id) FROM goose_db_version WHERE is_applied),
	  (SELECT count(*) FROM projects),(SELECT count(*) FROM asset_devices),(SELECT count(*) FROM equipment_types),
	  (SELECT count(*) FROM equipment_types WHERE status='ACTIVE'),(SELECT count(*) FROM users),(SELECT count(*) FROM project_memberships),
	  (SELECT count(*) FROM audit_logs),
	  (SELECT md5(coalesce(string_agg(user_id||'|'||action||'|'||result||'|'||coalesce(project_id,'')||'|'||request_id||'|'||observed_at::text, ',' ORDER BY id),'')) FROM audit_logs),
	  (SELECT md5(coalesce(string_agg(entity_type||'|'||entity_id||'|'||revision||'|'||coalesce(snapshot_sha256,''), ',' ORDER BY id),'')) FROM entity_revisions),
	  (SELECT md5(coalesce(string_agg(asset_id||'|'||location_id||'|'||valid_from::text||'|'||coalesce(valid_to::text,''), ',' ORDER BY id),'')) FROM asset_installations),
	  (SELECT md5(
	     coalesce((SELECT string_agg(id||'|'||coalesce(username,'')||'|'||coalesce(department_id,'')||'|'||job_title||'|'||enabled::text||'|'||version::text||'|'||idp_state, ',' ORDER BY id) FROM users),'')||'#'||
	     coalesce((SELECT string_agg(user_id||'|'||role, ',' ORDER BY user_id, role) FROM system_memberships),'')||'#'||
	     coalesce((SELECT string_agg(id||'|'||name||'|'||status, ',' ORDER BY id) FROM departments),'')||'#'||
	     coalesce((SELECT string_agg(id||'|'||state||'|'||username, ',' ORDER BY id) FROM account_provisions),'')||'#'||
	     coalesce((SELECT string_agg(coalesce(target_user_id,'')||'|'||coalesce(scope,'')||'|'||coalesce(change::text,''), ',' ORDER BY id) FROM audit_logs),'')))`).
		Scan(&f.Schema, &f.Projects, &f.Assets, &f.Types, &f.ActiveTypes, &f.Users, &f.Memberships, &f.Audits, &f.AuditHash, &f.RevisionHash, &f.InstallHash, &f.PersonnelHash)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// R7 isolated restore drill: a schema-13 database (P1: incl. personnel, system roles, departments) with the 43-type directory, users, grants,
// assets, moves, equipment and audit is dumped (pg_dump -Fc) and restored into a NEW database that
// is never a formal database. The restored copy must have the identical business fingerprint and
// work through the real BFF: OIDC login of the existing user, historical asset and location
// readback, 43 installable types, and a new install whose audit row appends to the restored log.
func TestR7IsolatedRestoreDrill(t *testing.T) {
	if os.Getenv("R7_RESTORE_DRILL") != "1" {
		t.Skip("restore drill runs only with R7_RESTORE_DRILL=1 (needs pg_dump/pg_restore)")
	}
	ctx := context.Background()
	src := testsupport.Database(t)
	provider := testsupport.NewOIDC(t)
	// Source data, all through the normal code paths.
	scope, _ := equipmenttypes.Scope(true, nil)
	p, _ := equipmenttypes.PlanTypes(ctx, src, scope)
	if _, err := equipmenttypes.Apply(ctx, src, scope, p.Digest, "drill-ops", "drill-types"); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(`INSERT INTO projects(id,tenant_id,name,lifecycle) VALUES('DR-P1','demo','DR-P1','PLANNED')`); err != nil {
		t.Fatal(err)
	}
	if err := identity.Bind(ctx, src, "u-drill", provider.Server.URL, "sub-drill", "Drill Admin", true, auditlog.Event{Actor: "drill"}); err != nil {
		t.Fatal(err)
	}
	if err := identity.ChangeGrant(ctx, src, "u-drill", "DR-P1", "platform_admin", false, auditlog.Event{Actor: "drill"}); err != nil {
		t.Fatal(err)
	}
	code := "DR-CRN-1"
	a, err := asset.CreateAsset(ctx, src, contract.AssetMasterData{ProjectId: "DR-P1", AssetType: "CRANE", AssetCode: &code}, "u-drill", "drill-a", "")
	if err != nil {
		t.Fatal(err)
	}
	site, _ := asset.CreateLocation(ctx, src, contract.AssetLocation{ProjectId: "DR-P1", Kind: "SITE", Name: "场地"}, "u-drill", "drill-l1")
	ws, _ := asset.CreateLocation(ctx, src, contract.AssetLocation{ProjectId: "DR-P1", Kind: "WORKSHOP", Name: "车间", ParentId: &site.Id}, "u-drill", "drill-l2")
	if err := asset.MoveAsset(ctx, src, a.Id, site.Id, 1, "u-drill", "drill-m1"); err != nil {
		t.Fatal(err)
	}
	if err := asset.MoveAsset(ctx, src, a.Id, ws.Id, 2, "u-drill", "drill-m2"); err != nil {
		t.Fatal(err)
	}
	if _, err := asset.CreateEquipment(ctx, src, contract.EquipmentInstallation{AssetId: a.Id, EquipmentCode: "DR-PLC", TypeCode: "PLC", Criticality: "HIGH"}, json.RawMessage(`{}`), "u-drill", "drill-e", ""); err != nil {
		t.Fatal(err)
	}
	// Schema 13 personnel state rides along in the backup: department, profile, system role, provision.
	for _, stmt := range []string{
		`INSERT INTO departments(id,name) VALUES('dr-ops','运维部')`,
		`UPDATE users SET username='u-drill', department_id='dr-ops', job_title='管理员' WHERE id='u-drill'`,
		`INSERT INTO system_memberships(user_id,role,granted_by) VALUES('u-drill','system_admin','host:drill')`,
		`INSERT INTO account_provisions(id,actor,idempotency_key,payload_sha256,username,request,state) VALUES('prov-dr','u-drill','key-drill-0001','x','u-drill','{}','COMPLETED')`,
	} {
		if _, err := src.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := auditlog.Write(ctx, src, auditlog.Event{Actor: "host:drill", Action: "account_adopted", Result: "success", TargetUser: "u-drill", Scope: "system", Change: map[string]any{"systemRoles": []string{"system_admin"}}}); err != nil {
		t.Fatal(err)
	}
	before := fingerprintDB(t, src)
	if before.Schema != 13 || before.Types != 43 || before.ActiveTypes != 43 {
		t.Fatalf("source fingerprint = %+v", before)
	}

	var srcName string
	_ = src.QueryRow(`SELECT current_database()`).Scan(&srcName)
	started := time.Now()
	dump := pgTool(t, "pg_dump", srcName, nil, "-Fc", "--no-owner", "--no-acl")
	target := "crane_drill_" + platform.ID()
	root, err := sql.Open("pgx", os.Getenv("A1_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if !strings.HasPrefix(target, "crane_drill_") || target == srcName {
		t.Fatal("drill target must be a fresh crane_drill_* database")
	}
	if _, err := root.Exec(`CREATE DATABASE "` + target + `"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = root.Exec(`DROP DATABASE "` + target + `" WITH (FORCE)`) })
	pgTool(t, "pg_restore", target, dump, "--exit-on-error", "--no-owner", "--no-acl")
	u, _ := url.Parse(os.Getenv("A1_TEST_DATABASE_URL"))
	u.Path = "/" + target
	restored, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	after := fingerprintDB(t, restored)
	elapsed := time.Since(started)
	if after != before {
		t.Fatalf("restored fingerprint differs:\nbefore %+v\nafter  %+v", before, after)
	}

	// The restored copy works through the real BFF (production webauth, real OIDC flow).
	store, err := device.New(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	var delegate atomic.Value
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { delegate.Load().(http.Handler).ServeHTTP(w, r) }))
	defer server.Close()
	manager, err := webauth.New(ctx, restored, webauth.Config{PublicURL: server.URL, Issuer: provider.Server.URL, ClientID: testsupport.OIDCClient, ClientSecret: []byte(testsupport.OIDCSecret), ServerSecret: []byte(strings.Repeat("drill-server-key-", 3)), AbsoluteTTL: time.Hour, IdleTTL: 10 * time.Minute, HTTPClient: provider.Server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	var ws1, dbe atomic.Int64
	h := &apiHandler{config: platform.Config{Env: "production"}, db: restored, store: store, leases: session.New(restored, time.Minute, 5*time.Second), active: func(contract.Shadow) bool { return true }, list: store.List, logger: platform.Logger("drill"), wsClients: &ws1, dbErrors: &dbe, web: manager, ops: operations.Config{StaleAfter: operations.DefaultStaleAfter, MaxFutureSkew: operations.DefaultMaxFutureSkew}}
	mux := http.NewServeMux()
	if err := h.register(mux); err != nil {
		t.Fatal(err)
	}
	delegate.Store(http.Handler(mux))
	provider.Set(testsupport.OIDCScenario{Subject: "sub-drill"})
	cookie, sess := testsupport.Login(t, mux, provider, server.URL)
	if sess.UserId != "u-drill" || len(sess.Projects) != 1 || sess.Projects[0].ProjectId != "DR-P1" {
		t.Fatalf("restored login session = %+v", sess)
	}
	get := func(path string, out any) int {
		r := httptest.NewRequest("GET", server.URL+path, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if out != nil {
			_ = json.Unmarshal(w.Body.Bytes(), out)
		}
		return w.Code
	}
	var got contract.AssetMasterData
	var loc contract.AssetLocationReadback
	var hist contract.AssetInstallationHistory
	var types []contract.EquipmentType
	if get("/api/v1/assets/"+a.Id, &got) != 200 || got.AssetCode == nil || *got.AssetCode != code ||
		get("/api/v1/assets/"+a.Id+"/location", &loc) != 200 || loc.Current == nil || loc.Current.LocationId != ws.Id ||
		get("/api/v1/assets/"+a.Id+"/installation-history", &hist) != 200 || hist.Total != 2 ||
		get("/api/v1/equipment/types", &types) != 200 || len(types) != 43 {
		t.Fatalf("restored readback: asset=%+v loc=%+v hist=%d types=%d", got, loc.Current, hist.Total, len(types))
	}
	body := `{"equipmentCode":"DR-MOTOR","typeCode":"MOTOR","criticality":"MEDIUM","administrativeState":"IN_SERVICE","spec":{}}`
	r := httptest.NewRequest("POST", server.URL+"/api/v1/assets/"+a.Id+"/equipment", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", server.URL)
	r.Header.Set("X-CSRF-Token", sess.CsrfToken)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("install on restored copy = %d %s", w.Code, w.Body.String())
	}
	var appended int
	_ = restored.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='equipment_created' AND user_id='u-drill'`).Scan(&appended)
	if appended != 2 {
		t.Fatalf("restored audit log did not continue: %d equipment_created rows", appended)
	}
	if fingerprintDB(t, src) != before {
		t.Fatal("drill touched the source database")
	}
	simRecord(t, "ISOLATED_RESTORE_DRILL", map[string]any{"dumpBytes": len(dump), "elapsedMs": elapsed.Milliseconds(), "fingerprint": before, "target": "crane_drill_*", "login": "PASS", "types": len(types), "auditContinues": true})
}
