package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

// R7 F-09: a duplicate asset code inside one project is a 409 business conflict (CREATE,
// PATCH and two concurrent CREATEs), never a 503; the same code in another project stays
// legal; a refused write has no revision/audit side effect; idempotent replay is unchanged.
func TestR7AssetCodeDuplicateHTTP(t *testing.T) {
	f := newSecurityFixture(t)
	admin, ai := f.login(t, "admin")
	create := func(project, code, key string) (int, string) {
		h := map[string]string{}
		if key != "" {
			h["Idempotency-Key"] = key
		}
		w := f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/assets", h, map[string]any{"projectId": project, "assetType": "CRANE", "assetCode": code})
		return w.Code, w.Body.String()
	}
	counts := func() (int, int, int) {
		var a, r, l int
		_ = f.db.QueryRow(`SELECT count(*) FROM asset_devices WHERE project_id='P001'`).Scan(&a)
		_ = f.db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE entity_type='asset'`).Scan(&r)
		_ = f.db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action IN ('asset_created','asset_updated') AND result='success'`).Scan(&l)
		return a, r, l
	}
	if c, b := create("P001", "R7-DUP-1", ""); c != 201 {
		t.Fatalf("first create = %d %s", c, b)
	}
	a0, r0, l0 := counts()
	if c, b := create("P001", "R7-DUP-1", ""); c != 409 {
		t.Fatalf("duplicate create = %d %s, want 409", c, b)
	}
	if c, b := create("P001", "  R7-DUP-1  ", ""); c != 409 {
		t.Fatalf("duplicate create (whitespace) = %d %s, want 409", c, b)
	}
	if a, r, l := counts(); a != a0 || r != r0 || l != l0 {
		t.Fatalf("refused duplicate wrote rows: %d/%d/%d → %d/%d/%d", a0, r0, l0, a, r, l)
	}
	// Idempotent replay of the original create still returns the original 201 result.
	if c, _ := create("P001", "R7-IDEM", "k-r7-1"); c != 201 {
		t.Fatal("idempotent first create failed")
	}
	if c, b := create("P001", "R7-IDEM", "k-r7-1"); c != 201 {
		t.Fatalf("idempotent replay = %d %s, want 201", c, b)
	}
	// A different payload under the same key is still the idempotency 409.
	if c, _ := create("P001", "R7-IDEM-X", "k-r7-1"); c != 409 {
		t.Fatalf("idempotency key reuse = %d, want 409", c)
	}

	// PATCH to an existing code → 409, the row keeps its old code and version.
	w := f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/assets", nil, map[string]any{"projectId": "P001", "assetType": "CRANE", "assetCode": "R7-DUP-2"})
	second := decodeJSON[contract.AssetMasterData](t, w.Body.Bytes())
	p := f.doBody(admin, ai.CsrfToken, "PATCH", "/api/v1/assets/"+second.Id, map[string]string{"If-Match": strconv.FormatInt(second.Version, 10)}, map[string]any{"assetCode": "R7-DUP-1"})
	if p.Code != 409 {
		t.Fatalf("PATCH to duplicate code = %d %s, want 409", p.Code, p.Body.String())
	}
	var code string
	var version int64
	if err := f.db.QueryRow(`SELECT asset_code, version FROM asset_devices WHERE id=$1`, second.Id).Scan(&code, &version); err != nil || code != "R7-DUP-2" || version != second.Version {
		t.Fatalf("refused PATCH changed the row: %s v%d", code, version)
	}

	// Concurrent creates of one new code: exactly one 201, the rest 409, one row.
	var wg sync.WaitGroup
	codes := make([]int, 6)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = create("P001", "R7-RACE", "")
		}(i)
	}
	wg.Wait()
	created, conflicts := 0, 0
	for _, c := range codes {
		switch c {
		case 201:
			created++
		case 409:
			conflicts++
		default:
			t.Fatalf("concurrent create status %d (all: %v)", c, codes)
		}
	}
	var rows int
	_ = f.db.QueryRow(`SELECT count(*) FROM asset_devices WHERE project_id='P001' AND asset_code='R7-RACE'`).Scan(&rows)
	if created != 1 || conflicts != len(codes)-1 || rows != 1 {
		t.Fatalf("concurrent create: created=%d conflicts=%d rows=%d", created, conflicts, rows)
	}

	// The same code in another project is legal (uniqueness is per project).
	if _, err := f.db.Exec(`INSERT INTO asset_devices(id,project_id,display_name,lifecycle,asset_code,asset_type,asset_status,version) VALUES('r7-p2-dup','P002','x','PLANNED','R7-DUP-1','CRANE','ACTIVE',1)`); err != nil {
		t.Fatalf("same code in another project rejected by the database: %v", err)
	}

	// A real database failure is still 503: the asset table is made unwritable.
	if _, err := f.db.Exec(`CREATE OR REPLACE FUNCTION r7_fail() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'injected outage'; END $$ LANGUAGE plpgsql; CREATE TRIGGER r7_fail BEFORE INSERT ON asset_devices FOR EACH ROW EXECUTE FUNCTION r7_fail()`); err != nil {
		t.Fatal(err)
	}
	if c, _ := create("P001", "R7-OUTAGE", ""); c != http.StatusServiceUnavailable {
		t.Fatalf("injected database failure = %d, want 503", c)
	}
}

