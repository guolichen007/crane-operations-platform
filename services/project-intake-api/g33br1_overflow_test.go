package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// G3-3B-R1 P1-A: the known-amount sum must not be capped by a fixed numeric
// precision. 10,000 KNOWN projects of 100000000000000.00 (the approved
// per-project maximum, 1e14) sum to 1000000000000000000.00 (19 integer
// digits), beyond numeric(20,2) (18 integer digits) used before R1.

// bulkKnown inserts n synthetic KNOWN projects with a consistent column,
// document amount and knowledge flag (SQL, labelled synthetic).
func bulkKnown(t *testing.T, f g1Fixture, n int, amount, owner, team, stage, tag string) {
	t.Helper()
	g1Exec(t, f.db, `INSERT INTO intake_projects(project_name,normalized_project_name,owner_subject,created_by_subject,draft_custodian_subject,team_id,intake_status,sales_stage,document,estimated_amount,evidence_state,protection_basis)
 SELECT $6||g,'synthetic big '||g,$2,$2,$2,$3::uuid,'DRAFT',$4,
 jsonb_build_object('projectName',$6||g,'estimatedAmount',$1::numeric,'knowledge',jsonb_build_object('estimatedAmount','KNOWN')),$1::numeric,'INCOMPLETE','LEGACY_CREATED_AT'
 FROM generate_series(1,$5::int) g`, amount, owner, team, stage, n, tag)
}

func ratTimes(amount string, n int) string {
	r, _ := new(big.Rat).SetString(amount)
	return ratString(r.Mul(r, big.NewRat(int64(n), 1)))
}

