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
	logger := platform.Logger("edge-agent")
	c, e := platform.Load(os.Args[1:])
	if e != nil {
		logger.Error("invalid configuration", "event", "startup_failed")
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if e = runner.Run(ctx, c, "SIM-001"); e != nil {
		logger.Error("agent stopped", "event", "agent_failed")
		os.Exit(1)
	}
}