// r7AssetTypes is the authoritative list as the 资产台账 create form offers it
// (apps/web-console/src/views/AssetCenter.vue typeOptions before R7).
var r7AssetTypes = []string{"CRANE", "GANTRY", "BRIDGE", "PORTAL", "JIB"}

// R7 F-10: assetType is one of the authoritative asset types on CREATE and on any PATCH that
// changes it; an Equipment type code is not an asset type; a legacy row with a non-standard
// type stays readable and editable as long as the client does not change the type.
func TestR7AssetTypeConstraintHTTP(t *testing.T) {
	f := newSecurityFixture(t)
	admin, ai := f.login(t, "admin")
	n := 0
	create := func(assetType string) int {
		n++
		return f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/assets", nil, map[string]any{"projectId": "P001", "assetType": assetType, "assetCode": "R7-T-" + strconv.Itoa(n)}).Code
	}
	for _, bad := range []string{"MOTOR", "PLC", "crane", "ANYTHING", "门机"} {
		if c := create(bad); c != 422 {
			t.Errorf("CREATE assetType %q = %d, want 422", bad, c)
		}
	}
	for i, good := range r7AssetTypes {
		w := f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/assets", nil, map[string]any{"projectId": "P001", "assetType": good, "assetCode": "R7-OK-" + strconv.Itoa(i)})
		if w.Code != 201 {
			t.Errorf("CREATE assetType %q = %d %s", good, w.Code, w.Body.String())
		}
	}
	// Omitted (or blank) assetType takes the column default CRANE, as before R7.
	w := f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/assets", nil, map[string]any{"projectId": "P001", "assetCode": "R7-DEFAULT"})
	if w.Code != 201 || decodeJSON[contract.AssetMasterData](t, w.Body.Bytes()).AssetType != "CRANE" {
		t.Fatalf("CREATE without assetType = %d %s, want 201 CRANE", w.Code, w.Body.String())
	}

	// Legacy non-standard row (existing field data is never auto-cleaned).
	if _, err := f.db.Exec(`INSERT INTO asset_devices(id,project_id,display_name,lifecycle,asset_code,asset_type,asset_status,version) VALUES('r7-legacy','P001','旧数据','PLANNED','R7-LEGACY','门机-旧','ACTIVE',1)`); err != nil {
		t.Fatal(err)
	}
	if g := f.request(admin, ai.CsrfToken, "/api/v1/assets/r7-legacy", "GET"); g.Code != 200 {
		t.Fatalf("legacy GET = %d", g.Code)
	}
	patch := func(version int64, body map[string]any) (int, contract.AssetMasterData) {
		w := f.doBody(admin, ai.CsrfToken, "PATCH", "/api/v1/assets/r7-legacy", map[string]string{"If-Match": strconv.FormatInt(version, 10)}, body)
		var out contract.AssetMasterData
		if w.Code == 200 {
			out = decodeJSON[contract.AssetMasterData](t, w.Body.Bytes())
		}
		return w.Code, out
	}
	// An old client edits another field and does not send assetType: allowed, type untouched.
	c, out := patch(1, map[string]any{"displayName": "旧数据-改名"})
	if c != 200 || out.AssetType != "门机-旧" || out.Version != 2 {
		t.Fatalf("legacy PATCH without assetType = %d %+v", c, out)
	}
	// An old client re-sends the unchanged legacy value: allowed (no-op for that field).
	c, out = patch(2, map[string]any{"assetType": "门机-旧", "serialNumber": "SN-1"})
	if c != 200 || out.AssetType != "门机-旧" {
		t.Fatalf("legacy PATCH re-sending unchanged type = %d %+v", c, out)
	}
	// Changing it to another non-standard value or an Equipment code is refused.
	for _, bad := range []string{"门机-新", "MOTOR"} {
		if c, _ := patch(3, map[string]any{"assetType": bad}); c != 422 {
			t.Errorf("legacy PATCH to %q = %d, want 422", bad, c)
		}
	}
	// Normalizing it to an authoritative type is allowed.
	if c, out := patch(3, map[string]any{"assetType": "GANTRY"}); c != 200 || out.AssetType != "GANTRY" {
		t.Fatalf("legacy PATCH to GANTRY = %d %+v", c, out)
	}
}

