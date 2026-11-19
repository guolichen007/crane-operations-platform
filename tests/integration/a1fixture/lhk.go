package main

// R9 B7 on the real B4 stack: the first real asset is imported with the production onboarding
// code, its SIMULATION runtime registered, the reviewed fixture batches (lhk-normalized-
// simulation-v1) ingested through the production metering path, and one test-only threshold rule
// evaluated. tests/e2e/b4/lhk-first-asset.spec.ts then drives the browser; verify-lhk-state checks
// PostgreSQL afterwards. Simulation only: no REAL runtime, no real machine control.

import (
	"context"
	"errors"
	"fmt"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/edge/agent/metersim"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/equipmenttypes"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/metering"
	"github.com/guolichen007/crane-operations-platform/internal/onboarding"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/internal/runtimeidentity"
)

func seedLHK(ctx context.Context) error {
	db, err := fixtureDB()
	if err != nil {
		return err
	}
	defer db.Close()
	// 1. The remaining equipment types (the B4 run initialized MOTOR / PLC only).
	scope, err := equipmenttypes.Scope(true, nil)
	if err != nil {
		return err
	}
	tp, err := equipmenttypes.PlanTypes(ctx, db, scope)
	if err != nil {
		return err
	}
	if _, err = equipmenttypes.Apply(ctx, db, scope, tp.Digest, "b4-fixture-operator", platform.ID()); err != nil {
		return err
	}
	// 2. Production onboarding import (plan → apply → verify).
	staging, _ := onboarding.Embedded()
	plan, _, err := onboarding.PlanImport(ctx, db, staging)
	if err != nil {
		return err
	}
	if _, err = onboarding.Apply(ctx, db, staging, plan.Digest, "b4-fixture-operator", platform.ID()); err != nil {
		return err
	}
	report, err := onboarding.Verify(ctx, db, staging)
	if err != nil || !report.Consistent {
		return errors.New("onboarding verify failed")
	}
	// 3. Grants: b4-maintainer executes maintenance, b4-viewer reads, b4-admin administers.
	event := auditlog.Event{Actor: "anonymous-a1-fixture", RequestID: platform.ID()}
	for user, role := range map[string]string{"b4-admin": "platform_admin", "b4-maintainer": "maintainer", "b4-viewer": "viewer"} {
		if err = identity.ChangeGrant(ctx, db, user, onboarding.ProjectID, role, false, event); err != nil {
			return err
		}
	}
	// 4. SIMULATION runtime via the controlled registration tool (never REAL).
	req := runtimeidentity.Request{TenantId: "demo", Entries: []runtimeidentity.Entry{{RuntimeDeviceId: "SIM-LHK-CRANE-001", ProjectId: onboarding.ProjectID, AssetDeviceId: onboarding.AssetID, SourceMode: "SIMULATION"}}}
	rp, err := runtimeidentity.PlanRegistration(ctx, db, req)
	if err != nil {
		return err
	}
	if _, err = runtimeidentity.Apply(ctx, db, req, rp.Digest, "b4-fixture-operator", platform.ID()); err != nil {
		return err
	}
	// 5a. The simulated instance of GANTRY-01 starts with the fixture's first counter value as its
	// initial usage (recorded before any data: accumulated 0 + 799 999 already used).
	first := 799999.0
	if err = metering.RecordBaseline(ctx, db, onboarding.AssetID, "GANTRY-01", "LHK-P001.cycles", "SIMULATION", &first, "b4-fixture-operator", "模拟初始使用量 = 夹具首值"); err != nil {
		return err
	}
	// 5. The reviewed fixture: every line ingested as one METER batch (57 meters, 1 fault event).
	fx, err := metersim.LoadFixture()
	if err != nil {
		return err
	}
	start := time.Now().UTC().Add(-10 * time.Minute)
	for _, l := range fx.Lines {
		samples := []contract.MeterSample{}
		for _, p := range l.Points {
			q := "GOOD"
			if p.Value == nil {
				q = "MISSING"
			}
			samples = append(samples, contract.MeterSample{PointCode: p.PointCode, Metric: p.Metric, Value: p.Value, Quality: q})
		}
		faults := []contract.FaultSample{}
		at := start.Add(time.Duration(l.RelativeSeconds) * time.Second)
		for _, f := range l.FaultEvents {
			faults = append(faults, contract.FaultSample{PointCode: f.PointCode, EventId: f.EventID, Code: f.Code, Text: f.Text, OccurredAt: at})
		}
		m := contract.WithPayload(contract.Envelope{SchemaVersion: 1, MessageId: platform.ID(), TenantId: "demo", ProjectId: onboarding.ProjectID, DeviceId: "SIM-LHK-CRANE-001",
			BootId: "lhk-fixture", BootSeq: 1, Sequence: int64(l.Sequence), SentAt: at, Kind: "METER"}, contract.MeterBatchPayload{SourceMode: "SIMULATION", TestRunId: l.TestRunID, SampledAt: at, Samples: samples, Faults: faults})
		if err = contract.Validate(m); err != nil {
			return err
		}
		if _, err = metering.Ingest(ctx, db, m); err != nil {
			return err
		}
	}
	// 6. Test-only threshold rule on the total contactor (simulation limit 1 000 000, never REAL).
	limit := 1000000.0
	anchor := time.Now().UTC().AddDate(0, 0, -335)
	days := 365
	if _, err = metering.UpsertTestRule(ctx, db, onboarding.AssetID, metering.TestRule{Code: "T-LHK-CONTACTOR", Name: "大车总接触器 寿命（模拟阈值）", Kind: "REPLACEMENT", Type: "HYBRID",
		Slot: "GANTRY-01", MeterCode: "LHK-P001.cycles", IntervalMeter: &limit, IntervalDays: &days, AnchorAt: &anchor}, "b4-fixture-operator"); err != nil {
		return err
	}
	evs, err := metering.EvaluateAll(ctx, db, onboarding.AssetID, time.Now())
	if err != nil {
		return err
	}
	for _, e := range evs {
		if e.SourceMode == "SIMULATION" && e.Status != metering.StatusDue && e.Status != metering.StatusOverdue {
			return fmt.Errorf("fixture rule did not reach due: %s", e.Status)
		}
	}
	fmt.Println("R9_LHK_FIXTURE=READY RAW_ROWS=55 EQUIPMENT=51 OPERATION_ROWS=4 RUNTIME=SIMULATION METERS=57")
	return nil
}

