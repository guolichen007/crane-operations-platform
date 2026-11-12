package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// errIdentityEpochMismatch: the database trusts a different issuer than the one
// this API was started with. The API refuses to serve rather than guessing.
var errIdentityEpochMismatch = errors.New("identity issuer does not match the database identity epoch")

// verifyIdentityEpoch runs at startup. Deployed environments require that the
// database already trusts exactly this issuer (set by the controlled
// identity-switch command), otherwise the API fails closed. Development
// initializes the row for its local issuer.
func verifyIdentityEpoch(ctx context.Context, pool *pgxpool.Pool, env, issuer string) error {
	var current string
	err := pool.QueryRow(ctx, `SELECT issuer FROM identity_epoch`).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		if deployed(env) {
			return fmt.Errorf("%w: run maintenance identity-switch first", errIdentityEpochMismatch)
		}
		_, err = pool.Exec(ctx, `INSERT INTO identity_epoch(issuer,epoch,changed_by,reason) VALUES($1,1,'development','开发环境初始化')
 ON CONFLICT (singleton) DO NOTHING`, issuer)
		return err
	}
	if err != nil {
		return err
	}
	if current != issuer {
		return errIdentityEpochMismatch
	}
	return nil
}

// switchIdentity is the only way to change the trusted issuer. In one
// transaction (under the identity lock, so no login can interleave) it deletes
// every Baobei session and staff session, then advances the epoch. Old cookies,
// old tokens and old staff reauthentication proofs all become invalid; every
// user must sign in again against the new realm. Other applications' sessions
// are untouched (they are not in this database).
func switchIdentity(ctx context.Context, db *pgxpool.Pool, issuer, operator, reason string) (from string, epoch int, revoked int64, err error) {
	return switchIdentityGuarded(ctx, db, issuer, operator, reason, nil)
}

func switchIdentityGuarded(ctx context.Context, db *pgxpool.Pool, issuer, operator, reason string, guard func(pgx.Tx) error) (from string, epoch int, revoked int64, err error) {
	if issuer == "" || !evidenceID.MatchString(operator) || !reasonOK(reason) {
		return "", 0, 0, staffInvalid
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return "", 0, 0, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, identityLock); err != nil {
		return "", 0, 0, err
	}
	if guard != nil {
		if err = guard(tx); err != nil {
			return "", 0, 0, err
		}
	}
	err = tx.QueryRow(ctx, `SELECT issuer,epoch FROM identity_epoch FOR UPDATE`).Scan(&from, &epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		from, epoch, err = "", 0, nil
	}
	if err != nil {
		return "", 0, 0, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM app_sessions`)
	if err != nil {
		return "", 0, 0, err
	}
	revoked = tag.RowsAffected()
	epoch++
	if _, err = tx.Exec(ctx, `INSERT INTO identity_epoch(issuer,epoch,changed_by,reason) VALUES($1,$2,$3,$4)
 ON CONFLICT (singleton) DO UPDATE SET issuer=EXCLUDED.issuer,epoch=EXCLUDED.epoch,changed_at=now(),changed_by=EXCLUDED.changed_by,reason=EXCLUDED.reason`,
		issuer, epoch, operator, reason); err != nil {
		return "", 0, 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(subject,action,revision,details) VALUES($1,'IDENTITY_SWITCH',$2,$3::jsonb)`,
		operator, epoch, string(mustJSON(map[string]any{"from": from, "to": issuer, "reason": reason, "revokedSessions": revoked}))); err != nil {
		return "", 0, 0, err
	}
	return from, epoch, revoked, tx.Commit(ctx)
}

// switchPrecheck is the report every switch (and every dry run) is based on.
// It fails closed: an unreadable or incomplete schema, a missing identity epoch
// (except the very first establishment on the shared realm) or the lack of an
// administrator who can sign in under the target realm is an error, never an
// empty "OK".
type switchPrecheck struct {
	From             string `json:"from"`
	To               string `json:"to"`
	Realm            string `json:"realm"`
	Epoch            int    `json:"epoch"`
	SessionsToRevoke int64  `json:"sessionsToRevoke"`
	// TakeoverAdmin: the named, approved administrator verified for the target.
	TakeoverAdmin string `json:"takeoverAdmin"`
	// LinkedAdmins: active administrators who can sign in under the target realm.
	LinkedAdmins int `json:"linkedAdmins"`
	// Members (active or pending) who will NOT be able to sign in after the switch
	// (no identity under the target realm). A switch never reports "everyone".
	CannotSignIn int `json:"cannotSignIn"`
}

var (
	errSwitchSchema = errors.New("identity-switch: 数据库 Schema 未就绪或无法读取（先在维护窗口完成迁移）")
	errSwitchEpoch  = errors.New("identity-switch: 身份纪元不存在；只能首先在共享 Realm 建立")
	errSwitchAdmin  = errors.New("identity-switch: 目标 Realm 没有可登录的已批准管理员，拒绝切换")
)

// precheckSwitch reads everything inside one repeatable-read snapshot.
func precheckSwitch(ctx context.Context, db *pgxpool.Pool, env, realm, takeover string) (*switchPrecheck, error) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, errSwitchSchema
	}
	defer tx.Rollback(ctx)
	return precheckIn(ctx, tx, env, realm, takeover)
}

