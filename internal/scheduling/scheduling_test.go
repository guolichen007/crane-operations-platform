package scheduling

import (
	"context"
	"database/sql"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

func strPtr(s string) *string { return &s }

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

// fixtureScope creates a portfolio scope and returns its id.
func fixtureScope(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES($1,'demo',$1,$2)`, id, "Portfolio "+id); err != nil {
		t.Fatal(err)
	}
}

// fixtureItem creates a PLANNED schedule item in the given portfolio scope.
func fixtureItem(t *testing.T, db *sql.DB, scope string, start, end time.Time) contract.ScheduleItem {
	t.Helper()
	item, err := CreateScheduleItem(context.Background(), db, contract.ScheduleItem{
		PortfolioScopeId: strPtr(scope),
		SourceType:       "PROJECT_ACTIVITY",
		SourceId:         platform.ID(),
		Name:             "item",
		StartAt:          start,
		EndAt:            end,
		Timezone:         "UTC",
		Status:           "PLANNED",
		Priority:         "MEDIUM",
		Version:          1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

// fixtureResource creates an exclusive resource in the given scope.
func fixtureResource(t *testing.T, db *sql.DB, scope, name string) string {
	t.Helper()
	res := contract.Resource{PortfolioScopeId: scope, ResourceType: "RUNWAY", Name: name, Capacity: 1, Exclusive: true, OverrideAllowed: false, HomeTimezone: "UTC", Status: "ACTIVE", Version: 1}
	if _, err := CreateResource(context.Background(), db, res); err != nil {
		t.Fatal(err)
	}
	var resID string
	if err := db.QueryRow(`SELECT id FROM resources WHERE name=$1`, name).Scan(&resID); err != nil {
		t.Fatal(err)
	}
	return resID
}

func TestExclusiveResourceOverlapConflict(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	fixtureScope(t, db, "PS-1")
	base := time.Now().UTC().Truncate(time.Hour)
	a := fixtureItem(t, db, "PS-1", base, base.Add(2*time.Hour))
	b := fixtureItem(t, db, "PS-1", base.Add(time.Hour), base.Add(3*time.Hour))
	resID := fixtureResource(t, db, "PS-1", "Runway 1")
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: a.Id, ResourceId: resID, StartAt: base, EndAt: base.Add(2 * time.Hour), Quantity: 1, Status: "PENDING", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: b.Id, ResourceId: resID, StartAt: base.Add(time.Hour), EndAt: base.Add(3 * time.Hour), Quantity: 1, Status: "PENDING", Version: 1}); err != nil {
		t.Fatal(err)
	}
	// Promote item A's reservation to ACTIVE so it is a competitor of B.
	if _, err := db.Exec(`UPDATE resource_reservations SET status='ACTIVE' WHERE schedule_item_id=$1`, a.Id); err != nil {
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
	fixtureScope(t, db, "PS-2")
	base := time.Now().UTC().Truncate(time.Hour)
	a := fixtureItem(t, db, "PS-2", base, base.Add(time.Hour))
	b := fixtureItem(t, db, "PS-2", base.Add(time.Hour), base.Add(2*time.Hour))
	resID := fixtureResource(t, db, "PS-2", "Runway 2")
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: a.Id, ResourceId: resID, StartAt: base, EndAt: base.Add(time.Hour), Quantity: 1, Status: "PENDING", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: b.Id, ResourceId: resID, StartAt: base.Add(time.Hour), EndAt: base.Add(2 * time.Hour), Quantity: 1, Status: "PENDING", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE resource_reservations SET status='ACTIVE' WHERE schedule_item_id=$1`, a.Id); err != nil {
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

// TestConcurrentExclusiveApprovalSingleWinner proves exactly one concurrent
// approval of two overlapping PLANNED items on one exclusive resource wins: the
// advisory resource lock serializes, the loser sees the winner's ACTIVE
// reservation and stays PENDING.
func TestConcurrentExclusiveApprovalSingleWinner(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	fixtureScope(t, db, "PS-3")
	base := time.Now().UTC().Truncate(time.Hour)
	a := fixtureItem(t, db, "PS-3", base, base.Add(2*time.Hour))
	b := fixtureItem(t, db, "PS-3", base.Add(time.Hour), base.Add(3*time.Hour))
	resID := fixtureResource(t, db, "PS-3", "Runway 3")
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: a.Id, ResourceId: resID, StartAt: base, EndAt: base.Add(2 * time.Hour), Quantity: 1, Status: "PENDING", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: b.Id, ResourceId: resID, StartAt: base.Add(time.Hour), EndAt: base.Add(3 * time.Hour), Quantity: 1, Status: "PENDING", Version: 1}); err != nil {
		t.Fatal(err)
	}
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
	successes, blocked := 0, 0
	for i := 0; i < 2; i++ {
		r := <-ch
		if r.err == nil {
			successes++
		} else {
			blocked++
		}
	}
	if successes != 1 || blocked != 1 {
		t.Fatalf("successes=%d blocked=%d; want exactly one winner", successes, blocked)
	}
	var approved, active, pending int
	if err := db.QueryRow(`SELECT count(*) FROM schedule_items WHERE status='APPROVED'`).Scan(&approved); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM resource_reservations WHERE status='ACTIVE'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM resource_reservations WHERE status='PENDING'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if approved != 1 || active != 1 || pending != 1 {
		t.Fatalf("approved=%d active=%d pending=%d; want 1/1/1", approved, active, pending)
	}
}

