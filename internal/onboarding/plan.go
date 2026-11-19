package onboarding

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/equipment"
)

// Fixed identifiers of the first real asset. They are business identifiers (stable across
// environments), never random, so a re-import finds the same rows.
const (
	TenantID    = "demo" // the single Crane tenant (projects.tenant_id of every crane-dev project)
	ProjectID   = "LHK"
	ProjectName = "老河口行车项目"
	AssetID     = "LHK-CRANE-001"
	SiteID      = "LHK-SITE"
	importKind  = "LHK_FIRST_ASSET"
	// lockClass serializes onboarding applies (advisory lock).
	lockClass = 7160021
)

var (
	ErrDigestMismatch = errors.New("onboarding state changed since the approved plan; re-run plan and re-approve")
	ErrConflict       = errors.New("existing data conflicts with the staging document; nothing applied")
	ErrActorRequired  = errors.New("explicit administrator actor required")
)

// Target identifies the database (no secret); part of the digest.
type Target struct {
	Database      string `json:"database"`
	Role          string `json:"role"`
	DatabaseOID   int64  `json:"databaseOid"`
	SchemaVersion int64  `json:"schemaVersion"`
}

// Item is one planned object.
type Item struct {
	Kind   string `json:"kind"`   // project | site | asset | manufacturer | catalog | equipment | source_row | point | meter | issue | pending | rule
	Key    string `json:"key"`    // stable business key
	Status string `json:"status"` // NEW | EXISTING | CONFLICT
	Reason string `json:"reason,omitempty"`
}

// Plan is the read-only classification of the import.
type Plan struct {
	Target        Target         `json:"target"`
	SourceFile    string         `json:"sourceFile"`
	SourceSHA256  string         `json:"sourceSha256"`
	StagingSHA256 string         `json:"stagingSha256"`
	Counts        map[string]int `json:"counts"`
	New           map[string]int `json:"new"`
	Existing      map[string]int `json:"existing"`
	Conflicts     []Item         `json:"conflicts"`
	Items         []Item         `json:"items"`
	Digest        string         `json:"digest"`
}

