// These tests run only through the Compose acceptance entrypoint against dev TLS.
package mqtt_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"github.com/eclipse/paho.golang/packets"
	"github.com/eclipse/paho.golang/paho"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/edge/agent/spool"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func enabled(t *testing.T) {
	t.Helper()
	if os.Getenv("T05_INTEGRATION") != "1" {
		t.Skip("requires the Compose integration harness")
	}
}
func api(t *testing.T, path string, target any) int {
	t.Helper()
	req, e := http.NewRequest("GET", os.Getenv("T05_API_URL")+"/api/v1/"+path, nil)
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("T05_API_TOKEN"))
	client := http.Client{Timeout: 3 * time.Second}
	res, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.StatusCode == 200 && target != nil {
		if e = json.NewDecoder(res.Body).Decode(target); e != nil {
			t.Fatal(e)
		}
	}
	return res.StatusCode
}
func until(t *testing.T, label string, f func() bool) {
	t.Helper()
	end := time.Now().Add(15 * time.Second)
	for time.Now().Before(end) {
		if f() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out:", label)
}
func fixture(t *testing.T) contract.Envelope {
	t.Helper()
	var old contract.Shadow
	api(t, "devices/TEST-001/shadow", &old)
	return contract.Envelope{SchemaVersion: 1, MessageId: platform.ID(), TenantId: "demo", ProjectId: "Project-A", DeviceId: "TEST-001", BootId: platform.ID(), BootSeq: old.Presence.BootSeq + 1, Sequence: 1, SentAt: time.Now().UTC(), Kind: "BIRTH"}
}
func birth(m contract.Envelope) contract.Envelope {
	return contract.WithPayload(m, contract.BirthPayload{EdgeHealth: "EDGE_HEALTHY", ControllerHealth: "CONTROLLER_HEALTHY", SensorHealth: "HEALTHY", SoftwareVersion: "test-0.5", Reported: contract.ReportedState{OperatingState: "SILENT", Mode: "AUTO", AlarmState: "NONE", Position: contract.Position{X: 10, Y: 20, Z: 5}, Load: 2.5}})
}

type connection struct {
	client *paho.Client
	conn   net.Conn
}

func connect(t *testing.T, id, identity string, will *contract.Envelope, received chan contract.Envelope) *connection {
	t.Helper()
	root := os.Getenv("T05_PROJECT_ROOT")
	group := "devices"
	if identity == "cloud-api" {
		group = "cloud"
	}
	tlsConfig, e := platform.TLS(filepath.Join(root, "deploy/compose/runtime/certs", group), identity)
	if e != nil {
		t.Fatal(e)
	}
	c, e := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", "127.0.0.1:8883", tlsConfig)
	if e != nil {
		t.Fatal(e)
	}
	client := paho.NewClient(paho.ClientConfig{ClientID: id, Conn: packets.NewThreadSafeConn(c), OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(p paho.PublishReceived) (bool, error) {
		m, e := contract.Decode(p.Packet.Topic, p.Packet.Payload)
		if e == nil && received != nil {
			select {
			case received <- m:
			default:
			}
		}
		return true, nil
	}}})
	expiry, delay := uint32(3600), uint32(5)
	packet := &paho.Connect{ClientID: id, CleanStart: false, KeepAlive: 10, Properties: &paho.ConnectProperties{SessionExpiryInterval: &expiry}}
	if will != nil {
		b, _ := json.Marshal(will)
		topic, _ := contract.Topic(will.TenantId, will.ProjectId, will.DeviceId, "shadow/reported")
		packet.WillMessage = &paho.WillMessage{Topic: topic, Payload: b, QoS: 1, Retain: true}
		packet.WillProperties = &paho.WillProperties{WillDelayInterval: &delay}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ack, e := client.Connect(ctx, packet)
	if e != nil || ack.ReasonCode >= 128 {
		c.Close()
		t.Fatal("MQTT connect", e)
	}
	return &connection{client: client, conn: c}
}
func (c *connection) close() { _ = c.client.Disconnect(&paho.Disconnect{}); _ = c.conn.Close() }
func (c *connection) publish(t *testing.T, m contract.Envelope, suffix string, expiry *uint32) {
	t.Helper()
	topic, e := contract.Topic(m.TenantId, m.ProjectId, m.DeviceId, suffix)
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	properties := &paho.PublishProperties{MessageExpiry: expiry}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ack, e := c.client.Publish(ctx, &paho.Publish{Topic: topic, QoS: 1, Retain: suffix == "shadow/reported", Payload: b, Properties: properties})
	if e != nil || ack.ReasonCode >= 128 {
		t.Fatal("MQTT publish", e)
	}
}
func (c *connection) subscribe(t *testing.T, suffix string) {
	t.Helper()
	topic, _ := contract.Topic("demo", "Project-A", "TEST-001", suffix)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ack, e := c.client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 1}}})
	if e != nil || len(ack.Reasons) != 1 || ack.Reasons[0] >= 128 {
		t.Fatal("subscribe", e)
	}
}
func presence(t *testing.T) contract.Shadow {
	t.Helper()
	var s contract.Shadow
	if status := api(t, "devices/TEST-001/shadow", &s); status != 200 && status != 404 {
		t.Fatal("shadow HTTP", status)
	}
	return s
}
func TestWillDelayShortAndLongDisconnect(t *testing.T) {
	enabled(t)
	m := fixture(t)
	will := m
	will.Kind = "LWT"
	will.Sequence = 100000
	will = contract.WithPayload(will, contract.DeathPayload{Reason: "UNEXPECTED"})
	id := "t05-will-" + platform.ID()
	first := connect(t, id, "TEST-001", &will, nil)
	first.publish(t, birth(m), "shadow/reported", nil)
	until(t, "birth", func() bool { return presence(t).Presence.BootId == m.BootId })
	_ = first.conn.Close()
	time.Sleep(time.Second)
	will.Sequence = 200000
	second := connect(t, id, "TEST-001", &will, nil)
	defer second.close()
	m.Sequence = 100001
	second.publish(t, birth(m), "shadow/reported", nil)
	// Sample through the original Will deadline: a brief disconnect cannot emit OFFLINE.
	end := time.Now().Add(6 * time.Second)
	for time.Now().Before(end) {
		if presence(t).Presence.Connectivity != "MQTT_CONNECTED" {
			t.Fatal("short outage caused OFFLINE")
		}
		time.Sleep(100 * time.Millisecond)
	}
	before := presence(t)
	disconnected := time.Now()
	_ = second.conn.Close()
	until(t, "delayed LWT offline", func() bool { return presence(t).Presence.Connectivity == "OFFLINE" })
	after := presence(t)
	if time.Since(disconnected) < 4500*time.Millisecond || after.Presence.DisconnectReason != "UNEXPECTED" || after.Reported != before.Reported {
		t.Fatal("invalid Will delay or lost operational snapshot", after)
	}
}
func TestEventACKLossRetriesSameMessageID(t *testing.T) {
	enabled(t)
	m := fixture(t)
	received := make(chan contract.Envelope, 16)
	c := connect(t, "t05-event-"+platform.ID(), "TEST-001", nil, received)
	defer c.close()
	c.subscribe(t, "event/ack")
	c.publish(t, birth(m), "shadow/reported", nil)
	until(t, "event fixture birth", func() bool { return presence(t).Presence.BootId == m.BootId })
	m.MessageId = platform.ID()
	m.Kind = "ALARM"
	m.Sequence = 2
	m = contract.WithPayload(m, contract.AlarmPayload{Code: "ACK_LOSS_TEST", Text: "Anonymous durable ACK retry fixture"})
	q, e := spool.Open(filepath.Join(t.TempDir(), "critical.json"), 256)
	if e != nil {
		t.Fatal(e)
	}
	if e = q.Push(m); e != nil {
		t.Fatal(e)
	}
	receiveACK := func() {
		t.Helper()
		select {
		case ack := <-received:
			if contract.Payload[contract.EventStoredAckPayload](ack).SourceMessageId != m.MessageId {
				t.Fatal("wrong application ACK")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("application ACK missing")
		}
	}
	c.publish(t, m, "alarm", nil)
	receiveACK() // Deliberately lose the first application ACK after broker PUBACK.
	if _, ok := q.Peek(); !ok {
		t.Fatal("PUBACK removed critical event")
	}
	q.Retry()
	c.publish(t, m, "alarm", nil)
	receiveACK()
	if e = q.Ack(m.MessageId); e != nil {
		t.Fatal(e)
	}
	if _, ok := q.Peek(); ok {
		t.Fatal("second stored ACK did not remove event")
	}
	var alarms []contract.Envelope
	if api(t, "alarms", &alarms) != 200 {
		t.Fatal("alarm API")
	}
	count := 0
	for _, a := range alarms {
		if a.MessageId == m.MessageId {
			count++
		}
	}
	if count != 1 {
		t.Fatal("retry duplicated database alarm", count)
	}
}
func TestMQTTExpiredWatchIsNotDelivered(t *testing.T) {
	enabled(t)
	received := make(chan contract.Envelope, 16)
	id := "t05-expiry-" + platform.ID()
	edge := connect(t, id, "TEST-001", nil, received)
	edge.subscribe(t, "command/watch/start")
	edge.close()
	cloud := connect(t, "t05-command-"+platform.ID(), "cloud-api", nil, nil)
	defer cloud.close()
	m := fixture(t)
	m.Kind = "WATCH_START"
	m.MessageId = platform.ID()
	m = contract.WithPayload(m, contract.WatchCommandPayload{LeaseId: platform.ID(), Generation: 1, TtlSeconds: 5, TelemetryHz: 1, ExpiresAt: time.Now().Add(time.Second)})
	expiry := uint32(1)
	cloud.publish(t, m, "command/watch/start", &expiry)
	time.Sleep(2500 * time.Millisecond)
	edge = connect(t, id, "TEST-001", nil, received)
	defer edge.close()
	edge.subscribe(t, "command/watch/start")
	select {
	case got := <-received:
		t.Fatal("expired WATCH delivered", got.MessageId)
	case <-time.After(2 * time.Second):
	}
}
