package metering

// R9 收口 — real-asset readiness (实车接入准备). Every decision that turns an imported, unverified
// fact into something a REAL maintenance conclusion may rely on is an explicit, optimistic-locked,
// audited write with history (entity_revisions / point_config_changes):
//
//   - nameplate / location / other pending attributes  → DecidePending (CONFIRMED / REJECTED + basis);
//     a confirmed rated capacity / span is copied to the asset ledger
//   - actual installation date of a slot's instance     → SetInstalledAt
//   - point address, active level, edge, data type, unit → ConfirmPoint (point_config_changes,
//     resolves the point's open review issues with the change; activates its meters)
//   - initial meter baseline                            → RecordBaseline (existing, write-once)
//   - rule approval                                     → ApproveRule (web; drafts stay drafts until then)
//   - statutory inspection dates                        → SetStatutoryInspection
//
// Unconfirmed points remain usable for SIMULATION acceptance, never as a trusted REAL meter
// (meterEvidence → UNCONFIRMED_POINT).

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

// revision writes one entity_revisions row (same canonical form as the asset ledger).
func revision(ctx context.Context, tx *sql.Tx, project, asset, entityType, entityID, actor, requestID, reason string, fields []string, snapshot any) error {
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	_, err = tx.ExecContext(ctx, `INSERT INTO entity_revisions(project_id,asset_id,entity_type,entity_id,revision,recorded_at,effective_at,changed_by,reason,request_id,changed_fields,snapshot_json,snapshot_sha256)
VALUES($1,$2,$3,$4,(SELECT COALESCE(MAX(revision),0)+1 FROM entity_revisions WHERE entity_type=$3 AND entity_id=$4),now(),now(),$5,$6,$7,$8,$9,$10)`,
		project, asset, entityType, entityID, actor, reason, requestID, fields, b, hex.EncodeToString(sum[:]))
	return err
}

func basisOK(s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if n := len([]rune(s)); n < 2 || n > max {
		return "", fmt.Errorf("%w: basis (2–%d characters) required", ErrInvalid, max)
	}
	return s, nil
}

func assetProject(ctx context.Context, tx *sql.Tx, assetID string) (string, error) {
	var project string
	err := tx.QueryRowContext(ctx, `SELECT project_id FROM asset_devices WHERE id=$1`, assetID).Scan(&project)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return project, err
}

// ---------------------------------------------------------------------------------------------
// Pending attributes (nameplate, location, rope count …)
// ---------------------------------------------------------------------------------------------

type PendingDecision struct {
	Status string          `json:"status"` // CONFIRMED | REJECTED
	Value  json.RawMessage `json:"value"`  // CONFIRMED: the confirmed value (number for ratedCapacityT / spanM)
	Basis  string          `json:"basis"`  // evidence: nameplate photo id, drawing, site record …
}

