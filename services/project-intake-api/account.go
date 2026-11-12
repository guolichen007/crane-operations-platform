package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// accountProvider is what the Baobei account service needs from the identity
// provider. keycloakAdmin implements it against the dedicated realm; devAccounts
// implements it in memory for development and browser tests only.
type accountProvider interface {
	findByUsername(ctx context.Context, username string) (*kcUser, error)
	getUser(ctx context.Context, id string) (*kcUser, error)
	createDisabled(ctx context.Context, username, staffID string) (string, error)
	update(ctx context.Context, id string, u kcUser) error
	setTemporaryPassword(ctx context.Context, id, password string) error
	setExactRole(ctx context.Context, id, role string) error
	roles(ctx context.Context, id string) ([]string, error)
	logout(ctx context.Context, id string) error
	delete(ctx context.Context, id string) error
}

const (
	accountNone     = "NONE"
	accountCreating = "CREATING"
	accountReady    = "READY"
	accountDisabled = "DISABLED"
	accountFailed   = "FAILED"
	// accountReview (待核对): Keycloak and Baobei could not be proven consistent.
	// The member cannot sign in until an administrator's reconcile resolves it.
	accountReview = "REVIEW"
	// accountLease: how long a process may drive an operation without progress.
	// After that the recovery job takes it over and reconciles with Keycloak.
	accountLease = 2 * time.Minute
)

var (
	accountUnavailable  = staffProblem{503, "ACCOUNT_SERVICE_UNAVAILABLE", "账号服务暂时不可用，请稍后重试；已填内容会保留"}
	accountNotSupported = staffProblem{409, "ACCOUNT_SERVICE_NOT_ENABLED", "当前登录方式不支持在网页内开通账号"}
	accountBusy         = staffProblem{409, "ACCOUNT_OPERATION_IN_PROGRESS", "该成员有一项账号操作正在处理，系统会自动核对；请稍后刷新"}
	accountPhoneTaken   = staffProblem{409, "ACCOUNT_PHONE_TAKEN", "该手机号已被其他登录账号使用"}
	accountMainlandOnly = staffProblem{400, "ACCOUNT_PHONE_MAINLAND_ONLY", "登录账号目前只支持中国大陆 11 位手机号"}
	accountNoPhone      = staffProblem{409, "ACCOUNT_PHONE_REQUIRED", "请先为成员填写工作手机，再开通账号"}
	accountKeyInvalid   = staffProblem{400, "ACCOUNT_IDEMPOTENCY_KEY", "请求标识无效，请刷新页面后重试"}
	accountKeyReused    = staffProblem{409, "ACCOUNT_IDEMPOTENCY_MISMATCH", "该请求标识已用于另一项操作，请关闭弹窗后重新发起"}
	accountReviewNeeded = staffProblem{409, "ACCOUNT_REVIEW_REQUIRED", "账号状态待核对：登录已暂停。请点击“核对账号状态”，核对完成前不能进行其他账号操作"}
	accountOpFailed     = staffProblem{409, "ACCOUNT_OPERATION_FAILED", "上一次操作未完成，请重新发起"}
)

// accountPending: the identity provider may have acted, but the result could not
// be stored. Nothing is lost: the operation stays recorded and is reconciled with
// Keycloak automatically (or by "核对账号状态").
func accountPending(id string) staffProblem {
	short := id
	if len(short) > 8 {
		short = short[:8]
	}
	return staffProblem{503, "ACCOUNT_RESULT_PENDING", "账号操作结果尚未保存，系统会自动核对，请稍后刷新查看（操作编号 " + short + "）"}
}

// errLeaseLost: another process took this operation over (its claim expired).
var errLeaseLost = errors.New("account operation claim lost")

// crashError is a test-only fault: the process "dies" at this point, so nothing
// after it runs (no compensation, no result). Production never produces it.
type crashError struct{ point string }

func (e crashError) Error() string { return "simulated crash at " + e.point }

// inject runs the test fault hook (nil in production).
func (a *App) inject(point string) error {
	if a.fault == nil {
		return nil
	}
	return a.fault(point)
}

// loginName: the Keycloak username for a canonical mainland phone is its 11
// digits, which is exactly what employees type on the sign-in page.
func loginName(phone string) (string, error) {
	if !mainlandMobile.MatchString(phone) {
		return "", accountMainlandOnly
	}
	return phone[3:], nil
}

// temporaryPassword: 12 characters from an unambiguous alphabet with at least one
// upper, lower and digit. Shown once to the administrator, never stored.
func temporaryPassword() string {
	const upper, lower, digits = "ABCDEFGHJKLMNPQRSTUVWXYZ", "abcdefghijkmnpqrstuvwxyz", "23456789"
	all := upper + lower + digits
	pick := func(set string) byte {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		if err != nil {
			panic("random unavailable")
		}
		return set[n.Int64()]
	}
	for {
		b := make([]byte, 12)
		for i := range b {
			b[i] = pick(all)
		}
		s := string(b)
		if strings.ContainsAny(s, upper) && strings.ContainsAny(s, lower) && strings.ContainsAny(s, digits) {
			return s
		}
	}
}

type accountRequest struct {
	Reason string `json:"reason"`
	Phone  string `json:"phone,omitempty"` // change-phone only
}

