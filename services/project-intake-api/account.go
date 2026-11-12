package main

import (
	"context"
	"crypto/rand"
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
)

var (
	accountUnavailable  = staffProblem{503, "ACCOUNT_SERVICE_UNAVAILABLE", "账号服务暂时不可用，请稍后重试；已填内容会保留"}
	accountNotSupported = staffProblem{409, "ACCOUNT_SERVICE_NOT_ENABLED", "当前登录方式不支持在网页内开通账号"}
	accountBusy         = staffProblem{409, "ACCOUNT_OPERATION_IN_PROGRESS", "该成员有一项账号操作正在进行，请稍后刷新"}
	accountPhoneTaken   = staffProblem{409, "ACCOUNT_PHONE_TAKEN", "该手机号已被其他登录账号使用"}
	accountMainlandOnly = staffProblem{400, "ACCOUNT_PHONE_MAINLAND_ONLY", "登录账号目前只支持中国大陆 11 位手机号"}
	accountNoPhone      = staffProblem{409, "ACCOUNT_PHONE_REQUIRED", "请先为成员填写工作手机，再开通账号"}
	accountKeyInvalid   = staffProblem{400, "ACCOUNT_IDEMPOTENCY_KEY", "请求标识无效，请刷新页面后重试"}
)

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
	Replayed bool `json:"replayed,omitempty"`
}

type credential struct {
	Login             string `json:"login"`
	TemporaryPassword string `json:"temporaryPassword"`
}

// accountOp is one row of account_operations held open while Keycloak is called.
type accountOp struct {
	id, staffID, operation, actor string
	actorAt                       *time.Time
}

