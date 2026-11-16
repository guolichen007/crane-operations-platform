package asset

import (
	"context"
	"errors"
	"sync"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

// R6-BIZ-R1 F-03: never-installed asset → honest empty state; after moves the
// current location, full SITE→PLANT→WORKSHOP path and newest-first history match
// asset_installations; reading writes nothing; a retired asset keeps both.
func TestAssetLocationReadbackAndHistory(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	a, err := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := GetAssetLocation(ctx, db, a.Id)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Current != nil || empty.ProjectId != "Project-A" || empty.AssetStatus != a.AssetStatus {
		t.Fatalf("never-installed readback = %+v, want current=null", empty)
	}
	h, err := ListAssetInstallationHistory(ctx, db, a.Id, ListOptions{})
	if err != nil || h.Total != 0 || len(h.Items) != 0 {
		t.Fatalf("never-installed history = %+v (err=%v)", h, err)
	}

	site, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "SITE", Name: "一号场地", Code: strPtr("S1")}, "actor", "r1")
	if err != nil {
		t.Fatal(err)
	}
	plant, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "PLANT", Name: "一号厂房", ParentId: &site.Id}, "actor", "r2")
	if err != nil {
		t.Fatal(err)
	}
	ws1, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "WORKSHOP", Name: "一车间", Code: strPtr("W1"), ParentId: &plant.Id}, "actor", "r3")
	if err != nil {
		t.Fatal(err)
	}
	ws2, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "WORKSHOP", Name: "二车间", Code: strPtr("W2"), ParentId: &plant.Id}, "actor", "r4")
	if err != nil {
		t.Fatal(err)
	}
	if err := MoveAsset(ctx, db, a.Id, ws1.Id, 1, "actor", "m1"); err != nil {
		t.Fatal(err)
	}
	first, err := GetAssetLocation(ctx, db, a.Id)
	if err != nil || first.Current == nil || first.Current.LocationId != ws1.Id {
		t.Fatalf("after first move = %+v (err=%v)", first, err)
	}
	if err := MoveAsset(ctx, db, a.Id, ws2.Id, 2, "actor", "m2"); err != nil {
		t.Fatal(err)
	}
	cur, err := GetAssetLocation(ctx, db, a.Id)
	if err != nil {
		t.Fatal(err)
	}
	c := cur.Current
	if c == nil || c.LocationId != ws2.Id || c.Name != "二车间" || c.Kind != "WORKSHOP" || c.Status != "ACTIVE" || c.Code == nil || *c.Code != "W2" {
		t.Fatalf("current = %+v", c)
	}
	if len(c.Path) != 3 || c.Path[0].Id != site.Id || c.Path[0].Kind != "SITE" || c.Path[1].Id != plant.Id || c.Path[2].Id != ws2.Id {
		t.Fatalf("path = %+v, want SITE→PLANT→WORKSHOP", c.Path)
	}
	var dbCurrent string
	if err := db.QueryRow(`SELECT id FROM asset_installations WHERE asset_id=$1 AND valid_to IS NULL`, a.Id).Scan(&dbCurrent); err != nil || dbCurrent != c.InstallationId {
		t.Fatalf("installation id %s vs db %s (err=%v)", c.InstallationId, dbCurrent, err)
	}
	if !c.ValidFrom.After(first.Current.ValidFrom) && !c.ValidFrom.Equal(first.Current.ValidFrom) {
		t.Fatal("validFrom went backwards")
	}

	hist, err := ListAssetInstallationHistory(ctx, db, a.Id, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if hist.Total != 2 || len(hist.Items) != 2 || hist.Limit != 100 || hist.Offset != 0 {
		t.Fatalf("history = %+v", hist)
	}
	newest, oldest := hist.Items[0], hist.Items[1]
	if !newest.Current || newest.ValidTo != nil || newest.LocationId != ws2.Id || oldest.Current || oldest.ValidTo == nil || oldest.LocationId != ws1.Id {
		t.Fatalf("history order/currency = %+v / %+v", newest, oldest)
	}
	if !oldest.ValidTo.Equal(newest.ValidFrom) && oldest.ValidTo.After(newest.ValidFrom) {
		t.Fatal("previous installation closed after the next began")
	}
	if len(oldest.Path) != 3 || oldest.Path[2].Name != "一车间" {
		t.Fatalf("old path = %+v", oldest.Path)
	}
	page, err := ListAssetInstallationHistory(ctx, db, a.Id, ListOptions{Limit: 1, Offset: 1})
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].InstallationId != oldest.InstallationId {
		t.Fatalf("page 2 = %+v (err=%v)", page, err)
	}

	// Read has no side effects: no revisions, audits, versions or installation rows change.
	fingerprint := func() string {
		var s string
		if err := db.QueryRow(`SELECT (SELECT count(*) FROM entity_revisions)::text||'/'||(SELECT count(*) FROM audit_logs)::text||'/'||(SELECT version FROM asset_devices WHERE id=$1)::text||'/'||(SELECT count(*) FROM asset_installations)::text`, a.Id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := fingerprint()
	for i := 0; i < 3; i++ {
		_, _ = GetAssetLocation(ctx, db, a.Id)
		_, _ = ListAssetInstallationHistory(ctx, db, a.Id, ListOptions{})
	}
	if after := fingerprint(); after != before {
		t.Fatalf("reads changed state %s → %s", before, after)
	}

	// A disabled current location is reported as such (not hidden, not invented).
	if err := DisableLocation(ctx, db, ws2.Id, ws2.Version, "actor", "d1"); err != nil {
		t.Fatal(err)
	}
	// Retire: readback and history stay readable.
	if err := RetireAsset(ctx, db, a.Id, 3, "actor", "ret"); err != nil {
		t.Fatal(err)
	}
	retired, err := GetAssetLocation(ctx, db, a.Id)
	if err != nil || retired.AssetStatus != "RETIRED" || retired.Current == nil || retired.Current.Status != "DISABLED" {
		t.Fatalf("retired readback = %+v (err=%v)", retired, err)
	}
	if h, err := ListAssetInstallationHistory(ctx, db, a.Id, ListOptions{}); err != nil || h.Total != 2 {
		t.Fatalf("retired history = %+v (err=%v)", h, err)
	}
	if _, err := GetAssetLocation(ctx, db, "no-such-asset"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown asset: %v", err)
	}
	if _, err := ListAssetInstallationHistory(ctx, db, "no-such-asset", ListOptions{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown asset history: %v", err)
	}
}

// Concurrent moves with the same If-Match version: exactly one wins, the other is
// a version conflict, and exactly one current installation remains — the winner's.
func TestConcurrentMoveLeavesExactlyOneCurrentLocation(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	a, _ := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-0", "")
	l1, _ := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "SITE", Name: "L1", Code: strPtr("L1")}, "actor", "r1")
	l2, _ := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "SITE", Name: "L2", Code: strPtr("L2")}, "actor", "r2")
	for round := 0; round < 5; round++ {
		var version int64
		if err := db.QueryRow(`SELECT version FROM asset_devices WHERE id=$1`, a.Id).Scan(&version); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, 2)
		targets := []string{l1.Id, l2.Id}
		for i := range targets {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				errs[i] = MoveAsset(ctx, db, a.Id, targets[i], version, "actor", "race")
			}(i)
		}
		close(start)
		wg.Wait()
		winners := 0
		winner := ""
		for i, err := range errs {
			switch {
			case err == nil:
				winners++
				winner = targets[i]
			case errors.Is(err, ErrVersionConflict):
			default:
				t.Fatalf("round %d: unexpected error %v", round, err)
			}
		}
		if winners != 1 {
			t.Fatalf("round %d: %d winners (errs=%v), want exactly 1", round, winners, errs)
		}
		var active int
		if err := db.QueryRow(`SELECT count(*) FROM asset_installations WHERE asset_id=$1 AND valid_to IS NULL`, a.Id).Scan(&active); err != nil || active != 1 {
			t.Fatalf("round %d: active installations = %d (err=%v)", round, active, err)
		}
		cur, err := GetAssetLocation(ctx, db, a.Id)
		if err != nil || cur.Current == nil || cur.Current.LocationId != winner {
			t.Fatalf("round %d: current = %+v, winner %s (err=%v)", round, cur.Current, winner, err)
		}
	}
	h, err := ListAssetInstallationHistory(ctx, db, a.Id, ListOptions{})
	if err != nil || h.Total != 5 {
		t.Fatalf("history total = %d (err=%v), want 5 (one per round)", h.Total, err)
	}
	currents := 0
	for _, r := range h.Items {
		if r.Current {
			currents++
		}
	}
	if currents != 1 {
		t.Fatalf("history marks %d current records", currents)
	}
}
