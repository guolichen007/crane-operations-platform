package operations

// Real PostgreSQL tests for the operations summary (all goose migrations applied by
// testsupport.Database). The fixture ports the H2-1B probe (docs/.../evidence/homepage-h2-0/summary-probe.sql)
// and adds the geography cases (country / province / prefecture / county / province-administered county /
// unlocated / unknown adminCode). Every expected number is derived in the comments next to it.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/geo"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

var sampled = time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return sampled }

func cfg(stale, skew int) Config {
	return Config{StaleAfter: time.Duration(stale) * time.Second, MaxFutureSkew: time.Duration(skew) * time.Second}
}

func index(t *testing.T) *geo.Index {
	t.Helper()
	ix, err := geo.Default()
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
}

func shadow(conn, lastSeen, alarm, op string) string {
	ls := ""
	if lastSeen != "-" {
		ls = `,"lastSeenAt":` + lastSeen
	}
	return fmt.Sprintf(`{"presence":{"connectivity":%q%s},"reported":{"alarmState":%q,"operatingState":%q}}`, conn, ls, alarm, op)
}

// seed builds the shared fixture. Projects:
//
//	P1  浙江 海宁市 330481 (county)        P2  广东 深圳市 440300 (unauthorized for u-admin-p1 / u-viewer-p1)
//	P3  no adminCode (unlocated)           P4  浙江 嘉兴市 330400 (prefecture-level code)
//	P5  北京 朝阳区 110105 (province-administered county)    P6 浙江 330000 (province-level code)
//	P7  adminCode 999998 (not in index → unmapped)            PL legacy (lifecycle NULL)
func seed(t *testing.T, db *sql.DB) {
	t.Helper()
	exec(t, db, `INSERT INTO tenants VALUES ('demo','demo') ON CONFLICT DO NOTHING`)
	exec(t, db, `INSERT INTO roles VALUES ('viewer'),('operator'),('maintainer'),('platform_admin') ON CONFLICT DO NOTHING`)
	exec(t, db, `INSERT INTO users(id,subject,issuer,enabled) VALUES
	  ('u-admin-p1','s1','https://idp',true),('u-viewer-p1','s2','https://idp',true),('u-none','s3','https://idp',true),
	  ('u-disabled','s4','https://idp',false),('u-geo','s5','https://idp',true)`)
	exec(t, db, `INSERT INTO projects(id,tenant_id,name,lifecycle,admin_code) VALUES
	  ('P1','demo','P1','PLANNED',330481),('P2','demo','P2','PLANNED',440300),('P3','demo','P3','PLANNED',NULL),
	  ('P4','demo','P4','PLANNED',330400),('P5','demo','P5','PLANNED',110105),('P6','demo','P6','PLANNED',330000),('P7','demo','P7','PLANNED',999998)`)
	exec(t, db, `INSERT INTO projects(id,tenant_id,name) VALUES ('PL','demo','legacy')`)
	exec(t, db, `INSERT INTO project_memberships(user_id,project_id,role) VALUES
	  ('u-admin-p1','P1','platform_admin'),('u-admin-p1','P3','platform_admin'),('u-admin-p1','PL','platform_admin'),
	  ('u-viewer-p1','P1','viewer'),('u-disabled','P1','viewer'),
	  ('u-geo','P1','viewer'),('u-geo','P3','viewer'),('u-geo','P4','viewer'),('u-geo','P5','viewer'),('u-geo','P6','viewer'),('u-geo','P7','viewer')`)
	exec(t, db, `INSERT INTO asset_devices(id,project_id,display_name,lifecycle,asset_status) VALUES
	  ('A1','P1','1号行车','PLANNED','ACTIVE'),('A2','P1','2号行车','PLANNED','ACTIVE'),('A3','P1','3号行车','PLANNED','ACTIVE'),
	  ('A4','P1','4号行车','PLANNED','DRAFT'),('A5','P1','5号行车','PLANNED','RETIRED'),('A6','P1','6号行车','PLANNED','ACTIVE'),
	  ('A7','P1','7号行车','PLANNED','ACTIVE'),('A8','P1','8号行车','PLANNED','DRAFT'),('A9','P1','9号行车','PLANNED','OUT_OF_SERVICE'),
	  ('A10','P1','10号行车','PLANNED','ACTIVE'),('A11','P1','11号行车','PLANNED','RETIRED'),('A12','P1','12号行车','PLANNED','OUT_OF_SERVICE'),
	  ('A13','P1','13号行车','PLANNED','DRAFT'),
	  ('T1','P1','t1','PLANNED','ACTIVE'),('T2','P1','t2','PLANNED','ACTIVE'),('T3','P1','t3','PLANNED','ACTIVE'),
	  ('T4','P1','t4','PLANNED','ACTIVE'),('T5','P1','t5','PLANNED','ACTIVE'),('T6','P1','t6','PLANNED','ACTIVE'),
	  ('D1','P1','d1','PLANNED','DRAFT'),('D2','P1','d2','PLANNED','DRAFT'),
	  ('B1','P2','1号行车','PLANNED','ACTIVE'),('N1','P3','n1','PLANNED','ACTIVE'),('L1','PL','l1','PLANNED','ACTIVE'),
	  ('G4','P4','g4','PLANNED','ACTIVE'),('G5','P5','g5','PLANNED','ACTIVE'),('G6','P6','g6','PLANNED','ACTIVE'),('G7','P7','g7','PLANNED','ACTIVE')`)
	exec(t, db, `INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES
	  ('EDGE-A1','P1','A1','REAL'),('SIM-A1','P1','A1','SIMULATION'),('EDGE-A2','P1','A2','REAL'),('EDGE-A3','P1','A3','REAL'),
	  ('EDGE-A5','P1','A5','REAL'),('EDGE-A6','P1','A6','REAL'),('EDGE-A7','P1','A7','REAL'),('EDGE-A8','P1','A8','REAL'),('EDGE-A9','P1','A9','REAL'),
	  ('EDGE-A10','P1','A10','REAL'),('SIM-A10','P1','A10','SIMULATION'),('EDGE-A11','P1','A11','REAL'),('SIM-A11','P1','A11','SIMULATION'),
	  ('SIM-A12','P1','A12','SIMULATION'),('EDGE-A13','P1','A13','REAL'),('SIM-A13','P1','A13','SIMULATION'),
	  ('EDGE-T1','P1','T1','REAL'),('EDGE-T2','P1','T2','REAL'),('EDGE-T3','P1','T3','REAL'),('EDGE-T4','P1','T4','REAL'),('EDGE-T5','P1','T5','REAL'),('EDGE-T6','P1','T6','REAL'),
	  ('EDGE-D1','P1','D1','REAL'),('EDGE-D2','P1','D2','REAL'),('SIM-D2','P1','D2','SIMULATION'),
	  ('EDGE-B1','P2','B1','REAL'),('EDGE-L1','PL','L1','REAL'),('EDGE-N1','P3','N1','REAL'),
	  ('EDGE-G4','P4','G4','REAL'),('EDGE-G5','P5','G5','REAL'),('EDGE-G6','P6','G6','REAL'),('EDGE-G7','P7','G7','REAL'),
	  ('ORPH-P1-TEST','P1',NULL,'TEST'),('ORPH-P1-REAL','P1',NULL,'REAL'),('ORPH-P2-REAL','P2',NULL,'REAL')`)
	fresh := `"2026-11-07T07:59:00Z"`
	for id, s := range map[string]string{
		"EDGE-A1":  shadow("OFFLINE", fresh, "NONE", "DISCONNECTED"),
		"SIM-A1":   shadow("MQTT_CONNECTED", `"2026-11-07T07:59:50Z"`, "ACTIVE", "LIVE"),
		"EDGE-A2":  shadow("MQTT_CONNECTED", `"2026-11-07T07:50:00Z"`, "ACTIVE", "LIVE"),
		"EDGE-A3":  shadow("MQTT_CONNECTED", `"2026-11-07T07:00:00Z"`, "ACTIVE", "SILENT"),
		"EDGE-A5":  shadow("MQTT_CONNECTED", fresh, "ACTIVE", "SILENT"),
		"EDGE-A6":  shadow("MQTT_CONNECTED", `"2026-11-07T09:00:00Z"`, "NONE", "SILENT"),
		"EDGE-A7":  shadow("MQTT_CONNECTED", "-", "ACTIVE", "SILENT"),
		"EDGE-A8":  shadow("MQTT_CONNECTED", fresh, "NONE", "SILENT"),
		"EDGE-A9":  shadow("MQTT_CONNECTED", fresh, "ACTIVE", "SILENT"),
		"EDGE-A10": shadow("MQTT_CONNECTED", `"2026-11-07T07:58:00Z"`, "ACTIVE", "SILENT"),
		"SIM-A10":  shadow("MQTT_CONNECTED", `"2026-11-07T07:58:00Z"`, "ACTIVE", "SILENT"),
		"EDGE-A11": shadow("MQTT_CONNECTED", fresh, "ACTIVE", "SILENT"),
		"SIM-A11":  shadow("MQTT_CONNECTED", fresh, "ACTIVE", "SILENT"),
		"SIM-A12":  shadow("MQTT_CONNECTED", fresh, "ACTIVE", "SILENT"),
		"EDGE-A13": shadow("MQTT_CONNECTED", fresh, "NONE", "SILENT"),
		"SIM-A13":  shadow("MQTT_CONNECTED", fresh, "NONE", "SILENT"),
		"EDGE-T1":  shadow("MQTT_CONNECTED", `""`, "ACTIVE", "SILENT"),
		"EDGE-T2":  shadow("MQTT_CONNECTED", `"yesterday-ish"`, "ACTIVE", "SILENT"),
		"EDGE-T3":  shadow("MQTT_CONNECTED", `"2026-11-07T07:59:00"`, "ACTIVE", "SILENT"),
		"EDGE-T4":  shadow("MQTT_CONNECTED", `"0001-01-01T00:00:00Z"`, "ACTIVE", "SILENT"),
		"EDGE-T5":  shadow("MQTT_CONNECTED", `1730966340`, "ACTIVE", "SILENT"),
		"EDGE-T6":  shadow("MQTT_CONNECTED", `"2026-11-07T15:59:00+08:00"`, "NONE", "SILENT"),
		"EDGE-D1":  shadow("MQTT_CONNECTED", fresh, "ACTIVE", "SILENT"),
		"EDGE-D2":  shadow("MQTT_CONNECTED", fresh, "ACTIVE", "SILENT"),
		"SIM-D2":   shadow("MQTT_CONNECTED", fresh, "ACTIVE", "SILENT"),
		"EDGE-B1":  shadow("MQTT_CONNECTED", fresh, "ACTIVE", "SILENT"),
		"EDGE-L1":  shadow("MQTT_CONNECTED", fresh, "ACTIVE", "SILENT"),
		"EDGE-G4":  shadow("MQTT_CONNECTED", fresh, "ACTIVE", "SILENT"),
		"EDGE-G5":  shadow("MQTT_CONNECTED", fresh, "NONE", "SILENT"),
		"EDGE-G6":  shadow("MQTT_CONNECTED", fresh, "NONE", "SILENT"),
		"EDGE-G7":  shadow("MQTT_CONNECTED", fresh, "ACTIVE", "SILENT"),
	} {
		exec(t, db, `INSERT INTO device_shadow(device_id,payload_json,updated_at,version) VALUES($1,$2,'2026-11-07T07:59:00Z',2)`, id, s)
	}
	for _, a := range []struct{ id, dev, at string }{
		{"w-start", "SIM-A1", "2026-11-06T08:00:00Z"}, {"w-before", "SIM-A1", "2026-11-06T07:59:59.999Z"},
		{"w-end", "EDGE-A2", "2026-11-07T08:00:00Z"}, {"w-real", "EDGE-A2", "2026-11-07T07:10:00Z"},
		{"w-oos", "EDGE-A9", "2026-11-07T07:11:00Z"}, {"w-retired", "EDGE-A5", "2026-11-07T07:10:00Z"},
		{"w-retired-sim", "SIM-A11", "2026-11-07T07:10:00Z"}, {"w-orph-test", "ORPH-P1-TEST", "2026-11-07T07:20:00Z"},
		{"w-orph-real", "ORPH-P1-REAL", "2026-11-07T07:21:00Z"}, {"w-orph-p2", "ORPH-P2-REAL", "2026-11-07T07:22:00Z"},
		{"w-p2", "EDGE-B1", "2026-11-07T07:30:00Z"}, {"w-legacy", "EDGE-L1", "2026-11-07T07:30:00Z"},
		{"f-scoped", "EDGE-A2", "2026-11-07T09:00:00Z"}, {"f-orphan", "ORPH-P1-REAL", "2026-11-07T09:00:00Z"},
		{"f-retired", "EDGE-A5", "2026-11-07T09:00:00Z"}, {"f-p2", "EDGE-B1", "2026-11-07T09:00:00Z"}, {"f-legacy", "EDGE-L1", "2026-11-07T09:00:00Z"},
		{"m-a10-r1", "EDGE-A10", "2026-11-07T06:00:00Z"}, {"m-a10-r2", "EDGE-A10", "2026-11-07T06:30:00Z"}, {"m-a10-s1", "SIM-A10", "2026-11-07T06:45:00Z"},
		{"m-n1", "EDGE-N1", "2026-11-07T06:50:00Z"},
		{"b-120", "EDGE-A2", "2026-11-07T08:02:00Z"}, {"b-600", "EDGE-A2", "2026-11-07T08:10:00Z"},
		{"g-g4", "EDGE-G4", "2026-11-07T06:55:00Z"},
	} {
		exec(t, db, `INSERT INTO alarms(message_id,device_id,observed_at,payload_json) VALUES($1,$2,$3,'{}')`, a.id, a.dev, a.at)
	}
}

