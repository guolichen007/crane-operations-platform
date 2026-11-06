package main

// G3-2A minimum-disclosure projection for related duplicate candidates.
//
// A candidate the caller cannot read (ProjectPolicy.Read == false) is never
// described in a response: no ID, case ID, score, risk, signals, region,
// stage, dates, decision or evidence grade, and it does not occupy an array
// slot, so the number of hidden candidates is not observable either. The only
// disclosure is one fixed notice saying that at least one related candidate
// needs a higher-level reviewer. Server-side decisions (independence, frozen
// evidence, pending duplicate checks) still use every candidate; this file
// only shapes responses.

// RestrictedRelated is the single fixed notice. Its content depends only on
// the caller's role, never on the hidden candidates.
type RestrictedRelated struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Route   string `json:"route"`
}

const restrictedRelatedCode = "RESTRICTED_RELATED_PRESENT"

func restrictedNotice(u User, hiddenPresent bool) *RestrictedRelated {
	if !hiddenPresent {
		return nil
	}
	if u.Role == salesRole {
		// Sales cannot tell whether a hidden candidate is in their own team or
		// another; the route is deliberately generic.
		return &RestrictedRelated{Code: restrictedRelatedCode, Route: "REVIEWER",
			Message: "检测到可能重复的已有报备。当前账号无权查看详情，提交后由审核人员核验。"}
	}
	return &RestrictedRelated{Code: restrictedRelatedCode, Route: "SALES_DIRECTOR",
		Message: "检测到需要跨组核验的相关项目。当前账号无权查看详情，请销售总监处理。"}
}

// projectDuplicates is used by GET /projects/{id}/duplicates and
// POST /duplicate-checks. Readable candidates keep the pre-G3-2A legal detail
// (sales never received candidate IDs or names, even for readable ones).
func projectDuplicates(ds []Duplicate, u User) ([]Duplicate, *RestrictedRelated) {
	out := []Duplicate{}
	hidden := false
	for _, d := range ds {
		if !canRead(u, d.source) {
			hidden = true
			continue
		}
		if u.Role == salesRole {
			d.CandidateID = ""
			d.ProjectName = ""
		}
		out = append(out, d)
	}
	return out, restrictedNotice(u, hidden)
}

// projectComparison is used by GET /projects/{id}/duplicate-comparison.
func projectComparison(cases []Comparison, u User) ([]Comparison, *RestrictedRelated) {
	out := []Comparison{}
	hidden := false
	for _, c := range cases {
		if c.Restricted {
			hidden = true
			continue
		}
		out = append(out, c)
	}
	return out, restrictedNotice(u, hidden)
}