type accountResult struct {
	Member     staffMember `json:"member"`
	Credential *credential `json:"credential,omitempty"`
	// Replayed: the same request was already completed; a password from that
	// request is not shown again (reset to get a new one).
	Replayed    bool   `json:"replayed,omitempty"`
	OperationID string `json:"operationId,omitempty"`
}

type credential struct {
	Login             string `json:"login"`
	TemporaryPassword string `json:"temporaryPassword"`
}

// accountOp is one claimed row of account_operations.
type accountOp struct {
	id, staffID, operation, actor, reason string
	lease                                 string
	targetLogin, previousLogin            string
	kcID                                  string
	createdUser                           bool
	step                                  string
}

func requestHash(op, staffID, phone, reason string) string {
	h := sha256.Sum256([]byte(op + "\x00" + staffID + "\x00" + phone + "\x00" + reason))
	return hex.EncodeToString(h[:])
}

// accountAction serves POST /api/v1/staff/{id}/account/{action}.
//
// Consistency contract: the operation row is committed before Keycloak is
// called, and every step is recorded before the Keycloak call it describes. The
// final business change, the audit row and the operation's DONE state commit in
// one transaction; only then is success (and a one-time password) returned. Any
// other outcome leaves the operation FAILED/COMPENSATED (nothing usable left),
// REVIEW (login paused, administrator reconciles) or still STARTED (result not
// stored; the recovery job reconciles it with Keycloak once its claim expires).
func (a *App) accountAction(w http.ResponseWriter, r *http.Request, actor User, staffID, action string) error {
	if a.accounts == nil {
		return accountNotSupported
	}
	if actor.Role != adminRole {
		return staffDenied
	}
	if !uuidPattern.MatchString(staffID) {
		return staffMissing
	}
	pool := a.staffPool()
	if pool == nil {
		return errors.New("staff database unavailable")
	}
	// The operation must not be abandoned halfway because the browser went away.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 25*time.Second)
	defer cancel()
	if action == "reconcile" {
		var in struct{}
		if !decode(w, r, &in) {
			return nil
		}
		return a.accountReconcileAction(ctx, w, pool, actor, r.Header.Get("X-Request-ID"), staffID)
	}
	key := r.Header.Get("Idempotency-Key")
	if !createKeyPattern.MatchString(key) {
		return accountKeyInvalid
	}
	op := map[string]string{"create": "CREATE", "reset-password": "RESET_PASSWORD", "change-phone": "CHANGE_PHONE",
		"disable": "DISABLE", "enable": "ENABLE", "sync-role": "SET_ROLE"}[action]
	if op == "" {
		return staffMissing
	}
	var in accountRequest
	if !decode(w, r, &in) {
		return nil
	}
	if strings.TrimSpace(in.Reason) == "" {
		in.Reason = map[string]string{"CREATE": "开通登录账号", "SET_ROLE": "同步登录角色", "ENABLE": "恢复登录账号"}[op]
	}
	if !reasonOK(in.Reason) {
		return staffReasonRequired
	}
	o, m, replay, err := a.openAccountOp(ctx, pool, actor, staffID, op, key, in)
	if err != nil {
		return err
	}
	if replay != nil {
		respond(w, 200, replay)
		return nil
	}
	var cred *credential
	switch op {
	case "CREATE":
		cred, err = a.accountCreate(ctx, pool, o, m)
	case "RESET_PASSWORD":
		cred, err = a.accountReset(ctx, pool, o, m)
	case "CHANGE_PHONE":
		err = a.accountChangePhone(ctx, pool, o, m)
	case "DISABLE":
		err = a.accountDisable(ctx, pool, o, m)
	case "ENABLE", "SET_ROLE":
		err = a.accountEnable(ctx, pool, o, m)
	}
	if err != nil {
		var crash crashError
		if errors.As(err, &crash) {
			return err
		}
		var p staffProblem
		if errors.As(err, &p) {
			return p
		}
		// Unexpected error (database, lost claim): the result is not known to be
		// stored; recovery reconciles it.
		return accountPending(o.id)
	}
	member, err := a.readMember(ctx, pool, actor, staffID)
	if err != nil {
		// The operation is committed; never withhold a one-time password because a
		// follow-up read failed.
		member = m
	}
	respond(w, 200, accountResult{Member: member, Credential: cred, OperationID: o.id})
	return nil
}

// readMember reads one member in the actor's scope (short read-only transaction).
func (a *App) readMember(ctx context.Context, pool *pgxpool.Pool, actor User, id string) (staffMember, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return staffMember{}, err
	}
	defer tx.Rollback(ctx)
	return readStaff(ctx, tx, actor, id)
}

