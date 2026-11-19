// Package onboarding imports the 老河口 first real asset (LHK-CRANE-001) from the reviewed staging
// document deploy/onboarding/lhk/LHK-首台行车接入暂存清单.json — R9 Stage A4/A5 + B2 point model.
//
// Rules (OWNER R9):
//   - plan → apply → verify; plan is read-only; apply requires the digest of the approved plan
//     (bound to the target database and schema) and runs in ONE transaction; verify reports drift;
//   - the original 55 rows are stored losslessly (onboarding_source_rows: sheet, row, raw cells,
//     staging proposal, review flags, source SHA256); 51 equipment rows become Equipment
//     installations on stable slots, the 4 operation rows (作业统计 / 历史故障) never do;
//   - every inferred value stays visibly pending: model capacity / span (PENDING_NAMEPLATE),
//     coordinates / dates / counters / serials NULL, points UNCONFIRMED, meters PENDING_REVIEW,
//     rules DRAFT_UNAPPROVED, rope count / LiDAR dimension / RFID entity UNCONFIRMED;
//   - nothing is guessed into a wrong category: equipment without a fitting type gets one of the
//     R9 types; the LiDAR whose 2D/3D is unknown is LIDAR; rows without a source keep NONE;
//   - writes reuse the existing tables (projects, asset_devices, asset_locations,
//     asset_installations, manufacturers, equipment_catalog_items, asset_equipment_installations)
//     with entity revisions and audit rows, never ad-hoc SQL from an operator;
//   - re-applying the same document is a noop (no duplicate project, asset, equipment or model);
//     a CHANGED document is refused (diff report) — corrections go through the business UI.
package onboarding

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed lhk_staging.json
var embeddedStaging []byte

// Staging is the reviewed staging document (format crane-lhk-onboarding-staging-v1).
type Staging struct {
	Format  string `json:"format"`
	Purpose string `json:"purpose"`
	Source  struct {
		Filename      string   `json:"filename"`
		SHA256        string   `json:"sha256"`
		Sheets        []string `json:"sheets"`
		NonemptySheet string   `json:"nonempty_sheet"`
		Title         string   `json:"title"`
	} `json:"source"`
	AssetDraft struct {
		ProposedProjectCode       string   `json:"proposed_project_code"`
		ProposedAssetCode         string   `json:"proposed_asset_code"`
		DisplayName               string   `json:"display_name"`
		Model                     string   `json:"model"`
		RatedLoadTCandidate       *float64 `json:"rated_load_t_candidate"`
		SpanMCandidate            *float64 `json:"span_m_candidate"`
		ModelInterpretationStatus string   `json:"model_interpretation_status"`
		PhysicalAsset             bool     `json:"physical_asset"`
		ActualLocation            struct {
			PlaceLabel       string   `json:"place_label"`
			Address          *string  `json:"address"`
			Longitude        *float64 `json:"longitude"`
			Latitude         *float64 `json:"latitude"`
			Workshop         *string  `json:"workshop"`
			CoordinateSystem string   `json:"coordinate_system"`
		} `json:"actual_location"`
		SerialNumber         *string `json:"serial_number"`
		InstalledAt          *string `json:"installed_at"`
		CommissionedAt       *string `json:"commissioned_at"`
		RegistrationNo       *string `json:"registration_no"`
		InspectionValidUntil *string `json:"inspection_valid_until"`
	} `json:"asset_draft"`
	RuntimeDraft struct {
		ProposedID             string `json:"proposed_id"`
		SourceMode             string `json:"sourceMode"`
		RealConnect            bool   `json:"real_connected"`
		RealRegistrationStatus string `json:"real_registration_status"`
		SimulatedNeverReal     bool   `json:"simulated_values_must_never_update_real_meters"`
	} `json:"runtime_draft"`
	Counts struct {
		Total                  int              `json:"total"`
		EquipmentCandidateRows int              `json:"equipment_candidate_rows"`
		OperationRows          int              `json:"operation_rows"`
		DuplicateAddressGroups map[string][]int `json:"duplicate_address_groups"`
		ByRawType              map[string]int   `json:"by_raw_type"`
		NonemptyPLCAddressRows int              `json:"nonempty_plc_address_rows"`
		DistinctPLCAddresses   int              `json:"distinct_plc_address_strings"`
		ByGroup                map[string]int   `json:"by_group"`
	} `json:"counts"`
	ProposedNewEquipmentTypes []string        `json:"proposed_new_equipment_types"`
	Records                   []Row           `json:"records"`
	SimulationAcceptance      json.RawMessage `json:"simulation_acceptance"`
}

