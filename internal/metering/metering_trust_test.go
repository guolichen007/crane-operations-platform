package metering

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

// R9 收口 — the counter boundary reviewed by the owner: a value below the trusted mark is never
// counted and never moves the mark, so returning to it does not count the same usage twice.
//
//	1000 → 1100 → 900 → 1100 → 1200  ⇒  trusted accumulated 200 (not 400)
//
// Duplicates, late samples, a suspected reset after an edge restart, the recovery and the
// administrator-confirmed real PLC reset are all exercised; the state survives a fresh connection
// pool (cloud restart) unchanged.
func TestCounterDecreaseIsHeldAndNeverCountedTwice(t *testing.T) {
	db := lhk(t)
	ctx := context.Background()
	s := &sender{t: t, db: db, device: "SIM-LHK-CRANE-001", mode: "SIMULATION", boot: "boot-1", bootSeq: 1}
	t0 := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	p := func(val float64) []contract.MeterSample {
		return []contract.MeterSample{sample("LHK-P001", "cycles", v(val))}
	}
	trust := func() (string, sql.NullFloat64) {
		var st string
		var pending sql.NullFloat64
		if err := db.QueryRow(`SELECT s.trust_state, s.pending_last_raw FROM meter_states s JOIN meter_definitions d ON d.id=s.meter_id WHERE d.meter_code='LHK-P001.cycles' AND s.source_mode='SIMULATION'`).Scan(&st, &pending); err != nil {
			t.Fatal(err)
		}
		return st, pending
	}
	for i, val := range []float64{1000, 1100, 900, 1100, 1200} {
		s.send(t0.Add(time.Duration(i)*time.Minute), p(val))
		if val == 900 {
			if st, pend := trust(); st != "DECREASE_PENDING" || !pend.Valid || pend.Float64 != 900 {
				t.Fatalf("900 below the mark must be held: %s %v", st, pend)
			}
		}
	}
	if a := acc(t, db, "LHK-P001.cycles", "SIMULATION"); a != 200 {
		t.Fatalf("1000→1100→900→1100→1200 accumulated %v, want 200", a)
	}
	if st, _ := trust(); st != "OK" {
		t.Fatalf("recovered meter still %s", st)
	}
	if n := cnt(t, db, `SELECT count(*) FROM meter_readings WHERE anomaly='DECREASE'`); n != 1 {
		t.Fatalf("decrease rows %d", n)
	}
	// Duplicate delivery of every batch again: idempotent, nothing counted.
	for seq := int64(1); seq <= 5; seq++ {
		if r, _ := s.raw(t0, seq, p(5000)); r.Duplicates != 1 || r.Stored != 0 {
			t.Fatalf("duplicate seq %d: %+v", seq, r)
		}
	}
	// A late (older sequence) sample with a lower value: history only.
	s.seq = 20
	s.send(t0.Add(10*time.Minute), p(1300))
	if r, _ := s.raw(t0.Add(9*time.Minute), 15, p(1250)); r.Anomalies["OUT_OF_ORDER"] != 1 {
		t.Fatalf("late sample %+v", r)
	}
	if a := acc(t, db, "LHK-P001.cycles", "SIMULATION"); a != 300 {
		t.Fatalf("after late sample %v want 300", a)
	}
	// Edge restart (new boot) that keeps its counter: no anomaly, normal counting continues.
	s.boot, s.bootSeq, s.seq = "boot-2", 2, 0
	if r := s.send(t0.Add(11*time.Minute), p(1310)); len(r.Anomalies) != 0 {
		t.Fatalf("restart with a continuing counter flagged %+v", r)
	}
	if a := acc(t, db, "LHK-P001.cycles", "SIMULATION"); a != 310 {
		t.Fatalf("after restart %v want 310", a)
	}
	// Edge restart whose counter is lower: a SUSPECTED reset — held, never auto-counted.
	s.boot, s.bootSeq, s.seq = "boot-3", 3, 0
	if r := s.send(t0.Add(12*time.Minute), p(4)); r.Anomalies["COUNTER_RESET"] != 1 {
		t.Fatalf("suspected reset %+v", r)
	}
	s.send(t0.Add(13*time.Minute), p(9))
	if a := acc(t, db, "LHK-P001.cycles", "SIMULATION"); a != 310 {
		t.Fatalf("suspected reset counted: %v", a)
	}
	// While pending, the meter is not a trusted basis (a maintenance conclusion is withheld).
	var meterID string
	_ = db.QueryRow(`SELECT id FROM meter_definitions WHERE meter_code='LHK-P001.cycles'`).Scan(&meterID)
	if _, ev, err := meterEvidence(ctx, db, meterID, "SIMULATION", t0.Add(13*time.Minute)); err != nil || ev.Issue != "PENDING_DECREASE" {
		t.Fatalf("pending evidence %+v %v", ev, err)
	}
	// Cloud restart: a fresh pool sees exactly the same persisted state.
	db2, err := sql.Open("pgx", testsupport.URL(t, db))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var a2 float64
	var st2 string
	if err = db2.QueryRow(`SELECT s.accumulated, s.trust_state FROM meter_states s WHERE s.meter_id=$1 AND s.source_mode='SIMULATION'`, meterID).Scan(&a2, &st2); err != nil || a2 != 310 || st2 != "DECREASE_PENDING" {
		t.Fatalf("persisted %v %s %v", a2, st2, err)
	}
	// Confirmation is a recorded administrator decision; wrong input / wrong asset are refused.
	if err = ConfirmCounterReset(ctx, db2, "LHK-CRANE-001", meterID, CounterResetInput{SourceMode: "SIMULATION", Reason: ""}, "admin", "r"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty reason: %v", err)
	}
	if err = ConfirmCounterReset(ctx, db2, "OTHER", meterID, CounterResetInput{SourceMode: "SIMULATION", Reason: "PLC 计数块清零"}, "admin", "r"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign asset: %v", err)
	}
	if err = ConfirmCounterReset(ctx, db2, "LHK-CRANE-001", meterID, CounterResetInput{SourceMode: "SIMULATION", Reason: "PLC 计数块清零（模拟验收）"}, "admin", "r"); err != nil {
		t.Fatal(err)
	}
	if a := acc(t, db, "LHK-P001.cycles", "SIMULATION"); a != 319 {
		t.Fatalf("confirmed reset %v want 310+9", a)
	}
	if err = ConfirmCounterReset(ctx, db2, "LHK-CRANE-001", meterID, CounterResetInput{SourceMode: "SIMULATION", Reason: "再次确认"}, "admin", "r"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second confirmation: %v", err)
	}
	s.send(t0.Add(14*time.Minute), p(15))
	if a := acc(t, db, "LHK-P001.cycles", "SIMULATION"); a != 325 {
		t.Fatalf("after confirmed reset %v want 325", a)
	}
	if cnt(t, db, `SELECT count(*) FROM meter_counter_events WHERE event_kind='RESET_CONFIRMED' AND accumulated_before=310 AND accumulated_after=319`) != 1 ||
		cnt(t, db, `SELECT count(*) FROM audit_logs WHERE action='meter_counter_reset_confirmed'`) != 1 {
		t.Fatal("reset confirmation not recorded")
	}
	// UNCERTAIN / BAD / MISSING never count and never move the mark.
	for i, q := range []string{"UNCERTAIN", "BAD"} {
		s.send(t0.Add(time.Duration(15+i)*time.Minute), []contract.MeterSample{{PointCode: "LHK-P001", Metric: "cycles", Value: v(99999), Quality: q}})
	}
	s.send(t0.Add(17*time.Minute), []contract.MeterSample{{PointCode: "LHK-P001", Metric: "cycles", Value: nil, Quality: "MISSING"}})
	s.send(t0.Add(18*time.Minute), p(20))
	if a := acc(t, db, "LHK-P001.cycles", "SIMULATION"); a != 330 {
		t.Fatalf("quality samples counted: %v want 330", a)
	}
}

