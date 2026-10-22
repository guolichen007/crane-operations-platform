package mock

import (
	"context"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"math"
	"time"
)

type Telemetry struct{}

func (Telemetry) Read(context.Context) (contract.Message, error) {
	v := float64(time.Now().Unix() % 60)
	return contract.Message{Connectivity: "MQTT_CONNECTED", EdgeHealth: "EDGE_HEALTHY", ControllerHealth: "CONTROLLER_HEALTHY", SensorHealth: "HEALTHY", Mode: "AUTO", AlarmState: "NONE", SoftwareVersion: "mock-0.1.0", X: math.Sin(v) * 10, Y: v, Z: 5, Load: 2.5}, nil
}
