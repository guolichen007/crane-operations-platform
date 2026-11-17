package asset

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

// R6-BIZ-R1 F-06: every successful move writes exactly one audit row, one asset revision
// (changed_fields {moved}, same request id, snapshot naming the closed and opened
// installation) and one LOCATION timeline event referencing the new installation. A refused
// move (stale version, disabled or foreign location) writes none of them.
func TestMoveAssetTraceability(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedProject(t, db, "Project-B")
	a, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-c", "")
	l1, _ := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "SITE", Name: "L1"}, "actor", "r1")
	l2, _ := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "SITE", Name: "L2"}, "actor", "r2")
	foreign, _ := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-B", Kind: "SITE", Name: "B"}, "actor", "r3")

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for i, step := range []struct{ loc, req string }{{l1.Id, "mv-1"}, {l2.Id, "mv-2"}} {
		if err := MoveAsset(ctx, db, a.Id, step.loc, int64(1+i), "mover", step.req); err != nil {
			t.Fatal(err)
		}
		if n := count(`SELECT count(*) FROM audit_logs WHERE action='asset_moved' AND result='success' AND request_id=$1 AND user_id='mover' AND project_id='Project-A'`, step.req); n != 1 {
			t.Fatalf("%s audit rows = %d", step.req, n)
		}
		var snap string
		var rev int64
		if err := db.QueryRow(`SELECT snapshot_json::text, revision FROM entity_revisions WHERE entity_type='asset' AND entity_id=$1 AND request_id=$2 AND changed_fields='{moved}' AND changed_by='mover'`, a.Id, step.req).Scan(&snap, &rev); err != nil {
			t.Fatalf("%s revision: %v", step.req, err)
		}
		var s struct {
			Version int64 `json:"version"`
			Move    struct {
				From *struct{ InstallationId, LocationId string } `json:"from"`
				To   struct{ InstallationId, LocationId string }  `json:"to"`
			} `json:"move"`
		}
		if err := json.Unmarshal([]byte(snap), &s); err != nil {
			t.Fatal(err)
		}
		var current string
		if err := db.QueryRow(`SELECT id FROM asset_installations WHERE asset_id=$1 AND valid_to IS NULL`, a.Id).Scan(&current); err != nil {
			t.Fatal(err)
		}
		if s.Version != int64(2+i) || s.Move.To.LocationId != step.loc || s.Move.To.InstallationId != current {
			t.Fatalf("%s snapshot = %s", step.req, snap)
		}
		if (i == 0) != (s.Move.From == nil) || (i == 1 && s.Move.From.LocationId != l1.Id) {
			t.Fatalf("%s from = %+v", step.req, s.Move.From)
		}
		if n := count(`SELECT count(*) FROM asset_timeline_events WHERE asset_id=$1 AND category='LOCATION' AND reference_type='asset_installation' AND reference_id=$2`, a.Id, current); n != 1 {
			t.Fatalf("%s timeline rows = %d", step.req, n)
		}
	}
	before := count(`SELECT (SELECT count(*) FROM audit_logs)+(SELECT count(*) FROM entity_revisions)+(SELECT count(*) FROM asset_timeline_events)+(SELECT count(*) FROM asset_installations)`)
	if err := MoveAsset(ctx, db, a.Id, l1.Id, 1, "mover", "mv-stale"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale move: %v", err)
	}
	if err := MoveAsset(ctx, db, a.Id, foreign.Id, 3, "mover", "mv-foreign"); !errors.Is(err, ErrCrossProject) {
		t.Fatalf("foreign move: %v", err)
	}
	if err := DisableLocation(ctx, db, l1.Id, l1.Version, "actor", "dis"); err != nil {
		t.Fatal(err)
	}
	before += 2 // the disable's own revision and audit row
	if err := MoveAsset(ctx, db, a.Id, l1.Id, 3, "mover", "mv-disabled"); !errors.Is(err, ErrDisabledLocation) {
		t.Fatalf("disabled move: %v", err)
	}
	if after := count(`SELECT (SELECT count(*) FROM audit_logs)+(SELECT count(*) FROM entity_revisions)+(SELECT count(*) FROM asset_timeline_events)+(SELECT count(*) FROM asset_installations)`); after != before {
		t.Fatalf("refused moves wrote rows: %d → %d", before, after)
	}
	// Revisions of the asset stay strictly sequential (created, moved, moved).
	if n := count(`SELECT count(*) FROM entity_revisions WHERE entity_type='asset' AND entity_id=$1`, a.Id); n != 3 {
		t.Fatalf("asset revisions = %d, want 3", n)
	}
}