func (a *App) openAccountOp(ctx context.Context, pool *pgxpool.Pool, actor User, staffID, op, key string, in accountRequest) (*accountOp, staffMember, *accountResult, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, staffMember{}, nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, identityLock); err != nil {
		return nil, staffMember{}, nil, err
	}
	at, err := adminSession(ctx, tx, actor)
	if err != nil {
		return nil, staffMember{}, nil, err
	}
	m, err := readStaff(ctx, tx, actor, staffID)
	if err != nil {
		return nil, staffMember{}, nil, err
	}
	phone := ""
	if op == "CHANGE_PHONE" {
		if phone, err = normalizedPhone(in.Phone); err != nil {
			return nil, staffMember{}, nil, err
		}
	}
	fingerprint := requestHash(op, staffID, phone, in.Reason)
	// Idempotent replay: the same key must mean the same request.
	var prevHash, prevState, prevDetail string
	err = tx.QueryRow(ctx, `SELECT coalesce(request_hash,''),state,detail FROM account_operations WHERE idempotency_key=$1`, key).Scan(&prevHash, &prevState, &prevDetail)
	if err == nil {
		if prevHash != fingerprint {
			return nil, staffMember{}, nil, accountKeyReused
		}
		switch prevState {
		case "DONE":
			return nil, staffMember{}, &accountResult{Member: m, Replayed: true}, nil
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
	var unresolved string
	err = tx.QueryRow(ctx, `SELECT state FROM account_operations WHERE staff_id=$1 AND state IN ('STARTED','REVIEW')`, staffID).Scan(&unresolved)
	if err == nil {
		if unresolved == "REVIEW" {
			return nil, staffMember{}, nil, accountReviewNeeded
		}
		return nil, staffMember{}, nil, accountBusy
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, staffMember{}, nil, err
	}
	if m.AccountState == accountReview {
		return nil, staffMember{}, nil, accountReviewNeeded
	}
	if !m.CanManage && !(op == "ENABLE" && m.Status == staffSuspended) || m.Status == staffArchived {
		return nil, staffMember{}, nil, staffDenied
	}
	// Advanced accounts (manager/director/admin) need a fresh password check for
	// identity actions; ordinary sales accounts do not.
	sensitive := m.Role != salesRole && op != "DISABLE"
	if err = needsApproval(actor, m.Role, m.Role, at, sensitive); err != nil {
		return nil, staffMember{}, nil, err
	}
	o := &accountOp{staffID: staffID, operation: op, actor: actor.Subject, reason: in.Reason, lease: newUUID(), step: "OPENED"}
	switch op {
	case "CREATE":
		var linked bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_links WHERE issuer=$1 AND subject=$2)`, a.sessionIssuer(), m.Subject).Scan(&linked); err != nil {
			return nil, staffMember{}, nil, err
		}
		// New member (pending:), or an existing member with shared-realm history and
		// no account in the dedicated realm yet (migration).
		migrating := a.linkedOnly && m.IdentityLinked && !linked && m.Status != staffSuspended
		if m.AccountState != accountNone && m.AccountState != accountFailed ||
			!migrating && (m.IdentityLinked || m.Status != staffPending && m.Status != staffFailed) {
			return nil, staffMember{}, nil, staffConflict
		}
		if m.Phone == "" {
			return nil, staffMember{}, nil, accountNoPhone
		}
		if o.targetLogin, err = loginName(m.Phone); err != nil {
			return nil, staffMember{}, nil, err
		}
	case "RESET_PASSWORD", "DISABLE":
		if !m.IdentityLinked || m.AccountState == accountNone {
			return nil, staffMember{}, nil, staffConflict
		}
	case "CHANGE_PHONE":
		if !m.IdentityLinked || m.AccountState == accountNone {
			return nil, staffMember{}, nil, staffConflict
		}
		if phone == m.Phone {
			return nil, staffMember{}, nil, staffProblem{400, "ACCOUNT_PHONE_UNCHANGED", "新手机号与当前手机号相同"}
		}
		if o.targetLogin, err = loginName(phone); err != nil {
			return nil, staffMember{}, nil, err
		}
		o.previousLogin, _ = loginName(m.Phone)
		var taken bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_users WHERE work_phone=$1 AND staff_id<>$2)`, phone, staffID).Scan(&taken); err != nil {
			return nil, staffMember{}, nil, err
		}
		if taken {
			return nil, staffMember{}, nil, staffProblem{409, "STAFF_PHONE_DUPLICATE", "该工作手机已被其他成员使用"}
		}
	case "ENABLE", "SET_ROLE":
		if !m.IdentityLinked {
			return nil, staffMember{}, nil, staffConflict
		}
	}
	err = tx.QueryRow(ctx, `INSERT INTO account_operations(idempotency_key,staff_id,operation,actor_subject,lease_token,lease_expires_at,
 request_hash,reason,target_login,previous_login) VALUES($1,$2,$3,$4,$5,now()+$6::interval,$7,$8,NULLIF($9,''),NULLIF($10,'')) RETURNING id::text`,
		key, staffID, op, actor.Subject, o.lease, leaseInterval(), fingerprint, in.Reason, o.targetLogin, o.previousLogin).Scan(&o.id)
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
	if op == "CREATE" {
		// The login gate refuses CREATING: nothing created from here on can sign in
		// until the final step commits READY.
		if _, err = tx.Exec(ctx, `UPDATE app_users SET account_state='CREATING' WHERE staff_id=$1`, staffID); err != nil {
			return nil, staffMember{}, nil, err
		}
	}
	if err = a.inject("db:open"); err != nil {
		return nil, staffMember{}, nil, err
	}
	return o, m, nil, tx.Commit(ctx)
}