// Maintenance conclusions follow the evidence: unknown initial usage is never zero, stale / bad /
// pending values give no NORMAL, a HYBRID rule with one computable condition is 部分数据不足 but
// still raises the condition that is due.
func TestEvidenceGovernsConclusions(t *testing.T) {
	now := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	anchor := now.AddDate(0, 0, -100)
	hybrid := Rule{ID: "h", Type: "HYBRID", IntervalDays: sql.NullInt64{Int64: 365, Valid: true}, IntervalMeter: sql.NullFloat64{Float64: 1000, Valid: true},
		LeadDays: []int64{90, 30, 7}, WarnPercents: []int64{80, 90}, MeterID: sql.NullString{String: "m", Valid: true}}
	// Both conditions known and fresh: NORMAL.
	if ev := EvaluateWith(hybrid, "SIMULATION", 1, &anchor, v(0), v(100), MeterEvidence{}, now); ev.Status != StatusNormal || len(ev.DataGaps) != 0 {
		t.Fatalf("complete hybrid %+v", ev)
	}
	// Meter base unknown (NULL initial usage): time part NORMAL, meter part missing → 部分数据不足.
	ev := EvaluateWith(hybrid, "SIMULATION", 1, &anchor, nil, v(100), MeterEvidence{}, now)
	if ev.Status != StatusInsufficient || len(ev.DataGaps) != 1 || ev.DataGaps[0] != "METER" || ev.NextDueAt == nil || ev.ProgressPercent != nil {
		t.Fatalf("partial hybrid %+v", ev)
	}
	if ev.Reason == "" || ev.Reason[:len("部分数据不足")] != "部分数据不足" {
		t.Fatalf("partial reason %q", ev.Reason)
	}
	// Meter unknown but the known time condition is due: the due condition is still raised.
	old := now.AddDate(0, 0, -400)
	ev = EvaluateWith(hybrid, "SIMULATION", 1, &old, nil, nil, MeterEvidence{}, now)
	if ev.Status != StatusOverdue || !contains(ev.Stages, "OVERDUE") || len(ev.DataGaps) != 1 {
		t.Fatalf("known due condition hidden %+v", ev)
	}
	// Time unknown, meter known and due → DUE with the TIME gap.
	ev = EvaluateWith(hybrid, "SIMULATION", 1, nil, v(0), v(1000), MeterEvidence{}, now)
	if ev.Status != StatusDue || !contains(ev.Stages, "PCT_100") || len(ev.DataGaps) != 1 || ev.DataGaps[0] != "TIME" {
		t.Fatalf("meter due, time unknown %+v", ev)
	}
	// Stale / bad / pending values: below any threshold → no NORMAL conclusion; a reached
	// threshold (the value is a lower bound of the usage) is still raised.
	meterOnly := hybrid
	meterOnly.Type = "METER_BASED"
	for _, issue := range []string{"STALE", "QUALITY", "PENDING_DECREASE"} {
		if ev := EvaluateWith(meterOnly, "SIMULATION", 1, nil, v(0), v(100), MeterEvidence{Issue: issue}, now); ev.Status != StatusInsufficient || ev.Reason == "" {
			t.Fatalf("%s low value concluded %+v", issue, ev)
		}
		if ev := EvaluateWith(meterOnly, "SIMULATION", 1, nil, v(0), v(950), MeterEvidence{Issue: issue}, now); ev.Status != StatusDueSoon || !contains(ev.Stages, "PCT_90") {
			t.Fatalf("%s reached stage hidden %+v", issue, ev)
		}
	}
	// An unconfirmed REAL point is never a basis, whatever its value.
	if ev := EvaluateWith(meterOnly, "REAL", 1, nil, v(0), v(5000), MeterEvidence{Issue: "UNCONFIRMED_POINT"}, now); ev.Status != StatusInsufficient || len(ev.Stages) != 0 {
		t.Fatalf("unconfirmed REAL point used %+v", ev)
	}
}

