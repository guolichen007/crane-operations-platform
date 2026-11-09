package app

// H2-1C HTTP acceptance for GET /api/v1/operations/summary through the real stack
// (OIDC web session, project grants, real PostgreSQL, embedded geo index).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/geo"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
)

// decodeSummary enforces the generated contract on the raw response body: unknown or missing
// fields fail (DisallowUnknownFields + contract.ValidateDocument on a re-marshal of the raw map
// would hide omitted fields, so the raw object is validated key by key).
var samples int

func decodeSummary(t *testing.T, body []byte) contract.OperationsSummary {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schemaVersion", "sampledAt", "generatedAt", "scope", "freshness", "metrics", "regions"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("response missing %s", key)
		}
	}
	// Optional export of real responses for the JSON Schema (ajv 2020) check in
	// tests/contract/operations-summary-contract.test.mjs (OPS_SUMMARY_SAMPLES_DIR).
	if dir := os.Getenv("OPS_SUMMARY_SAMPLES_DIR"); dir != "" {
		samples++
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("summary-%02d.json", samples)), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var s contract.OperationsSummary
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		t.Fatalf("response not the generated contract: %v", err)
	}
	if err := contract.ValidateDocument("OperationsSummary", s); err != nil {
		t.Fatalf("response violates generated schema: %v", err)
	}
	return s
}

