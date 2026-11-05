package asset

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

// runConcurrent launches the two operations on the pool's independent
// connections with a shared start barrier, returning each error. No sleeps.
func runConcurrent(fn1, fn2 func() error) (error, error) {
	var wg sync.WaitGroup
	var e1, e2 error
	start := make(chan struct{})
	wg.Add(2)
	go func() { defer wg.Done(); <-start; e1 = fn1() }()
	go func() { defer wg.Done(); <-start; e2 = fn2() }()
	close(start)
	wg.Wait()
	return e1, e2
}

// isDeadlockOrTimeout reports a PostgreSQL deadlock (40P01 / "deadlock
// detected") or a bounded-wait timeout — both are failures for a race test.
func isDeadlockOrTimeout(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "40P01") || strings.Contains(s, "deadlock detected") || strings.Contains(s, "context deadline exceeded")
}

// assertOpConsistency verifies a single business write produced exactly one
// success audit and one revision when it succeeded, and none when it failed.
func assertOpConsistency(t *testing.T, db *sql.DB, err error, reqID string) {
	t.Helper()
	want := 0
	if err == nil {
		want = 1
	}
	var audits, revisions int
	_ = db.QueryRow(`SELECT count(*) FROM audit_logs WHERE request_id=$1 AND result='success'`, reqID).Scan(&audits)
	_ = db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE request_id=$1`, reqID).Scan(&revisions)
	if audits != want || revisions != want {
		t.Fatalf("op %s (err=%v): success audits=%d revisions=%d, want %d/%d", reqID, err, audits, revisions, want, want)
	}
}

// A. UpdateEquipment vs RemoveEquipment on the same equipment: with a consistent
// parent-first lock order exactly one succeeds, the other fails with a terminal
// error, and there is no deadlock / partial audit.
func TestConcurrentEquipmentUpdateVsRemove(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	asset, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	eq, _ := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor", "req-1", "")

	update := eq
	update.Criticality = "HIGH"
	uCtx, uCancel := context.WithTimeout(ctx, 30*time.Second)
	defer uCancel()
	rCtx, rCancel := context.WithTimeout(ctx, 30*time.Second)
	defer rCancel()
	eU, eR := runConcurrent(
		func() error { _, err := UpdateEquipment(uCtx, db, update, eq.Version, "actor", "req-a-u"); return err },
		func() error { return RemoveEquipment(rCtx, db, eq.Id, eq.Version, "actor", "req-a-r") },
	)
	if isDeadlockOrTimeout(eU) || isDeadlockOrTimeout(eR) {
		t.Fatalf("deadlock/timeout: update=%v remove=%v", eU, eR)
	}
	succ := 0
	if eU == nil {
		succ++
	} else if !errors.Is(eU, ErrRemoved) && !errors.Is(eU, ErrVersionConflict) {
		t.Fatalf("update err = %v, want nil/ErrRemoved/ErrVersionConflict", eU)
	}
	if eR == nil {
		succ++
	} else if !errors.Is(eR, ErrRemoved) && !errors.Is(eR, ErrVersionConflict) {
		t.Fatalf("remove err = %v, want nil/ErrRemoved/ErrVersionConflict", eR)
	}
	if succ != 1 {
		t.Fatalf("exactly one operation should succeed, got %d (update=%v remove=%v)", succ, eU, eR)
	}
	assertOpConsistency(t, db, eU, "req-a-u")
	assertOpConsistency(t, db, eR, "req-a-r")
}

// B. CreateRelation vs RemoveEquipment on a relation endpoint: both lock the
// parent first, so they serialize; soft removal keeps the FK valid and both
// legitimate writes succeed without deadlock.
func TestConcurrentRelationCreateVsEquipmentRemove(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	asset, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	eq1, _ := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor", "req-1", "")
	eq2, _ := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-2"), motorSpec, "actor", "req-2", "")

	rel := contract.EquipmentRelation{AssetId: asset.Id, FromEquipmentInstallationId: eq1.Id, ToEquipmentInstallationId: eq2.Id, RelationType: "DRIVES"}
	cCtx, cCancel := context.WithTimeout(ctx, 30*time.Second)
	defer cCancel()
	rCtx, rCancel := context.WithTimeout(ctx, 30*time.Second)
	defer rCancel()
	eC, eR := runConcurrent(
		func() error { _, err := CreateRelation(cCtx, db, rel, "actor", "req-b-c"); return err },
		func() error { return RemoveEquipment(rCtx, db, eq1.Id, eq1.Version, "actor", "req-b-r") },
	)
	if isDeadlockOrTimeout(eC) || isDeadlockOrTimeout(eR) {
		t.Fatalf("deadlock/timeout: create=%v remove=%v", eC, eR)
	}
	if eC != nil {
		t.Fatalf("relation create should succeed, got %v", eC)
	}
	if eR != nil {
		t.Fatalf("equipment remove should succeed, got %v", eR)
	}
	assertOpConsistency(t, db, eC, "req-b-c")
	assertOpConsistency(t, db, eR, "req-b-r")
}

// C. RetireAsset vs UpdateEquipment on the same asset: retire always succeeds
// (no active binding), while the equipment update either wins first or is
// refused with ErrRetired once the parent is terminal — never a deadlock.
func TestConcurrentRetireVsEquipmentUpdate(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	asset, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	eq, _ := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor", "req-1", "")

	update := eq
	update.Criticality = "HIGH"
	rCtx, rCancel := context.WithTimeout(ctx, 30*time.Second)
	defer rCancel()
	uCtx, uCancel := context.WithTimeout(ctx, 30*time.Second)
	defer uCancel()
	eR, eU := runConcurrent(
		func() error { return RetireAsset(rCtx, db, asset.Id, asset.Version, "actor", "req-c-r") },
		func() error { _, err := UpdateEquipment(uCtx, db, update, eq.Version, "actor", "req-c-u"); return err },
	)
	if isDeadlockOrTimeout(eR) || isDeadlockOrTimeout(eU) {
		t.Fatalf("deadlock/timeout: retire=%v update=%v", eR, eU)
	}
	if eR != nil {
		t.Fatalf("retire should succeed (no active binding), got %v", eR)
	}
	if eU != nil && !errors.Is(eU, ErrRetired) {
		t.Fatalf("update err = %v, want nil or ErrRetired", eU)
	}
	var status string
	if err := db.QueryRow(`SELECT asset_status FROM asset_devices WHERE id=$1`, asset.Id).Scan(&status); err != nil || status != "RETIRED" {
		t.Fatalf("final asset status = %q (err=%v), want RETIRED", status, err)
	}
	assertOpConsistency(t, db, eR, "req-c-r")
	assertOpConsistency(t, db, eU, "req-c-u")
}
