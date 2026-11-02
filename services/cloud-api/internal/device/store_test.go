package device

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

func TestAutoProvisionIsLimitedToRetainedCountFixtures(t *testing.T) {
	for i := 1; i <= 20; i++ {
		if source, ok := legacySource(fmt.Sprintf("SIM-%03d", i)); !ok || source != "SIMULATION" {
			t.Fatal("original count fixture rejected", i)
		}
	}
	if source, ok := legacySource("TEST-001"); !ok || source != "TEST" {
		t.Fatal("dedicated test fixture rejected")
	}
	for _, id := range []string{"SIM-000", "SIM-021", "SIM-999", "SIM-01a", "SIM-01", "SIM-P001-01", "EDGE-P001-01", "TEST-002", "unknown"} {
		if _, ok := legacySource(id); ok {
			t.Fatal("unregistered identity could be auto-provisioned", id)
		}
	}
}

// New LIVE samples persist raw time-quality inputs from the V1 envelope; the
// wire contract and sampled_at semantics stay unchanged.
func TestTelemetryTimeQualityPersisted(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO devices(id,project_id,source_mode) VALUES('SIM-001','Project-A','SIMULATION')`); err != nil {
		t.Fatal(err)
	}
	s, err := New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	sent := time.Now().UTC().Add(-2 * time.Second)
	payload, err := json.Marshal(contract.LiveTelemetryPayload{LeaseId: "lease-1", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	env := contract.Envelope{
		SchemaVersion: 1,
		MessageId:     "msg-1",
		TenantId:      "demo",
		ProjectId:     "Project-A",
		DeviceId:      "SIM-001",
		BootId:        "boot-1",
		BootSeq:       1,
		Sequence:      1,
		SentAt:        sent,
		Kind:          "LIVE",
		CorrelationId: "",
		Payload:       payload,
	}
	if ok, err := s.Apply(ctx, env); err != nil || !ok {
		t.Fatalf("apply ok=%v err=%v", ok, err)
	}
	var sourceSent, received time.Time
	var bootID string
	var bootSeq, seq int64
	if err := db.QueryRow(`SELECT source_sent_at,cloud_received_at,boot_id,boot_seq,sequence FROM telemetry_samples WHERE device_id='SIM-001'`).Scan(&sourceSent, &received, &bootID, &bootSeq, &seq); err != nil {
		t.Fatal(err)
	}
	if !sourceSent.Equal(sent) {
		t.Fatalf("source_sent_at=%v want %v", sourceSent, sent)
	}
	if bootID != "boot-1" || bootSeq != 1 || seq != 1 {
		t.Fatalf("envelope fields: boot_id=%q boot_seq=%d seq=%d", bootID, bootSeq, seq)
	}
	if received.IsZero() {
		t.Fatal("cloud_received_at not populated")
	}
}

// Pre-00006 legacy rows keep their three-column shape and never fabricate
// time-quality metadata (NULL/UNKNOWN, never a fake GOOD).
func TestLegacyTelemetryRowStaysReadable(t *testing.T) {
	db := testsupport.Database(t)
	if _, err := db.Exec(`INSERT INTO devices(id,project_id,source_mode) VALUES('SIM-002','Project-A','SIMULATION')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO telemetry_samples(device_id,sampled_at,payload_json) VALUES('SIM-002',now(),'{}')`); err != nil {
		t.Fatal(err)
	}
	var sourceSent, received *time.Time
	var bootID *string
	if err := db.QueryRow(`SELECT source_sent_at,cloud_received_at,boot_id FROM telemetry_samples WHERE device_id='SIM-002'`).Scan(&sourceSent, &received, &bootID); err != nil {
		t.Fatal(err)
	}
	if sourceSent != nil || received != nil || bootID != nil {
		t.Fatal("legacy row fabricated time-quality metadata")
	}
}