// F-11: an asset needs a business name. Name and code both blank (after trim) → 422 with the reason,
// no asset / revision / success audit / idempotency row; a name alone is enough (code optional); a
// code alone still becomes the name (historical behaviour). Never "<projectId>-asset".
func TestR8AssetNameOrCodeRequiredHTTP(t *testing.T) {
	f := newSecurityFixture(t)
	admin, ai := f.login(t, "admin")
	counts := func() [4]int {
		var c [4]int
		_ = f.db.QueryRow(`SELECT count(*) FROM asset_devices WHERE project_id='P001'`).Scan(&c[0])
		_ = f.db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE entity_type='asset'`).Scan(&c[1])
		_ = f.db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='asset_created' AND result='success'`).Scan(&c[2])
		_ = f.db.QueryRow(`SELECT count(*) FROM idempotency_keys WHERE operation='asset_create'`).Scan(&c[3])
		return c
	}
	before := counts()
	for name, body := range map[string]map[string]any{
		"both missing":          {"projectId": "P001", "assetType": "CRANE"},
		"both blank":            {"projectId": "P001", "assetType": "CRANE", "displayName": "   ", "assetCode": "  "},
		"blank name, null code": {"projectId": "P001", "assetType": "CRANE", "displayName": "\t", "assetCode": nil},
	} {
		w := f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/assets", map[string]string{"Idempotency-Key": "f11-" + strings.ReplaceAll(name, " ", "-")}, body)
		if w.Code != 422 || !strings.Contains(w.Body.String(), "displayName or assetCode") {
			t.Fatalf("%s: %d %s, want 422 with reason", name, w.Code, w.Body.String())
		}
	}
	if after := counts(); after != before {
		t.Fatalf("refused create had side effects: %v → %v", before, after)
	}
	var placeholders int
	_ = f.db.QueryRow(`SELECT count(*) FROM asset_devices WHERE display_name LIKE '%-asset'`).Scan(&placeholders)
	if placeholders != 0 {
		t.Fatalf("%d placeholder-named assets", placeholders)
	}
	for _, c := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"projectId": "P001", "assetType": "CRANE", "displayName": "  一号行车 "}, "一号行车"},
		{map[string]any{"projectId": "P001", "assetType": "CRANE", "displayName": " ", "assetCode": " F11-CODE "}, "F11-CODE"},
	} {
		w := f.doBody(admin, ai.CsrfToken, "POST", "/api/v1/assets", nil, c.body)
		var a contract.AssetMasterData
		_ = json.Unmarshal(w.Body.Bytes(), &a)
		if w.Code != 201 || a.DisplayName != c.want {
			t.Fatalf("create %v = %d %s, want name %q", c.body, w.Code, w.Body.String(), c.want)
		}
	}
}
