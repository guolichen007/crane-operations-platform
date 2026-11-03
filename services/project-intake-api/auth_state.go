package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

var errAuthCapacity = errors.New("pending login capacity reached")

func (a *App) cleanupAuth(ctx context.Context) error {
	tx, e := a.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `DELETE FROM oidc_pending_logins WHERE expires_at<=now()`); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM app_sessions WHERE expires_at<=now()`); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (a *App) storePending(ctx context.Context, stateHash string, p pendingLogin) error {
	tx, e := a.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727103)`); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM oidc_pending_logins WHERE expires_at<=now()`); e != nil {
		return e
	}
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM oidc_pending_logins`).Scan(&count); e != nil {
		return e
	}
	if count >= 1024 {
		return errAuthCapacity
	}
	if _, e = tx.Exec(ctx, `INSERT INTO oidc_pending_logins(state_hash,nonce,pkce_verifier,binding_hash,expires_at) VALUES($1,$2,$3,$4,$5)`, stateHash, p.Nonce, p.Verifier, p.Binding, p.Until); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (a *App) consumePending(ctx context.Context, stateHash string) (pendingLogin, error) {
	var p pendingLogin
	e := a.db.QueryRow(ctx, `DELETE FROM oidc_pending_logins WHERE state_hash=$1 RETURNING nonce,pkce_verifier,binding_hash,expires_at`, stateHash).Scan(&p.Nonce, &p.Verifier, &p.Binding, &p.Until)
	if errors.Is(e, pgx.ErrNoRows) {
		return p, e
	}
	return p, e
}
