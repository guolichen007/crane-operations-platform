package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// V1.1 go-live: first-time account claim (首次认领, migration 00016).
//
// An administrator may open a login under a unique temporary name (claim_login,
// e.g. sales01) instead of a phone. Keycloak forces a new password at the first
// sign-in (temporary password). The member then must enter their real name and
// real mainland mobile; the account service renames the Keycloak user to the
// phone (the temporary name stops working), stores phone and name in one
// transaction, ends every session, and the member signs in again with the
// phone. Until then the API serves only GET /api/v1/me and this claim.
//
// The claim is an ordinary account operation (CLAIM) with the same idempotency
// key, single open operation per member and per login, lease, recovery and
// REVIEW rules as the administrator's operations. The staff id, subject,
// identity link, role and team never change.
const (
	claimNone    = "NONE"
	claimPending = "PENDING"
	claimDone    = "DONE"
)

var (
	claimStillPending = staffProblem{409, "ACCOUNT_CLAIM_PENDING", "该成员尚未完成首次认领：手机号由成员本人在首次登录时填写"}
	claimRequired     = staffProblem{403, "ACCOUNT_CLAIM_REQUIRED", "请先完成首次登录认领：填写本人姓名和手机号"}
	claimNotPending   = staffProblem{409, "ACCOUNT_CLAIM_NOT_PENDING", "该账号无需认领或已完成认领，请重新登录"}
)

type claimRequest struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

type claimResult struct {
	Claimed  bool   `json:"claimed"`
	Login    string `json:"login"`
	Replayed bool   `json:"replayed,omitempty"`
}