// DecidePending confirms or rejects one pending attribute (optimistic version). A confirmed
// asset rated capacity / span is written to the asset ledger (asset version + revision).
func DecidePending(ctx context.Context, db *sql.DB, assetID, pendingID string, version int64, in PendingDecision, actor, requestID string) error {
	basis, err := basisOK(in.Basis, 300)
	if err != nil {
		return err
	}
	if in.Status != "CONFIRMED" && in.Status != "REJECTED" {
		return fmt.Errorf("%w: status CONFIRMED | REJECTED", ErrInvalid)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	project, err := assetProject(ctx, tx, assetID)
	if err != nil {
		return err
	}
	var subject, attribute, status string
	var current int64
	err = tx.QueryRowContext(ctx, `SELECT subject, attribute, status, version FROM asset_pending_attributes WHERE id=$1 AND asset_id=$2 FOR UPDATE`, pendingID, assetID).Scan(&subject, &attribute, &status, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if current != version {
		return fmt.Errorf("%w: the attribute changed (version %d)", ErrConflict, current)
	}
	var value any
	if in.Status == "CONFIRMED" {
		if len(in.Value) == 0 || string(in.Value) == "null" {
			return fmt.Errorf("%w: a confirmed value is required", ErrInvalid)
		}
		if !json.Valid(in.Value) || len(in.Value) > 2000 {
			return fmt.Errorf("%w: value must be valid JSON (≤ 2000 bytes)", ErrInvalid)
		}
		value = string(in.Value)
		if subject == "asset" && (attribute == "ratedCapacityT" || attribute == "spanM") {
			var n float64
			if json.Unmarshal(in.Value, &n) != nil || n <= 0 || n > 1000 {
				return fmt.Errorf("%w: %s must be a positive number", ErrInvalid, attribute)
			}
			col := map[string]string{"ratedCapacityT": "rated_capacity_t", "spanM": "span_m"}[attribute]
			if _, err = tx.ExecContext(ctx, `UPDATE asset_devices SET `+col+`=$2, version=version+1, updated_at=now() WHERE id=$1`, assetID, n); err != nil {
				return err
			}
			if err = revision(ctx, tx, project, assetID, "asset", assetID, actor, requestID, "铭牌确认："+basis, []string{attribute}, map[string]any{attribute: n, "basis": basis}); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_pending_attributes SET status=$2, confirmed_value=$3::jsonb, decided_by=$4, decided_at=now(), decision_basis=$5, version=version+1 WHERE id=$1`,
		pendingID, in.Status, value, actor, basis); err != nil {
		return err
	}
	if err = revision(ctx, tx, project, assetID, "asset_pending_attribute", pendingID, actor, requestID, basis, []string{"status", "confirmedValue"},
		map[string]any{"subject": subject, "attribute": attribute, "from": status, "to": in.Status, "value": json.RawMessage(nullJSON(in.Value)), "basis": basis}); err != nil {
		return err
	}
	if err = auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: project, Action: "pending_attribute_decided", Result: in.Status, RequestID: requestID, Change: map[string]any{"subject": subject, "attribute": attribute}}); err != nil {
		return err
	}
	return tx.Commit()
}

func nullJSON(b json.RawMessage) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

// ---------------------------------------------------------------------------------------------
// Actual installation date of a slot's current instance
// ---------------------------------------------------------------------------------------------

// SetInstalledAt records the actual installation date of the slot's current instance (never the
// import time; never in the future). Optimistic on the installation version.
func SetInstalledAt(ctx context.Context, db *sql.DB, assetID, slot string, version int64, installedAt time.Time, basis, actor, requestID string, now time.Time) error {
	basis, err := basisOK(basis, 300)
	if err != nil {
		return err
	}
	if installedAt.IsZero() || installedAt.After(now) || installedAt.Year() < 1990 {
		return fmt.Errorf("%w: installedAt must be a past date", ErrInvalid)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	project, err := assetProject(ctx, tx, assetID)
	if err != nil {
		return err
	}
	var id string
	var current int64
	var before sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT id, version, installed_at FROM asset_equipment_installations WHERE asset_id=$1 AND semantic_slot=$2 AND valid_to IS NULL FOR UPDATE`, assetID, slot).Scan(&id, &current, &before)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if current != version {
		return fmt.Errorf("%w: the installation changed (version %d)", ErrConflict, current)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_equipment_installations SET installed_at=$2, version=version+1, updated_at=now() WHERE id=$1`, id, installedAt); err != nil {
		return err
	}
	var prev any
	if before.Valid {
		prev = before.Time
	}
	if err = revision(ctx, tx, project, assetID, "equipment", id, actor, requestID, basis, []string{"installedAt"}, map[string]any{"slot": slot, "installedAt": installedAt, "previous": prev, "basis": basis}); err != nil {
		return err
	}
	if err = auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: project, Action: "equipment_installed_at_recorded", Result: "success", RequestID: requestID, Change: map[string]any{"slot": slot}}); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------------------------
// Point confirmation
// ---------------------------------------------------------------------------------------------

type PointConfirmation struct {
	Status        string   `json:"status"`        // CONFIRMED | REJECTED
	PLCAddressRaw *string  `json:"plcAddressRaw"` // the address as verified on site (nil = unchanged)
	ActiveLevel   *string  `json:"activeLevel"`   // HIGH | LOW
	Edge          *string  `json:"edge"`          // RISING | FALLING | BOTH
	RawDataType   *string  `json:"rawDataType"`   // INT | FLOAT | STRING | BOOL
	Unit          *string  `json:"unit"`          // count | h | t | none
	Basis         string   `json:"basis"`         // what was checked (PLC program, I/O test, photo …)
	ResolveIssues []string `json:"resolveIssues"` // open review issues of this point settled by this change
}

var dbxAddress = regexp.MustCompile(`^DB(\d{1,5})\.DBX(\d{1,5})\.([0-7])$`)
var wordAddress = regexp.MustCompile(`^DB(\d{1,5})\.DB[WD](\d{1,5})$`)

type pointState struct {
	Address     *string `json:"plcAddressRaw"`
	Kind        string  `json:"addressKind"`
	ActiveLevel *string `json:"activeLevel"`
	Edge        *string `json:"edge"`
	RawDataType string  `json:"rawDataType"`
	Unit        string  `json:"unit"`
	Status      string  `json:"confirmationStatus"`
}

// ConfirmPoint confirms (or rejects) one raw point after the site check. A confirmation needs a
// parseable address for a PLC point (bit addresses need active level and, for counted points,
// the counted edge), settles every open review issue of the point in the same change, and
// activates the point's meters; a rejection disables them. Optimistic on the point version.
func ConfirmPoint(ctx context.Context, db *sql.DB, assetID, pointCode string, version int64, in PointConfirmation, actor, requestID string) error {
	basis, err := basisOK(in.Basis, 300)
	if err != nil {
		return err
	}
	if in.Status != "CONFIRMED" && in.Status != "REJECTED" {
		return fmt.Errorf("%w: status CONFIRMED | REJECTED", ErrInvalid)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	project, err := assetProject(ctx, tx, assetID)
	if err != nil {
		return err
	}
	var id, derived, source string
	var current int64
	var before pointState
	err = tx.QueryRowContext(ctx, `SELECT id, version, derived_type, acquisition_source, plc_address_raw, address_kind, active_level, edge, raw_data_type, unit, confirmation_status
FROM telemetry_points WHERE asset_id=$1 AND point_code=$2 FOR UPDATE`, assetID, pointCode).
		Scan(&id, &current, &derived, &source, &before.Address, &before.Kind, &before.ActiveLevel, &before.Edge, &before.RawDataType, &before.Unit, &before.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if current != version {
		return fmt.Errorf("%w: the point changed (version %d)", ErrConflict, current)
	}
	after := before
	after.Status = in.Status
	var plcArea any
	var plcDB, plcByte, plcBit any
	if in.PLCAddressRaw != nil {
		a := strings.TrimSpace(*in.PLCAddressRaw)
		if len(a) > 40 {
			return fmt.Errorf("%w: address too long", ErrInvalid)
		}
		after.Address = &a
	}
	after.Kind = "NONE"
	if after.Address != nil && strings.TrimSpace(*after.Address) != "" && strings.TrimSpace(*after.Address) != "/" {
		addr := strings.TrimSpace(*after.Address)
		if m := dbxAddress.FindStringSubmatch(addr); m != nil {
			after.Kind = "BIT"
			plcArea, plcDB, plcByte, plcBit = "DB", atoi(m[1]), atoi(m[2]), atoi(m[3])
		} else if m := wordAddress.FindStringSubmatch(addr); m != nil {
			after.Kind = "WORD"
			plcArea, plcDB, plcByte = "DB", atoi(m[1]), atoi(m[2])
		} else {
			after.Kind = "UNPARSED"
		}
	}
	for _, f := range []struct {
		dst     **string
		in      *string
		allowed []string
		name    string
	}{{&after.ActiveLevel, in.ActiveLevel, []string{"HIGH", "LOW"}, "activeLevel"}, {&after.Edge, in.Edge, []string{"RISING", "FALLING", "BOTH"}, "edge"}} {
		if f.in != nil {
			if !oneOf(*f.in, f.allowed) {
				return fmt.Errorf("%w: %s", ErrInvalid, f.name)
			}
			v := *f.in
			*f.dst = &v
		}
	}
	if in.RawDataType != nil {
		if !oneOf(*in.RawDataType, []string{"INT", "FLOAT", "STRING", "BOOL"}) {
			return fmt.Errorf("%w: rawDataType", ErrInvalid)
		}
		after.RawDataType = *in.RawDataType
	}
	if in.Unit != nil {
		if !oneOf(*in.Unit, []string{"count", "h", "t", "none"}) {
			return fmt.Errorf("%w: unit", ErrInvalid)
		}
		after.Unit = *in.Unit
	}
	if in.Status == "CONFIRMED" {
		switch {
		case source == "PLC" && after.Kind != "BIT" && after.Kind != "WORD":
			return fmt.Errorf("%w: a PLC point needs a verified DB address (DBn.DBXb.bit or DBn.DBWb)", ErrInvalid)
		case after.Kind == "BIT" && after.ActiveLevel == nil:
			return fmt.Errorf("%w: a bit point needs its active level", ErrInvalid)
		case after.Kind == "BIT" && derived == "EDGE_COUNT" && after.Edge == nil:
			return fmt.Errorf("%w: a counted bit point needs the counted edge", ErrInvalid)
		}
	}
	// Every open issue of the point must be settled by this change before it can be confirmed.
	open := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM point_review_issues WHERE point_id=$1 AND status='OPEN' FOR UPDATE`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var i string
		if err = rows.Scan(&i); err != nil {
			rows.Close()
			return err
		}
		open[i] = true
	}
	rows.Close()
	for _, i := range in.ResolveIssues {
		if !open[i] {
			return fmt.Errorf("%w: issue %s is not an open issue of this point", ErrInvalid, i)
		}
	}
	if in.Status == "CONFIRMED" && len(in.ResolveIssues) != len(open) {
		return fmt.Errorf("%w: %d open review issue(s) of this point must be settled with the confirmation", ErrInvalid, len(open))
	}
	changeID := platform.ID()
	bb, _ := json.Marshal(before)
	ab, _ := json.Marshal(after)
	if _, err = tx.ExecContext(ctx, `INSERT INTO point_config_changes(id,asset_id,point_id,from_version,to_version,before,after,basis,actor,request_id) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8,$9,$10)`,
		changeID, assetID, id, current, current+1, string(bb), string(ab), basis, actor, requestID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE telemetry_points SET plc_address_raw=$2, address_kind=$3, plc_area=$4, plc_db=$5, plc_byte=$6, plc_bit=$7, active_level=$8, edge=$9, raw_data_type=$10, unit=$11,
confirmation_status=$12, quality=$13, reviewed_by=$14, reviewed_at=now(), review_note=$15, version=version+1, updated_at=now() WHERE id=$1`,
		id, after.Address, after.Kind, plcArea, plcDB, plcByte, plcBit, after.ActiveLevel, after.Edge, after.RawDataType, after.Unit, in.Status,
		map[string]string{"CONFIRMED": "VERIFIED", "REJECTED": "SUSPECT"}[in.Status], actor, basis); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	for _, i := range in.ResolveIssues {
		if _, err = tx.ExecContext(ctx, `UPDATE point_review_issues SET status='RESOLVED', resolved_by=$2, resolved_at=now(), resolution=$3, config_change_id=$4 WHERE id=$1 AND status='OPEN'`,
			i, actor, "随点位配置变更处理："+basis, changeID); err != nil {
			return err
		}
	}
	meterStatus := "ACTIVE"
	if in.Status == "REJECTED" {
		meterStatus = "DISABLED"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE meter_definitions SET status=$2, version=version+1 WHERE point_id=$1 AND status<>$2`, id, meterStatus); err != nil {
		return err
	}
	if err = auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: project, Action: "telemetry_point_" + strings.ToLower(in.Status), Result: "success", RequestID: requestID,
		Change: map[string]any{"point": pointCode, "change": changeID, "issues": len(in.ResolveIssues)}}); err != nil {
		return err
	}
	return tx.Commit()
}

func oneOf(v string, list []string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// PointChange is one entry of a point's configuration history.
type PointChange struct {
	ID          string          `json:"id"`
	PointCode   string          `json:"pointCode"`
	FromVersion int64           `json:"fromVersion"`
	ToVersion   int64           `json:"toVersion"`
	Before      json.RawMessage `json:"before"`
	After       json.RawMessage `json:"after"`
	Basis       string          `json:"basis"`
	Actor       string          `json:"actor"`
	ChangedAt   time.Time       `json:"changedAt"`
	Issues      []string        `json:"resolvedIssues"`
}

func PointChanges(ctx context.Context, db *sql.DB, assetID string) ([]PointChange, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.id,p.point_code,c.from_version,c.to_version,c.before::text,c.after::text,c.basis,c.actor,c.changed_at,
COALESCE((SELECT json_agg(i.issue_code ORDER BY i.issue_code) FROM point_review_issues i WHERE i.config_change_id=c.id),'[]')::text
FROM point_config_changes c JOIN telemetry_points p ON p.id=c.point_id WHERE c.asset_id=$1 ORDER BY c.changed_at DESC LIMIT 500`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PointChange{}
	for rows.Next() {
		var v PointChange
		var b, a, issues string
		if err = rows.Scan(&v.ID, &v.PointCode, &v.FromVersion, &v.ToVersion, &b, &a, &v.Basis, &v.Actor, &v.ChangedAt, &issues); err != nil {
			return nil, err
		}
		v.Before, v.After = json.RawMessage(b), json.RawMessage(a)
		_ = json.Unmarshal([]byte(issues), &v.Issues)
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------------------------
// Rule approval and statutory inspection
// ---------------------------------------------------------------------------------------------

type RuleApproval struct {
	IntervalDays  *int      `json:"intervalDays"`
	IntervalMeter *float64  `json:"intervalMeter"`
	EffectiveFrom time.Time `json:"effectiveFrom"`
	Basis         string    `json:"basis"`
}

// ApproveRule makes one DRAFT real rule effective (explicit interval, effective date and basis;
// test-only rules can never be approved). version is the rule row version shown to the approver.
func ApproveRule(ctx context.Context, db *sql.DB, assetID, ruleID string, version int, in RuleApproval, actor, requestID string) error {
	basis, err := basisOK(in.Basis, 300)
	if err != nil {
		return err
	}
	if in.EffectiveFrom.IsZero() {
		return fmt.Errorf("%w: effectiveFrom required", ErrInvalid)
	}
	if (in.IntervalDays != nil && *in.IntervalDays <= 0) || (in.IntervalMeter != nil && *in.IntervalMeter <= 0) {
		return fmt.Errorf("%w: intervals must be positive", ErrInvalid)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	project, err := assetProject(ctx, tx, assetID)
	if err != nil {
		return err
	}
	var status, code string
	var current int
	var testOnly bool
	err = tx.QueryRowContext(ctx, `SELECT approval_status, rule_code, version, test_only FROM maintenance_rules WHERE id=$1 AND asset_id=$2 FOR UPDATE`, ruleID, assetID).Scan(&status, &code, &current, &testOnly)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if testOnly || status != "DRAFT_UNAPPROVED" || current != version {
		return fmt.Errorf("%w: rule is not this unapproved real rule version", ErrConflict)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE maintenance_rules SET approval_status='APPROVED', approved_by=$2, approved_at=now(), effective_from=$3,
interval_days=COALESCE($4,interval_days), interval_meter=COALESCE($5,interval_meter), basis=basis||' ｜ 审批依据：'||$6 WHERE id=$1`,
		ruleID, actor, in.EffectiveFrom, in.IntervalDays, in.IntervalMeter, basis); err != nil {
		return fmt.Errorf("%w: rule cannot be approved with these values (interval required for its type)", ErrInvalid)
	}
	if err = revision(ctx, tx, project, assetID, "maintenance_rule", ruleID, actor, requestID, basis, []string{"approvalStatus", "effectiveFrom"},
		map[string]any{"ruleCode": code, "version": version, "intervalDays": in.IntervalDays, "intervalMeter": in.IntervalMeter, "effectiveFrom": in.EffectiveFrom, "basis": basis}); err != nil {
		return err
	}
	if err = auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: project, Action: "maintenance_rule_approved", Result: "success", RequestID: requestID, Change: map[string]any{"rule": code}}); err != nil {
		return err
	}
	return tx.Commit()
}

