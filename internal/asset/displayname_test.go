package asset

import (
	"context"
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
