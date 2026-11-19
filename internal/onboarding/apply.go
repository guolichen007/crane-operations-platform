package onboarding

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/guolichen007/crane-operations-platform/internal/equipment"
)

// writer performs the NEW part of an approved plan inside the apply transaction. Every write uses
// the regular tables (and their constraints / triggers), writes an entity revision for the business
// objects, and is summarized in the single onboarding audit row.
type writer struct {
	ctx       context.Context
	tx        *sql.Tx
	d         desired
	p         Plan
	actor     string
	requestID string
	created   map[string]int
	isNew     map[string]bool
}

func (w *writer) exec(q string, args ...any) error {
	_, err := w.tx.ExecContext(w.ctx, q, args...)
	return err
}

func (w *writer) revision(entityType, entityID string, snapshot any) error {
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	return w.exec(`INSERT INTO entity_revisions(project_id,asset_id,entity_type,entity_id,revision,recorded_at,effective_at,changed_by,request_id,changed_fields,snapshot_json,snapshot_sha256)
VALUES($1,NULLIF($2,''),$3,$4,(SELECT COALESCE(MAX(revision),0)+1 FROM entity_revisions WHERE entity_type=$3 AND entity_id=$4),now(),now(),$5,$6,ARRAY['imported'],$7::jsonb,$8)`,
		ProjectID, AssetIDIf(entityType), entityType, entityID, w.actor, w.requestID, string(b), hex.EncodeToString(sum[:]))
}

// AssetIDIf anchors asset-scoped revisions to the asset (project-level ones have no asset).
func AssetIDIf(entityType string) string {
	if entityType == "project" || entityType == "location" || entityType == "manufacturer" || entityType == "catalog" {
		return ""
	}
	return AssetID
}