func run(t *testing.T, db *sql.DB, c Config, req Request) contract.OperationsSummary {
	t.Helper()
	if req.Window == "" {
		req.Window = "PT24H"
	}
	s, err := Summary(context.Background(), db, index(t), c, req, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func reg(code int) *int { return &code }

func TestSummaryIdentityRuntimeAndAlarms(t *testing.T) {
	db := testsupport.Database(t)
	seed(t, db)
	s := run(t, db, cfg(2100, 120), Request{User: "u-admin-p1"})
	m := s.Metrics
	// Scope: authorized Inventory projects P1, P3 (PL is legacy) → 2.
	if s.Scope.AuthorizedProjects != 2 || m.Projects != 2 || s.Scope.Region != nil {
		t.Fatalf("scope %+v projects %d", s.Scope, m.Projects)
	}
	// registered: P1 = A1–A4,A6–A10,A12,A13 (11) + T1–T6 (6) + D1,D2 (2) = 19, + P3 N1 = 20. RETIRED A5/A11, legacy L1, P2 excluded.
	want := contract.OperationsAssetCounts{Registered: 20, Draft: 5, OutOfService: 2, NoRuntime: 1, Unlocated: 1, AtScopeLevel: 0}
	if m.Assets != want {
		t.Errorf("assets %+v want %+v", m.Assets, want)
	}
	// SIM+REAL assets counted once: boundREAL 18 assets (incl. N1), SIM 5 (A1,A10,A12,A13,D2).
	if m.Bound != (contract.RuntimeSourceCounts{REAL: 18, SIMULATION: 5, TEST: 0}) {
		t.Errorf("bound %+v", m.Bound)
	}
	// realConnected: A2, A10, T6 (offset zone parses) = 3 — A1's SIM online never counts as REAL; DRAFT/OOS excluded.
	// realLive: only A2 (LIVE is acquisition, not operation). stale: A3 (1 h old), A6 (+1 h). unknownTime: A7 + T1–T5 = 6.
	wantReal := contract.OperationsRealCounts{Connected: 3, PendingReview: 5, PendingReviewAlarm: 3, Stale: 2, UnknownTime: 6, Offline: 1, Live: 1}
	got := m.Real
	got.PendingReviewAssets = nil
	if !reflect.DeepEqual(got, wantReal) {
		t.Errorf("real %+v want %+v", got, wantReal)
	}
	// Review list: deterministic byte order, each with status and alarm flag.
	var ids []string
	for _, a := range m.Real.PendingReviewAssets {
		ids = append(ids, fmt.Sprintf("%s:%s:%v", a.Id, a.AssetStatus, a.Alarm))
	}
	if strings.Join(ids, ",") != "A13:DRAFT:false,A8:DRAFT:false,A9:OUT_OF_SERVICE:true,D1:DRAFT:true,D2:DRAFT:true" {
		t.Errorf("review list %v", ids)
	}
	if m.Operation.Status != "NOT_AVAILABLE" || m.PendingEvents.Status != "NOT_AVAILABLE" {
		t.Error("operation / pendingEvents must be NOT_AVAILABLE")
	}
	// Alarm devices (ACTIVE only): REAL A2,A10; SIM A1,A10; all = A1,A2,A10 = 3 (A10 counted once).
	if m.Alarm.Devices != (contract.RuntimeSourceCounts{REAL: 2, SIMULATION: 2, TEST: 0}) || m.Alarm.DevicesAll != 3 {
		t.Errorf("alarm devices %+v all %d", m.Alarm.Devices, m.Alarm.DevicesAll)
	}
	// Records [2026-11-06T08:00Z, 08:00Z): REAL attributed w-real, w-oos, m-a10-r1, m-a10-r2, m-n1 = 5; REAL orphan 1;
	// SIM w-start, m-a10-s1 = 2; TEST orphan 1 → 9; distinct assets A1,A2,A9,A10,N1 = 5; future (>= 08:02) f-scoped, f-orphan, b-120, b-600 = 4.
	r := m.Alarm.Records
	if r.Cells != (contract.OperationsAlarmCells{REAL: contract.AlarmAttributionCell{Attributed: 5, Unattributed: 1}, SIMULATION: contract.AlarmAttributionCell{Attributed: 2}, TEST: contract.AlarmAttributionCell{Unattributed: 1}}) ||
		r.Total != 9 || r.Assets != 5 || r.FutureSkew != 4 {
		t.Errorf("records %+v", r)
	}
	if !s.SampledAt.Equal(sampled) || s.Freshness.WindowEnd != s.SampledAt || !s.Freshness.WindowStart.Equal(sampled.Add(-24*time.Hour)) ||
		s.Freshness.StaleAfterSeconds != 2100 || s.Freshness.MaxFutureSkewSeconds != 120 || s.GeneratedAt.Before(s.SampledAt) {
		t.Errorf("time %+v sampled %v generated %v", s.Freshness, s.SampledAt, s.GeneratedAt)
	}
	if len(s.Scope.GeoIndexVersion) != 64 {
		t.Error("geoIndexVersion missing")
	}
}

func TestSummaryAuthorizationScopes(t *testing.T) {
	db := testsupport.Database(t)
	seed(t, db)
	for _, tc := range []struct {
		name string
		req  Request
		want int // registered
	}{
		{"viewer P1 only", Request{User: "u-viewer-p1"}, 19},
		{"platform_admin P1+P3 cannot see P2", Request{User: "u-admin-p1"}, 20},
		{"no membership: legal zero", Request{User: "u-none"}, 0},
		{"disabled user: zero", Request{User: "u-disabled"}, 0},
		{"region with only unauthorized P2: zero", Request{User: "u-admin-p1", Region: reg(440000)}, 0},
		{"region drill never widens: 440300", Request{User: "u-admin-p1", Region: reg(440300)}, 0},
	} {
		s := run(t, db, cfg(2100, 120), tc.req)
		if s.Metrics.Assets.Registered != tc.want {
			t.Errorf("%s: registered %d want %d", tc.name, s.Metrics.Assets.Registered, tc.want)
		}
		if tc.want == 0 && (s.Metrics.Alarm.Records.Total != 0 || s.Metrics.Alarm.Records.FutureSkew != 0 || len(s.Regions) != 0 || len(s.Metrics.Real.PendingReviewAssets) != 0) {
			t.Errorf("%s: leaked numbers %+v regions %v", tc.name, s.Metrics.Alarm.Records, s.Regions)
		}
	}
}

func TestSummaryGeographyCountryProvincePrefectureCounty(t *testing.T) {
	db := testsupport.Database(t)
	seed(t, db)
	c := cfg(2100, 120)
	regions := func(s contract.OperationsSummary) map[int]contract.OperationsRegion {
		out := map[int]contract.OperationsRegion{}
		for _, r := range s.Regions {
			out[r.Adcode] = r
		}
		return out
	}
	// u-geo: P1 (330481, 19 assets), P3 (unlocated, N1), P4 (330400, G4), P5 (110105, G5), P6 (330000, G6), P7 (999998 unknown, G7).
	country := run(t, db, c, Request{User: "u-geo"})
	if country.Metrics.Assets.Registered != 24 || country.Metrics.Assets.Unlocated != 2 || country.Metrics.Assets.AtScopeLevel != 0 {
		t.Fatalf("country assets %+v", country.Metrics.Assets)
	}
	cr := regions(country)
	// Zhejiang = P1 19 + P4 1 + P6 1 = 21; Beijing = P5 1. Unlocated P3, P7 (unknown code) belong to no province.
	if len(cr) != 2 || cr[330000].Registered != 21 || cr[110000].Registered != 1 || cr[330000].Name != "浙江省" {
		t.Fatalf("country regions %+v", country.Regions)
	}
	if sum := cr[330000].Registered + cr[110000].Registered + country.Metrics.Assets.Unlocated + country.Metrics.Assets.AtScopeLevel; sum != country.Metrics.Assets.Registered {
		t.Fatalf("country partition %d != registered %d", sum, country.Metrics.Assets.Registered)
	}
	// realConnected/alarm per region: Zhejiang connected A2,A10,T6,G4,G6 = 5; REAL alarm devices A2,A10,G4 = 3.
	if cr[330000].RealConnected != 5 || cr[330000].RealAlarmDevices != 3 || cr[110000].RealConnected != 1 || cr[110000].RealAlarmDevices != 0 {
		t.Fatalf("country region metrics %+v", country.Regions)
	}
	// Province Zhejiang: 21 assets; G6 sits on the province code itself → atScopeLevel 1; Jiaxing 330400 = P1 19 + P4 1 = 20.
	zj := run(t, db, c, Request{User: "u-geo", Region: reg(330000)})
	zr := regions(zj)
	if zj.Metrics.Assets.Registered != 21 || zj.Metrics.Assets.Unlocated != 0 || zj.Metrics.Assets.AtScopeLevel != 1 || len(zr) != 1 || zr[330400].Registered != 20 || zj.Scope.RegionName != "浙江省" {
		t.Fatalf("province %+v regions %+v", zj.Metrics.Assets, zj.Regions)
	}
	// Prefecture Jiaxing: P1 (Haining county, 19) + P4 on the prefecture code (atScopeLevel 1).
	jx := run(t, db, c, Request{User: "u-geo", Region: reg(330400)})
	jr := regions(jx)
	if jx.Metrics.Assets.Registered != 20 || jx.Metrics.Assets.AtScopeLevel != 1 || len(jr) != 1 || jr[330481].Registered != 19 {
		t.Fatalf("prefecture %+v regions %+v", jx.Metrics.Assets, jx.Regions)
	}
	// County Haining: 19 assets, all at scope level, no sub-regions.
	hn := run(t, db, c, Request{User: "u-geo", Region: reg(330481)})
	if hn.Metrics.Assets.Registered != 19 || hn.Metrics.Assets.AtScopeLevel != 19 || len(hn.Regions) != 0 {
		t.Fatalf("county %+v regions %+v", hn.Metrics.Assets, hn.Regions)
	}
	// Province-administered county: Beijing → Chaoyang 110105 directly.
	bj := run(t, db, c, Request{User: "u-geo", Region: reg(110000)})
	if br := regions(bj); bj.Metrics.Assets.Registered != 1 || br[110105].Registered != 1 {
		t.Fatalf("beijing %+v %+v", bj.Metrics.Assets, bj.Regions)
	}
	// Alarm records follow the region: Haining window records = 7 attributed in P1 + 2 P1 orphans (no N1, no G4).
	if hn.Metrics.Alarm.Records.Total != 8 {
		t.Fatalf("county records %+v", hn.Metrics.Alarm.Records)
	}
	// Unknown region → ErrRegionNotFound (HTTP 404); no numbers.
	if _, err := Summary(context.Background(), db, index(t), c, Request{User: "u-geo", Region: reg(999999), Window: "PT24H"}, fixedNow); !errors.Is(err, ErrRegionNotFound) {
		t.Fatalf("unknown region err = %v", err)
	}
}

func TestSummaryFutureSkewBoundariesAndConfig(t *testing.T) {
	db := testsupport.Database(t)
	seed(t, db)
	// P1 future-side records: w-end 08:00:00, b-120 08:02:00, b-600 08:10:00, f-scoped 09:00, f-orphan 09:00.
	for _, tc := range []struct {
		skew, future, connected, stale int
	}{
		{0, 5, 3, 2},   // future starts at sampledAt: w-end counts (it is not in the window either)
		{120, 4, 3, 2}, // b-120 exactly on the boundary IS future (>=); w-end belongs to the next sample
		{600, 3, 3, 2}, // b-600 exactly on the boundary IS future; A6 (+3600 s) still stale
	} {
		s := run(t, db, cfg(2100, tc.skew), Request{User: "u-viewer-p1"})
		if s.Metrics.Alarm.Records.FutureSkew != tc.future || s.Metrics.Real.Connected != tc.connected || s.Metrics.Real.Stale != tc.stale {
			t.Errorf("skew %d: future %d connected %d stale %d", tc.skew, s.Metrics.Alarm.Records.FutureSkew, s.Metrics.Real.Connected, s.Metrics.Real.Stale)
		}
	}
	// Observation exactly at sampledAt + 600 s: fresh with skew 600, stale with skew 599.
	exec(t, db, `UPDATE device_shadow SET payload_json = jsonb_set(payload_json, '{presence,lastSeenAt}', '"2026-11-07T08:10:00Z"') WHERE device_id='EDGE-A6'`)
	if s := run(t, db, cfg(2100, 600), Request{User: "u-viewer-p1"}); s.Metrics.Real.Connected != 4 {
		t.Errorf("boundary 600: connected %d", s.Metrics.Real.Connected)
	}
	if s := run(t, db, cfg(2100, 599), Request{User: "u-viewer-p1"}); s.Metrics.Real.Connected != 3 {
		t.Errorf("boundary 599: connected %d", s.Metrics.Real.Connected)
	}
	// Out-of-contract configuration is refused by Config.Validate and by the statement itself.
	for _, c := range []Config{cfg(2100, -1), cfg(2100, 601), cfg(2100, 7200), cfg(299, 120), cfg(7201, 120)} {
		if c.Validate() == nil {
			t.Errorf("config %+v accepted", c)
		}
		if _, err := Summary(context.Background(), db, index(t), c, Request{User: "u-viewer-p1", Window: "PT24H"}, fixedNow); !errors.Is(err, ErrUnavailable) {
			t.Errorf("statement accepted %+v: %v", c, err)
		}
	}
	if _, err := Summary(context.Background(), db, index(t), cfg(2100, 120), Request{User: "u-viewer-p1", Window: "P30D"}, fixedNow); !errors.Is(err, ErrInvalidWindow) {
		t.Errorf("window err = %v", err)
	}
}

func TestSummaryEmptyDatabaseIsLegalZero(t *testing.T) {
	db := testsupport.Database(t)
	exec(t, db, `INSERT INTO users(id,subject,issuer,enabled) VALUES ('u','s','https://idp',true)`)
	s := run(t, db, cfg(2100, 120), Request{User: "u"})
	if s.Metrics.Assets.Registered != 0 || s.Metrics.Alarm.Records.Total != 0 || s.Regions == nil || s.Metrics.Real.PendingReviewAssets == nil || s.Metrics.Operation.Status != "NOT_AVAILABLE" {
		t.Fatalf("empty %+v", s)
	}
}

func TestSummaryMoreThan100RecordsAndReviewAssets(t *testing.T) {
	db := testsupport.Database(t)
	seed(t, db)
	// 150 window records on A2 (REAL attributed) and 130 more DRAFT assets with a fresh REAL connection.
	exec(t, db, `INSERT INTO alarms(message_id,device_id,observed_at,payload_json)
	  SELECT 'bulk-'||g, 'EDGE-A2', timestamptz '2026-11-07T07:00:00Z' + g*interval '1 second', '{}' FROM generate_series(1,150) g`)
	exec(t, db, `INSERT INTO asset_devices(id,project_id,display_name,lifecycle,asset_status) SELECT 'R'||lpad(g::text,3,'0'),'P1','r','PLANNED','DRAFT' FROM generate_series(1,130) g`)
	exec(t, db, `INSERT INTO devices(id,project_id,asset_device_id,source_mode) SELECT 'EDGE-R'||lpad(g::text,3,'0'),'P1','R'||lpad(g::text,3,'0'),'REAL' FROM generate_series(1,130) g`)
	exec(t, db, `INSERT INTO device_shadow(device_id,payload_json,updated_at,version)
	  SELECT 'EDGE-R'||lpad(g::text,3,'0'), '{"presence":{"connectivity":"MQTT_CONNECTED","lastSeenAt":"2026-11-07T07:59:00Z"},"reported":{"alarmState":"NONE","operatingState":"SILENT"}}', now(), 1 FROM generate_series(1,130) g`)
	s := run(t, db, cfg(2100, 120), Request{User: "u-viewer-p1"})
	// window total: P1 baseline 8 (9 minus N1) + 150 = 158; distinct assets unchanged (A1,A2,A9,A10).
	if s.Metrics.Alarm.Records.Total != 158 || s.Metrics.Alarm.Records.Assets != 4 || s.Metrics.Alarm.Records.Cells.REAL.Attributed != 154 {
		t.Fatalf("records %+v", s.Metrics.Alarm.Records)
	}
	// review: 5 + 130 = 135 in total, list capped at 100 in deterministic byte order (A13, A8, A9, D1, D2, R001…).
	if s.Metrics.Real.PendingReview != 135 || len(s.Metrics.Real.PendingReviewAssets) != MaxReviewAssets ||
		s.Metrics.Real.PendingReviewAssets[0].Id != "A13" || s.Metrics.Real.PendingReviewAssets[5].Id != "R001" || s.Metrics.Real.PendingReviewAssets[99].Id != "R095" {
		t.Fatalf("review %d %d %v", s.Metrics.Real.PendingReview, len(s.Metrics.Real.PendingReviewAssets), s.Metrics.Real.PendingReviewAssets[0])
	}
}

func TestSummaryQueryFailureIsUnavailableNotPartial(t *testing.T) {
	db := testsupport.Database(t)
	seed(t, db)
	// A cancelled context and a statement timeout both surface as ErrUnavailable with no data.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s, err := Summary(ctx, db, index(t), cfg(2100, 120), Request{User: "u-viewer-p1", Window: "PT24H"}, fixedNow); !errors.Is(err, ErrUnavailable) || s.SchemaVersion != 0 {
		t.Fatalf("cancelled: %v %+v", err, s.Scope)
	}
	// A missing relation (schema drift) fails the whole statement.
	exec(t, db, `ALTER TABLE alarms RENAME TO alarms_moved`)
	if s, err := Summary(context.Background(), db, index(t), cfg(2100, 120), Request{User: "u-viewer-p1", Window: "PT24H"}, fixedNow); !errors.Is(err, ErrUnavailable) || s.SchemaVersion != 0 {
		t.Fatalf("missing table: %v", err)
	}
	exec(t, db, `ALTER TABLE alarms_moved RENAME TO alarms`)
	// Unavailable administrative index → ErrUnavailable.
	if _, err := Summary(context.Background(), db, nil, cfg(2100, 120), Request{User: "u-viewer-p1", Window: "PT24H"}, fixedNow); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil index: %v", err)
	}
}

