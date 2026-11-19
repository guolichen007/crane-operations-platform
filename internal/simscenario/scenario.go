// Package simscenario is the controlled, idempotent SIMULATION maintenance scenario of the 老河口
// first asset (R9 收口 G2). It runs on the deployed server through the host tool `lhk-sim`:
//
//	plan            read-only: target, registered SIM runtime, what apply would create (digest)
//	apply           creates the marked scenario rules (test_only, code SIM-SCN-*) and simulated
//	                initial baselines — nothing else; never REAL, never approves a real rule
//	verify          read-only acceptance: the alerts produced by REAL MQTT meter batches of the
//	                registered simulator + the server's own evaluation (time, cycles, hours,
//	                hybrid, partial-data hybrid, calibration), SIM/REAL isolation, drafts untouched
//	cleanup-plan    read-only: the exact scenario records that cleanup would delete
//	cleanup-apply   deletes exactly those records (approved digest), so the scenario can rerun
//
// The tool never ingests telemetry and never evaluates schedules itself: meters advance only
// through the simulator → Mosquitto (mTLS + ACL) → cloud runtime loop, and alerts are raised by the
// cloud maintenance loop. It is not the CI fixture (tests/integration/a1fixture seed-lhk), which
// must never run against a deployed database.
package simscenario

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

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/metering"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

const (
	AssetID    = "LHK-CRANE-001"
	ProjectID  = "LHK"
	RuntimeID  = "SIM-LHK-CRANE-001"
	Prefix     = "SIM-SCN-"
	notePrefix = "SIM-SCN:"
)

var (
	ErrNotReady       = errors.New("scenario prerequisites missing")
	ErrDigestMismatch = errors.New("approved digest does not match the current plan")
	ErrActorRequired  = errors.New("explicit host administrator actor required")
)

