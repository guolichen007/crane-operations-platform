package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

func TestFourRolesAndScopeAreIndependent(t *testing.T) {
	for role, actions := range map[string]map[string]bool{
		"viewer": {"read": true}, "operator": {"read": true, "watch": true},
		"maintainer": {"read": true, "watch": true}, "platform_admin": {"read": true, "watch": true, "admin": true},
		"unknown": {},
	} {
		for _, action := range []string{"read", "watch", "admin", "remote", "plc"} {
			if Allowed(role, action) != actions[action] {
				t.Fatalf("role=%s action=%s", role, action)
			}
		}
	}
}

func TestExplicitSubjectBindingGrantsRevokeAndAudit(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	event := auditlog.Event{Actor: "bootstrap", RequestID: "anonymous-request"}
	if err := Bind(ctx, db, "user-a", "https://issuer.example.test", "sub-a", "Anonymous A", true, event); err != nil {
		t.Fatal(err)
	}
	if err := Bind(ctx, db, "user-b", "https://issuer.other.test", "sub-a", "Anonymous B", true, event); err != nil {
		t.Fatal("issuer+sub must permit same subject at another issuer", err)
	}
	if err := Bind(ctx, db, "user-a", "https://issuer.example.test", "changed", "Anonymous", true, event); err == nil {
		t.Fatal("existing local identity reassigned")
	}
	if _, err := Lookup(ctx, db, "https://issuer.example.test", "unknown"); !errors.Is(err, ErrDenied) {
		t.Fatal("unknown subject accepted")
	}
	if err := Authorize(ctx, db, "user-a", "Project-A", "read"); !errors.Is(err, ErrDenied) {
		t.Fatal("binding granted implicit project access")
	}
	if err := ChangeGrant(ctx, db, "user-a", "Project-A", "platform_admin", false, event); err != nil {
		t.Fatal(err)
	}
	if err := Authorize(ctx, db, "user-a", "Project-A", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,tenant_id,name) VALUES('Project-B','demo','Anonymous B')`); err != nil {
		t.Fatal(err)
	}
	if err := Authorize(ctx, db, "user-a", "Project-B", "read"); !errors.Is(err, ErrDenied) {
		t.Fatal("platform_admin bypassed project scope")
	}
	if err := ChangeGrant(ctx, db, "user-a", "Project-A", "", true, event); err != nil {
		t.Fatal(err)
	}
	if err := Authorize(ctx, db, "user-a", "Project-A", "read"); !errors.Is(err, ErrDenied) {
		t.Fatal("revoke ineffective")
	}
	if _, err := db.Exec(`UPDATE users SET enabled=false WHERE id='user-a'`); err != nil {
		t.Fatal(err)
	}
	if _, err := Lookup(ctx, db, "https://issuer.example.test", "sub-a"); !errors.Is(err, ErrDenied) {
		t.Fatal("disabled user authenticated")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='project_role_changed' AND project_id='Project-A' AND request_id='anonymous-request'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("role changes missing audit", count, err)
	}
	// Failure to write audit must roll back a role change.
	if _, err := db.Exec(`ALTER TABLE audit_logs RENAME TO audit_unavailable`); err != nil {
		t.Fatal(err)
	}
	if err := ChangeGrant(ctx, db, "user-b", "Project-A", "operator", false, event); err == nil {
		t.Fatal("grant succeeded without audit")
	}
	if err := db.QueryRow(`SELECT count(*) FROM project_memberships WHERE user_id='user-b'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unaudited grant committed")
	}
}
