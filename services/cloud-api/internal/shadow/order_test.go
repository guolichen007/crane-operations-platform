package shadow

import (
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"testing"
	"time"
)

func TestDuplicateOutOfOrderAndBoot(t *testing.T) {
	old := contract.Message{BootId: "one", Sequence: 10, Timestamp: time.Now(), Connectivity: "MQTT_CONNECTED"}
	for _, m := range []contract.Message{{BootId: "one", Sequence: 9}, {BootId: "one", Sequence: 10}, {BootId: "other", Sequence: 99, Kind: "LIVE"}, {BootId: "other", Kind: "BIRTH", Timestamp: old.Timestamp.Add(-time.Second)}} {
		if Accept(old, m) {
			t.Fatal("stale overwrite")
		}
	}
	if !Accept(old, contract.Message{BootId: "one", Kind: "LWT"}) || !Accept(old, contract.Message{BootId: "two", Kind: "BIRTH", Timestamp: old.Timestamp.Add(time.Second)}) {
		t.Fatal("birth/lwt")
	}
}
