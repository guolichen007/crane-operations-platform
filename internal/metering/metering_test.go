package metering

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/equipmenttypes"
	"github.com/guolichen007/crane-operations-platform/internal/onboarding"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

// lhk prepares the imported first asset with one SIMULATION and one REAL runtime (REAL only in
// this isolated test database, to prove isolation; the deployment never registers a REAL runtime).
func lhk(t *testing.T) *sql.DB {
	t.Helper()
	db := testsupport.Database(t)
	ctx := context.Background()
	scope, _ := equipmenttypes.Scope(true, nil)
	tp, err := equipmenttypes.PlanTypes(ctx, db, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = equipmenttypes.Apply(ctx, db, scope, tp.Digest, "ops", "types"); err != nil {
		t.Fatal(err)
	}
	b, _ := onboarding.Embedded()
	p, _, err := onboarding.PlanImport(ctx, db, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = onboarding.Apply(ctx, db, b, p.Digest, "ops", "import"); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES('SIM-LHK-CRANE-001','LHK','LHK-CRANE-001','SIMULATION'),('REAL-LHK-TEST','LHK','LHK-CRANE-001','REAL')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

type sender struct {
	t       *testing.T
	db      *sql.DB
	device  string
	mode    string
	boot    string
	bootSeq int64
	seq     int64
}

func (s *sender) send(at time.Time, samples []contract.MeterSample, faults ...contract.FaultSample) Result {
	s.t.Helper()
	s.seq++
	r, err := s.raw(at, s.seq, samples, faults...)
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

func (s *sender) raw(at time.Time, seq int64, samples []contract.MeterSample, faults ...contract.FaultSample) (Result, error) {
	if faults == nil {
		faults = []contract.FaultSample{}
	}
	m := contract.WithPayload(contract.Envelope{SchemaVersion: 1, MessageId: platform.ID(), TenantId: "demo", ProjectId: "LHK", DeviceId: s.device, BootId: s.boot, BootSeq: s.bootSeq, Sequence: seq, SentAt: at, Kind: "METER"},
		contract.MeterBatchPayload{SourceMode: s.mode, TestRunId: "LHK-FIXTURE-001", SampledAt: at, Samples: samples, Faults: faults})
	if err := contract.Validate(m); err != nil {
		s.t.Fatal("contract", err)
	}
	return Ingest(context.Background(), s.db, m)
}

func v(f float64) *float64 { return &f }

func sample(point, metric string, val *float64) contract.MeterSample {
	return contract.MeterSample{PointCode: point, Metric: metric, Value: val, Quality: "GOOD"}
}

func acc(t *testing.T, db *sql.DB, meter, mode string) float64 {
	t.Helper()
	var a float64
	if err := db.QueryRow(`SELECT s.accumulated FROM meter_states s JOIN meter_definitions d ON d.id=s.meter_id WHERE d.meter_code=$1 AND s.source_mode=$2`, meter, mode).Scan(&a); err != nil {
		t.Fatal(meter, mode, err)
	}
	return a
}

func cnt(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(q, err)
	}
	return n
}

// B3: increments, duplicates, out of order, gaps, counter reset, quality — accumulated never decreases.
func TestMeterIngestNeverLosesOrZeroesAccumulatedValue(t *testing.T) {
	db := lhk(t)
	s := &sender{t: t, db: db, device: "SIM-LHK-CRANE-001", mode: "SIMULATION", boot: "boot-a", bootSeq: 1}
	t0 := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	s.send(t0, []contract.MeterSample{sample("LHK-P003", "hours", v(100))})
	s.send(t0.Add(time.Hour), []contract.MeterSample{sample("LHK-P003", "hours", v(101))})
	// Missing sample: never zero, no delta.
	r := s.send(t0.Add(2*time.Hour), []contract.MeterSample{{PointCode: "LHK-P003", Metric: "hours", Value: nil, Quality: "MISSING"}})
	if r.Anomalies["GAP"] != 1 || acc(t, db, "LHK-P003.hours", "SIMULATION") != 1 {
		t.Fatalf("gap %+v acc %v", r, acc(t, db, "LHK-P003.hours", "SIMULATION"))
	}
	s.send(t0.Add(3*time.Hour), []contract.MeterSample{sample("LHK-P003", "hours", v(103))})
	if a := acc(t, db, "LHK-P003.hours", "SIMULATION"); a != 3 {
		t.Fatalf("after gap acc %v want 3", a)
	}
	// Duplicate delivery of the same (boot, sequence): idempotent.
	if r, _ := s.raw(t0.Add(3*time.Hour), s.seq, []contract.MeterSample{sample("LHK-P003", "hours", v(103))}); r.Duplicates != 1 || r.Stored != 0 {
		t.Fatalf("duplicate %+v", r)
	}
	// Late sample (older sequence, spool replay): stored as history, state not rewound.
	if r, _ := s.raw(t0.Add(30*time.Minute), 2, []contract.MeterSample{sample("LHK-P003", "hours", v(100.5))}); r.Duplicates != 1 {
		// sequence 2 already exists → duplicate; use a fresh lower sequence in a gap
		_ = r
	}
	s.seq = 10
	s.send(t0.Add(4*time.Hour), []contract.MeterSample{sample("LHK-P003", "hours", v(104))})
	if r, _ := s.raw(t0.Add(3*time.Hour+30*time.Minute), 7, []contract.MeterSample{sample("LHK-P003", "hours", v(103.5))}); r.Anomalies["OUT_OF_ORDER"] != 1 {
		t.Fatalf("out of order %+v", r)
	}
	if a := acc(t, db, "LHK-P003.hours", "SIMULATION"); a != 4 {
		t.Fatalf("out of order changed acc: %v", a)
	}
	// Device restart with counter reset (new bootId, lower raw value): delta = new value, never negative.
	s.boot, s.bootSeq, s.seq = "boot-b", 2, 0
	r = s.send(t0.Add(5*time.Hour), []contract.MeterSample{sample("LHK-P003", "hours", v(0.5))})
	if r.Anomalies["COUNTER_RESET"] != 1 || acc(t, db, "LHK-P003.hours", "SIMULATION") != 4.5 {
		t.Fatalf("reset %+v acc %v", r, acc(t, db, "LHK-P003.hours", "SIMULATION"))
	}
	// Same boot, value decreases: DECREASE, no change.
	r = s.send(t0.Add(6*time.Hour), []contract.MeterSample{sample("LHK-P003", "hours", v(0.2))})
	if r.Anomalies["DECREASE"] != 1 || acc(t, db, "LHK-P003.hours", "SIMULATION") != 4.5 {
		t.Fatalf("decrease %+v", r)
	}
	// BAD quality: stored, no delta.
	r = s.send(t0.Add(7*time.Hour), []contract.MeterSample{{PointCode: "LHK-P003", Metric: "hours", Value: v(99), Quality: "BAD"}})
	if r.Anomalies["QUALITY"] != 1 || acc(t, db, "LHK-P003.hours", "SIMULATION") != 4.5 {
		t.Fatalf("bad quality %+v", r)
	}
	// One raw signal, two meters (brake contactor cycles + mechanism running hours).
	s.send(t0.Add(8*time.Hour), []contract.MeterSample{sample("LHK-P002", "cycles", v(1000)), sample("LHK-P002", "mechanism_running_hours", v(50))})
	s.send(t0.Add(9*time.Hour), []contract.MeterSample{sample("LHK-P002", "cycles", v(1010)), sample("LHK-P002", "mechanism_running_hours", v(51.5))})
	if acc(t, db, "LHK-P002.cycles", "SIMULATION") != 10 || acc(t, db, "LHK-P002.mechanism_running_hours", "SIMULATION") != 1.5 {
		t.Fatal("derived meters")
	}
	// Daily values + fault events (unique by event id).
	s.send(t0.Add(10*time.Hour), []contract.MeterSample{sample("LHK-P053", "production_tonnes", v(4)), sample("LHK-P054", "daily_job_count", v(2))},
		contract.FaultSample{PointCode: "LHK-P055", EventId: "LHK-SIM-FAULT-3", Code: "SIM_TEST_FAULT", Text: "模拟故障验收", OccurredAt: t0})
	s.send(t0.Add(11*time.Hour), []contract.MeterSample{sample("LHK-P053", "production_tonnes", v(6))},
		contract.FaultSample{PointCode: "LHK-P055", EventId: "LHK-SIM-FAULT-3", Code: "SIM_TEST_FAULT", Text: "模拟故障验收", OccurredAt: t0})
	if cnt(t, db, `SELECT count(*) FROM fault_events`) != 1 || cnt(t, db, `SELECT count(*) FROM daily_metric_values WHERE value=6`) != 1 {
		t.Fatal("daily / faults")
	}
	// Unknown point / metric pairs are counted, never stored against a wrong meter.
	if r := s.send(t0.Add(12*time.Hour), []contract.MeterSample{sample("LHK-P999", "cycles", v(1))}); r.Unknown != 1 || r.Stored != 0 {
		t.Fatalf("unknown %+v", r)
	}
}

// SIM ↔ REAL isolation: a simulator can never write REAL meters; REAL state is independent.
func TestSimulationNeverTouchesRealMeters(t *testing.T) {
	db := lhk(t)
	sim := &sender{t: t, db: db, device: "SIM-LHK-CRANE-001", mode: "SIMULATION", boot: "b", bootSeq: 1}
	t0 := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	sim.send(t0, []contract.MeterSample{sample("LHK-P001", "cycles", v(10))})
	sim.send(t0.Add(time.Minute), []contract.MeterSample{sample("LHK-P001", "cycles", v(900010))})
	// The simulator claiming REAL is refused (mode comes from the registered runtime).
	liar := &sender{t: t, db: db, device: "SIM-LHK-CRANE-001", mode: "REAL", boot: "b", bootSeq: 1, seq: 100}
	if _, err := liar.raw(t0, 101, []contract.MeterSample{sample("LHK-P001", "cycles", v(5))}); !errors.Is(err, ErrModeMismatch) {
		t.Fatalf("simulator wrote REAL: %v", err)
	}
	if cnt(t, db, `SELECT count(*) FROM meter_states WHERE source_mode='REAL'`) != 0 || cnt(t, db, `SELECT count(*) FROM meter_readings WHERE source_mode='REAL'`) != 0 {
		t.Fatal("REAL meters touched by simulation")
	}
	// A test-only rule is never evaluated / alerted for REAL (DB triggers enforce it too).
	id, err := UpsertTestRule(context.Background(), db, "LHK-CRANE-001", TestRule{Code: "T-COUNT", Name: "测试计数", Kind: "REPLACEMENT", Type: "METER_BASED", Slot: "GANTRY-01", MeterCode: "LHK-P001.cycles", IntervalMeter: v(1000000)}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO maintenance_alerts(id,rule_id,asset_id,source_mode,cycle_no,stage,triggered_at) VALUES('x',$1,'LHK-CRANE-001','REAL',1,'DUE',now())`, id); err == nil {
		t.Fatal("test-only rule produced a REAL alert")
	}
	if _, err := db.Exec(`UPDATE maintenance_rules SET approval_status='APPROVED',approved_by='x',approved_at=now(),effective_from=now() WHERE id=$1`, id); err == nil {
		t.Fatal("test-only rule approved")
	}
}

// B4/B5/B7 thresholds: 80/90/100 %, calendar lead 90/30/7, due, overdue, hybrid first-come,
// restart / repeat without duplicate alerts, work order → completion → next cycle, replacement
// creates a new baseline without resetting the raw meter, data-insufficient real rules.
func TestThresholdsAlertsWorkOrderReplacementAndNextCycle(t *testing.T) {
	db := lhk(t)
	ctx := context.Background()
	sim := &sender{t: t, db: db, device: "SIM-LHK-CRANE-001", mode: "SIMULATION", boot: "b", bootSeq: 1}
	now := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	// The simulated instance of GANTRY-01 starts with a baseline at the first reading (0).
	sim.send(now, []contract.MeterSample{sample("LHK-P001", "cycles", v(0))})
	if err := RecordBaseline(ctx, db, "LHK-CRANE-001", "GANTRY-01", "LHK-P001.cycles", "SIMULATION", v(0), "test", "模拟基准"); err != nil {
		t.Fatal(err)
	}
	anchor := now.AddDate(0, 0, -335) // simulated installation date: 365-day interval → 30 days left
	if _, err := UpsertTestRule(ctx, db, "LHK-CRANE-001", TestRule{Code: "T-HYBRID", Name: "总接触器 混合规则（测试）", Kind: "REPLACEMENT", Type: "HYBRID", Slot: "GANTRY-01",
		MeterCode: "LHK-P001.cycles", IntervalMeter: v(1000000), IntervalDays: ptr(365), AnchorAt: &anchor}, "test"); err != nil {
		t.Fatal(err)
	}
	stages := func() map[string]bool {
		rows, _ := db.Query(`SELECT stage FROM maintenance_alerts WHERE source_mode='SIMULATION'`)
		defer rows.Close()
		m := map[string]bool{}
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			m[s] = true
		}
		return m
	}
	eval := func(at time.Time) Evaluation {
		evs, err := EvaluateAll(ctx, db, "LHK-CRANE-001", at)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range evs {
			if e.SourceMode == "SIMULATION" {
				return e
			}
		}
		t.Fatal("no simulation evaluation")
		return Evaluation{}
	}
	// 30 days left → LEAD_90 + LEAD_30, DUE_SOON; meter 0 %.
	e := eval(now)
	if e.Status != StatusDueSoon || !stages()["LEAD_30"] || stages()["LEAD_7"] {
		t.Fatalf("lead 30: %+v %v", e, stages())
	}
	// Meter 799 999 → 79.99 % (no PCT_80); 800 000 → PCT_80; 900 000 → PCT_90; 1 000 000 → PCT_100 / DUE.
	for _, c := range []struct {
		val  float64
		want string
		has  bool
	}{{799999, "PCT_80", false}, {800000, "PCT_80", true}, {899999, "PCT_90", false}, {900000, "PCT_90", true}, {999999, "PCT_100", false}, {1000000, "PCT_100", true}} {
		sim.send(now.Add(time.Minute), []contract.MeterSample{sample("LHK-P001", "cycles", v(c.val))})
		eval(now)
		if stages()[c.want] != c.has {
			t.Fatalf("at %v: %s present=%v want %v", c.val, c.want, stages()[c.want], c.has)
		}
	}
	// Hybrid: the meter reached 100 % first → DUE although the calendar has 30 days left.
	if e = eval(now); e.Status != StatusDue {
		t.Fatalf("hybrid first-come: %+v", e)
	}
	// Re-evaluation (restart, repeated reports) never duplicates alerts.
	before := cnt(t, db, `SELECT count(*) FROM maintenance_alerts`)
	eval(now)
	eval(now.Add(time.Second))
	sim.send(now.Add(2*time.Minute), []contract.MeterSample{sample("LHK-P001", "cycles", v(1000001))})
	eval(now.Add(2 * time.Minute)) // 100.0001 % → OVERDUE status, stage PCT_100 already raised
	if after := cnt(t, db, `SELECT count(*) FROM maintenance_alerts`); after != before {
		t.Fatalf("alerts duplicated: %d → %d", before, after)
	}
	// Calendar: 7 days left, due day, overdue (meter part already due; check time stages appear once).
	eval(now.AddDate(0, 0, 23))
	eval(now.AddDate(0, 0, 30))
	eval(now.AddDate(0, 0, 31))
	for _, st := range []string{"LEAD_90", "LEAD_30", "LEAD_7", "DUE", "OVERDUE", "PCT_80", "PCT_90", "PCT_100"} {
		if cnt(t, db, `SELECT count(*) FROM maintenance_alerts WHERE stage=$1 AND source_mode='SIMULATION'`, st) != 1 {
			t.Fatalf("stage %s count != 1", st)
		}
	}
	// Alert → work order (one per cycle even if requested twice) → assign → start → complete with
	// a replacement → next cycle anchored at completion; the raw meter is not reset.
	var alertID string
	_ = db.QueryRow(`SELECT id FROM maintenance_alerts WHERE stage='PCT_100'`).Scan(&alertID)
	wo, err := CreateFromAlert(ctx, db, alertID, "planner", "r1")
	if err != nil || wo.SourceMode != "SIMULATION" || wo.Status != "OPEN" || wo.EquipmentSlot == nil || *wo.EquipmentSlot != "GANTRY-01" {
		t.Fatalf("work order %+v %v", wo, err)
	}
	again, _ := CreateFromAlert(ctx, db, alertID, "planner", "r1b")
	if again.ID != wo.ID {
		t.Fatal("second work order for the same cycle")
	}
	if _, err = TransitionWorkOrder(ctx, db, wo.ID, wo.Version, Transition{To: "IN_PROGRESS"}, "planner", "r", now); !errors.Is(err, ErrTransition) {
		t.Fatal("OPEN → IN_PROGRESS must be refused")
	}
	wo, err = TransitionWorkOrder(ctx, db, wo.ID, wo.Version, Transition{To: "ASSIGNED", AssignedTo: "tech-1"}, "planner", "r2", now)
	if err != nil {
		t.Fatal(err)
	}
	wo, err = TransitionWorkOrder(ctx, db, wo.ID, wo.Version, Transition{To: "IN_PROGRESS"}, "tech-1", "r3", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = TransitionWorkOrder(ctx, db, wo.ID, wo.Version, Transition{To: "COMPLETED", Result: "DONE"}, "tech-1", "r4", now); !errors.Is(err, ErrInvalid) {
		t.Fatal("completion without basis accepted")
	}
	realBefore := cnt(t, db, `SELECT count(*) FROM asset_equipment_installations WHERE semantic_slot='GANTRY-01'`)
	done := now.AddDate(0, 0, 31)
	wo, err = TransitionWorkOrder(ctx, db, wo.ID, wo.Version, Transition{To: "COMPLETED", Result: "DONE", Basis: "更换总接触器（模拟）", Replacement: &ReplacementInput{SerialNumber: "SIM-SN-2", Reason: "达到寿命阈值（模拟）"}}, "tech-1", "r5", done)
	if err != nil || wo.Status != "COMPLETED" || wo.CompletedAt == nil {
		t.Fatalf("complete %+v %v", wo, err)
	}
	// SIM replacement: the real ledger is untouched; raw accumulated meter unchanged; new SIM baseline.
	if cnt(t, db, `SELECT count(*) FROM asset_equipment_installations WHERE semantic_slot='GANTRY-01'`) != realBefore ||
		cnt(t, db, `SELECT count(*) FROM equipment_replacements WHERE source_mode='SIMULATION' AND new_equipment_installation_id IS NULL AND simulated_instance IS NOT NULL`) != 1 {
		t.Fatal("simulated replacement touched the real ledger")
	}
	if acc(t, db, "LHK-P001.cycles", "SIMULATION") != 1000001 {
		t.Fatal("raw cumulative meter changed by replacement")
	}
	// Next cycle: anchored at completion → cycle 2, meter progress restarts at 0 %, next due date +365 d.
	evs, _ := EvaluateAll(ctx, db, "LHK-CRANE-001", done)
	var next Evaluation
	for _, x := range evs {
		if x.SourceMode == "SIMULATION" {
			next = x
		}
	}
	if next.CycleNo != 2 || next.ProgressPercent == nil || *next.ProgressPercent != 0 || next.NextDueAt == nil || !next.NextDueAt.Equal(done.AddDate(0, 0, 365)) || next.Status != StatusNormal {
		t.Fatalf("next cycle %+v", next)
	}
	if cnt(t, db, `SELECT count(*) FROM maintenance_alerts WHERE cycle_no=1 AND status='CLOSED'`) == 0 {
		t.Fatal("cycle alerts not closed")
	}
	// Real draft rules (unapproved) are not evaluated; an approved real rule without actual dates /
	// baselines is INSUFFICIENT_DATA (never a fabricated due date).
	if cnt(t, db, `SELECT count(*) FROM maintenance_schedules WHERE source_mode='REAL'`) != 0 {
		t.Fatal("draft real rule evaluated")
	}
	if _, err := db.Exec(`UPDATE maintenance_rules SET approval_status='APPROVED',approved_by='mgr',approved_at=now(),effective_from=now()-interval '1 day' WHERE rule_code='LIFE-GANTRY-03'`); err != nil {
		t.Fatal(err)
	}
	evs, _ = EvaluateAll(ctx, db, "LHK-CRANE-001", now)
	found := false
	for _, x := range evs {
		if x.SourceMode == "REAL" {
			found = true
			if x.Status != StatusInsufficient || x.NextDueAt != nil {
				t.Fatalf("real rule without dates: %+v", x)
			}
		}
	}
	if !found || cnt(t, db, `SELECT count(*) FROM maintenance_alerts WHERE source_mode='REAL'`) != 0 {
		t.Fatal("real evaluation missing or alerted without data")
	}
}

// Offline device: a TIME_BASED rule keeps progressing without any telemetry.
func TestTimeRuleProgressesWithoutTelemetry(t *testing.T) {
	r := Rule{ID: "r", Type: "TIME_BASED", IntervalDays: sql.NullInt64{Int64: 365, Valid: true}, LeadDays: []int64{90, 30, 7}}
	anchor := time.Date(2025, 11, 19, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		days   int
		status string
	}{{-100, StatusNormal}, {-89, StatusDueSoon}, {-1, StatusDueSoon}, {0, StatusDue}, {1, StatusOverdue}} {
		ev := Evaluate(r, "REAL", 1, &anchor, nil, nil, anchor.AddDate(0, 0, 365+c.days))
		if ev.Status != c.status {
			t.Fatalf("day %d: %s want %s (%+v)", c.days, ev.Status, c.status, ev)
		}
	}
	if ev := Evaluate(r, "REAL", 1, nil, nil, nil, anchor); ev.Status != StatusInsufficient {
		t.Fatal("no anchor must be INSUFFICIENT_DATA")
	}
}

// Wire rope: the inspector's conclusion is recorded; the system never derives a discard decision.
func TestWireRopeInspectionNeedsInspectorConclusion(t *testing.T) {
	db := lhk(t)
	ctx := context.Background()
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := UpsertTestRule(ctx, db, "LHK-CRANE-001", TestRule{Code: "T-ROPE", Name: "钢丝绳检查（测试）", Kind: "INSPECTION", Type: "TIME_BASED", Slot: "HOIST-38", IntervalDays: ptr(30), AnchorAt: &anchor}, "test"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 2, 5, 0, 0, 0, 0, time.UTC)
	if _, err := EvaluateAll(ctx, db, "LHK-CRANE-001", now); err != nil {
		t.Fatal(err)
	}
	var alert string
	_ = db.QueryRow(`SELECT id FROM maintenance_alerts WHERE stage='OVERDUE'`).Scan(&alert)
	wo, err := CreateFromAlert(ctx, db, alert, "planner", "r")
	if err != nil || wo.Type != "INSPECTION" {
		t.Fatalf("%+v %v", wo, err)
	}
	wo, _ = TransitionWorkOrder(ctx, db, wo.ID, 0, Transition{To: "ASSIGNED", AssignedTo: "inspector"}, "planner", "r", now)
	wo, _ = TransitionWorkOrder(ctx, db, wo.ID, 0, Transition{To: "IN_PROGRESS"}, "inspector", "r", now)
	if _, err = TransitionWorkOrder(ctx, db, wo.ID, 0, Transition{To: "COMPLETED", Result: "DONE_WITH_FINDINGS", Basis: "GB/T 5972 目视检查", RopeInspection: &RopeInspectionInput{BrokenWiresVisible: ptr(3)}}, "inspector", "r", now); !errors.Is(err, ErrInvalid) {
		t.Fatal("inspection without the inspector's conclusion accepted")
	}
	if _, err = TransitionWorkOrder(ctx, db, wo.ID, 0, Transition{To: "COMPLETED", Result: "DONE_WITH_FINDINGS", Basis: "GB/T 5972 目视检查",
		RopeInspection: &RopeInspectionInput{BrokenWiresVisible: ptr(3), MeasuredDiameterMm: v(15.6), Finding: "局部磨损", Conclusion: "CONTINUE_MONITOR"}}, "inspector", "r", now); err != nil {
		t.Fatal(err)
	}
	if cnt(t, db, `SELECT count(*) FROM wire_rope_inspections WHERE conclusion='CONTINUE_MONITOR' AND inspector='inspector' AND source_mode='SIMULATION'`) != 1 {
		t.Fatal("rope inspection not recorded")
	}
}

// CALIBRATION_DUE: a calibration rule (time based) raises LEAD_7 once, becomes a CALIBRATION work
// order and shows in the center as CALIBRATION_DUE; re-evaluating never duplicates the alert.
func TestCalibrationRuleDueCreatesCalibrationWorkOrder(t *testing.T) {
	db := lhk(t)
	ctx := context.Background()
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := UpsertTestRule(ctx, db, "LHK-CRANE-001", TestRule{Code: "T-CAL", Name: "称重传感器 校准（测试）", Kind: "CALIBRATION", Type: "TIME_BASED", Slot: "GANTRY-01",
		IntervalDays: ptr(180), LeadDays: []int{30, 7}, AnchorAt: &anchor}, "test"); err != nil {
		t.Fatal(err)
	}
	now := anchor.AddDate(0, 0, 175) // 5 days left
	for i := 0; i < 2; i++ {
		if _, err := EvaluateAll(ctx, db, "LHK-CRANE-001", now); err != nil {
			t.Fatal(err)
		}
	}
	if cnt(t, db, `SELECT count(*) FROM maintenance_alerts WHERE stage='LEAD_7' AND source_mode='SIMULATION'`) != 1 ||
		cnt(t, db, `SELECT count(*) FROM maintenance_alerts WHERE source_mode='REAL'`) != 0 {
		t.Fatal("calibration LEAD_7 alert missing, duplicated or leaked to REAL")
	}
	rows, err := Center(ctx, db, []string{"LHK"}, now)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.RuleName == "称重传感器 校准（测试）" {
			found = r.Category == "CALIBRATION_DUE" && r.Status == StatusDueSoon && r.SourceMode == "SIMULATION"
		}
	}
	if !found {
		t.Fatalf("center row %+v", rows)
	}
	var alert string
	if err = db.QueryRow(`SELECT id FROM maintenance_alerts WHERE stage='LEAD_7'`).Scan(&alert); err != nil {
		t.Fatal(err)
	}
	wo, err := CreateFromAlert(ctx, db, alert, "planner", "r")
	if err != nil || wo.Type != "CALIBRATION" || wo.SourceMode != "SIMULATION" {
		t.Fatalf("%+v %v", wo, err)
	}
}

func ptr[T any](v T) *T { return &v }
