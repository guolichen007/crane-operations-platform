package personnel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/kcadmin"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

// adminLock serializes every change that can affect who is an enabled system administrator
// (system role grant/revoke, disable/enable, provisioning with a system role, host recovery).
const adminLock = 7160013

// PortfolioGrantInput is one portfolio role to grant while opening an account.
type PortfolioGrantInput struct {
	ScopeID string `json:"scopeId"`
	Role    string `json:"role"`
}

// ProvisionRequest is the complete "new person + login account + initial permissions" request.
type ProvisionRequest struct {
	Username        string                `json:"username"`
	Profile         Profile               `json:"profile"`
	SystemRoles     []string              `json:"systemRoles"`
	PortfolioGrants []PortfolioGrantInput `json:"portfolioGrants"`
}

// ProvisionResult is returned once. TemporaryPassword is only present on the response that set it.
type ProvisionResult struct {
	ProvisionID       string `json:"provisionId"`
	State             string `json:"state"`
	Person            Person `json:"person"`
	TemporaryPassword string `json:"temporaryPassword,omitempty"`
	PasswordDelivered bool   `json:"passwordDelivered"`
	Replayed          bool   `json:"replayed"`
}

// Provision is one durable account-opening record (no secret is stored).
type Provision struct {
	ID        string           `json:"id"`
	Actor     string           `json:"actor"`
	Username  string           `json:"username"`
	State     string           `json:"state"`
	Failure   string           `json:"failure"`
	Request   ProvisionRequest `json:"request"`
	CreatedAt string           `json:"createdAt"`
	UpdatedAt string           `json:"updatedAt"`
}

func (r *ProvisionRequest) normalize() error {
	r.Username = strings.ToLower(strings.TrimSpace(r.Username))
	if err := validUsername(r.Username); err != nil {
		return err
	}
	if err := r.Profile.normalize(); err != nil {
		return err
	}
	seen := map[string]bool{}
	roles := []string{}
	for _, role := range r.SystemRoles {
		if !ValidSystemRole(role) {
			return InvalidError{"systemRoles", "未知系统角色"}
		}
		if !seen[role] {
			seen[role] = true
			roles = append(roles, role)
		}
	}
	r.SystemRoles = roles
	scopes := map[string]bool{}
	for _, g := range r.PortfolioGrants {
		if !idPattern.MatchString(g.ScopeID) || (g.Role != "portfolio_planner" && g.Role != "portfolio_manager") {
			return InvalidError{"portfolioGrants", "项目组合或角色无效"}
		}
		if scopes[g.ScopeID] {
			return InvalidError{"portfolioGrants", "同一项目组合只能授予一个角色"}
		}
		scopes[g.ScopeID] = true
	}
	if r.PortfolioGrants == nil {
		r.PortfolioGrants = []PortfolioGrantInput{}
	}
	return nil
}

// requireGrantable checks that the actor may grant what the request asks for.
func requireGrantable(roles []SystemRole, r ProvisionRequest) error {
	if len(r.SystemRoles) > 0 && !Has(roles, CapSystemRoleManage) {
		return fmt.Errorf("%w: granting system roles requires system_admin", ErrForbidden)
	}
	if len(r.PortfolioGrants) > 0 && !Has(roles, CapPortfolioRoleMgmt) {
		return fmt.Errorf("%w: granting portfolio roles requires system_admin", ErrForbidden)
	}
	return nil
}

