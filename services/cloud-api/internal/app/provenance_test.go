package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/deploy/postgres/migrations"
	"github.com/guolichen007/crane-operations-platform/internal/inventory"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/device"
	"github.com/pressly/goose/v3"
)

func provenanceDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	source := os.Getenv("A0_TEST_DATABASE_URL")
	if source == "" {
		t.Skip("real PostgreSQL startup and Apply counterexamples run in Compose")
	}
	root, err := sql.Open("pgx", source)
	if err != nil {
		t.Fatal(err)
	}
	name := "crane_hardening_test_" + platform.ID()
	if !contract.ValidID(name) {
		t.Fatal("invalid isolated test DB name")
	}
	if _, err = root.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		root.Close()
		t.Fatal(err)
	}
	u, err := url.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, err := root.Exec(`DROP DATABASE "` + name + `" WITH (FORCE)`)
		root.Close()
		if err != nil {
			t.Error("isolated test DB cleanup failed", err)
		}
	})
	goose.SetLogger(goose.NopLogger())
	goose.SetBaseFS(migrations.Files)
	if err = goose.SetDialect("postgres"); err == nil {
		err = goose.UpContext(context.Background(), db, ".")
	}
	if err != nil {
		t.Fatal(err)
	}
	return db, u.String()
}

func publicInventory(t *testing.T) contract.InventoryDocument {
	t.Helper()
	path := os.Getenv("A0_INVENTORY_EXAMPLE")
	if path == "" {
		path = "../../../../deploy/inventory/inventory.example.yaml"
	}
	document, err := inventory.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestExplicitFleetCloudStartupRejectsMissingOrMismatchedDB(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want error
	}{
		{"unregistered_first_runtime", `DELETE FROM devices WHERE id='SIM-P001-01'`, device.ErrRuntimeNotRegistered},
		{"unregistered_last_runtime", `DELETE FROM devices WHERE id='SIM-P006-03'`, device.ErrRuntimeNotRegistered},
		{"project_mismatch", `UPDATE devices SET asset_device_id=NULL,project_id='P002' WHERE id='SIM-P001-01'`, device.ErrRuntimeProvenanceMismatch},
		{"source_mode_mismatch", `UPDATE devices SET source_mode='TEST' WHERE id='SIM-P001-01'`, device.ErrRuntimeProvenanceMismatch},
		{"asset_missing", `UPDATE devices SET asset_device_id=NULL WHERE id='SIM-P001-01'`, device.ErrRuntimeProvenanceMismatch},
		{"asset_mismatch", `INSERT INTO asset_devices(id,project_id,display_name,lifecycle) VALUES('ASSET-OTHER','P001','Anonymous','PLANNED'); UPDATE devices SET asset_device_id='ASSET-OTHER' WHERE id='SIM-P001-01'`, device.ErrRuntimeProvenanceMismatch},
		{"tenant_mismatch", `INSERT INTO tenants VALUES('other','Anonymous'); UPDATE projects SET tenant_id='other' WHERE id='P001'`, device.ErrRuntimeProvenanceMismatch},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			db, source := provenanceDB(t)
			document := publicInventory(t)
			if err := inventory.Import(context.Background(), db, document); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(test.sql); err != nil {
				t.Fatal(err)
			}
			manifest := inventory.Fleet(document)
			path := filepath.Join(t.TempDir(), "fleet.json")
			b, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]string{
				"APP_CONFIG": "", "APP_ENV": "development", "INVENTORY_FILE": "", "FLEET_FILE": path,
				"DATABASE_URL": source, "DEV_API_TOKEN": strings.Repeat("x", 32),
				"HTTP_ADDR": "127.0.0.1:0", "CERT_DIR": t.TempDir(),
			} {
				t.Setenv(key, value)
			}
			// Run the real Cloud entry point. Missing certs ensure a missing startup
			// validation cannot accidentally pass by failing later at MQTT instead.
			if err = Run(nil); !errors.Is(err, test.want) {
				t.Fatalf("Cloud must reject before MQTT/HTTP startup: got %v, want %v", err, test.want)
			}
		})
	}
}

