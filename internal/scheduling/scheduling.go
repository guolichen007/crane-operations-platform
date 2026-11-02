// Package scheduling owns the deterministic scheduling kernel: resources,
// calendars, reservations, dependencies, conflict detection, approval and
// baseline. Time ranges are half-open [start_at, end_at). This is not a CPM
// optimizer and not an auto-leveler; it detects and explains, never silently
// moves schedules.
package scheduling

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

var (
	ErrHardConflict   = errors.New("schedule has an unresolved hard conflict")
	ErrHardOverride   = errors.New("hard exclusive conflict cannot be overridden")
	ErrShortReason    = errors.New("override reason must be at least 8 characters")
)

// Conflict is a detected scheduling conflict before persistence.
type Conflict struct {
	Type     string
	Severity string
	Left     string
	Right    string
	Resource string
	Detail   string
}

func (c Conflict) Fingerprint() string {
	return fmt.Sprintf("%s|%s|%s|%s", c.Type, c.Left, c.Right, c.Resource)
}

// Overlaps reports whether two half-open [start,end) ranges overlap. Adjacent
// ranges (10:00-11:00 and 11:00-12:00) do not overlap.
func Overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && aEnd.After(bStart)
}

type quantityWindow struct {
	start time.Time
	end   time.Time
	qty   int
}

// capacityPeak computes the concurrent-quantity peak over a set of overlapping
// reservation windows plus one candidate. At the same timestamp, end events are
// processed before start events (half-open semantics).
func capacityPeak(existing []quantityWindow, start, end time.Time, qty int) int {
	type event struct {
		at    time.Time
		delta int
	}
	events := make([]event, 0, 2*len(existing)+2)
	for _, w := range existing {
		events = append(events, event{w.start, w.qty}, event{w.end, -w.qty})
	}
	events = append(events, event{start, qty}, event{end, -qty})
	sort.Slice(events, func(i, j int) bool {
		if events[i].at.Equal(events[j].at) {
			return events[i].delta < events[j].delta
		}
		return events[i].at.Before(events[j].at)
	})
	peak, cur := 0, 0
	for _, e := range events {
		cur += e.delta
		if cur > peak {
			peak = cur
		}
	}
	return peak
}

