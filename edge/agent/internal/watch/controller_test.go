package watch

import (
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"path/filepath"
	"testing"
	"time"
)

func command(id, kind string, generation int64, now time.Time) contract.Envelope {
	return contract.WithPayload(contract.Envelope{MessageId: id, Kind: kind}, contract.WatchCommandPayload{LeaseId: "lease-1", Generation: generation, TtlSeconds: 5, TelemetryHz: 1, ExpiresAt: now.Add(5 * time.Second)})
}
func TestFencingDedupAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.json")
	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	start := command("start-2", "WATCH_START", 2, now)
	a, err := c.Apply(start, now, now.Add(5*time.Second))
	if err != nil || a.Result != "APPLIED" || !c.Active(now) {
		t.Fatal(a, err)
	}
	a, err = c.Apply(command("stop-1", "WATCH_STOP", 1, now), now, now.Add(5*time.Second))
	if err != nil || a.Result != "STALE" || !c.Active(now) {
		t.Fatal("old STOP killed LIVE", a, err)
	}
	effects := c.Effects
	a, err = c.Apply(start, now.Add(time.Second), now.Add(5*time.Second))
	if err != nil || a.Result != "DUPLICATE" || c.Effects != effects {
		t.Fatal("duplicate effects", a, err)
	}
	c, err = Open(path)
	if err != nil || c.Active(now) || c.Generation() != 2 {
		t.Fatal("restart restored an unsafe timer or lost fence", err)
	}
	a, err = c.Apply(start, now, now.Add(5*time.Second))
	if err != nil || a.Result != "DUPLICATE" || c.Active(now) {
		t.Fatal("durable dedup", a, err)
	}
	a, err = c.Apply(command("renew-2", "WATCH_RENEW", 2, now), now, now.Add(5*time.Second))
	if err != nil || a.Result != "APPLIED" || !c.Active(now) {
		t.Fatal("desired-state reconciliation", a, err)
	}
}
func TestExpiredCommandDoesNotRun(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "watch.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	a, err := c.Apply(command("old", "WATCH_START", 3, now.Add(-6*time.Second)), now, now.Add(-time.Second))
	if err != nil || a.Result != "EXPIRED" || c.Active(now) || c.Generation() != 0 || c.Effects != 0 {
		t.Fatal(a, err)
	}
}
func TestBrokerDeadlineIgnoresDeviceWallClockSkew(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "watch.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	m := command("clock-skew", "WATCH_START", 1, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	ack, err := c.Apply(m, now, now.Add(2*time.Second))
	if err != nil || ack.Result != "APPLIED" || !c.Active(now) {
		t.Fatal("device wall clock incorrectly rejected fresh broker delivery", ack, err)
	}
	m = command("delayed-inbox", "WATCH_START", 2, now.Add(time.Hour))
	ack, err = c.Apply(m, now.Add(3*time.Second), now.Add(2*time.Second))
	if err != nil || ack.Result != "EXPIRED" || c.Generation() != 1 {
		t.Fatal("expired inbox command executed", ack, err)
	}
}
