package asset

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

func seedProject(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO projects(id,tenant_id,name) VALUES($1,'demo',$1) ON CONFLICT (id) DO NOTHING`, id); err != nil {
		t.Fatal(err)
	}
}

func seedEquipmentType(t *testing.T, db *sql.DB, code string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO equipment_types(type_code,display_name,domain,spec_schema_version,status) VALUES($1,$1,'MECHANICAL','1','ACTIVE')`, code); err != nil {
		t.Fatal(err)
	}
}

func testAsset(projectID string) contract.AssetMasterData {
	return contract.AssetMasterData{ProjectId: projectID, AssetType: "CRANE", AssetCode: strPtr("CRN-001")}
}

func TestCreateAssetIdempotencyRetry(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	in := testAsset("Project-A")
	first, err := CreateAsset(ctx, db, in, "actor", "req-1", "key-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateAsset(ctx, db, in, "actor", "req-2", "key-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Id != second.Id {
		t.Fatalf("idempotent retry returned different entities: %s vs %s", first.Id, second.Id)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM asset_devices WHERE project_id='Project-A'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("idempotent retry inserted %d assets (err=%v)", count, err)
	}
}

func TestCreateAssetIdempotencyDifferentPayloadRejected(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	if _, err := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-1", "key-1"); err != nil {
		t.Fatal(err)
	}
	other := testAsset("Project-A")
	other.AssetCode = strPtr("CRN-002")
	if _, err := CreateAsset(ctx, db, other, "actor", "req-2", "key-1"); err != ErrIdempotencyKey {
		t.Fatalf("err = %v, want ErrIdempotencyKey", err)
	}
}

func TestCreateAssetConcurrentIdempotency(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	in := testAsset("Project-A")
	var wg sync.WaitGroup
	ids := make([]string, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := CreateAsset(ctx, db, in, "actor", "req", "key-concurrent")
			ids[i], errs[i] = out.Id, err
		}(i)
	}
	wg.Wait()
	for i := 0; i < 2; i++ {
		if errs[i] != nil {
			t.Fatalf("concurrent create %d failed: %v", i, errs[i])
		}
	}
	if ids[0] != ids[1] {
		t.Fatalf("concurrent idempotency created two entities: %s vs %s", ids[0], ids[1])
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM asset_devices WHERE project_id='Project-A'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent idempotency inserted %d assets (err=%v)", count, err)
	}
}

func TestUpdateAssetStaleIfMatch(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	out, err := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}
	update := out
	update.AssetCode = strPtr("CRN-001-B")
	if _, err := UpdateAsset(ctx, db, update, out.Version+1, "actor", "req-2"); err != ErrVersionConflict {
		t.Fatalf("err = %v, want ErrVersionConflict", err)
	}
}

func TestAssetRetireLifecycle(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	out, err := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := RetireAsset(ctx, db, out.Id, out.Version, "actor", "req-2"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRow(`SELECT asset_status FROM asset_devices WHERE id=$1`, out.Id).Scan(&status); err != nil || status != "RETIRED" {
		t.Fatalf("status=%s err=%v, want RETIRED", status, err)
	}
	if err := RetireAsset(ctx, db, out.Id, out.Version+1, "actor", "req-3"); err != ErrRetired {
		t.Fatalf("double retire err = %v, want ErrRetired", err)
	}
	// RETIRED is terminal: cannot patch back.
	update := out
	update.AssetCode = strPtr("X")
	if _, err := UpdateAsset(ctx, db, update, out.Version+1, "actor", "req-4"); err != ErrRetired {
		t.Fatalf("patch retired err = %v, want ErrRetired", err)
	}
}

func TestAssetRetireWithActiveBindingRejected(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	out, err := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}
	eq, err := CreateEquipment(ctx, db, contract.EquipmentInstallation{AssetId: out.Id, EquipmentCode: "EQ-1", TypeCode: "MOTOR", Criticality: "MEDIUM"}, json.RawMessage(`{"rated_power_kw": 7.5, "rated_speed_rpm": 1450, "brake_integrated": true}`), "actor", "req-2", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO equipment_runtime_bindings(id,asset_id,equipment_installation_id,source_kind,source_ref,binding_status,valid_from) VALUES('RB-1',$1,$2,'DEPLOYMENT','dev','ACTIVE',now())`, out.Id, eq.Id); err != nil {
		t.Fatal(err)
	}
	if err := RetireAsset(ctx, db, out.Id, out.Version, "actor", "req-3"); err != ErrActiveBinding {
		t.Fatalf("retire with active binding err = %v, want ErrActiveBinding", err)
	}
}

func TestLocationMultipleNullCodeAndDuplicateReject(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	if _, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "SITE", Name: "L1"}, "actor", "req-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "SITE", Name: "L2"}, "actor", "req-2"); err != nil {
		t.Fatalf("multiple NULL-code locations rejected: %v", err)
	}
	if _, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "SITE", Name: "L3", Code: strPtr("C1")}, "actor", "req-3"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "SITE", Name: "L4", Code: strPtr("C1")}, "actor", "req-4"); err == nil {
		t.Fatal("duplicate same-project code accepted")
	}
}

func TestLocationParentCrossProjectRejected(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedProject(t, db, "Project-B")
	parent, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "SITE", Name: "Parent"}, "actor", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-B", Kind: "SITE", Name: "Child", ParentId: &parent.Id}, "actor", "req-2"); err == nil {
		t.Fatal("cross-project parent accepted")
	}
}

func TestAssetMoveHistory(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	out, err := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}
	loc1, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "SITE", Name: "Loc1", Code: strPtr("A1")}, "actor", "req-2")
	if err != nil {
		t.Fatal(err)
	}
	loc2, err := CreateLocation(ctx, db, contract.AssetLocation{ProjectId: "Project-A", Kind: "SITE", Name: "Loc2", Code: strPtr("A2")}, "actor", "req-3")
	if err != nil {
		t.Fatal(err)
	}
	if err := MoveAsset(ctx, db, out.Id, loc1.Id, out.Version, "actor", "req-4"); err != nil {
		t.Fatal(err)
	}
	if err := MoveAsset(ctx, db, out.Id, loc2.Id, out.Version+1, "actor", "req-5"); err != nil {
		t.Fatal(err)
	}
	var active, total int
	if err := db.QueryRow(`SELECT count(*) FROM asset_installations WHERE asset_id=$1 AND valid_to IS NULL`, out.Id).Scan(&active); err != nil || active != 1 {
		t.Fatalf("active installations=%d err=%v, want 1", active, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM asset_installations WHERE asset_id=$1`, out.Id).Scan(&total); err != nil || total != 2 {
		t.Fatalf("total installations=%d err=%v, want 2", total, err)
	}
}