func TestOperationsSummaryHTTP(t *testing.T) {
	f := newSecurityFixture(t)
	// Locate the example projects: P001 Haining (county), P002 Shenzhen (prefecture).
	if _, err := f.db.Exec(`UPDATE projects SET admin_code=330481 WHERE id='P001'; UPDATE projects SET admin_code=440300 WHERE id='P002'`); err != nil {
		t.Fatal(err)
	}
	if err := identity.Bind(t.Context(), f.db, "user-nogrant", f.provider.Server.URL, "subject-nogrant", "Anonymous nogrant", true, auditlog.Event{Actor: "fixture"}); err != nil {
		t.Fatal(err)
	}
	a, sa := f.login(t, "a")           // viewer P001
	admin, sadm := f.login(t, "admin") // platform_admin P001 only
	none, snone := f.login(t, "nogrant")
	get := func(c *http.Cookie, csrf, path string) (int, []byte, http.Header) {
		w := f.request(c, csrf, path, "GET")
		return w.Code, w.Body.Bytes(), w.Header()
	}

	// 200: viewer and platform_admin see only P001; every number from the generated contract.
	for _, who := range []struct {
		name string
		c    *http.Cookie
		csrf string
	}{{"viewer", a, sa.CsrfToken}, {"platform_admin", admin, sadm.CsrfToken}} {
		code, body, h := get(who.c, who.csrf, "/api/v1/operations/summary")
		if code != 200 || h.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s %v", who.name, code, body, h)
		}
		s := decodeSummary(t, body)
		if s.Scope.AuthorizedProjects != 1 || s.Metrics.Projects != 1 || s.Metrics.Assets.Registered == 0 || strings.Contains(string(body), "P002") {
			t.Fatalf("%s scope %+v metrics %+v", who.name, s.Scope, s.Metrics.Assets)
		}
		if len(s.Regions) != 1 || s.Regions[0].Adcode != 330000 {
			t.Fatalf("%s regions %+v", who.name, s.Regions)
		}
	}
	// Drill into Guangdong / Shenzhen (only unauthorized P002 there): 200 with legal zeros, nothing leaked.
	for _, region := range []string{"440000", "440300", "440305"} {
		code, body, _ := get(admin, sadm.CsrfToken, "/api/v1/operations/summary?region="+region)
		s := decodeSummary(t, body)
		if code != 200 || s.Metrics.Projects != 0 || s.Metrics.Assets.Registered != 0 || s.Metrics.Alarm.Records.Total != 0 || len(s.Regions) != 0 || strings.Contains(string(body), "P002") {
			t.Fatalf("region %s: %d %s", region, code, body)
		}
		if s.Scope.Region == nil || *s.Scope.Region == 0 || s.Scope.AuthorizedProjects != 1 {
			t.Fatalf("region %s scope %+v", region, s.Scope)
		}
	}
	// Drill into Zhejiang → Jiaxing → Haining never widens P001.
	for _, region := range []string{"330000", "330400", "330481"} {
		_, body, _ := get(a, sa.CsrfToken, "/api/v1/operations/summary?region="+region+"&alarmWindow=P7D")
		s := decodeSummary(t, body)
		if s.Metrics.Projects != 1 || s.Freshness.AlarmWindow != "P7D" {
			t.Fatalf("region %s %+v", region, s.Metrics)
		}
	}
	// country code 100000 == whole scope.
	if _, body, _ := get(a, sa.CsrfToken, "/api/v1/operations/summary?region=100000"); decodeSummary(t, body).Scope.Region != nil {
		t.Fatal("100000 should be the whole scope")
	}
	// No grant: legal empty result.
	code, body, _ := get(none, snone.CsrfToken, "/api/v1/operations/summary")
	if s := decodeSummary(t, body); code != 200 || s.Scope.AuthorizedProjects != 0 || s.Metrics.Assets.Registered != 0 {
		t.Fatalf("no grant: %d %s", code, body)
	}

	// 400: malformed parameters (no statistics computed).
	for _, q := range []string{"?region=33", "?region=abcdef", "?region=3300000", "?alarmWindow=P30D", "?alarmWindow=", "?project=P002", "?status=ALARM", "?source=real", "?region=330000&region=440000"} {
		if code, body, _ := get(a, sa.CsrfToken, "/api/v1/operations/summary"+q); code != 400 || strings.Contains(string(body), "metrics") {
			t.Errorf("%s: %d %s", q, code, body)
		}
	}
	// 404: well-formed but unknown region.
	if code, body, _ := get(a, sa.CsrfToken, "/api/v1/operations/summary?region=999999"); code != 404 || !strings.Contains(string(body), "region_not_found") {
		t.Errorf("unknown region: %d %s", code, body)
	}
	// 401: no session / invalid session cookie.
	if code, _, _ := get(nil, "", "/api/v1/operations/summary"); code != 401 {
		t.Errorf("no session: %d", code)
	}
	if code, _, _ := get(&http.Cookie{Name: "__Host-crane_session", Value: strings.Repeat("x", 43)}, "", "/api/v1/operations/summary"); code != 401 {
		t.Errorf("forged session: %d", code)
	}
	// Disabled user: the session is rejected by the identity policy.
	if _, err := f.db.Exec(`UPDATE users SET enabled=false WHERE id='user-a'`); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := get(a, sa.CsrfToken, "/api/v1/operations/summary"); code != 401 {
		t.Errorf("disabled user: %d %s", code, body)
	}
	if _, err := f.db.Exec(`UPDATE users SET enabled=true WHERE id='user-a'`); err != nil {
		t.Fatal(err)
	}

	// 503: administrative index unavailable — never a partial or zero summary.
	f.api.geo = func() (*geo.Index, error) { return nil, geo.ErrUnavailable }
	if code, body, _ := get(admin, sadm.CsrfToken, "/api/v1/operations/summary"); code != 503 || strings.Contains(string(body), "metrics") {
		t.Errorf("geo unavailable: %d %s", code, body)
	}
	f.api.geo = nil
	// 503: statistics query failure (schema drift) — whole response fails.
	if _, err := f.db.Exec(`ALTER TABLE device_shadow RENAME TO device_shadow_moved`); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := get(admin, sadm.CsrfToken, "/api/v1/operations/summary"); code != 503 || strings.Contains(string(body), "metrics") {
		t.Errorf("query failure: %d %s", code, body)
	}
	if _, err := f.db.Exec(`ALTER TABLE device_shadow_moved RENAME TO device_shadow`); err != nil {
		t.Fatal(err)
	}
	// 503: authorization lookup failure (memberships unavailable).
	if _, err := f.db.Exec(`ALTER TABLE project_memberships RENAME TO project_memberships_moved`); err != nil {
		t.Fatal(err)
	}
	code, body, _ = get(admin, sadm.CsrfToken, "/api/v1/operations/summary")
	if _, err := f.db.Exec(`ALTER TABLE project_memberships_moved RENAME TO project_memberships`); err != nil {
		t.Fatal(err)
	}
	t.Logf("authorization unavailable → %d", code)
	if code != 503 || strings.Contains(string(body), "metrics") {
		t.Errorf("authorization unavailable: %d %s", code, body)
	}
	// Recovery: the same request succeeds again.
	if code, body, _ := get(admin, sadm.CsrfToken, "/api/v1/operations/summary"); code != 200 {
		t.Fatalf("recovery: %d %s", code, body)
	}

	// Review asset ids open in the asset center within the same authorization.
	if _, err := f.db.Exec(`UPDATE asset_devices SET asset_status='DRAFT' WHERE id='ASSET-P001-01'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO devices(id,project_id,asset_device_id,source_mode) VALUES('EDGE-P001-REVIEW','P001','ASSET-P001-01','REAL');
	  INSERT INTO device_shadow(device_id,payload_json,updated_at,version) VALUES('EDGE-P001-REVIEW', json_build_object('presence', json_build_object('connectivity','MQTT_CONNECTED','lastSeenAt', to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')),'reported', json_build_object('alarmState','NONE','operatingState','SILENT')), now(), 1)`); err != nil {
		t.Fatal(err)
	}
	_, body, _ = get(a, sa.CsrfToken, "/api/v1/operations/summary")
	s := decodeSummary(t, body)
	if s.Metrics.Real.PendingReview != 1 || len(s.Metrics.Real.PendingReviewAssets) != 1 {
		t.Fatalf("review %+v", s.Metrics.Real)
	}
	if code, _, _ := get(a, sa.CsrfToken, "/api/v1/assets/"+s.Metrics.Real.PendingReviewAssets[0].Id); code != 200 {
		t.Fatalf("review asset not openable in asset center: %d", code)
	}
}

