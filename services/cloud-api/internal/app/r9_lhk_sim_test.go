package app

import (
	"context"
	"testing"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/equipmenttypes"
	"github.com/guolichen007/crane-operations-platform/internal/metering"
	"github.com/guolichen007/crane-operations-platform/internal/onboarding"
)

// R9 B7 over the real chain: the imported first real asset LHK-CRANE-001, its SIMULATION runtime
// SIM-LHK-CRANE-001 registered with runtime-identity (never REAL), the unchanged Edge Agent with the
// edge-side meter source, real Mosquitto (mTLS + generated ACL incl. the new meter topic), the cloud
// runtime loop and PostgreSQL. Proves: all 57 meter sources arrive and increase, nothing is written
// as REAL, a broker outage is back-filled from the spool in order without loss or duplicates, the
// device restart keeps its counters (no zeroing).
func TestR9LHKFirstAssetSimulatedMeteringEndToEnd(t *testing.T) {
	e := newSimEnvWith(t, 30*time.Second, 5*time.Second, []simSpec{{project: onboarding.ProjectID, runtime: "SIM-LHK-CRANE-001", asset: onboarding.AssetID, source: "SIMULATION"}}, func(e *simEnv) {
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
	e.meterSeconds = 1
	count := func(q string) int {
		var n int
		if err := e.db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(q, err)
		}
		return n
	}
	e.startDevice("SIM-LHK-CRANE-001", "normal")
	e.until("connected", 20*time.Second, func() bool { return e.connected("SIM-LHK-CRANE-001") })
	e.until("57 meters receive data", 30*time.Second, func() bool {
		return count(`SELECT count(DISTINCT meter_id) FROM meter_states WHERE source_mode='SIMULATION' AND readings>=2`) == 57
	})
	if n := count(`SELECT count(*) FROM meter_states WHERE source_mode='REAL'`) + count(`SELECT count(*) FROM meter_readings WHERE source_mode='REAL'`); n != 0 {
		t.Fatalf("simulation wrote %d REAL rows", n)
	}
	// The 1-minute-per-second simulated duration meter advances every batch (counts are whole
	// numbers at the edge and may take longer to move).
	acc := func() float64 {
		var v float64
		_ = e.db.QueryRow(`SELECT s.accumulated FROM meter_states s JOIN meter_definitions d ON d.id=s.meter_id WHERE d.meter_code='LHK-P003.hours' AND s.source_mode='SIMULATION'`).Scan(&v)
		return v
	}
	var whole int
	_ = e.db.QueryRow(`SELECT count(*) FROM meter_readings r JOIN meter_definitions d ON d.id=r.meter_id WHERE d.metric='cycles' AND r.raw_value <> trunc(r.raw_value)`).Scan(&whole)
	if whole != 0 {
		t.Fatalf("%d fractional count readings", whole)
	}
	before := acc()
	if before <= 0 {
		t.Fatalf("counter not increasing: %v", before)
	}
	simRecord(t, "LHK_METERS_LIVE", map[string]any{"meters": 57, "p003Hours": before})

	// Broker outage: batches stay in the Edge spool and are back-filled in order after recovery.
	readingsBefore := count(`SELECT count(*) FROM meter_readings`)
	e.killBroker()
	time.Sleep(4 * time.Second)
	e.startBrokerAgain()
	e.until("back-filled after outage", 60*time.Second, func() bool {
		return count(`SELECT count(*) FROM meter_readings`) >= readingsBefore+57*3
	})
	if dup := count(`SELECT count(*) - count(DISTINCT (meter_id, source_mode, runtime_device_id, boot_id, sequence)) FROM meter_readings`); dup != 0 {
		t.Fatalf("duplicate readings after back-fill: %d", dup)
	}
	if out := count(`SELECT count(*) FROM meter_readings WHERE anomaly='OUT_OF_ORDER'`); out != 0 {
		t.Fatalf("spool replay out of order: %d", out)
	}
	afterOutage := acc()
	if afterOutage <= before {
		t.Fatalf("accumulated did not continue after outage: %v → %v", before, afterOutage)
	}
	// Device restart: the edge keeps its counters; the cloud value never drops or zeroes.
	e.stopDevice("SIM-LHK-CRANE-001")
	e.startDevice("SIM-LHK-CRANE-001", "normal")
	e.until("connected again", 20*time.Second, func() bool { return e.connected("SIM-LHK-CRANE-001") })
	e.until("counter continues after restart", 30*time.Second, func() bool { return acc() > afterOutage })
	if n := count(`SELECT count(*) FROM meter_readings WHERE anomaly IN ('COUNTER_RESET','DECREASE')`); n != 0 {
		t.Fatalf("restart produced %d resets / decreases", n)
	}
	// Maintenance evaluation runs from the cloud loop; the drafts stay unevaluated for REAL.
	if _, err := metering.EvaluateAll(context.Background(), e.db, onboarding.AssetID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM maintenance_alerts WHERE source_mode='REAL'`); n != 0 {
		t.Fatalf("REAL alerts from simulation: %d", n)
	}
	simRecord(t, "LHK_SIM_E2E", map[string]any{"p003HoursFinal": acc(), "readings": count(`SELECT count(*) FROM meter_readings`)})
}