// Row is one original spreadsheet row.
type Row struct {
	SourceSheet string `json:"source_sheet"`
	SourceRow   int    `json:"source_row"`
	SourceIndex int    `json:"source_index"`
	Group       string `json:"group"`
	Name        string `json:"name"`
	Raw         struct {
		VariableType string  `json:"variable_type"`
		PLCAddress   *string `json:"plc_address"`
		Statistic    *string `json:"statistic"`
		BrandModel   *string `json:"brand_model"`
		LifeText     *string `json:"life_text"`
		Remarks      *string `json:"remarks"`
	} `json:"raw"`
	Proposed struct {
		StableSlot             *string `json:"stable_slot"`
		PointCode              string  `json:"point_code"`
		EquipmentTypeCandidate *string `json:"equipment_type_candidate"`
		NewTypeRequired        bool    `json:"new_type_required"`
		MetricCandidate        string  `json:"metric_candidate"`
		PhysicalInputStatus    *string `json:"physical_input_status"`
		RuleStatus             *string `json:"rule_status"`
		ActualBaseline         *string `json:"actual_baseline"`
		InstalledAt            *string `json:"installed_at"`
	} `json:"proposed"`
	ReviewFlags []string        `json:"review_flags"`
	rawJSON     json.RawMessage // exact original raw object (lossless)
	propJSON    json.RawMessage
}

// Embedded returns the staging document compiled into this binary and its SHA256.
func Embedded() ([]byte, string) {
	sum := sha256.Sum256(embeddedStaging)
	return embeddedStaging, hex.EncodeToString(sum[:])
}

var ErrStaging = errors.New("invalid staging document")

// Parse decodes and validates the staging document (closed schema; every count cross-checked).
func Parse(b []byte) (Staging, error) {
	var s Staging
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return s, fmt.Errorf("%w: %v", ErrStaging, err)
	}
	// Keep each record's raw / proposed objects byte-exact for lossless storage.
	var loose struct {
		Records []struct {
			Raw      json.RawMessage `json:"raw"`
			Proposed json.RawMessage `json:"proposed"`
		} `json:"records"`
	}
	if err := json.Unmarshal(b, &loose); err != nil || len(loose.Records) != len(s.Records) {
		return s, fmt.Errorf("%w: records", ErrStaging)
	}
	for i := range s.Records {
		s.Records[i].rawJSON, s.Records[i].propJSON = loose.Records[i].Raw, loose.Records[i].Proposed
	}
	return s, s.validate()
}