// TestCrossPortfolioReservationRejected proves a schedule item can never reserve
// a resource of another portfolio (Go guard + DB composite FK).
func TestCrossPortfolioReservationRejected(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	fixtureScope(t, db, "PS-A")
	fixtureScope(t, db, "PS-B")
	base := time.Now().UTC().Truncate(time.Hour)
	item := fixtureItem(t, db, "PS-A", base, base.Add(2*time.Hour))
	resID := fixtureResource(t, db, "PS-B", "Runway B")
	_, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: item.Id, ResourceId: resID, StartAt: base, EndAt: base.Add(time.Hour), Quantity: 1, Status: "PENDING", Version: 1})
	if err != ErrCrossPortfolio {
		t.Fatalf("err = %v, want ErrCrossPortfolio", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM resource_reservations WHERE schedule_item_id=$1`, item.Id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cross-portfolio reservation inserted %d rows", count)
	}
}

func TestDependencyCycleDetection(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	fixtureScope(t, db, "PS-4")
	base := time.Now().UTC().Truncate(time.Hour)
	a := fixtureItem(t, db, "PS-4", base, base.Add(time.Hour))
	b := fixtureItem(t, db, "PS-4", base.Add(time.Hour), base.Add(2*time.Hour))
	if err := CreateDependency(ctx, db, contract.ScheduleDependency{PredecessorItemId: a.Id, SuccessorItemId: b.Id, DependencyType: "FS", LagMinutes: 0}); err != nil {
		t.Fatal(err)
	}
	// A -> B -> A is a cycle and must be rejected with no row persisted.
	err := CreateDependency(ctx, db, contract.ScheduleDependency{PredecessorItemId: b.Id, SuccessorItemId: a.Id, DependencyType: "FS", LagMinutes: 0})
	if err != ErrDependencyCycle {
		t.Fatalf("err = %v, want ErrDependencyCycle", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM schedule_dependencies WHERE predecessor_item_id=$1 AND successor_item_id=$2`, b.Id, a.Id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cycle dependency persisted %d rows", count)
	}
}

