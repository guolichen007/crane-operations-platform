package metering

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

// Schedule status values (B4).
const (
	StatusNormal       = "NORMAL"
	StatusDueSoon      = "DUE_SOON"
	StatusDue          = "DUE"
	StatusOverdue      = "OVERDUE"
	StatusInsufficient = "INSUFFICIENT_DATA"
)

// Rule is one approved (or test-only) maintenance rule.
type Rule struct {
	ID, AssetID, Code, Name, Kind, Type, Slot string
	MeterID                                   sql.NullString
	IntervalDays                              sql.NullInt64
	IntervalMeter                             sql.NullFloat64
	LeadDays, WarnPercents                    []int64
	TestOnly                                  bool
	TestAnchorAt                              sql.NullTime
	EffectiveFrom                             time.Time
}

// Evaluation is the computed state of one rule for one source mode.
type Evaluation struct {
	RuleID          string     `json:"ruleId"`
	SourceMode      string     `json:"sourceMode"`
	CycleNo         int        `json:"cycleNo"`
	Status          string     `json:"status"`
	NextDueAt       *time.Time `json:"nextDueAt"`
	NextDueMeter    *float64   `json:"nextDueMeter"`
	DaysLeft        *int       `json:"daysLeft"`
	ProgressPercent *float64   `json:"progressPercent"`
	Stages          []string   `json:"stages"` // every stage reached in this cycle (alerts are unique per stage)
	Reason          string     `json:"reason,omitempty"`
}

// Evaluate computes the schedule of a rule (pure). anchorAt / anchorMeter are the cycle start
// (installation or last completion); current is the meter accumulated value (nil = unknown).
// TIME_BASED is evaluated on the calendar alone, so an offline device keeps progressing.
// HYBRID = whichever comes first.
func Evaluate(r Rule, mode string, cycle int, anchorAt *time.Time, anchorMeter, current *float64, now time.Time) Evaluation {
	ev := Evaluation{RuleID: r.ID, SourceMode: mode, CycleNo: cycle, Stages: []string{}}
	rank := map[string]int{StatusInsufficient: 0, StatusNormal: 1, StatusDueSoon: 2, StatusDue: 3, StatusOverdue: 4}
	worst := ""
	merge := func(s string) {
		if worst == "" || rank[s] > rank[worst] {
			worst = s
		}
	}
	timeOK := false
	if r.Type == "TIME_BASED" || r.Type == "HYBRID" {
		if r.IntervalDays.Valid && anchorAt != nil {
			timeOK = true
			due := anchorAt.AddDate(0, 0, int(r.IntervalDays.Int64))
			ev.NextDueAt = &due
			days := int(math.Floor(due.Sub(now).Hours() / 24))
			if now.Before(due) && due.Sub(now) < 24*time.Hour && due.YearDay() == now.YearDay() {
				days = 0
			}
			ev.DaysLeft = &days
			lead := append([]int64(nil), r.LeadDays...)
			sort.Slice(lead, func(i, j int) bool { return lead[i] > lead[j] })
			switch {
			case days < 0:
				merge(StatusOverdue)
				ev.Stages = append(ev.Stages, leadStages(lead, 0)...)
				ev.Stages = append(ev.Stages, "DUE", "OVERDUE")
			case days == 0:
				merge(StatusDue)
				ev.Stages = append(ev.Stages, leadStages(lead, 0)...)
				ev.Stages = append(ev.Stages, "DUE")
			default:
				st := leadStages(lead, days)
				if len(st) > 0 {
					merge(StatusDueSoon)
				} else {
					merge(StatusNormal)
				}
				ev.Stages = append(ev.Stages, st...)
			}
		} else if r.Type == "TIME_BASED" {
			ev.Reason = "缺少实际安装 / 上次完成日期或周期，无法计算日期"
		}
	}
	meterOK := false
	if r.Type == "METER_BASED" || r.Type == "HYBRID" {
		if r.IntervalMeter.Valid && anchorMeter != nil && current != nil {
			meterOK = true
			due := *anchorMeter + r.IntervalMeter.Float64
			ev.NextDueMeter = &due
			// Thresholds compare the exact ratio (799 999 / 1 000 000 is below 80 %); only the
			// displayed progress is rounded.
			pct := (*current - *anchorMeter) / r.IntervalMeter.Float64 * 100
			shown := math.Floor(pct*100) / 100
			ev.ProgressPercent = &shown
			warn := append([]int64(nil), r.WarnPercents...)
			sort.Slice(warn, func(i, j int) bool { return warn[i] < warn[j] })
			for _, w := range warn {
				if pct >= float64(w) {
					ev.Stages = append(ev.Stages, fmt.Sprintf("PCT_%d", w))
				}
			}
			switch {
			case pct > 100:
				merge(StatusOverdue)
				ev.Stages = append(ev.Stages, "PCT_100")
			case pct >= 100:
				merge(StatusDue)
				ev.Stages = append(ev.Stages, "PCT_100")
			case len(ev.Stages) > 0:
				merge(StatusDueSoon)
			default:
				merge(StatusNormal)
			}
		} else if r.Type == "METER_BASED" {
			ev.Reason = "缺少计量基准（安装时累计值 / 初始使用量）或计量数据，无法计算使用量"
		}
	}
	if r.Type == "HYBRID" && !timeOK && !meterOK {
		ev.Reason = "日期与计量基准均不足"
	}
	if worst == "" {
		worst = StatusInsufficient
	}
	ev.Status = worst
	ev.Stages = dedupe(ev.Stages)
	return ev
}

