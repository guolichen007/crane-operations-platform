package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// G3-3B B0 amount forensics. Every case is classified three ways: the Go
// reference (real Input model + G1 knowledge rules + exact decimal compare),
// the SQL classifier used by the statistics, and the expected class. All
// three must agree. The report (BAOBEI_AMOUNT_REPORT) lists column value,
// document value and knowledge for each case.

type amountCase struct {
	no, label, expect, why string
	id                     string
}

func amountRow(t *testing.T, f g1Fixture, id string) (doc []byte, col *string, sqlClass string) {
	t.Helper()
	err := f.db.QueryRow(context.Background(), `SELECT p.document::text,p.estimated_amount::text,`+amountClassSQL("p")+` FROM intake_projects p WHERE p.id=$1`, id).Scan(&doc, &col, &sqlClass)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func createAmount(t *testing.T, c testClient, name string, amount any, knowledge map[string]string, stage string) (int, Project, []byte) {
	t.Helper()
	in := map[string]any{}
	_ = json.Unmarshal(mustJSON(sample(name)), &in)
	in["customer"] = name + "客户"
	in["salesStage"] = stage
	in["estimatedAmount"] = amount
	if knowledge != nil {
		in["knowledge"] = knowledge
	} else {
		delete(in, "knowledge")
	}
	s, b := c.call("POST", "/api/v1/projects", in)
	var p Project
	_ = json.Unmarshal(b, &p)
	return s, p, b
}

func TestDatabaseG33BAmountForensics(t *testing.T) {
	ctx := context.Background()
	f := g1FixtureFor(t)
	var cases []amountCase
	add := func(no, label, expect, why, id string) { cases = append(cases, amountCase{no, label, expect, why, id}) }
	mk := func(name string, amount any, k map[string]string, stage string) Project {
		s, p, b := createAmount(t, f.sales, name, amount, k, stage)
		expect(t, s, 200, b)
		return p
	}
	// 1-3: the three states written by the current API.
	add("1", "KNOWN + 正金额（API）", amountKnown, "显式 KNOWN，列=文档", mk("合成金额正", 125000.25, map[string]string{"estimatedAmount": "KNOWN"}, "L1_REQUIREMENT").ID)
	add("2", "KNOWN + 显式 0（API）", amountKnown, "显式确认的真实 0", mk("合成金额零", 0, map[string]string{"estimatedAmount": "KNOWN"}, "L1_REQUIREMENT").ID)
	add("3", "UNKNOWN + NULL（API）", amountUnknown, "null 即未知", mk("合成金额未知", nil, nil, "L1_REQUIREMENT").ID)
	// 4: a new client sending 0 without a flag is treated like a legacy 0.
	add("4", "无标记 + 0（API，旧客户端兼容）", amountLegacy, "G1 规则：未标记的 0 为 LEGACY_UNCONFIRMED", mk("合成金额旧零", 0, nil, "L1_REQUIREMENT").ID)
	// 5-6: migrated history (SQL, document without knowledge, like 00001-00003 rows).
	legacy := func(name string, amount string) string {
		var id string
		err := f.db.QueryRow(ctx, `INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,team_id,intake_status,sales_stage,document,estimated_amount,evidence_state,protection_basis)
 VALUES($1,$1,'synthetic-a','synthetic-a','synthetic-a',$2,'APPROVED','L4_SOLUTION',jsonb_build_object('projectName',$1::text,'estimatedAmount',$3::numeric),$3::numeric,'INCOMPLETE','LEGACY_CREATED_AT') RETURNING id::text`, name, syntheticTeam, amount).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	add("5", "旧记录无 knowledge + 金额 0（合成历史）", amountLegacy, "历史 0 不能证明真实 0", legacy("合成历史零", "0"))
	add("6", "旧记录无 knowledge + 正金额（合成历史）", amountKnown, "G1 规则：未标记正数视为 KNOWN；列=文档", legacy("合成历史正", "88000.50"))
	// 7: column and document disagree (synthetic corruption).
	p7 := mk("合成金额不一致", 5000, map[string]string{"estimatedAmount": "KNOWN"}, "L2_TECHNICAL")
	g1Exec(t, f.db, `UPDATE intake_projects SET estimated_amount=6000 WHERE id=$1`, p7.ID)
	add("7", "列 6000 与文档 5000 不一致（合成）", amountInvalid, "来源不一致，不纳入可信金额", p7.ID)
	// 8: illegal / missing / contradictory flags (synthetic; the API rejects them).
	for i, c := range []struct{ label, patch string }{
		{"KNOWN 但金额 null", `jsonb_set(document,'{knowledge}','{"estimatedAmount":"KNOWN"}')`},
		{"UNKNOWN 但有金额", `jsonb_set(document,'{knowledge}','{"estimatedAmount":"UNKNOWN"}')`},
		{"LEGACY 但金额为正", `jsonb_set(document,'{knowledge}','{"estimatedAmount":"LEGACY_UNCONFIRMED"}')`},
		{"非法标记值", `jsonb_set(document,'{knowledge}','{"estimatedAmount":"MAYBE"}')`},
		{"knowledge 不是对象", `jsonb_set(document,'{knowledge}','"KNOWN"')`},
		{"多余的 knowledge 键", `jsonb_set(document,'{knowledge}','{"estimatedAmount":"KNOWN","foo":"KNOWN"}')`},
		{"金额为字符串", `jsonb_set(document,'{estimatedAmount}','"5000"')`},
		{"金额为负数", `jsonb_set(jsonb_set(document,'{estimatedAmount}','-1'),'{knowledge}','{"estimatedAmount":"KNOWN"}')`},
	} {
		amount := any(5000)
		if strings.HasPrefix(c.label, "KNOWN 但金额 null") {
			amount = nil
		}
		p := mk(fmt.Sprintf("合成金额标记%d", i), amount, nil, "L2_TECHNICAL")
		g1Exec(t, f.db, `UPDATE intake_projects SET document=`+c.patch+` WHERE id=$1`, p.ID)
		add("8."+fmt.Sprint(i+1), c.label+"（合成）", amountInvalid, "已知性与数值矛盾或结构非法", p.ID)
	}
	// 9: extra precision. The API currently accepts it: column rounds to 2
	// places, the document keeps the original value.
	s, p9, b := createAmount(t, f.sales, "合成金额精度", 12345678.9999, map[string]string{"estimatedAmount": "KNOWN"}, "L3_SITE_SURVEY")
	expect(t, s, 200, b)
	add("9", "API 写入 12345678.9999（列 12345679.00）", amountInvalid, "当前写入路径的已知缺陷：列四舍五入、文档保留原值；不纳入", p9.ID)
	p9b := mk("合成金额精度二", 1, map[string]string{"estimatedAmount": "KNOWN"}, "L3_SITE_SURVEY")
	g1Exec(t, f.db, `UPDATE intake_projects SET document=jsonb_set(document,'{estimatedAmount}','100.005'),estimated_amount=100.01 WHERE id=$1`, p9b.ID)
	add("9b", "文档 100.005、列 100.01（合成）", amountInvalid, "多余精度且两源不等；不纳入", p9b.ID)
	p9c := mk("合成金额精度三", 1, map[string]string{"estimatedAmount": "KNOWN"}, "L3_SITE_SURVEY")
	g1Exec(t, f.db, `UPDATE intake_projects SET document=jsonb_set(document,'{estimatedAmount}','100.10'),estimated_amount=100.1 WHERE id=$1`, p9c.ID)
	add("9c", "文档 100.10、列 100.10（合成，书写不同数值相等）", amountKnown, "按十进制数值比较，不按文本", p9c.ID)
	// 10: amount edited later (API PATCH) is classified on the new value.
	p10 := mk("合成金额更新", 1000, map[string]string{"estimatedAmount": "KNOWN"}, "L1_REQUIREMENT")
	p10.Input.Version = p10.Version
	p10.EstimatedAmount = pointer(2500.5)
	s, b = f.sales.call("PATCH", "/api/v1/projects/"+p10.ID, p10.Input)
	expect(t, s, 200, b)
	add("10", "KNOWN 1000 → PATCH 2500.50（API）", amountKnown, "按最新持久化值", p10.ID)
	// 11: several pieces of equipment, one project amount.
	in := sample("合成金额多设备")
	in.Customer = "合成金额多设备客户"
	in.EstimatedAmount = pointer(300000.0)
	in.Knowledge = map[string]string{"estimatedAmount": "KNOWN"}
	in.PlannedEquipment = []Equipment{{EquipmentType: "BRIDGE", Quantity: pointer(2)}, {EquipmentType: "GANTRY", Quantity: pointer(3)}, {EquipmentType: "OTHER", Quantity: pointer(1)}}
	s, b = f.sales.call("POST", "/api/v1/projects", in)
	add("11", "3 种设备 6 台、项目金额 300000（API）", amountKnown, "金额属于项目，只计一次", projectFrom(t, s, b).ID)
	// 12: amount across intake states and sales stages (API).
	add("12a", "已提交 + 待签约（API）", amountKnown, "报备状态与阶段不影响金额判定", g1Submit(t, f.sales, mk("合成金额状态提交", 7000, map[string]string{"estimatedAmount": "KNOWN"}, "L8_PENDING_CONTRACT")).ID)
	p12 := g1Submit(t, f.sales, mk("合成金额状态成交", 7000, map[string]string{"estimatedAmount": "KNOWN"}, "L8_PENDING_CONTRACT"))
	s, b = g1Decision(f.manager, p12, "approve", nil)
	p12 = projectFrom(t, s, b)
	s, b = f.sales.call("POST", "/api/v1/projects/"+p12.ID+"/stage-change", map[string]any{"version": p12.Version, "salesStage": "WON", "reason": "合成成交"})
	p12 = projectFrom(t, s, b)
	add("12b", "已批准 + WON，经 stage-change（API）", amountKnown, "stage-change 只重写文档，列不变且一致", p12.ID)
	p13 := g1Submit(t, f.sales, mk("合成金额状态退回", 0, map[string]string{"estimatedAmount": "KNOWN"}, "L5_QUOTATION"))
	s, b = g1Decision(f.manager, p13, "return", nil)
	add("12c", "退回 + 已知 0（API）", amountKnown, "已知零", projectFrom(t, s, b).ID)
	add("12d", "草稿 + HOLD + 未知（API）", amountUnknown, "", mk("合成金额暂缓", nil, nil, "HOLD").ID)
	add("12e", "草稿 + LOST + 正金额（API）", amountKnown, "", mk("合成金额失单", 42, map[string]string{"estimatedAmount": "KNOWN"}, "LOST").ID)

	var report strings.Builder
	report.WriteString("| # | 情况 | 列 estimated_amount | document.estimatedAmount | document.knowledge | Go 参考 | SQL | 预期 | 说明 |\n| --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, c := range cases {
		doc, col, sqlClass := amountRow(t, f, c.id)
		goClass := amountClass(doc, col)
		var d struct {
			Amount    json.RawMessage `json:"estimatedAmount"`
			Knowledge json.RawMessage `json:"knowledge"`
		}
		_ = json.Unmarshal(doc, &d)
		colText := "NULL"
		if col != nil {
			colText = *col
		}
		fmt.Fprintf(&report, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", c.no, c.label, colText, string(d.Amount), string(d.Knowledge), goClass, sqlClass, c.expect, c.why)
		if goClass != c.expect || sqlClass != c.expect {
			t.Errorf("case %s %s: go=%s sql=%s want %s (col=%s doc=%s)", c.no, c.label, goClass, sqlClass, c.expect, colText, doc)
		}
	}
	// The details API still serves every non-corrupted case; corrupted
	// knowledge makes the project unreadable through the API (existing
	// behaviour, 503), which is why statistics must classify in SQL safely.
	for _, c := range cases {
		s, _ := f.sales.call("GET", "/api/v1/projects/"+c.id, nil)
		fmt.Fprintf(&report, "<!-- detail %s HTTP %d -->\n", c.no, s)
	}
	// SQL and Go agree on every project in the table, not only listed cases.
	rows, err := f.db.Query(ctx, `SELECT p.id::text,p.document::text,p.estimated_amount::text,`+amountClassSQL("p")+` FROM intake_projects p`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, sqlClass string
		var doc []byte
		var col *string
		if err = rows.Scan(&id, &doc, &col, &sqlClass); err != nil {
			t.Fatal(err)
		}
		if g := amountClass(doc, col); g != sqlClass {
			t.Errorf("%s go=%s sql=%s", id, g, sqlClass)
		}
		n++
	}
	t.Logf("AMOUNT_FORENSIC_CASES=%d ALL_ROWS_AGREE=%d", len(cases), n)
	if path := os.Getenv("BAOBEI_AMOUNT_REPORT"); path != "" {
		_ = os.WriteFile(path, []byte(report.String()), 0o600)
	}
}
