package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Account operation recovery (G4-S5-R1-F).
//
// An operation is recovered when its claim has expired (the process driving it
// died or lost the database) or when an administrator asks to reconcile a REVIEW
// (待核对) operation. Recovery always reads Keycloak first and then either
// completes the operation (only when Keycloak shows exactly the intended
// result), compensates it, or leaves it in REVIEW. It never repeats a creation,
// a password reset or a role change whose outcome is unknown.

const recoveryActor = "system:account-recovery"

// claimForRecovery takes over one operation: an expired STARTED claim, or a
// REVIEW operation when an administrator reconciles. Returns nil if there is
// nothing to take (another process holds a live claim, or it already finished).
func (a *App) claimForRecovery(ctx context.Context, pool *pgxpool.Pool, opID, actor string, review bool) (*accountOp, error) {
	o := &accountOp{lease: newUUID(), actor: actor}
	var kcID, target, previous *string
	cond := `state='STARTED' AND lease_expires_at<now()`
	if review {
		cond = `(state='REVIEW' OR state='STARTED' AND lease_expires_at<now())`
	}
	err := pool.QueryRow(ctx, `UPDATE account_operations SET state='STARTED',lease_token=$2,lease_expires_at=now()+$3::interval,attempts=attempts+1,updated_at=now()
 WHERE id=$1 AND `+cond+`
 RETURNING id::text,staff_id::text,operation,step,reason,keycloak_user_id,target_login,previous_login,created_user`, opID, o.lease, leaseInterval()).
		Scan(&o.id, &o.staffID, &o.operation, &o.step, &o.reason, &kcID, &target, &previous, &o.createdUser)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for dst, src := range map[*string]*string{&o.kcID: kcID, &o.targetLogin: target, &o.previousLogin: previous} {
		if src != nil {
			*dst = *src
		}
	}
	return o, nil
}

// recoverAccountOps reconciles every operation whose claim has expired. It runs
// at start-up and periodically; tests call it directly.
func (a *App) recoverAccountOps(ctx context.Context) (int, error) {
	pool := a.staffPool()
	if pool == nil || a.accounts == nil {
		return 0, nil
	}
	rows, err := pool.Query(ctx, `SELECT id::text FROM account_operations WHERE state='STARTED' AND lease_expires_at<now() ORDER BY created_at LIMIT 50`)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		o, err := a.claimForRecovery(ctx, pool, id, recoveryActor, false)
		if err != nil {
			return n, err
		}
		if o == nil {
			continue
		}
		var crash crashError
		if err = a.reconcile(ctx, pool, o); errors.As(err, &crash) {
			return n, err
		}
		n++
	}
	return n, nil
}

// accountReconcileAction serves POST /api/v1/staff/{id}/account/reconcile: the
// administrator's "核对账号状态" for a member whose account is REVIEW, or whose
// operation's claim has expired.
func (a *App) accountReconcileAction(ctx context.Context, w http.ResponseWriter, pool *pgxpool.Pool, actor User, _ string, staffID string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	if _, err = adminSession(ctx, tx, actor); err != nil {
		tx.Rollback(ctx)
		return err
	}
	m, err := readStaff(ctx, tx, actor, staffID)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	var opID, state string
	var expired bool
	err = tx.QueryRow(ctx, `SELECT id::text,state,coalesce(lease_expires_at<now(),true) FROM account_operations
 WHERE staff_id=$1 AND state IN ('STARTED','REVIEW')`, staffID).Scan(&opID, &state, &expired)
	tx.Rollback(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		respond(w, 200, accountResult{Member: m})
		return nil
	}
	if err != nil {
		return err
	}
	if state == "STARTED" && !expired {
		return accountBusy
	}
	if !m.CanManage && m.Status != staffSuspended {
		return staffDenied
	}
	o, err := a.claimForRecovery(ctx, pool, opID, actor.Subject, true)
	if err != nil {
		return err
	}
	if o == nil {
		return accountBusy
	}
	rerr := a.reconcile(ctx, pool, o)
	var crash crashError
	if errors.As(rerr, &crash) {
		return rerr
	}
	member, err := a.readMember(ctx, pool, actor, staffID)
	if err != nil {
		return err
	}
	var p staffProblem
	if errors.As(rerr, &p) && p.code == "ACCOUNT_RESULT_PENDING" {
		return p
	}
	respond(w, 200, accountResult{Member: member, OperationID: o.id})
	return nil
}

