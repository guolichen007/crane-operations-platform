package scheduling

import (
	"context"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

func TestOverlapsHalfOpen(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Back-to-back [10:00,11:00) and [11:00,12:00) must not conflict.
	if Overlaps(base, base.Add(time.Hour), base.Add(time.Hour), base.Add(2*time.Hour)) {
		t.Fatal("back-to-back ranges reported overlap")
	}
	// [10:00,11:00) and [10:59,12:00) must conflict.
	if !Overlaps(base, base.Add(time.Hour), base.Add(59*time.Minute), base.Add(2*time.Hour)) {
		t.Fatal("overlapping ranges not reported")
	}
}

func TestCapacityPeakSegmented(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Two existing windows that do not coexist across the whole candidate span.
	existing := []quantityWindow{
		{base.Add(9 * time.Hour), base.Add(10 * time.Hour), 3},
		{base.Add(10 * time.Hour), base.Add(11 * time.Hour), 3},
	}
	// Candidate 09:30-10:30 qty 2: peak is 5, not 6 (naive SUM would be wrong).
	if peak := capacityPeak(existing, base.Add(9*time.Hour+30*time.Minute), base.Add(10*time.Hour+30*time.Minute), 2); peak != 5 {
		t.Fatalf("peak = %d, want 5", peak)
	}
	// Candidate qty 3 pushes the 10:00-10:30 segment to 6.
	if peak := capacityPeak(existing, base.Add(9*time.Hour+30*time.Minute), base.Add(10*time.Hour+30*time.Minute), 3); peak != 6 {
		t.Fatalf("peak = %d, want 6", peak)
	}
}

func TestExclusiveResourceOverlapConflict(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS-1','demo','PS-1','Portfolio 1')`); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour)
	itemA := contract.ScheduleItem{SourceType: "PROJECT_ACTIVITY", SourceId: "a", Name: "A", StartAt: base, EndAt: base.Add(2 * time.Hour), Timezone: "UTC", Status: "PLANNED", Priority: "MEDIUM", Version: 1}
	itemB := contract.ScheduleItem{SourceType: "PROJECT_ACTIVITY", SourceId: "b", Name: "B", StartAt: base.Add(time.Hour), EndAt: base.Add(3 * time.Hour), Timezone: "UTC", Status: "PLANNED", Priority: "MEDIUM", Version: 1}
	a, err := CreateScheduleItem(ctx, db, itemA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CreateScheduleItem(ctx, db, itemB)
	if err != nil {
		t.Fatal(err)
	}
	res := contract.Resource{PortfolioScopeId: "PS-1", ResourceType: "RUNWAY", Name: "Runway 1", Capacity: 1, Exclusive: true, OverrideAllowed: false, HomeTimezone: "UTC", Status: "ACTIVE", Version: 1}
	if _, err := CreateResource(ctx, db, res); err != nil {
		t.Fatal(err)
	}
	var resID string
	if err := db.QueryRow(`SELECT id FROM resources WHERE name='Runway 1'`).Scan(&resID); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: a.Id, ResourceId: resID, StartAt: base, EndAt: base.Add(2 * time.Hour), Quantity: 1, Status: "ACTIVE", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: b.Id, ResourceId: resID, StartAt: base.Add(time.Hour), EndAt: base.Add(3 * time.Hour), Quantity: 1, Status: "ACTIVE", Version: 1}); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	conflicts, err := DetectConflicts(ctx, tx, b.Id)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range conflicts {
		if c.Type == "RESOURCE_OVERLAP" && c.Severity == "BLOCKING" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected exclusive overlap conflict, got %+v", conflicts)
	}
}

