package main

import "context"

// Review eligibility is derived from one set of SQL predicates. POST decisions,
// action hints, list hints and dashboard counts all use these fragments so the
// UI never advertises an action the transaction would reject (or vice versa).

// primaryConflictSQL: actor participated in the current submission round of p.
func primaryConflictSQL(p, actor string) string {
	return `coalesce(` + p + `.created_by_subject=` + actor + ` OR ` + p + `.owner_subject=` + actor + ` OR ` + p + `.submitted_by_subject=` + actor + `
 OR EXISTS(SELECT 1 FROM project_submissions s WHERE s.project_id=` + p + `.id AND s.revision=` + p + `.submission_revision
 AND (s.actor_subject=` + actor + ` OR s.creator_subject=` + actor + ` OR s.owner_subject=` + actor + ` OR s.participants ? ` + actor + `
 OR EXISTS(SELECT 1 FROM project_participations a WHERE a.project_id=s.project_id AND a.source_version>=s.source_version
 AND (a.actor_subject=` + actor + ` OR a.owner_subject=` + actor + `)))),false)`
}

// primaryCompleteSQL: the current submission has complete, frozen evidence.
func primaryCompleteSQL(p string) string {
	return `(` + p + `.evidence_state='COMPLETE' AND ` + p + `.submission_revision>0 AND EXISTS(SELECT 1 FROM project_submissions s
 WHERE s.project_id=` + p + `.id AND s.revision=` + p + `.submission_revision AND s.evidence_state='COMPLETE'))`
}

// candidateConflictSQL: actor ever created, owned, submitted, edited, assigned
// or held c. Candidates do not need a COMPLETE submission snapshot: migrated
// history is INCOMPLETE by design and must not block decisions on new filings.
// Missing history is treated conservatively through creator/owner/ownership rows.
// NULL columns (unassigned owner, legacy submitter) never make the result NULL.
func candidateConflictSQL(c, actor string) string {
	return `coalesce(` + c + `.created_by_subject=` + actor + ` OR ` + c + `.owner_subject=` + actor + ` OR ` + c + `.submitted_by_subject=` + actor + `
 OR EXISTS(SELECT 1 FROM project_submissions s WHERE s.project_id=` + c + `.id AND (s.actor_subject=` + actor + ` OR s.creator_subject=` + actor + `
 OR s.owner_subject=` + actor + ` OR s.participants ? ` + actor + `))
 OR EXISTS(SELECT 1 FROM project_participations x WHERE x.project_id=` + c + `.id AND (x.actor_subject=` + actor + ` OR x.owner_subject=` + actor + `))
 OR EXISTS(SELECT 1 FROM ownership_history h WHERE h.project_id=` + c + `.id AND (h.actor_subject=` + actor + ` OR h.from_subject=` + actor + ` OR h.to_subject=` + actor + `)),false)`
}

// relatedCandidatesSQL mirrors relatedReviewProjects: current-round cases plus
// every already-decided case. Callers append predicates on alias c.
func relatedCandidatesSQL(p string) string {
	return `SELECT 1 FROM duplicate_cases d JOIN intake_projects c ON c.id=d.candidate_id WHERE d.project_id=` + p + `.id
 AND (d.submission_revision=` + p + `.submission_revision OR d.decision<>'PENDING')`
}
func pendingCasesSQL(p string) string {
	return `(SELECT count(*) FROM duplicate_cases d WHERE d.project_id=` + p + `.id AND d.decision='PENDING' AND d.submission_revision=` + p + `.submission_revision)`
}

// reviewableSQL is true when the session ($1 subject, $2 role, $3 team) can
// make the next required decision on p: approve (no pending duplicates) or a
// duplicate decision (all related candidates visible and independent).
func reviewableSQL(p string) string {
	return `(` + p + `.intake_status IN ('SUBMITTED','UNDER_REVIEW') AND $2 IN ('baobei_manager','baobei_director','baobei_admin')
 AND ` + projectScope(p) + ` AND ` + primaryCompleteSQL(p) + ` AND NOT ` + primaryConflictSQL(p, "$1") + `
 AND (` + pendingCasesSQL(p) + `=0 OR (NOT EXISTS(` + relatedCandidatesSQL(p) + ` AND NOT ` + projectScope("c") + `)
 AND NOT EXISTS(` + relatedCandidatesSQL(p) + ` AND ` + candidateConflictSQL("c", "$1") + `))))`
}