func leadStages(lead []int64, daysLeft int) []string {
	out := []string{}
	for _, l := range lead {
		if int64(daysLeft) <= l {
			out = append(out, fmt.Sprintf("LEAD_%d", l))
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

var allowedStages = map[string]bool{"LEAD_90": true, "LEAD_30": true, "LEAD_7": true, "DUE": true, "OVERDUE": true, "PCT_80": true, "PCT_90": true, "PCT_100": true}

// EvaluateAll evaluates every APPROVED rule (REAL) and every test-only rule (its own mode) of an
// asset, persists the schedule state and writes each newly reached stage once as an alert.
// Re-running (restart, duplicate reports, periodic tick) never duplicates an alert: the alert key
// is (rule, mode, cycle, stage).
func EvaluateAll(ctx context.Context, db *sql.DB, assetID string, now time.Time) ([]Evaluation, error) {
	rules, err := loadRules(ctx, db, assetID, now)
	if err != nil {
		return nil, err
	}
	simModes, err := simulatedModes(ctx, db, assetID)
	if err != nil {
		return nil, err
	}
	out := []Evaluation{}
	for _, r := range rules {
		for _, mode := range modesFor(r, simModes) {
			ev, err := evaluateOne(ctx, db, r, mode, now)
			if err != nil {
				return nil, err
			}
			out = append(out, ev)
		}
	}
	return out, nil
}

// modesFor: a real rule runs on REAL data only; a test-only rule runs on the non-REAL modes the
// asset actually has (its registered SIMULATION / TEST runtimes), SIMULATION when it has none.
func modesFor(r Rule, simModes []string) []string {
	if r.TestOnly {
		return simModes
	}
	return []string{"REAL"}
}

func simulatedModes(ctx context.Context, db *sql.DB, assetID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT source_mode FROM devices WHERE asset_device_id=$1 AND source_mode<>'REAL' ORDER BY 1`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var m string
		if err = rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		out = []string{"SIMULATION"}
	}
	return out, rows.Err()
}

func loadRules(ctx context.Context, db *sql.DB, assetID string, now time.Time) ([]Rule, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, asset_id, rule_code, name, rule_kind, rule_type, COALESCE(equipment_slot,''), meter_id, interval_days, interval_meter, lead_days, warn_percents, test_only, test_anchor_at, COALESCE(effective_from, created_at)
FROM maintenance_rules WHERE asset_id=$1 AND ((approval_status='APPROVED' AND effective_from <= $2) OR (test_only AND approval_status<>'RETIRED')) ORDER BY rule_code`, assetID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		var r Rule
		var lead, warn string
		if err = rows.Scan(&r.ID, &r.AssetID, &r.Code, &r.Name, &r.Kind, &r.Type, &r.Slot, &r.MeterID, &r.IntervalDays, &r.IntervalMeter, &lead, &warn, &r.TestOnly, &r.TestAnchorAt, &r.EffectiveFrom); err != nil {
			return nil, err
		}
		r.LeadDays, r.WarnPercents = parseIntArray(lead), parseIntArray(warn)
		out = append(out, r)
	}
	return out, rows.Err()
}

func parseIntArray(s string) []int64 {
	out := []int64{}
	var cur int64
	in := false
	for _, c := range s {
		if c >= '0' && c <= '9' {
			cur, in = cur*10+int64(c-'0'), true
		} else if in {
			out, cur, in = append(out, cur), 0, false
		}
	}
	if in {
		out = append(out, cur)
	}
	return out
}

// anchor returns the cycle anchor: the last completed work order of the rule (mode) or the
// installation of the slot's current instance (actual installed_at, never the import time) and
// its meter baseline. Missing values stay nil (数据不足).
func anchor(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, r Rule, mode string) (cycle int, at *time.Time, meter *float64, current *float64, err error) {
	cycle = 1
	var completed sql.NullTime
	var lastCycle sql.NullInt64
	if err = q.QueryRowContext(ctx, `SELECT completed_at, cycle_no FROM maintenance_work_orders WHERE rule_id=$1 AND source_mode=$2 AND status='COMPLETED' ORDER BY cycle_no DESC LIMIT 1`, r.ID, mode).Scan(&completed, &lastCycle); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return
	}
	err = nil
	if r.MeterID.Valid {
		var acc sql.NullFloat64
		if e := q.QueryRowContext(ctx, `SELECT accumulated FROM meter_states WHERE meter_id=$1 AND source_mode=$2`, r.MeterID.String, mode).Scan(&acc); e != nil && !errors.Is(e, sql.ErrNoRows) {
			err = e
			return
		}
		if acc.Valid {
			v := acc.Float64
			current = &v
		}
	}
	if completed.Valid {
		cycle = int(lastCycle.Int64) + 1
		t := completed.Time
		at = &t
		if r.MeterID.Valid {
			var base sql.NullFloat64
			if e := q.QueryRowContext(ctx, `SELECT anchor_meter FROM maintenance_schedules WHERE rule_id=$1 AND source_mode=$2`, r.ID, mode).Scan(&base); e == nil && base.Valid {
				v := base.Float64
				meter = &v
			}
		}
		return
	}
	// First cycle: the installed instance of the slot (or the asset commissioning). A test-only rule
	// uses its simulated anchor date instead (the real ledger keeps installed_at unknown).
	if r.TestOnly && r.TestAnchorAt.Valid {
		t := r.TestAnchorAt.Time
		at = &t
	}
	if r.Slot != "" {
		var installed sql.NullTime
		var eqID string
		e := q.QueryRowContext(ctx, `SELECT id, installed_at FROM asset_equipment_installations WHERE asset_id=$1 AND semantic_slot=$2 AND valid_to IS NULL`, r.AssetID, r.Slot).Scan(&eqID, &installed)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			err = e
			return
		}
		if installed.Valid && !r.TestOnly {
			t := installed.Time
			at = &t
		}
		if r.MeterID.Valid && eqID != "" {
			var base, initial sql.NullFloat64
			if e := q.QueryRowContext(ctx, `SELECT baseline_accumulated, initial_usage FROM equipment_meter_baselines WHERE equipment_installation_id=$1 AND meter_id=$2 AND source_mode=$3`, eqID, r.MeterID.String, mode).Scan(&base, &initial); e == nil && base.Valid {
				v := base.Float64
				if initial.Valid {
					v -= initial.Float64 // instance already used before metering: its usage counts
				}
				meter = &v
			}
		}
	} else {
		var commissioned sql.NullTime
		if e := q.QueryRowContext(ctx, `SELECT commissioning_date::timestamptz FROM asset_devices WHERE id=$1`, r.AssetID).Scan(&commissioned); e == nil && commissioned.Valid {
			t := commissioned.Time
			at = &t
		}
	}
	return
}