// accountAction serves POST /api/v1/staff/{id}/account/{action}. Keycloak calls
// happen outside database transactions; the open operation row (one per member,
// unique index) serializes them, and the idempotency key makes retries safe.
func (a *App) accountAction(w http.ResponseWriter, r *http.Request, actor User, staffID, action string) error {
	if a.accounts == nil {
		return accountNotSupported
	}
	if actor.Role != adminRole {
		return staffDenied
	}
	key := r.Header.Get("Idempotency-Key")
	if !createKeyPattern.MatchString(key) {
		return accountKeyInvalid
	}
	op := map[string]string{"create": "CREATE", "reset-password": "RESET_PASSWORD", "change-phone": "CHANGE_PHONE",
		"disable": "DISABLE", "enable": "ENABLE", "sync-role": "SET_ROLE"}[action]
	if op == "" || !uuidPattern.MatchString(staffID) {
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
	ctx := r.Context()
	pool := a.staffPool()
	if pool == nil {
		return errors.New("staff database unavailable")
	}
	// Step 1: authorize, check state, open the operation (or replay).
	m, at, replay, err := a.openAccountOp(ctx, pool, actor, staffID, op, key)
	if err != nil {
		return err
	}
	if replay != nil {
		respond(w, 200, replay)
		return nil
	}
	o := accountOp{id: "", staffID: staffID, operation: op, actor: actor.Subject, actorAt: at}
	_ = pool.QueryRow(ctx, `SELECT id::text FROM account_operations WHERE idempotency_key=$1`, key).Scan(&o.id)
	var cred *credential
	switch op {
	case "CREATE":
		cred, err = a.accountCreate(ctx, pool, o, m, in.Reason)
	case "RESET_PASSWORD":
		cred, err = a.accountReset(ctx, pool, o, m, in.Reason)
	case "CHANGE_PHONE":
		err = a.accountChangePhone(ctx, pool, o, m, in)
	case "DISABLE":
		err = a.accountDisable(ctx, pool, o, m, in.Reason)
	case "ENABLE", "SET_ROLE":
		err = a.accountEnable(ctx, pool, o, m, in.Reason)
	}
	if err != nil {
		a.finishAccountOp(context.WithoutCancel(ctx), pool, o, "FAILED", accountErrorDetail(err))
		return err
	}
	a.finishAccountOp(ctx, pool, o, "DONE", "")
	member, err := a.readMember(ctx, pool, actor, staffID)
	if err != nil {
		return err
	}
	respond(w, 200, accountResult{Member: member, Credential: cred})
	return nil
}

func accountErrorDetail(err error) string {
	var p staffProblem
	if errors.As(err, &p) {
		return p.code
	}
	return "PROVIDER_ERROR"
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

func (a *App) openAccountOp(ctx context.Context, pool *pgxpool.Pool, actor User, staffID, op, key string) (staffMember, *time.Time, *accountResult, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return staffMember{}, nil, nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, identityLock); err != nil {
		return staffMember{}, nil, nil, err
	}
	// The actor's own session must still be a valid staff session (same check as staff routes).
	var at *time.Time
	var role string
	err = tx.QueryRow(ctx, `SELECT u.role,g.reauthenticated_at FROM app_users u JOIN app_sessions s ON s.subject=u.subject
 JOIN staff_sessions g ON g.token_hash=s.token_hash AND g.subject=u.subject JOIN identity_epoch i ON i.issuer=s.issuer AND i.epoch=s.identity_epoch
 WHERE s.token_hash=$1 AND u.subject=$2 AND s.expires_at>now() AND u.active`, actor.sessionHash, actor.Subject).Scan(&role, &at)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && role != adminRole {
		return staffMember{}, nil, nil, staffDenied
	}
	if err != nil {
		return staffMember{}, nil, nil, err
	}
	// Idempotent replay of a request that already ran.
	var prevStaff, prevOp, prevState string
	err = tx.QueryRow(ctx, `SELECT staff_id::text,operation,state FROM account_operations WHERE idempotency_key=$1`, key).Scan(&prevStaff, &prevOp, &prevState)
	if err == nil {
		if prevStaff != staffID || prevOp != op {
			return staffMember{}, nil, nil, accountKeyInvalid
		}
		if prevState == "STARTED" {
			return staffMember{}, nil, nil, accountBusy
		}
		m, err := readStaff(ctx, tx, actor, staffID)
		if err != nil {
			return staffMember{}, nil, nil, err
		}
		if prevState != "DONE" {
			return staffMember{}, nil, nil, staffProblem{409, "ACCOUNT_OPERATION_FAILED", "上一次操作未完成，请重新发起"}
		}
		return staffMember{}, nil, &accountResult{Member: m, Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return staffMember{}, nil, nil, err
	}
	m, err := readStaff(ctx, tx, actor, staffID)
	if err != nil {
		return staffMember{}, nil, nil, err
	}
	if !m.CanManage && !(op == "ENABLE" && m.Status == staffSuspended) {
		return staffMember{}, nil, nil, staffDenied
	}
	if m.Status == staffArchived {
		return staffMember{}, nil, nil, staffDenied
	}
	// Advanced accounts (manager/director/admin) need a fresh password check for
	// identity actions; ordinary sales accounts do not.
	sensitive := m.Role != salesRole && op != "DISABLE"
	if err = needsApproval(actor, m.Role, m.Role, at, sensitive); err != nil {
		return staffMember{}, nil, nil, err
	}
	var accountState string
	if err = tx.QueryRow(ctx, `SELECT account_state FROM app_users WHERE staff_id=$1`, staffID).Scan(&accountState); err != nil {
		return staffMember{}, nil, nil, err
	}
	switch op {
	case "CREATE":
		// New member (subject pending:), or an existing member who has history under
		// the shared realm but no account in the dedicated realm yet (migration).
		var linked bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_links WHERE issuer=$1 AND subject=$2)`, a.sessionIssuer(), m.Subject).Scan(&linked); err != nil {
			return staffMember{}, nil, nil, err
		}
		migrating := a.linkedOnly && m.IdentityLinked && !linked && m.Status != staffArchived
		if !migrating && (m.IdentityLinked || m.Status != staffPending && m.Status != staffFailed) {
			return staffMember{}, nil, nil, staffConflict
		}
		if m.Phone == "" {
			return staffMember{}, nil, nil, accountNoPhone
		}
		if _, err := loginName(m.Phone); err != nil {
			return staffMember{}, nil, nil, err
		}
	case "RESET_PASSWORD", "CHANGE_PHONE", "DISABLE":
		if !m.IdentityLinked || accountState == accountNone {
			return staffMember{}, nil, nil, staffConflict
		}
	case "ENABLE", "SET_ROLE":
		if !m.IdentityLinked {
			return staffMember{}, nil, nil, staffConflict
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO account_operations(idempotency_key,staff_id,operation,actor_subject) VALUES($1,$2,$3,$4)`, key, staffID, op, actor.Subject); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return staffMember{}, nil, nil, accountBusy
		}
		return staffMember{}, nil, nil, err
	}
	if op == "CREATE" {
		if _, err = tx.Exec(ctx, `UPDATE app_users SET account_state='CREATING' WHERE staff_id=$1`, staffID); err != nil {
			return staffMember{}, nil, nil, err
		}
	}
	return m, at, nil, tx.Commit(ctx)
}

