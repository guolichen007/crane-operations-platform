package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type Location struct {
	Province       string `json:"province"`
	City           string `json:"city"`
	District       string `json:"district"`
	IndustrialPark string `json:"industrialPark"`
	FactoryName    string `json:"factoryName"`
	Workshop       string `json:"workshop"`
	AddressDetail  string `json:"addressDetail"`
}
type Equipment struct {
	EquipmentType   string  `json:"equipmentType"`
	Quantity        int     `json:"quantity"`
	Manufacturer    string  `json:"manufacturer"`
	Model           string  `json:"model"`
	CapacityTons    float64 `json:"capacityTons"`
	SpanM           float64 `json:"spanM"`
	LiftHeightM     float64 `json:"liftHeightM"`
	ControlMode     string  `json:"controlMode"`
	PLCBrand        string  `json:"plcBrand"`
	PLCModel        string  `json:"plcModel"`
	DriveBrand      string  `json:"driveBrand"`
	Notes           string  `json:"notes"`
	ConfidenceLevel string  `json:"confidenceLevel"`
}
type Party struct {
	Role string `json:"role"`
	Name string `json:"name"`
}
type Input struct {
	Version              int         `json:"version"`
	ProjectName          string      `json:"projectName"`
	Customer             string      `json:"customer"`
	Location             Location    `json:"location"`
	SalesStage           string      `json:"salesStage"`
	ProjectType          string      `json:"projectType"`
	InformationSource    string      `json:"informationSource"`
	ConfidenceLevel      string      `json:"confidenceLevel"`
	EstimatedAmount      float64     `json:"estimatedAmount"`
	ExpectedPurchaseDate string      `json:"expectedPurchaseDate"`
	Requirements         []string    `json:"requirements"`
	RequirementNotes     string      `json:"requirementNotes"`
	PlannedEquipment     []Equipment `json:"plannedEquipment"`
	Parties              []Party     `json:"parties"`
	TenderNo             string      `json:"tenderNo"`
	Notes                string      `json:"notes"`
}
type Project struct {
	ProtectedUntil    *time.Time        `json:"protectedUntil"`
	NextReviewAt      *time.Time        `json:"nextReviewAt"`
	GovernanceHistory []GovernanceEvent `json:"governanceHistory,omitempty"`
	Input
	ID               string     `json:"id"`
	RegistrationNo   string     `json:"registrationNo"`
	OwnerSubject     string     `json:"ownerSubject"`
	CreatedBySubject string     `json:"createdBySubject"`
	TeamID           string     `json:"teamId"`
	IntakeStatus     string     `json:"intakeStatus"`
	ProtectionStatus string     `json:"protectionStatus"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	LastActivityAt   *time.Time `json:"lastActivityAt"`
	NextFollowupAt   *time.Time `json:"nextFollowupAt"`
	NextAction       string     `json:"nextAction"`
	Activities       []Activity `json:"activities,omitempty"`
	Audit            []Audit    `json:"audit,omitempty"`
}
type Activity struct {
	Version        int       `json:"version"`
	Type           string    `json:"type"`
	OccurredAt     time.Time `json:"occurredAt"`
	Summary        string    `json:"summary"`
	Result         string    `json:"result"`
	NextAction     string    `json:"nextAction"`
	NextFollowupAt string    `json:"nextFollowupAt"`
}
type Audit struct {
	Subject   string         `json:"subject"`
	Details   map[string]any `json:"details"`
	Action    string         `json:"action"`
	Revision  int            `json:"revision"`
	CreatedAt time.Time      `json:"createdAt"`
}
type User struct {
	Subject     string   `json:"subject"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	TeamID      string   `json:"teamId"`
	CSRF        string   `json:"csrfToken"`
	Permissions []string `json:"permissions"`
	sessionHash string
}

