package adapters

import (
	"context"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

type ControllerAdapter interface {
	Health(context.Context) (string, error)
}
type TelemetryAdapter interface {
	Read(context.Context) (contract.Message, error)
}
type AlarmAdapter interface {
	Events(context.Context) <-chan contract.Message
}
type PointCloudSource interface {
	Next(context.Context) ([]byte, error)
}
type LocalWebDescriptor struct {
	Enabled bool
	Origin  string
}
