package metering

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// ---------------------------------------------------------------------------------------------
// Read models for the asset detail tabs and the maintenance & due center (B6). Every list carries
// sourceMode so the UI can mark SIMULATION / TEST loudly; REAL views never include them.
// ---------------------------------------------------------------------------------------------

type PointView struct {
	ID                 string   `json:"id"`
	Code               string   `json:"pointCode"`
	Name               string   `json:"name"`
	Mechanism          string   `json:"mechanism"`
	Slot               *string  `json:"equipmentSlot"`
	EquipmentID        *string  `json:"equipmentInstallationId"`
	EquipmentCode      *string  `json:"equipmentCode"`
	TypeCode           *string  `json:"typeCode"`
	PLCAddressRaw      *string  `json:"plcAddressRaw"`
	AddressKind        string   `json:"addressKind"`
	RawDataType        string   `json:"rawDataType"`
	DerivedType        string   `json:"derivedType"`
	UnitRaw            *string  `json:"unitRaw"`
	Unit               string   `json:"unit"`
	ActiveLevel        *string  `json:"activeLevel"`
	Edge               *string  `json:"edge"`
	AcquisitionSource  string   `json:"acquisitionSource"`
	ConfirmationStatus string   `json:"confirmationStatus"`
	Quality            string   `json:"quality"`
	Version            int64    `json:"version"`
	SourceRow          int      `json:"sourceRow"`
	BrandModel         *string  `json:"brandModel"`
	LifeText           *string  `json:"lifeText"`
	ReviewFlags        []string `json:"reviewFlags"`
	OpenIssues         int      `json:"openIssues"`
	Meters             []string `json:"meters"`
}

// Points lists the raw signal points of an asset with their equipment, source row and issues.
func Points(ctx context.Context, db *sql.DB, assetID string) ([]PointView, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.id,p.point_code,p.name,p.mechanism,p.equipment_slot,e.id,e.equipment_code,e.type_code,p.plc_address_raw,p.address_kind,p.raw_data_type,p.derived_type,p.unit_raw,p.unit,
p.active_level,p.edge,p.acquisition_source,p.confirmation_status,p.quality,p.version,s.source_row,s.raw->>'brand_model',s.raw->>'life_text',array_to_json(s.review_flags)::text,
(SELECT count(*) FROM point_review_issues i WHERE i.point_id=p.id AND i.status='OPEN'),
COALESCE((SELECT json_agg(d.meter_code ORDER BY d.meter_code) FROM meter_definitions d WHERE d.point_id=p.id),'[]')::text
FROM telemetry_points p JOIN onboarding_source_rows s ON s.id=p.source_row_id
LEFT JOIN asset_equipment_installations e ON e.asset_id=p.asset_id AND e.semantic_slot=p.equipment_slot AND e.valid_to IS NULL
WHERE p.asset_id=$1 ORDER BY s.source_index`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PointView{}
	for rows.Next() {
		var v PointView
		var flags, meters string
		if err = rows.Scan(&v.ID, &v.Code, &v.Name, &v.Mechanism, &v.Slot, &v.EquipmentID, &v.EquipmentCode, &v.TypeCode, &v.PLCAddressRaw, &v.AddressKind, &v.RawDataType, &v.DerivedType, &v.UnitRaw, &v.Unit,
			&v.ActiveLevel, &v.Edge, &v.AcquisitionSource, &v.ConfirmationStatus, &v.Quality, &v.Version, &v.SourceRow, &v.BrandModel, &v.LifeText, &flags, &v.OpenIssues, &meters); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(flags), &v.ReviewFlags)
		_ = json.Unmarshal([]byte(meters), &v.Meters)
		if v.ReviewFlags == nil {
			v.ReviewFlags = []string{}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type IssueView struct {
	ID         string          `json:"id"`
	Code       string          `json:"issueCode"`
	PointCode  *string         `json:"pointCode"`
	SourceRow  *int            `json:"sourceRow"`
	Detail     json.RawMessage `json:"detail"`
	Status     string          `json:"status"`
	ResolvedBy *string         `json:"resolvedBy"`
	Resolution *string         `json:"resolution"`
	ChangeID   *string         `json:"configChangeId"`
}

func Issues(ctx context.Context, db *sql.DB, assetID string) ([]IssueView, error) {
	rows, err := db.QueryContext(ctx, `SELECT i.id,i.issue_code,p.point_code,s.source_row,i.detail::text,i.status,i.resolved_by,i.resolution,i.config_change_id
FROM point_review_issues i LEFT JOIN telemetry_points p ON p.id=i.point_id LEFT JOIN onboarding_source_rows s ON s.id=i.source_row_id
WHERE i.asset_id=$1 ORDER BY (i.status='OPEN') DESC, i.issue_code, s.source_row NULLS FIRST`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IssueView{}
	for rows.Next() {
		var v IssueView
		var d string
		if err = rows.Scan(&v.ID, &v.Code, &v.PointCode, &v.SourceRow, &d, &v.Status, &v.ResolvedBy, &v.Resolution, &v.ChangeID); err != nil {
			return nil, err
		}
		v.Detail = json.RawMessage(d)
		out = append(out, v)
	}
	return out, rows.Err()
}

type PendingView struct {
	ID             string          `json:"id"`
	Subject        string          `json:"subject"`
	Attribute      string          `json:"attribute"`
	Candidate      json.RawMessage `json:"candidate"`
	Basis          string          `json:"basis"`
	Status         string          `json:"status"`
	Version        int64           `json:"version"`
	ConfirmedValue json.RawMessage `json:"confirmedValue"`
	DecidedBy      *string         `json:"decidedBy"`
	DecidedAt      *time.Time      `json:"decidedAt"`
	DecisionBasis  *string         `json:"decisionBasis"`
}

func Pending(ctx context.Context, db *sql.DB, assetID string) ([]PendingView, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,subject,attribute,COALESCE(candidate::text,'null'),basis,status,version,COALESCE(confirmed_value::text,'null'),decided_by,decided_at,decision_basis
FROM asset_pending_attributes WHERE asset_id=$1 ORDER BY subject='asset' DESC, subject, attribute`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingView{}
	for rows.Next() {
		var v PendingView
		var c, cv string
		if err = rows.Scan(&v.ID, &v.Subject, &v.Attribute, &c, &v.Basis, &v.Status, &v.Version, &cv, &v.DecidedBy, &v.DecidedAt, &v.DecisionBasis); err != nil {
			return nil, err
		}
		v.Candidate, v.ConfirmedValue = json.RawMessage(c), json.RawMessage(cv)
		out = append(out, v)
	}
	return out, rows.Err()
}

