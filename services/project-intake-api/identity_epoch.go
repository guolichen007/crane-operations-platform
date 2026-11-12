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

// identitySwitchCommand: `maintenance identity-switch --realm <approved> --operator <id> --reason <text> [--dry-run]`.
// The realm must be one of the environment's approved realms; the issuer is
// taken from the profile, never from the command line.
func identitySwitchCommand(ctx context.Context, db *pgxpool.Pool, env string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("identity-switch", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	realm := flags.String("realm", "", "approved realm")
	operator := flags.String("operator", "", "operator id")
	reason := flags.String("reason", "", "reason")
	dry := flags.Bool("dry-run", false, "only report")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return errors.New("identity-switch 参数无效")
	}
	issuer := devIssuer
	if deployed(env) {
		r, err := selectRealm(env, *realm)
		if err != nil {
			return err
		}
		issuer = r.Issuer
	}
	if *dry {
		var current string
		var sessions int64
		_ = db.QueryRow(ctx, `SELECT issuer FROM identity_epoch`).Scan(&current)
		if err := db.QueryRow(ctx, `SELECT count(*) FROM app_sessions`).Scan(&sessions); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"dryRun": true, "from": current, "to": issuer, "sessionsToRevoke": sessions})
	}
	from, epoch, revoked, err := switchIdentity(ctx, db, issuer, *operator, *reason)
	if err != nil {
		return errors.New("identity-switch 未执行")
	}
	return json.NewEncoder(out).Encode(map[string]any{"from": from, "to": issuer, "epoch": epoch, "revokedSessions": revoked})
}
