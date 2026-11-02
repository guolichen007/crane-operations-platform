// Package identity: explicit project capability model.
//
// Roles are only bundles of capabilities. No code may compare roles ordinally
// (role >= maintainer) or treat platform_admin as a global bypass: every
// capability still requires a project grant. Equipment Condition is
// TELEMETRY_OWNED and deliberately has no write capability.
package identity

// Capability is an explicit project-scoped permission.
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

// roleCapabilities returns the frozen capability bundle for a role. Unknown
// roles yield no capabilities, so any check fails closed.
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

// HasCapability reports whether a role's bundle contains cap. It is the single
// authority; no capability is inferred outside the frozen bundles.
func HasCapability(role string, cap Capability) bool {
	for _, c := range roleCapabilities(role) {
		if c == cap {
			return true
		}
	}
	return false
}
