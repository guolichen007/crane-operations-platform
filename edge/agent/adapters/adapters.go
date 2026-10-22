package adapters

import (
	"context"
	"github.com/guolichen007/crane-operations-platform/edge/agent/internal/domain"
)

type ControllerAdapter interface {
	Health(context.Context) (string, error)
}
type TelemetryAdapter interface {
	Read(context.Context) (domain.TelemetrySnapshot, error)
}
type AlarmAdapter interface {
	Events(context.Context) <-chan domain.AlarmEvent
}
type PointCloudSource interface {
	Next(context.Context) ([]byte, error)
}
type LocalWebDescriptor struct {
	Enabled bool
	Origin  string
}
