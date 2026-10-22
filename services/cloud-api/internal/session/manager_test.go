package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/deploy/postgres/migrations"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"net/url"
	"os"
	"testing"
	"time"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	source := os.Getenv("T05_TEST_DATABASE_URL")
	if source == "" {
		t.Skip("real PostgreSQL assertions run in Compose integration with T05_TEST_DATABASE_URL")
	}
	root, e := sql.Open("pgx", source)
	if e != nil {
		t.Fatal(e)
	}
	name := "crane_t05_test_" + platform.ID()
	if !contract.ValidID(name) {
		t.Fatal("unsafe test database identifier")
	}
	if _, e = root.Exec(`CREATE DATABASE "` + name + `"`); e != nil {
		root.Close()
		t.Fatal(e)
	}
	u, e := url.Parse(source)
	if e != nil {
		t.Fatal(e)
	}
	u.Path = "/" + name
	db, e := sql.Open("pgx", u.String())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		db.Close()
		_, e := root.Exec(`DROP DATABASE "` + name + `" WITH (FORCE)`)
		root.Close()
		if e != nil {
			t.Error("isolated test database cleanup failed", e)
		}
	})
	return db
}
func migrate(t *testing.T, db *sql.DB, target int64) {
	t.Helper()
	goose.SetLogger(goose.NopLogger())
	goose.SetBaseFS(migrations.Files)
	if e := goose.SetDialect("postgres"); e != nil {
		t.Fatal(e)
	}
	if e := goose.UpToContext(context.Background(), db, ".", target); e != nil {
		t.Fatal(e)
	}
}
func TestMigrationEmptyAndPhase0Upgrade(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "phase0_upgrade"}[upgrade], func(t *testing.T) {
			db := testDB(t)
			if upgrade {
				migrate(t, db, 1)
				old := `{"deviceId":"SIM-001","tenantId":"demo","projectId":"Project-A","reported":{"bootId":"old","sequence":10,"connectivity":"OFFLINE","edgeHealth":"UNKNOWN","controllerHealth":"UNKNOWN","sensorHealth":"HEALTHY","operatingState":"SILENT","mode":"AUTO","alarmState":"NONE","runtimeTotal":42,"x":10,"y":20,"z":5,"load":2.5}}`
				if _, e := db.Exec(`INSERT INTO devices(id,project_id) VALUES('SIM-001','Project-A');`); e != nil {
					t.Fatal(e)
				}
				if _, e := db.Exec(`INSERT INTO device_shadow(device_id,payload_json,updated_at,version) VALUES('SIM-001',$1,now(),7)`, old); e != nil {
					t.Fatal(e)
				}
			}
			migrate(t, db, 2)
			version, e := goose.GetDBVersion(db)
			if e != nil || version != 2 {
				t.Fatal(version, e)
			}
			if upgrade {
				var raw []byte
				if e = db.QueryRow(`SELECT payload_json FROM device_shadow WHERE device_id='SIM-001'`).Scan(&raw); e != nil {
					t.Fatal(e)
				}
				var v contract.Shadow
				if e = json.Unmarshal(raw, &v); e != nil {
					t.Fatal(e)
				}
				if v.Version != 7 || v.Reported.Mode != "AUTO" || v.Reported.Position.X != 10 || v.Reported.Load != 2.5 || v.Presence.BootSeq != 0 {
					t.Fatal("migration lost Phase0 snapshot", v)
				}
			}
		})
	}
}