func leaseInterval() string { return accountLease.String() }

// adminSession: the actor's own session must still be a valid administrator staff
// session under the current identity epoch (same check as staff routes).
func adminSession(ctx context.Context, tx pgx.Tx, actor User) (*time.Time, error) {
	var at *time.Time
	var role string
	err := tx.QueryRow(ctx, `SELECT u.role,g.reauthenticated_at FROM app_users u JOIN app_sessions s ON s.subject=u.subject
 JOIN staff_sessions g ON g.token_hash=s.token_hash AND g.subject=u.subject JOIN identity_epoch i ON i.issuer=s.issuer AND i.epoch=s.identity_epoch
 WHERE s.token_hash=$1 AND u.subject=$2 AND s.expires_at>now() AND u.active`, actor.sessionHash, actor.Subject).Scan(&role, &at)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && role != adminRole {
		return nil, staffDenied
	}
	return at, err
}

// advance records the step about to be attempted (and the Keycloak user id once
// known) and renews the claim. It fails if another process has taken over.
func (a *App) advance(ctx context.Context, pool *pgxpool.Pool, o *accountOp, step string) error {
	if err := a.inject("db:step:" + step); err != nil {
		return err
	}
	tag, err := pool.Exec(ctx, `UPDATE account_operations SET step=$3,keycloak_user_id=NULLIF($4,''),created_user=$5,
 lease_expires_at=now()+$6::interval,updated_at=now() WHERE id=$1 AND lease_token=$2 AND state='STARTED'`,
		o.id, o.lease, step, o.kcID, o.createdUser, leaseInterval())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errLeaseLost
	}
	o.step = step
	return nil
}