var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (s Staging) validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrStaging}, a...)...)
	}
	if s.Format != "crane-lhk-onboarding-staging-v1" {
		return bad("format %q", s.Format)
	}
	if !sha256Pattern.MatchString(s.Source.SHA256) || s.Source.Filename == "" || s.Source.NonemptySheet == "" {
		return bad("source provenance")
	}
	if s.AssetDraft.ProposedAssetCode == "" || s.AssetDraft.ProposedProjectCode == "" || !s.AssetDraft.PhysicalAsset {
		return bad("asset draft")
	}
	if s.RuntimeDraft.SourceMode != "SIMULATION" || s.RuntimeDraft.RealConnect || !s.RuntimeDraft.SimulatedNeverReal || !strings.HasPrefix(s.RuntimeDraft.ProposedID, "SIM-") {
		return bad("runtime draft must stay SIMULATION")
	}
	if len(s.Records) != s.Counts.Total || s.Counts.Total != s.Counts.EquipmentCandidateRows+s.Counts.OperationRows {
		return bad("record counts %d / %d", len(s.Records), s.Counts.Total)
	}
	seenRow, seenIdx, seenPoint, seenSlot := map[int]bool{}, map[int]bool{}, map[string]bool{}, map[string]bool{}
	equipment, operation := 0, 0
	groups := map[string]int{}
	for _, r := range s.Records {
		if r.SourceSheet != s.Source.NonemptySheet || r.SourceRow < 1 || r.SourceIndex < 1 || seenRow[r.SourceRow] || seenIdx[r.SourceIndex] {
			return bad("row %d/%d duplicate or outside the source sheet", r.SourceRow, r.SourceIndex)
		}
		seenRow[r.SourceRow], seenIdx[r.SourceIndex] = true, true
		if !pointCodePattern.MatchString(r.Proposed.PointCode) || seenPoint[r.Proposed.PointCode] {
			return bad("point code %q", r.Proposed.PointCode)
		}
		seenPoint[r.Proposed.PointCode] = true
		groups[r.Group]++
		switch r.Raw.VariableType {
		case "INT", "FLOAT", "STRING":
		default:
			return bad("row %d variable type %q", r.SourceRow, r.Raw.VariableType)
		}
		if r.Proposed.StableSlot != nil {
			equipment++
			if seenSlot[*r.Proposed.StableSlot] {
				return bad("slot %s duplicated", *r.Proposed.StableSlot)
			}
			seenSlot[*r.Proposed.StableSlot] = true
		} else {
			operation++
		}
	}
	if equipment != s.Counts.EquipmentCandidateRows || operation != s.Counts.OperationRows {
		return bad("equipment %d / operation %d", equipment, operation)
	}
	for g, n := range s.Counts.ByGroup {
		if groups[g] != n {
			return bad("group %s count", g)
		}
	}
	return nil
}

var pointCodePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)

// ---------------------------------------------------------------------------------------------
// Mapping decisions (deterministic, documented; every one reviewable in the plan).
// ---------------------------------------------------------------------------------------------

// Address is the parsed PLC address. Only an unambiguous Siemens DBx.DBX<byte>.<bit> is parsed;
// anything else keeps NONE / UNPARSED and the original text.
type Address struct {
	Raw  *string `json:"raw"`
	Kind string  `json:"kind"` // BIT | NONE | UNPARSED
	Area string  `json:"area,omitempty"`
	DB   *int    `json:"db,omitempty"`
	Byte *int    `json:"byte,omitempty"`
	Bit  *int    `json:"bit,omitempty"`
}

var dbxBit = regexp.MustCompile(`^DB(\d{1,5})\.DBX(\d{1,5})\.([0-7])$`)

func parseAddress(raw *string) Address {
	a := Address{Raw: raw, Kind: "NONE"}
	if raw == nil || strings.TrimSpace(*raw) == "" || strings.TrimSpace(*raw) == "/" {
		return a
	}
	m := dbxBit.FindStringSubmatch(strings.TrimSpace(*raw))
	if m == nil {
		a.Kind = "UNPARSED" // e.g. DB505.DBX66 (missing bit index) — never completed by guessing
		return a
	}
	db, _ := strconv.Atoi(m[1])
	by, _ := strconv.Atoi(m[2])
	bit, _ := strconv.Atoi(m[3])
	a.Kind, a.Area, a.DB, a.Byte, a.Bit = "BIT", "DB", &db, &by, &bit
	return a
}

