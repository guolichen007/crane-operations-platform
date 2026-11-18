package devreset

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

var testEnv = Guard{Environment: "crane-dev", PublicURL: "https://crane.youdaozd.com"}

func TestResetDeletesBusinessDataKeepsFoundation(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	want := Guard{Environment: "crane-dev", PublicURL: "https://crane.youdaozd.com", Database: name}
	for _, stmt := range []string{
		`INSERT INTO users(id,subject) VALUES('u-admin','sub-admin')`,
		`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS-1','demo','PS-1','组合一')`,
		`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('u-admin','PS-1','portfolio_manager')`,
		`INSERT INTO equipment_types(type_code,display_name,domain) VALUES('PLC','PLC','CONTROL')`,
		`INSERT INTO manufacturers(id,project_id,name) VALUES('M-GLOBAL',NULL,'全局厂商')`,
		`INSERT INTO projects(id,tenant_id,name,lifecycle,portfolio_scope_id) VALUES('P-OLD','demo','旧示例','PLANNED','PS-1')`,
		`INSERT INTO manufacturers(id,project_id,name) VALUES('M-P','P-OLD','项目厂商')`,
		`INSERT INTO asset_devices(id,project_id,display_name,lifecycle) VALUES('A-OLD','P-OLD','旧行车','PLANNED')`,
		`INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES('SIM-OLD','P-OLD','A-OLD','SIMULATION')`,
		`INSERT INTO alarms(message_id,device_id,observed_at,payload_json) VALUES('m1','SIM-OLD',now(),'{}')`,
		`INSERT INTO resources(id,portfolio_scope_id,resource_type,name,capacity,exclusive,override_allowed,home_timezone,status,version) VALUES('R-1','PS-1','TEAM','班组',1,true,true,'Asia/Shanghai','ACTIVE',1)`,
		`INSERT INTO schedule_items(id,portfolio_scope_id,source_type,source_id,name,start_at,end_at,timezone,status,priority,version) VALUES('SI-1','PS-1','COMMISSIONING','s','调试',now(),now()+interval '1h','Asia/Shanghai','PLANNED','MEDIUM',1)`,
		`INSERT INTO project_memberships(user_id,project_id,role) VALUES('u-admin','P-OLD','viewer')`,
		// Schema 13 personnel foundation must survive the reset.
		`INSERT INTO departments(id,name) VALUES('ops','运维部')`,
		`UPDATE users SET username='u-admin', department_id='ops', job_title='系统管理员' WHERE id='u-admin'`,
		`INSERT INTO system_memberships(user_id,role,granted_by) VALUES('u-admin','system_admin','host:ops')`,
		`INSERT INTO account_provisions(id,actor,idempotency_key,payload_sha256,username,request,state) VALUES('prov-1','u-admin','key-00000001','x','u-admin','{}','COMPLETED')`,
		`INSERT INTO audit_logs(user_id,action,result,target_user_id,scope,change) VALUES('host:ops','personnel_created','success','u-admin','account','{"username":"u-admin"}')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(stmt, err)
		}
	}
	// Guards: wrong environment / URL / database refused before anything is read or written.
	for _, bad := range []Guard{{Environment: "", PublicURL: want.PublicURL}, {Environment: "crane-dev", PublicURL: "https://other.example"}} {
		if _, err := PlanReset(ctx, db, bad, want); !errors.Is(err, ErrGuard) {
			t.Fatalf("guard %+v: %v", bad, err)
		}
	}
	if _, err := PlanReset(ctx, db, testEnv, Guard{Environment: "crane-dev", PublicURL: want.PublicURL, Database: "crane_app"}); !errors.Is(err, ErrGuard) {
		t.Fatalf("database guard: %v", err)
	}
	p, err := PlanReset(ctx, db, testEnv, want)
	if err != nil {
		t.Fatal(err)
	}
	if p.Delete["projects"] != 2 || p.Delete["devices"] != 1 || p.Delete["alarms"] != 1 || p.Delete["manufacturers"] != 1 || p.Keep["manufacturers"] != 1 || p.Keep["portfolio_scopes"] != 1 || p.Keep["equipment_types"] != 1 {
		t.Fatalf("plan = %+v", p)
	}
	// Wrong digest / missing actor refused, nothing deleted.
	if _, err := Apply(ctx, db, testEnv, want, strings.Repeat("0", 64), "admin", "r1"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("digest: %v", err)
	}
	if _, err := Apply(ctx, db, testEnv, want, p.Digest, " ", "r1"); !errors.Is(err, ErrGuard) {
		t.Fatalf("actor: %v", err)
	}
	// A REAL runtime blocks the reset.
	if _, err := db.Exec(`INSERT INTO asset_devices(id,project_id,display_name,lifecycle) VALUES('A-R','P-OLD','真机','PLANNED'); INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES('EDGE-1','P-OLD','A-R','REAL')`); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanReset(ctx, db, testEnv, want); !errors.Is(err, ErrGuard) {
		t.Fatalf("REAL runtime must block: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM devices WHERE id='EDGE-1'; DELETE FROM asset_devices WHERE id='A-R'`); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, db, testEnv, want, p.Digest, "admin", "r1"); err != nil {
		t.Fatal(err)
	}
	for table, wantN := range map[string]int{"projects": 0, "asset_devices": 0, "devices": 0, "alarms": 0, "resources": 0, "schedule_items": 0, "project_memberships": 0,
		"users": 2, "portfolio_scopes": 1, "portfolio_memberships": 1, "equipment_types": 1, "manufacturers": 1, "tenants": 1,
		"departments": 1, "system_memberships": 1, "account_provisions": 1} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != wantN {
			t.Fatalf("%s = %d (%v), want %d", table, n, err, wantN)
		}
	}
	var kept int
	if err := db.QueryRow(`SELECT count(*) FROM users WHERE id='u-admin' AND username='u-admin' AND department_id='ops' AND enabled`).Scan(&kept); err != nil || kept != 1 {
		t.Fatal("system administrator profile not kept", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='personnel_created' AND target_user_id='u-admin'`).Scan(&kept); err != nil || kept != 1 {
		t.Fatal("account audit not kept", err)
	}
	if ok, err := WasReset(ctx, db); err != nil || !ok {
		t.Fatalf("reset not recorded: %v %v", ok, err)
	}
	// The digest is single use: the state changed.
	if _, err := Apply(ctx, db, testEnv, want, p.Digest, "admin", "r2"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("replayed digest: %v", err)
	}
}
