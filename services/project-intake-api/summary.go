package main

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// WorkspaceSummary is the minimal, server-side home page aggregate for G2.
// Every count uses the same projectScope as lists and details, a single SQL
// statement (one snapshot) and one asOf instant. Pipeline columns, funnels,
// amounts and trends are G3.
type WorkspaceSummary struct {
	AsOf     time.Time      `json:"asOf"`
	Scope    string         `json:"scope"`
	Counts   map[string]int `json:"counts"`
	Recent   []RecentItem   `json:"recent"`
	Followup []RecentItem   `json:"followups"`
}
type RecentItem struct {
	ID             string     `json:"id"`
	RegistrationNo string     `json:"registrationNo"`
	ProjectName    string     `json:"projectName"`
	Customer       string     `json:"customer"`
	IntakeStatus   string     `json:"intakeStatus"`
	SalesStage     string     `json:"salesStage"`
	OwnerName      string     `json:"ownerName"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	NextFollowupAt *time.Time `json:"nextFollowupAt"`
	NextAction     string     `json:"nextAction"`
}

// jitOffSQL disables JIT for the rest of the current transaction only
// (G3-3-P0, approved scope: review queue and workspace summary).
const jitOffSQL = `SET LOCAL jit = off`

// jitDefaultSQL returns to the session value inside the same transaction.
const jitDefaultSQL = `SET LOCAL jit TO DEFAULT`

// Count keys and their definitions, also documented in the G2 acceptance doc.
//
//	total               all projects in scope
//	drafts              DRAFT (assigned or not)
//	returned            RETURNED
//	unassignedDrafts    DRAFT with no owner (admin scope only; 0 for others)
//	submitted/underReview/approved   effective filings by status
//	overdue             next_followup_at <  asOf
//	upcoming7d          asOf <= next_followup_at < asOf+7d
//	reviewQueue         SUBMITTED or UNDER_REVIEW in scope (review roles; 0 for sales)
//	reviewable          reviewQueue items the caller can independently decide now
func (a *App) workspaceSummary(ctx context.Context, u User) (WorkspaceSummary, error) {
	asOf := time.Now().UTC().Truncate(time.Microsecond)
	s := WorkspaceSummary{AsOf: asOf, Scope: map[string]string{salesRole: "OWN", managerRole: "TEAM", directorRole: "ALL_ASSIGNED", adminRole: "ALL"}[u.Role], Counts: map[string]int{}}
	// One short read-only transaction so SET LOCAL can apply. READ COMMITTED
	// keeps the previous per-statement snapshots (the three statements were
	// separate autocommit reads before); asOf is unchanged.
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return s, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, jitOffSQL); err != nil {
		return s, err
	}
	var total, drafts, returned, unassigned, submitted, review, approved, overdue, upcoming, queue, reviewable int
	err = tx.QueryRow(ctx, `SELECT count(*),
 count(*) FILTER (WHERE p.intake_status='DRAFT'),
 count(*) FILTER (WHERE p.intake_status='RETURNED'),
 count(*) FILTER (WHERE p.intake_status='DRAFT' AND p.owner_subject IS NULL),
 count(*) FILTER (WHERE p.intake_status='SUBMITTED'),
 count(*) FILTER (WHERE p.intake_status='UNDER_REVIEW'),
 count(*) FILTER (WHERE p.intake_status='APPROVED'),
 count(*) FILTER (WHERE p.next_followup_at<$4),
 count(*) FILTER (WHERE p.next_followup_at>=$4 AND p.next_followup_at<$5),
 count(*) FILTER (WHERE $2<>'baobei_sales' AND p.intake_status IN ('SUBMITTED','UNDER_REVIEW')),
 count(*) FILTER (WHERE `+reviewableSQL("p")+`)
 FROM intake_projects p WHERE `+projectScope("p"), u.Subject, u.Role, u.TeamID, asOf, asOf.Add(7*24*time.Hour)).
		Scan(&total, &drafts, &returned, &unassigned, &submitted, &review, &approved, &overdue, &upcoming, &queue, &reviewable)
	if err != nil {
		return s, err
	}
	for k, v := range map[string]int{"total": total, "drafts": drafts, "returned": returned, "unassignedDrafts": unassigned, "submitted": submitted, "underReview": review, "approved": approved, "overdue": overdue, "upcoming7d": upcoming, "reviewQueue": queue, "reviewable": reviewable} {
		s.Counts[k] = v
	}
	recent := func(where, order string, limit int, args ...any) ([]RecentItem, error) {
		rows, err := tx.Query(ctx, `SELECT p.id::text,p.registration_no,p.project_name,coalesce(p.document->>'customer',''),p.intake_status,p.sales_stage,`+displayName("p.owner_subject")+`,p.updated_at,p.next_followup_at,p.next_action
 FROM intake_projects p WHERE `+projectScope("p")+where+` ORDER BY `+order+`,p.id LIMIT `+map[int]string{5: "5", 6: "6"}[limit], append([]any{u.Subject, u.Role, u.TeamID}, args...)...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []RecentItem{}
		for rows.Next() {
			var r RecentItem
			if err = rows.Scan(&r.ID, &r.RegistrationNo, &r.ProjectName, &r.Customer, &r.IntakeStatus, &r.SalesStage, &r.OwnerName, &r.UpdatedAt, &r.NextFollowupAt, &r.NextAction); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	if s.Recent, err = recent(``, `p.updated_at DESC`, 6); err != nil {
		return s, err
	}
	if s.Followup, err = recent(` AND p.next_followup_at<$4`, `p.next_followup_at ASC`, 5, asOf.Add(7*24*time.Hour)); err != nil {
		return s, err
	}
	return s, tx.Commit(ctx)
}

func (a *App) summaryHandler(w http.ResponseWriter, r *http.Request, u User) error {
	s, err := a.workspaceSummary(r.Context(), u)
	if err != nil {
		return err
	}
	respond(w, 200, s)
	return nil
}