type StatutoryInspection struct {
	UseRegistrationNo    *string `json:"useRegistrationNo"`
	LatestInspectionDate string  `json:"latestInspectionDate"` // YYYY-MM-DD
	InspectionValidUntil string  `json:"inspectionValidUntil"` // YYYY-MM-DD
	Basis                string  `json:"basis"`                // report number / certificate
}

// SetStatutoryInspection records the statutory inspection of the asset (optimistic on the asset
// version) and closes the pending "statutoryInspection" attribute with the same evidence.
func SetStatutoryInspection(ctx context.Context, db *sql.DB, assetID string, version int64, in StatutoryInspection, actor, requestID string) (int64, error) {
	basis, err := basisOK(in.Basis, 300)
	if err != nil {
		return 0, err
	}
	latest, e1 := time.Parse("2006-01-02", in.LatestInspectionDate)
	valid, e2 := time.Parse("2006-01-02", in.InspectionValidUntil)
	if e1 != nil || e2 != nil || !valid.After(latest) || latest.After(time.Now()) {
		return 0, fmt.Errorf("%w: latestInspectionDate ≤ today < inspectionValidUntil (YYYY-MM-DD)", ErrInvalid)
	}
	if in.UseRegistrationNo != nil && len(*in.UseRegistrationNo) > 80 {
		return 0, fmt.Errorf("%w: useRegistrationNo too long", ErrInvalid)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var project string
	var current int64
	err = tx.QueryRowContext(ctx, `SELECT project_id, version FROM asset_devices WHERE id=$1 FOR UPDATE`, assetID).Scan(&project, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if current != version {
		return current, fmt.Errorf("%w: the asset changed (version %d)", ErrConflict, current)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_devices SET latest_inspection_date=$2::date, inspection_valid_until=$3::date, use_registration_no=COALESCE($4,use_registration_no), version=version+1, updated_at=now() WHERE id=$1`,
		assetID, in.LatestInspectionDate, in.InspectionValidUntil, in.UseRegistrationNo); err != nil {
		return 0, err
	}
	if err = revision(ctx, tx, project, assetID, "asset", assetID, actor, requestID, "法定检验："+basis, []string{"latestInspectionDate", "inspectionValidUntil", "useRegistrationNo"},
		map[string]any{"latestInspectionDate": in.LatestInspectionDate, "inspectionValidUntil": in.InspectionValidUntil, "useRegistrationNo": in.UseRegistrationNo, "basis": basis}); err != nil {
		return 0, err
	}
	val, _ := json.Marshal(map[string]any{"latestInspectionDate": in.LatestInspectionDate, "inspectionValidUntil": in.InspectionValidUntil, "useRegistrationNo": in.UseRegistrationNo})
	if _, err = tx.ExecContext(ctx, `UPDATE asset_pending_attributes SET status='CONFIRMED', confirmed_value=$2::jsonb, decided_by=$3, decided_at=now(), decision_basis=$4, version=version+1
WHERE asset_id=$1 AND subject='asset' AND attribute='statutoryInspection'`, assetID, string(val), actor, basis); err != nil {
		return 0, err
	}
	if err = auditlog.Write(ctx, tx, auditlog.Event{Actor: actor, Project: project, Action: "statutory_inspection_recorded", Result: "success", RequestID: requestID}); err != nil {
		return 0, err
	}
	return current + 1, tx.Commit()
}

// ---------------------------------------------------------------------------------------------
// Readiness summary
// ---------------------------------------------------------------------------------------------

type ReadinessSlot struct {
	Slot          string     `json:"equipmentSlot"`
	EquipmentID   string     `json:"equipmentInstallationId"`
	EquipmentCode string     `json:"equipmentCode"`
	TypeCode      string     `json:"typeCode"`
	InstalledAt   *time.Time `json:"installedAt"`
	Version       int64      `json:"version"`
}

type ReadinessBaseline struct {
	Slot         string    `json:"equipmentSlot"`
	MeterCode    string    `json:"meterCode"`
	SourceMode   string    `json:"sourceMode"`
	InitialUsage *float64  `json:"initialUsage"`
	Kind         string    `json:"baselineKind"`
	RecordedBy   string    `json:"recordedBy"`
	RecordedAt   time.Time `json:"recordedAt"`
}

type Readiness struct {
	AssetVersion         int64               `json:"assetVersion"`
	RatedCapacityT       *float64            `json:"ratedCapacityT"`
	SpanM                *float64            `json:"spanM"`
	UseRegistrationNo    *string             `json:"useRegistrationNo"`
	LatestInspectionDate *string             `json:"latestInspectionDate"`
	InspectionValidUntil *string             `json:"inspectionValidUntil"`
	PointsTotal          int                 `json:"pointsTotal"`
	PointsConfirmed      int                 `json:"pointsConfirmed"`
	IssuesOpen           int                 `json:"issuesOpen"`
	PendingOpen          int                 `json:"pendingOpen"`
	SlotsTotal           int                 `json:"slotsTotal"`
	SlotsWithDate        int                 `json:"slotsWithInstalledAt"`
	RulesApproved        int                 `json:"rulesApproved"`
	RulesDraft           int                 `json:"rulesDraft"`
	RealRuntime          bool                `json:"realRuntimeRegistered"`
	RealBaselines        int                 `json:"realBaselines"`
	Slots                []ReadinessSlot     `json:"slots"`
	Baselines            []ReadinessBaseline `json:"baselines"`
	Blockers             []string            `json:"blockers"` // why REAL maintenance metering is not yet trusted
}

func ReadinessOf(ctx context.Context, db *sql.DB, assetID string) (Readiness, error) {
	var r Readiness
	err := db.QueryRowContext(ctx, `SELECT a.version, a.rated_capacity_t, a.span_m, a.use_registration_no, to_char(a.latest_inspection_date,'YYYY-MM-DD'), to_char(a.inspection_valid_until,'YYYY-MM-DD'),
(SELECT count(*) FROM telemetry_points p WHERE p.asset_id=a.id), (SELECT count(*) FROM telemetry_points p WHERE p.asset_id=a.id AND p.confirmation_status='CONFIRMED'),
(SELECT count(*) FROM point_review_issues i WHERE i.asset_id=a.id AND i.status='OPEN'), (SELECT count(*) FROM asset_pending_attributes x WHERE x.asset_id=a.id AND x.status='PENDING'),
(SELECT count(*) FROM maintenance_rules m WHERE m.asset_id=a.id AND NOT m.test_only AND m.approval_status='APPROVED'),
(SELECT count(*) FROM maintenance_rules m WHERE m.asset_id=a.id AND NOT m.test_only AND m.approval_status='DRAFT_UNAPPROVED'),
EXISTS(SELECT 1 FROM devices d WHERE d.asset_device_id=a.id AND d.source_mode='REAL'),
(SELECT count(*) FROM equipment_meter_baselines b JOIN asset_equipment_installations e ON e.id=b.equipment_installation_id WHERE e.asset_id=a.id AND e.valid_to IS NULL AND b.source_mode='REAL' AND b.initial_usage IS NOT NULL)
FROM asset_devices a WHERE a.id=$1`, assetID).Scan(&r.AssetVersion, &r.RatedCapacityT, &r.SpanM, &r.UseRegistrationNo, &r.LatestInspectionDate, &r.InspectionValidUntil,
		&r.PointsTotal, &r.PointsConfirmed, &r.IssuesOpen, &r.PendingOpen, &r.RulesApproved, &r.RulesDraft, &r.RealRuntime, &r.RealBaselines)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	rows, err := db.QueryContext(ctx, `SELECT semantic_slot, id, equipment_code, type_code, installed_at, version FROM asset_equipment_installations WHERE asset_id=$1 AND valid_to IS NULL AND semantic_slot IS NOT NULL ORDER BY semantic_slot`, assetID)
	if err != nil {
		return r, err
	}
	r.Slots = []ReadinessSlot{}
	for rows.Next() {
		var s ReadinessSlot
		if err = rows.Scan(&s.Slot, &s.EquipmentID, &s.EquipmentCode, &s.TypeCode, &s.InstalledAt, &s.Version); err != nil {
			rows.Close()
			return r, err
		}
		r.Slots = append(r.Slots, s)
		if s.InstalledAt != nil {
			r.SlotsWithDate++
		}
	}
	rows.Close()
	r.SlotsTotal = len(r.Slots)
	rows, err = db.QueryContext(ctx, `SELECT e.semantic_slot, d.meter_code, b.source_mode, b.initial_usage, b.baseline_kind, b.recorded_by, b.recorded_at
FROM equipment_meter_baselines b JOIN asset_equipment_installations e ON e.id=b.equipment_installation_id JOIN meter_definitions d ON d.id=b.meter_id
WHERE e.asset_id=$1 AND e.valid_to IS NULL ORDER BY b.source_mode, d.meter_code`, assetID)
	if err != nil {
		return r, err
	}
	r.Baselines = []ReadinessBaseline{}
	for rows.Next() {
		var b ReadinessBaseline
		if err = rows.Scan(&b.Slot, &b.MeterCode, &b.SourceMode, &b.InitialUsage, &b.Kind, &b.RecordedBy, &b.RecordedAt); err != nil {
			rows.Close()
			return r, err
		}
		r.Baselines = append(r.Baselines, b)
	}
	rows.Close()
	r.Blockers = []string{}
	add := func(cond bool, s string) {
		if cond {
			r.Blockers = append(r.Blockers, s)
		}
	}
	add(!r.RealRuntime, "未登记真实运行身份（真实硬件未接入）")
	add(r.PointsConfirmed < r.PointsTotal, fmt.Sprintf("点位待现场确认 %d / %d", r.PointsTotal-r.PointsConfirmed, r.PointsTotal))
	add(r.IssuesOpen > 0, fmt.Sprintf("核查事项未处理 %d 项", r.IssuesOpen))
	add(r.PendingOpen > 0, fmt.Sprintf("铭牌 / 位置等待确认属性 %d 项", r.PendingOpen))
	add(r.SlotsWithDate < r.SlotsTotal, fmt.Sprintf("器件实际安装日期未登记 %d / %d", r.SlotsTotal-r.SlotsWithDate, r.SlotsTotal))
	add(r.RealBaselines == 0, "未登记任何真实初始计量基准")
	add(r.RulesApproved == 0, fmt.Sprintf("维护规则均未审批（草案 %d 条，未生效）", r.RulesDraft))
	add(r.InspectionValidUntil == nil, "法定检验日期未登记")
	return r, nil
}