// verifyLHKState asserts the PostgreSQL state after the browser flow: the simulated work order was
// completed with a replacement, the next cycle restarted, REAL data untouched, original rows intact.
func verifyLHKState(ctx context.Context) error {
	db, err := fixtureDB()
	if err != nil {
		return err
	}
	defer db.Close()
	checks := map[string]struct {
		q    string
		want int
	}{
		"raw rows lossless":              {`SELECT count(*) FROM onboarding_source_rows WHERE asset_id='LHK-CRANE-001'`, 55},
		"equipment rows":                 {`SELECT count(*) FROM onboarding_source_rows WHERE asset_id='LHK-CRANE-001' AND row_kind='EQUIPMENT'`, 51},
		"operation rows":                 {`SELECT count(*) FROM onboarding_source_rows WHERE asset_id='LHK-CRANE-001' AND row_kind='OPERATION'`, 4},
		"points unconfirmed":             {`SELECT count(*) FROM telemetry_points WHERE asset_id='LHK-CRANE-001' AND confirmation_status='UNCONFIRMED'`, 55},
		"no REAL meter data":             {`SELECT count(*) FROM meter_readings WHERE source_mode='REAL'`, 0},
		"no REAL alerts":                 {`SELECT count(*) FROM maintenance_alerts WHERE source_mode='REAL'`, 0},
		"no REAL runtime":                {`SELECT count(*) FROM devices WHERE asset_device_id='LHK-CRANE-001' AND source_mode='REAL'`, 0},
		"sim work order completed":       {`SELECT count(*) FROM maintenance_work_orders WHERE source_mode='SIMULATION' AND status='COMPLETED' AND completed_by='b4-maintainer' AND assigned_to IS NOT NULL`, 1},
		"sim replacement, ledger intact": {`SELECT count(*) FROM equipment_replacements WHERE source_mode='SIMULATION' AND new_equipment_installation_id IS NULL`, 1},
		"real ledger unchanged":          {`SELECT count(*) FROM asset_equipment_installations WHERE asset_id='LHK-CRANE-001' AND valid_to IS NULL`, 51},
		"next cycle":                     {`SELECT count(*) FROM maintenance_schedules s JOIN maintenance_rules r ON r.id=s.rule_id WHERE r.rule_code='T-LHK-CONTACTOR' AND s.cycle_no=2`, 1},
		"fault event once":               {`SELECT count(*) FROM fault_events WHERE event_id='LHK-SIM-FAULT-3'`, 1},
		"audit work order":               {`SELECT count(*) FROM audit_logs WHERE action='work_order_transition' AND user_id='b4-maintainer'`, 3},
	}
	for name, c := range checks {
		var n int
		if err := db.QueryRowContext(ctx, c.q).Scan(&n); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if n != c.want {
			return fmt.Errorf("%s: %d, want %d", name, n, c.want)
		}
	}
	fmt.Println("R9_LHK_DB_STATE=VERIFIED RAW_ROWS_IMPORTED=55/55 EQUIPMENT_SOURCE_ROWS=51/51 OPERATION_RECORDS=4/4 SIM_REAL_ISOLATION=PASS WORK_ORDER=COMPLETED REPLACEMENT=SIMULATED NEXT_CYCLE=2")
	return nil
}