// claimHandler serves POST /api/v1/me/claim for the member's own live session.
func (a *App) claimHandler(w http.ResponseWriter, r *http.Request, u User) error {
	if r.Method != "POST" {
		fail(w, 405, "METHOD_NOT_ALLOWED", "操作不支持")
		return nil
	}
	if a.accounts == nil {
		return accountNotSupported
	}
	pool := a.staffPool()
	if pool == nil {
		return errors.New("staff database unavailable")
	}
	key := r.Header.Get("Idempotency-Key")
	if !createKeyPattern.MatchString(key) {
		return accountKeyInvalid
	}
	var in claimRequest
	if !decode(w, r, &in) {
		return nil
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 64 || digitsOnly.MatchString(in.Name) || !identityText(in.Name) {
		failField(w, 400, "PROFILE_NAME_INVALID", "请填写你的真实姓名（1–64 字，不能只包含数字）", "name")
		return nil
	}
	phone, err := normalizedPhone(in.Phone)
	if err != nil {
		failField(w, 400, "ACCOUNT_PHONE_MAINLAND_ONLY", "请填写本人的中国大陆 11 位手机号", "phone")
		return nil
	}
	login, err := loginName(phone)
	if err != nil {
		failField(w, 400, "ACCOUNT_PHONE_MAINLAND_ONLY", "请填写本人的中国大陆 11 位手机号", "phone")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 25*time.Second)
	defer cancel()
	o, m, replay, err := a.openClaim(ctx, pool, u, key, in.Name, phone, login)
	if err != nil {
		return err
	}
	if replay != nil {
		respond(w, 200, replay)
		return nil
	}
	if err = a.claimAccount(ctx, pool, o, m); err != nil {
		var crash crashError
		if errors.As(err, &crash) {
			return err
		}
		var p staffProblem
		if errors.As(err, &p) {
			return p
		}
		return accountPending(o.id)
	}
	a.cookie(w, a.cookieName(), "", -1)
	respond(w, 200, claimResult{Claimed: true, Login: login})
	return nil
}

func (a *App) openClaim(ctx context.Context, pool *pgxpool.Pool, u User, key, name, phone, login string) (*accountOp, staffMember, *claimResult, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, staffMember{}, nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, identityLock); err != nil {
		return nil, staffMember{}, nil, err
	}
	// The member of this live session under the current identity epoch; never a
	// caller-supplied subject or staff id.
	var m staffMember
	err = tx.QueryRow(ctx, `SELECT a.staff_id::text,a.subject,a.staff_status,a.account_state,coalesce(a.claim_login,''),a.claim_state
 FROM app_users a JOIN app_sessions s ON s.subject=a.subject JOIN identity_epoch i ON i.issuer=s.issuer AND i.epoch=s.identity_epoch
 WHERE s.token_hash=$1 AND a.subject=$2 AND s.expires_at>now() AND a.active`, u.sessionHash, u.Subject).
		Scan(&m.ID, &m.Subject, &m.Status, &m.AccountState, &m.ClaimLogin, &m.ClaimState)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, staffMember{}, nil, staffProblem{403, "BUSINESS_PERMISSION_DENIED", "账号状态已变化，请重新登录"}
	}
	if err != nil {
		return nil, staffMember{}, nil, err
	}
	fingerprint := requestHash("CLAIM", m.ID, phone, name)
	var prevHash, prevState string
	err = tx.QueryRow(ctx, `SELECT coalesce(request_hash,''),state FROM account_operations WHERE idempotency_key=$1`, key).Scan(&prevHash, &prevState)
	if err == nil {
		if prevHash != fingerprint {
			return nil, staffMember{}, nil, accountKeyReused
		}
		switch prevState {
		case "DONE":
			return nil, staffMember{}, &claimResult{Claimed: true, Login: login, Replayed: true}, nil
		case "STARTED":
			return nil, staffMember{}, nil, accountBusy
		case "REVIEW":
			return nil, staffMember{}, nil, accountReviewNeeded
		}
		return nil, staffMember{}, nil, accountOpFailed
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, staffMember{}, nil, err
	}
	if m.ClaimState != claimPending || m.AccountState != accountReady || m.Status != staffActive {
		return nil, staffMember{}, nil, claimNotPending
	}
	var unresolved string
	err = tx.QueryRow(ctx, `SELECT state FROM account_operations WHERE staff_id=$1 AND state IN ('STARTED','REVIEW')`, m.ID).Scan(&unresolved)
	if err == nil {
		if unresolved == "REVIEW" {
			return nil, staffMember{}, nil, accountReviewNeeded
		}
		return nil, staffMember{}, nil, accountBusy
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, staffMember{}, nil, err
	}
	var taken bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_users WHERE work_phone=$1 AND staff_id<>$2)`, phone, m.ID).Scan(&taken); err != nil {
		return nil, staffMember{}, nil, err
	}
	if taken {
		return nil, staffMember{}, nil, accountPhoneTaken
	}
	o := &accountOp{staffID: m.ID, operation: "CLAIM", actor: m.Subject, reason: "首次认领账号", lease: newUUID(), step: "OPENED",
		targetLogin: login, previousLogin: m.ClaimLogin}
	err = tx.QueryRow(ctx, `INSERT INTO account_operations(idempotency_key,staff_id,operation,actor_subject,lease_token,lease_expires_at,
 request_hash,reason,target_login,previous_login,claim_name) VALUES($1,$2,'CLAIM',$3,$4,now()+$5::interval,$6,$7,$8,$9,$10) RETURNING id::text`,
		key, m.ID, m.Subject, o.lease, leaseInterval(), fingerprint, o.reason, login, m.ClaimLogin, name).Scan(&o.id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "account_operations_one_login" {
				return nil, staffMember{}, nil, staffProblem{409, "ACCOUNT_PHONE_BUSY", "该手机号正在被另一项账号操作使用，请稍后重试"}
			}
			return nil, staffMember{}, nil, accountBusy
		}
		return nil, staffMember{}, nil, err
	}
	if err = a.inject("db:open"); err != nil {
		return nil, staffMember{}, nil, err
	}
	return o, m, nil, tx.Commit(ctx)
}

// claimAccount: Keycloak username temporary name → phone (verified), then the
// business record in one transaction. A refused database write puts the
// temporary name back.
func (a *App) claimAccount(ctx context.Context, pool *pgxpool.Pool, o *accountOp, m staffMember) error {
	ext, err := a.externalID(ctx, pool, m)
	if err != nil {
		return a.failBeforeProvider(ctx, pool, o, staffConflict)
	}
	o.kcID = ext
	if err = a.advance(ctx, pool, o, "KC_USERNAME"); err != nil {
		return stored(o, err, nil)
	}
	if err = a.accounts.update(ctx, ext, kcUser{Username: o.targetLogin}); err != nil {
		var p accountProblem
		if errors.As(err, &p) && p.status == 409 {
			return stored(o, a.commitOp(ctx, pool, o, "FAILED", "PHONE_TAKEN", "fail", nil), accountPhoneTaken)
		}
		u, gerr := a.accounts.getUser(ctx, ext)
		switch {
		case gerr != nil || u == nil:
			return a.review(ctx, pool, o, "USERNAME_OUTCOME_UNKNOWN")
		case u.Username == o.previousLogin:
			return stored(o, a.commitOp(ctx, pool, o, "FAILED", "PROVIDER_UNAVAILABLE", "fail", nil), accountUnavailable)
		case u.Username != o.targetLogin:
			return a.review(ctx, pool, o, "USERNAME_MISMATCH")
		}
	}
	if err = a.inject("crash:after-claim-username"); err != nil {
		return err
	}
	_ = a.accounts.logout(ctx, ext)
	return a.finishClaim(ctx, pool, o)
}

func (a *App) finishClaim(ctx context.Context, pool *pgxpool.Pool, o *accountOp) error {
	phone := "+86" + o.targetLogin
	err := a.commitOp(ctx, pool, o, "DONE", "", "final", func(tx pgx.Tx) error {
		// The name first: the definer function requires the claim still pending.
		var version int
		if err := tx.QueryRow(ctx, `SELECT claim_own_profile($1)`, o.id).Scan(&version); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "BB016" {
				return claimNotPending
			}
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE app_users SET work_phone=$2,claim_state='DONE',claim_login=NULL,staff_version=staff_version+1
 WHERE staff_id=$1 AND claim_state='PENDING' AND claim_login=$3`, o.staffID, phone, o.previousLogin)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return accountPhoneTaken
			}
			return err
		}
		if tag.RowsAffected() != 1 {
			return claimNotPending
		}
		if _, err = tx.Exec(ctx, `DELETE FROM app_sessions WHERE subject=(SELECT subject FROM app_users WHERE staff_id=$1)`, o.staffID); err != nil {
			return err
		}
		return auditOp(ctx, tx, o, "staff.account.claim", "临时登录名 "+o.previousLogin+" → "+maskedPhone(phone)+" · 本人首次认领")
	})
	var p staffProblem
	if errors.As(err, &p) {
		// The business record refused the phone: put the temporary name back.
		if a.accounts.update(ctx, o.kcID, kcUser{Username: o.previousLogin}) != nil {
			return a.review(ctx, pool, o, "USERNAME_NOT_RESTORED")
		}
		return stored(o, a.commitOp(ctx, pool, o, "COMPENSATED", p.code, "settle", nil), p)
	}
	return stored(o, err, nil)
}

// reconcileClaim: the recovery path for an interrupted claim.
func (a *App) reconcileClaim(ctx context.Context, pool *pgxpool.Pool, o *accountOp) error {
	u, err := a.accounts.getUser(ctx, o.kcID)
	switch {
	case err != nil:
		return a.review(ctx, pool, o, "PROVIDER_UNAVAILABLE")
	case u == nil:
		return a.review(ctx, pool, o, "LOGIN_MISSING")
	case u.Username == o.targetLogin:
		return a.finishClaim(ctx, pool, o)
	case u.Username == o.previousLogin:
		return a.settleUnchanged(ctx, pool, o, "FAILED", "NOT_APPLIED")
	}
	return a.review(ctx, pool, o, "USERNAME_MISMATCH")
}
