package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// G3-3B statistics. Amounts are checked against an independent Go reference
// that reads the same rows, classifies with amountClass() and sums with
// big.Rat — never float64.

func ratString(r *big.Rat) string { return r.FloatString(2) }

// referenceSummary recomputes the expected summary for the projects the
// caller can see under the same filters (ids come from the paged list API,
// so scope and filters are the API's own).
func referenceSummary(t *testing.T, f g1Fixture, c testClient, base string) map[string]AmountSummary {
	t.Helper()
	ids := []string{}
	for page := 1; ; page++ {
		q := fmt.Sprintf("page=%d&pageSize=100", page)
		if base != "" {
			q = base + "&" + q
		}
		l := listOf(t, c, q)
		for _, p := range l.Items {
			ids = append(ids, p.ID)
		}
		if page*100 >= l.Total {
			break
		}
	}
	rows, err := f.db.Query(context.Background(), `SELECT p.sales_stage,p.document::text,p.estimated_amount::text FROM intake_projects p WHERE p.id=ANY($1::uuid[])`, ids)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	sums := map[string]*big.Rat{}
	out := map[string]AmountSummary{}
	for rows.Next() {
		var stage string
		var doc []byte
		var col *string
		if err = rows.Scan(&stage, &doc, &col); err != nil {
			t.Fatal(err)
		}
		if !contains(stages, stage) {
			stage = ""
		}
		for _, key := range []string{stage, "*"} {
			m := out[key]
			if sums[key] == nil {
				sums[key] = new(big.Rat)
			}
			switch amountClass(doc, col) {
			case amountKnown:
				v, _ := new(big.Rat).SetString(*col)
				sums[key].Add(sums[key], v)
				m.KnownCount++
				if v.Sign() == 0 {
					m.KnownZeroCount++
				}
				var d struct {
					K map[string]string `json:"knowledge"`
				}
				_ = json.Unmarshal(doc, &d)
				if d.K["estimatedAmount"] == "" {
					m.KnownImplicitCount++
				}
			case amountUnknown:
				m.UnknownCount++
			case amountLegacy:
				m.LegacyUnconfirmedCount++
			default:
				m.InvalidCount++
			}
			out[key] = m
		}
	}
	for k, m := range out {
		m.KnownTotal = ratString(sums[k])
		out[k] = m
	}
	return out
}

// assertAmounts: API amounts == reference, per stage and globally, and the
// stage + unclassified amounts reconcile to the global amount.
func assertAmounts(t *testing.T, label string, f g1Fixture, c testClient, base string) StageSummary {
	t.Helper()
	sum := assertStageEquivalence(t, label, c, base)
	ref := referenceSummary(t, f, c, base)
	zero := AmountSummary{KnownTotal: "0.00"}
	get := func(k string) AmountSummary {
		if m, ok := ref[k]; ok {
			return m
		}
		return zero
	}
	if sum.Amount != get("*") {
		t.Fatalf("%s global amount %+v want %+v", label, sum.Amount, get("*"))
	}
	if sum.UnclassifiedAmount != get("") {
		t.Fatalf("%s unclassified amount %+v want %+v", label, sum.UnclassifiedAmount, get(""))
	}
	acc := new(big.Rat)
	n := 0
	add := func(code string, m AmountSummary, count int) {
		if m.KnownCount+m.UnknownCount+m.LegacyUnconfirmedCount+m.InvalidCount != count {
			t.Fatalf("%s %s: class counts do not add up to %d: %+v", label, code, count, m)
		}
		v, ok := new(big.Rat).SetString(m.KnownTotal)
		if !ok || !strings.Contains(m.KnownTotal, ".") || len(m.KnownTotal)-strings.Index(m.KnownTotal, ".") != 3 {
			t.Fatalf("%s %s: knownTotal %q is not a 2-place decimal string", label, code, m.KnownTotal)
		}
		acc.Add(acc, v)
		n += m.KnownCount
	}
	for _, sc := range sum.Stages {
		if sc.Amount != get(sc.Code) {
			t.Fatalf("%s %s amount %+v want %+v", label, sc.Code, sc.Amount, get(sc.Code))
		}
		add(sc.Code, sc.Amount, sc.Count)
	}
	add("unclassified", sum.UnclassifiedAmount, sum.Unclassified)
	add("global", sum.Amount, sum.Total)
	half := new(big.Rat).Quo(acc, big.NewRat(2, 1)) // stages+unclassified were added once, global once
	if ratString(half) != sum.Amount.KnownTotal {
		t.Fatalf("%s: stages+unclassified %s != global %s", label, ratString(half), sum.Amount.KnownTotal)
	}
	return sum
}

