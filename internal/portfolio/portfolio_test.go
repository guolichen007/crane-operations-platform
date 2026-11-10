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
	intake, err := CreateIntake(ctx, db, in, "u-planner")
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
	if _, err := CreateIntake(ctx, db, in, "someone"); err != nil {
		t.Fatal(err)
	}
	in2 := in
	in2.ProjectName = "B"
	if _, err := CreateIntake(ctx, db, in2, "someone"); !errors.Is(err, ErrDuplicateIntakeCode) {
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
	intake, err := CreateIntake(ctx, db, in, "u-manager")
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
