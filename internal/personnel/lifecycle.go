package personnel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/kcadmin"
)

// target is the locked state of the person an operation applies to.
type target struct {
	id, username, issuer, subject  string
	enabled, archived, testAccount bool
	protected                      string
	version                        int64
	roles                          []SystemRole
}

func (t target) managed() bool {
	return t.username != "" && kcadmin.ValidSubject(t.subject)
}

func (t target) hasRole(r SystemRole) bool {
	for _, v := range t.roles {
		if v == r {
			return true
		}
	}
	return false
}

func lockTarget(ctx context.Context, tx *sql.Tx, id string) (target, error) {
	var t target
	var username, issuer, subject sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id, username, issuer, subject, enabled, archived_at IS NOT NULL, COALESCE(protected_reason,''), version, test_account
FROM users WHERE id=$1 AND (issuer IS NOT NULL OR username IS NOT NULL) FOR UPDATE`, id).
		Scan(&t.id, &username, &issuer, &subject, &t.enabled, &t.archived, &t.protected, &t.version, &t.testAccount)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	t.username, t.issuer, t.subject = username.String, issuer.String, subject.String
	rows, err := tx.QueryContext(ctx, `SELECT role FROM system_memberships WHERE user_id=$1 ORDER BY role`, id)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var r string
		if err = rows.Scan(&r); err != nil {
			return t, err
		}
		t.roles = append(t.roles, SystemRole(r))
	}
	return t, rows.Err()
}

// guardTarget: account managers may not act on system role holders; nobody disables themselves.
func guardTarget(actorRoles []SystemRole, a Actor, t target) error {
	if len(t.roles) > 0 && !Has(actorRoles, CapSystemRoleManage) {
		return ErrProtected
	}
	return nil
}

func otherEnabledAdmins(ctx context.Context, tx *sql.Tx, exclude string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM system_memberships m JOIN users u ON u.id=m.user_id
WHERE m.role='system_admin' AND u.enabled AND u.id<>$1`, exclude).Scan(&n)
	return n, err
}

// AccountResult reports the outcome of a lifecycle operation.
type AccountResult struct {
	Person            Person `json:"person"`
	IdentitySync      string `json:"identitySync"` // SYNCED | PENDING | UNMANAGED
	SessionsRevoked   int    `json:"sessionsRevoked"`
	LiveLeasesRevoked int    `json:"liveLeasesRevoked"`
	TemporaryPassword string `json:"temporaryPassword,omitempty"`
}

func (s *Service) markSync(ctx context.Context, id string, synced bool) string {
	return s.setSync(ctx, id, synced)
}

func (s *Service) revokeLive(ctx context.Context, id string) (int, error) {
	if s.RevokeLive == nil {
		return 0, nil
	}
	return 0, s.RevokeLive(ctx, id)
}