type MeterView struct {
	ID            string     `json:"id"`
	Code          string     `json:"meterCode"`
	PointCode     string     `json:"pointCode"`
	Metric        string     `json:"metric"`
	Kind          string     `json:"kind"`
	Unit          string     `json:"unit"`
	Scope         string     `json:"scope"`
	Slot          *string    `json:"equipmentSlot"`
	Formula       string     `json:"formula"`
	Status        string     `json:"status"`
	SourceMode    *string    `json:"sourceMode"`
	Runtime       *string    `json:"runtimeDeviceId"`
	LastRaw       *float64   `json:"lastRawValue"`
	Accumulated   *float64   `json:"accumulated"`
	Usage         *float64   `json:"equipmentUsage"` // accumulated - baseline (+ initial); nil = 数据不足
	LastSampledAt *time.Time `json:"lastSampledAt"`
	LastQuality   *string    `json:"lastQuality"`
	Readings      int64      `json:"readings"`
	Today         *float64   `json:"today"`
	TrustState    *string    `json:"trustState"`      // OK | DECREASE_PENDING (nil = no data)
	PendingRaw    *float64   `json:"pendingRawValue"` // latest raw value below the trusted mark
	PendingSince  *time.Time `json:"pendingSince"`
	BaselineKnown bool       `json:"baselineKnown"` // initial usage of the current instance recorded (not NULL)
}

