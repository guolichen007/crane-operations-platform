package identity

import (
	"context"
	"testing"

	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

func mustHave(t *testing.T, role string, caps ...Capability) {
	t.Helper()
	for _, c := range caps {
		if !HasCapability(role, c) {
			t.Errorf("%s should have %s", role, c)
		}
	}
}

func mustLack(t *testing.T, role string, caps ...Capability) {
	t.Helper()
	for _, c := range caps {
		if HasCapability(role, c) {
			t.Errorf("%s should NOT have %s", role, c)
		}
	}
}

func TestRoleCapabilityBundles(t *testing.T) {
	mustHave(t, "viewer", CapRead)
	mustLack(t, "viewer", CapWatch, CapMaintenanceWrite, CapAssetMasterWrite, CapComplianceWrite)

	mustHave(t, "operator", CapRead, CapWatch, CapMaintenanceRequestCreate)
	mustLack(t, "operator", CapMaintenanceWrite, CapAssetMasterWrite, CapCatalogWrite)

	mustHave(t, "maintainer", CapRead, CapWatch, CapInspectionWrite, CapMaintenanceWrite, CapWireRopeWrite, CapDocumentWrite)
	mustLack(t, "maintainer", CapAssetMasterWrite, CapLocationWrite, CapCatalogWrite, CapAssetRetire, CapComplianceWrite, CapHandoverWrite)

	mustHave(t, "platform_admin", CapRead, CapWatch, CapInspectionWrite, CapMaintenanceWrite, CapWireRopeWrite, CapDocumentWrite,
		CapAssetMasterWrite, CapLocationWrite, CapCatalogWrite, CapAssetRetire, CapComplianceWrite, CapHandoverWrite)
}

// Equipment Condition is TELEMETRY_OWNED; no role has a direct write capability.
func TestConditionHasNoWriteCapability(t *testing.T) {
	cond := Capability("equipment_condition_write")
	for _, role := range []string{"viewer", "operator", "maintainer", "platform_admin"} {
		if HasCapability(role, cond) {
			t.Fatalf("%s must not expose equipment_condition_write", role)
		}
	}
}

func TestUnknownRoleFailsClosed(t *testing.T) {
	for _, cap := range []Capability{CapRead, CapWatch, CapMaintenanceWrite, CapAssetMasterWrite} {
		if HasCapability("unknown_role", cap) {
			t.Fatalf("unknown role granted %s", cap)
		}
	}
}

func TestLegacyActionCompat(t *testing.T) {
	if !Allowed("viewer", "read") || Allowed("viewer", "watch") || Allowed("viewer", "admin") {
		t.Fatal("viewer legacy action mapping")
	}
	if !Allowed("operator", "watch") || Allowed("operator", "admin") {
		t.Fatal("operator legacy action mapping")
	}
	if !Allowed("maintainer", "read") || Allowed("maintainer", "admin") {
		t.Fatal("maintainer legacy action mapping")
	}
	if Allowed("platform_admin", "admin") {
		t.Fatal("legacy coarse admin action must be fail-closed even for platform_admin")
	}
}

// AuthorizeCapability must require a real project grant and must not let a
// platform_admin bypass a missing grant or reach another project.
func TestAuthorizeCapabilityRequiresProjectGrant(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()

	if _, err := db.Exec(`
INSERT INTO users(id,issuer,subject,display_name,enabled) VALUES
('u-admin','https://issuer.example','admin','Admin',true),
('u-viewer','https://issuer.example','viewer','Viewer',true);
INSERT INTO projects(id,tenant_id,name) VALUES('Project-B','demo','Project-B');
INSERT INTO project_memberships(user_id,project_id,role) VALUES
('u-admin','Project-A','platform_admin'),
('u-viewer','Project-A','viewer');
`); err != nil {
		t.Fatal(err)
	}

	if err := AuthorizeCapability(ctx, db, "u-admin", "Project-A", CapAssetMasterWrite); err != nil {
		t.Fatalf("platform_admin with grant denied asset_master_write: %v", err)
	}
	if err := AuthorizeCapability(ctx, db, "u-viewer", "Project-A", CapRead); err != nil {
		t.Fatalf("viewer with grant denied read: %v", err)
	}
	if err := AuthorizeCapability(ctx, db, "u-viewer", "Project-A", CapAssetMasterWrite); err == nil {
		t.Fatal("viewer granted asset_master_write")
	}
	if err := AuthorizeCapability(ctx, db, "u-admin", "Project-B", CapRead); err == nil {
		t.Fatal("platform_admin without Project-B grant read Project-B")
	}
	if err := AuthorizeCapability(ctx, db, "u-viewer", "Project-B", CapRead); err == nil {
		t.Fatal("viewer without Project-B grant read Project-B")
	}
	if err := AuthorizeCapability(ctx, db, "u-admin", "Project-A", Capability("equipment_condition_write")); err == nil {
		t.Fatal("platform_admin exposed equipment_condition_write")
	}
}

func TestPortfolioCapabilityBundles(t *testing.T) {
	assertHave := func(role string, caps ...Capability) {
		t.Helper()
		for _, c := range caps {
			if !HasPortfolioCapability(role, c) {
				t.Errorf("%s should have %s", role, c)
			}
		}
	}
	assertLack := func(role string, caps ...Capability) {
		t.Helper()
		for _, c := range caps {
			if HasPortfolioCapability(role, c) {
				t.Errorf("%s should NOT have %s", role, c)
			}
		}
	}
	assertHave("portfolio_planner", CapPortfolioRead, CapProjectIntakeRead, CapProjectIntakeWrite, CapProjectScheduleRead, CapProjectScheduleWrite, CapResourceCalendarRead)
	assertLack("portfolio_planner", CapProjectApprove, CapScheduleOverride, CapResourceCalendarWrite)
	assertHave("portfolio_manager", CapPortfolioRead, CapProjectApprove, CapScheduleOverride, CapResourceCalendarWrite, CapPortfolioRiskWrite)
	// Q2: grant management is portfolio_manager-only; no project role (incl. platform_admin)
	// carries it, and it is never a project capability (it cannot read/write project assets).
	assertHave("portfolio_manager", CapProjectGrantManage)
	assertLack("portfolio_planner", CapProjectGrantManage)
	for _, role := range []string{"viewer", "operator", "maintainer", "platform_admin"} {
		if HasPortfolioCapability(role, CapProjectGrantManage) || HasCapability(role, CapProjectGrantManage) {
			t.Errorf("%s must not have project_grant_manage", role)
		}
	}
	for _, c := range []Capability{CapRead, CapWatch, CapAssetMasterWrite, CapLocationWrite} {
		if HasPortfolioCapability("portfolio_manager", c) {
			t.Errorf("portfolio_manager must not hold project capability %s", c)
		}
	}
}

// PortfolioGrant must never grant project asset access, and a project
// platform_admin must never get a portfolio bypass.
func TestPortfolioGrantIsolation(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	if _, err := db.Exec(`
INSERT INTO users(id,issuer,subject,display_name,enabled) VALUES
('u-pm','https://issuer.example','pm','PM',true),
('u-admin','https://issuer.example','admin','Admin',true);
INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS-A','demo','PS-A','Portfolio A');
INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('u-pm','PS-A','portfolio_manager');
`); err != nil {
		t.Fatal(err)
	}
	// portfolio_manager without PortfolioGrant? no — it has one, so this must pass.
	if err := AuthorizePortfolioCapability(ctx, db, "u-pm", "PS-A", CapProjectIntakeWrite); err != nil {
		t.Fatalf("portfolio_manager with grant denied intake_write: %v", err)
	}
	// platform_admin with only a ProjectGrant must NOT have a portfolio bypass.
	if err := AuthorizePortfolioCapability(ctx, db, "u-admin", "PS-A", CapProjectIntakeWrite); err == nil {
		t.Fatal("platform_admin without PortfolioGrant got portfolio access")
	}
	// Portfolio capability must not be a project capability: a portfolio grant
	// does not imply project asset read.
	if err := AuthorizeCapability(ctx, db, "u-pm", "Project-A", CapRead); err == nil {
		t.Fatal("portfolio_manager without ProjectGrant read project asset")
	}
}
