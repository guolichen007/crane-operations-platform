package shadow

import (
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"time"
)

// Accept never uses the device clock. bootId also detects accidental counter reuse.
func Accept(old contract.Presence, next contract.Envelope) bool {
	if next.BootSeq > old.BootSeq {
		return true
	}
	return next.BootSeq == old.BootSeq && next.BootId == old.BootId && next.Sequence > old.Sequence
}

// Merge preserves the last business snapshot when only presence changed.
func Merge(old contract.Shadow, m contract.Envelope, observed time.Time) (contract.Shadow, bool) {
	if !Accept(old.Presence, m) {
		return old, false
	}
	next := old
	next.DeviceId = m.DeviceId
	next.TenantId = m.TenantId
	next.ProjectId = m.ProjectId
	next.Version++
	next.UpdatedAt = observed
	next.UpstreamStatus = "SILENT"
	p := old.Presence
	if p.BootId == "" {
		p.EdgeHealth = "UNKNOWN"
		p.ControllerHealth = "UNKNOWN"
		p.SensorHealth = "UNKNOWN"
		next.Reported = contract.ReportedState{OperatingState: "UNKNOWN", Mode: "UNKNOWN", AlarmState: "UNKNOWN"}
	}
	p.BootId = m.BootId
	p.BootSeq = m.BootSeq
	p.Sequence = m.Sequence
	if m.Kind == "LWT" || m.Kind == "DEATH" {
		p.Connectivity = "OFFLINE"
		p.DisconnectedAt = observed
		p.DisconnectReason = contract.Payload[contract.DeathPayload](m).Reason
		p.EdgeHealth = "UNKNOWN"
		p.ControllerHealth = "UNKNOWN"
		p.SensorHealth = "UNKNOWN"
	} else {
		p.Connectivity = "MQTT_CONNECTED"
		p.LastSeenAt = observed
		p.DisconnectedAt = time.Time{}
		p.DisconnectReason = "NONE"
		switch m.Kind {
		case "BIRTH":
			v := contract.Payload[contract.BirthPayload](m)
			p.EdgeHealth = v.EdgeHealth
			p.ControllerHealth = v.ControllerHealth
			p.SensorHealth = v.SensorHealth
			next.Reported = v.Reported
		case "SNAPSHOT":
			v := contract.Payload[contract.SnapshotPayload](m)
			p.EdgeHealth = v.EdgeHealth
			p.ControllerHealth = v.ControllerHealth
			p.SensorHealth = v.SensorHealth
			next.Reported = v.Reported
		case "HEARTBEAT":
			v := contract.Payload[contract.HeartbeatPayload](m)
			p.EdgeHealth = v.EdgeHealth
			p.ControllerHealth = v.ControllerHealth
			p.SensorHealth = v.SensorHealth
		case "LIVE":
			next.Reported = contract.Payload[contract.LiveTelemetryPayload](m).Reported
		default:
			return old, false
		}
	}
	next.Presence = p
	return next, true
}
