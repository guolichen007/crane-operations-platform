package telemetry

import "time"

// SampleInterval limits history writes independently of browser fan-out.
const SampleInterval = 10 * time.Second
