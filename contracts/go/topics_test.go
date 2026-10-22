package contract

import (
	"encoding/json"
	"os"
	"testing"
)

func TestTopicBoundary(t *testing.T) {
	for _, id := range []string{"../x", "a/+", "a#", "", "中文"} {
		if _, err := Topic("demo", "project", id, "alarm"); err == nil {
			t.Fatal("accepted invalid id", id)
		}
	}
	for suffix := range Topics {
		v, err := Topic("demo", "Project-A", "SIM-001", suffix)
		if err != nil {
			t.Fatal(err)
		}
		_, _, d, s, e := ParseTopic(v)
		if e != nil || d != "SIM-001" || s != suffix {
			t.Fatal(v)
		}
	}
}
func TestTypedTopicKindPayloadBoundary(t *testing.T) {
	raw, err := os.ReadFile("../../tests/fixtures/birth.json")
	if err != nil {
		t.Fatal(err)
	}
	topic, _ := Topic("demo", "Project-A", "SIM-001", "shadow/reported")
	m, err := Decode(topic, raw)
	if err != nil {
		t.Fatal(err)
	}
	live, _ := Topic("demo", "Project-A", "SIM-001", "telemetry/live")
	if _, err = Decode(live, raw); err == nil {
		t.Fatal("topic/kind mismatch accepted")
	}
	m.Kind = "WATCH_STOP"
	m = WithPayload(m, WatchCommandPayload{LeaseId: "lease-1", Generation: 2, TtlSeconds: 5, TelemetryHz: 1, ExpiresAt: m.SentAt})
	stop, _ := Topic("demo", "Project-A", "SIM-001", "command/watch/stop")
	raw, _ = json.Marshal(m)
	if _, err = Decode(stop, raw); err != nil {
		t.Fatal(err)
	}
	m.Kind = "LIVE"
	raw, _ = json.Marshal(m)
	if _, err = Decode(live, raw); err == nil {
		t.Fatal("wrong payload accepted")
	}
	m.Kind = "WATCH_STOP"
	m.Payload = json.RawMessage(`{"leaseId":"lease-1","generation":2,"ttlSeconds":5,"telemetryHz":1}`)
	raw, _ = json.Marshal(m)
	if _, err = Decode(stop, raw); err == nil {
		t.Fatal("missing field accepted")
	}
	var v map[string]any
	_ = json.Unmarshal(raw, &v)
	delete(v, "correlationId")
	raw, _ = json.Marshal(v)
	if _, err = Decode(stop, raw); err == nil {
		t.Fatal("missing envelope field accepted")
	}
}
