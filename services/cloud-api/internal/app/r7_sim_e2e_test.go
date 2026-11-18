package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	goruntime "runtime"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/asset"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/telemetry"
)

// R7 SIM-E2E scenarios. Each asserts against PostgreSQL and the real BFF/WebSocket; nothing here
// stubs a REST or WebSocket response. Fault injection is per device (proxy) or per process
// (cloud runtime restart) and is recorded in the evidence.

// 1 device, full loop: TLS identity → BIRTH → shadow in PostgreSQL → HTTP read → WebSocket
// subscription → real WATCH START (cloud publishes, Agent ACKs, generation fenced) → LIVE 1 Hz with
// position/load → RENEW → STOP back to SILENT → graceful DEATH. Latency = Agent SentAt → shadow
// observed over the WebSocket.
func TestR7Sim1DeviceFullLoop(t *testing.T) {
	e := newSimEnv(t, 30*time.Second, 5*time.Second, []simSpec{{project: "R7-S1", runtime: "SIM-R7-0001", source: "SIMULATION", bind: true}})
	admin := e.user("s1-admin", "R7-S1", "platform_admin")
	e.startDevice("SIM-R7-0001", "normal")
	e.until("BIRTH persisted, MQTT_CONNECTED", 20*time.Second, func() bool { return e.connected("SIM-R7-0001") })
	var sh contract.Shadow
	if c := e.call(admin, "GET", "/api/v1/devices/SIM-R7-0001/shadow", &sh); c != 200 || sh.Reported.OperatingState != "SILENT" || sh.Presence.BootSeq < 1 {
		t.Fatalf("shadow over BFF = %d %+v", c, sh)
	}
	simRecord(t, "MQTT_SHADOW_DB", map[string]any{"bootSeq": sh.Presence.BootSeq, "state": sh.Reported.OperatingState, "edgeHealth": sh.Presence.EdgeHealth})

	conn, _, err := e.dialStream(admin, "SIM-R7-0001")
	if err != nil {
		t.Fatal("websocket", err)
	}
	defer conn.CloseNow()
	frames := make(chan contract.Shadow, 256)
	go func() {
		for {
			_, b, err := conn.Read(e.ctx)
			if err != nil {
				close(frames)
				return
			}
			var v contract.Shadow
			if json.Unmarshal(b, &v) == nil {
				select {
				case frames <- v:
				default:
				}
			}
		}
	}()

	var lease contract.LiveSession
	if c := e.call(admin, "POST", "/api/v1/devices/SIM-R7-0001/live-sessions", &lease); c != 201 {
		t.Fatalf("open live = %d", c)
	}
	e.until("device ACKed WATCH_START; upstream LIVE", 20*time.Second, func() bool {
		var n int
		_ = e.db.QueryRow(`SELECT count(*) FROM device_commands WHERE device_id='SIM-R7-0001' AND kind='WATCH_START' AND status='APPLIED' AND acked_at IS NOT NULL`).Scan(&n)
		s, _ := e.leases.Status(e.ctx, "SIM-R7-0001")
		return n == 1 && s == "LIVE"
	})
	var gen int64
	_ = e.db.QueryRow(`SELECT generation FROM device_live_state WHERE device_id='SIM-R7-0001'`).Scan(&gen)
	simRecord(t, "MQTT_LIVE_WATCH_ACK", map[string]any{"generation": gen, "lease": lease.SessionId})

	// LIVE frames over the real WebSocket carry moving position and load.
	var lat []time.Duration
	positions := map[float64]bool{}
	deadline := time.After(25 * time.Second)
	for len(lat) < 15 {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatal("websocket closed")
			}
			if f.UpstreamStatus == "LIVE" && f.Reported.OperatingState == "LIVE" {
				positions[f.Reported.Position.Y] = true
				if f.Reported.Load <= 0 {
					t.Fatalf("LIVE frame without load: %+v", f.Reported)
				}
				lat = append(lat, time.Since(f.UpdatedAt))
			}
		case <-deadline:
			t.Fatalf("only %d LIVE frames over WebSocket", len(lat))
		}
	}
	if len(positions) < 5 {
		t.Fatalf("position did not move across LIVE frames: %v", positions)
	}
	var samples int
	_ = e.db.QueryRow(`SELECT count(*) FROM telemetry_samples WHERE device_id='SIM-R7-0001' AND boot_id IS NOT NULL`).Scan(&samples)
	simRecord(t, "REAL_WEBSOCKET_E2E", map[string]any{"liveFrames": len(lat), "distinctY": len(positions), "telemetrySamples": samples})

	// End-to-end latency: device SentAt (LIVE envelope in telemetry_samples) → cloud receive.
	var p50, p95, maxMs float64
	_ = e.db.QueryRow(`SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM cloud_received_at-source_sent_at)*1000),
	  percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM cloud_received_at-source_sent_at)*1000),
	  max(extract(epoch FROM cloud_received_at-source_sent_at)*1000) FROM telemetry_samples WHERE device_id='SIM-R7-0001'`).Scan(&p50, &p95, &maxMs)
	simRecord(t, "LATENCY_EDGE_TO_CLOUD_MS", map[string]any{"p50": p50, "p95": p95, "max": maxMs, "samples": samples})

	if c := e.call(admin, "POST", "/api/v1/live-sessions/"+lease.SessionId+"/renew", nil); c != 200 {
		t.Fatalf("renew = %d", c)
	}
	if c := e.call(admin, "DELETE", "/api/v1/live-sessions/"+lease.SessionId, nil); c != 204 {
		t.Fatalf("close = %d", c)
	}
	e.until("grace elapsed, WATCH_STOP ACKed, SILENT", 30*time.Second, func() bool {
		s, _ := e.leases.Status(e.ctx, "SIM-R7-0001")
		v, _ := e.shadowDB("SIM-R7-0001")
		return s == "SILENT" && v.Reported.OperatingState == "SILENT"
	})
	e.stopDevice("SIM-R7-0001")
	e.until("graceful DEATH", 10*time.Second, func() bool {
		v, _ := e.shadowDB("SIM-R7-0001")
		return v.Presence.Connectivity == "OFFLINE" && v.Presence.DisconnectReason == "GRACEFUL"
	})
	// Item 12: the bound asset cannot be retired while the runtime binding is active.
	var version int64
	_ = e.db.QueryRow(`SELECT version FROM asset_devices WHERE id=$1`, e.assets["SIM-R7-0001"]).Scan(&version)
	if err := asset.RetireAsset(e.ctx, e.db, e.assets["SIM-R7-0001"], version, "u", "r"); err != asset.ErrActiveBinding {
		t.Fatalf("retire bound asset: %v", err)
	}
	simRecord(t, "RUNTIME_BINDING_E2E", "registered via runtime-identity, bound PLC, retire refused with active binding")
}

