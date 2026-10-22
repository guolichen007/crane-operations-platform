package shadow

import (
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"testing"
	"time"
)

func TestClockRollbackAndDelayedOldBoot(t *testing.T) {
	old := contract.Presence{BootId: "one", BootSeq: 10, Sequence: 100}
	for _, m := range []contract.Envelope{{BootId: "one", BootSeq: 10, Sequence: 99}, {BootId: "one", BootSeq: 10, Sequence: 100}, {BootId: "other", BootSeq: 10, Sequence: 101}, {BootId: "old", BootSeq: 9, Sequence: 9000, Kind: "LWT"}, {BootId: "old", BootSeq: 9, Sequence: 9000, Kind: "LIVE"}, {BootId: "old", BootSeq: 9, Sequence: 9000, Kind: "SNAPSHOT"}} {
		if Accept(old, m) {
			t.Fatal("stale overwrite", m)
		}
	}
	if !Accept(old, contract.Envelope{BootId: "two", BootSeq: 11, Sequence: 1, Kind: "BIRTH", SentAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}) {
		t.Fatal("clock rollback rejected new boot")
	}
}
func TestOfflineKeepsOperationalState(t *testing.T) {
	old := contract.Shadow{DeviceId: "SIM-001", Presence: contract.Presence{BootId: "one", BootSeq: 1, Sequence: 10}, Reported: contract.ReportedState{Mode: "AUTO", OperatingState: "LIVE", Position: contract.Position{X: 10, Y: 20, Z: 5}, Load: 2.5}}
	for _, kind := range []string{"LWT", "DEATH"} {
		m := contract.WithPayload(contract.Envelope{DeviceId: "SIM-001", BootId: "one", BootSeq: 1, Sequence: 11, Kind: kind}, contract.DeathPayload{Reason: "UNEXPECTED"})
		next, ok := Merge(old, m, time.Now())
		if !ok || next.Presence.Connectivity != "OFFLINE" || next.Reported != old.Reported {
			t.Fatal("offline erased state", next)
		}
	}
}
