package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// G3-2B supplement to G3-2A: the restricted manager has no readable pending
// candidate, only a hidden one. Every state change below is produced through
// the real HTTP API. The manager's review hints, actions, queue membership and
// home counts for the primary filing must not change with the hidden
// candidate's own lifecycle (submitted / approved / returned / rejected) or
// across a return-and-resubmit round of the primary.
func TestDatabaseG32BHiddenCandidateStates(t *testing.T) {
	f := g1FixtureFor(t)
	ctx := context.Background()
	hiddenSales := login(t, f.server, "synthetic-c")
	name := "合成隐藏状态"
	doc := sample(name)
	doc.Customer = "合成隐藏状态客户"
	submit := func(c testClient) Project {
		s, b := c.call("POST", "/api/v1/projects", doc)
		return g1Submit(t, c, projectFrom(t, s, b))
	}
	// A control filing in the same team with no candidate at all.
	control := g1Submit(t, f.other, g1Create(t, f.other, "合成无候选对照"))
	hidden := submit(hiddenSales)
	primary := submit(f.other)
	if primary.IntakeStatus != "UNDER_REVIEW" {
		t.Fatal("primary must match the hidden candidate", primary.IntakeStatus)
	}
	var readable int
	_ = f.db.QueryRow(ctx, `SELECT count(*) FROM duplicate_cases d JOIN intake_projects c ON c.id=d.candidate_id WHERE d.project_id=$1 AND c.team_id::text<>$2`, primary.ID, otherSyntheticTeam).Scan(&readable)
	if readable != 0 {
		t.Fatal("fixture: manager must have no readable candidate", readable)
	}

	view := func(label string) string {
		t.Helper()
		out := map[string]any{}
		s, b := f.manager.call("GET", "/api/v1/projects/"+primary.ID, nil)
		d := projectFrom(t, s, b)
		acts := map[string]ActionHint{}
		for _, a := range reviewActions {
			acts[a] = d.Actions[a]
		}
		out["detail.review"], out["detail.actions"] = d.Review, acts
		for _, kind := range []string{"pending", "duplicates"} {
			s, b = f.manager.call("GET", "/api/v1/reviews?kind="+kind, nil)
			expect(t, s, 200, b)
			var list struct{ Items []Project }
			_ = json.Unmarshal(b, &list)
			found := false
			for _, it := range list.Items {
				if it.ID == primary.ID {
					found = true
					out["list."+kind+".review"] = it.Review
					out["list."+kind+".approve"] = it.Actions["approve"]
					out["list."+kind+".duplicate-decisions"] = it.Actions["duplicate-decisions"]
				}
			}
			out["list."+kind+".contains"] = found
		}
		s, b = f.manager.call("GET", "/api/v1/workspace-summary", nil)
		expect(t, s, 200, b)
		var sum WorkspaceSummary
		_ = json.Unmarshal(b, &sum)
		out["summary.reviewable"], out["summary.reviewQueue"] = sum.Counts["reviewable"], sum.Counts["reviewQueue"]
		raw, _ := json.Marshal(out)
		t.Logf("HIDDEN_STATE %s %s", label, raw)
		return string(raw)
	}
	base := view("隐藏候选已提交")
	if !strings.Contains(base, `"reasonCode":"REVIEW_RELATED_PROJECT_RESTRICTED"`) || !strings.Contains(base, `"pendingDuplicates":0`) || !strings.Contains(base, `"DUPLICATE_DECISION_REQUIRED"`) {
		t.Fatal("restricted manager must be blocked with the fixed reason", base)
	}
	// Approval still blocked server-side even though no readable case is pending.
	s, b := g1Decision(f.manager, primary, "approve", nil)
	if s != 409 || !strings.Contains(string(b), "DUPLICATE_DECISION_REQUIRED") {
		t.Fatalf("approve must stay blocked: %d %s", s, b)
	}
	// The control filing shows that eligibility itself is the only disclosed bit.
	s, b = f.manager.call("GET", "/api/v1/projects/"+control.ID, nil)
	if c := projectFrom(t, s, b); c.Review == nil || !c.Review.Eligible {
		t.Fatal("control must be reviewable", c.Review)
	}

	compare := func(label string) {
		t.Helper()
		if got := view(label); got != base {
			t.Fatalf("隐藏候选状态变化导致受限主管视图变化（%s）：\n%s\n%s", label, base, got)
		}
	}
	// Hidden candidate lifecycle: approved by the director.
	s, b = g1Decision(f.director, hidden, "approve", nil)
	hidden = projectFrom(t, s, b)
	compare("隐藏候选已批准")

	// Primary returned and resubmitted: a new round re-detects the hidden candidate.
	s, b = f.manager.call("GET", "/api/v1/projects/"+primary.ID, nil)
	primary = projectFrom(t, s, b)
	s, b = g1Decision(f.director, primary, "return", nil)
	primary = projectFrom(t, s, b)
	primary = g1Submit(t, f.other, primary)
	if primary.SubmissionRevision != 2 || primary.IntakeStatus != "UNDER_REVIEW" {
		t.Fatal("resubmission", primary.SubmissionRevision, primary.IntakeStatus)
	}
	compare("主项目退回再提交")

	// A second hidden candidate outside this round, then rejected.
	h2 := submit(hiddenSales)
	s, b = f.manager.call("GET", "/api/v1/projects/"+h2.ID, nil)
	expect(t, s, 404, b)
	compare("第二个隐藏候选（不在本轮）")
	s, b = f.director.call("GET", "/api/v1/projects/"+h2.ID, nil)
	h2 = projectFrom(t, s, b)
	s, b = g1Decision(f.director, h2, "reject", nil)
	projectFrom(t, s, b)
	compare("第二个隐藏候选已拒绝")

	// Invariant instead of a forged row: a project still in review never
	// carries a decided duplicate case. Every decision moves the primary to
	// APPROVED or REJECTED, APPROVED cannot be returned, and a confirmed
	// duplicate cannot be resubmitted.
	other := submit(f.sales) // team one, readable for the manager
	s, b = f.director.call("GET", "/api/v1/projects/"+other.ID, nil)
	other = projectFrom(t, s, b)
	cases := comparisonCases(t, f.director, other)
	s, b = g1Decision(f.director, other, "duplicate-decisions", map[string]any{"caseId": cases[0].ID, "decision": "CONFIRMED_DUPLICATE"})
	other = projectFrom(t, s, b)
	s, b = f.sales.call("POST", "/api/v1/projects/"+other.ID+"/submit", map[string]int{"version": other.Version})
	if s != 409 {
		t.Fatalf("decided project must not re-enter review: %d %s", s, b)
	}
	s, b = g1Decision(f.director, other, "return", nil)
	if s != 409 {
		t.Fatalf("decided project must not be returned: %d %s", s, b)
	}
	var mixed int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM intake_projects p JOIN duplicate_cases d ON d.project_id=p.id
 WHERE p.intake_status IN ('SUBMITTED','UNDER_REVIEW') AND d.decision<>'PENDING'`).Scan(&mixed); err != nil || mixed != 0 {
		t.Fatal("invariant: in-review project with a decided case", mixed, err)
	}
	t.Log(fmt.Sprintf("INVARIANT in-review-with-decided-case=%d （已裁决与本轮待裁决并存的组合在业务状态机中不可达）", mixed))
	// Director can still finish the primary with the full candidate set.
	s, b = f.director.call("GET", "/api/v1/projects/"+primary.ID, nil)
	primary = projectFrom(t, s, b)
	cases = comparisonCases(t, f.director, primary)
	s, b = g1Decision(f.director, primary, "duplicate-decisions", map[string]any{"caseId": cases[0].ID, "decision": "DISTINCT_APPROVED"})
	if projectFrom(t, s, b).IntakeStatus != "APPROVED" {
		t.Fatal("director decision")
	}
}