// Record is the credential-free result of an apply.
type Record struct {
	Target    Target         `json:"target"`
	ImportID  string         `json:"importId"`
	Actor     string         `json:"actor"`
	Digest    string         `json:"approvedDigest"`
	Result    string         `json:"result"` // success | noop
	Created   map[string]int `json:"created"`
	ProjectID string         `json:"projectId"`
	AssetID   string         `json:"assetId"`
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func target(ctx context.Context, q queryer) (Target, error) {
	var t Target
	err := q.QueryRowContext(ctx, `SELECT current_database(), current_user, (SELECT oid::bigint FROM pg_database WHERE datname=current_database()), COALESCE((SELECT max(version_id) FROM goose_db_version WHERE is_applied),0)`).
		Scan(&t.Database, &t.Role, &t.DatabaseOID, &t.SchemaVersion)
	return t, err
}

// ---------------------------------------------------------------------------------------------
// Desired state (pure, from the staging document)
// ---------------------------------------------------------------------------------------------

type desired struct {
	s            Staging
	stagingSHA   string
	manufacturer map[string]string // manufacturer key → display name
	catalog      map[string]catalogItem
	equipment    []equipmentRow
	points       []pointRow
	meters       []meterRow
	issues       []Issue
	pending      []pendingRow
	rules        []ruleRow
}

type catalogItem struct {
	Key, TypeCode, Manufacturer, Brand, Model, Raw string
}
type equipmentRow struct {
	Code, Slot, TypeCode, CatalogKey, Position, Criticality string
	Rec                                                     Row
}
type pointRow struct {
	Code, Name, Mechanism, Slot, DerivedType, Unit, Source string
	Addr                                                   Address
	Rec                                                    Row
}
type meterRow struct {
	Meter
	PointCode string
}
type pendingRow struct {
	Subject, Attribute, Basis string
	Candidate                 any
	SourceIndex               int
}
type ruleRow struct {
	Code, Name, Kind, Type, Slot, MeterCode, Basis string
	IntervalDays                                   *int
	IntervalMeter                                  *float64
	SourceIndex                                    int
}

var knownBrands = []string{"施耐德", "ABB", "倍加福", "晨控智能", "正泰", "亚德客", "海康威视", "SICK", "速腾", "科星创", "恒达"}

// splitBrandModel splits the original brand/model cell into (manufacturer, model). Unknown or
// generic cells ("国产优质") keep the whole text as the model with no manufacturer.
func splitBrandModel(raw string) (string, string) {
	text := strings.Join(strings.Fields(raw), " ")
	for _, b := range knownBrands {
		if strings.HasPrefix(text, b) {
			return b, strings.TrimSpace(strings.TrimPrefix(text, b))
		}
		if strings.HasSuffix(text, " "+b) { // "10吨SBT-CW模块型 恒达"
			return b, strings.TrimSpace(strings.TrimSuffix(text, b))
		}
	}
	return "", text
}

var criticalTypes = map[string]string{"LIMIT_SWITCH": "SAFETY_CRITICAL", "BRAKE": "SAFETY_CRITICAL", "WIRE_ROPE": "SAFETY_CRITICAL", "LOAD_CELL": "HIGH", "CONTACTOR": "HIGH", "VFD": "HIGH", "THERMAL_OVERLOAD_RELAY": "HIGH", "MOTOR_PROTECTOR": "HIGH"}

var lifeCycles = regexp.MustCompile(`(电寿命约|机械寿命≥|起落寿命≥)(\d+)(万)?次`)

func build(s Staging, stagingSHA string) (desired, error) {
	d := desired{s: s, stagingSHA: stagingSHA, manufacturer: map[string]string{}, catalog: map[string]catalogItem{}}
	for _, r := range s.Records {
		if r.Proposed.StableSlot == nil {
			continue
		}
		t, _ := equipmentType(r)
		if t == "" || !equipment.ValidTypeCode(t) {
			return d, fmt.Errorf("%w: row %d has no registered equipment type (%q)", ErrStaging, r.SourceRow, t)
		}
		catKey := ""
		if r.Raw.BrandModel != nil && strings.TrimSpace(*r.Raw.BrandModel) != "" {
			mfr, model := splitBrandModel(*r.Raw.BrandModel)
			if mfr != "" {
				d.manufacturer[mfr] = mfr
			}
			catKey = t + "|" + mfr + "|" + model
			d.catalog[catKey] = catalogItem{Key: catKey, TypeCode: t, Manufacturer: mfr, Brand: mfr, Model: model, Raw: *r.Raw.BrandModel}
		}
		crit := criticalTypes[t]
		if crit == "" {
			crit = "MEDIUM"
		}
		d.equipment = append(d.equipment, equipmentRow{Code: AssetID + "-" + *r.Proposed.StableSlot, Slot: *r.Proposed.StableSlot, TypeCode: t, CatalogKey: catKey,
			Position: r.Group + " / " + r.Name, Criticality: crit, Rec: r})
	}
	for _, r := range s.Records {
		dt, unit, src := derived(r)
		slot := ""
		if r.Proposed.StableSlot != nil {
			slot = *r.Proposed.StableSlot
		}
		d.points = append(d.points, pointRow{Code: r.Proposed.PointCode, Name: r.Name, Mechanism: r.Group, Slot: slot, DerivedType: dt, Unit: unit, Source: src, Addr: parseAddress(r.Raw.PLCAddress), Rec: r})
		for _, m := range meters(r) {
			d.meters = append(d.meters, meterRow{Meter: m, PointCode: r.Proposed.PointCode})
		}
		// Draft (unapproved) life rules from the original life text: METER_BASED for 次 counts,
		// TIME_BASED (years) for design life; never approved, never a discard threshold.
		if slot != "" && r.Raw.LifeText != nil {
			text := *r.Raw.LifeText
			if m := lifeCycles.FindStringSubmatch(text); m != nil && r.Proposed.MetricCandidate == "cycles" {
				n := 0
				fmt.Sscanf(m[2], "%d", &n)
				v := float64(n)
				if m[3] == "万" {
					v *= 10000
				}
				d.rules = append(d.rules, ruleRow{Code: "LIFE-" + slot, Name: r.Group + " " + r.Name + " 寿命（次数，待审核）", Kind: "REPLACEMENT", Type: "METER_BASED", Slot: slot,
					MeterCode: r.Proposed.PointCode + ".cycles", IntervalMeter: &v, Basis: "原始清单寿命：" + text, SourceIndex: r.SourceIndex})
			} else if years := yearsOf(text); years > 0 {
				days := years * 365
				d.rules = append(d.rules, ruleRow{Code: "LIFE-" + slot, Name: r.Group + " " + r.Name + " 设计寿命（年限，待审核）", Kind: "REPLACEMENT", Type: "TIME_BASED", Slot: slot,
					IntervalDays: &days, Basis: "原始清单寿命：" + text, SourceIndex: r.SourceIndex})
			} else {
				d.rules = append(d.rules, ruleRow{Code: "LIFE-" + slot, Name: r.Group + " " + r.Name + " 寿命（待审核）", Kind: "REPLACEMENT", Type: "TIME_BASED", Slot: slot,
					Basis: "原始清单寿命：" + text + "（无法解析为确定间隔，待人工定义）", SourceIndex: r.SourceIndex})
			}
		}
		if t, inferred := equipmentType(r); inferred {
			d.pending = append(d.pending, pendingRow{Subject: slot, Attribute: "lidarDimension", Candidate: nil, Basis: "清单未注明 2D/3D（速腾 进料口激光雷达）；暂按 " + t + " 登记", SourceIndex: r.SourceIndex})
		}
		if t, _ := equipmentType(r); t == "RFID_READER" {
			d.pending = append(d.pending, pendingRow{Subject: slot, Attribute: "rfidEntity", Candidate: "RFID_READER", Basis: "原文为「RFID通讯」，读写器 / 标签 / 通讯模块实体待现场确认", SourceIndex: r.SourceIndex})
		}
		if t, _ := equipmentType(r); t == "RADAR" {
			d.pending = append(d.pending, pendingRow{Subject: slot, Attribute: "radarType", Candidate: nil, Basis: "车检雷达 / 毫米波雷达的探测原理与型号待确认", SourceIndex: r.SourceIndex})
		}
		if hasFlag(r, "ROPE_COUNT_AND_INSTALLATION_UNKNOWN") {
			d.pending = append(d.pending, pendingRow{Subject: slot, Attribute: "ropeCount", Candidate: nil, Basis: "清单仅一行钢丝绳，根数与安装日期未知", SourceIndex: r.SourceIndex})
		}
	}
	// Calibration rule for the load cell (原文「定期校准」, interval unknown → draft without interval).
	for _, e := range d.equipment {
		if e.TypeCode == "LOAD_CELL" {
			d.rules = append(d.rules, ruleRow{Code: "CAL-" + e.Slot, Name: "称重传感器 定期校准（周期待定）", Kind: "CALIBRATION", Type: "TIME_BASED", Slot: e.Slot, Basis: "原始清单：「定期校准」，周期未给出", SourceIndex: e.Rec.SourceIndex})
		}
		if e.TypeCode == "WIRE_ROPE" {
			d.rules = append(d.rules, ruleRow{Code: "INSP-" + e.Slot, Name: "钢丝绳 定期检查（GB/T 5972，周期待定）", Kind: "INSPECTION", Type: "TIME_BASED", Slot: e.Slot, Basis: "原始清单：按 GB/T 5972 检查断丝 / 磨损；不由系统判定报废", SourceIndex: e.Rec.SourceIndex})
		}
	}
	rated, span := s.AssetDraft.RatedLoadTCandidate, s.AssetDraft.SpanMCandidate
	d.pending = append(d.pending,
		pendingRow{Subject: "asset", Attribute: "ratedCapacityT", Candidate: rated, Basis: "由型号文本 " + s.AssetDraft.Model + " 推断，待铭牌确认"},
		pendingRow{Subject: "asset", Attribute: "spanM", Candidate: span, Basis: "由型号文本 " + s.AssetDraft.Model + " 推断，待铭牌确认"},
		pendingRow{Subject: "asset", Attribute: "location", Candidate: nil, Basis: "原文件仅有「老河口」，无地址 / 坐标 / 车间"},
		pendingRow{Subject: "asset", Attribute: "statutoryInspection", Candidate: nil, Basis: "原文件未提供使用登记号与法定检验有效期"},
	)
	d.issues = issues(s)
	return d, nil
}

func yearsOf(text string) int {
	m := regexp.MustCompile(`约?(\d+)(?:~(\d+))?年`).FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	n := 0
	fmt.Sscanf(m[1], "%d", &n) // the lower bound: earliest reminder; still a DRAFT for review
	return n
}

// ---------------------------------------------------------------------------------------------
// Plan
// ---------------------------------------------------------------------------------------------

func id(prefix, key string) string {
	sum := sha256.Sum256([]byte(prefix + "|" + key))
	return prefix + "-" + hex.EncodeToString(sum[:])[:20]
}

// PlanImport classifies every object against the database (read-only).
func PlanImport(ctx context.Context, q queryer, staging []byte) (Plan, desired, error) {
	sum := sha256.Sum256(staging)
	stagingSHA := hex.EncodeToString(sum[:])
	s, err := Parse(staging)
	if err != nil {
		return Plan{}, desired{}, err
	}
	d, err := build(s, stagingSHA)
	if err != nil {
		return Plan{}, d, err
	}
	t, err := target(ctx, q)
	if err != nil {
		return Plan{}, d, err
	}
	p := Plan{Target: t, SourceFile: s.Source.Filename, SourceSHA256: s.Source.SHA256, StagingSHA256: stagingSHA,
		Counts: map[string]int{}, New: map[string]int{}, Existing: map[string]int{}, Conflicts: []Item{}, Items: []Item{}}
	add := func(it Item) {
		p.Counts[it.Kind]++
		switch it.Status {
		case "NEW":
			p.New[it.Kind]++
		case "EXISTING":
			p.Existing[it.Kind]++
		default:
			p.Conflicts = append(p.Conflicts, it)
		}
		p.Items = append(p.Items, it)
	}
	exists := func(query string, args ...any) (bool, error) {
		var n int
		err := q.QueryRowContext(ctx, query, args...).Scan(&n)
		return n > 0, err
	}
	// Project.
	var tenant, name sql.NullString
	err = q.QueryRowContext(ctx, `SELECT tenant_id, name FROM projects WHERE id=$1`, ProjectID).Scan(&tenant, &name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		var tenantExists bool
		if tenantExists, err = exists(`SELECT count(*) FROM tenants WHERE id=$1`, TenantID); err != nil {
			return p, d, err
		}
		if !tenantExists {
			add(Item{Kind: "project", Key: ProjectID, Status: "CONFLICT", Reason: "tenant " + TenantID + " missing"})
		} else {
			add(Item{Kind: "project", Key: ProjectID, Status: "NEW"})
		}
	case err != nil:
		return p, d, err
	case tenant.String != TenantID || name.String != ProjectName:
		add(Item{Kind: "project", Key: ProjectID, Status: "CONFLICT", Reason: "project id LHK exists with another tenant/name"})
	default:
		add(Item{Kind: "project", Key: ProjectID, Status: "EXISTING"})
	}
	// Site location.
	var locProject sql.NullString
	err = q.QueryRowContext(ctx, `SELECT project_id FROM asset_locations WHERE id=$1`, SiteID).Scan(&locProject)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		add(Item{Kind: "site", Key: SiteID, Status: "NEW"})
	case err != nil:
		return p, d, err
	case locProject.String != ProjectID:
		add(Item{Kind: "site", Key: SiteID, Status: "CONFLICT", Reason: "location id belongs to another project"})
	default:
		add(Item{Kind: "site", Key: SiteID, Status: "EXISTING"})
	}
	// Asset (by id and by project + asset code).
	var assetProject, assetCode sql.NullString
	err = q.QueryRowContext(ctx, `SELECT project_id, asset_code FROM asset_devices WHERE id=$1`, AssetID).Scan(&assetProject, &assetCode)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		taken, err := exists(`SELECT count(*) FROM asset_devices WHERE project_id=$1 AND asset_code=$2`, ProjectID, s.AssetDraft.ProposedAssetCode)
		if err != nil {
			return p, d, err
		}
		if taken {
			add(Item{Kind: "asset", Key: AssetID, Status: "CONFLICT", Reason: "asset code already used by another asset"})
		} else {
			add(Item{Kind: "asset", Key: AssetID, Status: "NEW"})
		}
	case err != nil:
		return p, d, err
	case assetProject.String != ProjectID || assetCode.String != s.AssetDraft.ProposedAssetCode:
		add(Item{Kind: "asset", Key: AssetID, Status: "CONFLICT", Reason: "asset id exists with another project/code"})
	default:
		add(Item{Kind: "asset", Key: AssetID, Status: "EXISTING"})
	}
	// Equipment types must be installable (initialized with equipment-types apply).
	types := map[string]bool{}
	for _, e := range d.equipment {
		types[e.TypeCode] = true
	}
	typeList := make([]string, 0, len(types))
	for t := range types {
		typeList = append(typeList, t)
	}
	sort.Strings(typeList)
	for _, t := range typeList {
		var status string
		err := q.QueryRowContext(ctx, `SELECT status FROM equipment_types WHERE type_code=$1`, t).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && status != "ACTIVE") {
			add(Item{Kind: "equipment_type", Key: t, Status: "CONFLICT", Reason: "type not initialized or disabled: run equipment-types plan/apply --all first"})
			continue
		}
		if err != nil {
			return p, d, err
		}
		add(Item{Kind: "equipment_type", Key: t, Status: "EXISTING"})
	}
	// Manufacturers / catalog items (project-scoped, reused by stable id).
	for _, m := range sortedKeys(d.manufacturer) {
		ok, err := exists(`SELECT count(*) FROM manufacturers WHERE id=$1`, id("MFR", m))
		if err != nil {
			return p, d, err
		}
		add(Item{Kind: "manufacturer", Key: m, Status: map[bool]string{true: "EXISTING", false: "NEW"}[ok]})
	}
	for _, k := range sortedKeys(d.catalog) {
		ok, err := exists(`SELECT count(*) FROM equipment_catalog_items WHERE id=$1`, id("CAT", k))
		if err != nil {
			return p, d, err
		}
		add(Item{Kind: "catalog", Key: k, Status: map[bool]string{true: "EXISTING", false: "NEW"}[ok]})
	}
	// Equipment, source rows, points, meters: present → must match exactly (no silent update).
	for _, e := range d.equipment {
		var typeCode, slot sql.NullString
		err := q.QueryRowContext(ctx, `SELECT type_code, semantic_slot FROM asset_equipment_installations WHERE asset_id=$1 AND equipment_code=$2`, AssetID, e.Code).Scan(&typeCode, &slot)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			add(Item{Kind: "equipment", Key: e.Code, Status: "NEW"})
		case err != nil:
			return p, d, err
		case typeCode.String != e.TypeCode || slot.String != e.Slot:
			add(Item{Kind: "equipment", Key: e.Code, Status: "CONFLICT", Reason: fmt.Sprintf("existing %s/%s, staging %s/%s", typeCode.String, slot.String, e.TypeCode, e.Slot)})
		default:
			add(Item{Kind: "equipment", Key: e.Code, Status: "EXISTING"})
		}
	}
	for _, r := range s.Records {
		var raw string
		err := q.QueryRowContext(ctx, `SELECT raw::text FROM onboarding_source_rows WHERE source_sha256=$1 AND source_sheet=$2 AND source_row=$3`, s.Source.SHA256, r.SourceSheet, r.SourceRow).Scan(&raw)
		key := fmt.Sprintf("%s!%d", r.SourceSheet, r.SourceRow)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			add(Item{Kind: "source_row", Key: key, Status: "NEW"})
		case err != nil:
			return p, d, err
		case !jsonEqual([]byte(raw), r.rawJSON):
			add(Item{Kind: "source_row", Key: key, Status: "CONFLICT", Reason: "stored original row differs from the staging document"})
		default:
			add(Item{Kind: "source_row", Key: key, Status: "EXISTING"})
		}
	}
	for _, pt := range d.points {
		ok, err := exists(`SELECT count(*) FROM telemetry_points WHERE asset_id=$1 AND point_code=$2`, AssetID, pt.Code)
		if err != nil {
			return p, d, err
		}
		add(Item{Kind: "point", Key: pt.Code, Status: map[bool]string{true: "EXISTING", false: "NEW"}[ok]})
	}
	for _, m := range d.meters {
		ok, err := exists(`SELECT count(*) FROM meter_definitions WHERE asset_id=$1 AND meter_code=$2`, AssetID, m.Code)
		if err != nil {
			return p, d, err
		}
		add(Item{Kind: "meter", Key: m.Code, Status: map[bool]string{true: "EXISTING", false: "NEW"}[ok]})
	}
	for _, r := range d.rules {
		ok, err := exists(`SELECT count(*) FROM maintenance_rules WHERE asset_id=$1 AND rule_code=$2`, AssetID, r.Code)
		if err != nil {
			return p, d, err
		}
		add(Item{Kind: "rule", Key: r.Code, Status: map[bool]string{true: "EXISTING", false: "NEW"}[ok]})
	}
	for _, pa := range d.pending {
		ok, err := exists(`SELECT count(*) FROM asset_pending_attributes WHERE asset_id=$1 AND subject=$2 AND attribute=$3`, AssetID, pa.Subject, pa.Attribute)
		if err != nil {
			return p, d, err
		}
		add(Item{Kind: "pending", Key: pa.Subject + "." + pa.Attribute, Status: map[bool]string{true: "EXISTING", false: "NEW"}[ok]})
	}
	p.Counts["issue"] = len(d.issues)
	b, err := json.Marshal(struct {
		Target  Target `json:"target"`
		Staging string `json:"staging"`
		Items   []Item `json:"items"`
	}{p.Target, p.StagingSHA256, p.Items})
	if err != nil {
		return p, d, err
	}
	digest := sha256.Sum256(b)
	p.Digest = hex.EncodeToString(digest[:])
	return p, d, nil
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------------------------
// Apply
// ---------------------------------------------------------------------------------------------