// fence ends every application session of a person and moves the session fence forward: any OIDC
// login whose auth_time is not after this instant is refused by the BFF (old Keycloak SSO session,
// callback already in flight). Runs inside the caller's transaction (target row locked FOR UPDATE).
func fence(ctx context.Context, tx *sql.Tx, id string) (int, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE users SET sessions_revoked_at=now() WHERE id=$1`, id); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM web_sessions WHERE user_id=$1`, id)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// refence repeats the fence after Keycloak confirmed the change (password replaced / SSO logged
// out): a login that completed between the local commit and the Keycloak change is removed too.
func (s *Service) refence(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM users WHERE id=$1 FOR UPDATE`, id); err != nil {
		return err
	}
	if _, err = fence(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// begin opens the mutation transaction of a lifecycle operation: actor re-authorized (inside the
// person locks), target locked FOR UPDATE, account-manager protection applied.
func (s *Service) begin(ctx context.Context, a Actor, id string, cap SystemCapability, globalAdmin bool) (*sql.Tx, []SystemRole, target, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, target{}, err
	}
	fail := func(e error) (*sql.Tx, []SystemRole, target, error) {
		_ = tx.Rollback()
		return nil, nil, target{}, e
	}
	if globalAdmin {
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, adminLock); err != nil {
			return fail(err)
		}
	}
	roles, err := authorize(ctx, tx, a, cap)
	if err != nil {
		return fail(err)
	}
	t, err := lockTarget(ctx, tx, id)
	if err != nil {
		return fail(err)
	}
	if err = guardTarget(roles, a, t); err != nil {
		return fail(err)
	}
	if t.archived {
		// An archived (deleted) person is frozen: no profile edit, re-enable, password or session
		// operation. Only the permanent purge of a test account may follow.
		return fail(ErrArchived)
	}
	return tx, roles, t, nil
}

// UpdateProfile edits name / department / job title / contact (username is immutable).
func (s *Service) UpdateProfile(ctx context.Context, a Actor, id string, version int64, p Profile) (AccountResult, error) {
	var out AccountResult
	if _, err := s.Authorize(ctx, a, CapAccountManage); err != nil {
		return out, err
	}
	if err := p.normalize(); err != nil {
		return out, err
	}
	err := s.withPeople(ctx, []string{a.ID, id}, func() error {
		tx, _, t, err := s.begin(ctx, a, id, CapAccountManage, false)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if t.version != version {
			return fmt.Errorf("%w: the person was changed by someone else", ErrConflict)
		}
		if err = checkDepartment(ctx, tx, p.DepartmentID); err != nil {
			return err
		}
		before, err := loadPerson(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE users SET display_name=$2, department_id=NULLIF($3,''), job_title=$4, phone=NULLIF($5,''), email=NULLIF($6,''),
version=version+1, updated_at=now(), idp_state=CASE WHEN $7 THEN 'PENDING' ELSE idp_state END WHERE id=$1`,
			id, p.DisplayName, p.DepartmentID, p.JobTitle, p.Phone, p.Email, t.managed()); err != nil {
			return err
		}
		change := map[string]any{}
		for k, v := range map[string][2]string{"displayName": {before.DisplayName, p.DisplayName}, "departmentId": {before.DepartmentID, p.DepartmentID},
			"jobTitle": {before.JobTitle, p.JobTitle}, "phone": {before.Phone, p.Phone}, "email": {before.Email, p.Email}} {
			if v[0] != v[1] {
				change[k] = map[string]string{"from": v[0], "to": v[1]}
			}
		}
		if err = auditlog.Write(ctx, tx, a.event("profile_updated", "success", id, "account", change)); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		out.IdentitySync, err = s.syncIdentity(ctx, s.DB, id, true)
		return err
	})
	if err != nil {
		return out, err
	}
	out.Person, err = loadPerson(ctx, s.DB, id)
	return out, err
}

