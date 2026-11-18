package personnel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/kcadmin"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

// kcLogin drives the REAL Keycloak login form for crane-bff (Authorization Code) the way a browser
// does. It returns "OK" when Keycloak redirects to the BFF callback with a code, "UPDATE_PASSWORD"
// when it asks for a new password (and newPassword is empty), or "REJECTED".
func kcLogin(t *testing.T, base, username, password, newPassword string) string {
	t.Helper()
	// The crane realm has brute-force protection (quick-login check: two failures within 1 s lock the
	// account for a minute). Deliberate wrong-password probes are spaced so the lock does not mask
	// the behaviour under test.
	time.Sleep(1200 * time.Millisecond)
	jar := map[string]string{}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(method, u string, form url.Values) (int, string, string) {
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		r, _ := http.NewRequest(method, u, body)
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		var cookies []string
		for k, v := range jar {
			cookies = append(cookies, k+"="+v)
		}
		if len(cookies) > 0 {
			r.Header.Set("Cookie", strings.Join(cookies, "; "))
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		for _, c := range resp.Cookies() {
			if c.MaxAge < 0 {
				delete(jar, c.Name)
			} else {
				jar[c.Name] = c.Value
			}
		}
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Location"), string(b)
	}
	redirect := "https://crane.test/auth/callback"
	q := url.Values{"client_id": {"crane-bff"}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {"openid"}, "code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"}, "code_challenge_method": {"S256"}}
	status, loc, page := do("GET", base+"/realms/crane/protocol/openid-connect/auth?"+q.Encode(), nil)
	action := regexp.MustCompile(`action="([^"]+)"`)
	for i := 0; i < 8; i++ {
		for status == 302 || status == 303 {
			if strings.HasPrefix(loc, redirect) {
				if strings.Contains(loc, "code=") {
					return "OK"
				}
				return "REJECTED"
			}
			status, loc, page = do("GET", loc, nil)
		}
		m := action.FindStringSubmatch(page)
		if m == nil {
			return "REJECTED"
		}
		target := strings.ReplaceAll(m[1], "&amp;", "&")
		if strings.Contains(page, "password-new") {
			if newPassword == "" {
				return "UPDATE_PASSWORD"
			}
			status, loc, page = do("POST", target, url.Values{"password-new": {newPassword}, "password-confirm": {newPassword}})
			continue
		}
		if strings.Contains(page, "kc-form-login") && i > 0 {
			return "REJECTED"
		}
		status, loc, page = do("POST", target, url.Values{"username": {username}, "password": {password}, "credentialId": {""}})
	}
	return "REJECTED"
}

