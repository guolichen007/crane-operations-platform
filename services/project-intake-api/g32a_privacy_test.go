package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// G3-2A: minimum disclosure for related duplicate candidates the caller cannot
// read. Two teams that cannot see each other. Only the hidden (team-two)
// candidates change between variants; everything visible stays constant. For
// restricted viewers the whole normalized JSON of every affected endpoint must
// be identical across variants. Authorized viewers must still get full facts.
func TestDatabaseG32APrivacy(t *testing.T) {
	type variant struct {
		name   string
		hidden []hiddenSpec
	}
	base := hiddenSpec{customer: "合成甲钢铁有限公司", province: "江苏省", city: "无锡市", factory: "一厂", equipment: "BRIDGE", qty: 2, stage: "L1_REQUIREMENT", same: true}
	variants := []variant{
		{"A-完整-高相似", []hiddenSpec{base}},
		{"B-不完整-换客户地区设备阶段-招标号匹配", []hiddenSpec{{customer: "合成乙重工有限公司", tender: "ZB-合成-001", province: "浙江省", city: "宁波市", factory: "三厂", equipment: "GANTRY", qty: 1, stage: "L5_QUOTATION", legacy: true}}},
		{"A+B-两个隐藏候选", []hiddenSpec{base, {customer: "合成乙重工有限公司", tender: "ZB-合成-001", province: "浙江省", city: "宁波市", factory: "三厂", equipment: "GANTRY", qty: 1, stage: "L5_QUOTATION", legacy: true}}},
		{"A已裁决+B待裁决", []hiddenSpec{{customer: base.customer, province: base.province, city: base.city, factory: base.factory, equipment: "BRIDGE", qty: 2, stage: "L3_SITE_SURVEY", same: true, decided: true}, {customer: "合成乙重工有限公司", tender: "ZB-合成-001", province: "浙江省", city: "宁波市", factory: "三厂", equipment: "GANTRY", qty: 1, stage: "L5_QUOTATION", legacy: true}}},
	}
	observed := map[string]map[string]map[string]string{}
	full := map[string]map[string]string{}
	for _, v := range variants {
		views, raw := runPrivacyVariant(t, v.hidden)
		observed[v.name] = views
		full[v.name] = raw
	}
	report := &strings.Builder{}
	restricted := []string{"manager", "sales"}
	var leaks []string
	first := variants[0].name
	for _, who := range restricted {
		for key, want := range observed[first][who] {
			for _, v := range variants[1:] {
				got := observed[v.name][who][key]
				if got != want {
					leaks = append(leaks, fmt.Sprintf("%s %s: %q 与 %q 不同", who, key, first, v.name))
					fmt.Fprintf(report, "## %s · %s · %s vs %s\n%s\n---\n%s\n\n", who, key, first, v.name, want, got)
				}
			}
		}
	}
	if path := os.Getenv("BAOBEI_PRIVACY_REPORT"); path != "" {
		var b strings.Builder
		for _, v := range variants {
			fmt.Fprintf(&b, "# 变体 %s\n", v.name)
			keys := make([]string, 0, len(full[v.name]))
			for k := range full[v.name] {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&b, "## %s\n%s\n", k, full[v.name][k])
			}
		}
		b.WriteString("\n# 受限视角差异\n" + report.String())
		_ = os.WriteFile(path, []byte(b.String()), 0o600)
	}
	if len(leaks) > 0 {
		sort.Strings(leaks)
		t.Fatalf("受限视角可从响应推断隐藏候选（%d 处）：\n%s", len(leaks), strings.Join(leaks, "\n"))
	}
	// Authorized roles still see every hidden candidate with full details.
	for _, v := range variants {
		for _, who := range []string{"director", "admin"} {
			raw := full[v.name][who+" comparison"]
			var out struct {
				Cases []struct {
					Candidate *Project `json:"candidate"`
				} `json:"cases"`
			}
			if err := json.Unmarshal([]byte(raw), &out); err != nil {
				t.Fatal(err)
			}
			hiddenSeen := 0
			for _, c := range out.Cases {
				if c.Candidate != nil && c.Candidate.OwnerSubject == "synthetic-c" {
					hiddenSeen++
				}
			}
			if hiddenSeen != len(v.hidden) {
				t.Fatalf("%s %s 应看到 %d 个跨组候选的完整信息，实际 %d", v.name, who, len(v.hidden), hiddenSeen)
			}
		}
	}
}

type hiddenSpec struct {
	customer, province, city, factory, equipment, stage string
	qty                                                 int
	tender                                              string
	same, legacy, decided                               bool
}

var normalizers = []struct {
	re  *regexp.Regexp
	rep string
}{
	{regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`), "<id>"},
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}T[0-9:.]+(Z|[+-]\d{2}:\d{2})`), "<时间>"},
	{regexp.MustCompile(`BB-\d{4}-\d{6}`), "<编号>"},
	{regexp.MustCompile(`[0-9a-f]{64}`), "<哈希>"},
}

// canonical renders JSON with sorted keys and volatile values replaced; array
// order and length, field sets and status values are preserved.
func canonical(t *testing.T, status int, b []byte) string {
	t.Helper()
	var v any
	if len(b) > 0 && json.Unmarshal(b, &v) != nil {
		t.Fatal("non-JSON response", string(b))
	}
	if m, ok := v.(map[string]any); ok {
		delete(m, "requestId")
	}
	out, _ := json.Marshal(v) // encoding/json sorts map keys
	s := string(out)
	for _, n := range normalizers {
		s = n.re.ReplaceAllString(s, n.rep)
	}
	return fmt.Sprintf("HTTP %d %s", status, s)
}