func (a *App) finishAccountOp(ctx context.Context, pool *pgxpool.Pool, o accountOp, state, detail string) {
	_, _ = pool.Exec(ctx, `UPDATE account_operations SET state=$2,detail=$3,finished_at=now() WHERE id=$1 AND state='STARTED'`, o.id, state, detail)
	if state != "DONE" && o.operation == "CREATE" {
		// Never left half-open: an unbound member that failed to get an account is
		// "开通失败" and can be retried from the page.
		_, _ = pool.Exec(ctx, `UPDATE app_users SET account_state='FAILED',
 staff_status=CASE WHEN subject LIKE 'pending:%' THEN 'PROVISION_FAILED' ELSE staff_status END,
 active=CASE WHEN subject LIKE 'pending:%' THEN false ELSE active END
 WHERE staff_id=$1 AND account_state='CREATING'`, o.staffID)
	}
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

// accountCreate: check phone and permission (done) → create disabled user →
// temporary password → exact role → bind business identity → verify → enable.
// Until the final enable the Keycloak user cannot sign in, so any failure leaves
// no usable account; a user created by this attempt is deleted again.
func (a *App) accountCreate(ctx context.Context, pool *pgxpool.Pool, o accountOp, m staffMember, reason string) (*credential, error) {
	username, _ := loginName(m.Phone)
	existing, err := a.accounts.findByUsername(ctx, username)
	if err != nil {
		return nil, accountUnavailable
	}
	created := false
	var id string
	switch {
	case existing == nil:
		if id, err = a.accounts.createDisabled(ctx, username, m.ID); err != nil {
			var p accountProblem
			if errors.As(err, &p) && p.status == 409 {
				return nil, accountPhoneTaken
			}
			return nil, accountUnavailable
		}
		created = true
	case len(existing.Attributes["baobei_staff_id"]) == 1 && existing.Attributes["baobei_staff_id"][0] == m.ID && existing.Enabled != nil && !*existing.Enabled:
		// A previous attempt for this member stopped before binding: resume it.
		id = existing.ID
	default:
		return nil, accountPhoneTaken
	}
	fail := func(cause error) (*credential, error) {
		cleanup := context.WithoutCancel(ctx)
		if created {
			_ = a.accounts.delete(cleanup, id)
		} else {
			_ = a.accounts.update(cleanup, id, kcUser{Enabled: boolp(false)})
		}
		_ = writeMember(cleanup, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(cleanup, `DELETE FROM identity_links WHERE issuer=$1 AND external_subject=$2`, a.sessionIssuer(), id); err != nil {
				return err
			}
			_, err := tx.Exec(cleanup, `UPDATE app_users SET subject='pending:'||staff_id::text,account_state='FAILED',
 staff_status='PROVISION_FAILED',active=false,account_must_change_password=false WHERE staff_id=$1 AND subject=$2`, m.ID, id)
			return err
		})
		return nil, cause
	}
	password := temporaryPassword()
	if err = a.accounts.setTemporaryPassword(ctx, id, password); err != nil {
		return fail(accountUnavailable)
	}
	if err = a.accounts.setExactRole(ctx, id, m.Role); err != nil {
		return fail(accountUnavailable)
	}
	// Existing member (migration): keep the business subject and every reference;
	// only add the link from the new realm's user id.
	if !strings.HasPrefix(m.Subject, "pending:") {
		err = writeMember(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO identity_links(issuer,external_subject,subject) VALUES($1,$2,$3)`, a.sessionIssuer(), id, m.Subject); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE app_users SET account_state='CREATING',account_must_change_password=true,
 staff_status=CASE WHEN staff_status='ACTIVE' THEN 'PENDING_ACTIVATION' ELSE staff_status END,active=false WHERE staff_id=$1`, m.ID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE account_operations SET keycloak_user_id=$2 WHERE id=$1`, o.id, id); err != nil {
				return err
			}
			return staffAudit(ctx, tx, m.ID, o.actor, "staff.account.create", "迁移登录账号 "+maskedPhone(m.Phone)+" · "+reason, m.Version)
		})
		if err != nil {
			cleanup := context.WithoutCancel(ctx)
			_ = a.accounts.delete(cleanup, id)
			return nil, err
		}
		return a.finishCreate(ctx, pool, m, id, username, password, created)
	}
	// Bind the business identity to the real Keycloak UUID.
	err = writeMember(ctx, pool, func(tx pgx.Tx) error {
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_users WHERE subject=$1 AND staff_id<>$2)`, id, m.ID).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return accountPhoneTaken
		}
		tag, err := tx.Exec(ctx, `UPDATE app_users SET subject=$2,staff_status='PENDING_ACTIVATION',active=false,account_state='CREATING',
 account_must_change_password=true,staff_version=staff_version+1 WHERE staff_id=$1 AND staff_version=$3 AND subject LIKE 'pending:%'`, m.ID, id, m.Version)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return staffConflict
		}
		_, err = tx.Exec(ctx, `UPDATE staff_outbox SET status='SUPERSEDED',processed_at=now() WHERE staff_id=$1 AND status='PENDING'`, m.ID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE account_operations SET keycloak_user_id=$2 WHERE id=$1`, o.id, id)
		if err != nil {
			return err
		}
		// Sign-in under the dedicated realm resolves (issuer, sub) through this link.
		_, err = tx.Exec(ctx, `INSERT INTO identity_links(issuer,external_subject,subject) VALUES($1,$2,$2)
 ON CONFLICT (issuer,external_subject) DO NOTHING`, a.sessionIssuer(), id)
		if err != nil {
			return err
		}
		return staffAudit(ctx, tx, m.ID, o.actor, "staff.account.create", "登录账号 "+maskedPhone(m.Phone)+" · "+reason, m.Version+1)
	})
	if err != nil {
		return fail(err)
	}
	m.Subject = id
	return a.finishCreate(ctx, pool, m, id, username, password, created)
}

// finishCreate verifies the Keycloak side (exact username, exact single Baobei
// role) and only then enables the login. On failure the new login is removed and
// the business record marked failed, so nothing can sign in with a wrong state.
func (a *App) finishCreate(ctx context.Context, pool *pgxpool.Pool, m staffMember, id, username, password string, created bool) (*credential, error) {
	undo := func(cause error) (*credential, error) {
		cleanup := context.WithoutCancel(ctx)
		if created {
			_ = a.accounts.delete(cleanup, id)
		} else {
			_ = a.accounts.update(cleanup, id, kcUser{Enabled: boolp(false)})
		}
		_ = writeMember(cleanup, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(cleanup, `DELETE FROM identity_links WHERE issuer=$1 AND external_subject=$2`, a.sessionIssuer(), id); err != nil {
				return err
			}
			_, err := tx.Exec(cleanup, `UPDATE app_users SET account_state='FAILED',account_must_change_password=false,
 subject=CASE WHEN subject=$2 THEN 'pending:'||staff_id::text ELSE subject END,
 staff_status=CASE WHEN subject=$2 THEN 'PROVISION_FAILED' ELSE staff_status END,active=false WHERE staff_id=$1`, m.ID, id)
			return err
		})
		return nil, cause
	}
	u, err := a.accounts.getUser(ctx, id)
	roles, rerr := a.accounts.roles(ctx, id)
	if err != nil || rerr != nil || u == nil || u.Username != username || !exactClientRole(baobeiRoles(roles), m.Role) {
		return undo(accountUnavailable)
	}
	if err = a.accounts.update(ctx, id, kcUser{Enabled: boolp(true)}); err != nil {
		return undo(accountUnavailable)
	}
	err = writeMember(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE app_users SET account_state='READY' WHERE staff_id=$1`, m.ID)
		return err
	})
	if err != nil {
		return undo(err)
	}
	return &credential{Login: username, TemporaryPassword: password}, nil
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