// commitOp runs fn and moves the operation to state in one transaction. A REVIEW
// operation stays unresolved (it keeps the member's single open slot).
func (a *App) commitOp(ctx context.Context, pool *pgxpool.Pool, o *accountOp, state, detail, point string, fn func(pgx.Tx) error) error {
	return writeMember(ctx, pool, func(tx pgx.Tx) error {
		if fn != nil {
			if err := fn(tx); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE account_operations SET state=$3,detail=$4,step=CASE WHEN $3='REVIEW' THEN step ELSE 'FINAL' END,
 finished_at=CASE WHEN $3='REVIEW' THEN NULL ELSE now() END,lease_token=NULL,lease_expires_at=NULL,updated_at=now()
 WHERE id=$1 AND lease_token=$2 AND state='STARTED'`, o.id, o.lease, state, detail)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errLeaseLost
		}
		return a.inject("db:" + point)
	})
}

// writeMember runs fn in a short transaction under the identity lock.
func writeMember(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, identityLock); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// stored maps a failed result write: the provider may have acted, recovery
// reconciles (the operation is still STARTED and its claim will expire).
func stored(o *accountOp, err error, onSuccess error) error {
	if err == nil {
		return onSuccess
	}
	var crash crashError
	if errors.As(err, &crash) {
		return err
	}
	return accountPending(o.id)
}

// review parks the operation as REVIEW: the member cannot sign in and every
// Baobei session ends until an administrator reconciles.
func (a *App) review(ctx context.Context, pool *pgxpool.Pool, o *accountOp, detail string) error {
	err := a.commitOp(ctx, pool, o, "REVIEW", detail, "review", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE app_users SET account_state='REVIEW' WHERE staff_id=$1`, o.staffID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app_sessions WHERE subject=(SELECT subject FROM app_users WHERE staff_id=$1)`, o.staffID); err != nil {
			return err
		}
		return auditOp(ctx, tx, o, "staff.account.review", "账号状态待核对（"+detail+"）")
	})
	return stored(o, err, accountReviewNeeded)
}

func auditOp(ctx context.Context, tx pgx.Tx, o *accountOp, action, reason string) error {
	var version int
	if err := tx.QueryRow(ctx, `SELECT staff_version FROM app_users WHERE staff_id=$1`, o.staffID).Scan(&version); err != nil {
		return err
	}
	return staffAudit(ctx, tx, o.staffID, o.actor, action, reason, version)
}

func ownedBy(u *kcUser, staffID string) bool {
	return u != nil && len(u.Attributes["baobei_staff_id"]) == 1 && u.Attributes["baobei_staff_id"][0] == staffID
}

func enabled(u *kcUser) bool { return u != nil && u.Enabled != nil && *u.Enabled }

// accountCreate: create disabled user (or resume this member's own disabled user)
// → temporary password → exact role → bind business identity → verify → enable →
// READY. Until the final commit the member is CREATING, which the login gate
// refuses, so no failure can leave a usable account.
func (a *App) accountCreate(ctx context.Context, pool *pgxpool.Pool, o *accountOp, m staffMember) (*credential, error) {
	existing, err := a.accounts.findByUsername(ctx, o.targetLogin)
	if err != nil {
		return nil, a.settleCreate(ctx, pool, o, "FAILED", "PROVIDER_UNAVAILABLE", accountUnavailable)
	}
	switch {
	case existing == nil:
		if err = a.advance(ctx, pool, o, "KC_CREATE"); err != nil {
			return nil, stored(o, err, nil)
		}
		id, err := a.accounts.createDisabled(ctx, o.targetLogin, m.ID)
		if err != nil {
			var p accountProblem
			if errors.As(err, &p) && p.status == 409 {
				return nil, a.settleCreate(ctx, pool, o, "FAILED", "PHONE_TAKEN", accountPhoneTaken)
			}
			// Unknown outcome (for example a timeout after Keycloak created the user):
			// look again; only a user carrying this member's staff id is ours.
			again, lerr := a.accounts.findByUsername(ctx, o.targetLogin)
			switch {
			case lerr != nil:
				return nil, a.review(ctx, pool, o, "CREATE_OUTCOME_UNKNOWN")
			case again == nil:
				return nil, a.settleCreate(ctx, pool, o, "FAILED", "PROVIDER_UNAVAILABLE", accountUnavailable)
			case !ownedBy(again, m.ID):
				return nil, a.settleCreate(ctx, pool, o, "FAILED", "PHONE_TAKEN", accountPhoneTaken)
			}
			id = again.ID
		}
		o.kcID, o.createdUser = id, true
	case ownedBy(existing, m.ID) && !enabled(existing):
		// This member's own disabled user (an earlier attempt, or the migration
		// import): resume it. It was not created by this operation, so compensation
		// only ever disables it.
		o.kcID = existing.ID
	default:
		return nil, a.settleCreate(ctx, pool, o, "FAILED", "PHONE_TAKEN", accountPhoneTaken)
	}
	if err = a.advance(ctx, pool, o, "CREDENTIAL"); err != nil {
		return nil, stored(o, err, nil)
	}
	if err = a.inject("crash:after-create"); err != nil {
		return nil, err
	}
	password := temporaryPassword()
	if err = a.accounts.setTemporaryPassword(ctx, o.kcID, password); err != nil {
		return nil, a.compensateCreate(ctx, pool, o, accountUnavailable)
	}
	if err = a.advance(ctx, pool, o, "ROLE"); err != nil {
		return nil, stored(o, err, nil)
	}
	if err = a.accounts.setExactRole(ctx, o.kcID, m.Role); err != nil {
		return nil, a.compensateCreate(ctx, pool, o, accountUnavailable)
	}
	// Bind the business identity. A new member's placeholder subject becomes the
	// Keycloak UUID; an existing member keeps its subject and gains a link.
	err = writeMember(ctx, pool, func(tx pgx.Tx) error {
		if err := a.inject("db:bind"); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE account_operations SET step='BOUND',keycloak_user_id=$3,created_user=$4,lease_expires_at=now()+$5::interval,updated_at=now()
 WHERE id=$1 AND lease_token=$2 AND state='STARTED'`, o.id, o.lease, o.kcID, o.createdUser, leaseInterval())
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errLeaseLost
		}
		if strings.HasPrefix(m.Subject, "pending:") {
			var taken bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_users WHERE subject=$1 AND staff_id<>$2)`, o.kcID, m.ID).Scan(&taken); err != nil {
				return err
			}
			if taken {
				return accountPhoneTaken
			}
			tag, err = tx.Exec(ctx, `UPDATE app_users SET subject=$2,staff_status='PENDING_ACTIVATION',active=false,staff_version=staff_version+1
 WHERE staff_id=$1 AND subject LIKE 'pending:%' AND account_state='CREATING'`, m.ID, o.kcID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return staffConflict
			}
			if _, err = tx.Exec(ctx, `UPDATE staff_outbox SET status='SUPERSEDED',processed_at=now() WHERE staff_id=$1 AND status='PENDING'`, m.ID); err != nil {
				return err
			}
			// Sign-in under the dedicated realm resolves (issuer, sub) through this link.
			_, err = tx.Exec(ctx, `INSERT INTO identity_links(issuer,external_subject,subject) VALUES($1,$2,$2) ON CONFLICT (issuer,external_subject) DO NOTHING`, a.sessionIssuer(), o.kcID)
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO identity_links(issuer,external_subject,subject) VALUES($1,$2,$3)`, a.sessionIssuer(), o.kcID, m.Subject); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE app_users SET staff_status=CASE WHEN staff_status='ACTIVE' THEN 'PENDING_ACTIVATION' ELSE staff_status END,
 active=false,staff_version=staff_version+1 WHERE staff_id=$1 AND account_state='CREATING'`, m.ID)
		return err
	})
	if err != nil {
		var p staffProblem
		if errors.As(err, &p) {
			return nil, a.compensateCreate(ctx, pool, o, p)
		}
		return nil, stored(o, err, nil)
	}
	o.step = "BOUND"
	u, err := a.accounts.getUser(ctx, o.kcID)
	roles, rerr := a.accounts.roles(ctx, o.kcID)
	if err != nil || rerr != nil || u == nil || u.Username != o.targetLogin || !ownedBy(u, m.ID) || !exactClientRole(baobeiRoles(roles), m.Role) {
		return nil, a.compensateCreate(ctx, pool, o, accountUnavailable)
	}
	if err = a.advance(ctx, pool, o, "ENABLE"); err != nil {
		return nil, stored(o, err, nil)
	}
	if err = a.accounts.update(ctx, o.kcID, kcUser{Enabled: boolp(true)}); err != nil {
		return nil, a.compensateCreate(ctx, pool, o, accountUnavailable)
	}
	if err = a.inject("crash:after-enable"); err != nil {
		return nil, err
	}
	err = a.commitOp(ctx, pool, o, "DONE", "", "final", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE app_users SET account_state='READY',account_must_change_password=true WHERE staff_id=$1 AND account_state='CREATING'`, m.ID); err != nil {
			return err
		}
		label := "登录账号 "
		if !strings.HasPrefix(m.Subject, "pending:") {
			label = "迁移登录账号 "
		}
		return auditOp(ctx, tx, o, "staff.account.create", label+maskedPhone(m.Phone)+" · "+o.reason)
	})
	if err != nil {
		// Not stored: the password is not shown; recovery disables this login and
		// marks the attempt failed, and the administrator opens it again.
		return nil, stored(o, err, nil)
	}
	return &credential{Login: o.targetLogin, TemporaryPassword: password}, nil
}

