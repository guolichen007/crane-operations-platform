package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
)

// Q3-R1 A: the registration re-checks the ProjectGrant inside its own transaction.
//
// Case 1 (revoke BEFORE the registration takes its locks): the request has already passed the
// HTTP fast-path check; it is paused right before the transaction (a hook on the fast-path
// result is not available, so we revoke between the HTTP check and BeginTx by holding the
// project row with a separate transaction — the registration blocks on it, the revoke commits,
// the lock is released, the registration continues and must be refused).
func TestInventoryRegistrationRevokedBeforeLockIsRefusedHTTP(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, f securityFixture){
		"grant revoked": func(t *testing.T, f securityFixture) {
			mustExec(t, f, `DELETE FROM project_memberships WHERE user_id='user-new' AND project_id='FP-A'`)
		},
		"role downgraded": func(t *testing.T, f securityFixture) {
			mustExec(t, f, `UPDATE project_memberships SET role='viewer' WHERE user_id='user-new' AND project_id='FP-A'`)
		},
		"user disabled": func(t *testing.T, f securityFixture) {
			mustExec(t, f, `UPDATE users SET enabled=false WHERE id='user-new'`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, s := q3Fixture(t)
			nw := s["new"]
			// Hold the project row so the registration's first lock waits.
			hold, err := f.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = hold.Exec(`SELECT 1 FROM projects WHERE id='FP-A' FOR UPDATE`); err != nil {
				t.Fatal(err)
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				done <- f.requestJSON(nw.c, nw.csrf, "/api/v1/projects/FP-A/inventory-registration", "POST", regBody)
			}()
			waitForLockWaiter(t, f, "projects")
			// The request passed the HTTP check (it is now waiting on the row lock). Change the grant.
			change(t, f)
			if err = hold.Rollback(); err != nil {
				t.Fatal(err)
			}
			w := <-done
			if w.Code != 403 {
				t.Fatalf("registration after %s: %d %s", name, w.Code, w.Body.String())
			}
			assertNotRegistered(t, f)
		})
	}
}

// Case 2 (revoke AFTER the registration holds its locks): the revoke must wait for the
// registration; the order is registration → revoke, both succeed, and the revoke applies to the
// already registered project. No deadlock.
func TestInventoryRegistrationRevokeAfterLockSerializesHTTP(t *testing.T) {
	f, s := q3Fixture(t)
	nw := s["new"]
	locked, release := make(chan struct{}), make(chan struct{})
	f.api.registrationAfterLock = func() {
		close(locked)
		<-release
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- f.requestJSON(nw.c, nw.csrf, "/api/v1/projects/FP-A/inventory-registration", "POST", regBody)
	}()
	<-locked
	revoked := make(chan error, 1)
	go func() {
		revoked <- identity.ChangeGrant(context.Background(), f.db, "user-new", "FP-A", "", true, auditlog.Event{Actor: "host:test", RequestID: "revoke-after-lock"})
	}()
	waitForLockWaiter(t, f, "")
	select {
	case err := <-revoked:
		t.Fatalf("revoke did not wait for the registration's locks: %v", err)
	default:
	}
	close(release)
	if w := <-done; w.Code != 200 {
		t.Fatalf("registration holding the locks: %d %s", w.Code, w.Body.String())
	}
	select {
	case err := <-revoked:
		if err != nil {
			t.Fatalf("revoke after registration: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("revoke deadlocked")
	}
	f.api.registrationAfterLock = nil
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action='project_inventory_registered' AND project_id='FP-A'`); n != 1 {
		t.Fatalf("registration audits = %d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM project_memberships WHERE user_id='user-new' AND project_id='FP-A'`); n != 0 {
		t.Fatal("revoke lost")
	}
	// Serialization order is visible in the audit ids: registration first, revoke second.
	if n := f.count(t, `SELECT count(*) FROM audit_logs a JOIN audit_logs b ON a.action='project_inventory_registered' AND b.request_id='revoke-after-lock' AND a.id < b.id`); n != 1 {
		t.Fatal("registration did not commit before the revoke")
	}
}

// The same pause with the Q2 grant API (ChangeGrantGuarded locks project → user) in the second
// transaction: both orders complete, no deadlock (consistent lock order project → user).
func TestInventoryRegistrationAndGuardedGrantNoDeadlockHTTP(t *testing.T) {
	f, s := q3Fixture(t)
	nw, pm := s["new"], s["pm"]
	locked, release := make(chan struct{}), make(chan struct{})
	f.api.registrationAfterLock = func() {
		close(locked)
		<-release
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- f.requestJSON(nw.c, nw.csrf, "/api/v1/projects/FP-A/inventory-registration", "POST", regBody)
	}()
	<-locked
	grant := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		grant <- f.requestJSON(pm.c, pm.csrf, "/api/v1/portfolio/projects/FP-A/members", "POST", grantBody("user-new", nil, rp("platform_admin")))
	}()
	waitForLockWaiter(t, f, "")
	close(release)
	if w := <-done; w.Code != 200 {
		t.Fatalf("registration: %d %s", w.Code, w.Body.String())
	}
	select {
	case w := <-grant:
		if w.Code != 200 {
			t.Fatalf("guarded revoke after registration: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock between registration and ChangeGrantGuarded")
	}
	f.api.registrationAfterLock = nil
}

// Q3-R1 B: the response is built inside the transaction; a failure there rolls everything back
// (nothing written, nothing audited) and the client can safely retry — exactly one registration.
func TestInventoryRegistrationResponseReadFailsRollsBackHTTP(t *testing.T) {
	f, s := q3Fixture(t)
	nw := s["new"]
	f.api.registrationBeforeRead = func() error { return errors.New("injected read failure") }
	if w := f.requestJSON(nw.c, nw.csrf, "/api/v1/projects/FP-A/inventory-registration", "POST", regBody); w.Code != 503 {
		t.Fatalf("injected failure: %d %s", w.Code, w.Body.String())
	}
	assertNotRegistered(t, f)
	f.api.registrationBeforeRead = nil
	w := f.requestJSON(nw.c, nw.csrf, "/api/v1/projects/FP-A/inventory-registration", "POST", regBody)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"assetDeviceCount":0`) || !strings.Contains(w.Body.String(), `"realConnectedCount":0`) {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	if w := f.requestJSON(nw.c, nw.csrf, "/api/v1/projects/FP-A/inventory-registration", "POST", regBody); w.Code != 409 {
		t.Fatalf("second retry: %d", w.Code)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action='project_inventory_registered' AND project_id='FP-A'`); n != 1 {
		t.Fatalf("audits = %d", n)
	}
}

func assertNotRegistered(t *testing.T, f securityFixture) {
	t.Helper()
	if n := f.count(t, `SELECT count(*) FROM projects WHERE id='FP-A' AND lifecycle IS NULL AND admin_code IS NULL AND location_label=''`); n != 1 {
		t.Fatal("project fields changed")
	}
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action='project_inventory_registered'`); n != 0 {
		t.Fatalf("success audit written: %d", n)
	}
}

func mustExec(t *testing.T, f securityFixture, q string) {
	t.Helper()
	if _, err := f.db.Exec(q); err != nil {
		t.Fatalf("%v: %s", err, q)
	}
}

// waitForLockWaiter blocks until some other backend is waiting on a lock (deterministic: no
// sleeps as synchronization, only polling pg_stat_activity).
func waitForLockWaiter(t *testing.T, f securityFixture, _ string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := f.db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&n); err == nil && n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no backend started waiting on a lock")
}
