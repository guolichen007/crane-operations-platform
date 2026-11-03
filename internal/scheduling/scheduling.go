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
	Type     string
	Severity string
	Left     string
	Right    string
	Resource string
	Detail   string
	Start    time.Time
	End      time.Time
	Hard     bool
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
// The portfolio scope is resolved authoritatively, never trusted from the
// client body.
func CreateScheduleItem(ctx context.Context, db *sql.DB, in contract.ScheduleItem) (contract.ScheduleItem, error) {
	if in.Id == "" {
		in.Id = platform.ID()
	}
	if !in.StartAt.Before(in.EndAt) {
		return in, errors.New("schedule item start_at must be before end_at")
	}
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
	if in.Id == "" {
		in.Id = platform.ID()
	}
	if !in.StartAt.Before(in.EndAt) {
		return in, errors.New("reservation start_at must be before end_at")
	}
	var itemStart, itemEnd time.Time
	var itemScope sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT start_at,end_at,portfolio_scope_id FROM schedule_items WHERE id=$1`, in.ScheduleItemId).Scan(&itemStart, &itemEnd, &itemScope); err != nil {
		return in, err
	}
	if in.StartAt.Before(itemStart) || in.EndAt.After(itemEnd) {
		return in, errors.New("reservation range is outside its schedule item range")
	}
	var resScope string
	if err := db.QueryRowContext(ctx, `SELECT portfolio_scope_id FROM resources WHERE id=$1`, in.ResourceId).Scan(&resScope); err != nil {
		return in, err
	}
	if itemScope.Valid && itemScope.String != resScope {
		return in, ErrCrossPortfolio
	}
	_, err := db.ExecContext(ctx, `INSERT INTO resource_reservations(id,schedule_item_id,resource_id,portfolio_scope_id,start_at,end_at,quantity,status,created_by,version) VALUES($1,$2,$3,$4,$5,$6,$7,'PENDING',$8,$9)`,
		in.Id, in.ScheduleItemId, in.ResourceId, resScope, in.StartAt, in.EndAt, in.Quantity, "", in.Version)
	in.Status = "PENDING"
	return in, err
}

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

// CalendarRule is a weekly availability rule expressed as local wall-clock time.
type CalendarRule struct {
	Weekday      int
	StartLocal   string
	EndLocal     string
	Availability string
}

// ReplaceCalendar atomically replaces a resource calendar's timezone and weekly
// rules. The expected version provides optimistic concurrency.
func ReplaceCalendar(ctx context.Context, db *sql.DB, resourceID, timezone string, rules []CalendarRule, expectedVersion int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var calID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM resource_calendars WHERE resource_id=$1`, resourceID).Scan(&calID)
	if errors.Is(err, sql.ErrNoRows) {
		calID = platform.ID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO resource_calendars(id,resource_id,timezone,version) VALUES($1,$2,$3,1)`, calID, resourceID, timezone); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE resource_calendars SET timezone=$2,version=version+1,updated_at=now() WHERE id=$1 AND version=$3`, calID, timezone, expectedVersion); err != nil {
			return err
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