// reconcile drives one claimed operation to a final or REVIEW state from the
// real Keycloak state. Errors from settle functions only describe how the
// original request ended; for recovery the stored state is what matters.
func (a *App) reconcile(ctx context.Context, pool *pgxpool.Pool, o *accountOp) error {
	if err := a.inject("recover:" + o.operation); err != nil {
		return err
	}
	var subject, status, accountState string
	if err := pool.QueryRow(ctx, `SELECT subject,staff_status,account_state FROM app_users WHERE staff_id=$1`, o.staffID).Scan(&subject, &status, &accountState); err != nil {
		return err
	}
	m := staffMember{ID: o.staffID, Subject: subject, Status: status, AccountState: accountState}
	if o.step == "OPENED" && o.operation != "CREATE" {
		// Nothing was sent to Keycloak.
		return a.settleUnchanged(ctx, pool, o, "FAILED", "NOT_STARTED")
	}
	switch o.operation {
	case "CREATE":
		return a.reconcileCreate(ctx, pool, o)
	case "RESET_PASSWORD":
		// Never repeated: the lost password is unknown to everybody. Sessions end,
		// a new password is required, the administrator resets again.
		return a.settleResetLost(ctx, pool, o, m, nil)
	case "CHANGE_PHONE":
		u, err := a.accounts.getUser(ctx, o.kcID)
		switch {
		case err != nil:
			return a.review(ctx, pool, o, "PROVIDER_UNAVAILABLE")
		case u == nil:
			return a.review(ctx, pool, o, "LOGIN_MISSING")
		case u.Username == o.targetLogin:
			return a.finishChangePhone(ctx, pool, o)
		case u.Username == o.previousLogin:
			return a.settleUnchanged(ctx, pool, o, "FAILED", "NOT_APPLIED")
		}
		return a.review(ctx, pool, o, "USERNAME_MISMATCH")
	case "DISABLE":
		u, err := a.accounts.getUser(ctx, o.kcID)
		if err != nil {
			return a.review(ctx, pool, o, "PROVIDER_UNAVAILABLE")
		}
		if enabled(u) {
			if a.accounts.update(ctx, o.kcID, kcUser{Enabled: boolp(false)}) != nil {
				return a.review(ctx, pool, o, "DISABLE_UNCONFIRMED")
			}
		}
		_ = a.accounts.logout(ctx, o.kcID)
		return a.finishDisable(ctx, pool, o)
	case "ENABLE", "SET_ROLE":
		var role string
		if err := pool.QueryRow(ctx, `SELECT role FROM app_users WHERE staff_id=$1`, o.staffID).Scan(&role); err != nil {
			return err
		}
		u, err := a.accounts.getUser(ctx, o.kcID)
		roles, rerr := a.accounts.roles(ctx, o.kcID)
		if err == nil && rerr == nil && enabled(u) && exactClientRole(baobeiRoles(roles), role) {
			// Keycloak shows exactly the intended result: complete it.
			return a.finishEnable(ctx, pool, o)
		}
		return a.abandonEnable(ctx, pool, o, nil)
	}
	return a.review(ctx, pool, o, "UNKNOWN_OPERATION")
}

// reconcileCreate: the one-time password of an interrupted creation never
// reached anyone, so the attempt is always undone (delete the user this attempt
// created, or disable a pre-existing one of this member) and recorded as failed;
// the administrator opens the account again and gets a fresh password.
func (a *App) reconcileCreate(ctx context.Context, pool *pgxpool.Pool, o *accountOp) error {
	if o.kcID == "" && o.targetLogin != "" {
		u, err := a.accounts.findByUsername(ctx, o.targetLogin)
		if err != nil {
			return a.review(ctx, pool, o, "PROVIDER_UNAVAILABLE")
		}
		if ownedBy(u, o.staffID) {
			// Created by this attempt before its id could be recorded (KC_CREATE),
			// or this member's own disabled user resumed by it.
			o.kcID, o.createdUser = u.ID, o.step == "KC_CREATE"
		}
	}
	if o.kcID != "" {
		u, err := a.accounts.getUser(ctx, o.kcID)
		if err != nil {
			return a.review(ctx, pool, o, "PROVIDER_UNAVAILABLE")
		}
		if u != nil && !ownedBy(u, o.staffID) {
			// The recorded id belongs to somebody else: never touch it.
			return a.review(ctx, pool, o, "OWNER_MISMATCH")
		}
		if u == nil {
			o.kcID, o.createdUser = "", false
		}
	}
	return a.compensateCreate(ctx, pool, o, nil)
}

// settleUnchanged: Keycloak shows nothing changed. The operation ends; a member
// parked in REVIEW by this operation returns to the state Keycloak shows.
func (a *App) settleUnchanged(ctx context.Context, pool *pgxpool.Pool, o *accountOp, state, detail string) error {
	enabledNow := false
	if o.kcID != "" {
		u, err := a.accounts.getUser(ctx, o.kcID)
		if err != nil {
			return a.review(ctx, pool, o, "PROVIDER_UNAVAILABLE")
		}
		enabledNow = enabled(u)
	}
	return stored(o, a.commitOp(ctx, pool, o, state, detail, "settle", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE app_users SET account_state=CASE
   WHEN staff_status='SUSPENDED' OR NOT $2 THEN 'DISABLED' ELSE 'READY' END
 WHERE staff_id=$1 AND account_state='REVIEW'`, o.staffID, enabledNow)
		return err
	}), nil)
}