func runPrivacyVariant(t *testing.T, hidden []hiddenSpec) (map[string]map[string]string, map[string]string) {
	t.Helper()
	ctx := context.Background()
	f := g1FixtureFor(t)
	c := login(t, f.server, "synthetic-c") // team two
	doc := func(customer, province, city, factory, equipment string, qty int, stage string) Input {
		in := sample("合成隐私对照项目")
		in.Customer = customer
		in.Location = Location{Province: province, City: city, FactoryName: factory}
		in.PlannedEquipment = []Equipment{{EquipmentType: equipment, Quantity: pointer(qty)}}
		in.SalesStage = stage
		return in
	}
	submitDoc := func(cl testClient, in Input) Project {
		s, b := cl.call("POST", "/api/v1/projects", in)
		p := projectFrom(t, s, b)
		return g1Submit(t, cl, p)
	}
	visible := doc("合成甲钢铁有限公司", "江苏省", "无锡市", "一厂", "BRIDGE", 2, "L2_TECHNICAL")
	visible.TenderNo = "ZB-合成-001" // shared tender number: hidden B matches on it alone
	// Constant visible facts: one team-one candidate owned by sales A, and one
	// older project of the primary's own owner (sales B).
	submitDoc(f.sales, visible)
	submitDoc(f.other, visible)
	hiddenIDs := []string{}
	for _, h := range hidden {
		in := doc(h.customer, h.province, h.city, h.factory, h.equipment, h.qty, h.stage)
		in.TenderNo = h.tender
		p := submitDoc(c, in)
		hiddenIDs = append(hiddenIDs, p.ID)
		if h.legacy {
			g1Exec(t, f.db, `ALTER TABLE intake_projects DROP CONSTRAINT IF EXISTS submission_projection`)
			g1Exec(t, f.db, `UPDATE intake_projects SET intake_status='APPROVED',evidence_state='INCOMPLETE',submission_revision=0,submitted_by_subject=NULL WHERE id=$1`, p.ID)
		}
	}
	primary := submitDoc(f.other, visible)
	for i, h := range hidden {
		if h.decided {
			g1Exec(t, f.db, `UPDATE duplicate_cases SET decision='DISTINCT_APPROVED',reason='合成：总监已排除',decided_by_subject='synthetic-director',decided_at=now()
 WHERE project_id=$1 AND candidate_id=$2`, primary.ID, hiddenIDs[i])
		}
	}
	var hiddenCases int
	_ = f.db.QueryRow(ctx, `SELECT count(*) FROM duplicate_cases d JOIN intake_projects c ON c.id=d.candidate_id WHERE d.project_id=$1 AND c.owner_subject='synthetic-c'`, primary.ID).Scan(&hiddenCases)
	if hiddenCases != len(hidden) {
		t.Fatalf("fixture: 期望 %d 个跨组候选，实际 %d", len(hidden), hiddenCases)
	}
	views := map[string]map[string]string{}
	raw := map[string]string{}
	clients := map[string]testClient{"manager": f.manager, "sales": f.other, "director": f.director, "admin": f.admin}
	calls := []struct{ key, method, path string }{
		{"comparison", "GET", "/api/v1/projects/" + primary.ID + "/duplicate-comparison"},
		{"duplicates", "GET", "/api/v1/projects/" + primary.ID + "/duplicates"},
		{"detail", "GET", "/api/v1/projects/" + primary.ID},
		{"review-list", "GET", "/api/v1/reviews?kind=duplicates"},
		{"duplicate-checks", "POST", "/api/v1/duplicate-checks"},
	}
	for who, cl := range clients {
		views[who] = map[string]string{}
		for _, call := range calls {
			var body any
			if call.method == "POST" {
				body = visible
			}
			s, b := cl.call(call.method, call.path, body)
			views[who][call.key] = canonical(t, s, b)
			raw[who+" "+call.key] = string(b)
		}
	}
	// Server-side rules still apply to hidden candidates: the restricted
	// manager cannot decide (direct POST, guessing the visible case), while the
	// director can complete a decision with the full candidate set.
	var visibleCase string
	_ = f.db.QueryRow(ctx, `SELECT d.id::text FROM duplicate_cases d JOIN intake_projects c ON c.id=d.candidate_id WHERE d.project_id=$1 AND c.owner_subject<>'synthetic-c' AND d.decision='PENDING' ORDER BY c.registration_no LIMIT 1`, primary.ID).Scan(&visibleCase)
	s, b := g1Decision(f.manager, primary, "duplicate-decisions", map[string]any{"caseId": visibleCase, "decision": "DISTINCT_APPROVED"})
	if s != 403 {
		t.Fatalf("受限主管直接 POST 排除重复应被拒绝，实际 %d %s", s, b)
	}
	s, b = g1Decision(f.manager, primary, "approve", nil)
	if s != 403 && s != 409 {
		t.Fatalf("受限主管直接批准应被拒绝，实际 %d %s", s, b)
	}
	s, b = g1Decision(f.director, primary, "duplicate-decisions", map[string]any{"caseId": visibleCase, "decision": "DISTINCT_APPROVED", "acknowledgeLegacyEvidence": true})
	if out := projectFrom(t, s, b); out.IntakeStatus != "APPROVED" {
		t.Fatal("总监应能完成裁决", out.IntakeStatus)
	}
	return views, raw
}