// compensateCreate undoes a failed creation on the Keycloak side (delete a user
// this operation created, otherwise disable it), then records the failure. If
// Keycloak cannot confirm the undo, the member is parked as REVIEW.
func (a *App) compensateCreate(ctx context.Context, pool *pgxpool.Pool, o *accountOp, cause error) error {
	if o.kcID != "" {
		var err error
		if o.createdUser {
			err = a.accounts.delete(ctx, o.kcID)
		} else {
			err = a.accounts.update(ctx, o.kcID, kcUser{Enabled: boolp(false)})
		}
		if err != nil {
			return a.review(ctx, pool, o, "COMPENSATION_FAILED")
		}
	}
	return a.settleCreate(ctx, pool, o, "COMPENSATED", accountErrorDetail(cause), cause)
}

// settleCreate records a creation that left nothing usable in Keycloak: the
// binding of this attempt is removed and the member shows "开通失败" with a retry.
func (a *App) settleCreate(ctx context.Context, pool *pgxpool.Pool, o *accountOp, state, detail string, cause error) error {
	err := a.commitOp(ctx, pool, o, state, detail, "settle", func(tx pgx.Tx) error {
		if o.kcID != "" {
			if _, err := tx.Exec(ctx, `DELETE FROM identity_links WHERE issuer=$1 AND external_subject=$2`, a.sessionIssuer(), o.kcID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE app_users SET subject=CASE WHEN subject=$2 THEN 'pending:'||staff_id::text ELSE subject END,
 staff_status=CASE WHEN subject=$2 OR subject LIKE 'pending:%' THEN 'PROVISION_FAILED' ELSE staff_status END,
 active=CASE WHEN subject=$2 OR subject LIKE 'pending:%' THEN false ELSE active END,
 account_state='FAILED',account_must_change_password=false WHERE staff_id=$1`, o.staffID, o.kcID); err != nil {
			return err
		}
		return auditOp(ctx, tx, o, "staff.account.failed", "开通登录账号未完成（"+detail+"）")
	})
	return stored(o, err, cause)
}

func accountErrorDetail(err error) string {
	var p staffProblem
	if errors.As(err, &p) {
		return p.code
	}
	return "PROVIDER_ERROR"
}

// externalID: the member's Keycloak user id in the current realm (identity link),
// falling back to the business subject itself (shared realm, or created here).
func (a *App) externalID(ctx context.Context, pool *pgxpool.Pool, m staffMember) (string, error) {
	var id string
	err := pool.QueryRow(ctx, `SELECT external_subject FROM identity_links WHERE issuer=$1 AND subject=$2`, a.sessionIssuer(), m.Subject).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		if a.linkedOnly {
			return "", staffConflict
		}
		return m.Subject, nil
	}
	return id, err
}

func baobeiRoles(all []string) []string {
	out := make([]string, 0, len(all))
	for _, r := range all {
		if strings.HasPrefix(r, "baobei_") {
			out = append(out, r)
		}
	}
	return out
}

// failBeforeProvider: nothing was sent to Keycloak; the operation simply failed.
func (a *App) failBeforeProvider(ctx context.Context, pool *pgxpool.Pool, o *accountOp, cause error) error {
	return stored(o, a.commitOp(ctx, pool, o, "FAILED", accountErrorDetail(cause), "fail", nil), cause)
}

// accountReset: new temporary password (old one cannot be read, only replaced);
// every Keycloak and Baobei session of the member ends. A reset whose result is
// lost is never repeated: nobody knows that password, the member's sessions are
// ended, and the administrator simply resets again.
func (a *App) accountReset(ctx context.Context, pool *pgxpool.Pool, o *accountOp, m staffMember) (*credential, error) {
	login, err := loginName(m.Phone)
	if err != nil {
		return nil, a.failBeforeProvider(ctx, pool, o, err)
	}
	ext, err := a.externalID(ctx, pool, m)
	if err != nil {
		return nil, a.failBeforeProvider(ctx, pool, o, staffConflict)
	}
	o.kcID = ext
	if err = a.advance(ctx, pool, o, "KC_PASSWORD"); err != nil {
		return nil, stored(o, err, nil)
	}
	password := temporaryPassword()
	if err = a.accounts.setTemporaryPassword(ctx, ext, password); err != nil {
		var p accountProblem
		if errors.As(err, &p) {
			// Refused by Keycloak: nothing changed.
			return nil, stored(o, a.commitOp(ctx, pool, o, "FAILED", "PROVIDER_REFUSED", "fail", nil), accountUnavailable)
		}
		return nil, a.settleResetLost(ctx, pool, o, m, accountUnavailable)
	}
	if err = a.inject("crash:after-password"); err != nil {
		return nil, err
	}
	_ = a.accounts.logout(ctx, ext) // Baobei sessions end below; every login asks for the password
	err = a.commitOp(ctx, pool, o, "DONE", "", "final", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM app_sessions WHERE subject=$1`, m.Subject); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app_users SET account_must_change_password=true WHERE staff_id=$1`, m.ID); err != nil {
			return err
		}
		return auditOp(ctx, tx, o, "staff.account.reset-password", o.reason)
	})
	if err != nil {
		return nil, stored(o, err, nil)
	}
	return &credential{Login: login, TemporaryPassword: password}, nil
}

// settleResetLost: the password may or may not have changed, and its value is
// gone. End the member's sessions, require a new password and record it; the
// administrator resets again (a new request, never a replay).
func (a *App) settleResetLost(ctx context.Context, pool *pgxpool.Pool, o *accountOp, m staffMember, cause error) error {
	if o.kcID != "" {
		_ = a.accounts.logout(ctx, o.kcID)
	}
	err := a.commitOp(ctx, pool, o, "FAILED", "RESULT_LOST", "settle", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM app_sessions WHERE subject=$1`, m.Subject); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app_users SET account_must_change_password=true WHERE staff_id=$1`, m.ID); err != nil {
			return err
		}
		return auditOp(ctx, tx, o, "staff.account.failed", "重置密码结果未送达，请重新重置")
	})
	return stored(o, err, cause)
}

// accountChangePhone: the login name follows the work phone; the UUID stays.
// Keycloak is changed first and verified; the database follows in the final
// transaction. If the database refuses, the Keycloak username is put back.
func (a *App) accountChangePhone(ctx context.Context, pool *pgxpool.Pool, o *accountOp, m staffMember) error {
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
	if err = a.inject("crash:after-username"); err != nil {
		return err
	}
	_ = a.accounts.logout(ctx, ext)
	return a.finishChangePhone(ctx, pool, o)
}

func (a *App) finishChangePhone(ctx context.Context, pool *pgxpool.Pool, o *accountOp) error {
	newPhone, oldPhone := "+86"+o.targetLogin, ""
	if o.previousLogin != "" {
		oldPhone = "+86" + o.previousLogin
	}
	err := a.commitOp(ctx, pool, o, "DONE", "", "final", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE app_users SET work_phone=$2,staff_version=staff_version+1 WHERE staff_id=$1 AND (work_phone=$3 OR work_phone=$2)`, o.staffID, newPhone, oldPhone)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return staffProblem{409, "STAFF_PHONE_DUPLICATE", "该工作手机已被其他成员使用"}
			}
			return err
		}
		if tag.RowsAffected() != 1 {
			return staffConflict
		}
		if _, err = tx.Exec(ctx, `DELETE FROM app_sessions WHERE subject=(SELECT subject FROM app_users WHERE staff_id=$1)`, o.staffID); err != nil {
			return err
		}
		return auditOp(ctx, tx, o, "staff.account.change-phone", maskedPhone(oldPhone)+" → "+maskedPhone(newPhone)+" · "+o.reason)
	})
	var p staffProblem
	if errors.As(err, &p) {
		// The business record refused the new phone: put the login back.
		if o.previousLogin == "" || a.accounts.update(ctx, o.kcID, kcUser{Username: o.previousLogin}) != nil {
			return a.review(ctx, pool, o, "USERNAME_NOT_RESTORED")
		}
		return stored(o, a.commitOp(ctx, pool, o, "COMPENSATED", p.code, "settle", nil), p)
	}
	return stored(o, err, nil)
}