// CreateResource inserts a resource. exclusive resources are forced to capacity
// 1 by the DB CHECK.
func CreateResource(ctx context.Context, db *sql.DB, in contract.Resource) (contract.Resource, error) {
	if in.Id == "" {
		in.Id = platform.ID()
	}
	if in.Capacity <= 0 {
		return in, errors.New("resource capacity must be positive")
	}
	_, err := db.ExecContext(ctx, `INSERT INTO resources(id,portfolio_scope_id,resource_type,reference_id,name,capacity,exclusive,override_allowed,home_timezone,status,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		in.Id, in.PortfolioScopeId, in.ResourceType, in.ReferenceId, in.Name, in.Capacity, in.Exclusive, in.OverrideAllowed, in.HomeTimezone, in.Status, in.Version)
	return in, err
}

// CreateScheduleItem inserts a schedule item with half-open range validation.
func CreateScheduleItem(ctx context.Context, db *sql.DB, in contract.ScheduleItem) (contract.ScheduleItem, error) {
	if in.Id == "" {
		in.Id = platform.ID()
	}
	if !in.StartAt.Before(in.EndAt) {
		return in, errors.New("schedule item start_at must be before end_at")
	}
	_, err := db.ExecContext(ctx, `INSERT INTO schedule_items(id,project_id,source_type,source_id,name,start_at,end_at,timezone,status,priority,exclusive_location_id,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		in.Id, in.ProjectId, in.SourceType, in.SourceId, in.Name, in.StartAt, in.EndAt, in.Timezone, in.Status, in.Priority, in.ExclusiveLocationId, in.Version)
	return in, err
}

// CreateReservation inserts a reservation. The reservation range must lie
// within its schedule item range.
func CreateReservation(ctx context.Context, db *sql.DB, in contract.ResourceReservation) (contract.ResourceReservation, error) {
	if in.Id == "" {
		in.Id = platform.ID()
	}
	if !in.StartAt.Before(in.EndAt) {
		return in, errors.New("reservation start_at must be before end_at")
	}
	var itemStart, itemEnd time.Time
	if err := db.QueryRowContext(ctx, `SELECT start_at,end_at FROM schedule_items WHERE id=$1`, in.ScheduleItemId).Scan(&itemStart, &itemEnd); err != nil {
		return in, err
	}
	if in.StartAt.Before(itemStart) || in.EndAt.After(itemEnd) {
		return in, errors.New("reservation range is outside its schedule item range")
	}
	_, err := db.ExecContext(ctx, `INSERT INTO resource_reservations(id,schedule_item_id,resource_id,start_at,end_at,quantity,status,created_by,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		in.Id, in.ScheduleItemId, in.ResourceId, in.StartAt, in.EndAt, in.Quantity, in.Status, "", in.Version)
	return in, err
}

// DetectConflicts evaluates a schedule item's reservations against existing
// reservations and dependencies. Callers hold the required resource locks.
func DetectConflicts(ctx context.Context, tx *sql.Tx, itemID string) ([]Conflict, error) {
	var itemStart, itemEnd time.Time
	if err := tx.QueryRowContext(ctx, `SELECT start_at,end_at FROM schedule_items WHERE id=$1`, itemID).Scan(&itemStart, &itemEnd); err != nil {
		return nil, err
	}
	var conflicts []Conflict

	// Dependencies (FS): successor.start >= predecessor.end + lag_minutes.
	drows, err := tx.QueryContext(ctx, `SELECT d.predecessor_item_id,d.lag_minutes,p.end_at FROM schedule_dependencies d JOIN schedule_items p ON p.id=d.predecessor_item_id WHERE d.successor_item_id=$1`, itemID)
	if err != nil {
		return nil, err
	}
	type dep struct {
		pred string
		end  time.Time
		lag  int
	}
	var deps []dep
	for drows.Next() {
		var d dep
		if err := drows.Scan(&d.pred, &d.lag, &d.end); err != nil {
			drows.Close()
			return nil, err
		}
		deps = append(deps, d)
	}
	drows.Close()
	for _, d := range deps {
		required := d.end.Add(time.Duration(d.lag) * time.Minute)
		if itemStart.Before(required) {
			conflicts = append(conflicts, Conflict{Type: "DEPENDENCY_VIOLATION", Severity: "BLOCKING", Left: d.pred, Right: itemID, Detail: "successor starts before predecessor end + lag"})
		}
	}

	// Candidate reservations for this item, grouped by resource.
	crows, err := tx.QueryContext(ctx, `SELECT resource_id,start_at,end_at,quantity FROM resource_reservations WHERE schedule_item_id=$1 AND status='ACTIVE'`, itemID)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	candidates := map[string][]quantityWindow{}
	for crows.Next() {
		var rid string
		var cs, ce time.Time
		var cqty int
		if err := crows.Scan(&rid, &cs, &ce, &cqty); err != nil {
			return nil, err
		}
		candidates[rid] = append(candidates[rid], quantityWindow{cs, ce, cqty})
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}

	for rid, cands := range candidates {
		var exclusive bool
		var capacity int
		if err := tx.QueryRowContext(ctx, `SELECT exclusive,capacity FROM resources WHERE id=$1`, rid).Scan(&exclusive, &capacity); err != nil {
			return nil, err
		}
		erows, err := tx.QueryContext(ctx, `SELECT start_at,end_at,quantity FROM resource_reservations WHERE resource_id=$1 AND status='ACTIVE' AND schedule_item_id<>$2`, rid, itemID)
		if err != nil {
			return nil, err
		}
		var existing []quantityWindow
		for erows.Next() {
			var es, ee time.Time
			var eq int
			if err := erows.Scan(&es, &ee, &eq); err != nil {
				erows.Close()
				return nil, err
			}
			existing = append(existing, quantityWindow{es, ee, eq})
		}
		erows.Close()
		for _, cand := range cands {
			if exclusive {
				for _, w := range existing {
					if Overlaps(cand.start, cand.end, w.start, w.end) {
						conflicts = append(conflicts, Conflict{Type: "RESOURCE_OVERLAP", Severity: "BLOCKING", Left: itemID, Resource: rid, Detail: "exclusive resource overlap"})
						break
					}
				}
			} else {
				peak := capacityPeak(existing, cand.start, cand.end, cand.qty)
				if peak > capacity {
					conflicts = append(conflicts, Conflict{Type: "RESOURCE_OVER_CAPACITY", Severity: "BLOCKING", Left: itemID, Resource: rid, Detail: fmt.Sprintf("capacity peak %d > capacity %d", peak, capacity)})
				}
			}
		}
	}
	return conflicts, nil
}

// lockResources acquires deterministic advisory xact locks for a set of
// resource ids (sorted to avoid deadlock).
func lockResources(ctx context.Context, tx *sql.Tx, resourceIDs []string) error {
	sorted := append([]string(nil), resourceIDs...)
	sort.Strings(sorted)
	for _, rid := range sorted {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, rid); err != nil {
			return err
		}
	}
	return nil
}

// resourceIDsForItems loads the resource ids referenced by the items'
// reservations.
func resourceIDsForItems(ctx context.Context, tx *sql.Tx, itemIDs []string) ([]string, error) {
	seen := map[string]bool{}
	var ids []string
	for _, id := range itemIDs {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT resource_id FROM resource_reservations WHERE schedule_item_id=$1 AND status='ACTIVE'`, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var rid string
			if err := rows.Scan(&rid); err != nil {
				rows.Close()
				return nil, err
			}
			if !seen[rid] {
				seen[rid] = true
				ids = append(ids, rid)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// ApproveSchedule re-checks conflicts inside a transaction. It locks involved
// resources deterministically, recomputes conflicts, refuses to commit on an
// unresolved hard conflict, and only then marks items APPROVED.
func ApproveSchedule(ctx context.Context, db *sql.DB, itemIDs []string, actor, requestID string) ([]Conflict, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	resourceIDs, err := resourceIDsForItems(ctx, tx, itemIDs)
	if err != nil {
		return nil, err
	}
	if err := lockResources(ctx, tx, resourceIDs); err != nil {
		return nil, err
	}
	var hard []Conflict
	for _, id := range itemIDs {
		conflicts, err := DetectConflicts(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		for _, c := range conflicts {
			if c.Severity == "BLOCKING" {
				hard = append(hard, c)
			}
		}
	}
	if len(hard) > 0 {
		return hard, ErrHardConflict
	}
	for _, id := range itemIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE schedule_items SET status='APPROVED',version=version+1,updated_at=now() WHERE id=$1`, id); err != nil {
			return nil, err
		}
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Action: "schedule_approved", Result: "success", RequestID: requestID}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return nil, nil
}

// CreateBaseline writes an immutable snapshot; adjustments create a new
// baseline revision rather than updating the old one.
func CreateBaseline(ctx context.Context, db *sql.DB, in contract.ScheduleBaseline, snapshot []byte) (contract.ScheduleBaseline, error) {
	if in.Id == "" {
		in.Id = platform.ID()
	}
	_, err := db.ExecContext(ctx, `INSERT INTO schedule_baselines(id,portfolio_scope_id,project_id,name,baseline_version,approved_by,approved_at,snapshot_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		in.Id, in.PortfolioScopeId, in.ProjectId, in.Name, in.BaselineVersion, in.ApprovedBy, in.ApprovedAt, snapshot)
	return in, err
}

// OverrideConflict records a schedule conflict override. A hard exclusive
// conflict (override_allowed=false) can never be overridden. The caller has
// already checked the schedule_override capability.
func OverrideConflict(ctx context.Context, db *sql.DB, conflictID int64, reason, actor, requestID string) error {
	if len(reason) < 8 {
		return ErrShortReason
	}
	var resourceID sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT resource_id FROM schedule_conflicts WHERE id=$1`, conflictID).Scan(&resourceID); err != nil {
		return err
	}
	if resourceID.Valid {
		var overrideAllowed bool
		if err := db.QueryRowContext(ctx, `SELECT override_allowed FROM resources WHERE id=$1`, resourceID.String).Scan(&overrideAllowed); err != nil {
			return err
		}
		if !overrideAllowed {
			return ErrHardOverride
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE schedule_conflicts SET status='OVERRIDDEN',resolved_at=now(),updated_at=now() WHERE id=$1 AND status='OPEN'`, conflictID); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schedule_conflict_overrides(conflict_id,reason,overridden_by,overridden_at,request_id) VALUES($1,$2,$3,now(),$4)`, conflictID, reason, actor, requestID); err != nil {
		return err
	}
	return auditlog.Write(ctx, db, auditlog.Event{Actor: actor, Action: "conflict_override", Result: "success", RequestID: requestID})
}