// accountReset: new temporary password (old one cannot be read, only replaced);
// every Keycloak and Baobei session of the member ends.
func (a *App) accountReset(ctx context.Context, pool *pgxpool.Pool, o accountOp, m staffMember, reason string) (*credential, error) {
	username, err := loginName(m.Phone)
	if err != nil {
		return nil, err
	}
	ext, err := a.externalID(ctx, pool, m)
	if err != nil {
		return nil, err
	}
	password := temporaryPassword()
	if err = a.accounts.setTemporaryPassword(ctx, ext, password); err != nil {
		return nil, accountUnavailable
	}
	_ = a.accounts.logout(ctx, ext)
	err = writeMember(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM app_sessions WHERE subject=$1`, m.Subject); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app_users SET account_must_change_password=true WHERE staff_id=$1`, m.ID); err != nil {
			return err
		}
		return staffAudit(ctx, tx, m.ID, o.actor, "staff.account.reset-password", reason, m.Version)
	})
	if err != nil {
		return nil, err
	}
	return &credential{Login: username, TemporaryPassword: password}, nil
}

// accountChangePhone: the login name follows the work phone; the UUID stays.
// Keycloak is changed first; if the database update then fails, the Keycloak
// username is put back, so both sides always agree.
func (a *App) accountChangePhone(ctx context.Context, pool *pgxpool.Pool, o accountOp, m staffMember, in accountRequest) error {
	phone, err := normalizedPhone(in.Phone)
	if err != nil {
		return err
	}
	username, err := loginName(phone)
	if err != nil {
		return err
	}
	oldName, _ := loginName(m.Phone)
	if phone == m.Phone {
		return staffProblem{400, "ACCOUNT_PHONE_UNCHANGED", "新手机号与当前手机号相同"}
	}
	var taken bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_users WHERE work_phone=$1 AND staff_id<>$2)`, phone, m.ID).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return staffProblem{409, "STAFF_PHONE_DUPLICATE", "该工作手机已被其他成员使用"}
	}
	ext, err := a.externalID(ctx, pool, m)
	if err != nil {
		return err
	}
	if err = a.accounts.update(ctx, ext, kcUser{Username: username}); err != nil {
		var p accountProblem
		if errors.As(err, &p) && p.status == 409 {
			return accountPhoneTaken
		}
		return accountUnavailable
	}
	_ = a.accounts.logout(ctx, ext)
	err = writeMember(ctx, pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE app_users SET work_phone=$2,staff_version=staff_version+1 WHERE staff_id=$1 AND staff_version=$3`, m.ID, phone, m.Version)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return staffConflict
		}
		if _, err = tx.Exec(ctx, `DELETE FROM app_sessions WHERE subject=$1`, m.Subject); err != nil {
			return err
		}
		return staffAudit(ctx, tx, m.ID, o.actor, "staff.account.change-phone", maskedPhone(m.Phone)+" → "+maskedPhone(phone)+" · "+in.Reason, m.Version+1)
	})
	if err != nil {
		if oldName != "" {
			_ = a.accounts.update(context.WithoutCancel(ctx), ext, kcUser{Username: oldName})
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return staffProblem{409, "STAFF_PHONE_DUPLICATE", "该工作手机已被其他成员使用"}
		}
		return err
	}
	return nil
}