// runStartup calls the real Cloud entry point in a controlled environment and never lets it start serving:
// APP_ENV is set to a value ValidateCloud rejects, so a Run that passes the OPS_* and administrative-index
// checks stops at the very next startup check. The ambient environment (the integration job runs this
// binary inside the cloud-api container with a migrated DATABASE_URL, certificates and a dev token) must
// not decide the outcome: before H2-1C-R2 the in-range case started the real server there and hung the job.
// A 10 s watchdog turns any regression of that kind into a fast FAIL instead of a CI timeout.
func runStartup(t *testing.T, env map[string]string) error {
	t.Helper()
	for k, v := range map[string]string{"APP_CONFIG": "", "APP_ENV": "h21c-startup-test-invalid", "DATABASE_URL": "", "DEV_API_TOKEN": "",
		"FLEET_FILE": "", "INVENTORY_FILE": "", "HTTP_ADDR": "127.0.0.1:0", "OPS_STALE_AFTER_SECONDS": "", "OPS_MAX_FUTURE_SKEW_SECONDS": ""} {
		t.Setenv(k, v)
	}
	os.Unsetenv("OPS_STALE_AFTER_SECONDS")
	os.Unsetenv("OPS_MAX_FUTURE_SKEW_SECONDS")
	for k, v := range env {
		t.Setenv(k, v)
	}
	done := make(chan error, 1)
	go func() { done <- Run(nil) }()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10 s: the startup test reached a serving cloud-api")
		return nil
	}
}

func TestOperationsConfigRefusesOutOfRangeAtStartup(t *testing.T) {
	for _, env := range []map[string]string{
		{"OPS_STALE_AFTER_SECONDS": "299"}, {"OPS_STALE_AFTER_SECONDS": "7201"}, {"OPS_STALE_AFTER_SECONDS": "abc"},
		{"OPS_MAX_FUTURE_SKEW_SECONDS": "-1"}, {"OPS_MAX_FUTURE_SKEW_SECONDS": "601"}, {"OPS_MAX_FUTURE_SKEW_SECONDS": "1.5"},
	} {
		t.Run(strings.Join(func() []string {
			out := []string{}
			for k, v := range env {
				out = append(out, k+"="+v)
			}
			return out
		}(), ","), func(t *testing.T) {
			err := runStartup(t, env)
			if err == nil || !strings.Contains(err.Error(), "OPS_") {
				t.Fatalf("Run accepted %v: %v", env, err)
			}
		})
	}
	// In-range (and absent) values pass the OPS_* and administrative-index checks; Run then stops at the
	// next startup check (the deliberately invalid APP_ENV), proving it got past OPS without serving.
	for _, env := range []map[string]string{{}, {"OPS_STALE_AFTER_SECONDS": "300", "OPS_MAX_FUTURE_SKEW_SECONDS": "600"},
		{"OPS_STALE_AFTER_SECONDS": "7200", "OPS_MAX_FUTURE_SKEW_SECONDS": "0"}} {
		err := runStartup(t, env)
		if err == nil || strings.Contains(err.Error(), "OPS_") || !strings.Contains(err.Error(), "invalid Cloud environment") {
			t.Fatalf("in-range %v: want the next startup check (invalid Cloud environment), got %v", env, err)
		}
	}
}
