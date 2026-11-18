package kcadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/platform"
)

// TestRealKeycloakLeastPrivilege runs only when a real Keycloak with a provisioned
// crane-account-service is available (KCADMIN_REAL_BASE + KCADMIN_REAL_SECRET_FILE). The CI
// fixture and the local Keycloak 26.8.1 run set them; otherwise the test reports SKIP, which the
// gates never count as PASS.
func TestRealKeycloakLeastPrivilege(t *testing.T) {
	base, file := os.Getenv("KCADMIN_REAL_BASE"), os.Getenv("KCADMIN_REAL_SECRET_FILE")
	if base == "" || file == "" {
		t.Skip("real Keycloak not configured")
	}
	secret, err := platform.PrivateSecret(file, 32)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, c := range ProbeLeastPrivilege(ctx, base, "crane", "crane-account-service", secret, &http.Client{Timeout: 10 * time.Second}, escalation(t)) {
		if c.Pass {
			t.Logf("PASS %s", c.Name)
		} else {
			t.Errorf("FAIL %s %s", c.Name, c.Detail)
		}
	}
}

func escalation(t *testing.T) *Escalation {
	e := &Escalation{RealmManagementID: os.Getenv("KCADMIN_REAL_RM_ID")}
	if raw := os.Getenv("KCADMIN_REAL_ESCALATION_ROLES"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &e.Roles); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func TestClientRefusesMasterAndForeignPaths(t *testing.T) {
	if _, err := New("https://x.test/realms/master", "", "c", make([]byte, 32), nil); err == nil {
		t.Fatal("master realm accepted")
	}
	if _, err := New("https://x.test/realms/crane", "http://evil.example", "c", make([]byte, 32), nil); err == nil {
		t.Fatal("untrusted internal base accepted")
	}
	c, err := New("https://x.test/realms/crane", "http://keycloak:8080", "c", make([]byte, 32), nil)
	if err != nil || c.Realm() != "crane" {
		t.Fatal(err)
	}
	for _, p := range []string{"/clients", "/roles", "/users/../clients", "/users/abc/role-mappings", "/", "/users/00000000-0000-4000-8000-000000000001/role-mappings/clients/x"} {
		if allowedPath.MatchString(p) {
			t.Fatal("path allowed:", p)
		}
	}
	u := &User{Username: "service-account-crane-account-service", Attributes: map[string][]string{ManagedAttribute: {"true"}}}
	if u.Managed() {
		t.Fatal("service account treated as managed")
	}
}