func birth(id, project string) contract.Envelope {
	return contract.WithPayload(contract.Envelope{SchemaVersion: 1, TenantId: "demo", ProjectId: project, DeviceId: id,
		MessageId: platform.ID(), BootId: "first", BootSeq: 1, Sequence: 1, Kind: "BIRTH", SentAt: time.Now().UTC()}, contract.BirthPayload{})
}

func TestRegisteredInventoryFleetAndStoreApply(t *testing.T) {
	db, _ := provenanceDB(t)
	ctx := context.Background()
	document := publicInventory(t)
	if err := inventory.Import(ctx, db, document); err != nil {
		t.Fatal(err)
	}
	manifest := inventory.Fleet(document)
	if err := device.ValidateFleet(ctx, db, manifest); err != nil {
		t.Fatal("correct Inventory -> Fleet must pass", err)
	}
	// A missing manifest asset is not a wildcard for any registered DB asset.
	manifest.Devices[0].AssetDeviceId = nil
	if err := device.ValidateFleet(ctx, db, manifest); !errors.Is(err, device.ErrRuntimeProvenanceMismatch) {
		t.Fatal("nullable asset provenance was ignored", err)
	}
	store, err := device.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if accepted, err := store.Apply(ctx, birth("SIM-P001-01", "P001")); !accepted || err != nil {
		t.Fatal("registered SIM rejected", accepted, err)
	}
	if accepted, err := store.Apply(ctx, birth("SIM-P002-01", "P001")); accepted || !errors.Is(err, device.ErrRuntimeProvenanceMismatch) {
		t.Fatal("registered runtime accepted different project", accepted, err)
	}
	// REAL remains a registered identity only; this test adds no REAL adapter.
	real := document.RuntimeDevices[0]
	real.RuntimeDeviceId, real.SourceMode = "EDGE-P001-01", "REAL"
	document.RuntimeDevices = append(document.RuntimeDevices, real)
	if err := inventory.Import(ctx, db, document); err != nil {
		t.Fatal(err)
	}
	if accepted, err := store.Apply(ctx, birth(real.RuntimeDeviceId, real.ProjectId)); !accepted || err != nil {
		t.Fatal("registered identity rejected", accepted, err)
	}
}

func TestStoreNeverProvisionsUnknownExplicitRuntimeAndKeepsLegacyCount(t *testing.T) {
	db, _ := provenanceDB(t)
	ctx := context.Background()
	store, err := device.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"SIM-P001-01", "EDGE-P001-01", "SIM-000", "SIM-021", "TEST-002"} {
		if accepted, err := store.Apply(ctx, birth(id, "Project-A")); accepted || !errors.Is(err, device.ErrRuntimeNotRegistered) {
			t.Fatal("MQTT first packet provisioned unknown identity", id, accepted, err)
		}
		if _, ok := store.Get(id); ok {
			t.Fatal("rejected identity entered Shadow cache")
		}
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM devices").Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected packet changed DB", count, err)
	}
	for i := 1; i <= 21; i++ {
		id, mode := fmt.Sprintf("SIM-%03d", i), "SIMULATION"
		if i == 21 {
			id, mode = "TEST-001", "TEST"
		}
		if accepted, err := store.Apply(ctx, birth(id, "Project-A")); !accepted || err != nil {
			t.Fatal("retained fixture rejected", id, accepted, err)
		}
		var source string
		var asset sql.NullString
		if err = db.QueryRow("SELECT source_mode,asset_device_id FROM devices WHERE id=$1", id).Scan(&source, &asset); err != nil || source != mode || asset.Valid {
			t.Fatal("legacy provenance changed", err)
		}
	}
}
