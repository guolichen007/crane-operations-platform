package portfolio

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

func TestIntakeStateMachineAndApprove(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users(id,issuer,subject,display_name,enabled) VALUES('u-planner','https://issuer.example','planner','Planner',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS-1','demo','PS-1','Portfolio 1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('u-planner','PS-1','portfolio_manager')`); err != nil {
		t.Fatal(err)
	}
	in := contract.ProjectIntakeInput{
		PortfolioScopeId: "PS-1",
		IntakeCode:       "INT-001",
		ProjectName:      "Crane Project",
		ProjectType:      "NEW_INSTALLATION",
		Priority:         "MEDIUM",
		RiskLevel:        "MEDIUM",
		ScopeSummary:     "test intake",
	}
	intake, err := CreateIntake(ctx, db, in, "u-planner", "req-c")
	if err != nil {
		t.Fatal(err)
	}
	if intake.Status != "REGISTERED" {
		t.Fatalf("status = %s, want REGISTERED", intake.Status)
	}
	// Invalid transition: REGISTERED -> APPROVED.
	if err := TransitionIntake(ctx, db, intake.Id, "APPROVED", "u-planner", "jump", "req-1"); err == nil {
		t.Fatal("invalid transition accepted")
	}
	if err := TransitionIntake(ctx, db, intake.Id, "REVIEW", "u-planner", "review", "req-1"); err != nil {
		t.Fatal(err)
	}
	if err := TransitionIntake(ctx, db, intake.Id, "PLANNING", "u-planner", "plan", "req-1"); err != nil {
		t.Fatal(err)
	}
	projectID, err := ApproveIntake(ctx, db, intake.Id, "u-planner", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if projectID == "" {
		t.Fatal("empty formal project id")
	}
	// Double approve must not create a second project.
	if _, err := ApproveIntake(ctx, db, intake.Id, "u-planner", "req-2"); err == nil {
		t.Fatal("double approve accepted")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM projects WHERE id=$1`, projectID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("formal project count=%d err=%v", count, err)
	}
}

func TestIntakeDuplicateCodeRejected(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS-2','demo','PS-2','Portfolio 2')`); err != nil {
		t.Fatal(err)
	}
	in := contract.ProjectIntakeInput{PortfolioScopeId: "PS-2", IntakeCode: "INT-001", ProjectName: "A", ProjectType: "NEW_INSTALLATION", Priority: "MEDIUM", RiskLevel: "MEDIUM"}
	if _, err := CreateIntake(ctx, db, in, "someone", "req-c"); err != nil {
		t.Fatal(err)
	}
	in2 := in
	in2.ProjectName = "B"
	if _, err := CreateIntake(ctx, db, in2, "someone", "req-c"); !errors.Is(err, ErrDuplicateIntakeCode) {
		t.Fatalf("duplicate intake code: want ErrDuplicateIntakeCode, got %v", err)
	}
}

