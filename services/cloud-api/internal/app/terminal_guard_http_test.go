package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

func seedMOTORType(f securityFixture, t *testing.T) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO equipment_types(type_code,display_name,domain,spec_schema_version,status) VALUES('MOTOR','MOTOR','MECHANICAL','1','ACTIVE')`); err != nil {
		t.Fatal(err)
	}
}

func installEquipmentHTTP(f securityFixture, t *testing.T, cookie *http.Cookie, csrf, assetID, code string) contract.EquipmentInstallation {
	t.Helper()
	w := f.doBody(cookie, csrf, "POST", "/api/v1/assets/"+assetID+"/equipment", nil,
		map[string]any{"assetId": assetID, "equipmentCode": code, "typeCode": "MOTOR", "spec": map[string]any{"rated_power_kw": 7.5}})
	if w.Code != http.StatusCreated {
		t.Fatalf("install equipment = %d (%s)", w.Code, w.Body.String())
	}
	var eq contract.EquipmentInstallation
	if err := json.Unmarshal(w.Body.Bytes(), &eq); err != nil {
		t.Fatal(err)
	}
	return eq
}

// A retired asset must reject ordinary child writes over HTTP with 409 (not 503):
// equipment update and relation creation are refused at the transaction layer.
func TestHTTPRetiredAssetDeniesChildWrites(t *testing.T) {
	f := newSecurityFixture(t)
	admin, info := f.login(t, "admin")
	seedMOTORType(f, t)

	create := f.doBody(admin, info.CsrfToken, "POST", "/api/v1/assets", nil, map[string]any{"projectId": "P001", "assetType": "CRANE", "assetCode": "CRN-TERM-1"})
	if create.Code != http.StatusCreated {
		t.Fatalf("asset create = %d (%s)", create.Code, create.Body.String())
	}
	var asset contract.AssetMasterData
	if err := json.Unmarshal(create.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	eq1 := installEquipmentHTTP(f, t, admin, info.CsrfToken, asset.Id, "EQ-T1")
	eq2 := installEquipmentHTTP(f, t, admin, info.CsrfToken, asset.Id, "EQ-T2")

	retire := f.doBody(admin, info.CsrfToken, "POST", "/api/v1/assets/"+asset.Id+"/retire",
		map[string]string{"If-Match": strconv.FormatInt(asset.Version, 10)}, map[string]any{})
	if retire.Code != http.StatusOK {
		t.Fatalf("retire = %d (%s)", retire.Code, retire.Body.String())
	}

	patch := f.doBody(admin, info.CsrfToken, "PATCH", "/api/v1/equipment/"+eq1.Id,
		map[string]string{"If-Match": strconv.FormatInt(eq1.Version, 10)}, map[string]any{"criticality": "HIGH"})
	if patch.Code != http.StatusConflict {
		t.Fatalf("update equipment on retired asset = %d, want 409 (%s)", patch.Code, patch.Body.String())
	}

	rel := f.doBody(admin, info.CsrfToken, "POST", "/api/v1/assets/"+asset.Id+"/equipment-relations", nil,
		map[string]any{"assetId": asset.Id, "fromEquipmentInstallationId": eq1.Id, "toEquipmentInstallationId": eq2.Id, "relationType": "DRIVES"})
	if rel.Code != http.StatusConflict {
		t.Fatalf("create relation on retired asset = %d, want 409 (%s)", rel.Code, rel.Body.String())
	}
}

// A relation can only be terminated once; a second termination (even with the
// latest version) is a 409 and leaves the first valid_to intact.
func TestHTTPRelationDoubleRemoveDenied(t *testing.T) {
	f := newSecurityFixture(t)
	admin, info := f.login(t, "admin")
	seedMOTORType(f, t)

	create := f.doBody(admin, info.CsrfToken, "POST", "/api/v1/assets", nil, map[string]any{"projectId": "P001", "assetType": "CRANE", "assetCode": "CRN-TERM-2"})
	if create.Code != http.StatusCreated {
		t.Fatalf("asset create = %d (%s)", create.Code, create.Body.String())
	}
	var asset contract.AssetMasterData
	if err := json.Unmarshal(create.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	eq1 := installEquipmentHTTP(f, t, admin, info.CsrfToken, asset.Id, "EQ-R1")
	eq2 := installEquipmentHTTP(f, t, admin, info.CsrfToken, asset.Id, "EQ-R2")

	createRel := f.doBody(admin, info.CsrfToken, "POST", "/api/v1/assets/"+asset.Id+"/equipment-relations", nil,
		map[string]any{"assetId": asset.Id, "fromEquipmentInstallationId": eq1.Id, "toEquipmentInstallationId": eq2.Id, "relationType": "DRIVES"})
	if createRel.Code != http.StatusCreated {
		t.Fatalf("create relation = %d (%s)", createRel.Code, createRel.Body.String())
	}
	var relation contract.EquipmentRelation
	if err := json.Unmarshal(createRel.Body.Bytes(), &relation); err != nil {
		t.Fatal(err)
	}

	remove1 := f.doBody(admin, info.CsrfToken, "POST", "/api/v1/equipment-relations/"+relation.Id+"/remove",
		map[string]string{"If-Match": strconv.FormatInt(relation.Version, 10)}, map[string]any{})
	if remove1.Code != http.StatusOK {
		t.Fatalf("first remove = %d (%s)", remove1.Code, remove1.Body.String())
	}

	remove2 := f.doBody(admin, info.CsrfToken, "POST", "/api/v1/equipment-relations/"+relation.Id+"/remove",
		map[string]string{"If-Match": strconv.FormatInt(relation.Version+1, 10)}, map[string]any{})
	if remove2.Code != http.StatusConflict {
		t.Fatalf("second remove = %d, want 409 (%s)", remove2.Code, remove2.Body.String())
	}
}
