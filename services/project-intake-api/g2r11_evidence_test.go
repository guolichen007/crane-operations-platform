package main

import (
	"context"
	"strings"
	"testing"
)

// G2-R1.1: a candidate is COMPLETE_VERIFIED only when its frozen submission
// record for the current revision exists and is itself COMPLETE. The project
// row's evidence_state/submission_revision is a projection and may disagree.
func TestDatabaseG2R11EvidenceGrade(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"frozen-incomplete", "frozen-missing", "consistent"} {
		t.Run(mode, func(t *testing.T) {
			f := g1FixtureFor(t)
			name := "合成证据一致性" + mode
			cand := g1Submit(t, f.sales, g1Create(t, f.sales, name))
			switch mode {
			case "frozen-incomplete":
				// Projection stays COMPLETE; the immutable record says INCOMPLETE.
				g1Exec(t, f.db, `ALTER TABLE project_submissions DISABLE TRIGGER submission_immutable`)
				g1Exec(t, f.db, `UPDATE project_submissions SET evidence_state='INCOMPLETE' WHERE project_id=$1`, cand.ID)
				g1Exec(t, f.db, `ALTER TABLE project_submissions ENABLE TRIGGER submission_immutable`)
			case "frozen-missing":
				// Projection points at a revision that has no frozen record.
				g1Exec(t, f.db, `UPDATE intake_projects SET submission_revision=submission_revision+5 WHERE id=$1`, cand.ID)
			}
			var proj string
			_ = f.db.QueryRow(ctx, `SELECT evidence_state FROM intake_projects WHERE id=$1`, cand.ID).Scan(&proj)
			if proj != "COMPLETE" {
				t.Fatal("fixture must keep the projection COMPLETE", proj)
			}
			fresh := g1Submit(t, f.other, g1Create(t, f.other, name))
			want := map[string]string{"frozen-incomplete": "LEGACY_PARTIAL_EVIDENCE", "frozen-missing": "LEGACY_PARTIAL_EVIDENCE", "consistent": "COMPLETE_VERIFIED"}[mode]
			cases := comparisonCases(t, f.director, fresh)
			if len(cases) != 1 || cases[0].EvidenceGrade != want {
				t.Fatalf("grade %q want %q", cases[0].EvidenceGrade, want)
			}
			s, b := f.director.call("GET", "/api/v1/projects/"+fresh.ID, nil)
			d := projectFrom(t, s, b)
			wantLegacy := 0
			if want == "LEGACY_PARTIAL_EVIDENCE" {
				wantLegacy = 1
			}
			if d.Review == nil || d.Review.LegacyCandidates != wantLegacy {
				t.Fatal("legacyCandidates", d.Review)
			}
			extra := map[string]any{"caseId": cases[0].ID, "decision": "DISTINCT_APPROVED"}
			s, b = g1Decision(f.director, fresh, "duplicate-decisions", extra)
			if want == "LEGACY_PARTIAL_EVIDENCE" {
				expect(t, s, 409, b)
				if !strings.Contains(string(b), "LEGACY_EVIDENCE_ACK_REQUIRED") {
					t.Fatal(string(b))
				}
				extra["acknowledgeLegacyEvidence"] = true
				s, b = g1Decision(f.director, fresh, "duplicate-decisions", extra)
			}
			if out := projectFrom(t, s, b); out.IntakeStatus != "APPROVED" {
				t.Fatal(out.IntakeStatus)
			}
			var details string
			_ = f.db.QueryRow(ctx, `SELECT details::text FROM audit_events WHERE project_id=$1 AND action='duplicate.dismiss'`, fresh.ID).Scan(&details)
			if !strings.Contains(details, want) {
				t.Fatal("audit grade", details)
			}
		})
	}
}
