package mapper

import (
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/edge/agent/internal/boot"
	"github.com/guolichen007/crane-operations-platform/edge/agent/internal/domain"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"sync"
	"time"
)

const connectionBlock int64 = 1 << 32

type Factory struct {
	mu                sync.Mutex
	Tenant, Project   string
	Device            string
	Boot              boot.Identity
	sequence, ceiling int64
}

func (f *Factory) envelope(kind string, seq int64) contract.Envelope {
	return contract.Envelope{SchemaVersion: 1, MessageId: platform.ID(), TenantId: f.Tenant, ProjectId: f.Project, DeviceId: f.Device, BootId: f.Boot.ID, BootSeq: f.Boot.Seq, Sequence: seq, SentAt: time.Now().UTC(), Kind: kind}
}

// One sequence block per MQTT connection reserves a terminal sequence for its Will.
// The next connection starts above that Will, even when it was delivered late.
func (f *Factory) ConnectionWill() contract.Envelope {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sequence = f.ceiling + 1
	f.ceiling = f.sequence + connectionBlock - 1
	if f.ceiling > 9007199254740991 {
		panic("connection sequence exhausted; restart Agent")
	}
	return contract.WithPayload(f.envelope("LWT", f.ceiling), contract.DeathPayload{Reason: "UNEXPECTED"})
}
func (f *Factory) New(kind string, payload any) contract.Envelope {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ceiling == 0 {
		f.ceiling = connectionBlock
	}
	f.sequence++
	if f.sequence >= f.ceiling {
		panic("connection sequence exhausted; reconnect Agent")
	}
	return contract.WithPayload(f.envelope(kind, f.sequence), payload)
}
func Reported(s domain.TelemetrySnapshot, state domain.State) contract.ReportedState {
	return contract.ReportedState{OperatingState: string(state), Mode: s.Mode, AlarmState: s.AlarmState, RuntimeTotal: s.RuntimeTotal, Position: contract.Position{X: s.Position.X, Y: s.Position.Y, Z: s.Position.Z}, Load: s.Load}
}
func Birth(s domain.TelemetrySnapshot, state domain.State) contract.BirthPayload {
	return contract.BirthPayload{EdgeHealth: s.Health.Edge, ControllerHealth: s.Health.Controller, SensorHealth: s.Health.Sensor, SoftwareVersion: s.SoftwareVersion, Reported: Reported(s, state)}
}
func Snapshot(s domain.TelemetrySnapshot, state domain.State) contract.SnapshotPayload {
	return contract.SnapshotPayload{EdgeHealth: s.Health.Edge, ControllerHealth: s.Health.Controller, SensorHealth: s.Health.Sensor, Reported: Reported(s, state)}
}