func TestManufacturerGlobalReadOnly(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	global, err := CreateManufacturer(ctx, db, contract.ManufacturerInput{Name: "Global Mfg"}, "actor", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if global.ProjectId != nil {
		t.Fatal("expected global manufacturer")
	}
	if _, err := UpdateManufacturer(ctx, db, global, global.Version, "actor", "req-2"); err != ErrGlobalReadOnly {
		t.Fatalf("update global err = %v, want ErrGlobalReadOnly", err)
	}
	if err := DisableManufacturer(ctx, db, global.Id, global.Version, "actor", "req-3"); err != ErrGlobalReadOnly {
		t.Fatalf("disable global err = %v, want ErrGlobalReadOnly", err)
	}
}

func TestManufacturerCrossProjectRejected(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedProject(t, db, "Project-B")
	mfg, err := CreateManufacturer(ctx, db, contract.ManufacturerInput{ProjectId: strPtr("Project-A"), Name: "Mfg A"}, "actor", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	in := testAsset("Project-B")
	in.ManufacturerId = &mfg.Id
	if _, err := CreateAsset(ctx, db, in, "actor", "req-2", ""); err == nil {
		t.Fatal("cross-project manufacturer reference accepted")
	}
}

func TestCatalogGlobalReadAndCrossProjectReject(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedProject(t, db, "Project-B")
	seedEquipmentType(t, db, "MOTOR")
	global, err := CreateCatalogItem(ctx, db, contract.EquipmentCatalogItem{Name: "Global Catalog", TypeCode: "MOTOR", Brand: "B", Model: "M"}, "actor", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetCatalogItem(ctx, db, global.Id); err != nil {
		t.Fatalf("global catalog read failed: %v", err)
	}
	// Cross-project equipment -> catalog reference is rejected by the DB guard.
	out, err := CreateAsset(ctx, db, testAsset("Project-B"), "actor", "req-2", "")
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := CreateCatalogItem(ctx, db, contract.EquipmentCatalogItem{ProjectId: strPtr("Project-A"), Name: "Scoped Catalog", TypeCode: "MOTOR", Brand: "B", Model: "M"}, "actor", "req-3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateEquipment(ctx, db, contract.EquipmentInstallation{AssetId: out.Id, EquipmentCode: "EQ-X", TypeCode: "MOTOR", CatalogItemId: &scoped.Id, Criticality: "MEDIUM"}, json.RawMessage(`{"rated_power_kw": 7.5, "rated_speed_rpm": 1450, "brake_integrated": true}`), "actor", "req-4", ""); err == nil {
		t.Fatal("cross-project catalog reference accepted")
	}
}

func TestEquipmentSpecValidation(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	out, err := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateEquipment(ctx, db, contract.EquipmentInstallation{AssetId: out.Id, EquipmentCode: "EQ-1", TypeCode: "MOTOR"}, json.RawMessage(`{"nonsense": 1}`), "actor", "req-2", ""); err == nil {
		t.Fatal("invalid spec accepted")
	}
	if _, err := CreateEquipment(ctx, db, contract.EquipmentInstallation{AssetId: out.Id, EquipmentCode: "EQ-2", TypeCode: "MOTOR"}, json.RawMessage(`{"rated_power_kw": 7.5, "rated_speed_rpm": 1450, "brake_integrated": true}`), "actor", "req-3", ""); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
}

func TestEquipmentActiveBindingRemoveRejected(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	out, err := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}
	eq, err := CreateEquipment(ctx, db, contract.EquipmentInstallation{AssetId: out.Id, EquipmentCode: "EQ-1", TypeCode: "MOTOR"}, json.RawMessage(`{"rated_power_kw": 7.5, "rated_speed_rpm": 1450, "brake_integrated": true}`), "actor", "req-2", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO equipment_runtime_bindings(id,asset_id,equipment_installation_id,source_kind,source_ref,binding_status,valid_from) VALUES('RB-1',$1,$2,'DEPLOYMENT','dev','ACTIVE',now())`, out.Id, eq.Id); err != nil {
		t.Fatal(err)
	}
	if err := RemoveEquipment(ctx, db, eq.Id, eq.Version, "actor", "req-3"); err != ErrActiveBinding {
		t.Fatalf("remove with active binding err = %v, want ErrActiveBinding", err)
	}
}

func TestRelationCrossAssetRejected(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	seedEquipmentType(t, db, "MOTOR")
	a1, _ := CreateAsset(ctx, db, contract.AssetMasterData{ProjectId: "Project-A", AssetType: "CRANE"}, "actor", "req-1", "")
	a2, _ := CreateAsset(ctx, db, contract.AssetMasterData{ProjectId: "Project-A", AssetType: "CRANE"}, "actor", "req-2", "")
	e1, err := CreateEquipment(ctx, db, contract.EquipmentInstallation{AssetId: a1.Id, EquipmentCode: "EQ-1", TypeCode: "MOTOR"}, json.RawMessage(`{"rated_power_kw": 7.5, "rated_speed_rpm": 1450, "brake_integrated": true}`), "actor", "req-3", "")
	if err != nil {
		t.Fatal(err)
	}
	e2, err := CreateEquipment(ctx, db, contract.EquipmentInstallation{AssetId: a2.Id, EquipmentCode: "EQ-2", TypeCode: "MOTOR"}, json.RawMessage(`{"rated_power_kw": 7.5, "rated_speed_rpm": 1450, "brake_integrated": true}`), "actor", "req-4", "")
	if err != nil {
		t.Fatal(err)
	}
	// from on asset2, relation claims asset1 -> composite FK reject.
	if _, err := CreateRelation(ctx, db, contract.EquipmentRelation{AssetId: a1.Id, FromEquipmentInstallationId: e2.Id, ToEquipmentInstallationId: e1.Id, RelationType: "DRIVES"}, "actor", "req-5"); err == nil {
		t.Fatal("cross-asset relation accepted")
	}
}

func TestRevisionAndAuditWritten(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	out, err := CreateAsset(ctx, db, testAsset("Project-A"), "actor", "req-1", "")
	if err != nil {
		t.Fatal(err)
	}
	var revisions int
	if err := db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE entity_type='asset' AND entity_id=$1`, out.Id).Scan(&revisions); err != nil || revisions != 1 {
		t.Fatalf("revisions=%d err=%v, want 1", revisions, err)
	}
	var audits int
	if err := db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='asset_created' AND result='success' AND user_id='actor'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits=%d err=%v, want 1", audits, err)
	}
	// Update writes another revision + audit.
	upd := out
	upd.AssetCode = strPtr("CRN-002")
	if _, err := UpdateAsset(ctx, db, upd, out.Version, "actor", "req-2"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM entity_revisions WHERE entity_type='asset' AND entity_id=$1`, out.Id).Scan(&revisions); err != nil || revisions != 2 {
		t.Fatalf("post-update revisions=%d err=%v, want 2", revisions, err)
	}
}

func TestListAssetsPagingAndSearch(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	seedProject(t, db, "Project-A")
	for i := 0; i < 3; i++ {
		in := testAsset("Project-A")
		code := "CRN-00" + string(rune('1'+i))
		in.AssetCode = strPtr(code)
		if _, err := CreateAsset(ctx, db, in, "actor", "req", ""); err != nil {
			t.Fatal(err)
		}
	}
	values, total, err := ListAssets(ctx, db, "Project-A", ListOptions{Limit: 2, Offset: 0})
	if err != nil || total != 3 || len(values) != 2 {
		t.Fatalf("page len=%d total=%d err=%v, want 2/3", len(values), total, err)
	}
	values, _, err = ListAssets(ctx, db, "Project-A", ListOptions{Q: "CRN-002"})
	if err != nil || len(values) != 1 {
		t.Fatalf("search len=%d err=%v, want 1", len(values), err)
	}
}
