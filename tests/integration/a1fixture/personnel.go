package main

// P1 personnel on the real stack (Keycloak 26.8.1 + PostgreSQL + production cloud-api image):
//   probe-account-service  least-privilege proof of crane-account-service against the real realm
//                          (the bootstrap credential is used ONLY to look up role representations
//                          for the escalation probe, never handed to the application);
//   seed-personnel         makes the existing b4-admin login a managed system_admin through the
//                          production host recovery path (personnel.AdoptAdministrator) and adds a
//                          department; b4-admin keeps its project role (system role ≠ project role);
//   verify-personnel-state asserts the PostgreSQL end state left by tests/e2e/b4/personnel.spec.ts.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/kcadmin"
	"github.com/guolichen007/crane-operations-platform/internal/personnel"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

func fixtureDB() (*sql.DB, error) {
	password, err := platform.PrivateSecret("/fixture/secrets/app_db_password", 32)
	if err != nil {
		return nil, err
	}
	dsn, err := platform.DatabaseWithPassword("postgres://crane_app@postgres:5432/crane_app?sslmode=disable", password)
	if err != nil {
		return nil, err
	}
	return sql.Open("pgx", dsn)
}

func bootstrapToken(ctx context.Context) (string, error) {
	password, err := platform.PrivateSecret("/fixture/secrets/keycloak_bootstrap_password", 32)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"password"}, "client_id": {"admin-cli"}, "username": {"bootstrap-crane"}, "password": {string(password)}}
	r, _ := http.NewRequestWithContext(ctx, "POST", "http://keycloak:8080/realms/master/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var t struct {
		AccessToken string `json:"access_token"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&t) != nil {
		return "", errors.New("bootstrap token rejected")
	}
	return t.AccessToken, nil
}

func adminGet(ctx context.Context, token, path string, out any) error {
	r, _ := http.NewRequestWithContext(ctx, "GET", "http://keycloak:8080/admin/realms/crane"+path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("admin GET %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func probeAccountService(ctx context.Context) error {
	secret, err := platform.PrivateSecret("/fixture/secrets/account_service_secret", 32)
	if err != nil {
		return err
	}
	token, err := bootstrapToken(ctx)
	if err != nil {
		return err
	}
	var clients []struct {
		ID string `json:"id"`
	}
	if err = adminGet(ctx, token, "/clients?clientId=realm-management", &clients); err != nil || len(clients) != 1 {
		return errors.New("realm-management lookup")
	}
	esc := &kcadmin.Escalation{RealmManagementID: clients[0].ID}
	for _, name := range []string{"realm-admin", "manage-realm", "manage-clients"} {
		var role map[string]any
		if err = adminGet(ctx, token, "/clients/"+clients[0].ID+"/roles/"+name, &role); err != nil {
			return err
		}
		esc.Roles = append(esc.Roles, map[string]any{"id": role["id"], "name": role["name"], "clientRole": true, "containerId": clients[0].ID})
	}
	failed := 0
	for _, c := range kcadmin.ProbeLeastPrivilege(ctx, "http://keycloak:8080", "crane", "crane-account-service", secret, &http.Client{Timeout: 10 * time.Second}, esc) {
		state := "PASS"
		if !c.Pass {
			state, failed = "FAIL", failed+1
		}
		fmt.Printf("ACCOUNT_SERVICE_PROBE %s %s\n", state, c.Name)
	}
	if failed > 0 {
		return fmt.Errorf("%d least-privilege checks failed", failed)
	}
	fmt.Println("P1_ACCOUNT_SERVICE_LEAST_PRIVILEGE=PASS KEYCLOAK=REAL")
	return nil
}

func seedPersonnel(ctx context.Context) error {
	db, err := fixtureDB()
	if err != nil {
		return err
	}
	defer db.Close()
	secret, err := platform.PrivateSecret("/fixture/secrets/account_service_secret", 32)
	if err != nil {
		return err
	}
	issuer := os.Getenv("OIDC_ISSUER")
	idp, err := kcadmin.New(issuer, "http://keycloak:8080", "crane-account-service", secret, nil)
	if err != nil {
		return err
	}
	svc := &personnel.Service{DB: db, IdP: idp, Issuer: issuer}
	host := personnel.Actor{ID: "b4-fixture", Host: true, Event: auditlog.Event{RequestID: platform.ID()}}
	if _, err = svc.AdoptAdministrator(ctx, host, "b4-admin", "B4 系统管理员"); err != nil {
		return err
	}
	if _, err = svc.SaveDepartment(ctx, host, personnel.Department{ID: "b4-ops", Name: "B4 运维部"}); err != nil && !errors.Is(err, personnel.ErrConflict) {
		return err
	}
	fmt.Println("P1_PERSONNEL_SEED=READY system_admin=b4-admin")
	return nil
}

func verifyPersonnelState(ctx context.Context, runID string) error {
	db, err := fixtureDB()
	if err != nil {
		return err
	}
	defer db.Close()
	user := "p1-" + strings.ToLower(runID)
	var enabled bool
	var username, dept sql.NullString
	if err = db.QueryRowContext(ctx, `SELECT enabled, username, department_id FROM users WHERE id=$1`, user).Scan(&enabled, &username, &dept); err != nil {
		return fmt.Errorf("personnel %s: %w", user, err)
	}
	if !username.Valid || username.String != user || dept.String != "b4-ops" {
		return errors.New("personnel profile not persisted")
	}
	need := map[string]int{}
	rows, err := db.QueryContext(ctx, `SELECT action, count(*) FROM audit_logs WHERE target_user_id=$1 AND request_id<>'' GROUP BY action`, user)
	if err != nil {
		return err
	}
	for rows.Next() {
		var a string
		var n int
		if err = rows.Scan(&a, &n); err != nil {
			return err
		}
		need[a] = n
	}
	rows.Close()
	for _, a := range []string{"personnel_created", "account_disabled", "password_reset", "portfolio_role_changed"} {
		if need[a] < 1 {
			return fmt.Errorf("audit %s missing", a)
		}
	}
	var leaked int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM audit_logs WHERE change::text ~* 'password"\s*:\s*"[^i]'`).Scan(&leaked); err != nil || leaked != 0 {
		return errors.New("credential-like value in audit change")
	}
	var admins int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM system_memberships m JOIN users u ON u.id=m.user_id WHERE m.role='system_admin' AND u.enabled`).Scan(&admins); err != nil || admins < 1 {
		return errors.New("no enabled system administrator remains")
	}
	fmt.Println("P1_PERSONNEL_DB_STATE=VERIFIED user=" + user)
	return nil
}