func (w *writer) run() error {
	w.isNew = map[string]bool{}
	for _, it := range w.p.Items {
		if it.Status == "NEW" {
			w.isNew[it.Kind+"|"+it.Key] = true
		}
	}
	s := w.d.s
	count := func(kind string) { w.created[kind]++ }
	// 1. Project (registered in the Inventory scope: 湖北省 襄阳市 老河口市 from the source title; no coordinates).
	if w.isNew["project|"+ProjectID] {
		if err := w.exec(`INSERT INTO projects(id,tenant_id,name,lifecycle,province,city,county,location_label,admin_code,notes) VALUES($1,$2,$3,'PLANNED','湖北省','襄阳市','老河口市','湖北省 襄阳市 老河口市',420682,$4)`,
			ProjectID, TenantID, ProjectName, "R9 首台真实资产导入；项目阶段与详细地址待确认（原文件仅有「老河口」）"); err != nil {
			return err
		}
		if err := w.revision("project", ProjectID, map[string]any{"id": ProjectID, "name": ProjectName, "adminCode": 420682, "source": s.Source.Filename}); err != nil {
			return err
		}
		count("project")
	}
	// 2. Site location (no address / coordinates: UNKNOWN, never guessed).
	if w.isNew["site|"+SiteID] {
		if err := w.exec(`INSERT INTO asset_locations(id,project_id,kind,code,name,province,city,county,coordinate_system,coordinate_source,status) VALUES($1,$2,'SITE',$3,$4,'湖北省','襄阳市','老河口市','UNKNOWN','IMPORTED','ACTIVE')`,
			SiteID, ProjectID, SiteID, "老河口现场（地址 / 车间待确认）"); err != nil {
			return err
		}
		if err := w.revision("location", SiteID, map[string]any{"id": SiteID, "coordinateSystem": "UNKNOWN"}); err != nil {
			return err
		}
		count("site")
	}
	// 3. Physical asset: DRAFT until the nameplate confirms the model interpretation; rated capacity
	// and span stay NULL (candidates in asset_pending_attributes).
	if w.isNew["asset|"+AssetID] {
		if err := w.exec(`INSERT INTO asset_devices(id,project_id,display_name,crane_model,rated_capacity_t,span_m,lifecycle,asset_code,asset_type,asset_status,model_snapshot)
VALUES($1,$2,$3,$4,NULL,NULL,'PLANNED',$5,'CRANE','DRAFT',$4)`, AssetID, ProjectID, s.AssetDraft.DisplayName, s.AssetDraft.Model, s.AssetDraft.ProposedAssetCode); err != nil {
			return err
		}
		if err := w.exec(`INSERT INTO asset_installations(id,asset_id,location_id,valid_from,source,notes) VALUES($1,$2,$3,now(),'IMPORTED',$4)`,
			id("AI", AssetID), AssetID, SiteID, "导入登记时间，非实际安装日期（待确认）"); err != nil {
			return err
		}
		if err := w.revision("asset", AssetID, map[string]any{"id": AssetID, "assetCode": s.AssetDraft.ProposedAssetCode, "model": s.AssetDraft.Model, "status": "DRAFT",
			"ratedCapacityT": nil, "spanM": nil, "source": s.Source.Filename, "sourceSha256": s.Source.SHA256}); err != nil {
			return err
		}
		if err := w.exec(`INSERT INTO asset_timeline_events(project_id,asset_id,category,occurred_at,source,reference_type,reference_id,summary) VALUES($1,$2,'ONBOARDING',now(),'IMPORT','onboarding',$3,$4)`,
			ProjectID, AssetID, w.p.StagingSHA256, "首台资产台账导入（55 行原始清单）"); err != nil {
			return err
		}
		count("asset")
	}
	// 4. Manufacturers and catalog models (project-scoped, deduplicated by stable id).
	for _, m := range sortedKeys(w.d.manufacturer) {
		if !w.isNew["manufacturer|"+m] {
			continue
		}
		if err := w.exec(`INSERT INTO manufacturers(id,project_id,name,short_name,status) VALUES($1,$2,$3,$3,'ACTIVE')`, id("MFR", m), ProjectID, m); err != nil {
			return err
		}
		if err := w.revision("manufacturer", id("MFR", m), map[string]any{"name": m}); err != nil {
			return err
		}
		count("manufacturer")
	}
	for _, k := range sortedKeys(w.d.catalog) {
		if !w.isNew["catalog|"+k] {
			continue
		}
		c := w.d.catalog[k]
		var mfr any
		if c.Manufacturer != "" {
			mfr = id("MFR", c.Manufacturer)
		}
		name := strings.TrimSpace(c.Brand + " " + c.Model)
		if name == "" {
			name = c.TypeCode
		}
		if err := w.exec(`INSERT INTO equipment_catalog_items(id,project_id,name,type_code,manufacturer_id,brand,model,spec_schema_version,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'ACTIVE')`,
			id("CAT", k), ProjectID, name, c.TypeCode, mfr, c.Brand, c.Model, equipment.SpecSchemaVersion); err != nil {
			return err
		}
		if err := w.revision("catalog", id("CAT", k), map[string]any{"typeCode": c.TypeCode, "brand": c.Brand, "model": c.Model, "raw": c.Raw}); err != nil {
			return err
		}
		count("catalog")
	}
	// 5. Original rows (all 55, lossless) and equipment installations (51 equipment rows).
	equipmentID := map[int]string{}
	for _, e := range w.d.equipment {
		eqID := id("EQ", e.Code)
		equipmentID[e.Rec.SourceIndex] = eqID
		if !w.isNew["equipment|"+e.Code] {
			continue
		}
		var cat any
		var brand, model string
		if e.CatalogKey != "" {
			cat = id("CAT", e.CatalogKey)
			brand, model = w.d.catalog[e.CatalogKey].Brand, w.d.catalog[e.CatalogKey].Model
		}
		// valid_from = ledger registration time; installed_at (actual) stays NULL until confirmed.
		if err := w.exec(`INSERT INTO asset_equipment_installations(id,asset_id,equipment_code,type_code,catalog_item_id,manufacturer_snapshot,brand_snapshot,model_snapshot,semantic_slot,parent_semantic_slot,position_label,criticality,administrative_state,valid_from,notes,spec_json)
VALUES($1,$2,$3,$4,$5,$6,$6,$7,$8,$9,$10,$11,'IN_SERVICE',now(),$12,'{}'::jsonb)`,
			eqID, AssetID, e.Code, e.TypeCode, cat, brand, model, e.Slot, mechanismCode[e.Rec.Group], truncate(e.Position, 80), e.Criticality,
			fmt.Sprintf("来源 %s 第 %d 行（序号 %d）；实际安装日期、序列号与初始累计值待确认", w.d.s.Source.Filename, e.Rec.SourceRow, e.Rec.SourceIndex)); err != nil {
			return err
		}
		if err := w.revision("equipment", eqID, map[string]any{"equipmentCode": e.Code, "typeCode": e.TypeCode, "slot": e.Slot, "sourceRow": e.Rec.SourceRow, "installedAt": nil}); err != nil {
			return err
		}
		if e.TypeCode == "WIRE_ROPE" {
			raw := deref(e.Rec.Raw.BrandModel)
			var diameter any
			if strings.Contains(raw, "φ16") {
				diameter = 16 // stated in the original text (φ16); grade / count / install date stay unknown
			}
			if err := w.exec(`INSERT INTO wire_rope_details(equipment_installation_id,asset_id,construction_raw,nominal_diameter_mm) VALUES($1,$2,$3,$4)`, eqID, AssetID, raw, diameter); err != nil {
				return err
			}
		}
		count("equipment")
	}
	sourceRowID := map[int]string{}
	for _, r := range s.Records {
		key := fmt.Sprintf("%s!%d", r.SourceSheet, r.SourceRow)
		srcID := id("SRC", s.Source.SHA256+"|"+key)
		sourceRowID[r.SourceIndex] = srcID
		if !w.isNew["source_row|"+key] {
			continue
		}
		kind := "OPERATION"
		var eq any
		if r.Proposed.StableSlot != nil {
			kind, eq = "EQUIPMENT", equipmentID[r.SourceIndex]
		}
		flags := r.ReviewFlags
		if flags == nil {
			flags = []string{}
		}
		if err := w.exec(`INSERT INTO onboarding_source_rows(id,project_id,asset_id,source_filename,source_sha256,source_sheet,source_row,source_index,row_kind,group_name,item_name,raw,proposed,review_flags,equipment_installation_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13::jsonb,$14,$15)`,
			srcID, ProjectID, AssetID, s.Source.Filename, s.Source.SHA256, r.SourceSheet, r.SourceRow, r.SourceIndex, kind, r.Group, r.Name, string(r.rawJSON), string(r.propJSON), flags, eq); err != nil {
			return err
		}
		count("source_row")
	}
	// 6. Raw points (all UNCONFIRMED / UNVERIFIED) and meters (PENDING_REVIEW).
	pointID := map[string]string{}
	for _, pt := range w.d.points {
		pid := id("PT", AssetID+"|"+pt.Code)
		pointID[pt.Code] = pid
		if !w.isNew["point|"+pt.Code] {
			continue
		}
		var slot any
		if pt.Slot != "" {
			slot = pt.Slot
		}
		if err := w.exec(`INSERT INTO telemetry_points(id,asset_id,point_code,name,mechanism,equipment_slot,plc_address_raw,plc_area,plc_db,plc_byte,plc_bit,address_kind,raw_data_type,derived_type,unit_raw,unit,acquisition_source,source_row_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
			pid, AssetID, pt.Code, pt.Name, pt.Mechanism, slot, pt.Addr.Raw, nullable(pt.Addr.Area), pt.Addr.DB, pt.Addr.Byte, pt.Addr.Bit, pt.Addr.Kind,
			pt.Rec.Raw.VariableType, pt.DerivedType, pt.Rec.Raw.Statistic, pt.Unit, pt.Source, sourceRowID[pt.Rec.SourceIndex]); err != nil {
			return err
		}
		count("point")
	}
	meterID := map[string]string{}
	for _, m := range w.d.meters {
		mid := id("MTR", AssetID+"|"+m.Code)
		meterID[m.Code] = mid
		if !w.isNew["meter|"+m.Code] {
			continue
		}
		var slot any
		if m.Slot != "" {
			slot = m.Slot
		}
		if err := w.exec(`INSERT INTO meter_definitions(id,asset_id,point_id,meter_code,metric,meter_kind,unit,scope,equipment_slot,formula,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			mid, AssetID, pointID[m.PointCode], m.Code, m.Metric, m.Kind, m.Unit, m.Scope, slot, m.Formula, m.Status); err != nil {
			return err
		}
		count("meter")
	}
	// 7. Review issues (idempotent by key), pending attributes, draft rules.
	for _, is := range w.d.issues {
		var pid, srcID any
		if is.PointCode != "" {
			pid = pointID[is.PointCode]
		}
		for _, r := range s.Records {
			if r.SourceRow == is.SourceRow && is.SourceRow > 0 {
				srcID = sourceRowID[r.SourceIndex]
			}
		}
		detail, _ := json.Marshal(is.Detail)
		res, err := w.tx.ExecContext(w.ctx, `INSERT INTO point_review_issues(id,asset_id,point_id,source_row_id,issue_code,detail) VALUES($1,$2,$3,$4,$5,$6::jsonb) ON CONFLICT DO NOTHING`,
			id("ISS", is.Code+"|"+is.PointCode+"|"+fmt.Sprint(is.Detail["plcAddress"])), AssetID, pid, srcID, is.Code, string(detail))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			count("issue")
		}
	}
	for _, pa := range w.d.pending {
		if !w.isNew["pending|"+pa.Subject+"."+pa.Attribute] {
			continue
		}
		var candidate any
		if pa.Candidate != nil {
			b, _ := json.Marshal(pa.Candidate)
			candidate = string(b)
		}
		var srcID any
		if pa.SourceIndex > 0 {
			srcID = sourceRowID[pa.SourceIndex]
		}
		if err := w.exec(`INSERT INTO asset_pending_attributes(id,asset_id,subject,attribute,candidate,basis,source_row_id) VALUES($1,$2,$3,$4,$5::jsonb,$6,$7)`,
			id("PA", AssetID+"|"+pa.Subject+"|"+pa.Attribute), AssetID, pa.Subject, pa.Attribute, candidate, pa.Basis, srcID); err != nil {
			return err
		}
		count("pending")
	}
	for _, r := range w.d.rules {
		if !w.isNew["rule|"+r.Code] {
			continue
		}
		var meter any
		if r.MeterCode != "" {
			meter = meterID[r.MeterCode]
		}
		if err := w.exec(`INSERT INTO maintenance_rules(id,asset_id,rule_code,version,name,rule_kind,rule_type,equipment_slot,meter_id,interval_days,interval_meter,basis,source_row_id,approval_status,created_by)
VALUES($1,$2,$3,1,$4,$5,$6,$7,$8,$9,$10,$11,$12,'DRAFT_UNAPPROVED',$13)`,
			id("RULE", AssetID+"|"+r.Code+"|1"), AssetID, r.Code, r.Name, r.Kind, r.Type, r.Slot, meter, r.IntervalDays, r.IntervalMeter, r.Basis, sourceRowID[r.SourceIndex], w.actor); err != nil {
			return err
		}
		count("rule")
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// ---------------------------------------------------------------------------------------------
// Verify
// ---------------------------------------------------------------------------------------------

// Report is the verify result: counts in the database for this asset versus the staging document.
type Report struct {
	Consistent bool           `json:"consistent"`
	Expected   map[string]int `json:"expected"`
	Actual     map[string]int `json:"actual"`
	Plan       Plan           `json:"plan"`
	// R9 收口: the scope the project lives in. The import registers an Inventory project only: no
	// portfolio scope, no intake, no approval or acceptance history is created for it. A portfolio
	// link (if the business decides one) is made through the reviewed portfolio flow, never here.
	Scope ProjectScope `json:"projectScope"`
}

type ProjectScope struct {
	PortfolioScopeID *string `json:"portfolioScopeId"`
	IntakeRecords    int     `json:"intakeRecords"`
	ApprovalAudits   int     `json:"approvalAudits"`
	Members          int     `json:"projectMembers"`
}

// Verify re-plans (read-only) and counts every imported object: consistent means the plan has no
// NEW and no CONFLICT item and every count matches.
func Verify(ctx context.Context, q queryer, staging []byte) (Report, error) {
	p, d, err := PlanImport(ctx, q, staging)
	if err != nil {
		return Report{}, err
	}
	r := Report{Plan: p, Expected: map[string]int{
		"source_rows": len(d.s.Records), "equipment_rows": d.s.Counts.EquipmentCandidateRows, "operation_rows": d.s.Counts.OperationRows,
		"equipment": len(d.equipment), "points": len(d.points), "meters": len(d.meters), "rules": len(d.rules), "pending": len(d.pending),
	}, Actual: map[string]int{}}
	for k, query := range map[string]string{
		"source_rows":    `SELECT count(*) FROM onboarding_source_rows WHERE asset_id=$1`,
		"equipment_rows": `SELECT count(*) FROM onboarding_source_rows WHERE asset_id=$1 AND row_kind='EQUIPMENT'`,
		"operation_rows": `SELECT count(*) FROM onboarding_source_rows WHERE asset_id=$1 AND row_kind='OPERATION'`,
		"equipment":      `SELECT count(*) FROM asset_equipment_installations WHERE asset_id=$1 AND equipment_code LIKE 'LHK-CRANE-001-%' AND id IN (SELECT equipment_installation_id FROM onboarding_source_rows WHERE asset_id=$1)`,
		"points":         `SELECT count(*) FROM telemetry_points WHERE asset_id=$1`,
		"meters":         `SELECT count(*) FROM meter_definitions WHERE asset_id=$1`,
		"rules":          `SELECT count(DISTINCT rule_code) FROM maintenance_rules WHERE asset_id=$1 AND NOT test_only`,
		"pending":        `SELECT count(*) FROM asset_pending_attributes WHERE asset_id=$1`,
	} {
		var n int
		if err := q.QueryRowContext(ctx, query, AssetID).Scan(&n); err != nil {
			return r, err
		}
		r.Actual[k] = n
	}
	if err = q.QueryRowContext(ctx, `SELECT p.portfolio_scope_id, (SELECT count(*) FROM project_intakes i WHERE i.formal_project_id=p.id),
 (SELECT count(*) FROM audit_logs a WHERE a.project_id=p.id AND a.action IN ('intake_approved','schedule_approved','acceptance_recorded')),
 (SELECT count(*) FROM project_memberships m WHERE m.project_id=p.id) FROM projects p WHERE p.id=$1`, ProjectID).
		Scan(&r.Scope.PortfolioScopeID, &r.Scope.IntakeRecords, &r.Scope.ApprovalAudits, &r.Scope.Members); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	r.Consistent = len(p.Conflicts) == 0 && len(p.New) == 0 && r.Scope.IntakeRecords == 0 && r.Scope.ApprovalAudits == 0
	for k, v := range r.Expected {
		if r.Actual[k] != v {
			r.Consistent = false
		}
	}
	return r, nil
}
