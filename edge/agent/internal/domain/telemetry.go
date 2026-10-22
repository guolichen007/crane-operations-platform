package domain

// Adapter models contain device facts only. They have no Cloud identity or MQTT metadata.
type Position struct{ X, Y, Z float64 }
type DeviceHealth struct{ Edge, Controller, Sensor string }
type TelemetrySnapshot struct {
	Health                            DeviceHealth
	Mode, AlarmState, SoftwareVersion string
	RuntimeTotal                      float64
	Position                          Position
	Load                              float64
}
type AlarmEvent struct{ Code, Text string }