// One statement = one snapshot: while a writer keeps committing new assets that each get a REAL
// identity in the same transaction, every summary must see either both rows or neither, so
// bound.REAL can never exceed registered and the asset partition always adds up.
func TestSummaryConcurrentWritersSeeOneSnapshot(t *testing.T) {
	db := testsupport.Database(t)
	seed(t, db)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			tx, err := db.Begin()
			if err != nil {
				return
			}
			id := fmt.Sprintf("C%05d", i)
			_, err1 := tx.Exec(`INSERT INTO asset_devices(id,project_id,display_name,lifecycle,asset_status) VALUES($1,'P1','c','PLANNED','ACTIVE')`, id)
			_, err2 := tx.Exec(`INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES($1,'P1',$2,'REAL')`, "E"+id, id)
			if err1 != nil || err2 != nil {
				_ = tx.Rollback()
				return
			}
			_ = tx.Commit()
		}
	}()
	seen := map[int]bool{}
	for i := 0; i < 40; i++ {
		s := run(t, db, cfg(2100, 120), Request{User: "u-viewer-p1"})
		a := s.Metrics.Assets
		// P1 assets without REAL identity: A4, A12 — every other registered P1 asset has REAL.
		if s.Metrics.Bound.REAL != a.Registered-2 {
			t.Fatalf("torn snapshot: registered %d boundREAL %d", a.Registered, s.Metrics.Bound.REAL)
		}
		seen[a.Registered] = true
	}
	close(stop)
	wg.Wait()
	if len(seen) < 2 {
		t.Log("writer did not interleave; snapshot invariant still held")
	}
}

// The jit setting is SET LOCAL inside the summary's read-only transaction: after many summaries on
// a single pooled connection, the session still reports the server default.
func TestSummaryLeavesNoSessionSettingOnPooledConnection(t *testing.T) {
	db := testsupport.Database(t)
	seed(t, db)
	db.SetMaxOpenConns(1)
	var before string
	if err := db.QueryRow(`SHOW jit`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		run(t, db, cfg(2100, 120), Request{User: "u-viewer-p1"})
	}
	var after string
	if err := db.QueryRow(`SHOW jit`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("session setting leaked: jit %s → %s", before, after)
	}
	// And a summary cancelled mid-flight leaves the connection usable and clean.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = Summary(ctx, db, index(t), cfg(2100, 120), Request{User: "u-viewer-p1", Window: "PT24H"}, fixedNow)
	if err := db.QueryRow(`SHOW jit`).Scan(&after); err != nil || after != before {
		t.Fatalf("after cancel: %v %s", err, after)
	}
}
