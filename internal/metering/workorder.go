package metering

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrInvalid       = errors.New("invalid input")
	ErrConflict      = errors.New("conflict")
	ErrTransition    = errors.New("invalid work order transition")
	ErrModeIsolation = errors.New("simulation / test records cannot change the real ledger")
)

// Work order state machine: OPEN → ASSIGNED → IN_PROGRESS → COMPLETED; CANCELLED from any open state.
var woTransitions = map[string][]string{
	"OPEN":        {"ASSIGNED", "CANCELLED"},
	"ASSIGNED":    {"ASSIGNED", "IN_PROGRESS", "CANCELLED"},
	"IN_PROGRESS": {"COMPLETED", "CANCELLED"},
}

func validTransition(from, to string) bool {
	for _, t := range woTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// WorkOrder is the API view of a maintenance work order.
type WorkOrder struct {
	ID                      string     `json:"id"`
	Code                    string     `json:"code"`
	ProjectID               string     `json:"projectId"`
	AssetID                 string     `json:"assetId"`
	SourceMode              string     `json:"sourceMode"`
	EquipmentInstallationID *string    `json:"equipmentInstallationId"`
	EquipmentSlot           *string    `json:"equipmentSlot"`
	RuleID                  *string    `json:"ruleId"`
	AlertID                 *string    `json:"alertId"`
	CycleNo                 *int       `json:"cycleNo"`
	Type                    string     `json:"type"`
	Priority                string     `json:"priority"`
	Status                  string     `json:"status"`
	Title                   string     `json:"title"`
	Description             string     `json:"description"`
	CreatedBy               string     `json:"createdBy"`
	CreatedAt               time.Time  `json:"createdAt"`
	AssignedTo              *string    `json:"assignedTo"`
	PlannedAt               *time.Time `json:"plannedAt"`
	DueAt                   *time.Time `json:"dueAt"`
	StartedAt               *time.Time `json:"startedAt"`
	CompletedAt             *time.Time `json:"completedAt"`
	CompletedBy             *string    `json:"completedBy"`
	Result                  *string    `json:"result"`
	Basis                   *string    `json:"basis"`
	Notes                   string     `json:"notes"`
	Version                 int64      `json:"version"`
}

const woColumns = `id,code,project_id,asset_id,source_mode,equipment_installation_id,equipment_slot,rule_id,alert_id,cycle_no,wo_type,priority,status,title,description,created_by,created_at,assigned_to,planned_at,due_at,started_at,completed_at,completed_by,result,basis,notes,version`

func scanWO(row interface{ Scan(...any) error }) (WorkOrder, error) {
	var w WorkOrder
	err := row.Scan(&w.ID, &w.Code, &w.ProjectID, &w.AssetID, &w.SourceMode, &w.EquipmentInstallationID, &w.EquipmentSlot, &w.RuleID, &w.AlertID, &w.CycleNo, &w.Type, &w.Priority, &w.Status,
		&w.Title, &w.Description, &w.CreatedBy, &w.CreatedAt, &w.AssignedTo, &w.PlannedAt, &w.DueAt, &w.StartedAt, &w.CompletedAt, &w.CompletedBy, &w.Result, &w.Basis, &w.Notes, &w.Version)
	return w, err
}

// GetWorkOrder loads one work order.
func GetWorkOrder(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (WorkOrder, error) {
	w, err := scanWO(q.QueryRowContext(ctx, `SELECT `+woColumns+` FROM maintenance_work_orders WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	return w, err
}

// CreateFromAlert opens the work order of an alert's rule cycle (one per rule, mode and cycle:
// a second request returns the existing order).
func CreateFromAlert(ctx context.Context, db *sql.DB, alertID, actor, requestID string) (WorkOrder, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return WorkOrder{}, err
	}
	defer tx.Rollback()
	var ruleID, assetID, mode, stage, ruleName, kind, slot, project string
	var cycle int
	var status string
	err = tx.QueryRowContext(ctx, `SELECT a.rule_id,a.asset_id,a.source_mode,a.cycle_no,a.stage,a.status,r.name,r.rule_kind,COALESCE(r.equipment_slot,''),d.project_id
FROM maintenance_alerts a JOIN maintenance_rules r ON r.id=a.rule_id JOIN asset_devices d ON d.id=a.asset_id WHERE a.id=$1 FOR UPDATE OF a`, alertID).
		Scan(&ruleID, &assetID, &mode, &cycle, &stage, &status, &ruleName, &kind, &slot, &project)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkOrder{}, ErrNotFound
	}
	if err != nil {
		return WorkOrder{}, err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM maintenance_work_orders WHERE rule_id=$1 AND source_mode=$2 AND cycle_no=$3 AND status<>'CANCELLED'`, ruleID, mode, cycle).Scan(&existing)
	if err == nil {
		if _, err = tx.ExecContext(ctx, `UPDATE maintenance_alerts SET status='WORK_ORDER', work_order_id=$2 WHERE id=$1 AND status='OPEN'`, alertID, existing); err != nil {
			return WorkOrder{}, err
		}
		if err = tx.Commit(); err != nil {
			return WorkOrder{}, err
		}
		return GetWorkOrder(ctx, db, existing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WorkOrder{}, err
	}
	woType := map[string]string{"MAINTENANCE": "PREVENTIVE", "CALIBRATION": "CALIBRATION", "INSPECTION": "INSPECTION", "REPLACEMENT": "REPLACEMENT"}[kind]
	priority := "MEDIUM"
	if stage == "OVERDUE" || stage == "PCT_100" || stage == "DUE" {
		priority = "HIGH"
	}
	var eqID, slotRef any
	if slot != "" {
		slotRef = slot
		var id string
		if e := tx.QueryRowContext(ctx, `SELECT id FROM asset_equipment_installations WHERE asset_id=$1 AND semantic_slot=$2 AND valid_to IS NULL`, assetID, slot).Scan(&id); e == nil {
			eqID = id
		}
	}
	id := platform.ID()
	code := fmt.Sprintf("WO-%s-%s", time.Now().UTC().Format("20060102"), strings.ToUpper(id[len(id)-6:]))
	if mode != "REAL" {
		code = "SIM-" + code
	}
	title := ruleName
	if mode != "REAL" {
		title = "【模拟】" + ruleName
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO maintenance_work_orders(id,code,project_id,asset_id,source_mode,equipment_installation_id,equipment_slot,rule_id,alert_id,cycle_no,wo_type,priority,title,description,created_by)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, id, code, project, assetID, mode, eqID, slotRef, ruleID, alertID, cycle, woType, priority, title, "由预警 "+stage+" 生成", actor); err != nil {
		return WorkOrder{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE maintenance_alerts SET status='WORK_ORDER', work_order_id=$1 WHERE rule_id=$2 AND source_mode=$3 AND cycle_no=$4 AND status='OPEN'`, id, ruleID, mode, cycle); err != nil {
		return WorkOrder{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_transitions(work_order_id,from_status,to_status,actor,note,request_id) VALUES($1,'','OPEN',$2,$3,$4)`, id, actor, "from alert "+stage, requestID); err != nil {
		return WorkOrder{}, err
	}
	if err = auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: project, Action: "work_order_created", Result: "success:" + mode, RequestID: requestID, Change: map[string]any{"workOrder": code, "rule": ruleID, "stage": stage}}); err != nil {
		return WorkOrder{}, err
	}
	if err = tx.Commit(); err != nil {
		return WorkOrder{}, err
	}
	return GetWorkOrder(ctx, db, id)
}

// Transition is one requested state change.
type Transition struct {
	To         string     `json:"to"`
	AssignedTo string     `json:"assignedTo"`
	Result     string     `json:"result"` // COMPLETED: DONE | DONE_WITH_FINDINGS | NOT_DONE
	Basis      string     `json:"basis"`  // COMPLETED: what was done / on what basis
	Note       string     `json:"note"`
	PlannedAt  *time.Time `json:"plannedAt"`
	// Replacement (COMPLETED of a REPLACEMENT / PREVENTIVE order with a slot): the replaced instance.
	Replacement *ReplacementInput `json:"replacement"`
	// Wire-rope inspection recorded with the completion of an INSPECTION order on a WIRE_ROPE slot.
	RopeInspection *RopeInspectionInput `json:"ropeInspection"`
}

type ReplacementInput struct {
	SerialNumber  string   `json:"serialNumber"`
	CatalogItemID string   `json:"catalogItemId"`
	Reason        string   `json:"reason"`
	InitialUsage  *float64 `json:"initialUsage"` // usage of the new instance before installation (normally 0 / null)
}

type RopeInspectionInput struct {
	InspectionType     string     `json:"inspectionType"`
	MeasuredDiameterMm *float64   `json:"measuredDiameterMm"`
	BrokenWiresVisible *int       `json:"brokenWiresVisible"`
	Wear               string     `json:"wear"`
	Corrosion          string     `json:"corrosion"`
	Deformation        string     `json:"deformation"`
	Lubrication        string     `json:"lubrication"`
	Finding            string     `json:"finding"`
	Conclusion         string     `json:"conclusion"` // the inspector's: CONTINUE | CONTINUE_MONITOR | REPLACE_RECOMMENDED | STOP_PENDING_REVIEW
	NextDueAt          *time.Time `json:"nextDueAt"`
}

// TransitionWorkOrder applies one state change with optimistic version and audit. Completing an
// order closes its rule cycle (the next evaluation anchors the next cycle at completion: the next
// due date / meter is recomputed), records a replacement (REAL: new ledger instance + meter
// baselines; SIMULATION / TEST: never touches the ledger) and a wire-rope inspection.
func TransitionWorkOrder(ctx context.Context, db *sql.DB, id string, version int64, t Transition, actor, requestID string, now time.Time) (WorkOrder, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return WorkOrder{}, err
	}
	defer tx.Rollback()
	w, err := scanWO(tx.QueryRowContext(ctx, `SELECT `+woColumns+` FROM maintenance_work_orders WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	if err != nil {
		return w, err
	}
	if version != 0 && w.Version != version {
		return w, fmt.Errorf("%w: work order changed by someone else", ErrConflict)
	}
	if !validTransition(w.Status, t.To) {
		return w, fmt.Errorf("%w: %s → %s", ErrTransition, w.Status, t.To)
	}
	switch t.To {
	case "ASSIGNED":
		if strings.TrimSpace(t.AssignedTo) == "" {
			return w, fmt.Errorf("%w: assignedTo required", ErrInvalid)
		}
		_, err = tx.ExecContext(ctx, `UPDATE maintenance_work_orders SET status='ASSIGNED', assigned_to=$2, planned_at=COALESCE($3,planned_at), version=version+1, updated_at=now() WHERE id=$1`, id, strings.TrimSpace(t.AssignedTo), t.PlannedAt)
	case "IN_PROGRESS":
		_, err = tx.ExecContext(ctx, `UPDATE maintenance_work_orders SET status='IN_PROGRESS', started_at=$2, version=version+1, updated_at=now() WHERE id=$1`, id, now)
	case "CANCELLED":
		if strings.TrimSpace(t.Note) == "" {
			return w, fmt.Errorf("%w: cancellation reason required", ErrInvalid)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE maintenance_work_orders SET status='CANCELLED', notes=$2, version=version+1, updated_at=now() WHERE id=$1`, id, t.Note); err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE maintenance_alerts SET status='OPEN', work_order_id=NULL WHERE work_order_id=$1`, id)
		}
	case "COMPLETED":
		if t.Result != "DONE" && t.Result != "DONE_WITH_FINDINGS" && t.Result != "NOT_DONE" {
			return w, fmt.Errorf("%w: result DONE | DONE_WITH_FINDINGS | NOT_DONE required", ErrInvalid)
		}
		if strings.TrimSpace(t.Basis) == "" {
			return w, fmt.Errorf("%w: basis (what was done) required", ErrInvalid)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE maintenance_work_orders SET status='COMPLETED', completed_at=$2, completed_by=$3, result=$4, basis=$5, notes=$6, version=version+1, updated_at=now() WHERE id=$1`,
			id, now, actor, t.Result, t.Basis, t.Note); err != nil {
			return w, err
		}
		if t.Result != "NOT_DONE" {
			if _, err = tx.ExecContext(ctx, `UPDATE maintenance_alerts SET status='CLOSED', closed_at=$2 WHERE work_order_id=$1`, id, now); err != nil {
				return w, err
			}
			// Next cycle anchor: the meter accumulated at completion.
			if w.RuleID != nil {
				if _, err = tx.ExecContext(ctx, `UPDATE maintenance_schedules s SET anchor_meter=(SELECT ms.accumulated FROM maintenance_rules r JOIN meter_states ms ON ms.meter_id=r.meter_id AND ms.source_mode=s.source_mode WHERE r.id=s.rule_id)
WHERE s.rule_id=$1 AND s.source_mode=$2`, *w.RuleID, w.SourceMode); err != nil {
					return w, err
				}
			}
		}
		if t.Replacement != nil {
			if err = replace(ctx, tx, w, *t.Replacement, actor, requestID, now); err != nil {
				return w, err
			}
		}
		if t.RopeInspection != nil {
			if err = ropeInspection(ctx, tx, w, *t.RopeInspection, actor, now); err != nil {
				return w, err
			}
		}
	}
	if err != nil {
		return w, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_transitions(work_order_id,from_status,to_status,actor,note,request_id) VALUES($1,$2,$3,$4,$5,$6)`, id, w.Status, t.To, actor, t.Note, requestID); err != nil {
		return w, err
	}
	if err = auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: w.ProjectID, Action: "work_order_transition", Result: w.Status + ":" + t.To, RequestID: requestID, Change: map[string]any{"workOrder": w.Code, "mode": w.SourceMode}}); err != nil {
		return w, err
	}
	if err = tx.Commit(); err != nil {
		return w, err
	}
	return GetWorkOrder(ctx, db, id)
}

// replace records a component replacement. REAL: the old installation ends, a new installation
// (same slot, new equipment code) starts, and every meter of the slot gets a REPLACEMENT baseline
// at the current accumulated value — the new instance's usage starts from there, the raw cumulative
// meter is never reset or overwritten. SIMULATION / TEST: only the replacement record (with the
// simulated instance) and simulated baselines are written; the real ledger is untouched.
func replace(ctx context.Context, tx *sql.Tx, w WorkOrder, in ReplacementInput, actor, requestID string, now time.Time) error {
	if w.EquipmentSlot == nil || w.EquipmentInstallationID == nil {
		return fmt.Errorf("%w: replacement needs an equipment slot", ErrInvalid)
	}
	if strings.TrimSpace(in.Reason) == "" {
		return fmt.Errorf("%w: replacement reason required", ErrInvalid)
	}
	old := *w.EquipmentInstallationID
	var newID any
	var simulated any
	if w.SourceMode == "REAL" {
		var code, typeCode, crit string
		var cat sql.NullString
		var gen int
		if err := tx.QueryRowContext(ctx, `SELECT equipment_code,type_code,catalog_item_id,criticality,(SELECT count(*) FROM asset_equipment_installations WHERE asset_id=$2 AND semantic_slot=$3) FROM asset_equipment_installations WHERE id=$1 AND valid_to IS NULL FOR UPDATE`,
			old, w.AssetID, *w.EquipmentSlot).Scan(&code, &typeCode, &cat, &crit, &gen); err != nil {
			return fmt.Errorf("%w: the slot's current instance changed", ErrConflict)
		}
		catalog := any(nil)
		if in.CatalogItemID != "" {
			catalog = in.CatalogItemID
		} else if cat.Valid {
			catalog = cat.String
		}
		id := platform.ID()
		newID = id
		base := code
		if i := strings.LastIndex(code, "#"); i > 0 {
			base = code[:i]
		}
		if _, err := tx.ExecContext(ctx, `UPDATE asset_equipment_installations SET valid_to=$2, removed_at=$2, administrative_state='OUT_OF_SERVICE', version=version+1, updated_at=now() WHERE id=$1`, old, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO asset_equipment_installations(id,asset_id,equipment_code,type_code,catalog_item_id,serial_number,semantic_slot,position_label,criticality,administrative_state,valid_from,installed_at,notes)
SELECT $1,asset_id,$2,type_code,$3,NULLIF($4,''),semantic_slot,position_label,criticality,'IN_SERVICE',$5,$5,$6 FROM asset_equipment_installations WHERE id=$7`,
			id, fmt.Sprintf("%s#%d", base, gen+1), catalog, in.SerialNumber, now, "更换自 "+code+"（工单 "+w.Code+"）", old); err != nil {
			return err
		}
	} else {
		b, _ := json.Marshal(map[string]any{"serialNumber": in.SerialNumber, "catalogItemId": in.CatalogItemID, "replaces": old})
		simulated = string(b)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO equipment_replacements(id,asset_id,source_mode,equipment_slot,old_equipment_installation_id,new_equipment_installation_id,simulated_instance,work_order_id,replaced_at,replaced_by,reason)
VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10,$11)`, platform.ID(), w.AssetID, w.SourceMode, *w.EquipmentSlot, old, newID, simulated, w.ID, now, actor, in.Reason); err != nil {
		return err
	}
	// New baselines for every meter measuring the slot (current accumulated value of that mode).
	target := old
	if newID != nil {
		target = newID.(string)
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.id, COALESCE(s.accumulated,0) FROM meter_definitions d LEFT JOIN meter_states s ON s.meter_id=d.id AND s.source_mode=$3 WHERE d.asset_id=$1 AND d.equipment_slot=$2`, w.AssetID, *w.EquipmentSlot, w.SourceMode)
	if err != nil {
		return err
	}
	type b struct {
		meter string
		acc   float64
	}
	list := []b{}
	for rows.Next() {
		var x b
		if err = rows.Scan(&x.meter, &x.acc); err != nil {
			rows.Close()
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		if w.SourceMode != "REAL" {
			// Simulated replacement: re-base the simulated instance on the same (old) installation row.
			if _, err = tx.ExecContext(ctx, `INSERT INTO equipment_meter_baselines(id,equipment_installation_id,meter_id,source_mode,baseline_accumulated,initial_usage,baseline_kind,recorded_by,note)
VALUES($1,$2,$3,$4,$5,$6,'REPLACEMENT',$7,$8) ON CONFLICT (equipment_installation_id,meter_id,source_mode) DO UPDATE SET baseline_accumulated=excluded.baseline_accumulated,initial_usage=excluded.initial_usage,baseline_kind='REPLACEMENT',recorded_by=excluded.recorded_by,recorded_at=now(),note=excluded.note`,
				platform.ID(), target, x.meter, w.SourceMode, x.acc, in.InitialUsage, actor, "模拟更换（工单 "+w.Code+"）"); err != nil {
				return err
			}
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO equipment_meter_baselines(id,equipment_installation_id,meter_id,source_mode,baseline_accumulated,initial_usage,baseline_kind,recorded_by,note) VALUES($1,$2,$3,'REAL',$4,$5,'REPLACEMENT',$6,$7)`,
			platform.ID(), target, x.meter, x.acc, in.InitialUsage, actor, "更换（工单 "+w.Code+"）"); err != nil {
			return err
		}
	}
	return auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: w.ProjectID, Action: "equipment_replaced", Result: "success:" + w.SourceMode, RequestID: requestID, Change: map[string]any{"slot": *w.EquipmentSlot, "old": old, "new": newID, "workOrder": w.Code}})
}

func ropeInspection(ctx context.Context, tx *sql.Tx, w WorkOrder, in RopeInspectionInput, actor string, now time.Time) error {
	if w.EquipmentInstallationID == nil {
		return fmt.Errorf("%w: inspection needs the rope installation", ErrInvalid)
	}
	var t string
	if err := tx.QueryRowContext(ctx, `SELECT type_code FROM asset_equipment_installations WHERE id=$1`, *w.EquipmentInstallationID).Scan(&t); err != nil || t != "WIRE_ROPE" {
		return fmt.Errorf("%w: not a wire rope", ErrInvalid)
	}
	switch in.Conclusion {
	case "CONTINUE", "CONTINUE_MONITOR", "REPLACE_RECOMMENDED", "STOP_PENDING_REVIEW":
	default:
		return fmt.Errorf("%w: the inspector's conclusion is required (the system never derives it)", ErrInvalid)
	}
	kind := in.InspectionType
	if kind == "" {
		kind = "PERIODIC"
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO wire_rope_inspections(id,asset_id,equipment_installation_id,source_mode,work_order_id,inspection_type,inspected_at,inspector,measured_diameter_mm,broken_wires_visible,wear,corrosion,deformation,lubrication,finding,conclusion,next_due_at,created_by)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$8)`, platform.ID(), w.AssetID, *w.EquipmentInstallationID, w.SourceMode, w.ID, kind, now, actor,
		in.MeasuredDiameterMm, in.BrokenWiresVisible, in.Wear, in.Corrosion, in.Deformation, in.Lubrication, in.Finding, in.Conclusion, in.NextDueAt)
	return err
}

// ---------------------------------------------------------------------------------------------
// Baselines and test-only threshold fixtures
// ---------------------------------------------------------------------------------------------

// RecordBaseline records the initial usage base of the slot's current instance for one meter
// (e.g. the counter value when the REAL meter started, entered from the site record). Written once.
func RecordBaseline(ctx context.Context, db *sql.DB, assetID, slot, meterCode, mode string, initialUsage *float64, actor, note string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var eqID, meterID string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM asset_equipment_installations WHERE asset_id=$1 AND semantic_slot=$2 AND valid_to IS NULL`, assetID, slot).Scan(&eqID); err != nil {
		return ErrNotFound
	}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM meter_definitions WHERE asset_id=$1 AND meter_code=$2`, assetID, meterCode).Scan(&meterID); err != nil {
		return ErrNotFound
	}
	var acc float64
	_ = tx.QueryRowContext(ctx, `SELECT accumulated FROM meter_states WHERE meter_id=$1 AND source_mode=$2`, meterID, mode).Scan(&acc)
	r, err := tx.ExecContext(ctx, `INSERT INTO equipment_meter_baselines(id,equipment_installation_id,meter_id,source_mode,baseline_accumulated,initial_usage,baseline_kind,recorded_by,note) VALUES($1,$2,$3,$4,$5,$6,'INITIAL_RECORD',$7,$8) ON CONFLICT DO NOTHING`,
		platform.ID(), eqID, meterID, mode, acc, initialUsage, actor, note)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: baseline already recorded (corrections need a reviewed procedure)", ErrConflict)
	}
	return tx.Commit()
}