// RuleSpec is one scenario rule. AnchorDaysAgo positions the simulated installation date so the
// calendar part is in a known stage; InitialUsage positions the meter part (nil = deliberately
// unknown: the rule must show 部分数据不足 and still raise its known due condition).
type RuleSpec struct {
	Code          string   `json:"code"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Type          string   `json:"type"`
	Slot          string   `json:"slot"`
	MeterCode     string   `json:"meterCode,omitempty"`
	IntervalDays  *int     `json:"intervalDays,omitempty"`
	IntervalMeter *float64 `json:"intervalMeter,omitempty"`
	AnchorDaysAgo *int     `json:"anchorDaysAgo,omitempty"`
	InitialUsage  *float64 `json:"initialUsage,omitempty"`
	Expect        Expect   `json:"expect"`
}

// Expect is the acceptance condition of one rule (verify).
type Expect struct {
	Stage    string `json:"stage"`              // alert stage that must exist (SIMULATION)
	Gap      string `json:"gap,omitempty"`      // data gap that must be reported (partial data)
	Category string `json:"category,omitempty"` // center category (e.g. CALIBRATION_DUE)
	ByMeter  bool   `json:"byMeter"`            // reached only through simulated meter data (needs the simulator)
}

func ip(v int) *int         { return &v }
func fp(v float64) *float64 { return &v }

// Rules is the fixed scenario. The meter thresholds are reached by the normal simulator rates
// (1 cycle and 1/60 h per simulated minute): 40 cycles and 0.2 h after the baseline, i.e. about
// 40 simulated minutes — with --meter-speedup 60 well under one real minute, in real time ~40 min.
var Rules = []RuleSpec{
	{Code: Prefix + "TIME", Name: "【模拟】大车变频器 定期保养（时间）", Kind: "MAINTENANCE", Type: "TIME_BASED", Slot: "GANTRY-03",
		IntervalDays: ip(365), AnchorDaysAgo: ip(360), Expect: Expect{Stage: "LEAD_7"}},
	{Code: Prefix + "CYCLES", Name: "【模拟】大车总接触器 寿命（次数）", Kind: "REPLACEMENT", Type: "METER_BASED", Slot: "GANTRY-01", MeterCode: "LHK-P001.cycles",
		IntervalMeter: fp(1000), InitialUsage: fp(960), Expect: Expect{Stage: "PCT_100", ByMeter: true}},
	{Code: Prefix + "HOURS", Name: "【模拟】大车变频器 运行小时保养（小时）", Kind: "MAINTENANCE", Type: "METER_BASED", Slot: "GANTRY-03", MeterCode: "LHK-P003.hours",
		IntervalMeter: fp(100), InitialUsage: fp(99.8), Expect: Expect{Stage: "PCT_100", ByMeter: true}},
	{Code: Prefix + "HYBRID", Name: "【模拟】大车抱闸接触器 混合规则（时间先到）", Kind: "REPLACEMENT", Type: "HYBRID", Slot: "GANTRY-02", MeterCode: "LHK-P002.cycles",
		IntervalDays: ip(30), IntervalMeter: fp(100000), AnchorDaysAgo: ip(25), InitialUsage: fp(0), Expect: Expect{Stage: "LEAD_7"}},
	{Code: Prefix + "HYBRID-PARTIAL", Name: "【模拟】小车总接触器 混合规则（计量基准未知）", Kind: "REPLACEMENT", Type: "HYBRID", Slot: "TROLLEY-14", MeterCode: "LHK-P014.cycles",
		IntervalDays: ip(30), IntervalMeter: fp(100000), AnchorDaysAgo: ip(31), InitialUsage: nil, Expect: Expect{Stage: "OVERDUE", Gap: "METER"}},
	{Code: Prefix + "CAL", Name: "【模拟】称重传感器 校准", Kind: "CALIBRATION", Type: "TIME_BASED", Slot: "GRAB-43",
		IntervalDays: ip(180), AnchorDaysAgo: ip(175), Expect: Expect{Stage: "LEAD_7", Category: "CALIBRATION_DUE"}},
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Target identifies the database the tool acts on.
type Target struct {
	Database      string `json:"database"`
	Role          string `json:"role"`
	SchemaVersion int64  `json:"schemaVersion"`
}

func target(ctx context.Context, q queryer) (Target, error) {
	var t Target
	err := q.QueryRowContext(ctx, `SELECT current_database(), current_user, COALESCE((SELECT max(version_id) FROM goose_db_version WHERE is_applied),0)`).Scan(&t.Database, &t.Role, &t.SchemaVersion)
	return t, err
}

// Plan is the read-only review surface of apply.
type Plan struct {
	Target      Target     `json:"target"`
	Runtime     string     `json:"runtime"`
	RuntimeMode string     `json:"runtimeMode"`
	Rules       []PlanItem `json:"rules"`
	Baselines   []PlanItem `json:"baselines"`
	Problems    []string   `json:"problems"`
	RealDrafts  int        `json:"realDraftRules"` // informational: never changed by this tool
	Digest      string     `json:"digest"`
}

type PlanItem struct {
	Key    string `json:"key"`
	Status string `json:"status"` // NEW | EXISTING | CHANGED
}

func digest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// PlanScenario checks the prerequisites (asset imported, the SIM runtime registered as SIMULATION
// and bound to the asset, every slot / meter present) and lists what apply creates.
func PlanScenario(ctx context.Context, q queryer) (Plan, error) {
	p := Plan{Runtime: RuntimeID, Rules: []PlanItem{}, Baselines: []PlanItem{}, Problems: []string{}}
	var err error
	if p.Target, err = target(ctx, q); err != nil {
		return p, err
	}
	var asset, mode sql.NullString
	if err = q.QueryRowContext(ctx, `SELECT asset_device_id, source_mode FROM devices WHERE id=$1 AND project_id=$2`, RuntimeID, ProjectID).Scan(&asset, &mode); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	p.RuntimeMode = mode.String
	switch {
	case !mode.Valid:
		p.Problems = append(p.Problems, "SIM-LHK-CRANE-001 is not registered (runtime-identity plan/apply first)")
	case mode.String != "SIMULATION":
		p.Problems = append(p.Problems, "SIM-LHK-CRANE-001 is registered as "+mode.String+", not SIMULATION")
	case asset.String != AssetID:
		p.Problems = append(p.Problems, "SIM-LHK-CRANE-001 is not bound to LHK-CRANE-001")
	}
	if err = q.QueryRowContext(ctx, `SELECT count(*) FROM maintenance_rules WHERE asset_id=$1 AND NOT test_only AND approval_status='DRAFT_UNAPPROVED'`, AssetID).Scan(&p.RealDrafts); err != nil {
		return p, err
	}
	for _, r := range Rules {
		var eq string
		if e := q.QueryRowContext(ctx, `SELECT id FROM asset_equipment_installations WHERE asset_id=$1 AND semantic_slot=$2 AND valid_to IS NULL`, AssetID, r.Slot).Scan(&eq); e != nil {
			p.Problems = append(p.Problems, "slot "+r.Slot+" missing (onboarding apply first)")
			continue
		}
		if r.MeterCode != "" {
			var n int
			if e := q.QueryRowContext(ctx, `SELECT count(*) FROM meter_definitions WHERE asset_id=$1 AND meter_code=$2`, AssetID, r.MeterCode).Scan(&n); e != nil || n != 1 {
				p.Problems = append(p.Problems, "meter "+r.MeterCode+" missing")
				continue
			}
		}
		status, e := ruleStatus(ctx, q, r)
		if e != nil {
			return p, e
		}
		p.Rules = append(p.Rules, PlanItem{Key: r.Code, Status: status})
		if r.MeterCode != "" {
			var n int
			if e := q.QueryRowContext(ctx, `SELECT count(*) FROM equipment_meter_baselines b JOIN meter_definitions d ON d.id=b.meter_id WHERE b.equipment_installation_id=$1 AND d.meter_code=$2 AND b.source_mode='SIMULATION'`, eq, r.MeterCode).Scan(&n); e != nil {
				return p, e
			}
			st := "NEW"
			if n > 0 {
				st = "EXISTING"
			}
			p.Baselines = append(p.Baselines, PlanItem{Key: r.Slot + "|" + r.MeterCode, Status: st})
		}
	}
	p.Digest = digest(struct {
		T Target
		M string
		R []PlanItem
		B []PlanItem
		P []string
	}{p.Target, p.RuntimeMode, p.Rules, p.Baselines, p.Problems})
	return p, nil
}

// ruleStatus: EXISTING when an active scenario rule with the same definition exists, CHANGED when
// it exists with another definition (apply replaces it by a new version), NEW otherwise.
func ruleStatus(ctx context.Context, q queryer, r RuleSpec) (string, error) {
	var name, kind, typ, slot string
	var meter sql.NullString
	var days sql.NullInt64
	var interval sql.NullFloat64
	var testOnly bool
	err := q.QueryRowContext(ctx, `SELECT r.name, r.rule_kind, r.rule_type, COALESCE(r.equipment_slot,''), d.meter_code, r.interval_days, r.interval_meter, r.test_only
FROM maintenance_rules r LEFT JOIN meter_definitions d ON d.id=r.meter_id WHERE r.asset_id=$1 AND r.rule_code=$2 AND r.approval_status<>'RETIRED' ORDER BY r.version DESC LIMIT 1`, AssetID, r.Code).
		Scan(&name, &kind, &typ, &slot, &meter, &days, &interval, &testOnly)
	if errors.Is(err, sql.ErrNoRows) {
		return "NEW", nil
	}
	if err != nil {
		return "", err
	}
	same := testOnly && name == r.Name && kind == r.Kind && typ == r.Type && slot == r.Slot && meter.String == r.MeterCode &&
		(r.IntervalDays == nil) == !days.Valid && (r.IntervalDays == nil || int64(*r.IntervalDays) == days.Int64) &&
		(r.IntervalMeter == nil) == !interval.Valid && (r.IntervalMeter == nil || *r.IntervalMeter == interval.Float64)
	if same {
		return "EXISTING", nil
	}
	return "CHANGED", nil
}

// Record is the apply outcome.
type Record struct {
	Result         string   `json:"result"` // success | noop
	RulesCreated   []string `json:"rulesCreated"`
	BaselinesAdded []string `json:"baselinesRecorded"`
	Actor          string   `json:"actor"`
	Digest         string   `json:"approvedDigest"`
}

// Apply creates the scenario rules and simulated baselines. Re-running with the same plan is a
// noop; it writes only test_only rules (DB CHECK: never approvable, never REAL) and SIMULATION
// baselines. It does not evaluate: alerts come from the server loop on real MQTT meter data.
func Apply(ctx context.Context, db *sql.DB, approved, actor, requestID string, now time.Time) (Record, error) {
	rec := Record{Actor: actor, Digest: approved, RulesCreated: []string{}, BaselinesAdded: []string{}}
	if strings.TrimSpace(actor) == "" {
		return rec, ErrActorRequired
	}
	p, err := PlanScenario(ctx, db)
	if err != nil {
		return rec, err
	}
	if p.Digest != approved {
		return rec, ErrDigestMismatch
	}
	if len(p.Problems) > 0 {
		return rec, fmt.Errorf("%w: %s", ErrNotReady, strings.Join(p.Problems, "; "))
	}
	host := "host:" + actor
	status := map[string]string{}
	for _, it := range p.Rules {
		status[it.Key] = it.Status
	}
	baseline := map[string]string{}
	for _, it := range p.Baselines {
		baseline[it.Key] = it.Status
	}
	for _, r := range Rules {
		if status[r.Code] != "EXISTING" {
			var anchor *time.Time
			if r.AnchorDaysAgo != nil {
				a := now.UTC().AddDate(0, 0, -*r.AnchorDaysAgo)
				anchor = &a
			}
			if _, err = metering.UpsertTestRule(ctx, db, AssetID, metering.TestRule{Code: r.Code, Name: r.Name, Kind: r.Kind, Type: r.Type, Slot: r.Slot, MeterCode: r.MeterCode,
				IntervalDays: r.IntervalDays, IntervalMeter: r.IntervalMeter, AnchorAt: anchor}, host); err != nil {
				return rec, err
			}
			rec.RulesCreated = append(rec.RulesCreated, r.Code)
		}
		if r.MeterCode != "" && baseline[r.Slot+"|"+r.MeterCode] == "NEW" {
			note := notePrefix + " 模拟初始基准（" + r.Code + "）"
			if r.InitialUsage == nil {
				note = notePrefix + " 模拟初始使用量未知（验证部分数据不足，" + r.Code + "）"
			}
			if err = metering.RecordBaseline(ctx, db, AssetID, r.Slot, r.MeterCode, "SIMULATION", r.InitialUsage, host, note); err != nil {
				return rec, err
			}
			rec.BaselinesAdded = append(rec.BaselinesAdded, r.Slot+"|"+r.MeterCode)
		}
	}
	rec.Result = "noop"
	if len(rec.RulesCreated)+len(rec.BaselinesAdded) > 0 {
		rec.Result = "success"
	}
	err = auditlog.Write(ctx, db, auditlog.Event{Actor: host, Project: ProjectID, Action: "lhk_sim_scenario_applied", Result: rec.Result, RequestID: requestID,
		Change: map[string]any{"rules": rec.RulesCreated, "baselines": rec.BaselinesAdded, "digest": approved}})
	return rec, err
}

// ---------------------------------------------------------------------------------------------
// Verify
// ---------------------------------------------------------------------------------------------

// Check is one acceptance item: PASS, PENDING (meter-driven, the simulator has not produced enough
// usage yet — wait and re-run) or FAIL.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type Report struct {
	Checks  []Check `json:"checks"`
	Verdict string  `json:"verdict"` // PASS | PENDING | FAIL
}

func (r *Report) add(name, status, detail string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Detail: detail})
}

// Verify is read-only. freshFor bounds how recent the simulator's meter data must be.
func Verify(ctx context.Context, q *sql.DB, now time.Time, freshFor time.Duration) (Report, error) {
	var rep Report
	count := func(query string, args ...any) (int, error) {
		var n int
		err := q.QueryRowContext(ctx, query, args...).Scan(&n)
		return n, err
	}
	var mode sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT source_mode FROM devices WHERE id=$1 AND asset_device_id=$2`, RuntimeID, AssetID).Scan(&mode); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return rep, err
	}
	if mode.String == "SIMULATION" {
		rep.add("runtime registered SIMULATION", "PASS", RuntimeID)
	} else {
		rep.add("runtime registered SIMULATION", "FAIL", "mode="+mode.String)
	}
	var last sql.NullTime
	var meters int
	if err := q.QueryRowContext(ctx, `SELECT max(s.last_sampled_at), count(*) FILTER (WHERE s.readings>=2) FROM meter_states s JOIN meter_definitions d ON d.id=s.meter_id
WHERE d.asset_id=$1 AND s.source_mode='SIMULATION' AND s.runtime_device_id=$2`, AssetID, RuntimeID).Scan(&last, &meters); err != nil {
		return rep, err
	}
	switch {
	case !last.Valid:
		rep.add("simulated meters via MQTT", "PENDING", "no meter batch received yet (is the simulator running?)")
	case now.Sub(last.Time) > freshFor:
		rep.add("simulated meters via MQTT", "FAIL", fmt.Sprintf("last batch %s ago", now.Sub(last.Time).Round(time.Second)))
	case meters < 57:
		rep.add("simulated meters via MQTT", "PENDING", fmt.Sprintf("%d / 57 meters with ≥ 2 readings", meters))
	default:
		rep.add("simulated meters via MQTT", "PASS", fmt.Sprintf("57 meters, last batch %s", last.Time.UTC().Format(time.RFC3339)))
	}
	real, err := count(`SELECT (SELECT count(*) FROM meter_states s JOIN meter_definitions d ON d.id=s.meter_id WHERE d.asset_id=$1 AND s.source_mode='REAL')
 + (SELECT count(*) FROM meter_readings r JOIN meter_definitions d ON d.id=r.meter_id WHERE d.asset_id=$1 AND r.source_mode='REAL')
 + (SELECT count(*) FROM maintenance_alerts WHERE asset_id=$1 AND source_mode='REAL')
 + (SELECT count(*) FROM maintenance_work_orders WHERE asset_id=$1 AND source_mode='REAL')
 + (SELECT count(*) FROM devices WHERE asset_device_id=$1 AND source_mode='REAL')`, AssetID)
	if err != nil {
		return rep, err
	}
	if real == 0 {
		rep.add("SIM / REAL isolation", "PASS", "no REAL runtime, meter, alert or work order on LHK-CRANE-001")
	} else {
		rep.add("SIM / REAL isolation", "FAIL", fmt.Sprintf("%d REAL rows", real))
	}
	nonTest, err := count(`SELECT count(*) FROM maintenance_rules WHERE asset_id=$1 AND rule_code LIKE 'SIM-SCN-%' AND (NOT test_only OR approval_status='APPROVED')`, AssetID)
	if err != nil {
		return rep, err
	}
	approvedByTool, err := count(`SELECT count(*) FROM maintenance_rules WHERE asset_id=$1 AND NOT test_only AND approval_status='APPROVED' AND approved_by LIKE 'host:%'`, AssetID)
	if err != nil {
		return rep, err
	}
	if nonTest == 0 && approvedByTool == 0 {
		rep.add("scenario rules test-only; real drafts not approved by tooling", "PASS", "")
	} else {
		rep.add("scenario rules test-only; real drafts not approved by tooling", "FAIL", fmt.Sprintf("nonTest=%d approvedByHost=%d", nonTest, approvedByTool))
	}
	for _, r := range Rules {
		name := "rule " + r.Code + " → " + r.Expect.Stage
		var ruleID string
		if err := q.QueryRowContext(ctx, `SELECT id FROM maintenance_rules WHERE asset_id=$1 AND rule_code=$2 AND test_only AND approval_status<>'RETIRED' ORDER BY version DESC LIMIT 1`, AssetID, r.Code).Scan(&ruleID); err != nil {
			rep.add(name, "FAIL", "scenario rule missing (lhk-sim apply)")
			continue
		}
		n, err := count(`SELECT count(*) FROM maintenance_alerts WHERE rule_id=$1 AND source_mode='SIMULATION' AND stage=$2`, ruleID, r.Expect.Stage)
		if err != nil {
			return rep, err
		}
		var status, gaps, reason string
		_ = q.QueryRowContext(ctx, `SELECT status, array_to_string(data_gaps,','), COALESCE(reason,'') FROM maintenance_schedules WHERE rule_id=$1 AND source_mode='SIMULATION'`, ruleID).Scan(&status, &gaps, &reason)
		switch {
		case n == 0 && r.Expect.ByMeter:
			rep.add(name, "PENDING", "simulated usage below the threshold yet (status "+status+")")
		case n == 0:
			rep.add(name, "FAIL", "alert missing (status "+status+")")
		case r.Expect.Gap != "" && !strings.Contains(gaps, r.Expect.Gap):
			rep.add(name, "FAIL", "partial-data gap "+r.Expect.Gap+" not reported (gaps "+gaps+")")
		default:
			rep.add(name, "PASS", strings.TrimSpace(status+" "+gaps+" "+reason))
		}
		if r.Expect.Category != "" {
			rows, err := metering.Center(ctx, q, []string{ProjectID}, now)
			if err != nil {
				return rep, err
			}
			ok := false
			for _, c := range rows {
				ok = ok || (c.RuleID != nil && *c.RuleID == ruleID && c.Category == r.Expect.Category && c.SourceMode == "SIMULATION")
			}
			if ok {
				rep.add("center "+r.Code+" "+r.Expect.Category, "PASS", "")
			} else {
				rep.add("center "+r.Code+" "+r.Expect.Category, "FAIL", "")
			}
		}
	}
	dups, err := count(`SELECT count(*) - count(DISTINCT (meter_id, source_mode, runtime_device_id, boot_id, sequence)) FROM meter_readings r JOIN meter_definitions d ON d.id=r.meter_id WHERE d.asset_id=$1`, AssetID)
	if err != nil {
		return rep, err
	}
	if dups == 0 {
		rep.add("no duplicated readings", "PASS", "")
	} else {
		rep.add("no duplicated readings", "FAIL", fmt.Sprint(dups))
	}
	rep.Verdict = "PASS"
	for _, c := range rep.Checks {
		if c.Status == "FAIL" {
			rep.Verdict = "FAIL"
			break
		}
		if c.Status == "PENDING" {
			rep.Verdict = "PENDING"
		}
	}
	return rep, nil
}