func TestApproveDoesNotAutoGrantProject(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users(id,issuer,subject,display_name,enabled) VALUES('u-manager','https://issuer.example','manager','Manager',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS-3','demo','PS-3','Portfolio 3')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('u-manager','PS-3','portfolio_manager')`); err != nil {
		t.Fatal(err)
	}
	in := contract.ProjectIntakeInput{PortfolioScopeId: "PS-3", IntakeCode: "INT-001", ProjectName: "A", ProjectType: "NEW_INSTALLATION", Priority: "MEDIUM", RiskLevel: "MEDIUM"}
	intake, err := CreateIntake(ctx, db, in, "u-manager", "req-c")
	if err != nil {
		t.Fatal(err)
	}
	if err := TransitionIntake(ctx, db, intake.Id, "REVIEW", "u-manager", "review", "req-1"); err != nil {
		t.Fatal(err)
	}
	if err := TransitionIntake(ctx, db, intake.Id, "PLANNING", "u-manager", "plan", "req-1"); err != nil {
		t.Fatal(err)
	}
	projectID, err := ApproveIntake(ctx, db, intake.Id, "u-manager", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	// Approving must not implicitly create a ProjectGrant for the manager.
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM project_memberships WHERE project_id=$1`, projectID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("approval implicitly created %d project memberships", count)
	}
}

func TestValidateIntakeInputRejectsBeforeWriting(t *testing.T) {
	ok := contract.ProjectIntakeInput{PortfolioScopeId: "PS-1", IntakeCode: "INT-1", ProjectName: "A", ProjectType: "NEW_INSTALLATION", Priority: "MEDIUM", RiskLevel: "LOW"}
	if err := ValidateIntakeInput(ok); err != nil {
		t.Fatalf("valid input refused: %v", err)
	}
	start, end := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*contract.ProjectIntakeInput){
		"blank code":       func(v *contract.ProjectIntakeInput) { v.IntakeCode = " " },
		"blank name":       func(v *contract.ProjectIntakeInput) { v.ProjectName = "" },
		"blank type":       func(v *contract.ProjectIntakeInput) { v.ProjectType = "\t" },
		"bad priority":     func(v *contract.ProjectIntakeInput) { v.Priority = "URGENT" },
		"bad risk":         func(v *contract.ProjectIntakeInput) { v.RiskLevel = "" },
		"bad scope id":     func(v *contract.ProjectIntakeInput) { v.PortfolioScopeId = "PS 1" },
		"name too long":    func(v *contract.ProjectIntakeInput) { v.ProjectName = strings.Repeat("长", 161) },
		"plan end ≤ start": func(v *contract.ProjectIntakeInput) { v.PlannedStart, v.PlannedEnd = &start, &end },
		"handover ≤ start": func(v *contract.ProjectIntakeInput) { v.RequestedStart, v.RequestedHandover = &start, &start },
	} {
		v := ok
		mutate(&v)
		if err := ValidateIntakeInput(v); !errors.Is(err, ErrInvalidIntake) {
			t.Errorf("%s: want ErrInvalidIntake, got %v", name, err)
		}
	}
}

// The in-transaction precondition used by grant changes must re-check BOTH the project's scope
// and the actor's capability, independently of any earlier HTTP-level check (a membership
// revoked between the check and the write, or a scope mix-up, must not slip through).
func TestLockManageableProjectRechecksScopeAndCapability(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO users(id,issuer,subject,display_name,enabled) VALUES('u-m','https://i','m','M',true),('u-p','https://i','p','P',true),('u-off','https://i','o','O',false)`,
		`INSERT INTO portfolio_scopes(id,tenant_id,code,name) VALUES('PS-L','demo','PS-L','L'),('PS-O','demo','PS-O','O')`,
		`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('u-m','PS-L','portfolio_manager'),('u-p','PS-L','portfolio_planner'),('u-off','PS-L','portfolio_manager'),('u-m','PS-O','portfolio_manager')`,
		`INSERT INTO projects(id,tenant_id,name,portfolio_scope_id) VALUES('FP-L','demo','L','PS-L'),('FP-X','demo','X','PS-L')`,
		`INSERT INTO project_intakes(id,portfolio_scope_id,tenant_id,intake_code,project_name,project_type,status,formal_project_id) VALUES('I-L','PS-L','demo','I-L','L','T','APPROVED','FP-L')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%v: %s", err, q)
		}
	}
	manager := func(role string) bool { return role == "portfolio_manager" }
	try := func(project, scope, actor string) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		return LockManageableProject(ctx, tx, project, scope, actor, manager)
	}
	if err := try("FP-L", "PS-L", "u-m"); err != nil {
		t.Fatalf("manager refused: %v", err)
	}
	for name, c := range map[string][3]string{
		"planner":                 {"FP-L", "PS-L", "u-p"},
		"disabled manager":        {"FP-L", "PS-L", "u-off"},
		"no membership":           {"FP-L", "PS-L", "u-none"},
		"scope mismatch":          {"FP-L", "PS-O", "u-m"},
		"not an approved project": {"FP-X", "PS-L", "u-m"},
		"unknown project":         {"FP-?", "PS-L", "u-m"},
	} {
		if err := try(c[0], c[1], c[2]); !errors.Is(err, ErrProjectNotManageable) {
			t.Errorf("%s: want ErrProjectNotManageable, got %v", name, err)
		}
	}
}