// SetEnabled disables or re-enables a person. The database is the authority and changes first;
// Keycloak is then brought to the same state (syncIdentity). Login needs BOTH users.enabled (BFF,
// every request and WebSocket frame) and an enabled Keycloak account, so a Keycloak failure after
// a disable can never re-open access: the result reports identitySync=PENDING and repeating the
// same disable (or enable) re-runs only the Keycloak synchronization — it is idempotent instead of
// answering "already disabled".
//
// Disable also moves the session fence and deletes every web session (existing cookies and
// streams stop at once), and ends live-view leases. Enable never restores grants revoked in the
// meantime. Accounts of an unfinished opening can only be completed / cancelled through the
// provisioning flow.
func (s *Service) SetEnabled(ctx context.Context, a Actor, id string, version int64, enabled bool) (AccountResult, error) {
	var out AccountResult
	if _, err := s.Authorize(ctx, a, CapAccountManage); err != nil {
		return out, err
	}
	if !a.Host && a.ID == id {
		return out, ErrSelf
	}
	err := s.withPeople(ctx, []string{a.ID, id}, func() error {
		tx, _, t, err := s.begin(ctx, a, id, CapAccountManage, true)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if pending, err := unfinishedProvision(ctx, tx, t.username); err != nil {
			return err
		} else if pending {
			return ErrProvisionIncomplete
		}
		var idpState string
		if err = tx.QueryRowContext(ctx, `SELECT idp_state FROM users WHERE id=$1`, id).Scan(&idpState); err != nil {
			return err
		}
		if t.enabled == enabled {
			if !t.managed() || idpState != "PENDING" {
				return fmt.Errorf("%w: the account is already %s", ErrConflict, map[bool]string{true: "enabled", false: "disabled"}[enabled])
			}
			// Idempotent retry of a change whose Keycloak half did not complete.
			if err = tx.Commit(); err != nil {
				return err
			}
			if !enabled {
				if _, err = s.revokeLive(ctx, id); err != nil {
					return err
				}
			}
			out.IdentitySync, err = s.syncIdentity(ctx, s.DB, id, false)
			if err != nil {
				return err
			}
			if out.IdentitySync == "SYNCED" && !enabled {
				if err = s.refence(ctx, id); err != nil {
					return err
				}
			}
			return auditlog.Write(ctx, s.DB, a.event("identity_sync", map[bool]string{true: "success", false: "pending"}[out.IdentitySync == "SYNCED"], id, "account",
				map[string]any{"enabled": enabled, "retry": true}))
		}
		if version != 0 && t.version != version {
			return fmt.Errorf("%w: the person was changed by someone else", ErrConflict)
		}
		if !enabled && t.hasRole(RoleSystemAdmin) {
			n, err := otherEnabledAdmins(ctx, tx, id)
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrLastAdmin
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE users SET enabled=$2, idp_state=CASE WHEN $3 THEN 'PENDING' ELSE idp_state END, version=version+1, updated_at=now() WHERE id=$1`,
			id, enabled, t.managed()); err != nil {
			return err
		}
		action, change := "account_enabled", map[string]any{"restoredRevokedGrants": false}
		if !enabled {
			n, err := fence(ctx, tx, id)
			if err != nil {
				return err
			}
			out.SessionsRevoked = n
			action, change = "account_disabled", map[string]any{"sessionsRevoked": n}
		}
		if err = auditlog.Write(ctx, tx, a.event(action, "success", id, "account", change)); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if !enabled {
			if _, err = s.revokeLive(ctx, id); err != nil {
				return err
			}
		}
		out.IdentitySync, err = s.syncIdentity(ctx, s.DB, id, false)
		if err == nil && !enabled && out.IdentitySync == "SYNCED" {
			err = s.refence(ctx, id)
		}
		return err
	})
	if err != nil {
		return out, err
	}
	out.Person, err = loadPerson(ctx, s.DB, id)
	return out, err
}

// RevokeSessions ends every application session and live-view lease of a person, logs out their
// Keycloak SSO sessions and moves the session fence (an old SSO session or an in-flight callback
// cannot mint a new application session; the person must enter the password again).
func (s *Service) RevokeSessions(ctx context.Context, a Actor, id string) (AccountResult, error) {
	var out AccountResult
	err := s.withPeople(ctx, []string{a.ID, id}, func() error {
		tx, _, t, err := s.begin(ctx, a, id, CapAccountManage, false)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		n, err := fence(ctx, tx, id)
		if err != nil {
			return err
		}
		out.SessionsRevoked = n
		if err = auditlog.Write(ctx, tx, a.event("sessions_revoked", "success", id, "account", map[string]any{"sessionsRevoked": n})); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if _, err = s.revokeLive(ctx, id); err != nil {
			return err
		}
		out.IdentitySync = "UNMANAGED"
		if t.managed() {
			synced := s.IdP != nil && s.IdP.Logout(ctx, t.subject) == nil
			out.IdentitySync = s.markSync(ctx, id, synced)
			if synced {
				return s.refence(ctx, id)
			}
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	out.Person, err = loadPerson(ctx, s.DB, id)
	return out, err
}

// ResetPassword issues a new one-time password (returned once, UPDATE_PASSWORD required). Order:
// fence + delete sessions + end live views (local, one transaction) → Keycloak password replaced →
// Keycloak SSO logout → fence again. Neither the old password, an old application session, an old
// SSO session nor a login that raced the reset can be used afterwards. A Keycloak failure returns
// ErrIdentityProvider (sessions are already revoked; retry issues a fresh password). Concurrent
// resets of the same person are serialized: each caller gets the password that is actually set.
func (s *Service) ResetPassword(ctx context.Context, a Actor, id string) (AccountResult, error) {
	var out AccountResult
	if s.IdP == nil {
		return out, ErrNotConfigured
	}
	err := s.withPeople(ctx, []string{a.ID, id}, func() error {
		tx, _, t, err := s.begin(ctx, a, id, CapAccountManage, false)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if !t.managed() {
			return InvalidError{"account", "该人员的登录账号未纳入平台管理，请先由宿主机管理员纳管"}
		}
		if pending, err := unfinishedProvision(ctx, tx, t.username); err != nil {
			return err
		} else if pending {
			return ErrProvisionIncomplete
		}
		n, err := fence(ctx, tx, id)
		if err != nil {
			return err
		}
		out.SessionsRevoked = n
		if err = tx.Commit(); err != nil {
			return err
		}
		if _, err = s.revokeLive(ctx, id); err != nil {
			return err
		}
		password, err := GeneratePassword()
		if err != nil {
			return err
		}
		if err = s.IdP.SetTemporaryPassword(ctx, t.subject, password); err != nil {
			_ = auditlog.Write(ctx, s.DB, a.event("password_reset", "identity_provider_failed", id, "account", map[string]any{"sessionsRevoked": n}))
			return ErrIdentityProvider
		}
		if err = s.fault("after_password_reset"); err != nil {
			return err
		}
		logoutErr := s.IdP.Logout(ctx, t.subject)
		if err = s.refence(ctx, id); err != nil {
			return err
		}
		if err = auditlog.Write(ctx, s.DB, a.event("password_reset", "success", id, "account", map[string]any{"sessionsRevoked": n, "temporaryPassword": "issued", "ssoLogout": logoutErr == nil})); err != nil {
			return err
		}
		out.IdentitySync = s.markSync(ctx, id, logoutErr == nil)
		out.TemporaryPassword = string(password)
		return nil
	})
	if err != nil {
		return out, err
	}
	out.Person, err = loadPerson(ctx, s.DB, id)
	return out, err
}

// SetSystemRoles replaces a person's system roles. expected is the role set the caller last saw
// (concurrency guard). Self-changes are refused; the last enabled system_admin cannot lose it.
func (s *Service) SetSystemRoles(ctx context.Context, a Actor, id string, roles, expected []string) (Person, error) {
	var out Person
	err := s.withPeople(ctx, []string{a.ID, id}, func() error {
		p, err := s.setSystemRoles(ctx, a, id, roles, expected)
		out = p
		return err
	})
	return out, err
}

func (s *Service) setSystemRoles(ctx context.Context, a Actor, id string, roles, expected []string) (Person, error) {
	norm := func(in []string) ([]string, error) {
		set := map[string]bool{}
		for _, r := range in {
			if !ValidSystemRole(r) {
				return nil, InvalidError{"systemRoles", "未知系统角色"}
			}
			set[r] = true
		}
		out := make([]string, 0, len(set))
		for r := range set {
			out = append(out, r)
		}
		sort.Strings(out)
		return out, nil
	}
	if _, err := s.Authorize(ctx, a, CapSystemRoleManage); err != nil {
		return Person{}, err
	}
	want, err := norm(roles)
	if err != nil {
		return Person{}, err
	}
	seen, err := norm(expected)
	if err != nil {
		return Person{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Person{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, adminLock); err != nil {
		return Person{}, err
	}
	if _, err = authorize(ctx, tx, a, CapSystemRoleManage); err != nil {
		return Person{}, err
	}
	if !a.Host && a.ID == id {
		return Person{}, ErrSelf
	}
	t, err := lockTarget(ctx, tx, id)
	if err != nil {
		return Person{}, err
	}
	if t.archived {
		return Person{}, ErrArchived
	}
	current := make([]string, 0, len(t.roles))
	for _, r := range t.roles {
		current = append(current, string(r))
	}
	if strings.Join(current, ",") != strings.Join(seen, ",") {
		return Person{}, fmt.Errorf("%w: system roles were changed by someone else", ErrConflict)
	}
	if strings.Join(current, ",") == strings.Join(want, ",") {
		return Person{}, fmt.Errorf("%w: system roles already in the requested state", ErrConflict)
	}
	keepsAdmin := false
	for _, r := range want {
		if r == string(RoleSystemAdmin) {
			keepsAdmin = true
		}
	}
	if t.hasRole(RoleSystemAdmin) && !keepsAdmin {
		n, err := otherEnabledAdmins(ctx, tx, id)
		if err != nil {
			return Person{}, err
		}
		if n == 0 {
			return Person{}, ErrLastAdmin
		}
	}
	if len(want) > 0 && !t.enabled {
		return Person{}, InvalidError{"systemRoles", "已停用的人员不能授予系统角色"}
	}
	if pending, err := unfinishedProvision(ctx, tx, t.username); err != nil {
		return Person{}, err
	} else if pending {
		return Person{}, ErrProvisionIncomplete
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM system_memberships WHERE user_id=$1 AND NOT (role = ANY($2))`, id, want); err != nil {
		return Person{}, err
	}
	for _, r := range want {
		if _, err = tx.ExecContext(ctx, `INSERT INTO system_memberships(user_id,role,granted_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, r, a.event("", "", "", "", nil).Actor); err != nil {
			return Person{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET version=version+1, updated_at=now() WHERE id=$1`, id); err != nil {
		return Person{}, err
	}
	if err = auditlog.Write(ctx, tx, a.event("system_role_changed", "success", id, "system", map[string]any{"from": current, "to": want})); err != nil {
		return Person{}, err
	}
	if err = tx.Commit(); err != nil {
		return Person{}, err
	}
	return loadPerson(ctx, s.DB, id)
}

// SetPortfolioRole grants, changes (role) or revokes (role="") one portfolio role. expected is the
// role the caller last saw ("" = none). Self-changes are refused.
func (s *Service) SetPortfolioRole(ctx context.Context, a Actor, id, scope, role, expected string) (Person, error) {
	var out Person
	err := s.withPeople(ctx, []string{a.ID, id}, func() error {
		p, err := s.setPortfolioRole(ctx, a, id, scope, role, expected)
		out = p
		return err
	})
	return out, err
}

func (s *Service) setPortfolioRole(ctx context.Context, a Actor, id, scope, role, expected string) (Person, error) {
	if _, err := s.Authorize(ctx, a, CapPortfolioRoleMgmt); err != nil {
		return Person{}, err
	}
	if !idPattern.MatchString(scope) {
		return Person{}, ErrNotFound
	}
	if role != "" && role != "portfolio_planner" && role != "portfolio_manager" {
		return Person{}, InvalidError{"role", "未知项目组合角色"}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Person{}, err
	}
	defer tx.Rollback()
	if _, err = authorize(ctx, tx, a, CapPortfolioRoleMgmt); err != nil {
		return Person{}, err
	}
	if !a.Host && a.ID == id {
		return Person{}, ErrSelf
	}
	t, err := lockTarget(ctx, tx, id)
	if err != nil {
		return Person{}, err
	}
	if t.archived {
		return Person{}, ErrArchived
	}
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM portfolio_scopes WHERE id=$1 FOR SHARE`, scope).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return Person{}, ErrNotFound
	}
	if err != nil {
		return Person{}, err
	}
	var current string
	err = tx.QueryRowContext(ctx, `SELECT role FROM portfolio_memberships WHERE user_id=$1 AND portfolio_scope_id=$2`, id, scope).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Person{}, err
	}
	if current != expected {
		return Person{}, fmt.Errorf("%w: the portfolio role was changed by someone else", ErrConflict)
	}
	if current == role {
		return Person{}, fmt.Errorf("%w: portfolio role already in the requested state", ErrConflict)
	}
	if role != "" && (!t.enabled || status != "ACTIVE") {
		return Person{}, InvalidError{"role", "人员已停用或项目组合已停用"}
	}
	if pending, err := unfinishedProvision(ctx, tx, t.username); err != nil {
		return Person{}, err
	} else if pending {
		return Person{}, ErrProvisionIncomplete
	}
	if role == "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM portfolio_memberships WHERE user_id=$1 AND portfolio_scope_id=$2`, id, scope)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES($1,$2,$3)
ON CONFLICT (user_id,portfolio_scope_id) DO UPDATE SET role=excluded.role, updated_at=now()`, id, scope, role)
	}
	if err != nil {
		return Person{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET version=version+1, updated_at=now() WHERE id=$1`, id); err != nil {
		return Person{}, err
	}
	if err = auditlog.Write(ctx, tx, a.event("portfolio_role_changed", "success", id, "portfolio:"+scope, map[string]any{"from": current, "to": role})); err != nil {
		return Person{}, err
	}
	if err = tx.Commit(); err != nil {
		return Person{}, err
	}
	return loadPerson(ctx, s.DB, id)
}

// PortfolioScope is a portfolio the system administrator can assign roles in.
type PortfolioScope struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (s *Service) PortfolioScopes(ctx context.Context, a Actor) ([]PortfolioScope, error) {
	if _, err := s.Authorize(ctx, a, CapPersonnelRead); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, status FROM portfolio_scopes ORDER BY status, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PortfolioScope{}
	for rows.Next() {
		var p PortfolioScope
		if err = rows.Scan(&p.ID, &p.Name, &p.Status); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Candidate is the minimum a project-member picker may see about a person.
type Candidate struct {
	UserID         string `json:"userId"`
	DisplayName    string `json:"displayName"`
	Username       string `json:"username"`
	DepartmentName string `json:"departmentName"`
	JobTitle       string `json:"jobTitle"`
}

// Candidates searches ENABLED people of the configured identity provider for a project-member
// picker. The caller must already have proven project_grant_manage on the project; the query must
// have at least 1 character and the result is capped, so the picker cannot enumerate the directory
// in one call and exposes no contact data, status history or roles.
func Candidates(ctx context.Context, db *sql.DB, issuer, query string, limit int) ([]Candidate, error) {
	q := strings.TrimSpace(query)
	if q == "" || len([]rune(q)) > 40 {
		return []Candidate{}, InvalidError{"q", "请输入姓名、用户名或部门进行搜索"}
	}
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	pattern := "%" + escapeLike(q) + "%"
	rows, err := db.QueryContext(ctx, `SELECT u.id, u.display_name, COALESCE(u.username,''), COALESCE(d.name,''), u.job_title
FROM users u LEFT JOIN departments d ON d.id=u.department_id
WHERE u.enabled AND u.issuer=$1 AND (u.display_name ILIKE $2 OR u.username ILIKE $2 OR u.id ILIKE $2 OR d.name ILIKE $2)
ORDER BY u.display_name, u.id LIMIT $3`, issuer, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Candidate{}
	for rows.Next() {
		var c Candidate
		if err = rows.Scan(&c.UserID, &c.DisplayName, &c.Username, &c.DepartmentName, &c.JobTitle); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------------------------
// Host recovery (tools/identity) — controlled, audited, never reachable over HTTP.
// ---------------------------------------------------------------------------------------------

// AdoptAdministrator makes an EXISTING realm user (looked up by exact username in Keycloak) a
// managed, enabled person holding system_admin. Used to bootstrap the first administrator from an
// account that already logs in, or to recover when no administrator remains. It never changes the
// user's password. If a local identity for that subject exists it is kept (same id, grants and
// history) and only gains the username and the role.
func (s *Service) AdoptAdministrator(ctx context.Context, a Actor, username, displayName string) (Person, error) {
	var out Person
	err := s.withPeople(ctx, []string{"adopt:" + strings.ToLower(strings.TrimSpace(username))}, func() error {
		p, err := s.adoptAdministrator(ctx, a, username, displayName)
		out = p
		return err
	})
	return out, err
}

func (s *Service) adoptAdministrator(ctx context.Context, a Actor, username, displayName string) (Person, error) {
	if !a.Host {
		return Person{}, ErrForbidden
	}
	if s.IdP == nil {
		return Person{}, ErrNotConfigured
	}
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(username) && !strings.Contains(username, "@") {
		return Person{}, InvalidError{"username", "用户名无效"}
	}
	u, err := s.IdP.FindByUsername(ctx, username)
	if err != nil {
		return Person{}, ErrIdentityProvider
	}
	if u == nil {
		return Person{}, ErrNotFound
	}
	var local string
	var archived bool
	err = s.DB.QueryRowContext(ctx, `SELECT id, archived_at IS NOT NULL FROM users WHERE issuer=$1 AND subject=$2`, s.Issuer, u.ID).Scan(&local, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		local = sanitizeLocalID(username)
		if reserved, e := usernameReserved(ctx, s.DB, local); e != nil {
			return Person{}, e
		} else if reserved {
			return Person{}, fmt.Errorf("%w: local id %s belonged to a purged person", ErrConflict, local)
		}
	} else if err != nil {
		return Person{}, err
	}
	if archived {
		// An archived person is never revived, not even by host recovery.
		return Person{}, ErrArchived
	}
	if _, err = s.IdP.Adopt(ctx, u.ID, local); err != nil {
		return Person{}, ErrIdentityProvider
	}
	if u.Enabled == nil || !*u.Enabled {
		if err = s.IdP.SetEnabled(ctx, u.ID, true); err != nil {
			return Person{}, ErrIdentityProvider
		}
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = strings.TrimSpace(u.FirstName + " " + u.LastName)
	}
	if name == "" {
		name = username
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Person{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, adminLock); err != nil {
		return Person{}, err
	}
	stored := username
	if !usernamePattern.MatchString(stored) {
		stored = local
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO users(id,issuer,subject,display_name,enabled,username,idp_state,created_by) VALUES($1,$2,$3,$4,true,$5,'SYNCED',$6)
ON CONFLICT (id) DO UPDATE SET enabled=true, username=COALESCE(users.username, excluded.username), display_name=CASE WHEN users.display_name='' THEN excluded.display_name ELSE users.display_name END,
idp_state='SYNCED', version=users.version+1, updated_at=now() WHERE users.issuer=excluded.issuer AND users.subject=excluded.subject AND users.archived_at IS NULL`,
		local, s.Issuer, u.ID, name, stored, a.event("", "", "", "", nil).Actor); err != nil {
		if isUniqueViolation(err) {
			return Person{}, fmt.Errorf("%w: username or local id already used by another person", ErrConflict)
		}
		return Person{}, err
	}
	var bound string
	if err = tx.QueryRowContext(ctx, `SELECT subject FROM users WHERE id=$1`, local).Scan(&bound); err != nil || bound != u.ID {
		return Person{}, fmt.Errorf("%w: local id %s is bound to another identity", ErrConflict, local)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO system_memberships(user_id,role,granted_by) VALUES($1,'system_admin',$2) ON CONFLICT DO NOTHING`, local, a.event("", "", "", "", nil).Actor); err != nil {
		return Person{}, err
	}
	if err = auditlog.Write(ctx, tx, a.event("account_adopted", "success", local, "system", map[string]any{"username": username, "systemRoles": []string{"system_admin"}})); err != nil {
		return Person{}, err
	}
	if err = tx.Commit(); err != nil {
		return Person{}, err
	}
	return loadPerson(ctx, s.DB, local)
}

func sanitizeLocalID(username string) string {
	var b strings.Builder
	for _, c := range username {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
			b.WriteRune(c)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

// RecordLogin stamps the last successful login (called by the BFF inside its session transaction).
func RecordLogin(ctx context.Context, tx *sql.Tx, user string, at time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE users SET last_login_at=$2 WHERE id=$1`, user, at)
	return err
}