func TestDatabaseG33BR1AggregationRange(t *testing.T) {
	ctx := context.Background()
	report := &strings.Builder{}
	fixed := os.Getenv("BAOBEI_R1_PRE") == "" // BAOBEI_R1_PRE=1 documents the pre-R1 behaviour

	t.Run("10000-projects-at-the-per-project-maximum", func(t *testing.T) {
		f := g1FixtureFor(t)
		bulkKnown(t, f, 10000, "100000000000000.00", "synthetic-a", syntheticTeam, "L5_QUOTATION", "合成大额")
		var pgErr *pgconn.PgError
		var legacy string
		err := f.db.QueryRow(ctx, `SELECT coalesce(sum(p.estimated_amount),0)::numeric(20,2)::text FROM intake_projects p`).Scan(&legacy)
		if !errors.As(err, &pgErr) || pgErr.Code != "22003" {
			t.Fatalf("pre-R1 expression should overflow with 22003, got %q %v", legacy, err)
		}
		fmt.Fprintf(report, "旧聚合表达式 sum(...)::numeric(20,2)：SQLSTATE %s %s\n", pgErr.Code, pgErr.Message)
		s, b := f.manager.call("GET", "/api/v1/stage-summary", nil)
		fmt.Fprintf(report, "接口 GET /stage-summary（主管）：HTTP %d\n", s)
		if !fixed {
			if s == 200 {
				t.Fatal("pre-R1 implementation should fail")
			}
			return
		}
		expect(t, s, 200, b)
		var sum StageSummary
		_ = json.Unmarshal(b, &sum)
		want := "1000000000000000000.00"
		if sum.Amount.KnownTotal != want || sum.Amount.KnownCount != 10000 || sum.Stages[5].Amount.KnownTotal != want {
			t.Fatal("total", sum.Amount, sum.Stages[5].Amount)
		}
		fmt.Fprintf(report, "修复后：knownTotal=%s knownCount=%d\n", sum.Amount.KnownTotal, sum.Amount.KnownCount)
	})
	if !fixed {
		if p := os.Getenv("BAOBEI_R1_REPORT"); p != "" {
			_ = os.WriteFile(p, []byte(report.String()), 0o600)
		}
		return
	}

	t.Run("boundary-matrix-roles-and-reconciliation", func(t *testing.T) {
		f := g1FixtureFor(t)
		otherTeam := "22222222-2222-4222-8222-222222222222"
		// Empty scope first: "0.00" with zero counts.
		if m := stageSummaryOf(t, f.manager, "").Amount; m.KnownTotal != "0.00" || m.KnownCount != 0 {
			t.Fatal("empty", m)
		}
		// All unknown (API).
		for i := 0; i < 3; i++ {
			s, _, b := createAmount(t, f.sales, fmt.Sprintf("合成全未知%d", i), nil, nil, "L0_LEAD")
			expect(t, s, 200, b)
		}
		if m := stageSummaryOf(t, f.manager, "").Amount; m.KnownTotal != "0.00" || m.KnownCount != 0 || m.UnknownCount != 3 {
			t.Fatal("all unknown", m)
		}
		// One KNOWN 0.
		s, _, b := createAmount(t, f.sales, "合成已知零", 0, map[string]string{"estimatedAmount": "KNOWN"}, "L0_LEAD")
		expect(t, s, 200, b)
		if m := stageSummaryOf(t, f.manager, "").Amount; m.KnownTotal != "0.00" || m.KnownCount != 1 || m.KnownZeroCount != 1 {
			t.Fatal("known zero", m)
		}
		// 0.01 repeated 1,000 times; large amounts in several stages for team
		// one, team two, and an illegal stage; more than numeric(20,2) per stage.
		bulkKnown(t, f, 1000, "0.01", "synthetic-a", syntheticTeam, "L1_REQUIREMENT", "合成一分")
		bulkKnown(t, f, 6000, "100000000000000.00", "synthetic-a", syntheticTeam, "L6_TENDER", "合成大额甲")
		bulkKnown(t, f, 5000, "99999999999999.99", "synthetic-b", syntheticTeam, "WON", "合成大额乙")
		bulkKnown(t, f, 4000, "100000000000000.00", "synthetic-c", otherTeam, "L6_TENDER", "合成大额丙")
		bulkKnown(t, f, 2000, "100000000000000.00", "synthetic-a", syntheticTeam, "LEGACY_STAGE_X", "合成大额非标")
		g1Exec(t, f.db, `ANALYZE intake_projects`)
		team1 := []struct {
			amount string
			n      int
		}{{"0.01", 1000}, {"100000000000000.00", 6000}, {"99999999999999.99", 5000}, {"100000000000000.00", 2000}}
		acc := new(big.Rat)
		for _, x := range team1 {
			v, _ := new(big.Rat).SetString(ratTimes(x.amount, x.n))
			acc.Add(acc, v)
		}
		team1Total := ratString(acc)
		allTotal := ratString(acc.Add(acc, func() *big.Rat { v, _ := new(big.Rat).SetString(ratTimes("100000000000000.00", 4000)); return v }()))
		want := map[string]struct {
			total string
			count int
			l6    string
		}{
			"manager":       {team1Total, 14001, ratTimes("100000000000000.00", 6000)},
			"other-manager": {ratTimes("100000000000000.00", 4000), 4000, ratTimes("100000000000000.00", 4000)},
			"director":      {allTotal, 18001, ratTimes("100000000000000.00", 10000)},
			"admin":         {allTotal, 18001, ratTimes("100000000000000.00", 10000)},
		}
		clients := map[string]testClient{"manager": f.manager, "other-manager": login(t, f.server, "synthetic-manager-other"), "director": f.director, "admin": f.admin}
		for who, c := range clients {
			sum := stageSummaryOf(t, c, "")
			if sum.Amount.KnownTotal != want[who].total || sum.Amount.KnownCount != want[who].count {
				t.Fatalf("%s global %+v want %s/%d", who, sum.Amount, want[who].total, want[who].count)
			}
			if sum.Stages[6].Amount.KnownTotal != want[who].l6 {
				t.Fatalf("%s L6 %s want %s", who, sum.Stages[6].Amount.KnownTotal, want[who].l6)
			}
			// 12 stages + unclassified == global, for every amount and count.
			stagesAcc := new(big.Rat)
			n := AmountSummary{}
			for _, sc := range append(sum.Stages, StageCount{Amount: sum.UnclassifiedAmount}) {
				v, ok := new(big.Rat).SetString(sc.Amount.KnownTotal)
				if !ok {
					t.Fatal(sc.Amount.KnownTotal)
				}
				stagesAcc.Add(stagesAcc, v)
				n.KnownCount += sc.Amount.KnownCount
				n.KnownZeroCount += sc.Amount.KnownZeroCount
				n.UnknownCount += sc.Amount.UnknownCount
				n.LegacyUnconfirmedCount += sc.Amount.LegacyUnconfirmedCount
				n.InvalidCount += sc.Amount.InvalidCount
			}
			n.KnownTotal = ratString(stagesAcc)
			n.KnownImplicitCount = sum.Amount.KnownImplicitCount
			if n != sum.Amount {
				t.Fatalf("%s reconciliation %+v != %+v", who, n, sum.Amount)
			}
			if who == "manager" && (sum.Unclassified != 2000 || sum.UnclassifiedAmount.KnownTotal != ratTimes("100000000000000.00", 2000)) {
				t.Fatal("unclassified", sum.Unclassified, sum.UnclassifiedAmount)
			}
			if who == "manager" && sum.Stages[1].Amount.KnownTotal != "10.00" {
				t.Fatal("0.01 x 1000", sum.Stages[1].Amount)
			}
			fmt.Fprintf(report, "%s：knownTotal=%s knownCount=%d unknown=%d unclassified=%d\n", who, sum.Amount.KnownTotal, sum.Amount.KnownCount, sum.Amount.UnknownCount, sum.Unclassified)
		}
		// Sales sees only their own projects; the large other-team rows are absent.
		if m := stageSummaryOf(t, f.other, "").Amount; m.KnownTotal != ratTimes("99999999999999.99", 5000) || m.KnownCount != 5000 {
			t.Fatal("sales B", m)
		}
		// Class counts do not change with large amounts: an INVALID large row
		// stays INVALID and is not summed.
		g1Exec(t, f.db, `UPDATE intake_projects SET estimated_amount=estimated_amount-1 WHERE id=(SELECT id FROM intake_projects WHERE project_name='合成大额甲1')`)
		m := stageSummaryOf(t, f.manager, "").Amount
		v, _ := new(big.Rat).SetString(team1Total)
		v.Sub(v, big.NewRat(100000000000000, 1))
		if m.InvalidCount != 1 || m.KnownCount != 14000 || m.KnownTotal != ratString(v) {
			t.Fatal("invalid large row", m)
		}
	})
	if p := os.Getenv("BAOBEI_R1_REPORT"); p != "" {
		_ = os.WriteFile(p, []byte(report.String()), 0o600)
	}
	t.Log("\n" + report.String())
}