// TestRule is a SIMULATION / TEST threshold fixture (never evaluated for REAL; DB-enforced).
type TestRule struct {
	Code, Name, Kind, Type, Slot, MeterCode string
	IntervalDays                            *int
	IntervalMeter                           *float64
	LeadDays, WarnPercents                  []int
	AnchorAt                                *time.Time // simulated installation date of the slot instance
}

// UpsertTestRule creates (or replaces, by bumping the version) a test-only rule.
func UpsertTestRule(ctx context.Context, db *sql.DB, assetID string, r TestRule, actor string) (string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var meter any
	if r.MeterCode != "" {
		var id string
		if err = tx.QueryRowContext(ctx, `SELECT id FROM meter_definitions WHERE asset_id=$1 AND meter_code=$2`, assetID, r.MeterCode).Scan(&id); err != nil {
			return "", ErrNotFound
		}
		meter = id
	}
	var version int
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(max(version),0) FROM maintenance_rules WHERE asset_id=$1 AND rule_code=$2`, assetID, r.Code).Scan(&version)
	if _, err = tx.ExecContext(ctx, `UPDATE maintenance_rules SET approval_status='RETIRED' WHERE asset_id=$1 AND rule_code=$2 AND test_only`, assetID, r.Code); err != nil {
		return "", err
	}
	lead, warn := intArray(r.LeadDays, "{90,30,7}"), intArray(r.WarnPercents, "{80,90}")
	id := platform.ID()
	var slot any
	if r.Slot != "" {
		slot = r.Slot
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO maintenance_rules(id,asset_id,rule_code,version,name,rule_kind,rule_type,equipment_slot,meter_id,interval_days,interval_meter,lead_days,warn_percents,basis,approval_status,test_only,test_anchor_at,created_by)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::int[],$13::int[],'SIMULATION / TEST threshold fixture — not a real maintenance rule','DRAFT_UNAPPROVED',true,$14,$15)`,
		id, assetID, r.Code, version+1, r.Name, r.Kind, r.Type, slot, meter, r.IntervalDays, r.IntervalMeter, lead, warn, r.AnchorAt, actor); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func intArray(v []int, def string) string {
	if len(v) == 0 {
		return def
	}
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = fmt.Sprint(x)
	}
	return "{" + strings.Join(parts, ",") + "}"
}