var stages = strings.Fields("L0_LEAD L1_REQUIREMENT L2_TECHNICAL L3_SITE_SURVEY L4_SOLUTION L5_QUOTATION L6_TENDER L7_COMMERCIAL L8_PENDING_CONTRACT WON LOST HOLD")
var sources = strings.Fields("END_CUSTOMER CRANE_MANUFACTURER DESIGN_INSTITUTE EPC SYSTEM_INTEGRATOR AGENT TENDER_PLATFORM HISTORICAL_CUSTOMER INTERNAL_REFERRAL OUTBOUND OTHER")
var confidence = strings.Fields("RUMOR VERBAL CUSTOMER_CONFIRMED TECHNICAL_CONFIRMED DOCUMENT_CONFIRMED TENDER_CONFIRMED")
var requirements = strings.Fields("AUTONOMOUS ANTI_SWAY COLLISION_AVOIDANCE POSITIONING VISION LIDAR REMOTE_OPERATION SAFETY MONITORING OTHER")
var activityTypes = strings.Fields("PHONE WECHAT VISIT TECHNICAL SITE_SURVEY SOLUTION QUOTATION TENDER COMMERCIAL OTHER")

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
func normalize(s string) string {
	s = strings.ToLower(norm.NFKC.String(s))
	s = strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
func fingerprint(p Input) string {
	h := sha256.Sum256([]byte(normalize(p.Customer) + "|" + normalize(p.Location.Province+p.Location.City+p.Location.FactoryName) + "|" + normalize(p.ProjectName)))
	return hex.EncodeToString(h[:])
}
func canRead(u User, p Project) bool {
	return u.Subject == p.OwnerSubject || u.Role == "baobei_director" || u.Role == "baobei_manager" && u.TeamID != "" && u.TeamID == p.TeamID
}
func canWrite(u User, p Project) bool { return u.Role == "baobei_sales" && u.Subject == p.OwnerSubject }
func editable(s string) bool          { return s == "DRAFT" || s == "RETURNED" }
func validateInput(p *Input, submit bool) error {
	if p.Requirements == nil {
		p.Requirements = []string{}
	}
	if p.PlannedEquipment == nil {
		p.PlannedEquipment = []Equipment{}
	}
	if p.Parties == nil {
		p.Parties = []Party{}
	}
	p.ProjectName = strings.TrimSpace(p.ProjectName)
	p.Customer = strings.TrimSpace(p.Customer)
	if len(p.ProjectName) > 600 || len(p.Customer) > 600 || len(p.RequirementNotes) > 12000 || len(p.Notes) > 12000 || p.EstimatedAmount < 0 || math.IsNaN(p.EstimatedAmount) || p.EstimatedAmount > 1e14 || len(p.Parties) > 30 || len(p.Requirements) > 10 || len(p.PlannedEquipment) > 30 {
		return errors.New("字段超过允许长度或金额范围")
	}
	if p.SalesStage == "" {
		p.SalesStage = "L0_LEAD"
	}
	if !contains(stages, p.SalesStage) {
		return errors.New("销售阶段不正确")
	}
	if p.InformationSource != "" && !contains(sources, p.InformationSource) || p.ConfidenceLevel != "" && !contains(confidence, p.ConfidenceLevel) {
		return errors.New("来源或可信度不正确")
	}
	if p.ExpectedPurchaseDate != "" {
		if _, e := time.Parse("2006-01-02", p.ExpectedPurchaseDate); e != nil {
			return errors.New("采购日期不正确")
		}
	}
	seen := map[string]bool{}
	for _, r := range p.Requirements {
		if !contains(requirements, r) || seen[r] {
			return errors.New("需求分类不正确或重复")
		}
		seen[r] = true
	}
	for _, e := range p.PlannedEquipment {
		if e.Quantity < 1 || e.Quantity > 10000 || e.CapacityTons < 0 || e.SpanM < 0 || e.LiftHeightM < 0 {
			return errors.New("设备数量或尺寸不正确")
		}
	}
	for _, v := range p.Parties {
		if !contains(strings.Fields("EPC DESIGN_INSTITUTE CRANE_MANUFACTURER SYSTEM_INTEGRATOR AGENT OTHER"), v.Role) || strings.TrimSpace(v.Name) == "" {
			return errors.New("关系方不正确")
		}
	}
	if p.ProjectType != "" && !contains([]string{"NEW_BUILD", "RETROFIT", "EXPANSION"}, p.ProjectType) {
		return errors.New("项目类型不正确")
	}
	if submit && (p.ProjectName == "" || p.Customer == "" || p.Location.Province == "" || p.Location.City == "" || p.ProjectType == "" || p.RequirementNotes == "" || len(p.Requirements) == 0 || p.InformationSource == "" || p.ConfidenceLevel == "") {
		return errors.New("请填写客户、省市、项目名称、类型、需求、来源及可信度")
	}
	return nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Duplicate struct {
	Signals     []string  `json:"signals"`
	CandidateID string    `json:"candidateId,omitempty"`
	Score       int       `json:"score"`
	Risk        string    `json:"risk"`
	Region      string    `json:"region"`
	CreatedAt   time.Time `json:"createdAt"`
	SalesStage  string    `json:"salesStage"`
	Message     string    `json:"message"`
	ProjectName string    `json:"projectName,omitempty"`
}

func score(p, q Input, similarity float64, sameCustomer bool) int {
	if normalize(p.TenderNo) != "" && normalize(p.TenderNo) == normalize(q.TenderNo) {
		return 100
	}
	n := 0
	if sameCustomer {
		n += 35
	}
	samePlace := normalize(p.Location.Province+p.Location.City+p.Location.FactoryName+p.Location.Workshop) == normalize(q.Location.Province+q.Location.City+q.Location.FactoryName+q.Location.Workshop)
	if samePlace {
		n += 20
	}
	n += int(15 * similarity)
	if sameCustomer && p.Location.FactoryName != "" && samePlace && similarity >= 0.8 {
		return 95
	}
	if len(p.PlannedEquipment) > 0 && len(q.PlannedEquipment) > 0 && p.PlannedEquipment[0].EquipmentType == q.PlannedEquipment[0].EquipmentType && p.PlannedEquipment[0].Quantity == q.PlannedEquipment[0].Quantity {
		n += 10
	}
	for _, v := range p.Requirements {
		if contains(q.Requirements, v) {
			n += 10
			break
		}
	}
	if p.ExpectedPurchaseDate != "" && p.ExpectedPurchaseDate == q.ExpectedPurchaseDate {
		n += 5
	}
	if p.ProjectType != "" && p.ProjectType == q.ProjectType {
		n += 5
	}
	return n
}

func duplicateSignals(p, q Input, similarity float64, sameCustomer bool) []string {
	out := []string{}
	if p.TenderNo != "" && normalize(p.TenderNo) == normalize(q.TenderNo) {
		out = append(out, "相同招标编号")
	}
	if sameCustomer {
		out = append(out, "相同客户或别名")
	}
	if normalize(p.Location.Province+p.Location.City+p.Location.FactoryName+p.Location.Workshop) == normalize(q.Location.Province+q.Location.City+q.Location.FactoryName+q.Location.Workshop) {
		out = append(out, "相同位置")
	}
	if similarity >= 0.8 {
		out = append(out, "项目名称高度相似")
	}
	if len(p.PlannedEquipment) > 0 && len(q.PlannedEquipment) > 0 && p.PlannedEquipment[0].EquipmentType == q.PlannedEquipment[0].EquipmentType && p.PlannedEquipment[0].Quantity == q.PlannedEquipment[0].Quantity {
		out = append(out, "相同设备类型及数量")
	}
	for _, r := range p.Requirements {
		if contains(q.Requirements, r) {
			out = append(out, "需求相同")
			break
		}
	}
	return out
}