// TestRealKeycloakAccountLifecycle: personnel.Service + real PostgreSQL + REAL Keycloak 26.8.1 with
// the restricted account service. Runs when KCADMIN_REAL_BASE / KCADMIN_REAL_SECRET_FILE are set
// (local real run and the CI fixture); SKIP otherwise, which no gate counts as PASS.
func TestRealKeycloakAccountLifecycle(t *testing.T) {
	base, file := os.Getenv("KCADMIN_REAL_BASE"), os.Getenv("KCADMIN_REAL_SECRET_FILE")
	if base == "" || file == "" {
		t.Skip("real Keycloak not configured")
	}
	secret, err := platform.PrivateSecret(file, 32)
	if err != nil {
		t.Fatal(err)
	}
	issuer := base + "/realms/crane"
	idp, err := kcadmin.New(strings.Replace(issuer, "http://", "https://", 1), base, "crane-account-service", secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := setup(t)
	f.svc.IdP, f.svc.Issuer = idp, issuer
	ctx := context.Background()
	// Bootstrap the first administrator from a local row (host path without Keycloak adoption).
	if _, err = f.db.Exec(`INSERT INTO users(id,issuer,subject,display_name,enabled,username) VALUES('root-admin',$1,'00000000-0000-4000-8000-0000000000aa','Root',true,'root-admin')`, issuer); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO system_memberships(user_id,role,granted_by) VALUES('root-admin','system_admin','host:test')`); err != nil {
		t.Fatal(err)
	}
	admin := Actor{ID: "root-admin", Event: auditlog.Event{RequestID: platform.ID()}}
	username := "p1-" + strings.ToLower(platform.ID()[:10])
	r, err := f.svc.ProvisionAccount(ctx, admin, "key-real-"+username, ProvisionRequest{Username: username, Profile: Profile{DisplayName: "真实开户", DepartmentID: "ops", JobTitle: "调度"},
		PortfolioGrants: []PortfolioGrantInput{{ScopeID: "PS1", Role: "portfolio_planner"}}})
	if err != nil || r.State != "COMPLETED" || r.TemporaryPassword == "" {
		t.Fatalf("real provision: %+v %v", r, err)
	}
	t.Log("PASS web account create (real Keycloak user enabled after grants)")
	if _, err = f.svc.ProvisionAccount(ctx, admin, "key-real-dup-"+username, ProvisionRequest{Username: username, Profile: Profile{DisplayName: "重复"}}); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate username must be 409", err)
	}
	// First login: the temporary password forces UPDATE_PASSWORD; after the change the new password works.
	if got := kcLogin(t, base, username, r.TemporaryPassword, ""); got != "UPDATE_PASSWORD" {
		t.Fatal("first login must require a password change, got", got)
	}
	newPassword := "Np-" + platform.ID()[:12] + "x9A"
	if got := kcLogin(t, base, username, r.TemporaryPassword, newPassword); got != "OK" {
		t.Fatal("first login with password change failed:", got)
	}
	if got := kcLogin(t, base, username, newPassword, ""); got != "OK" {
		t.Fatal("login with the changed password failed:", got)
	}
	if got := kcLogin(t, base, username, r.TemporaryPassword, ""); got != "REJECTED" {
		t.Fatal("temporary password still accepted after change:", got)
	}
	t.Log("PASS first login password change")
	// Reset: old (changed) password rejected, new temporary password forces another change.
	reset, err := f.svc.ResetPassword(ctx, admin, username)
	if err != nil || reset.TemporaryPassword == "" {
		t.Fatal(err)
	}
	if got := kcLogin(t, base, username, newPassword, ""); got != "REJECTED" {
		t.Fatal("old password still works after reset:", got)
	}
	if got := kcLogin(t, base, username, reset.TemporaryPassword, ""); got != "UPDATE_PASSWORD" {
		t.Fatal("reset password must require change:", got)
	}
	t.Log("PASS password reset invalidates old password")
	// Disable: Keycloak login refused; enable: login works again with the reset password flow.
	var version int64
	_ = f.db.QueryRow(`SELECT version FROM users WHERE id=$1`, username).Scan(&version)
	out, err := f.svc.SetEnabled(ctx, admin, username, version, false)
	if err != nil || out.IdentitySync != "SYNCED" {
		t.Fatalf("disable %+v %v", out, err)
	}
	if got := kcLogin(t, base, username, reset.TemporaryPassword, ""); got != "REJECTED" {
		t.Fatal("disabled account can still log in:", got)
	}
	if _, err = f.svc.SetEnabled(ctx, admin, username, 0, true); err != nil {
		t.Fatal(err)
	}
	if got := kcLogin(t, base, username, reset.TemporaryPassword, ""); got != "UPDATE_PASSWORD" {
		t.Fatal("re-enabled account login:", got)
	}
	t.Log("PASS disable blocks identity-provider login; enable restores it")
	// Crash between Keycloak create and bind, then retry: one Keycloak user, completed.
	crash := true
	f.svc.Fault = func(step string) error {
		if step == "after_idp_create" && crash {
			crash = false
			return errors.New("simulated crash")
		}
		return nil
	}
	u2 := username + "-r"
	req := ProvisionRequest{Username: u2, Profile: Profile{DisplayName: "恢复测试"}}
	if _, err = f.svc.ProvisionAccount(ctx, admin, "key-real-crash-"+u2, req); err == nil {
		t.Fatal("expected simulated crash")
	}
	if kc, _ := idp.FindByUsername(ctx, u2); kc == nil || *kc.Enabled {
		t.Fatal("after the crash the Keycloak user must exist DISABLED")
	}
	if got := kcLogin(t, base, u2, "anything-1A", ""); got != "REJECTED" {
		t.Fatal("half-opened account can log in")
	}
	r2, err := f.svc.ProvisionAccount(ctx, admin, "key-real-crash-"+u2, req)
	if err != nil || r2.State != "COMPLETED" {
		t.Fatal("retry after crash", err)
	}
	if got := kcLogin(t, base, u2, r2.TemporaryPassword, ""); got != "UPDATE_PASSWORD" {
		t.Fatal("recovered account login:", got)
	}
	t.Log("PASS provision crash recovery (Keycloak success, DB not yet bound) and retry")
	// Host recovery: adopt a pre-existing, unmanaged realm login as system administrator.
	if owner := os.Getenv("KCADMIN_REAL_ADOPT"); owner != "" {
		host := Actor{ID: "real-test", Host: true, Event: auditlog.Event{RequestID: platform.ID()}}
		p, err := f.svc.AdoptAdministrator(ctx, host, owner, "")
		if err != nil || !p.Enabled || len(p.SystemRoles) != 1 || p.Username != owner {
			t.Fatalf("adopt %+v %v", p, err)
		}
		kc, _ := idp.FindByUsername(ctx, owner)
		if kc == nil || !kc.Managed() {
			t.Fatal("adopted user not marked managed")
		}
		if got := kcLogin(t, base, owner, os.Getenv("KCADMIN_REAL_ADOPT_PASSWORD"), ""); got != "OK" {
			t.Fatal("adoption must not change the password:", got)
		}
		t.Log("PASS host adopt-admin keeps the existing password and marks the account managed")
	}
}