// accountDisable: the business account is suspended first (fail closed: Baobei
// refuses the member immediately), then Keycloak disables the login.
func (a *App) accountDisable(ctx context.Context, pool *pgxpool.Pool, o accountOp, m staffMember, reason string) error {
	ext, err := a.externalID(ctx, pool, m)
	if err != nil {
		return err
	}
	err = writeMember(ctx, pool, func(tx pgx.Tx) error {
		if m.Status != staffSuspended {
			if _, err := tx.Exec(ctx, `UPDATE app_users SET staff_status='SUSPENDED',active=false,staff_version=staff_version+1 WHERE staff_id=$1`, m.ID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO staff_handoffs(staff_id,project_id) SELECT $1,id FROM intake_projects WHERE owner_subject=$2 AND `+openProjects+` ON CONFLICT DO NOTHING`, m.ID, m.Subject); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app_sessions WHERE subject=$1`, m.Subject); err != nil {
			return err
		}
		return staffAudit(ctx, tx, m.ID, o.actor, "staff.account.disable", reason, m.Version+1)
	})
	if err != nil {
		return err
	}
	if err = a.accounts.update(ctx, ext, kcUser{Enabled: boolp(false)}); err != nil {
		_ = writeMember(context.WithoutCancel(ctx), pool, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE app_users SET account_state='FAILED' WHERE staff_id=$1`, m.ID)
			return err
		})
		return accountUnavailable
	}
	_ = a.accounts.logout(ctx, ext)
	return writeMember(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE app_users SET account_state='DISABLED' WHERE staff_id=$1`, m.ID)
		return err
	})
}