// ProvisionAccount opens an account. The non-atomic Keycloak + database sequence is:
//
//	STARTED      provision row claimed (idempotency key; live username unique)
//	IDP_CREATED  Keycloak user created DISABLED (managed attribute, crane_user_id)
//	BOUND        ONE transaction: local user (enabled=false) + system/portfolio grants + audit
//	             → temporary password set in Keycloak (UPDATE_PASSWORD required)
//	             → Keycloak user enabled
//	COMPLETED    local user enabled
//
// Invariants: a Keycloak account is only enabled after the local identity and its grants are
// committed; the local identity is only enabled after Keycloak is. Login needs BOTH (Keycloak
// authenticates, the BFF requires users.enabled), so no crash or lost response can leave an
// account that can log in without its approved permissions. Every step is re-entrant: a retry with
// the same idempotency key (or Resume) continues from the recorded state. A retry after COMPLETED
// returns the person without a password (it is shown once; reset it if it was not recorded).
func (s *Service) ProvisionAccount(ctx context.Context, a Actor, key string, req ProvisionRequest) (ProvisionResult, error) {
	var out ProvisionResult
	if s.IdP == nil {
		return out, ErrNotConfigured
	}
	// Permission before validation: an unauthorized caller learns nothing about the input rules.
	if _, err := s.Authorize(ctx, a, CapAccountManage); err != nil {
		return out, err
	}
	key = strings.TrimSpace(key)
	if len(key) < 8 || len(key) > 128 {
		return out, InvalidError{"Idempotency-Key", "缺少有效的幂等键"}
	}
	if err := req.normalize(); err != nil {
		return out, err
	}
	canonical, _ := json.Marshal(req)
	digest := sha256hex(canonical)

	// Step 1: claim.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	roles, err := authorize(ctx, tx, a, CapAccountManage)
	if err != nil {
		return out, err
	}
	if err = requireGrantable(roles, req); err != nil {
		return out, err
	}
	var id, state, existingDigest string
	err = tx.QueryRowContext(ctx, `SELECT id,state,payload_sha256 FROM account_provisions WHERE actor=$1 AND idempotency_key=$2 FOR UPDATE`, a.ID, key).Scan(&id, &state, &existingDigest)
	switch {
	case err == nil:
		if existingDigest != digest {
			return out, ErrIdempotencyKey
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		return s.continueProvision(ctx, a, id, true)
	case !errors.Is(err, sql.ErrNoRows):
		return out, err
	}
	var taken bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 OR username=$1)`, req.Username).Scan(&taken); err != nil {
		return out, err
	}
	if taken {
		return out, fmt.Errorf("%w: username already exists", ErrConflict)
	}
	if err = checkDepartment(ctx, tx, req.Profile.DepartmentID); err != nil {
		return out, err
	}
	for _, g := range req.PortfolioGrants {
		var status string
		if e := tx.QueryRowContext(ctx, `SELECT status FROM portfolio_scopes WHERE id=$1`, g.ScopeID).Scan(&status); errors.Is(e, sql.ErrNoRows) || (e == nil && status != "ACTIVE") {
			return out, InvalidError{"portfolioGrants", "项目组合不存在或已停用"}
		} else if e != nil {
			return out, e
		}
	}
	id = "prov-" + platform.ID()
	_, err = tx.ExecContext(ctx, `INSERT INTO account_provisions(id,actor,idempotency_key,payload_sha256,username,request,state) VALUES($1,$2,$3,$4,$5,$6,'STARTED')`,
		id, a.ID, key, digest, req.Username, string(canonical))
	if isUniqueViolation(err) {
		return out, fmt.Errorf("%w: an account for this username is already being opened", ErrConflict)
	}
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return s.continueProvision(ctx, a, id, false)
}

// ResumeProvision continues an unfinished provision (e.g. after the browser was closed).
func (s *Service) ResumeProvision(ctx context.Context, a Actor, id string) (ProvisionResult, error) {
	if s.IdP == nil {
		return ProvisionResult{}, ErrNotConfigured
	}
	roles, err := s.Authorize(ctx, a, CapAccountManage)
	if err != nil {
		return ProvisionResult{}, err
	}
	p, err := s.provision(ctx, s.DB, id)
	if err != nil {
		return ProvisionResult{}, err
	}
	if err = requireGrantable(roles, p.Request); err != nil {
		return ProvisionResult{}, err
	}
	return s.continueProvision(ctx, a, id, false)
}

func (s *Service) provision(ctx context.Context, q queryer, id string) (Provision, error) {
	var p Provision
	var request string
	err := q.QueryRowContext(ctx, `SELECT id,actor,username,state,failure,request::text,to_char(created_at,'YYYY-MM-DD"T"HH24:MI:SSOF'),to_char(updated_at,'YYYY-MM-DD"T"HH24:MI:SSOF')
FROM account_provisions WHERE id=$1`, id).Scan(&p.ID, &p.Actor, &p.Username, &p.State, &p.Failure, &request, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal([]byte(request), &p.Request)
}

// PendingProvisions lists unfinished or failed account openings (recovery view).
func (s *Service) PendingProvisions(ctx context.Context, a Actor) ([]Provision, error) {
	if _, err := s.Authorize(ctx, a, CapAccountManage); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM account_provisions WHERE state IN ('STARTED','IDP_CREATED','BOUND','FAILED') AND updated_at > now() - interval '30 days' ORDER BY updated_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := []Provision{}
	for _, id := range ids {
		p, err := s.provision(ctx, s.DB, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// withProvisionLock runs fn while holding a session advisory lock for one provision, so two
// concurrent retries of the same opening never interleave their Keycloak calls.
func (s *Service) withProvisionLock(ctx context.Context, id string, fn func() error) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var got bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1, hashtext($2))`, adminLock, id).Scan(&got); err != nil {
		return err
	}
	if !got {
		return fmt.Errorf("%w: this account opening is already in progress", ErrConflict)
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1, hashtext($2))`, adminLock, id)
	return fn()
}

func (s *Service) setProvisionState(ctx context.Context, id, state, subject, failure string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE account_provisions SET state=$2, idp_subject=COALESCE(NULLIF($3,''),idp_subject), failure=$4, updated_at=now() WHERE id=$1`, id, state, subject, failure)
	return err
}

