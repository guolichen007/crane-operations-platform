// Package identity: explicit project and portfolio capability model.
//
// Roles are only bundles of capabilities. No code may compare roles ordinally
// (role >= maintainer) or treat platform_admin as a global bypass: every
// capability still requires a project grant (Project Scope) or a portfolio
// membership (Portfolio Scope). Equipment Condition is TELEMETRY_OWNED and
// deliberately has no write capability.
package identity

// Capability is an explicit scoped permission. Project capabilities and
// Portfolio capabilities share the type but are resolved against different
// grants; a Portfolio capability never grants project asset access.
type Capability string

const CapRead Capability = "read"
const CapWatch Capability = "watch"
const CapMaintenanceRequestCreate Capability = "maintenance_request_create"
const CapInspectionWrite Capability = "inspection_write"
const CapMaintenanceWrite Capability = "maintenance_write"
const CapWireRopeWrite Capability = "wire_rope_write"
const CapDocumentWrite Capability = "document_write"
const CapAssetMasterWrite Capability = "asset_master_write"
const CapLocationWrite Capability = "location_write"
const CapCatalogWrite Capability = "catalog_write"
const CapAssetRetire Capability = "asset_retire"
const CapComplianceWrite Capability = "compliance_write"
const CapHandoverWrite Capability = "handover_write"

// Portfolio capabilities (narrow; never grant project asset access).
const CapPortfolioRead Capability = "portfolio_read"
const CapProjectIntakeRead Capability = "project_intake_read"
const CapProjectIntakeWrite Capability = "project_intake_write"
const CapProjectApprove Capability = "project_approve"
const CapProjectScheduleRead Capability = "project_schedule_read"
const CapProjectScheduleWrite Capability = "project_schedule_write"
const CapResourceCalendarRead Capability = "resource_calendar_read"
const CapResourceCalendarWrite Capability = "resource_calendar_write"
const CapScheduleOverride Capability = "schedule_override"
const CapPortfolioRiskWrite Capability = "portfolio_risk_write"

// roleCapabilities returns the frozen project capability bundle for a role.
// Unknown roles yield no capabilities, so any check fails closed.
func roleCapabilities(role string) []Capability {
	switch role {
	case "viewer":
		return []Capability{CapRead}
	case "operator":
		return []Capability{CapRead, CapWatch, CapMaintenanceRequestCreate}
	case "maintainer":
		return []Capability{
			CapRead, CapWatch, CapMaintenanceRequestCreate,
			CapInspectionWrite, CapMaintenanceWrite, CapWireRopeWrite, CapDocumentWrite,
		}
	case "platform_admin":
		return []Capability{
			CapRead, CapWatch, CapMaintenanceRequestCreate,
			CapInspectionWrite, CapMaintenanceWrite, CapWireRopeWrite, CapDocumentWrite,
			CapAssetMasterWrite, CapLocationWrite, CapCatalogWrite,
			CapAssetRetire, CapComplianceWrite, CapHandoverWrite,
		}
	}
	return nil
}

// portfolioRoleCapabilities returns the frozen portfolio capability bundle.
// A PortfolioGrant never implies a ProjectGrant; project asset/equipment/
// telemetry access still requires an explicit project membership.
func portfolioRoleCapabilities(role string) []Capability {
	switch role {
	case "portfolio_planner":
		return []Capability{
			CapPortfolioRead, CapProjectIntakeRead, CapProjectIntakeWrite,
			CapProjectScheduleRead, CapProjectScheduleWrite, CapResourceCalendarRead,
		}
	case "portfolio_manager":
		return []Capability{
			CapPortfolioRead, CapProjectIntakeRead, CapProjectIntakeWrite, CapProjectApprove,
			CapProjectScheduleRead, CapProjectScheduleWrite,
			CapResourceCalendarRead, CapResourceCalendarWrite,
			CapScheduleOverride, CapPortfolioRiskWrite,
		}
	}
	return nil
}

// HasCapability reports whether a project role's bundle contains cap.
func HasCapability(role string, cap Capability) bool {
	for _, c := range roleCapabilities(role) {
		if c == cap {
			return true
		}
	}
	return false
}

// HasPortfolioCapability reports whether a portfolio role's bundle contains cap.
func HasPortfolioCapability(role string, cap Capability) bool {
	for _, c := range portfolioRoleCapabilities(role) {
		if c == cap {
			return true
		}
	}
	return false
}
