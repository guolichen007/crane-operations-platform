package mapper

import (
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/edge/agent/internal/boot"
	"testing"
)

func TestConnectionWillFencing(t *testing.T) {
	f := Factory{Tenant: "demo", Project: "Project-A", Device: "SIM-001", Boot: boot.Identity{ID: "boot-1", Seq: 1}}
	will1 := f.ConnectionWill()
	message := f.New("DEATH", contract.DeathPayload{Reason: "GRACEFUL"})
	if message.Sequence >= will1.Sequence {
		t.Fatal("Will cannot terminate connection")
	}
	will2 := f.ConnectionWill()
	birth := f.New("HEARTBEAT", contract.HeartbeatPayload{EdgeHealth: "EDGE_HEALTHY", ControllerHealth: "CONTROLLER_HEALTHY", SensorHealth: "HEALTHY"})
	if birth.Sequence <= will1.Sequence || will2.Sequence <= birth.Sequence || birth.BootSeq != 1 {
		t.Fatal("reconnect failed to fence delayed Will")
	}
	if err := contract.Validate(birth); err != nil {
		t.Fatal(err)
	}
}