func (s *Service) continueProvision(ctx context.Context, a Actor, id string, replay bool) (ProvisionResult, error) {
	out := ProvisionResult{ProvisionID: id, Replayed: replay}
	err := s.withProvisionLock(ctx, id, func() error {
		p, err := s.provision(ctx, s.DB, id)
		if err != nil {
			return err
		}
		var subject sql.NullString
		if err = s.DB.QueryRowContext(ctx, `SELECT idp_subject FROM account_provisions WHERE id=$1`, id).Scan(&subject); err != nil {
			return err
		}
		req, local := p.Request, p.Request.Username
		switch p.State {
		case "COMPLETED":
			out.State = p.State
			out.Person, err = loadPerson(ctx, s.DB, local)
			return err
		case "CANCELLED":
			return fmt.Errorf("%w: this account opening was cancelled", ErrConflict)
		case "FAILED":
			return fmt.Errorf("%w: %s", ErrConflict, p.Failure)
		}

		// Step 2: Keycloak user (created disabled; a lost response is recovered by exact username
		// plus the crane_user_id attribute this provision wrote).
		if p.State == "STARTED" {
			existing, err := s.IdP.FindByUsername(ctx, req.Username)
			if err != nil {
				return ErrIdentityProvider
			}
			if existing != nil {
				owned := existing.Managed() && len(existing.Attributes["crane_user_id"]) == 1 && existing.Attributes["crane_user_id"][0] == local &&
					(existing.Enabled == nil || !*existing.Enabled)
				if !owned {
					_ = s.setProvisionState(ctx, id, "FAILED", "", "username already exists in the identity provider")
					_ = auditlog.Write(ctx, s.DB, a.event("account_provision_failed", "conflict", local, "account", map[string]any{"username": local, "reason": "idp_username_exists"}))
					return fmt.Errorf("%w: username already exists in the identity provider", ErrConflict)
				}
				subject.String = existing.ID
			} else {
				created, err := s.IdP.CreateDisabled(ctx, kcadmin.Profile{Username: req.Username, DisplayName: req.Profile.DisplayName, Email: req.Profile.Email, LocalUserID: local})
				if errors.Is(err, kcadmin.ErrConflict) {
					_ = s.setProvisionState(ctx, id, "FAILED", "", "username already exists in the identity provider")
					_ = auditlog.Write(ctx, s.DB, a.event("account_provision_failed", "conflict", local, "account", map[string]any{"username": local, "reason": "idp_username_exists"}))
					return fmt.Errorf("%w: username already exists in the identity provider", ErrConflict)
				}
				if err != nil {
					return ErrIdentityProvider
				}
				subject.String = created
			}
			if err = s.fault("after_idp_create"); err != nil {
				return err
			}
			if err = s.setProvisionState(ctx, id, "IDP_CREATED", subject.String, ""); err != nil {
				return err
			}
			p.State = "IDP_CREATED"
		}

		// Step 3: bind + grants + audit in one transaction (local user stays disabled).
		if p.State == "IDP_CREATED" {
			if err = s.bind(ctx, a, id, req, subject.String); err != nil {
				return err
			}
			p.State = "BOUND"
		}

		// Step 4: temporary password, then enable Keycloak, then enable locally.
		password, err := GeneratePassword()
		if err != nil {
			return err
		}
		if err = s.IdP.SetTemporaryPassword(ctx, subject.String, password); err != nil {
			return ErrIdentityProvider
		}
		if err = s.fault("after_password"); err != nil {
			return err
		}
		if err = s.IdP.SetEnabled(ctx, subject.String, true); err != nil {
			return ErrIdentityProvider
		}
		if err = s.fault("after_idp_enable"); err != nil {
			return err
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, `UPDATE users SET enabled=true, idp_state='SYNCED', version=version+1, updated_at=now() WHERE id=$1`, local); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE account_provisions SET state='COMPLETED', failure='', updated_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		if err = auditlog.Write(ctx, tx, a.event("account_enabled", "success", local, "account", map[string]any{"via": "provision", "temporaryPassword": "issued"})); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		out.State = "COMPLETED"
		out.TemporaryPassword = string(password)
		out.PasswordDelivered = true
		out.Person, err = loadPerson(ctx, s.DB, local)
		return err
	})
	if err != nil {
		return out, err
	}
	if out.State == "COMPLETED" && out.TemporaryPassword == "" {
		out.PasswordDelivered = false
	}
	return out, nil
}