// accountDisable: the business account is suspended first (fail closed: Baobei
// refuses the member immediately), then Keycloak disables the login.
func (a *App) accountDisable(ctx context.Context, pool *pgxpool.Pool, o *accountOp, m staffMember) error {
	ext, err := a.externalID(ctx, pool, m)
	if err != nil {
		return a.failBeforeProvider(ctx, pool, o, staffConflict)
	}
	o.kcID = ext
	err = writeMember(ctx, pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE account_operations SET step='KC_DISABLE',keycloak_user_id=$3,lease_expires_at=now()+$4::interval,updated_at=now()
 WHERE id=$1 AND lease_token=$2 AND state='STARTED'`, o.id, o.lease, ext, leaseInterval())
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errLeaseLost
		}
		return suspendMember(ctx, tx, o.staffID)
	})
	if err != nil {
		return stored(o, err, nil)
	}
	o.step = "KC_DISABLE"
	if err = a.accounts.update(ctx, ext, kcUser{Enabled: boolp(false)}); err != nil {
		if u, gerr := a.accounts.getUser(ctx, ext); gerr != nil || u == nil || enabled(u) {
			// Baobei already refuses the member; Keycloak could not confirm.
			return a.review(ctx, pool, o, "DISABLE_UNCONFIRMED")
		}
	}
	if err = a.inject("crash:after-disable"); err != nil {
		return err
	}
	_ = a.accounts.logout(ctx, ext)
	return a.finishDisable(ctx, pool, o)
}

func suspendMember(ctx context.Context, tx pgx.Tx, staffID string) error {
	var subject, status string
	if err := tx.QueryRow(ctx, `SELECT subject,staff_status FROM app_users WHERE staff_id=$1`, staffID).Scan(&subject, &status); err != nil {
		return err
	}
	if status != staffSuspended {
		if _, err := tx.Exec(ctx, `UPDATE app_users SET staff_status='SUSPENDED',active=false,staff_version=staff_version+1 WHERE staff_id=$1`, staffID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO staff_handoffs(staff_id,project_id) SELECT $1,id FROM intake_projects WHERE owner_subject=$2 AND `+openProjects+` ON CONFLICT DO NOTHING`, staffID, subject); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `DELETE FROM app_sessions WHERE subject=$1`, subject)
	return err
}

