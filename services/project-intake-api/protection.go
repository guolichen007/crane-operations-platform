package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

type ProtectionPolicy struct {
	WarningDays  int `json:"warningDays"`
	ExpiringDays int `json:"expiringDays"`
	ExpiredDays  int `json:"expiredDays"`
}

func protection(policy ProtectionPolicy, anchor, now time.Time) (string, time.Time, time.Time) {
	end := anchor.Add(time.Duration(policy.ExpiredDays) * 24 * time.Hour)
	state := "ACTIVE"
	next := anchor.Add(time.Duration(policy.WarningDays) * 24 * time.Hour)
	if !now.Before(next) {
		state = "WARNING"
		next = anchor.Add(time.Duration(policy.ExpiringDays) * 24 * time.Hour)
	}
	if !now.Before(anchor.Add(time.Duration(policy.ExpiringDays) * 24 * time.Hour)) {
		state = "EXPIRING"
		next = end
	}
	if !now.Before(end) {
		state = "EXPIRED"
		next = end
	}
	return state, end, next
}
func (a *App) protectionSweep(ctx context.Context, now time.Time) (int, error) {
	tx, e := a.db.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727104)`); e != nil {
		return 0, e
	}
	rows, e := tx.Query(ctx, `SELECT id::text FROM intake_projects WHERE intake_status IN ('SUBMITTED','UNDER_REVIEW','APPROVED') ORDER BY id FOR UPDATE`)
	if e != nil {
		return 0, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return 0, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	count := 0
	for _, id := range ids {
		changed, e := a.refreshProtection(ctx, tx, id, now, true)
		if e != nil {
			return 0, e
		}
		if changed {
			count++
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return 0, e
	}
	return count, nil
}
func (a *App) refreshProtection(ctx context.Context, tx pgx.Tx, id string, now time.Time, bump bool) (bool, error) {
	var policy ProtectionPolicy
	var b []byte
	if e := tx.QueryRow(ctx, `SELECT value FROM reference_values WHERE key='protection_policy'`).Scan(&b); e != nil {
		return false, e
	}
	if e := json.Unmarshal(b, &policy); e != nil {
		return false, e
	}
	if policy.WarningDays < 1 || policy.WarningDays >= policy.ExpiringDays || policy.ExpiringDays >= policy.ExpiredDays || policy.ExpiredDays > 3650 {
		return false, errors.New("invalid protection policy")
	}
	p, e := get(ctx, tx, id, false)
	if e != nil {
		return false, e
	}
	if !contains([]string{"SUBMITTED", "UNDER_REVIEW", "APPROVED"}, p.IntakeStatus) {
		return false, nil
	}
	anchor := p.CreatedAt
	if p.ProtectionBasis == "FIRST_SUBMISSION" {
		if p.ProtectionStartedAt == nil {
			return false, errors.New("missing first submission protection anchor")
		}
		anchor = *p.ProtectionStartedAt
	}
	if p.LastActivityAt != nil && p.LastActivityAt.After(anchor) {
		anchor = *p.LastActivityAt
	}
	state, end, next := protection(policy, anchor, now)
	if state == p.ProtectionStatus && p.ProtectedUntil != nil && p.NextReviewAt != nil && p.ProtectedUntil.Equal(end) && p.NextReviewAt.Equal(next) {
		return false, nil
	}
	increment := 0
	if bump {
		increment = 1
	}
	if _, e = tx.Exec(ctx, `UPDATE intake_projects SET protection_status=$2,protected_until=$3,next_review_at=$4,version=version+$5,updated_at=now() WHERE id=$1`, id, state, end, next, increment); e != nil {
		return false, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO audit_events(project_id,subject,action,revision,details) SELECT id,'maintenance','protection.sweep',version,$2 FROM intake_projects WHERE id=$1`, id, mustJSON(map[string]any{"from": p.ProtectionStatus, "to": state, "anchor": anchor, "nextReviewAt": next}))
	return true, e
}
