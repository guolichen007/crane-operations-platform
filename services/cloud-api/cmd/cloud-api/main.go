package main

import (
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/app"
	"os"
)

func main() {
	if e := app.Run(os.Args[1:]); e != nil {
		platform.Logger("cloud-api").Error("service stopped; inspect dependency readiness", "event", "startup_failed")
		os.Exit(1)
	}
}
