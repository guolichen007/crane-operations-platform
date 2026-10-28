package identity

import (
	"context"
	"database/sql"
	"errors"
)

type authorizationKey struct{}
type authorization struct {
	user        string
	sessionHash []byte
}

func WithAuthorization(ctx context.Context, user string, sessionHash []byte) context.Context {
	return context.WithValue(ctx, authorizationKey{}, authorization{user: user, sessionHash: sessionHash})
}

// Live mutation repositories recheck authorization inside their transaction.
// Row locks serialize logout/revoke against commit, closing the check/use window.
// Legacy development calls carry no production authorization context.
func RuntimeTransaction(ctx context.Context, tx *sql.Tx, device, user string) error {
	request, present := ctx.Value(authorizationKey{}).(authorization)
	if !present {
		return nil
	}
	if request.user != user || len(request.sessionHash) != 32 {
		return ErrDenied
	}
	var role string
	err := tx.QueryRowContext(ctx, `SELECT m.role FROM devices d
JOIN project_memberships m ON m.project_id=d.project_id AND m.user_id=$2
JOIN users u ON u.id=m.user_id
JOIN web_sessions s ON s.user_id=u.id AND s.session_hash=$3
WHERE d.id=$1 AND u.enabled AND s.expires_at>now() AND s.idle_expires_at>now()
FOR SHARE OF d,m,u,s`, device, user, request.sessionHash).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !Allowed(role, "watch")) {
		return ErrDenied
	}
	return err
}