func amountOf(t *testing.T, c testClient, query, code string) AmountSummary {
	t.Helper()
	sum := stageSummaryOf(t, c, query)
	if code == "*" {
		return sum.Amount
	}
	for _, sc := range sum.Stages {
		if sc.Code == code {
			return sc.Amount
		}
	}
	t.Fatal("no stage", code)
	return AmountSummary{}
}

func TestDatabaseG33BAmountStatistics(t *testing.T) {
	ctx := context.Background()

	t.Run("precision-and-classes", func(t *testing.T) {
		f := g1FixtureFor(t)
		k := map[string]string{"estimatedAmount": "KNOWN"}
		for i, v := range []any{0, 0.01, 0.10, 125000.25, 99999999999.99, 0.01, 0.01, 0.01} {
			s, _, b := createAmount(t, f.sales, fmt.Sprintf("合成精度%02d", i), v, k, "L5_QUOTATION")
			expect(t, s, 200, b)
		}
		for i := 0; i < 3; i++ {
			s, _, b := createAmount(t, f.sales, fmt.Sprintf("合成精度未知%d", i), nil, nil, "L5_QUOTATION")
			expect(t, s, 200, b)
		}
		s, _, b := createAmount(t, f.sales, "合成精度旧零", 0, nil, "L5_QUOTATION")
		expect(t, s, 200, b)
		// Since G3-3B-R1 the API refuses 1.005 (400); the inconsistent record the
		// pre-R1 API produced is reproduced byte-for-byte (synthetic).
		s, _, b = createAmount(t, f.sales, "合成精度多位", 1.005, k, "L5_QUOTATION")
		expect(t, s, 400, b)
		s, pm, b := createAmount(t, f.sales, "合成精度多位", 1, k, "L5_QUOTATION")
		expect(t, s, 200, b)
		g1Exec(t, f.db, `UPDATE intake_projects SET estimated_amount=1.01, document=jsonb_set(document,'{estimatedAmount}','1.005') WHERE id=$1`, pm.ID)
		// Legacy migrated rows (SQL) and one illegal-stage row.
		g1Exec(t, f.db, `INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,team_id,intake_status,sales_stage,document,estimated_amount,evidence_state,protection_basis)
 SELECT '合成精度历史'||g,'synthetic legacy '||g,'synthetic-a','synthetic-a','synthetic-a',$1,'APPROVED',CASE WHEN g=3 THEN 'LEGACY_STAGE_X' ELSE 'L5_QUOTATION' END,
 jsonb_build_object('projectName','合成精度历史'||g,'estimatedAmount',(ARRAY[0,250.75,1000])[g]),(ARRAY[0,250.75,1000])[g],'INCOMPLETE','LEGACY_CREATED_AT' FROM generate_series(1,3) g`, syntheticTeam)
		sum := assertAmounts(t, "precision", f, f.sales, "")
		l5 := amountOf(t, f.sales, "", "L5_QUOTATION")
		want := AmountSummary{KnownTotal: "100000125250.89", KnownCount: 9, KnownZeroCount: 1, KnownImplicitCount: 1, UnknownCount: 3, LegacyUnconfirmedCount: 2, InvalidCount: 1}
		ref := new(big.Rat)
		for _, v := range []string{"0", "0.01", "0.10", "125000.25", "99999999999.99", "0.01", "0.01", "0.01", "250.75"} {
			x, _ := new(big.Rat).SetString(v)
			ref.Add(ref, x)
		}
		want.KnownTotal = ratString(ref)
		if l5 != want {
			t.Fatalf("L5 %+v want %+v", l5, want)
		}
		if sum.Unclassified != 1 || sum.UnclassifiedAmount.KnownTotal != "1000.00" || sum.UnclassifiedAmount.KnownImplicitCount != 1 {
			t.Fatalf("unclassified %d %+v", sum.Unclassified, sum.UnclassifiedAmount)
		}
		// No known amount at all: "0.00" together with knownCount=0.
		if m := amountOf(t, f.sales, "", "L0_LEAD"); m.KnownTotal != "0.00" || m.KnownCount != 0 {
			t.Fatal("empty stage", m)
		}
		// 0+0.01+0.10+125000.25+99999999999.99+3×0.01+250.75: exact decimal, not float64.
		if want.KnownTotal != "100000125251.13" {
			t.Fatal("reference", want.KnownTotal)
		}
	})

	t.Run("roles-scope-and-filters", func(t *testing.T) {
		f := g1FixtureFor(t)
		c := login(t, f.server, "synthetic-c")
		k := map[string]string{"estimatedAmount": "KNOWN"}
		// The same customer in two teams with different amounts.
		for _, x := range []struct {
			cl     testClient
			name   string
			amount any
			stage  string
		}{{f.sales, "合成共享客户甲", 100000.10, "L5_QUOTATION"}, {f.other, "合成共享客户乙", 2000, "L5_QUOTATION"}, {c, "合成共享客户丙", 777777.77, "L5_QUOTATION"}, {f.sales, "合成共享客户丁", nil, "L2_TECHNICAL"}} {
			in := map[string]any{}
			_ = json.Unmarshal(mustJSON(sample(x.name)), &in)
			in["customer"] = "合成共享客户"
			in["location"] = map[string]any{"province": "江苏省", "city": "无锡市", "factoryName": x.name}
			in["salesStage"] = x.stage
			in["estimatedAmount"] = x.amount
			if x.amount != nil {
				in["knowledge"] = k
			}
			s, b := x.cl.call("POST", "/api/v1/projects", in)
			p := projectFrom(t, s, b)
			if x.name == "合成共享客户甲" {
				g1Submit(t, f.sales, p)
			}
		}
		s, b := f.admin.call("POST", "/api/v1/projects", map[string]any{"projectName": "合成待分配金额", "customer": "合成共享客户", "salesStage": "L5_QUOTATION", "estimatedAmount": 5, "knowledge": k})
		expect(t, s, 200, b)
		otherManager := login(t, f.server, "synthetic-manager-other")
		want := map[string]string{"sales-a": "100000.10", "sales-b": "2000.00", "manager": "102000.10", "other-manager": "777777.77", "director": "879777.87", "admin": "879782.87"}
		clients := map[string]testClient{"sales-a": f.sales, "sales-b": f.other, "manager": f.manager, "other-manager": otherManager, "director": f.director, "admin": f.admin}
		for who, cl := range clients {
			for _, base := range []string{"", "q=" + url.QueryEscape("合成共享客户"), "province=" + url.QueryEscape("江苏省"), "status=SUBMITTED", "status=DRAFT"} {
				assertAmounts(t, who+" "+base, f, cl, base)
			}
			if got := amountOf(t, cl, "", "L5_QUOTATION").KnownTotal; got != want[who] {
				t.Fatalf("%s L5 %s want %s", who, got, want[who])
			}
		}
		// The response for a restricted viewer carries nothing about hidden
		// projects: no other-team amount, name or id appears in the raw body.
		_, raw := f.manager.call("GET", "/api/v1/stage-summary?q="+url.QueryEscape("合成共享客户"), nil)
		for _, secret := range []string{"777777", "合成共享客户丙", "879777", "879782"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("manager summary leaks", secret)
			}
		}
		// The manager summary equals what the manager could add up from cards
		// he can open; other teams change nothing for him.
		before := stageSummaryOf(t, f.manager, "")
		s, b = c.call("POST", "/api/v1/projects", map[string]any{"projectName": "合成隐藏新增", "customer": "合成共享客户", "salesStage": "L5_QUOTATION", "estimatedAmount": 1, "knowledge": k})
		expect(t, s, 200, b)
		after := stageSummaryOf(t, f.manager, "")
		after.AsOf = before.AsOf
		if fmt.Sprint(before) != fmt.Sprint(after) {
			t.Fatal("hidden change visible to manager")
		}
	})

	t.Run("consistency-through-lifecycle", func(t *testing.T) {
		f := g1FixtureFor(t)
		k := map[string]string{"estimatedAmount": "KNOWN"}
		q := "q=" + url.QueryEscape("合成生命周期")
		s, p, b := createAmount(t, f.sales, "合成生命周期", 1000, k, "L1_REQUIREMENT")
		expect(t, s, 200, b)
		check := func(label, stage, total string, count int) {
			t.Helper()
			m := amountOf(t, f.sales, q, stage)
			if m.KnownTotal != total || m.KnownCount != count {
				t.Fatalf("%s: %s %+v want %s/%d", label, stage, m, total, count)
			}
			assertAmounts(t, label, f, f.sales, q)
		}
		check("创建后", "L1_REQUIREMENT", "1000.00", 1)
		p.Input.Version = p.Version
		p.EstimatedAmount = pointer(1500.5)
		s, b = f.sales.call("PATCH", "/api/v1/projects/"+p.ID, p.Input)
		p = projectFrom(t, s, b)
		check("编辑后", "L1_REQUIREMENT", "1500.50", 1)
		// Amount and stage changed together in one edit (draft).
		p.Input.Version = p.Version
		p.EstimatedAmount = pointer(1800.0)
		p.SalesStage = "L2_TECHNICAL"
		s, b = f.sales.call("PATCH", "/api/v1/projects/"+p.ID, p.Input)
		p = projectFrom(t, s, b)
		check("同时改金额与阶段", "L2_TECHNICAL", "1800.00", 1)
		check("原阶段", "L1_REQUIREMENT", "0.00", 0)
		// A failed edit (stale version, invalid knowledge) changes nothing.
		stale := p.Input
		stale.Version = p.Version - 1
		stale.EstimatedAmount = pointer(9999.0)
		s, b = f.sales.call("PATCH", "/api/v1/projects/"+p.ID, stale)
		expect(t, s, 409, b)
		bad := map[string]any{}
		_ = json.Unmarshal(mustJSON(p.Input), &bad)
		bad["version"] = p.Version
		bad["estimatedAmount"] = 5
		bad["knowledge"] = map[string]string{"estimatedAmount": "UNKNOWN"}
		s, b = f.sales.call("PATCH", "/api/v1/projects/"+p.ID, bad)
		expect(t, s, 400, b)
		check("失败编辑不改变汇总", "L2_TECHNICAL", "1800.00", 1)
		// Draft → submitted → returned → resubmitted; stage L2 → L3.
		p = g1Submit(t, f.sales, p)
		if m := amountOf(t, f.sales, q+"&status=SUBMITTED", "*"); m.KnownTotal != "1800.00" {
			t.Fatal("submitted filter", m)
		}
		if m := amountOf(t, f.sales, q+"&status=DRAFT", "*"); m.KnownCount != 0 || m.KnownTotal != "0.00" {
			t.Fatal("draft filter after submit", m)
		}
		s, b = g1Decision(f.manager, p, "return", nil)
		p = projectFrom(t, s, b)
		p.Input.Version = p.Version
		p.EstimatedAmount = pointer(2000.0)
		s, b = f.sales.call("PATCH", "/api/v1/projects/"+p.ID, p.Input)
		p = g1Submit(t, f.sales, projectFrom(t, s, b))
		check("退回再提交", "L2_TECHNICAL", "2000.00", 1)
		s, b = f.sales.call("POST", "/api/v1/projects/"+p.ID+"/stage-change", map[string]any{"version": p.Version, "salesStage": "L3_SITE_SURVEY", "reason": "合成推进"})
		p = projectFrom(t, s, b)
		check("阶段推进", "L3_SITE_SURVEY", "2000.00", 1)
		check("推进后原阶段", "L2_TECHNICAL", "0.00", 0)
		// Two concurrent stage changes of the same version: one wins, one 409;
		// the summary reflects exactly the winner.
		var wg sync.WaitGroup
		codes := make([]int, 2)
		for i := range codes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes[i], _ = f.sales.call("POST", "/api/v1/projects/"+p.ID+"/stage-change", map[string]any{"version": p.Version, "salesStage": []string{"L4_SOLUTION", "HOLD"}[i], "reason": "合成并发"})
			}()
		}
		wg.Wait()
		if !(codes[0] == 200 && codes[1] == 409 || codes[0] == 409 && codes[1] == 200) {
			t.Fatal("concurrent", codes)
		}
		cur := projectFromGet(t, f.sales, p.ID)
		check("并发后", cur.SalesStage, "2000.00", 1)
		// Summary taken while other users write stays internally consistent.
		var wg2 sync.WaitGroup
		stop := make(chan struct{})
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				createAmount(t, f.other, fmt.Sprintf("合成并发写入%03d", i), float64(i)+0.25, k, "L6_TENDER")
			}
		}()
		for i := 0; i < 15; i++ {
			sum := stageSummaryOf(t, f.manager, "")
			acc := new(big.Rat)
			n := 0
			for _, sc := range sum.Stages {
				v, _ := new(big.Rat).SetString(sc.Amount.KnownTotal)
				acc.Add(acc, v)
				n += sc.Count
			}
			u, _ := new(big.Rat).SetString(sum.UnclassifiedAmount.KnownTotal)
			acc.Add(acc, u)
			if ratString(acc) != sum.Amount.KnownTotal || n+sum.Unclassified != sum.Total {
				t.Fatal("summary not from one snapshot", ratString(acc), sum.Amount.KnownTotal)
			}
		}
		close(stop)
		wg2.Wait()
		_ = ctx
	})
}

func projectFromGet(t *testing.T, c testClient, id string) Project {
	t.Helper()
	s, b := c.call("GET", "/api/v1/projects/"+id, nil)
	return projectFrom(t, s, b)
}