func TestDependencyCycleThreeNode(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	fixtureScope(t, db, "PS-5")
	base := time.Now().UTC().Truncate(time.Hour)
	a := fixtureItem(t, db, "PS-5", base, base.Add(time.Hour))
	b := fixtureItem(t, db, "PS-5", base.Add(time.Hour), base.Add(2*time.Hour))
	c := fixtureItem(t, db, "PS-5", base.Add(2*time.Hour), base.Add(3*time.Hour))
	for _, d := range []contract.ScheduleDependency{
		{PredecessorItemId: a.Id, SuccessorItemId: b.Id, DependencyType: "FS"},
		{PredecessorItemId: b.Id, SuccessorItemId: c.Id, DependencyType: "FS"},
	} {
		if err := CreateDependency(ctx, db, d); err != nil {
			t.Fatal(err)
		}
	}
	if err := CreateDependency(ctx, db, contract.ScheduleDependency{PredecessorItemId: c.Id, SuccessorItemId: a.Id, DependencyType: "FS"}); err != ErrDependencyCycle {
		t.Fatalf("err = %v, want ErrDependencyCycle", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM schedule_dependencies WHERE predecessor_item_id=$1 AND successor_item_id=$2`, c.Id, a.Id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("three-node cycle dependency persisted %d rows", count)
	}
}

func TestUnsupportedDependencyTypeRejected(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	fixtureScope(t, db, "PS-6")
	base := time.Now().UTC().Truncate(time.Hour)
	a := fixtureItem(t, db, "PS-6", base, base.Add(time.Hour))
	b := fixtureItem(t, db, "PS-6", base.Add(time.Hour), base.Add(2*time.Hour))
	for _, typ := range []string{"SS", "FF", "SF"} {
		err := CreateDependency(ctx, db, contract.ScheduleDependency{PredecessorItemId: a.Id, SuccessorItemId: b.Id, DependencyType: typ})
		if err == nil {
			t.Fatalf("dependency type %s accepted, want rejection", typ)
		}
	}
}

// reconcileConflictSet runs DetectConflicts + ReconcileConflicts in one committed
// transaction and returns the resulting OPEN conflict rows for the item.
func reconcileConflictSet(t *testing.T, db *sql.DB, itemID string) int {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	conflicts, err := DetectConflicts(context.Background(), tx, itemID)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := ReconcileConflicts(context.Background(), tx, itemID, conflicts); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var open int
	if err := db.QueryRow(`SELECT count(*) FROM schedule_conflicts WHERE left_item_id=$1 AND status='OPEN'`, itemID).Scan(&open); err != nil {
		t.Fatal(err)
	}
	return open
}

func TestConflictReconcileIdempotentAndReschedule(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	fixtureScope(t, db, "PS-7")
	base := time.Now().UTC().Truncate(time.Hour)
	a := fixtureItem(t, db, "PS-7", base, base.Add(3*time.Hour))
	b := fixtureItem(t, db, "PS-7", base.Add(time.Hour), base.Add(3*time.Hour))
	resID := fixtureResource(t, db, "PS-7", "Runway 7")
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: a.Id, ResourceId: resID, StartAt: base, EndAt: base.Add(3 * time.Hour), Quantity: 1, Status: "PENDING", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateReservation(ctx, db, contract.ResourceReservation{ScheduleItemId: b.Id, ResourceId: resID, StartAt: base.Add(time.Hour), EndAt: base.Add(3 * time.Hour), Quantity: 1, Status: "PENDING", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE resource_reservations SET status='ACTIVE' WHERE schedule_item_id=$1`, a.Id); err != nil {
		t.Fatal(err)
	}
	// First detection: one OPEN conflict.
	if open := reconcileConflictSet(t, db, b.Id); open != 1 {
		t.Fatalf("first reconcile open=%d, want 1", open)
	}
	// Re-detecting the same conflict keeps exactly one row (fingerprint dedup).
	if open := reconcileConflictSet(t, db, b.Id); open != 1 {
		t.Fatalf("second reconcile open=%d, want 1", open)
	}
	// Move item B's reservation to a new window: old fingerprint RESOLVED, new OPEN.
	if _, err := db.Exec(`UPDATE resource_reservations SET start_at=$1,end_at=$2 WHERE schedule_item_id=$3`, base.Add(-time.Hour), base.Add(30*time.Minute), b.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE schedule_items SET start_at=$1,end_at=$2 WHERE id=$3`, base.Add(-time.Hour), base.Add(30*time.Minute), b.Id); err != nil {
		t.Fatal(err)
	}
	_ = reconcileConflictSet(t, db, b.Id)
	var open, resolved int
	if err := db.QueryRow(`SELECT count(*) FROM schedule_conflicts WHERE left_item_id=$1 AND status='OPEN'`, b.Id).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM schedule_conflicts WHERE left_item_id=$1 AND status='RESOLVED'`, b.Id).Scan(&resolved); err != nil {
		t.Fatal(err)
	}
	if open < 1 || resolved < 1 {
		t.Fatalf("reschedule open=%d resolved=%d; want both >=1", open, resolved)
	}
}

// calendarFixture creates a resource calendar with the given timezone.
func calendarFixture(t *testing.T, db *sql.DB, resourceID, tz string) string {
	t.Helper()
	calID := "CAL-" + resourceID
	if _, err := db.Exec(`INSERT INTO resource_calendars(id,resource_id,timezone) VALUES($1,$2,$3)`, calID, resourceID, tz); err != nil {
		t.Fatal(err)
	}
	return calID
}

func calendarRule(t *testing.T, db *sql.DB, calID string, weekday int, start, end string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO resource_calendar_rules(calendar_id,weekday,start_local,end_local,availability) VALUES($1,$2,$3,$4,'AVAILABLE')`, calID, weekday, start, end); err != nil {
		t.Fatal(err)
	}
}

func calendarException(t *testing.T, db *sql.DB, calID, kind string, start, end time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO resource_calendar_exceptions(calendar_id,start_at,end_at,kind) VALUES($1,$2,$3,$4)`, calID, start, end, kind); err != nil {
		t.Fatal(err)
	}
}

