package app

import (
	"context"
	"testing"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/equipmenttypes"
	"github.com/guolichen007/crane-operations-platform/internal/onboarding"
	"github.com/guolichen007/crane-operations-platform/internal/simscenario"
)

// R9 收口 G2 over the real chain: the server-side scenario tool (lhk-sim) creates only marked
// test-only rules and simulated baselines; the registered SIMULATION runtime publishes meter batches
// through real Mosquitto (mTLS + ACL); the cloud runtime loop ingests them and the cloud maintenance
// loop raises the alerts — the tool never ingests telemetry or evaluates. Verify reports the time,
// cycles, hours, hybrid, partial-data hybrid and calibration cases; cleanup removes exactly the
// scenario and a rerun is possible; REAL data and the 53 drafts are untouched.
func TestR9LHKSimScenarioToolOverMQTT(t *testing.T) {
	e := newSimEnvWith(t, 30*time.Second, 5*time.Second, []simSpec{{project: onboarding.ProjectID, runtime: simscenario.RuntimeID, asset: onboarding.AssetID, source: "SIMULATION"}}, func(e *simEnv) {
		ctx := context.Background()
		scope, _ := equipmenttypes.Scope(true, nil)
		tp, err := equipmenttypes.PlanTypes(ctx, e.db, scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = equipmenttypes.Apply(ctx, e.db, scope, tp.Digest, "ops", "types"); err != nil {
			t.Fatal(err)
		}
		b, _ := onboarding.Embedded()
		p, _, err := onboarding.PlanImport(ctx, e.db, b)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = onboarding.Apply(ctx, e.db, b, p.Digest, "ops", "import"); err != nil {
			t.Fatal(err)
		}
	})
	ctx := context.Background()
	count := func(q string, args ...any) int {
		var n int
		if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(q, err)
		}
		return n
	}
	plan, err := simscenario.PlanScenario(ctx, e.db)
	if err != nil || len(plan.Problems) != 0 || len(plan.Rules) != len(simscenario.Rules) || plan.RealDrafts != 53 {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if _, err = simscenario.Apply(ctx, e.db, "wrong", "ops", "r", time.Now()); err == nil {
		t.Fatal("wrong digest accepted")
	}
	rec, err := simscenario.Apply(ctx, e.db, plan.Digest, "ops", "r1", time.Now())
	if err != nil || rec.Result != "success" {
		t.Fatalf("apply %+v %v", rec, err)
	}
	again, _ := simscenario.PlanScenario(ctx, e.db)
	if rec2, err := simscenario.Apply(ctx, e.db, again.Digest, "ops", "r2", time.Now()); err != nil || rec2.Result != "noop" {
		t.Fatalf("idempotent apply %+v %v", rec2, err)
	}
	if count(`SELECT count(*) FROM maintenance_alerts`) != 0 || count(`SELECT count(*) FROM meter_readings`) != 0 {
		t.Fatal("the tool wrote alerts or telemetry itself")
	}
	// Real chain: simulator → Mosquitto → cloud; accelerated simulated operating time.
	e.meterSeconds, e.meterSpeedup = 1, 120 // 1 batch / s, 2 simulated minutes per real second
	e.startDevice(simscenario.RuntimeID, "normal")
	e.until("connected", 20*time.Second, func() bool { return e.connected(simscenario.RuntimeID) })
	var rep simscenario.Report
	e.until("scenario verified through MQTT + cloud evaluation", 90*time.Second, func() bool {
		// The cloud maintenance loop evaluates after every stored batch (debounced); nothing here evaluates.
		rep, err = simscenario.Verify(ctx, e.db, time.Now(), 2*time.Minute)
		if err == nil && rep.Verdict != "PASS" && time.Now().Second()%15 == 0 {
			for _, c := range rep.Checks {
				if c.Status != "PASS" {
					t.Logf("waiting: %s %s %s", c.Status, c.Name, c.Detail)
				}
			}
		}
		return err == nil && rep.Verdict == "PASS"
	})
	for _, c := range rep.Checks {
		t.Logf("%s %s %s", c.Status, c.Name, c.Detail)
	}
	simRecord(t, "LHK_SIM_SCENARIO", rep.Verdict)
	if count(`SELECT count(*) FROM maintenance_rules WHERE asset_id='LHK-CRANE-001' AND NOT test_only AND approval_status='DRAFT_UNAPPROVED'`) != 53 ||
		count(`SELECT count(*) FROM maintenance_alerts WHERE source_mode='REAL'`)+count(`SELECT count(*) FROM meter_readings WHERE source_mode='REAL'`) != 0 {
		t.Fatal("drafts changed or REAL data written")
	}
	// Exact cleanup and rerun (simulator stopped so the scope is stable).
	e.stopDevice(simscenario.RuntimeID)
	cp, err := simscenario.PlanCleanup(ctx, e.db, true)
	if err != nil || cp.Counts["rules"] != len(simscenario.Rules) || cp.Counts["alerts"] == 0 || cp.Counts["meter_readings"] == 0 {
		t.Fatalf("cleanup plan %+v %v", cp, err)
	}
	ledger := count(`SELECT count(*) FROM asset_equipment_installations WHERE asset_id='LHK-CRANE-001'`)
	if _, err = simscenario.ApplyCleanup(ctx, e.db, true, cp.Digest, "ops", "c1"); err != nil {
		t.Fatal(err)
	}
	if count(`SELECT count(*) FROM maintenance_rules WHERE rule_code LIKE 'SIM-SCN-%'`)+count(`SELECT count(*) FROM maintenance_alerts`)+
		count(`SELECT count(*) FROM meter_readings`)+count(`SELECT count(*) FROM equipment_meter_baselines`) != 0 {
		t.Fatal("cleanup left scenario data")
	}
	if count(`SELECT count(*) FROM maintenance_rules WHERE asset_id='LHK-CRANE-001' AND NOT test_only`) != 53 ||
		count(`SELECT count(*) FROM asset_equipment_installations WHERE asset_id='LHK-CRANE-001'`) != ledger ||
		count(`SELECT count(*) FROM onboarding_source_rows WHERE asset_id='LHK-CRANE-001'`) != 55 {
		t.Fatal("cleanup touched the imported ledger")
	}
	rerun, _ := simscenario.PlanScenario(ctx, e.db)
	if r, err := simscenario.Apply(ctx, e.db, rerun.Digest, "ops", "r3", time.Now()); err != nil || r.Result != "success" {
		t.Fatalf("rerun %+v %v", r, err)
	}
	simRecord(t, "LHK_SIM_SCENARIO_CLEANUP_RERUN", "PASS")
}