// equipmentType returns the Equipment type for a row: the staging candidate when it is a
// registered type, otherwise the R9 decision (LiDAR of unknown dimension → LIDAR).
func equipmentType(r Row) (string, bool) {
	if r.Proposed.EquipmentTypeCandidate != nil && *r.Proposed.EquipmentTypeCandidate != "" {
		return *r.Proposed.EquipmentTypeCandidate, false
	}
	if r.Proposed.StableSlot != nil && hasFlag(r, "LIDAR_2D_OR_3D_UNCONFIRMED") {
		return "LIDAR", true
	}
	return "", false
}

func hasFlag(r Row, f string) bool {
	for _, v := range r.ReviewFlags {
		if v == f {
			return true
		}
	}
	return false
}

// derived returns (derived type, unit, acquisition source) of the raw point.
func derived(r Row) (string, string, string) {
	addr := parseAddress(r.Raw.PLCAddress)
	source := "UNKNOWN"
	switch {
	case addr.Kind == "BIT" || addr.Kind == "UNPARSED":
		source = "PLC"
	case hasFlag(r, "NON_PLC_SOURCE_UNKNOWN"):
		source = "UNKNOWN"
	}
	switch r.Proposed.MetricCandidate {
	case "cycles":
		return "EDGE_COUNT", "count", source
	case "hours":
		return "ON_DURATION", "h", source
	case "daily_auto_hours":
		return "DAILY_VALUE", "h", source
	case "production_tonnes":
		return "DAILY_VALUE", "t", source
	case "daily_job_count":
		return "DAILY_VALUE", "count", source
	case "fault_events":
		return "EVENT_LIST", "none", source
	}
	return "STATE", "none", source
}

// Meter is one meter derived from a point.
type Meter struct {
	Code    string `json:"code"`
	Metric  string `json:"metric"`
	Kind    string `json:"kind"` // CUMULATIVE | DAILY
	Unit    string `json:"unit"`
	Scope   string `json:"scope"` // EQUIPMENT | MECHANISM | ASSET
	Slot    string `json:"slot,omitempty"`
	Formula string `json:"formula"`
	Status  string `json:"status"`
}

// mechanismCode maps the source group to a stable mechanism code for mechanism-level meters.
var mechanismCode = map[string]string{"大车机构": "GANTRY", "小车机构": "TROLLEY", "起升机构": "HOIST", "抓斗机构": "GRAB", "门禁系统": "ACCESS", "数采系统": "ACQ", "自动作业": "AUTO"}

func meters(r Row) []Meter {
	code := r.Proposed.PointCode
	_, unit, _ := derived(r)
	out := []Meter{}
	switch r.Proposed.MetricCandidate {
	case "cycles":
		out = append(out, Meter{Code: code + ".cycles", Metric: "cycles", Kind: "CUMULATIVE", Unit: "count", Scope: "EQUIPMENT", Slot: *r.Proposed.StableSlot,
			Formula: "edge-counted rising edges of the raw bit (counted at PLC / edge, cloud persists the cumulative value)", Status: "PENDING_REVIEW"})
		if hasFlag(r, "ONE_SIGNAL_MULTIPLE_DERIVED_METERS") {
			// Remark 「…运行时间也用这个」: the same brake contactor signal also yields the mechanism running hours.
			m := mechanismCode[r.Group]
			out = append(out, Meter{Code: code + ".mechanism_running_hours", Metric: "mechanism_running_hours", Kind: "CUMULATIVE", Unit: "h", Scope: "MECHANISM",
				Formula: "edge-accumulated ON duration of the same raw bit = " + m + " running hours (source remark)", Status: "PENDING_REVIEW"})
		}
	case "hours":
		formula := "edge-accumulated ON duration of the raw bit (h)"
		status := "PENDING_REVIEW"
		if hasFlag(r, "SHARED_HOIST_BRAKE_SOURCE") {
			formula = "PROXY: hoist brake contactor ON duration (shared address); rope usage proxy only, never a discard criterion"
		}
		out = append(out, Meter{Code: code + ".hours", Metric: "hours", Kind: "CUMULATIVE", Unit: unit, Scope: "EQUIPMENT", Slot: *r.Proposed.StableSlot, Formula: formula, Status: status})
	case "daily_auto_hours", "daily_job_count":
		out = append(out, Meter{Code: code + "." + r.Proposed.MetricCandidate, Metric: r.Proposed.MetricCandidate, Kind: "DAILY", Unit: unit, Scope: "ASSET", Formula: "daily value reported by the edge (resets daily at the source)", Status: "PENDING_REVIEW"})
	case "production_tonnes":
		// Source statistic says 小时 for 吨数: unit conflict, kept pending until confirmed.
		out = append(out, Meter{Code: code + ".production_tonnes", Metric: "production_tonnes", Kind: "DAILY", Unit: "t", Scope: "ASSET", Formula: "daily tonnes reported by the edge; source statistic column says 小时 (unit conflict)", Status: "UNIT_CONFLICT_PENDING"})
	}
	return out
}