func evalCalendar(t *testing.T, db *sql.DB, resourceID string, start, end time.Time) (bool, string) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ok, reason, err := EvaluateCalendarAvailability(context.Background(), tx, resourceID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	return ok, reason
}

// TestCalendarFullIntervalEngine proves the whole [start,end) must be covered by
// the union of rule + exceptions; a partial overlap is a block.
func TestCalendarFullIntervalEngine(t *testing.T) {
	db := testsupport.Database(t)
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("Asia/Shanghai tz must be loadable (embedded tzdata): %v", err)
	}
	fixtureScope(t, db, "PS-8")
	resID := fixtureResource(t, db, "PS-8", "Runway 8")
	calID := calendarFixture(t, db, resID, "Asia/Shanghai")
	// Rule 08:00-17:00 on the weekday of the chosen date.
	day := time.Date(2026, 1, 15, 0, 0, 0, 0, shanghai)
	calendarRule(t, db, calID, int(day.Weekday()), "08:00:00", "17:00:00")

	local := func(h, m int) time.Time { return time.Date(2026, 1, 15, h, m, 0, 0, shanghai) }

	// Fully inside the rule -> PASS.
	if ok, reason := evalCalendar(t, db, resID, local(9, 0), local(16, 0)); !ok {
		t.Fatalf("09:00-16:00 inside rule blocked: %s", reason)
	}
	// Ends after the rule -> BLOCK.
	if ok, _ := evalCalendar(t, db, resID, local(9, 0), local(18, 0)); ok {
		t.Fatal("09:00-18:00 outside rule passed")
	}
	// Starts before the rule -> BLOCK.
	if ok, _ := evalCalendar(t, db, resID, local(7, 0), local(10, 0)); ok {
		t.Fatal("07:00-10:00 outside rule passed")
	}

	// AVAILABLE exception 18:00-20:00 extends availability.
	calendarException(t, db, calID, "AVAILABLE", local(18, 0), local(20, 0))
	if ok, reason := evalCalendar(t, db, resID, local(18, 30), local(19, 30)); !ok {
		t.Fatalf("18:30-19:30 with AVAILABLE exception blocked: %s", reason)
	}

	// HOLIDAY 12:00-14:00 carves a hole.
	calendarException(t, db, calID, "HOLIDAY", local(12, 0), local(14, 0))
	if ok, _ := evalCalendar(t, db, resID, local(13, 0), local(13, 30)); ok {
		t.Fatal("13:00-13:30 inside HOLIDAY passed")
	}

	// Partial SITE_CLOSED overlap 16:00-18:00 blocks a 16:30-17:30 window.
	calendarException(t, db, calID, "SITE_CLOSED", local(16, 0), local(18, 0))
	if ok, _ := evalCalendar(t, db, resID, local(16, 30), local(17, 30)); ok {
		t.Fatal("16:30-17:30 with partial SITE_CLOSED overlap passed")
	}
}