func TestBackToBackReservationNoConflict(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS-2','demo','PS-2','Portfolio 2')`); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour)
	a, err := CreateScheduleItem(ctx, db, contract.ScheduleItem{SourceType: "PROJECT_ACTIVITY", SourceId: "a", Name: "A", StartAt: base, EndAt: base.Add(time.Hour), Timezone: "UTC", Status: "PLANNED", Priority: "MEDIUM", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := CreateScheduleItem(ctx, db, contract.ScheduleItem{SourceType: "PROJECT_ACTIVITY", SourceId: "b", Name: "B", StartAt: base.Add(time.Hour), EndAt: base.Add(2 * time.Hour), Timezone: "UTC", Status: "PLANNED", Priority: "MEDIUM", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	res := contract.Resource{PortfolioScopeId: "PS-2", ResourceType: "RUNWAY", Name: "Runway 2", Capacity: 1, Exclusive: true, OverrideAllowed: false, HomeTimezone: "UTC", Status: "ACTIVE", Version: 1}
	if _, err := CreateResource(ctx, db, res); err != nil {
		t.Fatal(err)
	}
	var resID string
	if err := db.QueryRow(`SELECT id FROM resources WHERE name='Runway 2'`).Scan(&resID); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: a.Id, ResourceId: resID, StartAt: base, EndAt: base.Add(time.Hour), Quantity: 1, Status: "ACTIVE", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: b.Id, ResourceId: resID, StartAt: base.Add(time.Hour), EndAt: base.Add(2 * time.Hour), Quantity: 1, Status: "ACTIVE", Version: 1}); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	conflicts, err := DetectConflicts(ctx, tx, b.Id)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("back-to-back reservations should not conflict, got %+v", conflicts)
	}
}

func TestConcurrentExclusiveApprovalSingleWinner(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS-3','demo','PS-3','Portfolio 3')`); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour)
	a, err := CreateScheduleItem(ctx, db, contract.ScheduleItem{SourceType: "PROJECT_ACTIVITY", SourceId: "a", Name: "A", StartAt: base, EndAt: base.Add(2 * time.Hour), Timezone: "UTC", Status: "PLANNED", Priority: "MEDIUM", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := CreateScheduleItem(ctx, db, contract.ScheduleItem{SourceType: "PROJECT_ACTIVITY", SourceId: "b", Name: "B", StartAt: base.Add(time.Hour), EndAt: base.Add(3 * time.Hour), Timezone: "UTC", Status: "PLANNED", Priority: "MEDIUM", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateResource(ctx, db, contract.Resource{PortfolioScopeId: "PS-3", ResourceType: "RUNWAY", Name: "Runway 3", Capacity: 1, Exclusive: true, OverrideAllowed: false, HomeTimezone: "UTC", Status: "ACTIVE", Version: 1}); err != nil {
		t.Fatal(err)
	}
	var resID string
	if err := db.QueryRow(`SELECT id FROM resources WHERE name='Runway 3'`).Scan(&resID); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: a.Id, ResourceId: resID, StartAt: base, EndAt: base.Add(2 * time.Hour), Quantity: 1, Status: "ACTIVE", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: b.Id, ResourceId: resID, StartAt: base.Add(time.Hour), EndAt: base.Add(3 * time.Hour), Quantity: 1, Status: "ACTIVE", Version: 1}); err != nil {
		t.Fatal(err)
	}
	// Two goroutines approve overlapping items; at most one may succeed because
	// the advisory lock serializes and the second sees the exclusive overlap.
	type result struct {
		conflicts []Conflict
		err       error
	}
	ch := make(chan result, 2)
	run := func(itemID string) {
		conflicts, err := ApproveSchedule(ctx, db, []string{itemID}, "tester", "req-1")
		ch <- result{conflicts, err}
	}
	go run(a.Id)
	go run(b.Id)
	successes := 0
	for i := 0; i < 2; i++ {
		r := <-ch
		if r.err == nil {
			successes++
		}
	}
	if successes == 2 {
		t.Fatal("two concurrent approvals both succeeded on an exclusive resource")
	}
	if successes == 0 {
		t.Fatal("neither approval succeeded; expected exactly one winner")
	}
}