// Apply re-plans inside one transaction (under an advisory lock), requires the approved digest
// and a conflict-free plan, and writes only NEW objects; an all-EXISTING plan is a noop.
func Apply(ctx context.Context, db *sql.DB, staging []byte, approved, actor, requestID string) (Record, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return Record{}, ErrActorRequired
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockClass); err != nil {
		return Record{}, err
	}
	p, d, err := PlanImport(ctx, tx, staging)
	if err != nil {
		return Record{}, err
	}
	if p.Digest != approved {
		return Record{}, ErrDigestMismatch
	}
	if len(p.Conflicts) > 0 {
		return Record{}, fmt.Errorf("%w: %d conflicts (first: %s %s: %s)", ErrConflict, len(p.Conflicts), p.Conflicts[0].Kind, p.Conflicts[0].Key, p.Conflicts[0].Reason)
	}
	actorID := "host:" + strings.TrimPrefix(actor, "host:")
	rec := Record{Target: p.Target, Actor: actorID, Digest: approved, Created: map[string]int{}, ProjectID: ProjectID, AssetID: AssetID}
	total := 0
	for _, n := range p.New {
		total += n
	}
	if total == 0 {
		rec.Result = "noop"
		if err = auditlog.Write(ctx, tx, auditlog.Event{Actor: actorID, Project: ProjectID, Action: "onboarding_applied", Result: "noop", RequestID: requestID,
			Change: map[string]any{"kind": importKind, "staging": p.StagingSHA256}}); err != nil {
			return rec, err
		}
		return rec, tx.Commit()
	}
	w := &writer{ctx: ctx, tx: tx, d: d, p: p, actor: actorID, requestID: requestID, created: rec.Created}
	if err = w.run(); err != nil {
		return rec, err
	}
	rec.ImportID = id("IMP", p.StagingSHA256+"|"+approved)
	summary, _ := json.Marshal(map[string]any{"created": rec.Created, "counts": p.Counts, "issues": len(d.issues)})
	if _, err = tx.ExecContext(ctx, `INSERT INTO onboarding_imports(id,kind,project_id,asset_id,source_filename,source_sha256,staging_sha256,approved_digest,applied_by,result,summary)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'success',$10::jsonb)`, rec.ImportID, importKind, ProjectID, AssetID, p.SourceFile, p.SourceSHA256, p.StagingSHA256, approved, actorID, string(summary)); err != nil {
		return rec, err
	}
	if err = auditlog.Write(ctx, tx, auditlog.Event{Actor: actorID, Project: ProjectID, Action: "onboarding_applied", Result: "success", RequestID: requestID,
		Change: map[string]any{"kind": importKind, "staging": p.StagingSHA256, "source": p.SourceSHA256, "created": rec.Created}}); err != nil {
		return rec, err
	}
	rec.Result = "success"
	return rec, tx.Commit()
}
