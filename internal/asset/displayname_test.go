package asset

import (
	"context"
	"strings"
	"testing"

	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

// display_name is a real editable business field: it is persisted on create,
// returned on read, editable on update, and blank updates keep the existing name
// (backward compatible with clients that predate the field).
func TestAssetDisplayNamePersistedAndEditable(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")

	in := testAsset("Project-A")
	in.DisplayName = "一号门机"
	created, err := CreateAsset(ctx, db, in, "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if created.DisplayName != "一号门机" {
		t.Fatalf("created display name = %q, want 一号门机", created.DisplayName)
	}
	got, err := GetAsset(ctx, db, created.Id)
	if err != nil || got.DisplayName != "一号门机" {
		t.Fatalf("get display name = %q (err=%v), want 一号门机", got.DisplayName, err)
	}

	upd := got
	upd.DisplayName = "一号门机·改"
	updated, err := UpdateAsset(ctx, db, upd, got.Version, "actor", "req-2")
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "一号门机·改" {
		t.Fatalf("updated display name = %q, want 一号门机·改", updated.DisplayName)
	}

	// Blank name on update keeps the existing name (historical compatibility).
	upd2 := updated
	upd2.DisplayName = ""
	kept, err := UpdateAsset(ctx, db, upd2, updated.Version, "actor", "req-3")
	if err != nil {
		t.Fatal(err)
	}
	if kept.DisplayName != "一号门机·改" {
		t.Fatalf("blank update kept display name = %q, want 一号门机·改", kept.DisplayName)
	}
}

// Whitespace-only and absent display names fall back to the asset code (a real
// business name, not a second id); a Chinese name round-trips; the created
// revision snapshot records the resolved name; idempotent replay preserves it.
func TestAssetDisplayNameFallbackAndSnapshot(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")

	// Whitespace-only + no explicit name → fallback to asset code.
	in := testAsset("Project-A")
	in.AssetCode = strPtr("CRN-9001")
	in.DisplayName = "   "
	created, err := CreateAsset(ctx, db, in, "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if created.DisplayName != "CRN-9001" {
		t.Fatalf("whitespace display name = %q, want fallback CRN-9001", created.DisplayName)
	}

	// Chinese name round-trips and lands in the created revision snapshot.
	in2 := testAsset("Project-A")
	in2.AssetCode = strPtr("CRN-9002")
	in2.DisplayName = "二号门式起重机"
	created2, err := CreateAsset(ctx, db, in2, "actor", "req-2", "")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot string
	if err := db.QueryRow(`SELECT snapshot_json::text FROM entity_revisions WHERE entity_type='asset' AND entity_id=$1 AND changed_fields='{"created"}'`, created2.Id).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot, "二号门式起重机") {
		t.Fatalf("created revision snapshot missing display name: %s", snapshot)
	}

	// Idempotent replay with the same key returns the same id and name.
	in3 := testAsset("Project-A")
	in3.AssetCode = strPtr("CRN-9003")
	in3.DisplayName = "三号门式起重机"
	first, err := CreateAsset(ctx, db, in3, "actor", "req-3", "idem-name3")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := CreateAsset(ctx, db, in3, "actor", "req-4", "idem-name3")
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Id != first.Id || replayed.DisplayName != "三号门式起重机" {
		t.Fatalf("replay = %q/%q, want %q/三号门式起重机", replayed.Id, replayed.DisplayName, first.Id)
	}
}
