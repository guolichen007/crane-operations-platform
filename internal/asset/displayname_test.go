package asset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
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

// Over-long names (counted in characters, matching the contract's maxLength 160)
// are rejected with ErrInvalidInput on both create and update, and nothing is
// written. Exactly 160 Chinese characters is accepted.
func TestAssetDisplayNameLengthLimit(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")

	ok := testAsset("Project-A")
	ok.AssetCode = strPtr("CRN-LEN-OK")
	ok.DisplayName = strings.Repeat("机", 160) // 160 runes, 480 bytes
	created, err := CreateAsset(ctx, db, ok, "actor", "req-ok", "")
	if err != nil {
		t.Fatalf("160-character name rejected: %v", err)
	}

	long := testAsset("Project-A")
	long.AssetCode = strPtr("CRN-LEN-BAD")
	long.DisplayName = strings.Repeat("机", 161)
	if _, err := CreateAsset(ctx, db, long, "actor", "req-long", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("161-character create = %v, want ErrInvalidInput", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM asset_devices WHERE asset_code='CRN-LEN-BAD'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected create wrote %d rows (err=%v)", n, err)
	}

	upd := created
	upd.DisplayName = strings.Repeat("机", 161)
	if _, err := UpdateAsset(ctx, db, upd, created.Version, "actor", "req-long-u"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("161-character update = %v, want ErrInvalidInput", err)
	}
	got, err := GetAsset(ctx, db, created.Id)
	if err != nil || got.Version != created.Version || got.DisplayName != created.DisplayName {
		t.Fatalf("rejected update changed row: v%d %q (err=%v)", got.Version, got.DisplayName, err)
	}
}

// A legacy client that predates displayName (field absent → empty string after
// decode) must not wipe the stored name on update, and a fresh GET after a real
// rename returns the new name.
func TestAssetDisplayNameLegacyClientAndReread(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")

	in := testAsset("Project-A")
	in.DisplayName = "四号门机"
	created, err := CreateAsset(ctx, db, in, "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}

	// Legacy payload: decode JSON without displayName, as an old client sends it.
	var legacy contract.AssetMasterData
	if err := json.Unmarshal([]byte(`{"assetType":"GANTRY","assetCode":"CRN-001"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Id = created.Id
	if _, err := UpdateAsset(ctx, db, legacy, created.Version, "actor", "req-2"); err != nil {
		t.Fatal(err)
	}
	afterLegacy, err := GetAsset(ctx, db, created.Id)
	if err != nil || afterLegacy.DisplayName != "四号门机" || afterLegacy.AssetType != "GANTRY" {
		t.Fatalf("legacy update: name=%q type=%q (err=%v), want 四号门机/GANTRY", afterLegacy.DisplayName, afterLegacy.AssetType, err)
	}

	rename := afterLegacy
	rename.DisplayName = "四号门机（大修后）"
	if _, err := UpdateAsset(ctx, db, rename, afterLegacy.Version, "actor", "req-3"); err != nil {
		t.Fatal(err)
	}
	reread, err := GetAsset(ctx, db, created.Id)
	if err != nil || reread.DisplayName != "四号门机（大修后）" || reread.Version != afterLegacy.Version+1 {
		t.Fatalf("reread = %q v%d (err=%v), want 四号门机（大修后） v%d", reread.DisplayName, reread.Version, err, afterLegacy.Version+1)
	}
}
