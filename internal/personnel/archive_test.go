package personnel

import (
	"context"
	"errors"
	"testing"

	"github.com/guolichen007/crane-operations-platform/internal/identity"
)

// R9 A1: archive ("删除人员") — system_admin only; revokes every grant, session, live view and the
// login ability; keeps the users row and every historical reference; hidden by default; frozen.
func TestArchiveRevokesEverythingKeepsHistoryAndIsFrozen(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	r := f.open(t, root, "leaver")
	for _, stmt := range []string{
		`INSERT INTO web_sessions(session_hash,user_id,csrf_hash,created_at,last_seen_at,expires_at,idle_expires_at)
VALUES(decode(repeat('ab',32),'hex'),'leaver',decode(repeat('cd',32),'hex'),now(),now(),now()+interval '1 hour',now()+interval '30 minutes')`,
		`INSERT INTO portfolio_memberships(user_id,portfolio_scope_id,role) VALUES('leaver','PS1','portfolio_planner')`,
		`INSERT INTO projects(id,tenant_id,name) VALUES('P-AR','t1','P-AR')`,
		`INSERT INTO project_memberships(user_id,project_id,role) VALUES('leaver','P-AR','maintainer')`,
		// Business history the person is responsible for.
		`INSERT INTO entity_revisions(project_id,entity_type,entity_id,recorded_at,effective_at,changed_by) VALUES('P-AR','asset','A1',now(),now(),'leaver')`,
	} {
		if _, err := f.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	// An account manager may disable but never delete.
	mgr := f.open(t, root, "acct-mgr", "account_manager")
	mgrActor := Actor{ID: mgr.Person.ID}
	if _, err := f.svc.Archive(ctx, mgrActor, "leaver", 0, "离职", "leaver"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("account manager archive: %v", err)
	}
	// Reason and the typed username are both required.
	if _, err := f.svc.Archive(ctx, root, "leaver", 0, "", "leaver"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing reason: %v", err)
	}
	if _, err := f.svc.Archive(ctx, root, "leaver", 0, "离职", "Leaver2"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong confirmation: %v", err)
	}
	out, err := f.svc.Archive(ctx, root, "leaver", r.Person.Version, "离职", "leaver")
	if err != nil {
		t.Fatal(err)
	}
	if out.SessionsRevoked != 1 || out.IdentitySync != "SYNCED" || f.live["leaver"] != 1 || out.Person.Enabled || out.Person.ArchivedAt == nil ||
		len(out.Revoked.Portfolio) != 1 || len(out.Revoked.Projects) != 1 {
		t.Fatalf("archive result %+v live=%d", out, f.live["leaver"])
	}
	kc, _ := f.idp.FindByUsername(ctx, "leaver")
	if kc == nil || *kc.Enabled {
		t.Fatal("keycloak login still enabled after archive")
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM web_sessions WHERE user_id='leaver'`:                                                                 0,
		`SELECT count(*) FROM portfolio_memberships WHERE user_id='leaver'`:                                                        0,
		`SELECT count(*) FROM project_memberships WHERE user_id='leaver'`:                                                          0,
		`SELECT count(*) FROM users WHERE id='leaver' AND NOT enabled`:                                                             1, // row kept
		`SELECT count(*) FROM entity_revisions WHERE changed_by='leaver'`:                                                          1, // history intact
		`SELECT count(*) FROM audit_logs WHERE action='personnel_archived' AND target_user_id='leaver' AND change->>'reason'='离职'`: 1,
		`SELECT count(*) FROM audit_logs WHERE action='project_role_changed' AND project_id='P-AR' AND result='revoked:leaver'`:    1,
	} {
		if n := count(t, f.db, q); n != want {
			t.Fatalf("%s = %d, want %d", q, n, want)
		}
	}
	if _, err := identity.ProjectRole(ctx, f.db, "leaver", "P-AR"); !errors.Is(err, identity.ErrDenied) {
		t.Fatal("archived person keeps project access")
	}
	// Hidden by default, visible with status=ARCHIVED.
	list, err := f.svc.List(ctx, root, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list.Items {
		if p.ID == "leaver" {
			t.Fatal("archived person in the default list")
		}
	}
	archived, _ := f.svc.List(ctx, root, ListFilter{Status: "ARCHIVED"})
	if archived.Total != 1 || archived.Items[0].ID != "leaver" {
		t.Fatalf("archived filter %+v", archived)
	}
	// Frozen: no enable, no reset, no role, no profile edit, no project grant, no re-adopt.
	if _, err := f.svc.SetEnabled(ctx, root, "leaver", 0, true); !errors.Is(err, ErrArchived) {
		t.Fatalf("enable archived: %v", err)
	}
	if _, err := f.svc.ResetPassword(ctx, root, "leaver"); !errors.Is(err, ErrArchived) {
		t.Fatalf("reset archived: %v", err)
	}
	if _, err := f.svc.SetPortfolioRole(ctx, root, "leaver", "PS1", "portfolio_planner", ""); !errors.Is(err, ErrArchived) {
		t.Fatalf("grant archived: %v", err)
	}
	if _, err := f.svc.UpdateProfile(ctx, root, "leaver", 0, Profile{DisplayName: "x"}); !errors.Is(err, ErrArchived) {
		t.Fatalf("edit archived: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE users SET enabled=true WHERE id='leaver'`); err == nil {
		t.Fatal("database allowed re-enabling an archived person")
	}
	if _, err := f.svc.AdoptAdministrator(ctx, f.host, "leaver", ""); !errors.Is(err, ErrArchived) {
		t.Fatalf("adopt archived: %v", err)
	}
	// Referenced by business history: purge refused, person stays archived.
	if err := f.svc.Purge(ctx, root, "leaver", "leaver"); !errors.Is(err, ErrReferenced) {
		t.Fatalf("purge referenced: %v", err)
	}
	if n := count(t, f.db, `SELECT count(*) FROM users WHERE id='leaver'`); n != 1 {
		t.Fatal("referenced person deleted")
	}
}

func TestArchiveGuardsSelfLastAdminProtectedAndUnfinishedOpening(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	if _, err := f.svc.Archive(ctx, root, root.ID, 0, "x", "root-admin"); !errors.Is(err, ErrSelf) {
		t.Fatalf("self archive: %v", err)
	}
	// Last enabled system_admin (host actor bypasses ErrSelf, not the last-admin rule).
	if _, err := f.svc.Archive(ctx, f.host, root.ID, 0, "x", "root-admin"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last admin archive: %v", err)
	}
	// OWNER-protected identity: never archived, even by another admin or the host.
	owner := f.admin(t, "owner-admin")
	if err := f.svc.SetProtected(ctx, f.host, owner.ID, "OWNER"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Archive(ctx, root, owner.ID, 0, "x", "owner-admin"); !errors.Is(err, ErrProtectedIdentity) {
		t.Fatalf("protected archive: %v", err)
	}
	if err := f.svc.SetProtected(ctx, root, owner.ID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("web actor changed protection")
	}
	// Second admin can now be archived by root (another enabled admin remains).
	other := f.admin(t, "other-admin")
	if _, err := f.svc.Archive(ctx, root, other.ID, 0, "测试结束", "other-admin"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, f.db, `SELECT count(*) FROM system_memberships WHERE user_id=$1`, other.ID); n != 0 {
		t.Fatal("system role survived archive")
	}
}

// Keycloak down during archive: the database side is final (no login possible: users.enabled is
// false and the fence moved); identitySync=PENDING; repeating the archive re-runs only the sync.
func TestArchiveIdentityProviderDownIsRetryable(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	f.open(t, root, "kc-down")
	f.idp.Down = true
	out, err := f.svc.Archive(ctx, root, "kc-down", 0, "离职", "kc-down")
	if err != nil || out.IdentitySync != "PENDING" || out.Person.ArchivedAt == nil {
		t.Fatalf("archive with keycloak down %+v %v", out, err)
	}
	f.idp.Down = false
	again, err := f.svc.Archive(ctx, root, "kc-down", 0, "离职", "kc-down")
	if err != nil || again.IdentitySync != "SYNCED" {
		t.Fatalf("retry %+v %v", again, err)
	}
	if kc, _ := f.idp.FindByUsername(ctx, "kc-down"); *kc.Enabled {
		t.Fatal("keycloak still enabled after retry")
	}
	if _, err := f.svc.Archive(ctx, root, "kc-down", 0, "离职", "kc-down"); !errors.Is(err, ErrArchived) {
		t.Fatalf("third archive: %v", err)
	}
}

// Permanent purge: archived + unreferenced test accounts only; Keycloak user deleted; username
// reserved forever; audit kept. Never cascading.
func TestPurgeOnlyArchivedUnreferencedAndReservesUsername(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	f.open(t, root, "tmp-tester")
	if err := f.svc.Purge(ctx, root, "tmp-tester", "tmp-tester"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("purge before archive: %v", err)
	}
	if _, err := f.svc.Archive(ctx, root, "tmp-tester", 0, "测试账号清理", "tmp-tester"); err != nil {
		t.Fatal(err)
	}
	chk, err := f.svc.CheckPurge(ctx, root, "tmp-tester")
	if err != nil || !chk.Purgeable {
		t.Fatalf("check %+v %v", chk, err)
	}
	kc, _ := f.idp.FindByUsername(ctx, "tmp-tester")
	if err := f.svc.Purge(ctx, root, "tmp-tester", "tmp-tester"); err != nil {
		t.Fatal(err)
	}
	if u, _ := f.idp.Get(ctx, kc.ID); u != nil {
		t.Fatal("keycloak user survived purge")
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM users WHERE id='tmp-tester'`:                                                  0,
		`SELECT count(*) FROM purged_usernames WHERE username='tmp-tester'`:                                 1,
		`SELECT count(*) FROM account_provisions WHERE username='tmp-tester' AND state='PURGED'`:            1,
		`SELECT count(*) FROM account_provisions WHERE username='tmp-tester' AND request::text LIKE '%员工%'`: 0,
		`SELECT count(*) FROM audit_logs WHERE target_user_id='tmp-tester' AND action='personnel_purged'`:   1,
		`SELECT count(*) FROM audit_logs WHERE target_user_id='tmp-tester' AND action='personnel_created'`:  1, // history kept
	} {
		if n := count(t, f.db, q); n != want {
			t.Fatalf("%s = %d, want %d", q, n, want)
		}
	}
	// The username can never be given to a new person (its audit history keeps that id).
	if _, err := f.svc.ProvisionAccount(ctx, root, "key-reuse-0001", ProvisionRequest{Username: "tmp-tester", Profile: Profile{DisplayName: "新人"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("reuse of a purged username: %v", err)
	}
}

// Cleanup works only from an explicit (id, username) list; plan is read-only; apply needs the
// approved digest; unknown / mismatched / protected / admin entries refuse the whole apply.
func TestCleanupExplicitListOnly(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	root := f.admin(t, "root-admin")
	f.open(t, root, "uat-a")
	f.open(t, root, "uat-b")
	f.open(t, root, "uat-keep") // similar name, NOT listed: must be untouched
	if _, err := f.db.Exec(`INSERT INTO entity_revisions(project_id,entity_type,entity_id,recorded_at,effective_at,changed_by) SELECT id,'x','y',now(),now(),'uat-b' FROM projects LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	bad := CleanupList{Reason: "UAT 清理", Accounts: []CleanupEntry{{ID: "uat-a", Username: "uat-a"}, {ID: "uat-a2", Username: "uat-a2"}}}
	plan, err := f.svc.PlanCleanup(ctx, bad)
	if err != nil || plan.Items[1].Action != CleanupRefused {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if _, err = f.svc.ApplyCleanup(ctx, f.host, bad, plan.Digest); !errors.Is(err, ErrConflict) {
		t.Fatalf("refused entries must block apply: %v", err)
	}
	list := CleanupList{Reason: "UAT 清理", Accounts: []CleanupEntry{{ID: "uat-a", Username: "uat-a"}, {ID: "uat-b", Username: "uat-b"}}}
	plan, err = f.svc.PlanCleanup(ctx, list)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Items[0].Action != CleanupArchiveAndPurge || plan.Items[1].Action != CleanupArchiveOnly {
		t.Fatalf("classification %+v", plan.Items)
	}
	if _, err = f.svc.ApplyCleanup(ctx, f.host, list, "0000"); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong digest: %v", err)
	}
	if _, err = f.svc.ApplyCleanup(ctx, root, list, plan.Digest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("web actor apply: %v", err)
	}
	if _, err = f.svc.ApplyCleanup(ctx, f.host, list, plan.Digest); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM users WHERE id='uat-a'`:                                        0,
		`SELECT count(*) FROM users WHERE id='uat-b' AND archived_at IS NOT NULL`:            1,
		`SELECT count(*) FROM users WHERE id='uat-keep' AND archived_at IS NULL AND enabled`: 1,
	} {
		if n := count(t, f.db, q); n != want {
			t.Fatalf("%s = %d, want %d", q, n, want)
		}
	}
}