// accountEnable: restore a suspended member, or apply a changed role. The Keycloak
// side gets exactly the current Baobei role and is enabled; the member then signs
// in again, where the exact-role check activates the business account.
func (a *App) accountEnable(ctx context.Context, pool *pgxpool.Pool, o accountOp, m staffMember, reason string) error {
	ext, err := a.externalID(ctx, pool, m)
	if err != nil {
		return err
	}
	if err := a.accounts.setExactRole(ctx, ext, m.Role); err != nil {
		return accountUnavailable
	}
	roles, err := a.accounts.roles(ctx, ext)
	if err != nil || !exactClientRole(baobeiRoles(roles), m.Role) {
		return accountUnavailable
	}
	if err = a.accounts.update(ctx, ext, kcUser{Enabled: boolp(true)}); err != nil {
		return accountUnavailable
	}
	return writeMember(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE app_users SET staff_status='PENDING_ACTIVATION',active=false,account_state='READY',staff_version=staff_version+1
 WHERE staff_id=$1 AND staff_status IN ('SUSPENDED','PENDING_PROVISION','PROVISION_FAILED')`, m.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE staff_outbox SET status='SUPERSEDED',processed_at=now() WHERE staff_id=$1 AND status='PENDING'`, m.ID); err != nil {
			return err
		}
		action := map[string]string{"ENABLE": "staff.account.enable", "SET_ROLE": "staff.account.sync-role"}[o.operation]
		return staffAudit(ctx, tx, m.ID, o.actor, action, reason, m.Version+1)
	})
}
