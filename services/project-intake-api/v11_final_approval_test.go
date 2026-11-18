package main

import (
	"context"
	"testing"
)

// V1.1 final quality closure (00015, PRD 9.3): approval history is immutable
// evidence. Every real review path (approve, return, resubmit, re-review,
// reject, closing via duplicate decisions, transfer) only appends; no ordinary
// UPDATE, DELETE or TRUNCATE can change an earlier approval, even for the
// table owner. (A superuser can disable triggers; that is out of scope.)
func TestDatabaseV11FinalApprovalImmutable(t *testing.T) {
	f := g1FixtureFor(t)
	ctx := context.Background()
	a1, ma := f.sales, f.manager
	// approve
	p := g1Submit(t, a1, g1Create(t, a1, "V11审批不可变-通过"))
	s, b := g1Decision(ma, v11Get(t, ma, p.ID), "approve", nil)
	expect(t, s, 200, b)
	// return -> resubmit -> re-review approve
	q := g1Submit(t, a1, g1Create(t, a1, "V11审批不可变-退回重提"))
	s, b = g1Decision(ma, v11Get(t, ma, q.ID), "return", map[string]any{"reason": "补充现场资料"})
	expect(t, s, 200, b)
	q = g1Submit(t, a1, v11Get(t, a1, q.ID))
	s, b = g1Decision(ma, v11Get(t, ma, q.ID), "approve", nil)
	expect(t, s, 200, b)
	// reject
	r := g1Submit(t, a1, g1Create(t, a1, "V11审批不可变-驳回"))
	s, b = g1Decision(ma, v11Get(t, ma, r.ID), "reject", map[string]any{"reason": "不属于报备范围"})
	expect(t, s, 200, b)
	// transfer after approval still works (writes ownership history, not approvals)
	s, b = g1Decision(ma, v11Get(t, ma, p.ID), "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
	expect(t, s, 200, b)

	snapshot := func() string {
		var h string
		if err := f.db.QueryRow(ctx, `SELECT md5(string_agg(id::text||project_id||subject||decision||reason||created_at::text||coalesce(submission_revision::text,''),',' ORDER BY id)) FROM approvals`).Scan(&h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	if n := countRows(t, f.db, `SELECT count(*) FROM approvals WHERE project_id IN ('`+p.ID+`','`+q.ID+`','`+r.ID+`')`); n != 4 {
		t.Fatal("approval rows (approve, return, re-approve, reject)", n)
	}
	before := snapshot()
	for _, sql := range []string{
		`UPDATE approvals SET reason='改写理由'`,
		`UPDATE approvals SET decision='APPROVE' WHERE decision<>'APPROVE'`,
		`UPDATE approvals SET subject='synthetic-director'`,
		`DELETE FROM approvals WHERE project_id='` + r.ID + `'`,
		`DELETE FROM approvals`,
		`TRUNCATE approvals`,
		`TRUNCATE approvals CASCADE`,
	} {
		if _, err := f.db.Exec(ctx, sql); !isCode(err, "BB002") {
			t.Fatalf("approval history changed by %q: %v", sql, err)
		}
	}
	if snapshot() != before {
		t.Fatal("approval history changed")
	}
	// Appending through the business path still works after the guards.
	z := g1Submit(t, a1, g1Create(t, a1, "V11审批不可变-追加"))
	s, b = g1Decision(ma, v11Get(t, ma, z.ID), "approve", nil)
	expect(t, s, 200, b)
	if n := countRows(t, f.db, `SELECT count(*) FROM approvals WHERE project_id='`+z.ID+`'`); n != 1 {
		t.Fatal("append after guard", n)
	}
	var triggers int
	_ = f.db.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE tgrelid='approvals'::regclass AND tgname IN ('approval_immutable','approval_no_truncate') AND tgenabled='O'`).Scan(&triggers)
	if triggers != 2 {
		t.Fatal("approval triggers", triggers)
	}
}
