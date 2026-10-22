package main

import (
	"context"
	"github.com/guolichen007/crane-operations-platform/edge/agent/runner"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	logger := platform.Logger("device-simulator")
	c, e := platform.Load(os.Args[1:])
	if e != nil {
		logger.Error("invalid configuration", "event", "startup_failed")
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if e = runner.RunFleet(ctx, c); e != nil {
		logger.Error("simulator stopped", "event", "simulator_failed")
		os.Exit(1)
	}
}