// End to end on PostgreSQL: a NULL initial usage keeps the meter part 数据不足 (never 0), and a
// stale value gives no NORMAL; the schedule row records the gap and the reason.
func TestNullInitialUsageAndStaleValueOnDatabase(t *testing.T) {
	db := lhk(t)
	ctx := context.Background()
	s := &sender{t: t, db: db, device: "SIM-LHK-CRANE-001", mode: "SIMULATION", boot: "b", bootSeq: 1}
	now := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	s.send(now, []contract.MeterSample{sample("LHK-P002", "cycles", v(10))})
	if err := RecordBaseline(ctx, db, "LHK-CRANE-001", "GANTRY-02", "LHK-P002.cycles", "SIMULATION", nil, "test", "初始使用量未知"); err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertTestRule(ctx, db, "LHK-CRANE-001", TestRule{Code: "T-NULL", Name: "未知初始使用量", Kind: "REPLACEMENT", Type: "METER_BASED", Slot: "GANTRY-02", MeterCode: "LHK-P002.cycles", IntervalMeter: v(100)}, "test"); err != nil {
		t.Fatal(err)
	}
	s.send(now.Add(time.Minute), []contract.MeterSample{sample("LHK-P002", "cycles", v(500))}) // 490 used since baseline
	if _, err := EvaluateAll(ctx, db, "LHK-CRANE-001", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var status string
	var gaps string
	var reason sql.NullString
	if err := db.QueryRow(`SELECT s.status, array_to_string(s.data_gaps,','), s.reason FROM maintenance_schedules s JOIN maintenance_rules r ON r.id=s.rule_id WHERE r.rule_code='T-NULL'`).Scan(&status, &gaps, &reason); err != nil {
		t.Fatal(err)
	}
	if status != StatusInsufficient || gaps != "METER" || !reason.Valid || cnt(t, db, `SELECT count(*) FROM maintenance_alerts a JOIN maintenance_rules r ON r.id=a.rule_id WHERE r.rule_code='T-NULL'`) != 0 {
		t.Fatalf("NULL initial usage treated as known: %s %s %v", status, gaps, reason)
	}
	var usage sql.NullFloat64
	_ = db.QueryRow(`SELECT (SELECT s.accumulated - b.baseline_accumulated + b.initial_usage FROM equipment_meter_baselines b WHERE b.meter_id=s.meter_id AND b.source_mode=s.source_mode) FROM meter_states s JOIN meter_definitions d ON d.id=s.meter_id WHERE d.meter_code='LHK-P002.cycles'`).Scan(&usage)
	if usage.Valid {
		t.Fatalf("equipment usage shown with unknown initial usage: %v", usage.Float64)
	}
	// A known base but stale data: no NORMAL two days later.
	if _, err := UpsertTestRule(ctx, db, "LHK-CRANE-001", TestRule{Code: "T-STALE", Name: "过期数据", Kind: "MAINTENANCE", Type: "METER_BASED", Slot: "GANTRY-01", MeterCode: "LHK-P001.cycles", IntervalMeter: v(1000)}, "test"); err != nil {
		t.Fatal(err)
	}
	s.send(now.Add(2*time.Minute), []contract.MeterSample{sample("LHK-P001", "cycles", v(0))})
	if err := RecordBaseline(ctx, db, "LHK-CRANE-001", "GANTRY-01", "LHK-P001.cycles", "SIMULATION", v(0), "test", "模拟"); err != nil {
		t.Fatal(err)
	}
	s.send(now.Add(3*time.Minute), []contract.MeterSample{sample("LHK-P001", "cycles", v(100))})
	if _, err := EvaluateAll(ctx, db, "LHK-CRANE-001", now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT s.status FROM maintenance_schedules s JOIN maintenance_rules r ON r.id=s.rule_id WHERE r.rule_code='T-STALE'`).Scan(&status)
	if status != StatusNormal {
		t.Fatalf("fresh known meter %s", status)
	}
	if _, err := EvaluateAll(ctx, db, "LHK-CRANE-001", now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT s.status, s.reason FROM maintenance_schedules s JOIN maintenance_rules r ON r.id=s.rule_id WHERE r.rule_code='T-STALE'`).Scan(&status, &reason)
	if status != StatusInsufficient || !reason.Valid {
		t.Fatalf("stale meter concluded %s %v", status, reason)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
