// Package personnel is the Crane system-administration domain: people (users), lightweight
// departments, login accounts in the shared Keycloak realm, and the independent SYSTEM
// permission domain.
//
// Permission model (P1, 2026-11-18):
//
//	system_admin     — personnel + accounts + system roles + portfolio roles; may grant/revoke
//	                   system roles (never to themselves); the last enabled system_admin cannot be
//	                   removed, disabled or demoted.
//	account_manager  — personnel + accounts (create, edit, enable/disable, reset password,
//	                   revoke sessions) for people who hold NO system role; cannot grant system or
//	                   portfolio roles and cannot act on any system role holder.
//
// A system role is NOT a project or portfolio role: it never grants access to project assets,
// devices, telemetry, schedules or portfolio data. platform_admin keeps meaning "project
// administrator" and gives no system rights. Project memberships continue to be granted only by
// portfolio managers inside their own portfolio (unchanged Q2 rule).
package personnel

import "errors"

type SystemRole string

const (
	RoleSystemAdmin    SystemRole = "system_admin"
	RoleAccountManager SystemRole = "account_manager"
)

var SystemRoles = []SystemRole{RoleSystemAdmin, RoleAccountManager}

func ValidSystemRole(r string) bool {
	for _, v := range SystemRoles {
		if string(v) == r {
			return true
		}
	}
	return false
}

// SystemCapability is an explicit system-domain permission.
type SystemCapability string

const (
	CapPersonnelRead     SystemCapability = "personnel_read"
	CapAccountManage     SystemCapability = "account_manage"
	CapDepartmentManage  SystemCapability = "department_manage"
	CapSystemRoleManage  SystemCapability = "system_role_manage"
	CapPortfolioRoleMgmt SystemCapability = "portfolio_role_manage"
	CapPersonnelAudit    SystemCapability = "personnel_audit_read"
	// CapPersonnelArchive (R9): archive ("delete") a person and purge an unreferenced test
	// account. system_admin only — an account manager can disable but never delete.
	CapPersonnelArchive SystemCapability = "personnel_archive"
)

func roleCapabilities(role SystemRole) []SystemCapability {
	switch role {
	case RoleSystemAdmin:
		return []SystemCapability{CapPersonnelRead, CapAccountManage, CapDepartmentManage, CapSystemRoleManage, CapPortfolioRoleMgmt, CapPersonnelAudit, CapPersonnelArchive}
	case RoleAccountManager:
		return []SystemCapability{CapPersonnelRead, CapAccountManage, CapPersonnelAudit}
	}
	return nil
}

// Has reports whether any of the roles carries cap.
func Has(roles []SystemRole, cap SystemCapability) bool {
	for _, r := range roles {
		for _, c := range roleCapabilities(r) {
			if c == cap {
				return true
			}
		}
	}
	return false
}

var (
	// ErrForbidden: authenticated but lacking the system capability (HTTP 403).
	ErrForbidden = errors.New("system permission required")
	// ErrNotFound: unknown person / department (HTTP 404).
	ErrNotFound = errors.New("not found")
	// ErrInvalid: input failed validation (HTTP 422); the message is user-facing.
	ErrInvalid = errors.New("invalid input")
	// ErrConflict: duplicate username, stale version, or concurrent change (HTTP 409).
	ErrConflict = errors.New("conflict")
	// ErrLastAdmin: the change would leave no enabled system administrator (HTTP 409).
	ErrLastAdmin = errors.New("the last enabled system administrator must remain")
	// ErrSelf: an administrator may not change their own system role or disable themselves (HTTP 422).
	ErrSelf = errors.New("this change cannot be applied to your own account")
	// ErrProtected: the target holds a system role the actor may not manage (HTTP 403).
	ErrProtected = errors.New("the target holds a system role you cannot manage")
	// ErrIdempotencyKey: the idempotency key was reused with a different request (HTTP 422).
	ErrIdempotencyKey = errors.New("idempotency key reused with a different request")
	// ErrIdentityProvider: Keycloak unavailable or refused; the operation can be retried (HTTP 503).
	ErrIdentityProvider = errors.New("identity provider unavailable; the operation can be retried")
	// ErrArchived: the person is archived (deleted); only a test-account purge may follow (HTTP 409).
	ErrArchived = errors.New("the person is archived")
	// ErrProtectedIdentity: OWNER / service / system identities are never archived or purged (HTTP 403).
	ErrProtectedIdentity = errors.New("protected identity")
	// ErrReferenced: a purge was refused because business history references the person (HTTP 409).
	ErrReferenced = errors.New("the person is referenced by business history; archive only")
	// ErrNotConfigured: the deployment has no account service credential (HTTP 503).
	ErrNotConfigured = errors.New("account service is not configured on this deployment")
)

// InvalidError wraps ErrInvalid with a field-level message.
type InvalidError struct{ Field, Message string }

func (e InvalidError) Error() string { return e.Field + ": " + e.Message }
func (e InvalidError) Unwrap() error { return ErrInvalid }
