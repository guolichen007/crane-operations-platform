package mock

import (
	"context"
	"github.com/guolichen007/crane-operations-platform/edge/agent/internal/domain"
	"math"
	"time"
)

type Telemetry struct{}

func (Telemetry) Read(context.Context) (domain.TelemetrySnapshot, error) {
	v := float64(time.Now().Unix() % 60)
	return domain.TelemetrySnapshot{Health: domain.DeviceHealth{Edge: "EDGE_HEALTHY", Controller: "CONTROLLER_HEALTHY", Sensor: "HEALTHY"}, Mode: "AUTO", AlarmState: "NONE", SoftwareVersion: "mock-0.5.0", Position: domain.Position{X: math.Sin(v) * 10, Y: v, Z: 5}, Load: 2.5}, nil
}