type ReviewEligibility struct {
	ProjectReadable     bool       `json:"projectReadable"`
	ProjectIndependence ActionHint `json:"projectIndependence"`
	RelatedCandidates   ActionHint `json:"relatedCandidates"`
	// Pending duplicate cases whose candidate the viewer can read. Hidden
	// candidates are never counted here (G3-2A); their presence is reported only
	// through RelatedCandidates=REVIEW_RELATED_PROJECT_RESTRICTED. Decision
	// gating still uses every pending case on the server.
	PendingDuplicates int `json:"pendingDuplicates"`
	// Related candidates that only have migrated, partial evidence. For these
	// "no participation found" is not proof of independence. Counted only
	// within the viewer's read scope; hidden candidates are reported solely via
	// RelatedCandidates=REVIEW_RELATED_PROJECT_RESTRICTED.
	LegacyCandidates int     `json:"legacyCandidates"`
	Eligible         bool    `json:"eligible"`
	ReasonCode       *string `json:"reasonCode"`
}

type reviewFacts struct {
	primaryConflict, primaryComplete, candidateRestricted, candidateConflict bool
	pending, visiblePending, legacy                                          int
}

func loadReviewFacts(ctx context.Context, q querier, u User, ids []string) (map[string]reviewFacts, error) {
	out := map[string]reviewFacts{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT p.id::text,`+primaryConflictSQL("p", "$1")+`,`+primaryCompleteSQL("p")+`,
 EXISTS(`+relatedCandidatesSQL("p")+` AND NOT `+projectScope("c")+`),
 EXISTS(`+relatedCandidatesSQL("p")+` AND `+candidateConflictSQL("c", "$1")+`),`+pendingCasesSQL("p")+`,
 (SELECT count(DISTINCT c.id) FROM duplicate_cases d JOIN intake_projects c ON c.id=d.candidate_id WHERE d.project_id=p.id
  AND d.decision='PENDING' AND d.submission_revision=p.submission_revision AND `+projectScope("c")+` AND NOT `+primaryCompleteSQL("c")+`),
 (SELECT count(*) FROM duplicate_cases d JOIN intake_projects c ON c.id=d.candidate_id WHERE d.project_id=p.id
  AND d.decision='PENDING' AND d.submission_revision=p.submission_revision AND `+projectScope("c")+`)
 FROM intake_projects p WHERE p.id=ANY($4::uuid[])`, u.Subject, u.Role, u.TeamID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var f reviewFacts
		if err = rows.Scan(&id, &f.primaryConflict, &f.primaryComplete, &f.candidateRestricted, &f.candidateConflict, &f.pending, &f.legacy, &f.visiblePending); err != nil {
			return nil, err
		}
		out[id] = f
	}
	return out, rows.Err()
}

// reviewIndependence is the transactional check for the project being decided.
func reviewIndependence(ctx context.Context, q querier, u User, p Project) error {
	if !(ProjectPolicy{u}).Read(p) {
		return problem(403, "REVIEW_RELATED_PROJECT_RESTRICTED")
	}
	if p.EvidenceState != "COMPLETE" || p.SubmissionRevision < 1 {
		return problem(409, "REVIEW_EVIDENCE_INCOMPLETE")
	}
	var conflict, complete bool
	err := q.QueryRow(ctx, `SELECT `+primaryConflictSQL("p", "$1")+`,`+primaryCompleteSQL("p")+` FROM intake_projects p WHERE p.id=$2`, u.Subject, p.ID).Scan(&conflict, &complete)
	if err != nil {
		return err
	}
	if conflict || u.Subject == p.CreatedBySubject || u.Subject == p.OwnerSubject {
		return problem(403, "REVIEW_CONFLICT")
	}
	if !complete {
		return problem(409, "REVIEW_EVIDENCE_INCOMPLETE")
	}
	return nil
}

// candidateIndependence checks a related duplicate candidate. It must be fully
// visible and the actor must never have participated in it.
func candidateIndependence(ctx context.Context, q querier, u User, c Project) error {
	if !(ProjectPolicy{u}).Read(c) {
		return problem(403, "REVIEW_RELATED_PROJECT_RESTRICTED")
	}
	var conflict bool
	if err := q.QueryRow(ctx, `SELECT `+candidateConflictSQL("c", "$1")+` FROM intake_projects c WHERE c.id=$2`, u.Subject, c.ID).Scan(&conflict); err != nil {
		return err
	}
	if conflict {
		return problem(403, "REVIEW_CONFLICT")
	}
	return nil
}

var reviewActions = []string{"approve", "return", "reject", "duplicate-decisions"}

// projectHints computes action hints for one or many projects with a constant
// number of queries (owner lookup + review facts), independent of page size.
func (a *App) projectHints(ctx context.Context, q querier, u User, p *Project) error {
	return a.batchHints(ctx, q, u, []*Project{p})
}
func (a *App) batchHints(ctx context.Context, q querier, u User, ps []*Project) error {
	policy := ProjectPolicy{u}
	owners, reviewIDs := []string{}, []string{}
	for _, p := range ps {
		if p.OwnerSubject != "" && policy.Action(*p, "submit") == "" {
			owners = append(owners, p.OwnerSubject)
		}
		if policy.Manage() && policy.Read(*p) && contains([]string{"SUBMITTED", "UNDER_REVIEW"}, p.IntakeStatus) {
			reviewIDs = append(reviewIDs, p.ID)
		}
	}
	teams := map[string]string{}
	if len(owners) > 0 {
		rows, err := q.Query(ctx, `SELECT subject,team_id::text FROM app_users WHERE subject=ANY($1) AND active AND role='baobei_sales' AND team_id IS NOT NULL`, owners)
		if err != nil {
			return err
		}
		for rows.Next() {
			var s, t string
			if err = rows.Scan(&s, &t); err != nil {
				rows.Close()
				return err
			}
			teams[s] = t
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	facts, err := loadReviewFacts(ctx, q, u, reviewIDs)
	if err != nil {
		return err
	}
	for _, p := range ps {
		p.AssignmentStatus = "ASSIGNED"
		if p.OwnerSubject == "" {
			p.AssignmentStatus = "UNASSIGNED_DRAFT"
		}
		p.Actions = map[string]ActionHint{}
		f, hasFacts := facts[p.ID]
		primary := ""
		if hasFacts {
			if !f.primaryComplete {
				primary = "REVIEW_EVIDENCE_INCOMPLETE"
			} else if f.primaryConflict {
				primary = "REVIEW_CONFLICT"
			}
		}
		related := ""
		if f.candidateRestricted {
			related = "REVIEW_RELATED_PROJECT_RESTRICTED"
		} else if f.candidateConflict {
			related = "REVIEW_CONFLICT"
		}
		for _, action := range []string{"edit", "submit", "activities", "stage-change", "ownership-transfer", "approve", "return", "reject", "duplicate-decisions"} {
			code := policy.Action(*p, action)
			if code == "" && action == "submit" {
				if team, ok := teams[p.OwnerSubject]; !ok || team != p.TeamID {
					code = "OWNER_INACTIVE"
				}
			}
			if code == "" && contains(reviewActions, action) {
				code = primary
				if code == "" && action == "duplicate-decisions" {
					if f.pending == 0 {
						code = "INVALID_STATE"
					} else {
						code = related
					}
				}
				if code == "" && action == "approve" && f.pending > 0 {
					code = "DUPLICATE_DECISION_REQUIRED"
				}
			}
			p.Actions[action] = hint(code)
		}
		p.Review = nil
		if hasFacts {
			r := &ReviewEligibility{ProjectReadable: true, ProjectIndependence: hint(firstCode(policy.Action(*p, "return"), primary)), RelatedCandidates: hint(related), PendingDuplicates: f.visiblePending, LegacyCandidates: f.legacy}
			next := "approve"
			if f.pending > 0 {
				next = "duplicate-decisions"
			}
			r.Eligible = p.Actions[next].Allowed
			r.ReasonCode = p.Actions[next].ReasonCode
			p.Review = r
		}
	}
	return nil
}
func firstCode(codes ...string) string {
	for _, c := range codes {
		if c != "" {
			return c
		}
	}
	return ""
}