// TestCalendarTimezoneDST proves UTC storage + local evaluation in a DST
// timezone and in Asia/Shanghai (never a fixed +8).
func TestCalendarTimezoneDST(t *testing.T) {
	db := testsupport.Database(t)
	// Europe/Berlin must also be loadable from the embedded tzdata (Alpine has
	// no host zoneinfo).
	if _, err := time.LoadLocation("Europe/Berlin"); err != nil {
		t.Fatalf("Europe/Berlin tz must be loadable (embedded tzdata): %v", err)
	}
	fixtureScope(t, db, "PS-9")
	resID := fixtureResource(t, db, "PS-9", "Runway 9")

	// America/Los_Angeles (DST-observing).
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("America/Los_Angeles tz must be loadable (embedded tzdata): %v", err)
	}
	calID := calendarFixture(t, db, resID, "America/Los_Angeles")
	summer := time.Date(2026, 7, 15, 0, 0, 0, 0, la)
	calendarRule(t, db, calID, int(summer.Weekday()), "08:00:00", "17:00:00")
	laLocal := func(h, m int) time.Time { return time.Date(2026, 7, 15, h, m, 0, 0, la) }
	// Local 09:00-16:00 must be available even though the UTC wall-clock differs
	// from a naive fixed-offset interpretation.
	if ok, reason := evalCalendar(t, db, resID, laLocal(9, 0), laLocal(16, 0)); !ok {
		t.Fatalf("LA local 09:00-16:00 blocked: %s", reason)
	}
	if ok, _ := evalCalendar(t, db, resID, laLocal(7, 0), laLocal(9, 0)); ok {
		t.Fatal("LA local 07:00-09:00 outside rule passed")
	}

	// Asia/Shanghai (no DST) — a separate resource/calendar.
	resSH := fixtureResource(t, db, "PS-9", "Runway SH")
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("Asia/Shanghai tz must be loadable (embedded tzdata): %v", err)
	}
	calSH := calendarFixture(t, db, resSH, "Asia/Shanghai")
	day := time.Date(2026, 1, 15, 0, 0, 0, 0, sh)
	calendarRule(t, db, calSH, int(day.Weekday()), "09:00:00", "18:00:00")
	shLocal := func(h, m int) time.Time { return time.Date(2026, 1, 15, h, m, 0, 0, sh) }
	if ok, reason := evalCalendar(t, db, resSH, shLocal(10, 0), shLocal(17, 0)); !ok {
		t.Fatalf("Shanghai local 10:00-17:00 blocked: %s", reason)
	}
	if ok, _ := evalCalendar(t, db, resSH, shLocal(8, 0), shLocal(10, 0)); ok {
		t.Fatal("Shanghai local 08:00-10:00 outside rule passed")
	}
}
