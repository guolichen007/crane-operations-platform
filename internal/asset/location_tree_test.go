package asset

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

// R6-BIZ-R1 F-08: the location hierarchy must stay a forest. UpdateLocation must
// refuse a self parent, any cycle (A→B→C→A), a parent of another project or a
// disabled new parent, and two concurrent re-parentings must not jointly build a
// cycle. Legal re-parenting and ordinary edits keep working, and an installed
// asset's current location path follows the legal change.

type testDB struct{ db *sql.DB }

func createLoc(t *testing.T, ctx context.Context, d *testDB, project, kind, name string, parent *string) contract.AssetLocation {
	t.Helper()
	l, err := CreateLocation(ctx, d.db, contract.AssetLocation{ProjectId: project, Kind: kind, Name: name, Code: strPtr(name), ParentId: parent}, "actor", "r-"+name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func reparent(ctx context.Context, d *testDB, l contract.AssetLocation, parent *string) (contract.AssetLocation, error) {
	cur, err := GetLocation(ctx, d.db, l.Id)
	if err != nil {
		return cur, err
	}
	cur.ParentId = parent
	return UpdateLocation(ctx, d.db, cur, cur.Version, "actor", "r-up")
}

func cycleRows(t *testing.T, d *testDB) int {
	t.Helper()
	// Any node whose ancestor walk revisits itself within 64 steps is on a cycle.
	var n int
	if err := d.db.QueryRow(`WITH RECURSIVE up(start, id, depth) AS (
	   SELECT id, parent_id, 1 FROM asset_locations WHERE parent_id IS NOT NULL
	   UNION ALL SELECT up.start, l.parent_id, up.depth+1 FROM up JOIN asset_locations l ON l.id=up.id WHERE l.parent_id IS NOT NULL AND up.depth < 64)
	 SELECT count(DISTINCT start) FROM up WHERE id = start`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLocationParentGuards(t *testing.T) {
	d := &testDB{db: testsupport.Database(t)}
	ctx := context.Background()
	seedProject(t, d.db, "Project-A")
	seedProject(t, d.db, "Project-B")
	a := createLoc(t, ctx, d, "Project-A", "SITE", "A", nil)
	b := createLoc(t, ctx, d, "Project-A", "PLANT", "B", &a.Id)
	c := createLoc(t, ctx, d, "Project-A", "WORKSHOP", "C", &b.Id)
	other := createLoc(t, ctx, d, "Project-B", "SITE", "OTHER", nil)
	off := createLoc(t, ctx, d, "Project-A", "SITE", "OFF", nil)
	if err := DisableLocation(ctx, d.db, off.Id, off.Version, "actor", "r-off"); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]struct {
		node   contract.AssetLocation
		parent *string
	}{
		"self parent":            {a, &a.Id},
		"cycle A→B→C→A":          {a, &c.Id},
		"two-node cycle":         {b, &c.Id},
		"parent of another proj": {c, &other.Id},
		"unknown parent":         {c, strPtr("no-such-location")},
		"disabled new parent":    {c, &off.Id},
	} {
		if _, err := reparent(ctx, d, c.node, c.parent); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if n := cycleRows(t, d); n != 0 {
		t.Fatalf("%d locations are on a cycle", n)
	}
	// Ordinary edits are validated like create.
	cur, _ := GetLocation(ctx, d.db, c.Id)
	cur.Name = "  "
	if _, err := UpdateLocation(ctx, d.db, cur, cur.Version, "actor", "r-name"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("blank name: err = %v, want ErrInvalidInput", err)
	}
	cur.Name, cur.Kind = "C", "CITY"
	if _, err := UpdateLocation(ctx, d.db, cur, cur.Version, "actor", "r-kind"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unknown kind: err = %v, want ErrInvalidInput", err)
	}
}

func TestLocationLegalUpdatesAndInstalledAssetPath(t *testing.T) {
	d := &testDB{db: testsupport.Database(t)}
	ctx := context.Background()
	seedProject(t, d.db, "Project-A")
	s1 := createLoc(t, ctx, d, "Project-A", "SITE", "S1", nil)
	s2 := createLoc(t, ctx, d, "Project-A", "SITE", "S2", nil)
	ws := createLoc(t, ctx, d, "Project-A", "WORKSHOP", "WS", &s1.Id)
	a, _ := CreateAsset(ctx, d.db, testAsset("Project-A"), "actor", "r-a", "")
	if err := MoveAsset(ctx, d.db, a.Id, ws.Id, a.Version, "actor", "r-m"); err != nil {
		t.Fatal(err)
	}
	// Rename keeping the same (still ACTIVE) parent.
	cur, _ := GetLocation(ctx, d.db, ws.Id)
	cur.Name = "一车间"
	if _, err := UpdateLocation(ctx, d.db, cur, cur.Version, "actor", "r-rn"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	// Keeping a parent that was disabled later is not a new reference and stays legal.
	p, _ := GetLocation(ctx, d.db, s1.Id)
	if err := DisableLocation(ctx, d.db, s1.Id, p.Version, "actor", "r-d"); err != nil {
		t.Fatal(err)
	}
	cur, _ = GetLocation(ctx, d.db, ws.Id)
	cur.Address = "东区"
	if _, err := UpdateLocation(ctx, d.db, cur, cur.Version, "actor", "r-addr"); err != nil {
		t.Fatalf("edit under a later-disabled parent: %v", err)
	}
	// Legal re-parent to another ACTIVE site and detach to a root.
	if _, err := reparent(ctx, d, ws, &s2.Id); err != nil {
		t.Fatalf("legal re-parent: %v", err)
	}
	rb, err := GetAssetLocation(ctx, d.db, a.Id)
	if err != nil || rb.Current == nil || len(rb.Current.Path) != 2 || rb.Current.Path[0].Id != s2.Id || rb.Current.Name != "一车间" {
		t.Fatalf("installed asset path after re-parent = %+v (err=%v)", rb.Current, err)
	}
	if _, err := reparent(ctx, d, ws, nil); err != nil {
		t.Fatalf("detach to root: %v", err)
	}
	var active int
	if err := d.db.QueryRow(`SELECT count(*) FROM asset_installations WHERE asset_id=$1 AND valid_to IS NULL AND location_id=$2`, a.Id, ws.Id).Scan(&active); err != nil || active != 1 {
		t.Fatalf("installation changed by location edits: %d (err=%v)", active, err)
	}
}

// Two concurrent re-parentings (A under B, B under A) each pass a naive cycle
// check against their own snapshot; serialized per project, exactly one wins.
func TestConcurrentReparentCannotBuildCycle(t *testing.T) {
	d := &testDB{db: testsupport.Database(t)}
	ctx := context.Background()
	seedProject(t, d.db, "Project-A")
	for round := 0; round < 10; round++ {
		x := createLoc(t, ctx, d, "Project-A", "SITE", "X"+string(rune('a'+round)), nil)
		y := createLoc(t, ctx, d, "Project-A", "SITE", "Y"+string(rune('a'+round)), nil)
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, 2)
		for i, pair := range [][2]contract.AssetLocation{{x, y}, {y, x}} {
			wg.Add(1)
			go func(i int, node, parent contract.AssetLocation) {
				defer wg.Done()
				<-start
				_, errs[i] = UpdateLocation(ctx, d.db, contract.AssetLocation{Id: node.Id, ProjectId: node.ProjectId, Kind: node.Kind, Name: node.Name, Code: node.Code, ParentId: &parent.Id}, node.Version, "actor", "race")
			}(i, pair[0], pair[1])
		}
		close(start)
		wg.Wait()
		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			} else if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("round %d: unexpected error %v", round, err)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d re-parentings succeeded (errs=%v), want exactly 1", round, ok, errs)
		}
		if n := cycleRows(t, d); n != 0 {
			t.Fatalf("round %d: %d locations on a cycle", round, n)
		}
	}
}