func evaluateOne(ctx context.Context, db *sql.DB, r Rule, mode string, now time.Time) (Evaluation, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Evaluation{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(7160022, hashtext($1||$2))`, r.ID, mode); err != nil {
		return Evaluation{}, err
	}
	cycle, at, base, current, err := anchor(ctx, tx, r, mode)
	if err != nil {
		return Evaluation{}, err
	}
	ev := Evaluate(r, mode, cycle, at, base, current, now)
	if _, err = tx.ExecContext(ctx, `INSERT INTO maintenance_schedules(rule_id,source_mode,cycle_no,anchor_at,anchor_meter,next_due_at,next_due_meter,status,stage,progress_percent,days_left,evaluated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (rule_id,source_mode) DO UPDATE SET cycle_no=excluded.cycle_no,anchor_at=excluded.anchor_at,anchor_meter=excluded.anchor_meter,next_due_at=excluded.next_due_at,
next_due_meter=excluded.next_due_meter,status=excluded.status,stage=excluded.stage,progress_percent=excluded.progress_percent,days_left=excluded.days_left,evaluated_at=excluded.evaluated_at,version=maintenance_schedules.version+1`,
		r.ID, mode, cycle, at, base, ev.NextDueAt, ev.NextDueMeter, ev.Status, lastStage(ev.Stages), ev.ProgressPercent, ev.DaysLeft, now); err != nil {
		return ev, err
	}
	for _, st := range ev.Stages {
		if !allowedStages[st] {
			continue // a configured non-standard lead / percent stays visible in status, not as an alert
		}
		detail, _ := json.Marshal(map[string]any{"status": ev.Status, "nextDueAt": ev.NextDueAt, "nextDueMeter": ev.NextDueMeter, "progressPercent": ev.ProgressPercent, "daysLeft": ev.DaysLeft})
		if _, err = tx.ExecContext(ctx, `INSERT INTO maintenance_alerts(id,rule_id,asset_id,source_mode,cycle_no,stage,triggered_at,detail) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb)
ON CONFLICT (rule_id,source_mode,cycle_no,stage) DO NOTHING`, platform.ID(), r.ID, r.AssetID, mode, cycle, st, now, string(detail)); err != nil {
			return ev, err
		}
	}
	return ev, tx.Commit()
}

func lastStage(s []string) any {
	if len(s) == 0 {
		return nil
	}
	return s[len(s)-1]
}
