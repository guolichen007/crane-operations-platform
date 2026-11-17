// Package scheduling owns the deterministic scheduling kernel: resources,
// calendars, reservations, dependencies, conflict detection, reconciliation,
// approval and baseline. Time ranges are half-open [start_at, end_at). This is
// not a CPM optimizer and not an auto-leveler; it detects and explains, never
// silently moves schedules.
package scheduling

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

var (
	ErrHardConflict      = errors.New("schedule has an unresolved hard conflict")
	ErrHardOverride      = errors.New("hard exclusive conflict cannot be overridden")
	ErrShortReason       = errors.New("override reason must be at least 8 characters")
	ErrInvalidTransition = errors.New("invalid schedule item transition")
	ErrVersionConflict   = errors.New("schedule item version conflict")
	ErrDependencyCycle   = errors.New("schedule dependency cycle detected")
	ErrInvalidTimezone   = errors.New("invalid IANA timezone")
	ErrCrossPortfolio    = errors.New("schedule item cannot reserve a resource of another portfolio")
	ErrScopeMismatch     = errors.New("schedule scope mismatch")
	ErrProjectNotManaged = errors.New("project is not portfolio-managed")
	// ErrInvalid is a client input error (HTTP 422), never a database outage.
	ErrInvalid  = errors.New("invalid scheduling input")
	ErrNotFound = errors.New("scheduling object not found")
)

