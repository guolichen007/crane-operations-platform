package asset

import (
	"context"
	"database/sql"
	"testing"

	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

// A real business write must correlate HTTP X-Request-Id with entity_revisions
// and audit_logs: the same non-empty request id, same actor, same project, and
// exactly one success audit. A version conflict / idempotent replay must not
// fabricate extra success records.
func TestAssetWriteAuditCorrelation(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")

	created, err := CreateAsset(ctx, db, testAsset("Project-A"), "actor-1", "req-corr-1", "")
	if err != nil {
		t.Fatal(err)
	}

	var revRequestID, revActor, revProject string
	if err := db.QueryRow(`SELECT request_id, changed_by, project_id FROM entity_revisions WHERE entity_type='asset' AND entity_id=$1`, created.Id).Scan(&revRequestID, &revActor, &revProject); err != nil {
		t.Fatal(err)
	}
	if revRequestID != "req-corr-1" {
		t.Fatalf("entity_revisions.request_id = %q, want req-corr-1", revRequestID)
	}
	if revActor != "actor-1" || revProject != "Project-A" {
		t.Fatalf("entity_revisions actor/project = %q/%q, want actor-1/Project-A", revActor, revProject)
	}

	var auditRequestID, auditActor, auditProject string
	if err := db.QueryRow(`SELECT request_id, user_id, project_id FROM audit_logs WHERE action='asset_created' AND project_id='Project-A'`).Scan(&auditRequestID, &auditActor, &auditProject); err != nil {
		t.Fatal(err)
	}
	if auditRequestID != "req-corr-1" || auditRequestID != revRequestID {
		t.Fatalf("audit_logs.request_id = %q vs entity_revisions.request_id = %q; must match and equal req-corr-1", auditRequestID, revRequestID)
	}
	if auditActor != "actor-1" || auditProject != "Project-A" {
		t.Fatalf("audit_logs actor/project = %q/%q, want actor-1/Project-A", auditActor, auditProject)
	}
}

// A version-conflict failure must not write a success audit row.
func TestFailedWriteWritesNoSuccessAudit(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	created, err := CreateAsset(ctx, db, testAsset("Project-A"), "actor-1", "req-ok", "")
	if err != nil {
		t.Fatal(err)
	}
	// Wrong expected version → conflict; no audit_logs success row for this attempt.
	update := created
	update.AssetCode = strPtr("CRN-002")
	if _, err := UpdateAsset(ctx, db, update, created.Version+999, "actor-1", "req-conflict"); err != ErrVersionConflict {
		t.Fatalf("UpdateAsset conflict = %v, want ErrVersionConflict", err)
	}
	var successCount int
	if err := db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='asset_updated' AND result='success' AND request_id='req-conflict'`).Scan(&successCount); err != nil {
		t.Fatal(err)
	}
	if successCount != 0 {
		t.Fatalf("conflict produced %d success audit rows, want 0", successCount)
	}
}

// Idempotent equipment create must produce exactly one success audit + one
// revision even across a replay.
func TestEquipmentIdempotentAuditCount(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	asset, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor-1", "req-a", "")
	first, err := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor-1", "req-1", "ekey")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateEquipment(ctx, db, equipmentInput(asset.Id, "EQ-1"), motorSpec, "actor-1", "req-2", "ekey"); err != nil {
		t.Fatal(err)
	}
	var audits, revisions int
	_ = db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='equipment_created' AND project_id='Project-A'`).Scan(&audits)
	_ = db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE entity_type='equipment' AND entity_id=$1`, first.Id).Scan(&revisions)
	if audits != 1 || revisions != 1 {
		t.Fatalf("idempotent replay: audits=%d revisions=%d, want 1/1", audits, revisions)
	}
}

var _ = sql.ErrNoRows // keep database/sql import if unused in future edits