// Meters lists every meter with its state per source mode (one row per meter × mode present;
// meters without data appear once with nil state).
func Meters(ctx context.Context, db *sql.DB, assetID string) ([]MeterView, error) {
	rows, err := db.QueryContext(ctx, `SELECT d.id,d.meter_code,p.point_code,d.metric,d.meter_kind,d.unit,d.scope,d.equipment_slot,d.formula,d.status,
s.source_mode,s.runtime_device_id,s.last_raw_value,s.accumulated,
(SELECT s.accumulated - b.baseline_accumulated + b.initial_usage FROM equipment_meter_baselines b JOIN asset_equipment_installations e ON e.id=b.equipment_installation_id
  WHERE b.meter_id=d.id AND b.source_mode=s.source_mode AND e.semantic_slot=d.equipment_slot AND e.asset_id=d.asset_id AND e.valid_to IS NULL ORDER BY b.recorded_at DESC LIMIT 1),
s.last_sampled_at,s.last_quality,COALESCE(s.readings,0),
(SELECT v.value FROM daily_metric_values v WHERE v.meter_id=d.id AND v.source_mode=s.source_mode ORDER BY v.day DESC LIMIT 1),
s.trust_state, s.pending_last_raw, s.pending_since,
COALESCE((SELECT b.initial_usage IS NOT NULL FROM equipment_meter_baselines b JOIN asset_equipment_installations e ON e.id=b.equipment_installation_id
  WHERE b.meter_id=d.id AND b.source_mode=s.source_mode AND e.semantic_slot=d.equipment_slot AND e.asset_id=d.asset_id AND e.valid_to IS NULL ORDER BY b.recorded_at DESC LIMIT 1), false)
FROM meter_definitions d JOIN telemetry_points p ON p.id=d.point_id LEFT JOIN meter_states s ON s.meter_id=d.id
WHERE d.asset_id=$1 ORDER BY p.point_code, d.metric, s.source_mode`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MeterView{}
	for rows.Next() {
		var v MeterView
		if err = rows.Scan(&v.ID, &v.Code, &v.PointCode, &v.Metric, &v.Kind, &v.Unit, &v.Scope, &v.Slot, &v.Formula, &v.Status, &v.SourceMode, &v.Runtime, &v.LastRaw, &v.Accumulated, &v.Usage,
			&v.LastSampledAt, &v.LastQuality, &v.Readings, &v.Today, &v.TrustState, &v.PendingRaw, &v.PendingSince, &v.BaselineKnown); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type ReadingView struct {
	SampledAt   time.Time `json:"sampledAt"`
	SourceMode  string    `json:"sourceMode"`
	RawValue    *float64  `json:"rawValue"`
	Quality     string    `json:"quality"`
	Delta       float64   `json:"delta"`
	Accumulated float64   `json:"accumulated"`
	Anomaly     *string   `json:"anomaly"`
	BootID      string    `json:"bootId"`
	Sequence    int64     `json:"sequence"`
}

func Readings(ctx context.Context, db *sql.DB, meterID string, limit int) ([]ReadingView, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := db.QueryContext(ctx, `SELECT sampled_at,source_mode,raw_value,quality,delta,accumulated,anomaly,boot_id,sequence FROM meter_readings WHERE meter_id=$1 ORDER BY sampled_at DESC, id DESC LIMIT $2`, meterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReadingView{}
	for rows.Next() {
		var v ReadingView
		if err = rows.Scan(&v.SampledAt, &v.SourceMode, &v.RawValue, &v.Quality, &v.Delta, &v.Accumulated, &v.Anomaly, &v.BootID, &v.Sequence); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type RuleView struct {
	ID              string     `json:"id"`
	Code            string     `json:"ruleCode"`
	Version         int        `json:"version"`
	Name            string     `json:"name"`
	Kind            string     `json:"kind"`
	Type            string     `json:"type"`
	Slot            *string    `json:"equipmentSlot"`
	MeterCode       *string    `json:"meterCode"`
	IntervalDays    *int       `json:"intervalDays"`
	IntervalMeter   *float64   `json:"intervalMeter"`
	LeadDays        []int      `json:"leadDays"`
	WarnPercents    []int      `json:"warnPercents"`
	Basis           string     `json:"basis"`
	ApprovalStatus  string     `json:"approvalStatus"`
	ApprovedBy      *string    `json:"approvedBy"`
	EffectiveFrom   *time.Time `json:"effectiveFrom"`
	TestOnly        bool       `json:"testOnly"`
	SourceMode      *string    `json:"sourceMode"`
	CycleNo         *int       `json:"cycleNo"`
	Status          *string    `json:"status"`
	NextDueAt       *time.Time `json:"nextDueAt"`
	NextDueMeter    *float64   `json:"nextDueMeter"`
	ProgressPercent *float64   `json:"progressPercent"`
	DaysLeft        *int       `json:"daysLeft"`
	EvaluatedAt     *time.Time `json:"evaluatedAt"`
	DataGaps        []string   `json:"dataGaps"` // TIME / METER conditions that could not be computed
	Reason          *string    `json:"reason"`
	SourceRow       *int       `json:"sourceRow"`
	ApprovedAt      *time.Time `json:"approvedAt"`
}

func ruleQuery(where string) string {
	return `SELECT r.id,r.rule_code,r.version,r.name,r.rule_kind,r.rule_type,r.equipment_slot,d.meter_code,r.interval_days,r.interval_meter,array_to_json(r.lead_days)::text,array_to_json(r.warn_percents)::text,
r.basis,r.approval_status,r.approved_by,r.effective_from,r.test_only,s.source_mode,s.cycle_no,s.status,s.next_due_at,s.next_due_meter,s.progress_percent,s.days_left,s.evaluated_at,
COALESCE(array_to_json(s.data_gaps)::text,'[]'),s.reason,(SELECT o.source_row FROM onboarding_source_rows o WHERE o.id=r.source_row_id),r.approved_at
FROM maintenance_rules r LEFT JOIN meter_definitions d ON d.id=r.meter_id LEFT JOIN maintenance_schedules s ON s.rule_id=r.id
WHERE r.approval_status<>'RETIRED' AND ` + where
}

func scanRules(rows *sql.Rows) ([]RuleView, error) {
	defer rows.Close()
	out := []RuleView{}
	for rows.Next() {
		var v RuleView
		var lead, warn, gaps string
		if err := rows.Scan(&v.ID, &v.Code, &v.Version, &v.Name, &v.Kind, &v.Type, &v.Slot, &v.MeterCode, &v.IntervalDays, &v.IntervalMeter, &lead, &warn, &v.Basis, &v.ApprovalStatus, &v.ApprovedBy, &v.EffectiveFrom, &v.TestOnly,
			&v.SourceMode, &v.CycleNo, &v.Status, &v.NextDueAt, &v.NextDueMeter, &v.ProgressPercent, &v.DaysLeft, &v.EvaluatedAt, &gaps, &v.Reason, &v.SourceRow, &v.ApprovedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(gaps), &v.DataGaps)
		if v.DataGaps == nil {
			v.DataGaps = []string{}
		}
		_ = json.Unmarshal([]byte(lead), &v.LeadDays)
		_ = json.Unmarshal([]byte(warn), &v.WarnPercents)
		out = append(out, v)
	}
	return out, rows.Err()
}

func Rules(ctx context.Context, db *sql.DB, assetID string) ([]RuleView, error) {
	rows, err := db.QueryContext(ctx, ruleQuery(`r.asset_id=$1 ORDER BY r.test_only DESC, r.rule_code, s.source_mode`), assetID)
	if err != nil {
		return nil, err
	}
	return scanRules(rows)
}

type AlertView struct {
	ID          string          `json:"id"`
	RuleID      string          `json:"ruleId"`
	RuleName    string          `json:"ruleName"`
	AssetID     string          `json:"assetId"`
	SourceMode  string          `json:"sourceMode"`
	CycleNo     int             `json:"cycleNo"`
	Stage       string          `json:"stage"`
	TriggeredAt time.Time       `json:"triggeredAt"`
	Status      string          `json:"status"`
	WorkOrderID *string         `json:"workOrderId"`
	Detail      json.RawMessage `json:"detail"`
	Slot        *string         `json:"equipmentSlot"` // R9-UX: additive, for the device name
	SlotName    *string         `json:"equipmentName"`
	Mechanism   *string         `json:"mechanism"`
}

func Alerts(ctx context.Context, db *sql.DB, assetIDs []string, mode string) ([]AlertView, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.id,a.rule_id,r.name,a.asset_id,a.source_mode,a.cycle_no,a.stage,a.triggered_at,a.status,a.work_order_id,a.detail::text,r.equipment_slot,tp.name,tp.mechanism
FROM maintenance_alerts a JOIN maintenance_rules r ON r.id=a.rule_id
LEFT JOIN LATERAL (SELECT name, mechanism FROM telemetry_points t WHERE t.asset_id=r.asset_id AND t.equipment_slot=r.equipment_slot ORDER BY t.point_code LIMIT 1) tp ON true WHERE a.asset_id = ANY($1) AND ($2='' OR a.source_mode=$2) ORDER BY a.triggered_at DESC, a.stage LIMIT 500`, assetIDs, mode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertView{}
	for rows.Next() {
		var v AlertView
		var d string
		if err = rows.Scan(&v.ID, &v.RuleID, &v.RuleName, &v.AssetID, &v.SourceMode, &v.CycleNo, &v.Stage, &v.TriggeredAt, &v.Status, &v.WorkOrderID, &d, &v.Slot, &v.SlotName, &v.Mechanism); err != nil {
			return nil, err
		}
		v.Detail = json.RawMessage(d)
		out = append(out, v)
	}
	return out, rows.Err()
}

func WorkOrders(ctx context.Context, db *sql.DB, assetIDs []string, mode, status string) ([]WorkOrder, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+woColumns+` FROM maintenance_work_orders WHERE asset_id = ANY($1) AND ($2='' OR source_mode=$2) AND ($3='' OR status=$3) ORDER BY created_at DESC LIMIT 500`, assetIDs, mode, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkOrder{}
	for rows.Next() {
		w, err := scanWO(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// HistoryEntry is one timeline item of the asset (B6 历史): ledger revisions, replacements,
// work order transitions, rope inspections, fault events.
type HistoryEntry struct {
	At         time.Time `json:"at"`
	Category   string    `json:"category"`
	SourceMode string    `json:"sourceMode"`
	Actor      string    `json:"actor"`
	Summary    string    `json:"summary"`
	Reference  string    `json:"reference"`
}

func History(ctx context.Context, db *sql.DB, assetID string, limit int) ([]HistoryEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := db.QueryContext(ctx, `SELECT * FROM (
 SELECT recorded_at, 'LEDGER', 'REAL', COALESCE(changed_by,''), entity_type||' '||array_to_string(changed_fields,','), entity_id FROM entity_revisions WHERE asset_id=$1
 UNION ALL SELECT replaced_at, 'REPLACEMENT', source_mode, replaced_by, equipment_slot||'：'||reason, work_order_id FROM equipment_replacements WHERE asset_id=$1
 UNION ALL SELECT t.occurred_at, 'WORK_ORDER', w.source_mode, t.actor, w.code||' '||t.from_status||'→'||t.to_status||CASE WHEN t.note<>'' THEN '（'||t.note||'）' ELSE '' END, w.id FROM work_order_transitions t JOIN maintenance_work_orders w ON w.id=t.work_order_id WHERE w.asset_id=$1
 UNION ALL SELECT inspected_at, 'WIRE_ROPE', source_mode, inspector, inspection_type||' '||conclusion||CASE WHEN finding<>'' THEN '：'||finding ELSE '' END, id FROM wire_rope_inspections WHERE asset_id=$1
 UNION ALL SELECT occurred_at, 'FAULT', source_mode, runtime_device_id, code||' '||text, event_id FROM fault_events WHERE asset_id=$1
) h ORDER BY 1 DESC LIMIT $2`, assetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var v HistoryEntry
		if err = rows.Scan(&v.At, &v.Category, &v.SourceMode, &v.Actor, &v.Summary, &v.Reference); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CenterRow is one row of the maintenance & due center: a rule schedule of an asset plus the
// statutory inspection due date of the asset itself (INSPECTION_DUE from asset_devices).
type CenterRow struct {
	ProjectID       string     `json:"projectId"`
	ProjectName     string     `json:"projectName"`
	AssetID         string     `json:"assetId"`
	AssetName       string     `json:"assetName"`
	AssetCode       *string    `json:"assetCode"`
	Mechanism       *string    `json:"mechanism"`
	Slot            *string    `json:"equipmentSlot"`
	EquipmentName   *string    `json:"equipmentName"` // R9-UX: the device name of the slot (source list), additive
	EquipmentType   *string    `json:"equipmentType"`
	Category        string     `json:"category"` // MAINTENANCE_DUE | CALIBRATION_DUE | INSPECTION_DUE | REPLACEMENT_DUE | STATUTORY_INSPECTION
	RuleID          *string    `json:"ruleId"`
	RuleName        string     `json:"ruleName"`
	ApprovalStatus  string     `json:"approvalStatus"`
	SourceMode      string     `json:"sourceMode"`
	Status          string     `json:"status"`
	NextDueAt       *time.Time `json:"nextDueAt"`
	ProgressPercent *float64   `json:"progressPercent"`
	DaysLeft        *int       `json:"daysLeft"`
	OpenWorkOrders  int        `json:"openWorkOrders"`
}

// Center returns the due rows of the given (authorized) projects.
func Center(ctx context.Context, db *sql.DB, projects []string, now time.Time) ([]CenterRow, error) {
	rows, err := db.QueryContext(ctx, `
SELECT a.project_id, p.name, a.id, a.display_name, a.asset_code, tp.mechanism, r.equipment_slot, tp.name, e.type_code,
  CASE r.rule_kind WHEN 'CALIBRATION' THEN 'CALIBRATION_DUE' WHEN 'INSPECTION' THEN 'INSPECTION_DUE' WHEN 'REPLACEMENT' THEN 'REPLACEMENT_DUE' ELSE 'MAINTENANCE_DUE' END,
  r.id, r.name, r.approval_status, COALESCE(s.source_mode, CASE WHEN r.test_only THEN 'SIMULATION' ELSE 'REAL' END),
  COALESCE(s.status, CASE WHEN r.approval_status='APPROVED' THEN 'INSUFFICIENT_DATA' ELSE 'DRAFT' END),
  s.next_due_at, s.progress_percent, s.days_left,
  (SELECT count(*) FROM maintenance_work_orders w WHERE w.rule_id=r.id AND w.source_mode=COALESCE(s.source_mode,'REAL') AND w.status IN ('OPEN','ASSIGNED','IN_PROGRESS'))
FROM maintenance_rules r JOIN asset_devices a ON a.id=r.asset_id JOIN projects p ON p.id=a.project_id
LEFT JOIN maintenance_schedules s ON s.rule_id=r.id
LEFT JOIN asset_equipment_installations e ON e.asset_id=r.asset_id AND e.semantic_slot=r.equipment_slot AND e.valid_to IS NULL
LEFT JOIN LATERAL (SELECT mechanism, name FROM telemetry_points t WHERE t.asset_id=r.asset_id AND t.equipment_slot=r.equipment_slot ORDER BY t.point_code LIMIT 1) tp ON true
WHERE a.project_id = ANY($1) AND r.approval_status<>'RETIRED'
UNION ALL
SELECT a.project_id, p.name, a.id, a.display_name, a.asset_code, NULL, NULL, NULL, NULL, 'STATUTORY_INSPECTION', NULL, '法定定期检验', 'SOURCE',
  'REAL', CASE WHEN a.inspection_valid_until IS NULL THEN 'INSUFFICIENT_DATA' WHEN a.inspection_valid_until < $2::date THEN 'OVERDUE' WHEN a.inspection_valid_until = $2::date THEN 'DUE'
   WHEN a.inspection_valid_until <= $2::date + 30 THEN 'DUE_SOON' ELSE 'NORMAL' END,
  a.inspection_valid_until::timestamptz, NULL, (a.inspection_valid_until - $2::date), 0
FROM asset_devices a JOIN projects p ON p.id=a.project_id WHERE a.project_id = ANY($1) AND a.asset_status<>'RETIRED'
ORDER BY 1, 3`, projects, now.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CenterRow{}
	for rows.Next() {
		var v CenterRow
		if err = rows.Scan(&v.ProjectID, &v.ProjectName, &v.AssetID, &v.AssetName, &v.AssetCode, &v.Mechanism, &v.Slot, &v.EquipmentName, &v.EquipmentType, &v.Category, &v.RuleID, &v.RuleName, &v.ApprovalStatus, &v.SourceMode, &v.Status,
			&v.NextDueAt, &v.ProgressPercent, &v.DaysLeft, &v.OpenWorkOrders); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// AttentionCounts are the REAL maintenance counters of the home "需要关注" panel. Simulation and
// test rows never contribute; `simulation*` fields are reported separately so the UI can show them
// marked as simulated without mixing them in.
type AttentionCounts struct {
	MaintenanceDue      int `json:"maintenanceDue"`     // REAL rule schedules DUE / OVERDUE
	MaintenanceDueSoon  int `json:"maintenanceDueSoon"` // REAL rule schedules DUE_SOON
	InspectionDue       int `json:"inspectionDue"`      // statutory inspection due within 30 days or overdue
	InspectionUnknown   int `json:"inspectionUnknown"`  // assets without an inspection-valid-until date
	OpenWorkOrders      int `json:"openWorkOrders"`     // REAL
	InsufficientData    int `json:"insufficientData"`   // approved REAL rules that cannot be computed
	DraftRules          int `json:"draftRules"`         // unapproved rules awaiting review
	SimulationDue       int `json:"simulationDue"`      // SIMULATION / TEST schedules DUE / OVERDUE (shown marked)
	SimulationWorkOrder int `json:"simulationWorkOrders"`
}

func Attention(ctx context.Context, db *sql.DB, projects []string, now time.Time) (AttentionCounts, error) {
	var c AttentionCounts
	err := db.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM maintenance_schedules s JOIN maintenance_rules r ON r.id=s.rule_id JOIN asset_devices a ON a.id=r.asset_id WHERE a.project_id=ANY($1) AND s.source_mode='REAL' AND s.status IN ('DUE','OVERDUE')),
 (SELECT count(*) FROM maintenance_schedules s JOIN maintenance_rules r ON r.id=s.rule_id JOIN asset_devices a ON a.id=r.asset_id WHERE a.project_id=ANY($1) AND s.source_mode='REAL' AND s.status='DUE_SOON'),
 (SELECT count(*) FROM asset_devices a WHERE a.project_id=ANY($1) AND a.asset_status<>'RETIRED' AND a.inspection_valid_until <= $2::date + 30),
 (SELECT count(*) FROM asset_devices a WHERE a.project_id=ANY($1) AND a.asset_status<>'RETIRED' AND a.inspection_valid_until IS NULL),
 (SELECT count(*) FROM maintenance_work_orders w WHERE w.project_id=ANY($1) AND w.source_mode='REAL' AND w.status IN ('OPEN','ASSIGNED','IN_PROGRESS')),
 (SELECT count(*) FROM maintenance_schedules s JOIN maintenance_rules r ON r.id=s.rule_id JOIN asset_devices a ON a.id=r.asset_id WHERE a.project_id=ANY($1) AND s.source_mode='REAL' AND s.status='INSUFFICIENT_DATA'),
 (SELECT count(*) FROM maintenance_rules r JOIN asset_devices a ON a.id=r.asset_id WHERE a.project_id=ANY($1) AND r.approval_status='DRAFT_UNAPPROVED' AND NOT r.test_only),
 (SELECT count(*) FROM maintenance_schedules s JOIN maintenance_rules r ON r.id=s.rule_id JOIN asset_devices a ON a.id=r.asset_id WHERE a.project_id=ANY($1) AND s.source_mode<>'REAL' AND s.status IN ('DUE','OVERDUE')),
 (SELECT count(*) FROM maintenance_work_orders w WHERE w.project_id=ANY($1) AND w.source_mode<>'REAL' AND w.status IN ('OPEN','ASSIGNED','IN_PROGRESS'))`,
		projects, now.Format("2006-01-02")).Scan(&c.MaintenanceDue, &c.MaintenanceDueSoon, &c.InspectionDue, &c.InspectionUnknown, &c.OpenWorkOrders, &c.InsufficientData, &c.DraftRules, &c.SimulationDue, &c.SimulationWorkOrder)
	return c, err
}

// ResolveIssue closes a point review issue with a recorded resolution (never deletes it).
func ResolveIssue(ctx context.Context, db *sql.DB, assetID, issueID, status, resolution, actor string) error {
	if status != "RESOLVED" && status != "ACCEPTED" {
		return ErrInvalid
	}
	if len([]rune(resolution)) < 2 {
		return ErrInvalid
	}
	r, err := db.ExecContext(ctx, `UPDATE point_review_issues SET status=$3, resolved_by=$4, resolved_at=now(), resolution=$5 WHERE id=$1 AND asset_id=$2 AND status='OPEN'`, issueID, assetID, status, actor, resolution)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return ErrConflict
	}
	return nil
}
