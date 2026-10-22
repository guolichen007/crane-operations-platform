package platform

import (
	"errors"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"testing"
	"time"
)

func TestWatchMessageExpiryIsBounded(t *testing.T) {
	now := time.Now()
	for _, kind := range []string{"WATCH_START", "WATCH_RENEW", "WATCH_STOP"} {
		m := contract.WithPayload(contract.Envelope{Kind: kind}, contract.WatchCommandPayload{TtlSeconds: 5, ExpiresAt: now.Add(30 * time.Second)})
		p, e := PublishProperties(m, now)
		if e != nil || p.MessageExpiry == nil || *p.MessageExpiry != 5 {
			t.Fatal(kind, p, e)
		}
		_, e = PublishProperties(m, now.Add(31*time.Second))
		if !errors.Is(e, ErrMessageExpired) {
			t.Fatal("expired command published", e)
		}
	}
}
func TestWillDelayBounds(t *testing.T) {
	for _, value := range []string{"4", "61"} {
		t.Setenv("MQTT_WILL_DELAY_SECONDS", value)
		if _, err := Load(nil); err == nil {
			t.Fatal("Will outside bounds", value)
		}
	}
	t.Setenv("MQTT_WILL_DELAY_SECONDS", "20")
	c, err := Load(nil)
	if err != nil || c.WillDelay != 20 {
		t.Fatal(c.WillDelay, err)
	}
}