// ---------------------------------------------------------------------------------------------
// Cleanup
// ---------------------------------------------------------------------------------------------

// CleanupPlan lists the exact rows cleanup deletes. Scope: the SIM-SCN-* test rules of
// LHK-CRANE-001 with their SIMULATION schedules, alerts, work orders, transitions, simulated
// replacements and rope inspections; the SIMULATION / TEST baselines of the asset; and, only with
// includeMeterData, the simulator's own SIMULATION meter data. REAL rows are never in scope.
type CleanupPlan struct {
	Target           Target         `json:"target"`
	IncludeMeterData bool           `json:"includeMeterData"`
	Counts           map[string]int `json:"counts"`
	Digest           string         `json:"digest"`
}

type cleanupStep struct {
	key, count, del string
	meter           bool
}

const scnRules = `SELECT id FROM maintenance_rules WHERE asset_id='` + AssetID + `' AND rule_code LIKE 'SIM-SCN-%' AND test_only`
const scnOrders = `SELECT id FROM maintenance_work_orders WHERE asset_id='` + AssetID + `' AND source_mode<>'REAL' AND rule_id IN (` + scnRules + `)`
const lhkMeters = `SELECT id FROM meter_definitions WHERE asset_id='` + AssetID + `'`

var cleanupSteps = []cleanupStep{
	{"rope_inspections", `SELECT count(*) FROM wire_rope_inspections WHERE source_mode<>'REAL' AND work_order_id IN (` + scnOrders + `)`, `DELETE FROM wire_rope_inspections WHERE source_mode<>'REAL' AND work_order_id IN (` + scnOrders + `)`, false},
	{"replacements", `SELECT count(*) FROM equipment_replacements WHERE source_mode<>'REAL' AND work_order_id IN (` + scnOrders + `)`, `DELETE FROM equipment_replacements WHERE source_mode<>'REAL' AND work_order_id IN (` + scnOrders + `)`, false},
	{"work_order_transitions", `SELECT count(*) FROM work_order_transitions WHERE work_order_id IN (` + scnOrders + `)`, `DELETE FROM work_order_transitions WHERE work_order_id IN (` + scnOrders + `)`, false},
	{"alert_links", `SELECT count(*) FROM maintenance_alerts WHERE source_mode<>'REAL' AND rule_id IN (` + scnRules + `) AND work_order_id IS NOT NULL`, `UPDATE maintenance_alerts SET work_order_id=NULL WHERE source_mode<>'REAL' AND rule_id IN (` + scnRules + `)`, false},
	{"work_orders", `SELECT count(*) FROM (` + scnOrders + `) x`, `DELETE FROM maintenance_work_orders WHERE id IN (` + scnOrders + `)`, false},
	{"alerts", `SELECT count(*) FROM maintenance_alerts WHERE source_mode<>'REAL' AND rule_id IN (` + scnRules + `)`, `DELETE FROM maintenance_alerts WHERE source_mode<>'REAL' AND rule_id IN (` + scnRules + `)`, false},
	{"schedules", `SELECT count(*) FROM maintenance_schedules WHERE source_mode<>'REAL' AND rule_id IN (` + scnRules + `)`, `DELETE FROM maintenance_schedules WHERE source_mode<>'REAL' AND rule_id IN (` + scnRules + `)`, false},
	{"rules", `SELECT count(*) FROM (` + scnRules + `) x`, `DELETE FROM maintenance_rules WHERE id IN (` + scnRules + `)`, false},
	{"baselines", `SELECT count(*) FROM equipment_meter_baselines WHERE source_mode<>'REAL' AND meter_id IN (` + lhkMeters + `)`, `DELETE FROM equipment_meter_baselines WHERE source_mode<>'REAL' AND meter_id IN (` + lhkMeters + `)`, false},
	{"meter_counter_events", `SELECT count(*) FROM meter_counter_events WHERE source_mode<>'REAL' AND meter_id IN (` + lhkMeters + `)`, `DELETE FROM meter_counter_events WHERE source_mode<>'REAL' AND meter_id IN (` + lhkMeters + `)`, true},
	{"meter_readings", `SELECT count(*) FROM meter_readings WHERE source_mode<>'REAL' AND runtime_device_id='` + RuntimeID + `' AND meter_id IN (` + lhkMeters + `)`, `DELETE FROM meter_readings WHERE source_mode<>'REAL' AND runtime_device_id='` + RuntimeID + `' AND meter_id IN (` + lhkMeters + `)`, true},
	{"daily_values", `SELECT count(*) FROM daily_metric_values WHERE source_mode<>'REAL' AND meter_id IN (` + lhkMeters + `)`, `DELETE FROM daily_metric_values WHERE source_mode<>'REAL' AND meter_id IN (` + lhkMeters + `)`, true},
	{"meter_states", `SELECT count(*) FROM meter_states WHERE source_mode<>'REAL' AND runtime_device_id='` + RuntimeID + `' AND meter_id IN (` + lhkMeters + `)`, `DELETE FROM meter_states WHERE source_mode<>'REAL' AND runtime_device_id='` + RuntimeID + `' AND meter_id IN (` + lhkMeters + `)`, true},
	{"fault_events", `SELECT count(*) FROM fault_events WHERE asset_id='` + AssetID + `' AND source_mode<>'REAL' AND runtime_device_id='` + RuntimeID + `'`, `DELETE FROM fault_events WHERE asset_id='` + AssetID + `' AND source_mode<>'REAL' AND runtime_device_id='` + RuntimeID + `'`, true},
}