// ResolveScheduleScope derives the authoritative portfolio scope for a schedule
// item. A project-linked item resolves from the project's portfolio scope; a
// pure portfolio item requires an explicit scope. The client-supplied scope is
// only a consistency check, never the authority.
func ResolveScheduleScope(ctx context.Context, db *sql.DB, projectID, clientScope *string) (string, error) {
	if projectID != nil && *projectID != "" {
		var scope sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT portfolio_scope_id FROM projects WHERE id=$1`, *projectID).Scan(&scope); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", errors.New("project not found")
			}
			return "", err
		}
		if !scope.Valid {
			return "", ErrProjectNotManaged
		}
		if clientScope != nil && *clientScope != "" && *clientScope != scope.String {
			return "", ErrScopeMismatch
		}
		return scope.String, nil
	}
	if clientScope == nil || *clientScope == "" {
		return "", errors.New("portfolio schedule requires an explicit portfolio scope")
	}
	return *clientScope, nil
}

// Conflict is a detected scheduling conflict before persistence. Hard marks a
// conflict that can never be overridden (e.g. exclusive resource with
// override_allowed=false).
type Conflict struct {
	Type     string    `json:"conflictType"`
	Severity string    `json:"severity"`
	Left     string    `json:"leftItemId"`
	Right    string    `json:"rightItemId,omitempty"`
	Resource string    `json:"resourceId,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	Start    time.Time `json:"startAt"`
	End      time.Time `json:"endAt"`
	Hard     bool      `json:"hard"`
}

// Fingerprint returns a stable identity including the conflict interval, so a
// re-scheduled conflict in a later window is a distinct conflict.
func (c Conflict) Fingerprint() string {
	canonical := fmt.Sprintf("%s|%s|%s|%s|%s|%s", c.Type, c.Left, c.Right, c.Resource, c.Start.UTC().Format(time.RFC3339), c.End.UTC().Format(time.RFC3339))
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
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
	in.Id = platform.ID()
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 200 {
		return in, fmt.Errorf("%w: name is required (max 200)", ErrInvalid)
	}
	if !resourceTypes[in.ResourceType] {
		return in, fmt.Errorf("%w: unknown resourceType %q", ErrInvalid, in.ResourceType)
	}
	if in.Capacity <= 0 || in.Capacity > 10000 {
		return in, fmt.Errorf("%w: capacity must be 1..10000", ErrInvalid)
	}
	if in.Exclusive && in.Capacity != 1 {
		return in, fmt.Errorf("%w: an exclusive resource has capacity 1", ErrInvalid)
	}
	if in.HomeTimezone == "" {
		in.HomeTimezone = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(in.HomeTimezone); err != nil {
		return in, fmt.Errorf("%w: invalid timezone", ErrInvalid)
	}
	in.Status, in.Version = "ACTIVE", 1
	_, err := db.ExecContext(ctx, `INSERT INTO resources(id,portfolio_scope_id,resource_type,reference_id,name,capacity,exclusive,override_allowed,home_timezone,status,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		in.Id, in.PortfolioScopeId, in.ResourceType, in.ReferenceId, in.Name, in.Capacity, in.Exclusive, in.OverrideAllowed, in.HomeTimezone, in.Status, in.Version)
	return in, err
}

// CreateScheduleItem inserts a schedule item with half-open range validation.
// The portfolio scope is resolved authoritatively, never trusted from the
// client body.
func CreateScheduleItem(ctx context.Context, db *sql.DB, in contract.ScheduleItem) (contract.ScheduleItem, error) {
	in.Id = platform.ID()
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 200 {
		return in, fmt.Errorf("%w: name is required (max 200)", ErrInvalid)
	}
	if !sourceTypes[in.SourceType] {
		return in, fmt.Errorf("%w: unknown sourceType %q", ErrInvalid, in.SourceType)
	}
	if in.Priority == "" {
		in.Priority = "MEDIUM"
	}
	if !priorities[in.Priority] {
		return in, fmt.Errorf("%w: unknown priority %q", ErrInvalid, in.Priority)
	}
	if in.Timezone == "" {
		in.Timezone = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return in, fmt.Errorf("%w: invalid timezone", ErrInvalid)
	}
	if in.StartAt.IsZero() || in.EndAt.IsZero() || !in.StartAt.Before(in.EndAt) {
		return in, fmt.Errorf("%w: startAt must be before endAt", ErrInvalid)
	}
	if in.EndAt.Sub(in.StartAt) > 366*24*time.Hour {
		return in, fmt.Errorf("%w: a schedule item spans at most one year", ErrInvalid)
	}
	if strings.TrimSpace(in.SourceId) == "" {
		in.SourceId = in.Id
	}
	in.Status, in.Version = "PLANNED", 1
	scope, err := ResolveScheduleScope(ctx, db, in.ProjectId, in.PortfolioScopeId)
	if err != nil {
		return in, err
	}
	in.PortfolioScopeId = &scope
	_, err = db.ExecContext(ctx, `INSERT INTO schedule_items(id,project_id,portfolio_scope_id,source_type,source_id,name,start_at,end_at,timezone,status,priority,exclusive_location_id,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		in.Id, in.ProjectId, in.PortfolioScopeId, in.SourceType, in.SourceId, in.Name, in.StartAt, in.EndAt, in.Timezone, in.Status, in.Priority, in.ExclusiveLocationId, in.Version)
	return in, err
}

// CreateReservation inserts a PENDING reservation. The reservation range must
// lie within its schedule item range, and its portfolio scope must equal both
// the item's and the resource's scope (DB composite FKs enforce this too).
func CreateReservation(ctx context.Context, db *sql.DB, in contract.ResourceReservation) (contract.ResourceReservation, error) {
	return CreateReservationBy(ctx, db, in, "")
}

// CreateReservationBy is CreateReservation with the acting user recorded. Only a PLANNED item can
// take reservations (an approved schedule is history; change it by cancelling and re-planning).
func CreateReservationBy(ctx context.Context, db *sql.DB, in contract.ResourceReservation, actor string) (contract.ResourceReservation, error) {
	in.Id = platform.ID()
	if in.StartAt.IsZero() || in.EndAt.IsZero() || !in.StartAt.Before(in.EndAt) {
		return in, fmt.Errorf("%w: reservation startAt must be before endAt", ErrInvalid)
	}
	if in.Quantity <= 0 {
		return in, fmt.Errorf("%w: quantity must be positive", ErrInvalid)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	var itemStart, itemEnd time.Time
	var itemScope sql.NullString
	var itemStatus string
	if err := tx.QueryRowContext(ctx, `SELECT start_at,end_at,portfolio_scope_id,status FROM schedule_items WHERE id=$1 FOR UPDATE`, in.ScheduleItemId).Scan(&itemStart, &itemEnd, &itemScope, &itemStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return in, ErrNotFound
		}
		return in, err
	}
	if itemStatus != "PLANNED" {
		return in, fmt.Errorf("%w: only a PLANNED schedule item takes reservations (status %s)", ErrInvalidTransition, itemStatus)
	}
	if in.StartAt.Before(itemStart) || in.EndAt.After(itemEnd) {
		return in, fmt.Errorf("%w: reservation range is outside its schedule item range", ErrInvalid)
	}
	var resScope, resStatus string
	var capacity int
	if err := tx.QueryRowContext(ctx, `SELECT portfolio_scope_id,status,capacity FROM resources WHERE id=$1`, in.ResourceId).Scan(&resScope, &resStatus, &capacity); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return in, fmt.Errorf("%w: resource not found", ErrInvalid)
		}
		return in, err
	}
	if itemScope.Valid && itemScope.String != resScope {
		return in, ErrCrossPortfolio
	}
	if resStatus != "ACTIVE" {
		return in, fmt.Errorf("%w: resource is disabled", ErrInvalid)
	}
	if in.Quantity > capacity {
		return in, fmt.Errorf("%w: quantity %d exceeds resource capacity %d", ErrInvalid, in.Quantity, capacity)
	}
	in.Status, in.Version = "PENDING", 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO resource_reservations(id,schedule_item_id,resource_id,portfolio_scope_id,start_at,end_at,quantity,status,created_by,version) VALUES($1,$2,$3,$4,$5,$6,$7,'PENDING',$8,1)`,
		in.Id, in.ScheduleItemId, in.ResourceId, resScope, in.StartAt, in.EndAt, in.Quantity, actor); err != nil {
		return in, err
	}
	return in, tx.Commit()
}

// ListReservations returns the reservations of one schedule item (all statuses, oldest first).
func ListReservations(ctx context.Context, db *sql.DB, itemID string) ([]contract.ResourceReservation, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,schedule_item_id,resource_id,start_at,end_at,quantity,status,version FROM resource_reservations WHERE schedule_item_id=$1 ORDER BY created_at,id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []contract.ResourceReservation{}
	for rows.Next() {
		var v contract.ResourceReservation
		if err := rows.Scan(&v.Id, &v.ScheduleItemId, &v.ResourceId, &v.StartAt, &v.EndAt, &v.Quantity, &v.Status, &v.Version); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CancelReservation cancels a PENDING reservation of a PLANNED item (If-Match version). The row is
// kept as CANCELLED; nothing is deleted.
func CancelReservation(ctx context.Context, db *sql.DB, reservationID string, expectedVersion int64, actor, requestID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var itemID, status string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT schedule_item_id,status,version FROM resource_reservations WHERE id=$1`, reservationID).Scan(&itemID, &status, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var itemStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM schedule_items WHERE id=$1 FOR UPDATE`, itemID).Scan(&itemStatus); err != nil {
		return err
	}
	if itemStatus != "PLANNED" || status != "PENDING" {
		return fmt.Errorf("%w: only a PENDING reservation of a PLANNED item can be cancelled here", ErrInvalidTransition)
	}
	res, err := tx.ExecContext(ctx, `UPDATE resource_reservations SET status='CANCELLED',version=version+1,updated_at=now() WHERE id=$1 AND version=$2 AND status='PENDING'`, reservationID, expectedVersion)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrVersionConflict
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Action: "reservation_cancelled", Result: "success", RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}

// CancelScheduleItem moves a PLANNED or APPROVED item to CANCELLED (If-Match version), cancels its
// reservations (releasing approved capacity) and resolves its open conflicts. The approved history
// stays: the item row is kept with status CANCELLED and the approval baselines are immutable.
func CancelScheduleItem(ctx context.Context, db *sql.DB, itemID string, expectedVersion int64, reason, actor, requestID string) error {
	if len([]rune(strings.TrimSpace(reason))) < 4 {
		return fmt.Errorf("%w: a cancellation reason is required", ErrInvalid)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM schedule_items WHERE id=$1 FOR UPDATE`, itemID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if status != "PLANNED" && status != "APPROVED" {
		return fmt.Errorf("%w: %s cannot be cancelled", ErrInvalidTransition, status)
	}
	res, err := tx.ExecContext(ctx, `UPDATE schedule_items SET status='CANCELLED',version=version+1,updated_at=now() WHERE id=$1 AND version=$2`, itemID, expectedVersion)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrVersionConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE resource_reservations SET status='CANCELLED',version=version+1,updated_at=now() WHERE schedule_item_id=$1 AND status IN ('PENDING','ACTIVE')`, itemID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE schedule_conflicts SET status='RESOLVED',resolved_at=now(),updated_at=now() WHERE (left_item_id=$1 OR right_item_id=$1) AND status='OPEN'`, itemID); err != nil {
		return err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Action: "schedule_cancelled", Result: "success", RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}

var (
	resourceTypes = map[string]bool{"PERSON": true, "TEAM": true, "SITE": true, "WORKSHOP": true, "BAY": true, "RUNWAY": true, "ASSET": true, "TEST_EQUIPMENT": true, "TOOL": true, "VEHICLE": true, "COMMISSIONING_SLOT": true, "NETWORK_CUTOVER_SLOT": true, "OTHER": true}
	sourceTypes   = map[string]bool{"PROJECT_ACTIVITY": true, "WORK_ORDER": true, "INSPECTION": true, "CALIBRATION": true, "COMMISSIONING": true, "SITE_WINDOW": true, "NETWORK_CUTOVER": true}
	priorities    = map[string]bool{"LOW": true, "MEDIUM": true, "HIGH": true, "CRITICAL": true}
)

// timeInterval is a half-open local-time interval within one calendar day.
type timeInterval struct {
	start time.Time
	end   time.Time
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// covered reports whether [start,end) is fully contained in the union of the
// given intervals (merged, non-overlapping).
func covered(intervals []timeInterval, start, end time.Time) bool {
	if len(intervals) == 0 {
		return false
	}
	merged := append([]timeInterval(nil), intervals...)
	sort.Slice(merged, func(i, j int) bool { return merged[i].start.Before(merged[j].start) })
	cursor := start
	for _, iv := range merged {
		if iv.start.After(cursor) {
			return false
		}
		if iv.end.After(cursor) {
			cursor = iv.end
		}
		if !cursor.Before(end) {
			return true
		}
	}
	return !cursor.Before(end)
}

// EvaluateCalendarAvailability checks that the entire [start,end) window is
// covered by the resource calendar's availability in its IANA timezone. An
// invalid timezone is a validation error, never silently UTC.
func EvaluateCalendarAvailability(ctx context.Context, tx *sql.Tx, resourceID string, start, end time.Time) (bool, string, error) {
	var calID sql.NullString
	var tz string
	if err := tx.QueryRowContext(ctx, `SELECT id,timezone FROM resource_calendars WHERE resource_id=$1`, resourceID).Scan(&calID, &tz); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true, "", nil
		}
		return false, "", err
	}
	if !calID.Valid {
		return true, "", nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return false, "", fmt.Errorf("%w: %s", ErrInvalidTimezone, tz)
	}
	localStart := start.In(loc)
	localEnd := end.In(loc)
	for day := time.Date(localStart.Year(), localStart.Month(), localStart.Day(), 0, 0, 0, 0, loc); day.Before(localEnd); day = day.AddDate(0, 0, 1) {
		segStart := later(localStart, day)
		segEnd := earlier(localEnd, day.AddDate(0, 0, 1))
		if !segStart.Before(segEnd) {
			continue
		}
		intervals := []timeInterval{}
		var avail sql.NullString
		var sh, sm, eh, em float64
		err := tx.QueryRowContext(ctx, `SELECT availability,EXTRACT(HOUR FROM start_local),EXTRACT(MINUTE FROM start_local),EXTRACT(HOUR FROM end_local),EXTRACT(MINUTE FROM end_local) FROM resource_calendar_rules WHERE calendar_id=$1 AND weekday=$2 LIMIT 1`, calID.String, int(day.Weekday())).Scan(&avail, &sh, &sm, &eh, &em)
		if errors.Is(err, sql.ErrNoRows) {
			intervals = append(intervals, timeInterval{day, day.AddDate(0, 0, 1)})
		} else if err != nil {
			return false, "", err
		} else if avail.Valid && avail.String == "AVAILABLE" {
			ws := day.Add(time.Duration(sh)*time.Hour + time.Duration(sm)*time.Minute)
			we := day.Add(time.Duration(eh)*time.Hour + time.Duration(em)*time.Minute)
			if we.After(ws) {
				intervals = append(intervals, timeInterval{ws, we})
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT kind,start_at,end_at FROM resource_calendar_exceptions WHERE calendar_id=$1 AND start_at < $2 AND end_at > $3`, calID.String, segEnd, segStart)
		if err != nil {
			return false, "", err
		}
		for rows.Next() {
			var kind string
			var es, ee time.Time
			if err := rows.Scan(&kind, &es, &ee); err != nil {
				rows.Close()
				return false, "", err
			}
			excStart := later(es.In(loc), day)
			excEnd := earlier(ee.In(loc), day.AddDate(0, 0, 1))
			if !excStart.Before(excEnd) {
				continue
			}
			if kind == "AVAILABLE" {
				intervals = append(intervals, timeInterval{excStart, excEnd})
			} else {
				sub := []timeInterval{}
				for _, iv := range intervals {
					if excEnd.Before(iv.start) || excStart.After(iv.end) {
						sub = append(sub, iv)
						continue
					}
					if iv.start.Before(excStart) {
						sub = append(sub, timeInterval{iv.start, excStart})
					}
					if iv.end.After(excEnd) {
						sub = append(sub, timeInterval{excEnd, iv.end})
					}
				}
				intervals = sub
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return false, "", err
		}
		if !covered(intervals, segStart, segEnd) {
			return false, "CALENDAR_UNAVAILABLE", nil
		}
	}
	return true, "", nil
}

// ValidateDependencyAcyclic performs directed-graph cycle detection over the
// schedule_dependencies table reachable from the given successor item.
func ValidateDependencyAcyclic(ctx context.Context, tx *sql.Tx, startItem string) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var dfs func(id string) error
	dfs = func(id string) error {
		color[id] = gray
		rows, err := tx.QueryContext(ctx, `SELECT predecessor_item_id FROM schedule_dependencies WHERE successor_item_id=$1`, id)
		if err != nil {
			return err
		}
		var preds []string
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return err
			}
			preds = append(preds, p)
		}
		rows.Close()
		for _, p := range preds {
			if color[p] == gray {
				return ErrDependencyCycle
			}
			if color[p] == white {
				if err := dfs(p); err != nil {
					return err
				}
			}
		}
		color[id] = black
		return nil
	}
	return dfs(startItem)
}

// DetectConflicts evaluates a schedule item's reservations against existing
// ACTIVE reservations, calendar and dependencies. Callers hold resource locks.
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
			conflicts = append(conflicts, Conflict{Type: "DEPENDENCY_VIOLATION", Severity: "BLOCKING", Left: d.pred, Right: itemID, Start: itemStart, End: required, Detail: "successor starts before predecessor end + lag"})
		}
	}

	// Candidate PENDING reservations for this item, grouped by resource.
	crows, err := tx.QueryContext(ctx, `SELECT resource_id,start_at,end_at,quantity FROM resource_reservations WHERE schedule_item_id=$1 AND status='PENDING'`, itemID)
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
		var exclusive, overrideAllowed bool
		var capacity int
		if err := tx.QueryRowContext(ctx, `SELECT exclusive,override_allowed,capacity FROM resources WHERE id=$1`, rid).Scan(&exclusive, &overrideAllowed, &capacity); err != nil {
			return nil, err
		}
		// Existing ACTIVE reservations of OTHER items only.
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
		if err := erows.Err(); err != nil {
			return nil, err
		}
		for _, cand := range cands {
			if exclusive {
				for _, w := range existing {
					if Overlaps(cand.start, cand.end, w.start, w.end) {
						conflicts = append(conflicts, Conflict{Type: "RESOURCE_OVERLAP", Severity: "BLOCKING", Left: itemID, Resource: rid, Start: cand.start, End: cand.end, Hard: !overrideAllowed})
						break
					}
				}
			} else {
				peak := capacityPeak(existing, cand.start, cand.end, cand.qty)
				if peak > capacity {
					conflicts = append(conflicts, Conflict{Type: "RESOURCE_OVER_CAPACITY", Severity: "BLOCKING", Left: itemID, Resource: rid, Start: cand.start, End: cand.end, Hard: !overrideAllowed, Detail: fmt.Sprintf("capacity peak %d > capacity %d", peak, capacity)})
				}
			}
			ok, reason, err := EvaluateCalendarAvailability(ctx, tx, rid, cand.start, cand.end)
			if err != nil {
				return nil, err
			}
			if !ok {
				conflicts = append(conflicts, Conflict{Type: "CALENDAR_UNAVAILABLE", Severity: "BLOCKING", Left: itemID, Resource: rid, Start: cand.start, End: cand.end, Hard: false, Detail: reason})
			}
		}
	}
	return conflicts, nil
}

// ReconcileConflicts upserts the current conflict set: new conflicts OPEN,
// still-existing conflicts kept, disappeared OPEN conflicts marked RESOLVED.
// History is never deleted.
func ReconcileConflicts(ctx context.Context, tx *sql.Tx, itemID string, current []Conflict) error {
	currentSet := map[string]bool{}
	for _, c := range current {
		fp := c.Fingerprint()
		currentSet[fp] = true
		detail, err := json.Marshal(c)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_conflicts(conflict_type,severity,left_item_id,right_item_id,resource_id,fingerprint,details_json,status) VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7::jsonb,'OPEN') ON CONFLICT(fingerprint) DO UPDATE SET detected_at=now(),details_json=excluded.details_json,updated_at=now()`,
			c.Type, c.Severity, c.Left, c.Right, c.Resource, fp, string(detail)); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,fingerprint FROM schedule_conflicts WHERE left_item_id=$1 AND status='OPEN'`, itemID)
	if err != nil {
		return err
	}
	type openConf struct {
		id int64
		fp string
	}
	var opens []openConf
	for rows.Next() {
		var oc openConf
		if err := rows.Scan(&oc.id, &oc.fp); err != nil {
			rows.Close()
			return err
		}
		opens = append(opens, oc)
	}
	rows.Close()
	for _, oc := range opens {
		if !currentSet[oc.fp] {
			if _, err := tx.ExecContext(ctx, `UPDATE schedule_conflicts SET status='RESOLVED',resolved_at=now(),updated_at=now() WHERE id=$1`, oc.id); err != nil {
				return err
			}
		}
	}
	return nil
}

// recordConflicts re-detects and persists the conflicts of PLANNED items under the same resource
// locks, without changing any item or reservation state.
func recordConflicts(ctx context.Context, db *sql.DB, itemIDs []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids, err := resourceIDsForItems(ctx, tx, itemIDs)
	if err != nil {
		return err
	}
	if err := lockResources(ctx, tx, ids); err != nil {
		return err
	}
	for _, id := range itemIDs {
		conflicts, err := DetectConflicts(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := ReconcileConflicts(ctx, tx, id, conflicts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

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

func resourceIDsForItems(ctx context.Context, tx *sql.Tx, itemIDs []string) ([]string, error) {
	seen := map[string]bool{}
	var ids []string
	for _, id := range itemIDs {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT resource_id FROM resource_reservations WHERE schedule_item_id=$1 AND status='PENDING'`, id)
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
// resources deterministically, recomputes conflicts, reconciles them, refuses
// to commit on an unresolved HARD block or an overridable block without a valid
// override, and only then flips PENDING reservations to ACTIVE and items to
// APPROVED. Items must currently be PLANNED (status + version guard).
func ApproveSchedule(ctx context.Context, db *sql.DB, itemIDs []string, actor, requestID string) ([]Conflict, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Status/version guard.
	type itemState struct {
		status  string
		version int64
	}
	states := map[string]itemState{}
	for _, id := range itemIDs {
		var st string
		var v int64
		if err := tx.QueryRowContext(ctx, `SELECT status,version FROM schedule_items WHERE id=$1 FOR UPDATE`, id).Scan(&st, &v); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		if st != "PLANNED" {
			return nil, ErrInvalidTransition
		}
		states[id] = itemState{st, v}
	}
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
		if err := ReconcileConflicts(ctx, tx, id, conflicts); err != nil {
			return nil, err
		}
		for _, c := range conflicts {
			if c.Hard {
				hard = append(hard, c)
				continue
			}
			// Overridable block: allow only with a valid override on this exact fingerprint.
			var overrideCount int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM schedule_conflicts sc JOIN schedule_conflict_overrides o ON o.conflict_id=sc.id WHERE sc.fingerprint=$1 AND sc.status='OVERRIDDEN'`, c.Fingerprint()).Scan(&overrideCount); err != nil {
				return nil, err
			}
			if overrideCount == 0 && c.Severity == "BLOCKING" {
				hard = append(hard, c)
			}
		}
	}
	if len(hard) > 0 {
		// The approval is refused; the detected conflicts are still recorded (in their own committed
		// transaction) so planners and managers can see, override or re-plan them.
		_ = tx.Rollback()
		if err := recordConflicts(ctx, db, itemIDs); err != nil {
			return hard, err
		}
		return hard, ErrHardConflict
	}
	for _, id := range itemIDs {
		st := states[id]
		result, err := tx.ExecContext(ctx, `UPDATE schedule_items SET status='APPROVED',version=version+1,updated_at=now() WHERE id=$1 AND version=$2 AND status='PLANNED'`, id, st.version)
		if err != nil {
			return nil, err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return nil, ErrVersionConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE resource_reservations SET status='ACTIVE',updated_at=now() WHERE schedule_item_id=$1 AND status='PENDING'`, id); err != nil {
			return nil, err
		}
	}
	if err := approvalBaselines(ctx, tx, itemIDs, actor); err != nil {
		return nil, err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Action: "schedule_approved", Result: "success", RequestID: requestID}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return nil, nil
}

// approvalBaselines writes one immutable baseline per portfolio scope for an approval: the exact
// approved items and their (now ACTIVE) reservations, with a sha256 of the snapshot. Baselines are
// append-only; a later cancellation never edits them.
func approvalBaselines(ctx context.Context, tx *sql.Tx, itemIDs []string, actor string) error {
	type res struct {
		Id         string    `json:"id"`
		ResourceId string    `json:"resourceId"`
		StartAt    time.Time `json:"startAt"`
		EndAt      time.Time `json:"endAt"`
		Quantity   int       `json:"quantity"`
	}
	type item struct {
		Id           string    `json:"id"`
		ProjectId    *string   `json:"projectId"`
		Name         string    `json:"name"`
		StartAt      time.Time `json:"startAt"`
		EndAt        time.Time `json:"endAt"`
		Version      int64     `json:"version"`
		Reservations []res     `json:"reservations"`
	}
	byScope := map[string][]item{}
	var scopes []string
	for _, id := range itemIDs {
		var it item
		var scope string
		if err := tx.QueryRowContext(ctx, `SELECT id,project_id,name,start_at,end_at,version,portfolio_scope_id FROM schedule_items WHERE id=$1`, id).Scan(&it.Id, &it.ProjectId, &it.Name, &it.StartAt, &it.EndAt, &it.Version, &scope); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,resource_id,start_at,end_at,quantity FROM resource_reservations WHERE schedule_item_id=$1 AND status='ACTIVE' ORDER BY id`, id)
		if err != nil {
			return err
		}
		it.Reservations = []res{}
		for rows.Next() {
			var r res
			if err := rows.Scan(&r.Id, &r.ResourceId, &r.StartAt, &r.EndAt, &r.Quantity); err != nil {
				rows.Close()
				return err
			}
			it.Reservations = append(it.Reservations, r)
		}
		rows.Close()
		if _, ok := byScope[scope]; !ok {
			scopes = append(scopes, scope)
		}
		byScope[scope] = append(byScope[scope], it)
	}
	sort.Strings(scopes)
	for _, scope := range scopes {
		var next int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(baseline_version),0)+1 FROM schedule_baselines WHERE portfolio_scope_id=$1`, scope).Scan(&next); err != nil {
			return err
		}
		snapshot, err := json.Marshal(map[string]any{"items": byScope[scope]})
		if err != nil {
			return err
		}
		sum := sha256.Sum256(snapshot)
		var project *string
		if items := byScope[scope]; len(items) == 1 {
			project = items[0].ProjectId
		}
		name := fmt.Sprintf("审批 %d 项排期", len(byScope[scope]))
		if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_baselines(id,portfolio_scope_id,project_id,name,baseline_version,approved_by,approved_at,snapshot_json,snapshot_sha256) VALUES($1,$2,$3,$4,$5,$6,now(),$7,$8)`,
			platform.ID(), scope, project, name, next, actor, snapshot, hex.EncodeToString(sum[:])); err != nil {
			return err
		}
	}
	return nil
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
	if len([]rune(strings.TrimSpace(reason))) < 8 {
		return ErrShortReason
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var resourceID sql.NullString
	var conflictType string
	if err := tx.QueryRowContext(ctx, `SELECT resource_id,conflict_type FROM schedule_conflicts WHERE id=$1 FOR UPDATE`, conflictID).Scan(&resourceID, &conflictType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if conflictType == "DEPENDENCY_VIOLATION" {
		// A dependency violation is fixed by re-planning, never overridden.
		return ErrHardOverride
	}
	if resourceID.Valid {
		var overrideAllowed bool
		if err := tx.QueryRowContext(ctx, `SELECT override_allowed FROM resources WHERE id=$1`, resourceID.String).Scan(&overrideAllowed); err != nil {
			return err
		}
		if !overrideAllowed && conflictType != "CALENDAR_UNAVAILABLE" {
			return ErrHardOverride
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE schedule_conflicts SET status='OVERRIDDEN',resolved_at=now(),updated_at=now() WHERE id=$1 AND status='OPEN'`, conflictID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: conflict is no longer open", ErrInvalidTransition)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_conflict_overrides(conflict_id,reason,overridden_by,overridden_at,request_id) VALUES($1,$2,$3,now(),$4)`, conflictID, strings.TrimSpace(reason), actor, requestID); err != nil {
		return err
	}
	if err := auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Action: "conflict_override", Result: "success", RequestID: requestID}); err != nil {
		return err
	}
	return tx.Commit()
}

