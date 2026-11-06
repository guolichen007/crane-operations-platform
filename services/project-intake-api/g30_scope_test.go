package main

import (
	"encoding/json"
	"testing"
)

// G3-0: the legacy-candidate count must not reveal anything about candidates
// the reviewer cannot read. Two teams that cannot see each other's projects,
// the same duplicate relation, different evidence on the hidden candidate:
// the restricted reviewer must receive identical review hints.
func TestDatabaseG30LegacyCountScope(t *testing.T) {
	type view struct {
		Legacy, Pending int
		Related         string
	}
	observe := func(t *testing.T, legacy bool) (view, view) {
		f := g1FixtureFor(t)
		name := "合成跨组计数"
		// Hidden candidate belongs to team two (synthetic-c).
		other := login(t, f.server, "synthetic-c")
		cand := g1Submit(t, other, g1Create(t, other, name))
		if legacy {
			g1Exec(t, f.db, `ALTER TABLE intake_projects DROP CONSTRAINT submission_projection`)
			g1Exec(t, f.db, `UPDATE intake_projects SET intake_status='APPROVED',evidence_state='INCOMPLETE',submission_revision=0,submitted_by_subject=NULL WHERE id=$1`, cand.ID)
		}
		// Primary filing in team one, reviewed by team one's manager.
		fresh := g1Submit(t, f.other, g1Create(t, f.other, name))
		read := func(c testClient) view {
			s, b := c.call("GET", "/api/v1/projects/"+fresh.ID, nil)
			p := projectFrom(t, s, b)
			if p.Review == nil {
				t.Fatal("no review hints")
			}
			r, _ := json.Marshal(p.Review.RelatedCandidates)
			return view{p.Review.LegacyCandidates, p.Review.PendingDuplicates, string(r)}
		}
		return read(f.manager), read(f.director)
	}
	var mgr, dir [2]view
	mgr[0], dir[0] = observe(t, false)
	mgr[1], dir[1] = observe(t, true)
	t.Logf("LEGACY_COUNT manager complete=%+v legacy=%+v director complete=%+v legacy=%+v", mgr[0], mgr[1], dir[0], dir[1])
	if mgr[0] != mgr[1] {
		t.Fatalf("restricted manager can infer hidden candidate evidence: %+v vs %+v", mgr[0], mgr[1])
	}
	// G3-2A: hidden pending cases are not counted for the restricted manager.
	if mgr[0].Legacy != 0 || mgr[0].Pending != 0 {
		t.Fatal("restricted manager view", mgr[0])
	}
	// The director sees the candidate and must still get the real count.
	if dir[0].Legacy != 0 || dir[1].Legacy != 1 {
		t.Fatal("director must see legacy count", dir)
	}
}