func precheckIn(ctx context.Context, tx pgx.Tx, env, realm, takeover string) (*switchPrecheck, error) {
	issuer, linkedOnly := devIssuer, false
	if deployed(env) {
		r, err := selectRealm(env, realm)
		if err != nil {
			return nil, err
		}
		issuer, linkedOnly = r.Issuer, r.AccountService
	}
	if verifySchema(ctx, tx) != nil {
		return nil, errSwitchSchema
	}
	p := &switchPrecheck{To: issuer, Realm: realm}
	err := tx.QueryRow(ctx, `SELECT issuer,epoch FROM identity_epoch`).Scan(&p.From, &p.Epoch)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Only the first establishment, on the shared (non linked-only) realm.
		if linkedOnly {
			return nil, errSwitchEpoch
		}
	case err != nil:
		return nil, errSwitchSchema
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM app_sessions`).Scan(&p.SessionsToRevoke); err != nil {
		return nil, errSwitchSchema
	}
	// Who can sign in under the target: linked-only realms need an identity link;
	// the shared realm signs in with sub = business subject, which members created
	// in the dedicated realm (subject = their dedicated-realm id) do not have.
	canSignIn := `EXISTS(SELECT 1 FROM identity_links l WHERE l.issuer=$1 AND l.subject=u.subject)`
	if !linkedOnly {
		canSignIn = `NOT EXISTS(SELECT 1 FROM identity_links l WHERE l.subject=u.subject AND l.external_subject=u.subject AND l.issuer<>$1)
 AND u.subject ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'`
	}
	if err = tx.QueryRow(ctx, `SELECT
 count(*) FILTER (WHERE u.role='baobei_admin' AND u.staff_status='ACTIVE' AND u.active AND u.account_state NOT IN ('CREATING','REVIEW') AND `+canSignIn+`),
 count(*) FILTER (WHERE u.staff_status IN ('ACTIVE','PENDING_ACTIVATION') AND NOT (`+canSignIn+`))
 FROM app_users u`, issuer).Scan(&p.LinkedAdmins, &p.CannotSignIn); err != nil {
		return nil, errSwitchSchema
	}
	if p.Epoch == 0 && !linkedOnly {
		// First establishment of the epoch on the shared realm: nothing changes for
		// sign-in (sub = subject), no takeover administrator is required.
		return p, nil
	}
	if !uuidPattern.MatchString(takeover) {
		return nil, errSwitchAdmin
	}
	var ok bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_users u WHERE u.staff_id=$2 AND u.role='baobei_admin'
 AND u.staff_status='ACTIVE' AND u.active AND u.account_state NOT IN ('CREATING','REVIEW')
 AND u.work_phone ~ '^\+861[3-9][0-9]{9}$' AND `+canSignIn+`)`, issuer, takeover).Scan(&ok); err != nil {
		return nil, errSwitchSchema
	}
	if !ok || p.LinkedAdmins < 1 {
		return nil, errSwitchAdmin
	}
	p.TakeoverAdmin = takeover
	return p, nil
}

// identitySwitchCommand: `maintenance identity-switch --realm <approved> --operator <id>
// --reason <text> --takeover-admin <staff_id> --approval <ref> [--dry-run]`.
// The realm must be one of the environment's approved realms; the issuer is
// taken from the profile, never from the command line. The dry run and the
// switch run the same fail-closed precheck; the switch re-checks it inside its
// own transaction.
func identitySwitchCommand(ctx context.Context, db *pgxpool.Pool, env string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("identity-switch", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	realm := flags.String("realm", "", "approved realm")
	operator := flags.String("operator", "", "operator id")
	reason := flags.String("reason", "", "reason")
	takeover := flags.String("takeover-admin", "", "staff id of the approved takeover administrator")
	approval := flags.String("approval", "", "approval / emergency takeover plan reference")
	dry := flags.Bool("dry-run", false, "only report")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return errors.New("identity-switch 参数无效")
	}
	if db == nil {
		return errSwitchSchema
	}
	if deployed(env) {
		if _, err := selectRealm(env, *realm); err != nil {
			return err
		}
	}
	p, err := precheckSwitch(ctx, db, env, *realm, *takeover)
	if err != nil {
		return err
	}
	if *dry {
		return json.NewEncoder(out).Encode(map[string]any{"dryRun": true, "precheck": "PASS", "report": p})
	}
	if p.TakeoverAdmin != "" && !evidenceID.MatchString(*approval) {
		return errors.New("identity-switch: 需要已批准的接管方案编号 --approval")
	}
	from, epoch, revoked, err := switchIdentityChecked(ctx, db, env, *realm, p, *operator, *reason, *approval)
	if err != nil {
		if errors.Is(err, errSwitchAdmin) || errors.Is(err, errSwitchSchema) || errors.Is(err, errSwitchEpoch) {
			return err
		}
		return errors.New("identity-switch 未执行")
	}
	return json.NewEncoder(out).Encode(map[string]any{"from": from, "to": p.To, "epoch": epoch, "revokedSessions": revoked,
		"takeoverAdmin": p.TakeoverAdmin, "cannotSignIn": p.CannotSignIn,
		"note": "切换后仅目标 Realm 中已建立身份的成员可登录；cannotSignIn 名单需人工处置"})
}

// switchIdentityChecked repeats the whole precheck inside the switch
// transaction, under the identity lock: nothing can change between the check
// and the switch, and a precheck that no longer holds aborts it.
func switchIdentityChecked(ctx context.Context, db *pgxpool.Pool, env, realm string, p *switchPrecheck, operator, reason, approval string) (string, int, int64, error) {
	if approval != "" {
		reason = reason + " · 接管方案 " + approval
	}
	return switchIdentityGuarded(ctx, db, p.To, operator, reason, func(tx pgx.Tx) error {
		again, err := precheckIn(ctx, tx, env, realm, p.TakeoverAdmin)
		if err != nil {
			return err
		}
		if again.From != p.From || again.Epoch != p.Epoch || again.To != p.To {
			return errSwitchSchema
		}
		return nil
	})
}