func (a *App) finishDisable(ctx context.Context, pool *pgxpool.Pool, o *accountOp) error {
	return stored(o, a.commitOp(ctx, pool, o, "DONE", "", "final", func(tx pgx.Tx) error {
		if err := suspendMember(ctx, tx, o.staffID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app_users SET account_state='DISABLED' WHERE staff_id=$1`, o.staffID); err != nil {
			return err
		}
		return auditOp(ctx, tx, o, "staff.account.disable", o.reason)
	}), nil)
}

// accountEnable: restore a suspended member, or apply a changed role. Keycloak
// gets exactly the current Baobei role and is enabled; if that cannot be
// confirmed the login is disabled again (no unintended privilege) and the member
// stays suspended (restore) or is parked for review (role sync).
func (a *App) accountEnable(ctx context.Context, pool *pgxpool.Pool, o *accountOp, m staffMember) error {
	ext, err := a.externalID(ctx, pool, m)
	if err != nil {
		return a.failBeforeProvider(ctx, pool, o, staffConflict)
	}
	o.kcID = ext
	if err = a.advance(ctx, pool, o, "ROLE"); err != nil {
		return stored(o, err, nil)
	}
	ok := a.accounts.setExactRole(ctx, ext, m.Role) == nil
	if ok {
		roles, rerr := a.accounts.roles(ctx, ext)
		ok = rerr == nil && exactClientRole(baobeiRoles(roles), m.Role)
	}
	if ok {
		if err = a.advance(ctx, pool, o, "ENABLE"); err != nil {
			return stored(o, err, nil)
		}
		ok = a.accounts.update(ctx, ext, kcUser{Enabled: boolp(true)}) == nil
	}
	if !ok {
		return a.abandonEnable(ctx, pool, o, accountUnavailable)
	}
	if err = a.inject("crash:after-enable"); err != nil {
		return err
	}
	return a.finishEnable(ctx, pool, o)
}

func (a *App) abandonEnable(ctx context.Context, pool *pgxpool.Pool, o *accountOp, cause error) error {
	if a.accounts.update(ctx, o.kcID, kcUser{Enabled: boolp(false)}) != nil {
		return a.review(ctx, pool, o, "ENABLE_UNCONFIRMED")
	}
	if o.operation == "SET_ROLE" {
		// The member's business record says READY but the login is now disabled.
		return a.review(ctx, pool, o, "ROLE_UNCONFIRMED")
	}
	return stored(o, a.commitOp(ctx, pool, o, "COMPENSATED", accountErrorDetail(cause), "settle", nil), cause)
}

func (a *App) finishEnable(ctx context.Context, pool *pgxpool.Pool, o *accountOp) error {
	return stored(o, a.commitOp(ctx, pool, o, "DONE", "", "final", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE app_users SET staff_status='PENDING_ACTIVATION',active=false,staff_version=staff_version+1
 WHERE staff_id=$1 AND staff_status IN ('SUSPENDED','PENDING_PROVISION','PROVISION_FAILED')`, o.staffID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app_users SET account_state='READY' WHERE staff_id=$1`, o.staffID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE staff_outbox SET status='SUPERSEDED',processed_at=now() WHERE staff_id=$1 AND status='PENDING'`, o.staffID); err != nil {
			return err
		}
		action := map[string]string{"ENABLE": "staff.account.enable", "SET_ROLE": "staff.account.sync-role"}[o.operation]
		return auditOp(ctx, tx, o, action, o.reason)
	}), nil)
}

// accountOpView: the member's latest unfinished or failed account operation, for
// the page ("处理中", "待核对", "开通失败" with the reason and a retry).
type accountOpView struct {
	ID        string    `json:"id"`
	Operation string    `json:"operation"`
	State     string    `json:"state"`
	Step      string    `json:"step"`
	Detail    string    `json:"detail"`
	At        time.Time `json:"at"`
}

func parseAccountOp(raw []byte) *accountOpView {
	if len(raw) == 0 {
		return nil
	}
	var v accountOpView
	if json.Unmarshal(raw, &v) != nil || v.State == "DONE" {
		return nil
	}
	return &v
}