// Issue is one review item recorded with the import.
type Issue struct {
	Code      string         `json:"code"`
	PointCode string         `json:"pointCode,omitempty"`
	SourceRow int            `json:"sourceRow,omitempty"`
	Detail    map[string]any `json:"detail"`
}

// issues lists every review item: the per-row flags of the staging document plus the explicit
// R9 checks (3 duplicate address groups, 1 missing bit index, thermal relay hours vs counts,
// tonnes unit, DBX bit vs INT/FLOAT published value).
func issues(s Staging) []Issue {
	out := []Issue{}
	thermal := map[string][]string{}
	for _, r := range s.Records {
		for _, f := range r.ReviewFlags {
			if f == "ACTUAL_INSTALLATION_DATE_AND_COUNTER_BASELINE_UNKNOWN" {
				continue // one asset-level issue below, not 51 copies
			}
			out = append(out, Issue{Code: f, PointCode: r.Proposed.PointCode, SourceRow: r.SourceRow, Detail: map[string]any{"name": r.Name, "group": r.Group, "plcAddress": r.Raw.PLCAddress, "variableType": r.Raw.VariableType, "statistic": r.Raw.Statistic}})
		}
		if t, _ := equipmentType(r); t == "THERMAL_OVERLOAD_RELAY" {
			thermal[r.Group] = append(thermal[r.Group], r.Raw.VariableType+"/"+deref(r.Raw.Statistic))
		}
	}
	dups := make([]string, 0, len(s.Counts.DuplicateAddressGroups))
	for a := range s.Counts.DuplicateAddressGroups {
		dups = append(dups, a)
	}
	sort.Strings(dups)
	for _, a := range dups {
		out = append(out, Issue{Code: "DUPLICATE_PLC_ADDRESS_GROUP", Detail: map[string]any{"plcAddress": a, "sourceIndexes": s.Counts.DuplicateAddressGroups[a], "action": "not enabled for REAL until confirmed on site"}})
	}
	out = append(out, Issue{Code: "THERMAL_RELAY_HOURS_VS_COUNT", Detail: map[string]any{"byMechanism": thermal, "question": "大车热继按次数、小车热继按小时统计，需现场确认统计口径"}})
	out = append(out, Issue{Code: "ASSET_INSTALLATION_DATES_AND_BASELINES_UNKNOWN", Detail: map[string]any{"rows": s.Counts.EquipmentCandidateRows, "effect": "时间类规则与器件使用量显示数据不足，直至录入实际安装日期 / 初始累计值"}})
	out = append(out, Issue{Code: "MODEL_INTERPRETATION_PENDING_NAMEPLATE", Detail: map[string]any{"model": s.AssetDraft.Model, "ratedLoadTCandidate": s.AssetDraft.RatedLoadTCandidate, "spanMCandidate": s.AssetDraft.SpanMCandidate}})
	return out
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