// This retains Phase0's five-viewer, ownership, TTL and grace checks against real persistence.
func TestReferenceAndExpiry(t *testing.T) {
	db := testDB(t)
	migrate(t, db, 2)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, e := db.Exec(`INSERT INTO devices(id,project_id) VALUES('SIM-001','Project-A')`); e != nil {
		t.Fatal(e)
	}
	m := New(db, 5*time.Second, 2*time.Second)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		s, e := m.Open(ctx, id, "SIM-001", "dev-admin", now)
		if e != nil || s.UpstreamStatus != "STARTING" || s.TelemetryHz != 1 {
			t.Fatal(s, e)
		}
	}
	pending, e := m.Pending(ctx, now)
	if e != nil || len(pending) != 1 {
		t.Fatal("five viewers created multiple upstreams", len(pending), e)
	}
	if e = m.Published(ctx, pending[0].MessageId, now); e != nil {
		t.Fatal(e)
	}
	status, e := m.Status(ctx, "SIM-001")
	if e != nil || status != "STARTING" {
		t.Fatal("broker PUBACK incorrectly means LIVE", status, e)
	}
	if e = m.Close(ctx, "a", "other", now); !errors.Is(e, ErrNotFound) {
		t.Fatal("cross-user close", e)
	}
	if _, e = m.Renew(ctx, "a", "dev-admin", now.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	// A new Manager contains no cache; all five viewers and the desired generation survive.
	m = New(db, 5*time.Second, 2*time.Second)
	viewers, active, e := m.Counts(ctx)
	if e != nil || viewers != 5 || active != 0 {
		t.Fatal("restart lost leases or promoted unacked upstream", viewers, active, e)
	}
	if e = m.Reconcile(ctx, "SIM-001", now.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	old := pending[0]
	pending, e = m.Pending(ctx, now.Add(time.Second))
	if e != nil || len(pending) != 1 {
		t.Fatal(e)
	}
	ack := func(command contract.Envelope) {
		t.Helper()
		p := contract.Payload[contract.WatchCommandPayload](command)
		state := "LIVE"
		if command.Kind == "WATCH_STOP" {
			state = "SILENT"
		}
		if e := m.Ack(ctx, "SIM-001", contract.WatchAckPayload{CommandId: command.MessageId, LeaseId: p.LeaseId, Generation: p.Generation, Action: command.Kind, Result: "APPLIED", EdgeState: state}, now); e != nil {
			t.Fatal(e)
		}
	}
	ack(old)
	status, e = m.Status(ctx, "SIM-001")
	if e != nil || status != "STARTING" {
		t.Fatal("old generation ACK promoted upstream", status, e)
	}
	ack(pending[0])
	status, e = m.Status(ctx, "SIM-001")
	if e != nil || status != "LIVE" {
		t.Fatal("application ACK did not activate", status, e)
	}
	if e = m.Tick(ctx, now.Add(7*time.Second)); e != nil {
		t.Fatal(e)
	}
	status, e = m.Status(ctx, "SIM-001")
	if e != nil || status == "SILENT" {
		t.Fatal("grace lost", status, e)
	}
	if e = m.Tick(ctx, now.Add(8*time.Second)); e != nil {
		t.Fatal(e)
	}
	pending, e = m.Pending(ctx, now.Add(8*time.Second))
	if e != nil || len(pending) != 1 || pending[0].Kind != "WATCH_STOP" {
		t.Fatal("expiry did not stop upstream", pending, e)
	}
	ack(pending[0])
	status, e = m.Status(ctx, "SIM-001")
	viewers, active, countErr := m.Counts(ctx)
	if e != nil || countErr != nil || status != "SILENT" || viewers != 0 || active != 0 {
		t.Fatal("expired viewers retained LIVE", status, viewers, active, e, countErr)
	}
}
func TestCommandACKMustConfirmActualState(t *testing.T) {
	for _, v := range []struct{ kind, result, state, want string }{{"WATCH_START", "APPLIED", "LIVE", "APPLIED"}, {"WATCH_START", "DUPLICATE", "SILENT", "FAILED"}, {"WATCH_START", "STALE", "LIVE", "FAILED"}, {"WATCH_START", "EXPIRED", "SILENT", "EXPIRED"}, {"WATCH_STOP", "APPLIED", "LIVE", "FAILED"}, {"WATCH_STOP", "DUPLICATE", "SILENT", "APPLIED"}} {
		if got := ackStatus(v.kind, contract.WatchAckPayload{Result: v.result, EdgeState: v.state}); got != v.want {
			t.Fatal(v, got)
		}
	}
}