func (s *Service) bind(ctx context.Context, a Actor, provisionID string, req ProvisionRequest, subject string) error {
	local := req.Username
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(req.SystemRoles) > 0 {
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, adminLock); err != nil {
			return err
		}
	}
	// Re-check the actor (a concurrent role removal must stop the grant) and the department.
	roles, err := authorize(ctx, tx, a, CapAccountManage)
	if err != nil {
		return err
	}
	if err = requireGrantable(roles, req); err != nil {
		return err
	}
	if err = checkDepartment(ctx, tx, req.Profile.DepartmentID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO users(id,issuer,subject,display_name,enabled,username,department_id,job_title,phone,email,idp_state,created_by)
VALUES($1,$2,$3,$4,false,$1,NULLIF($5,''),$6,NULLIF($7,''),NULLIF($8,''),'PENDING',$9)`,
		local, s.Issuer, subject, req.Profile.DisplayName, req.Profile.DepartmentID, req.Profile.JobTitle, req.Profile.Phone, req.Profile.Email, a.event("", "", "", "", nil).Actor)
	if isUniqueViolation(err) {
		_ = s.setProvisionState(ctx, provisionID, "FAILED", "", "username or identity already bound")
		return fmt.Errorf("%w: username already exists", ErrConflict)
	}
	if err != nil {
		return err
	}
	for _, role := range req.SystemRoles {
		if _, err = tx.ExecContext(ctx, `INSERT INTO system_memberships(user_id,role,granted_by) VALUES($1,$2,$3)`, local, role, a.ID); err != nil {
			return err
		}
	}
	for _, g := range req.PortfolioGrants {
		var status string
		if e := tx.QueryRowContext(ctx, `SELECT status FROM portfolio_scopes WHERE id=$1 FOR SHARE`, g.ScopeID).Scan(&status); e != nil || status != "ACTIVE" {
			return InvalidError{"portfolioGrants", "项目组合不存在或已停用"}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES($1,$2,$3)`, local, g.ScopeID, g.Role); err != nil {
			return err
		}
	}
	if err = auditlog.Write(ctx, tx, a.event("personnel_created", "success", local, "account", map[string]any{
		"username": local, "displayName": req.Profile.DisplayName, "departmentId": req.Profile.DepartmentID, "jobTitle": req.Profile.JobTitle,
		"systemRoles": req.SystemRoles, "portfolioGrants": req.PortfolioGrants, "provisionId": provisionID,
	})); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE account_provisions SET state='BOUND', updated_at=now() WHERE id=$1`, provisionID); err != nil {
		return err
	}
	if err = s.fault("before_bind_commit"); err != nil {
		return err
	}
	return tx.Commit()
}

// CancelProvision abandons an opening that has not been bound yet: the disabled Keycloak user it
// created (if any) is deleted and the username is released. A bound provision must be resumed.
func (s *Service) CancelProvision(ctx context.Context, a Actor, id string) error {
	if s.IdP == nil {
		return ErrNotConfigured
	}
	if _, err := s.Authorize(ctx, a, CapAccountManage); err != nil {
		return err
	}
	return s.withProvisionLock(ctx, id, func() error {
		p, err := s.provision(ctx, s.DB, id)
		if err != nil {
			return err
		}
		if p.State != "STARTED" && p.State != "IDP_CREATED" && p.State != "FAILED" {
			return fmt.Errorf("%w: only an unbound opening can be cancelled (state %s)", ErrConflict, p.State)
		}
		var subject sql.NullString
		if err = s.DB.QueryRowContext(ctx, `SELECT idp_subject FROM account_provisions WHERE id=$1`, id).Scan(&subject); err != nil {
			return err
		}
		if !subject.Valid && p.State == "STARTED" {
			// A lost CreateDisabled response may have created the user: find it by name + owner.
			if u, err := s.IdP.FindByUsername(ctx, p.Username); err != nil {
				return ErrIdentityProvider
			} else if u != nil && u.Managed() && len(u.Attributes["crane_user_id"]) == 1 && u.Attributes["crane_user_id"][0] == p.Username {
				var bound bool
				if err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE subject=$1)`, u.ID).Scan(&bound); err != nil {
					return err
				}
				if !bound {
					subject.String, subject.Valid = u.ID, true
				}
			}
		}
		if subject.Valid && subject.String != "" {
			if err = s.IdP.DeleteUnbound(ctx, subject.String); err != nil {
				return ErrIdentityProvider
			}
		}
		if err = s.setProvisionState(ctx, id, "CANCELLED", "", ""); err != nil {
			return err
		}
		return auditlog.Write(ctx, s.DB, a.event("account_provision_cancelled", "success", p.Username, "account", map[string]any{"username": p.Username, "provisionId": id}))
	})
}