func PlanCleanup(ctx context.Context, q queryer, includeMeterData bool) (CleanupPlan, error) {
	p := CleanupPlan{IncludeMeterData: includeMeterData, Counts: map[string]int{}}
	var err error
	if p.Target, err = target(ctx, q); err != nil {
		return p, err
	}
	for _, s := range cleanupSteps {
		if s.meter && !includeMeterData {
			continue
		}
		var n int
		if err = q.QueryRowContext(ctx, s.count).Scan(&n); err != nil {
			return p, fmt.Errorf("%s: %w", s.key, err)
		}
		p.Counts[s.key] = n
	}
	keys := make([]string, 0, len(p.Counts))
	for k := range p.Counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make([]string, 0, len(keys))
	for _, k := range keys {
		ordered = append(ordered, fmt.Sprintf("%s=%d", k, p.Counts[k]))
	}
	p.Digest = digest(struct {
		T Target
		M bool
		C []string
	}{p.Target, includeMeterData, ordered})
	return p, nil
}

// ApplyCleanup deletes exactly the planned scope in one transaction (re-planned inside it; the
// approved digest must still match). The audit row records the counts; history of other assets,
// REAL data and the imported ledger are untouched.
func ApplyCleanup(ctx context.Context, db *sql.DB, includeMeterData bool, approved, actor, requestID string) (CleanupPlan, error) {
	if strings.TrimSpace(actor) == "" {
		return CleanupPlan{}, ErrActorRequired
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return CleanupPlan{}, err
	}
	defer tx.Rollback()
	p, err := PlanCleanup(ctx, tx, includeMeterData)
	if err != nil {
		return p, err
	}
	if p.Digest != approved {
		return p, ErrDigestMismatch
	}
	for _, s := range cleanupSteps {
		if s.meter && !includeMeterData {
			continue
		}
		if _, err = tx.ExecContext(ctx, s.del); err != nil {
			return p, fmt.Errorf("%s: %w", s.key, err)
		}
	}
	if err = auditlog.Write(ctx, tx, auditlog.Event{Actor: "host:" + actor, Project: ProjectID, Action: "lhk_sim_scenario_cleaned", Result: "success", RequestID: requestID,
		Change: map[string]any{"counts": p.Counts, "includeMeterData": includeMeterData, "digest": approved}}); err != nil {
		return p, err
	}
	return p, tx.Commit()
}

// NewRequestID is exposed for the tool.
func NewRequestID() string { return platform.ID() }