// 3 independent devices: normal, alarm-storm and network loss on ONE device only. Alarms are
// non-empty, deduplicated by messageId, attributed to the right device/project, and the other two
// devices are unaffected by the third one's outage (LWT only on the faulted device).
func TestR7Sim3DevicesIndependent(t *testing.T) {
	e := newSimEnv(t, 30*time.Second, 5*time.Second, []simSpec{
		{project: "R7-S3", runtime: "SIM-R7-N", source: "SIMULATION"},
		{project: "R7-S3", runtime: "SIM-R7-A", source: "SIMULATION"},
		{project: "R7-S3", runtime: "SIM-R7-O", source: "SIMULATION"},
	})
	viewer := e.user("s3-viewer", "R7-S3", "viewer")
	e.startDevice("SIM-R7-N", "normal")
	e.startDevice("SIM-R7-A", "alarm-storm")
	e.startDevice("SIM-R7-O", "normal")
	e.until("3 connected", 20*time.Second, func() bool {
		return e.connected("SIM-R7-N") && e.connected("SIM-R7-A") && e.connected("SIM-R7-O")
	})
	e.until("alarm-storm alarms stored", 20*time.Second, func() bool {
		var n int
		_ = e.db.QueryRow(`SELECT count(*) FROM alarms WHERE device_id='SIM-R7-A'`).Scan(&n)
		return n >= 3
	})
	// Network loss for SIM-R7-O only: drop its TCP connections and refuse reconnects.
	p := e.proxies["SIM-R7-O"]
	p.refusing.Store(true)
	p.dropAll()
	simRecord(t, "FAULT", map[string]any{"target": "SIM-R7-O", "kind": "tcp_drop_and_refuse"})
	e.until("LWT (UNEXPECTED) on the faulted device after will delay", 30*time.Second, func() bool {
		v, _ := e.shadowDB("SIM-R7-O")
		return v.Presence.Connectivity == "OFFLINE" && v.Presence.DisconnectReason == "UNEXPECTED"
	})
	if !e.connected("SIM-R7-N") || !e.connected("SIM-R7-A") {
		t.Fatal("an unrelated device went offline")
	}
	var alarms []contract.Envelope
	if c := e.call(viewer, "GET", "/api/v1/alarms", &alarms); c != 200 || len(alarms) == 0 {
		t.Fatalf("alarms over BFF = %d len %d", c, len(alarms))
	}
	for _, a := range alarms {
		if a.DeviceId != "SIM-R7-A" || a.ProjectId != "R7-S3" || a.Kind != "ALARM" {
			t.Fatalf("misattributed alarm %+v", a)
		}
	}
	var filtered []contract.Envelope
	if e.call(viewer, "GET", "/api/v1/alarms?deviceId=SIM-R7-N", &filtered) != 200 || len(filtered) != 0 {
		t.Fatalf("filter by normal device returned %d alarms", len(filtered))
	}
	var dupes int
	_ = e.db.QueryRow(`SELECT count(*) FROM (SELECT payload_json->>'messageId' m FROM alarms GROUP BY 1 HAVING count(*)>1) x`).Scan(&dupes)
	simRecord(t, "ALARM_NONEMPTY_E2E", map[string]any{"alarms": len(alarms), "duplicates": dupes})
	if dupes != 0 {
		t.Fatalf("duplicate alarm rows: %d", dupes)
	}
	// Recovery: allow reconnects, the Agent's autopaho reconnects without a new boot.
	var before contract.Shadow
	before, _ = e.shadowDB("SIM-R7-O")
	p.refusing.Store(false)
	e.until("short-outage device reconnects", 40*time.Second, func() bool { return e.connected("SIM-R7-O") })
	after, _ := e.shadowDB("SIM-R7-O")
	simRecord(t, "OFFLINE_RECOVERY", map[string]any{"bootSeqBefore": before.Presence.BootSeq, "bootSeqAfter": after.Presence.BootSeq})
	// Summary/time window: alarm counts by source over the BFF summary.
	var sum contract.OperationsSummary
	if c := e.call(viewer, "GET", "/api/v1/operations/summary?alarmWindow=PT1H", &sum); c != 200 {
		t.Fatalf("summary = %d", c)
	}
	simRecord(t, "SUMMARY", map[string]any{"bound": sum.Metrics.Bound, "real": sum.Metrics.Real, "alarm": sum.Metrics.Alarm})
	if sum.Metrics.Bound.SIMULATION != 3 || sum.Metrics.Bound.REAL != 0 {
		t.Fatalf("runtime source counts = %+v", sum.Metrics.Bound)
	}
}