// CalendarRule is a weekly availability rule expressed as local wall-clock time.
type CalendarRule struct {
	Weekday      int    `json:"weekday"`
	StartLocal   string `json:"startLocal"`
	EndLocal     string `json:"endLocal"`
	Availability string `json:"availability"`
}

// ReplaceCalendar atomically replaces a resource calendar's timezone and weekly
// rules. The expected version provides optimistic concurrency.
func ReplaceCalendar(ctx context.Context, db *sql.DB, resourceID, timezone string, rules []CalendarRule, expectedVersion int64) error {
	if _, err := time.LoadLocation(timezone); err != nil || timezone == "" {
		return fmt.Errorf("%w: invalid timezone", ErrInvalid)
	}
	seen := map[int]bool{}
	for i, r := range rules {
		if r.Weekday < 0 || r.Weekday > 6 || seen[r.Weekday] {
			return fmt.Errorf("%w: rule %d: weekday 0..6, at most one rule per weekday", ErrInvalid, i+1)
		}
		seen[r.Weekday] = true
		s, e1 := time.Parse("15:04", r.StartLocal)
		e, e2 := time.Parse("15:04", r.EndLocal)
		if e1 != nil || e2 != nil || !e.After(s) {
			return fmt.Errorf("%w: rule %d: HH:MM times with end after start", ErrInvalid, i+1)
		}
		if r.Availability != "AVAILABLE" && r.Availability != "UNAVAILABLE" {
			return fmt.Errorf("%w: rule %d: availability AVAILABLE or UNAVAILABLE", ErrInvalid, i+1)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var calID string
	// Serialize calendar writers per resource (the first write has no row to lock yet).
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM resources WHERE id=$1 FOR UPDATE`, resourceID); err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM resource_calendars WHERE resource_id=$1`, resourceID).Scan(&calID)
	if errors.Is(err, sql.ErrNoRows) {
		if expectedVersion != 0 {
			return ErrVersionConflict
		}
		calID = platform.ID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO resource_calendars(id,resource_id,timezone,version) VALUES($1,$2,$3,1)`, calID, resourceID, timezone); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		res, err := tx.ExecContext(ctx, `UPDATE resource_calendars SET timezone=$2,version=version+1,updated_at=now() WHERE id=$1 AND version=$3`, calID, timezone, expectedVersion)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrVersionConflict
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM resource_calendar_rules WHERE calendar_id=$1`, calID); err != nil {
		return err
	}
	for _, r := range rules {
		if _, err := tx.ExecContext(ctx, `INSERT INTO resource_calendar_rules(calendar_id,weekday,start_local,end_local,availability) VALUES($1,$2,$3,$4,$5)`, calID, r.Weekday, r.StartLocal, r.EndLocal, r.Availability); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CreateDependency inserts an FS dependency after cycle detection. SS/FF/SF are
// rejected as unsupported in this batch.
func CreateDependency(ctx context.Context, db *sql.DB, in contract.ScheduleDependency) error {
	if in.DependencyType != "FS" {
		return errors.New("unsupported dependency type")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if in.Id == "" {
		in.Id = platform.ID()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_dependencies(id,predecessor_item_id,successor_item_id,dependency_type,lag_minutes) VALUES($1,$2,$3,$4,$5)`, in.Id, in.PredecessorItemId, in.SuccessorItemId, in.DependencyType, in.LagMinutes); err != nil {
		return err
	}
	if err := ValidateDependencyAcyclic(ctx, tx, in.SuccessorItemId); err != nil {
		return err
	}
	return tx.Commit()
}

// Calendar is the read model of a resource calendar: version 0 and no rules when none exists yet.
type Calendar struct {
	Id       string         `json:"id"`
	Timezone string         `json:"timezone"`
	Version  int64          `json:"version"`
	Rules    []CalendarRule `json:"rules"`
}

func GetCalendar(ctx context.Context, db *sql.DB, resourceID string) (Calendar, error) {
	c := Calendar{Timezone: "Asia/Shanghai", Rules: []CalendarRule{}}
	err := db.QueryRowContext(ctx, `SELECT id,timezone,version FROM resource_calendars WHERE resource_id=$1`, resourceID).Scan(&c.Id, &c.Timezone, &c.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	rows, err := db.QueryContext(ctx, `SELECT weekday,to_char(start_local,'HH24:MI'),to_char(end_local,'HH24:MI'),availability FROM resource_calendar_rules WHERE calendar_id=$1 ORDER BY weekday`, c.Id)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var r CalendarRule
		if err := rows.Scan(&r.Weekday, &r.StartLocal, &r.EndLocal, &r.Availability); err != nil {
			return c, err
		}
		c.Rules = append(c.Rules, r)
	}
	return c, rows.Err()
}
