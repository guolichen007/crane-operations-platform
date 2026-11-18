package personnel

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/kcadmin"
)

// personLock is the advisory-lock class serializing every account / permission operation on one
// person END TO END, including the Keycloak calls that happen outside database transactions.
//
// An operation holds the lock of BOTH the acting administrator and the target person (acquired in
// sorted order, so two operations can never deadlock on each other). Consequences:
//   - disable / enable / reset password / revoke sessions / profile / role changes / provisioning
//     of the same person never interleave (no "disable then enable" half states in Keycloak);
//   - a target cannot gain a system role while an account manager is acting on it (the role change
//     needs the target's lock), and the checks of the running operation stay valid;
//   - an administrator's own permissions cannot be removed while one of their operations is in
//     flight; the removal is ordered after it, and every later operation re-reads the roles and is
//     refused.
const personLock = 7160014

// ErrBusy: the person is locked by another operation for longer than the wait limit (HTTP 409).
var ErrBusy = errors.New("another account operation on this person is in progress")

// ErrProvisionIncomplete: the account was created by an opening that has not completed; it can
// only be finished (or cancelled) through the provisioning flow, never enabled directly.
var ErrProvisionIncomplete = errors.New("account opening not completed")

// lockWait bounds how long an operation waits for a person lock (tests shorten it).
var lockWait = 15 * time.Second

func (s *Service) withPeople(ctx context.Context, ids []string, fn func() error) error {
	set := map[string]bool{}
	sorted := []string{}
	for _, id := range ids {
		if id != "" && !set[id] {
			set[id] = true
			sorted = append(sorted, id)
		}
	}
	sort.Strings(sorted)
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	held := 0
	defer func() {
		// Session advisory locks survive on a pooled connection: release explicitly, and if that
		// fails discard the connection so the lock dies with the backend session.
		if held > 0 {
			if _, e := conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock_all()`); e != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
		}
		_ = conn.Close()
	}()
	for _, id := range sorted {
		deadline := time.Now().Add(lockWait)
		for {
			var got bool
			if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1, hashtext($2))`, personLock, id).Scan(&got); err != nil {
				return err
			}
			if got {
				held++
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%w: %s", ErrBusy, id)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	return fn()
}

// unfinishedProvision reports whether the user row was created by an opening that is still
// STARTED / IDP_CREATED / BOUND (not COMPLETED, CANCELLED or FAILED).
func unfinishedProvision(ctx context.Context, q queryer, username string) (bool, error) {
	if username == "" {
		return false, nil
	}
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM account_provisions WHERE username=$1 AND state IN ('STARTED','IDP_CREATED','BOUND')`, username).Scan(&n)
	return n > 0, err
}

// syncIdentity pushes the person's CURRENT database state to Keycloak (enabled flag, and the profile
// when withProfile; a
// disabled account is also logged out everywhere) and records the result in users.idp_state. It
// is idempotent: repeating a disable / enable / profile change re-runs exactly this. Callers hold
// the person lock.
func (s *Service) syncIdentity(ctx context.Context, q queryer, id string, withProfile bool) (string, error) {
	var username, subject, issuer sql.NullString
	var display, email sql.NullString
	var enabled bool
	err := q.QueryRowContext(ctx, `SELECT username, subject, issuer, display_name, email, enabled FROM users WHERE id=$1`, id).Scan(&username, &subject, &issuer, &display, &email, &enabled)
	if err != nil {
		return "", err
	}
	if !username.Valid || !kcadmin.ValidSubject(subject.String) || issuer.String != s.Issuer {
		return "UNMANAGED", nil
	}
	if s.IdP == nil {
		return s.setSync(ctx, id, false), nil
	}
	var e error
	if withProfile {
		// Only after a profile edit: an adopted account's Keycloak profile is otherwise left untouched.
		e = s.IdP.UpdateProfile(ctx, subject.String, kcadmin.Profile{Username: username.String, DisplayName: display.String, Email: email.String, LocalUserID: id})
	}
	if e == nil {
		e = s.IdP.SetEnabled(ctx, subject.String, enabled)
	}
	if e == nil && !enabled {
		e = s.IdP.Logout(ctx, subject.String)
	}
	if e == nil {
		e = s.fault("after_identity_sync")
	}
	return s.setSync(ctx, id, e == nil), nil
}

func (s *Service) setSync(ctx context.Context, id string, synced bool) string {
	state := "SYNCED"
	if !synced {
		state = "PENDING"
	}
	_, _ = s.DB.ExecContext(context.WithoutCancel(ctx), `UPDATE users SET idp_state=$2 WHERE id=$1`, id, state)
	return state
}