// Critical event spool: the cloud runtime is stopped while an alarm-storm device produces ALARMs
// (broker PUBACK does not remove the spool head); on restart the stored ALARMs are acknowledged
// (EVENT_STORED_ACK) and drained; re-delivery of the same messageId never creates a second row.
func TestR7SimSpoolAckLossNoDuplicates(t *testing.T) {
	e := newSimEnv(t, 30*time.Second, 5*time.Second, []simSpec{{project: "R7-SP", runtime: "SIM-R7-SP", source: "SIMULATION"}})
	e.startDevice("SIM-R7-SP", "alarm-storm")
	e.until("connected", 20*time.Second, func() bool { return e.connected("SIM-R7-SP") })
	e.stopCloud()
	simRecord(t, "FAULT", map[string]any{"target": "cloud-runtime", "kind": "stopped (no EVENT_STORED_ACK)"})
	spool := e.root + "/spool/SIM-R7-SP.json"
	e.until("ALARMs accumulate in the Agent spool while unacknowledged", 30*time.Second, func() bool {
		b, err := os.ReadFile(spool)
		var q []contract.Envelope
		return err == nil && json.Unmarshal(b, &q) == nil && len(q) >= 3
	})
	var queued []contract.Envelope
	b, _ := os.ReadFile(spool)
	_ = json.Unmarshal(b, &queued)
	var storedBefore int
	_ = e.db.QueryRow(`SELECT count(*) FROM alarms WHERE device_id='SIM-R7-SP'`).Scan(&storedBefore)
	e.startCloud()
	e.until("spool drained after application ACK", 60*time.Second, func() bool {
		var stored int
		_ = e.db.QueryRow(`SELECT count(*) FROM alarms WHERE message_id=$1`, queued[0].MessageId).Scan(&stored)
		return stored == 1
	})
	time.Sleep(6 * time.Second) // allow retries of in-flight heads
	var dupes, total int
	_ = e.db.QueryRow(`SELECT count(*) FROM (SELECT payload_json->>'messageId' FROM alarms GROUP BY 1 HAVING count(*)>1) x`).Scan(&dupes)
	_ = e.db.QueryRow(`SELECT count(*) FROM alarms WHERE device_id='SIM-R7-SP'`).Scan(&total)
	simRecord(t, "OFFLINE_SPOOL_ACK", map[string]any{"queuedWhileCloudDown": len(queued), "storedBefore": storedBefore, "storedAfter": total})
	simRecord(t, "DUPLICATE_EVENT_COUNT", dupes)
	if dupes != 0 {
		t.Fatalf("duplicate alarm rows: %d", dupes)
	}
}

