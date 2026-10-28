// Package identity is the local authority for OIDC identities and explicit project grants.
package identity

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
)

var ErrDenied = errors.New("identity or project authorization denied")

type User struct {
	ID, DisplayName string
}

func Allowed(role, action string) bool {
	switch action {
	case "read":
		return role == "viewer" || role == "operator" || role == "maintainer" || role == "platform_admin"
	case "watch":
		return role == "operator" || role == "maintainer" || role == "platform_admin"
	case "admin":
		return role == "platform_admin"
	default:
		return false // Remote control is not a production permission in A1.
	}
}

func Lookup(ctx context.Context, db *sql.DB, issuer, subject string) (User, error) {
	var user User
	err := db.QueryRowContext(ctx, `SELECT id,display_name FROM users WHERE issuer=$1 AND subject=$2 AND enabled`, issuer, subject).Scan(&user.ID, &user.DisplayName)
	if errors.Is(err, sql.ErrNoRows) {
		return user, ErrDenied
	}
	return user, err
}

// Authorize always resolves the database project first; no request project_id is trusted.
func Authorize(ctx context.Context, db *sql.DB, user, project, action string) error {
	role, err := ProjectRole(ctx, db, user, project)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !Allowed(role, action)) {
		return ErrDenied
	}
	return err
}

func ProjectRole(ctx context.Context, db *sql.DB, user, project string) (string, error) {
	var role string
	err := db.QueryRowContext(ctx, `SELECT m.role FROM project_memberships m JOIN users u ON u.id=m.user_id
WHERE m.user_id=$1 AND m.project_id=$2 AND u.enabled`, user, project).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrDenied
	}
	return role, err
}

func DeviceProject(ctx context.Context, db *sql.DB, id string) (string, error) {
	var project string
	err := db.QueryRowContext(ctx, `SELECT project_id FROM devices WHERE id=$1`, id).Scan(&project)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrDenied
	}
	return project, err
}

func Grants(ctx context.Context, db *sql.DB, user string) ([]contract.ProjectGrant, error) {
	rows, err := db.QueryContext(ctx, `SELECT m.project_id,p.name,m.role FROM project_memberships m
JOIN projects p ON p.id=m.project_id JOIN users u ON u.id=m.user_id WHERE m.user_id=$1 AND u.enabled ORDER BY m.project_id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := []contract.ProjectGrant{}
	for rows.Next() {
		var grant contract.ProjectGrant
		if err = rows.Scan(&grant.ProjectId, &grant.ProjectName, &grant.Role); err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

// Bind does not assign any role. issuer+sub is immutable; email is never an identity key.
func Bind(ctx context.Context, db *sql.DB, id, issuer, subject, name string, enabled bool, event auditlog.Event) error {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		!contract.ValidID(id) || strings.TrimSpace(subject) == "" || len(subject) > 512 || len(name) > 256 {
		return errors.New("invalid explicit identity binding")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO users(id,issuer,subject,display_name,enabled) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,enabled=excluded.enabled,updated_at=now()
WHERE users.issuer=excluded.issuer AND users.subject=excluded.subject`, id, issuer, subject, name, enabled)
	if err != nil {
		return errors.New("identity binding conflicts with an existing identity")
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return errors.New("identity binding cannot reassign an existing user")
	}
	event.Action, event.Result = "identity_bound", "success"
	if err = auditlog.Write(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

func ChangeGrant(ctx context.Context, db *sql.DB, user, project, role string, revoke bool, event auditlog.Event) error {
	if !contract.ValidID(user) || !contract.ValidID(project) || (!revoke && !Allowed(role, "read")) {
		return errors.New("invalid explicit project role")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize each user's grant changes, including revoke and bootstrap tools.
	var issuer sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT issuer FROM users WHERE id=$1 FOR UPDATE`, user).Scan(&issuer); err != nil || !issuer.Valid {
		return ErrDenied
	}
	if revoke {
		_, err = tx.ExecContext(ctx, `DELETE FROM project_memberships WHERE user_id=$1 AND project_id=$2`, user, project)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO project_memberships(user_id,project_id,role) VALUES($1,$2,$3)
ON CONFLICT(user_id,project_id) DO UPDATE SET role=excluded.role,updated_at=now()`, user, project, role)
	}
	if err != nil {
		return err
	}
	event.Project, event.Action = project, "project_role_changed"
	event.Result = "granted:" + user + ":" + role
	if revoke {
		event.Result = "revoked:" + user
	}
	if err = auditlog.Write(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}