// MQTT abuse: Topic of another device, another project, a device with a revoked/unknown
// certificate, wrong payload, out-of-order bootSeq/sequence and a stale boot message. Every one is
// refused by the broker ACL or ignored by the cloud; PostgreSQL shows no effect.
func TestR7SimMQTTAbuseAndOrdering(t *testing.T) {
	e := newSimEnv(t, 30*time.Second, 5*time.Second, []simSpec{
		{project: "R7-X1", runtime: "SIM-R7-X1", source: "SIMULATION"},
		{project: "R7-X2", runtime: "SIM-R7-X2", source: "SIMULATION"},
	})
	e.startDevice("SIM-R7-X1", "normal")
	e.until("X1 connected", 20*time.Second, func() bool { return e.connected("SIM-R7-X1") })
	x1, _ := e.shadowDB("SIM-R7-X1")

	// A certificate the broker CA never issued.
	rogue := newSimPKI(t, t.TempDir())
	rogue.issue(t, rogue.dir, "SIM-R7-X1", false)
	if _, err := e.raw("SIM-R7-X1", rogue.dir); err == nil {
		t.Fatal("foreign CA certificate connected")
	}
	// A valid certificate for an identity that is not in the ACL.
	e.pki.issue(t, e.pki.dir, "SIM-R7-GHOST", false)
	ghost, err := e.raw("SIM-R7-GHOST", e.pki.dir)
	if err == nil {
		topic, _ := contract.Topic("demo", "R7-X1", "SIM-R7-X1", "shadow/reported")
		b, _ := json.Marshal(envelopeFor("SIM-R7-X1", "R7-X1", "BIRTH", "ghost", 999, 1, contract.BirthPayload{EdgeHealth: "EDGE_HEALTHY", ControllerHealth: "CONTROLLER_HEALTHY", SensorHealth: "HEALTHY", Reported: contract.ReportedState{OperatingState: "SILENT", Mode: "AUTO", AlarmState: "NONE"}}))
		rc, _ := ghost.publish(e.ctx, topic, b)
		simRecord(t, "ACL_GHOST_PUBLISH_RC", rc)
		ghost.close()
	}
	// X2 (registered, own cert) publishes to X1's topic and to a topic in X1's project.
	x2, err := e.raw("SIM-R7-X2", e.pki.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer x2.close()
	forged := envelopeFor("SIM-R7-X1", "R7-X1", "BIRTH", "forged", 999, 1, contract.BirthPayload{EdgeHealth: "EDGE_HEALTHY", ControllerHealth: "CONTROLLER_HEALTHY", SensorHealth: "HEALTHY", Reported: contract.ReportedState{OperatingState: "SILENT", Mode: "MANUAL", AlarmState: "NONE"}})
	fb, _ := json.Marshal(forged)
	t1, _ := contract.Topic("demo", "R7-X1", "SIM-R7-X1", "shadow/reported")
	rc1, _ := x2.publish(e.ctx, t1, fb)
	alarmTopic, _ := contract.Topic("demo", "R7-X1", "SIM-R7-X1", "alarm")
	fa, _ := json.Marshal(envelopeFor("SIM-R7-X1", "R7-X1", "ALARM", "forged", 999, 2, contract.AlarmPayload{Code: "FORGED", Text: "forged"}))
	rc2, _ := x2.publish(e.ctx, alarmTopic, fa)
	// Own topic but the envelope claims another device/project (topic/identity mismatch).
	own, _ := contract.Topic("demo", "R7-X2", "SIM-R7-X2", "alarm")
	rc3, _ := x2.publish(e.ctx, own, fa)
	// Own topic, malformed payload.
	rc4, _ := x2.publish(e.ctx, own, []byte(`{"schemaVersion":1,"kind":"ALARM","unknown":true}`))
	simRecord(t, "MQTT_REAL_TLS_ACL", map[string]any{"crossDeviceShadowRC": rc1, "crossDeviceAlarmRC": rc2, "identityMismatchRC": rc3, "badPayloadRC": rc4})
	time.Sleep(2 * time.Second)
	var forgedAlarms int
	_ = e.db.QueryRow(`SELECT count(*) FROM alarms WHERE payload_json#>>'{payload,code}'='FORGED'`).Scan(&forgedAlarms)
	now, _ := e.shadowDB("SIM-R7-X1")
	if forgedAlarms != 0 || now.Presence.BootId == "forged" || now.Reported.Mode == "MANUAL" {
		t.Fatalf("forged traffic had an effect: alarms=%d shadow=%+v", forgedAlarms, now.Presence)
	}

	// Ordering: as X2 itself (own topic, own identity). Establish boot 5 seq 10, then try older.
	bootID := "r7-order"
	send := func(seq, bootSeq int64, id string, mode string) {
		m := envelopeFor("SIM-R7-X2", "R7-X2", "SNAPSHOT", id, bootSeq, seq, contract.SnapshotPayload{EdgeHealth: "EDGE_HEALTHY", ControllerHealth: "CONTROLLER_HEALTHY", SensorHealth: "HEALTHY", Reported: contract.ReportedState{OperatingState: "SILENT", Mode: mode, AlarmState: "NONE"}})
		b, _ := json.Marshal(m)
		topic, _ := contract.Topic("demo", "R7-X2", "SIM-R7-X2", "shadow/reported")
		if _, err := x2.publish(e.ctx, topic, b); err != nil {
			t.Fatal(err)
		}
	}
	birth := envelopeFor("SIM-R7-X2", "R7-X2", "BIRTH", bootID, 5, 1, contract.BirthPayload{EdgeHealth: "EDGE_HEALTHY", ControllerHealth: "CONTROLLER_HEALTHY", SensorHealth: "HEALTHY", Reported: contract.ReportedState{OperatingState: "SILENT", Mode: "AUTO", AlarmState: "NONE"}})
	bb, _ := json.Marshal(birth)
	if _, err := x2.publish(e.ctx, mustTopic("R7-X2", "SIM-R7-X2", "shadow/reported"), bb); err != nil {
		t.Fatal(err)
	}
	send(10, 5, bootID, "AUTO")
	e.until("X2 at boot 5 seq 10", 10*time.Second, func() bool {
		v, _ := e.shadowDB("SIM-R7-X2")
		return v.Presence.BootSeq == 5 && v.Presence.Sequence == 10
	})
	send(9, 5, bootID, "MANUAL")        // older sequence
	send(10, 5, bootID, "MANUAL")       // replay of the same sequence
	send(50, 4, "old-boot", "MANUAL")   // older boot session
	send(11, 5, "other-boot", "MANUAL") // same bootSeq, different bootId
	time.Sleep(2 * time.Second)
	v, _ := e.shadowDB("SIM-R7-X2")
	simRecord(t, "ORDERING", map[string]any{"bootSeq": v.Presence.BootSeq, "sequence": v.Presence.Sequence, "mode": v.Reported.Mode})
	if v.Presence.BootSeq != 5 || v.Presence.Sequence != 10 || v.Reported.Mode != "AUTO" || v.Presence.BootId != bootID {
		t.Fatalf("out-of-order message applied: %+v %+v", v.Presence, v.Reported)
	}
	_ = x1
}

func mustTopic(project, id, suffix string) string {
	t, _ := contract.Topic("demo", project, id, suffix)
	return t
}

// 5 devices in 2 projects, different roles: a viewer of P1 sees only P1 devices/alarms and gets
// 404 on P2 devices; an operator can WATCH; a viewer cannot; revoking the grant closes an open
// WebSocket within the next frame interval; logout invalidates the stream; two users watch one
// device; a browser disconnect (WS close) leaves the lease to expire by TTL.
func TestR7Sim5DevicesRolesIsolationRevocation(t *testing.T) {
	e := newSimEnv(t, 8*time.Second, 3*time.Second, []simSpec{
		{project: "R7-P1", runtime: "SIM-R7-P1A", source: "SIMULATION"},
		{project: "R7-P1", runtime: "SIM-R7-P1B", source: "SIMULATION"},
		{project: "R7-P1", runtime: "TEST-R7-P1C", source: "TEST"},
		{project: "R7-P2", runtime: "SIM-R7-P2A", source: "SIMULATION"},
		{project: "R7-P2", runtime: "SIM-R7-P2B", source: "SIMULATION"},
	})
	for _, id := range []string{"SIM-R7-P1A", "SIM-R7-P1B", "TEST-R7-P1C", "SIM-R7-P2A", "SIM-R7-P2B"} {
		e.startDevice(id, "normal")
	}
	e.until("5 connected", 30*time.Second, func() bool {
		for _, id := range []string{"SIM-R7-P1A", "SIM-R7-P1B", "TEST-R7-P1C", "SIM-R7-P2A", "SIM-R7-P2B"} {
			if !e.connected(id) {
				return false
			}
		}
		return true
	})
	viewer := e.user("p1-viewer", "R7-P1", "viewer")
	op := e.user("p1-operator", "R7-P1", "operator")
	op2 := e.user("p1-operator2", "R7-P1", "operator")
	p2 := e.user("p2-admin", "R7-P2", "platform_admin")

	var list []contract.Shadow
	e.call(viewer, "GET", "/api/v1/devices", &list)
	ids := map[string]bool{}
	for _, d := range list {
		ids[d.DeviceId] = true
	}
	if len(list) != 3 || ids["SIM-R7-P2A"] || ids["SIM-R7-P2B"] {
		t.Fatalf("P1 viewer device list = %v", ids)
	}
	cross := []int{e.call(viewer, "GET", "/api/v1/devices/SIM-R7-P2A", nil), e.call(p2, "GET", "/api/v1/devices/SIM-R7-P1A", nil), e.call(p2, "POST", "/api/v1/devices/SIM-R7-P1A/live-sessions", nil)}
	if _, resp, err := e.dialStream(p2, "SIM-R7-P1A"); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Fatalf("cross-project stream = %v %v", resp, err)
	}
	simRecord(t, "CROSS_PROJECT_ISOLATION", map[string]any{"viewerSees": len(list), "crossStatuses": cross})
	for _, c := range cross {
		if c != 404 {
			t.Fatalf("cross-project status %v", cross)
		}
	}
	if c := e.call(viewer, "POST", "/api/v1/devices/SIM-R7-P1A/live-sessions", nil); c != 403 {
		t.Fatalf("viewer WATCH = %d, want 403", c)
	}
	// Two operators watch the same device; one generation serves both.
	var l1, l2 contract.LiveSession
	if e.call(op, "POST", "/api/v1/devices/SIM-R7-P1A/live-sessions", &l1) != 201 || e.call(op2, "POST", "/api/v1/devices/SIM-R7-P1A/live-sessions", &l2) != 201 {
		t.Fatal("operators cannot WATCH")
	}
	e.until("multi-viewer LIVE", 20*time.Second, func() bool { s, _ := e.leases.Status(e.ctx, "SIM-R7-P1A"); return s == "LIVE" })
	var starts int
	_ = e.db.QueryRow(`SELECT count(*) FROM device_commands WHERE device_id='SIM-R7-P1A' AND kind='WATCH_START'`).Scan(&starts)
	if starts != 1 {
		t.Fatalf("two viewers issued %d WATCH_START commands", starts)
	}
	// Viewer of op opens a stream, then op's grant is revoked → stream closed by policy.
	conn, _, err := e.dialStream(op, "SIM-R7-P1A")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(e.ctx); err != nil {
		t.Fatal("first frame", err)
	}
	if err := identity.ChangeGrant(e.ctx, e.db, op.id, "R7-P1", "", true, auditlog.Event{Actor: "r7-sim"}); err != nil {
		t.Fatal(err)
	}
	closed := time.Now()
	var closeErr error
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
		_, _, closeErr = conn.Read(ctx)
		cancel()
		if closeErr != nil {
			break
		}
	}
	status := websocket.CloseStatus(closeErr)
	simRecord(t, "REVOKED_STREAM_CLOSED", map[string]any{"status": int(status), "afterMs": time.Since(closed).Milliseconds()})
	if status != websocket.StatusPolicyViolation {
		t.Fatalf("revoked stream close = %v (%v)", status, closeErr)
	}
	if e.call(op, "POST", "/api/v1/live-sessions/"+l1.SessionId+"/renew", nil) != 404 {
		t.Fatal("revoked user renewed a lease")
	}
	// Logout closes access: op2 logs out, its stream is refused.
	if e.call(op2, "POST", "/auth/logout", nil) >= 400 {
		t.Fatal("logout failed")
	}
	if _, resp, err := e.dialStream(op2, "SIM-R7-P1A"); err == nil || resp == nil || resp.StatusCode < 400 {
		t.Fatalf("stream after logout = %v %v", resp, err)
	}
	// Browser disconnect: nobody renews; TTL+grace returns the device to SILENT.
	e.until("TTL expiry returns SILENT", 40*time.Second, func() bool { s, _ := e.leases.Status(e.ctx, "SIM-R7-P1A"); return s == "SILENT" })
	// Source separation in the summary: P1 (2 SIM + 1 TEST) for the viewer.
	var sum contract.OperationsSummary
	e.call(viewer, "GET", "/api/v1/operations/summary", &sum)
	simRecord(t, "REAL_SOURCE_MISCOUNT", map[string]any{"bound": sum.Metrics.Bound, "realConnected": sum.Metrics.Real.Connected})
	if sum.Metrics.Bound.REAL != 0 || sum.Metrics.Bound.SIMULATION != 2 || sum.Metrics.Bound.TEST != 1 || sum.Metrics.Real.Connected != 0 {
		t.Fatalf("source counts = %+v real=%+v", sum.Metrics.Bound, sum.Metrics.Real)
	}
}

// R7/R8 LIVE latency target (Edge sentAt → Cloud receive, P95). No separate document fixes an
// edge→cloud number; the protocol does: LIVE is sampled at exactly 1 Hz (docs/02-云边协议/实时会话协议.md),
// so a sample must reach the cloud within one sampling period — otherwise the live view lags a full
// sample behind. Measured in R7: P95 19.2 ms.
const simLiveP95TargetMs = 1000.0

// 20 devices: capacity and (optionally) a long soak. R7_SIM_SOAK_MINUTES=60 runs the 60-minute
// soak (manual / server evidence); the default CI run holds 20 devices for R7_SIM_SOAK_SECONDS (90).
// During the run: every device stays registered, a rolling WATCH keeps one device LIVE, two
// devices drop/reconnect every 30 s, the cloud runtime restarts once, one device runs alarm-storm.
// Hard assertions (R8): every SQL read succeeds; the run really lasted the requested time; the
// faults really happened (drops counted per proxy, a reconnect after each drop, one cloud restart);
// persisted LIVE samples exist only for the watched device and match the sampling policy
// (telemetry.SampleInterval) for the duration; P95 latency within simLiveP95TargetMs; alarms
// present with zero duplicates; all 20 reconnected. Resource trend is recorded every minute.
func TestR7Sim20DevicesSoak(t *testing.T) {
	specs := []simSpec{}
	for i := 1; i <= 20; i++ {
		project := "R7-C" + strconv.Itoa((i-1)/5+1)
		specs = append(specs, simSpec{project: project, runtime: fmt.Sprintf("SIM-R7-C%02d", i), source: "SIMULATION"})
	}
	e := newSimEnv(t, 30*time.Second, 5*time.Second, specs)
	start := time.Now()
	for _, s := range specs {
		scenario := "normal"
		if s.runtime == "SIM-R7-C20" {
			scenario = "alarm-storm"
		}
		e.startDevice(s.runtime, scenario)
	}
	all := func() int {
		n := 0
		for _, s := range specs {
			if e.connected(s.runtime) {
				n++
			}
		}
		return n
	}
	e.until("20 connected", 60*time.Second, func() bool { return all() == 20 })
	connectAll := time.Since(start)
	admin := e.user("c-admin", "R7-C1", "platform_admin")
	duration := simSoakDuration()
	q := func(label string, query string, args []any, dest ...any) {
		t.Helper()
		if err := e.db.QueryRow(query, args...).Scan(dest...); err != nil {
			t.Fatalf("soak query %s: %v", label, err)
		}
	}
	resources := func(phase string) map[string]any {
		var ms goruntime.MemStats
		goruntime.ReadMemStats(&ms)
		var conns, alarmsNow, samplesNow int
		q("pg connections", `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()`, nil, &conns)
		q("alarms so far", `SELECT count(*) FROM alarms WHERE device_id LIKE 'SIM-R7-C%'`, nil, &alarmsNow)
		q("samples so far", `SELECT count(*) FROM telemetry_samples WHERE device_id LIKE 'SIM-R7-C%'`, nil, &samplesNow)
		v := map[string]any{"phase": phase, "elapsedSec": int(time.Since(start).Seconds()), "goroutines": goruntime.NumGoroutine(), "heapMB": ms.HeapAlloc / (1 << 20), "pgConnections": conns, "alarms": alarmsNow, "liveSamples": samplesNow, "connected": all()}
		simRecord(t, "SOAK_RESOURCES", v)
		return v
	}
	baseline := resources("start")
	var lease contract.LiveSession
	if e.call(admin, "POST", "/api/v1/devices/SIM-R7-C01/live-sessions", &lease) != 201 {
		t.Fatal("watch")
	}
	watchStart := time.Now()
	end := watchStart.Add(duration)
	var minConnected = 20
	var cloudRestarted bool
	ticks, scheduledDrops := 0, 0
	nextTrend := time.Now().Add(time.Minute)
	for time.Now().Before(end) {
		time.Sleep(5 * time.Second)
		ticks++
		if code := e.call(admin, "POST", "/api/v1/live-sessions/"+lease.SessionId+"/renew", nil); code != 200 {
			t.Fatalf("renew failed during soak after %s: HTTP %d", time.Since(watchStart).Round(time.Second), code)
		}
		if ticks%6 == 0 { // every 30 s: short network drop on two devices
			for _, id := range []string{"SIM-R7-C07", "SIM-R7-C13"} {
				e.proxies[id].dropAll()
			}
			scheduledDrops++
		}
		if !cloudRestarted && time.Until(end) < duration/2 {
			e.stopCloud()
			e.startCloud()
			cloudRestarted = true
			simRecord(t, "FAULT", map[string]any{"target": "cloud-runtime", "kind": "restart"})
		}
		if c := all(); c < minConnected {
			minConnected = c
		}
		if time.Now().After(nextTrend) {
			resources("running")
			nextTrend = nextTrend.Add(time.Minute)
		}
	}
	held := time.Since(watchStart)
	// Recovery is bounded: every drop must be followed by a new broker connection within 60 s (the
	// shadow alone cannot show it — a short drop stays within the delayed Will and never goes offline).
	for _, id := range []string{"SIM-R7-C07", "SIM-R7-C13"} {
		p := e.proxies[id]
		e.until(id+" reconnected after every drop", 60*time.Second, func() bool { return p.accepts.Load()-1 >= p.drops.Load() })
	}
	e.until("all 20 reconnected at the end", 60*time.Second, func() bool { return all() == 20 })
	final := resources("end")

	// Faults really happened and every dropped device reconnected after each drop.
	faults := map[string]any{}
	for _, id := range []string{"SIM-R7-C07", "SIM-R7-C13"} {
		p := e.proxies[id]
		drops, accepts := p.drops.Load(), p.accepts.Load()
		faults[id] = map[string]int64{"drops": drops, "reconnects": accepts - 1}
		if scheduledDrops == 0 || drops < int64(scheduledDrops) {
			t.Fatalf("%s: %d drops recorded, %d scheduled — fault injection did not happen", id, drops, scheduledDrops)
		}
		// the first accept is the initial connection; every drop must be followed by a new one
		if accepts-1 < int64(scheduledDrops) {
			t.Fatalf("%s: %d reconnects after %d drops — connection not recovered", id, accepts-1, scheduledDrops)
		}
	}
	if !cloudRestarted {
		t.Fatal("cloud runtime restart did not happen")
	}
	if held < duration {
		t.Fatalf("soak held %s, requested %s", held, duration)
	}

	// LIVE samples: only the watched device, persisted per the sampling policy for the whole run.
	var samples, sampleDevices, otherDevices int
	var p50, p95, p99 float64
	q("latency", `SELECT count(*), coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM cloud_received_at-source_sent_at)*1000),0),
	  coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM cloud_received_at-source_sent_at)*1000),0),
	  coalesce(percentile_cont(0.99) WITHIN GROUP (ORDER BY extract(epoch FROM cloud_received_at-source_sent_at)*1000),0)
	  FROM telemetry_samples WHERE device_id LIKE 'SIM-R7-C%'`, nil, &samples, &p50, &p95, &p99)
	q("sample devices", `SELECT count(DISTINCT device_id), count(DISTINCT device_id) FILTER (WHERE device_id<>'SIM-R7-C01') FROM telemetry_samples WHERE device_id LIKE 'SIM-R7-C%'`, nil, &sampleDevices, &otherDevices)
	interval := telemetry.SampleInterval
	expected := int(held / interval)
	// lower bound: half of the ideal count (start-up of LIVE, the cloud restart gap and the reconnect
	// windows of the run); upper bound: the policy never stores more than one sample per interval
	// (+1 at the start and +1 after the cloud restart resets the in-memory sampler).
	minSamples, maxSamples := max(1, expected/2), expected+2
	if samples < minSamples || samples > maxSamples || sampleDevices != 1 || otherDevices != 0 {
		t.Fatalf("LIVE samples=%d (want %d..%d for %s at one per %s) devices=%d others=%d", samples, minSamples, maxSamples, held.Round(time.Second), interval, sampleDevices, otherDevices)
	}
	if p95 <= 0 || p95 > simLiveP95TargetMs {
		t.Fatalf("LIVE latency P95 %.1f ms, target ≤ %.0f ms", p95, simLiveP95TargetMs)
	}

	// Alarms: present (alarm-storm), never duplicated (critical-event dedupe by messageId).
	var dupes, alarms, distinctAlarms, shadows, extraDevices int
	q("duplicate alarms", `SELECT count(*) FROM (SELECT payload_json->>'messageId' FROM alarms GROUP BY 1 HAVING count(*)>1) x`, nil, &dupes)
	q("alarms", `SELECT count(*), count(DISTINCT payload_json->>'messageId') FROM alarms WHERE device_id='SIM-R7-C20'`, nil, &alarms, &distinctAlarms)
	q("shadows", `SELECT count(*) FROM device_shadow WHERE device_id LIKE 'SIM-R7-C%'`, nil, &shadows)
	q("alarms of other devices", `SELECT count(*) FROM alarms WHERE device_id LIKE 'SIM-R7-C%' AND device_id<>'SIM-R7-C20'`, nil, &extraDevices)
	// alarm-storm emits one alarm per 2 s; spool + application ACK may hold some back during faults.
	minAlarms := max(1, int(held/(2*time.Second))/4)
	simRecord(t, "SIM_20_DEVICES", map[string]any{"durationSec": int(duration.Seconds()), "heldSec": int(held.Seconds()), "connectAllMs": connectAll.Milliseconds(),
		"minConnectedDuringFaults": minConnected, "faults": faults, "scheduledDropRounds": scheduledDrops, "cloudRestarted": cloudRestarted, "shadows": shadows,
		"alarms": alarms, "distinctAlarms": distinctAlarms, "duplicateAlarms": dupes, "alarmsOtherDevices": extraDevices,
		"liveSamples": samples, "liveSamplesExpected": map[string]int{"min": minSamples, "max": maxSamples}, "latencyMs": map[string]float64{"p50": p50, "p95": p95, "p99": p99, "p95Target": simLiveP95TargetMs},
		"resources": map[string]any{"start": baseline, "end": final}})
	if dupes != 0 || alarms != distinctAlarms || shadows != 20 || alarms < minAlarms || extraDevices != 0 {
		t.Fatalf("soak invariants: dupes=%d alarms=%d distinct=%d (min %d) shadows=%d otherDeviceAlarms=%d", dupes, alarms, distinctAlarms, minAlarms, shadows, extraDevices)
	}
	_ = platform.ID
}
