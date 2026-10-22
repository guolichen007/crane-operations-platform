package runner

import (
	"context"
	"encoding/json"
	"fmt"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/edge/agent/adapters/mock"
	"github.com/guolichen007/crane-operations-platform/edge/agent/internal/domain"
	"github.com/guolichen007/crane-operations-platform/edge/agent/spool"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"path/filepath"
	"sync/atomic"
	"time"
)

func Run(ctx context.Context, c platform.Config, id string) error {
	logger := platform.Logger("edge-agent").With("deviceId", id)
	machine := &domain.Machine{}
	boot := platform.ID()
	var seq atomic.Int64
	adapter := mock.Telemetry{}
	envelope := func(kind string) contract.Message {
		m, _ := adapter.Read(ctx)
		m.SchemaVersion = 1
		m.MessageId = platform.ID()
		m.TenantId = "demo"
		m.ProjectId = "Project-A"
		m.DeviceId = id
		m.BootId = boot
		m.Sequence = seq.Add(1)
		m.Timestamp = time.Now().UTC()
		m.Kind = kind
		m.OperatingState = string(machine.State(time.Now()))
		return m
	}
	q, e := spool.Open(filepath.Join(c.DataDir, id+".json"), 256)
	if e != nil {
		return e
	}
	commands := make(chan contract.Message, 32)
	born := make(chan struct{}, 1)
	sub, _ := contract.Topic("demo", "Project-A", id, "command/watch/start")
	prefix := sub[:len(sub)-len("start")]
	will := envelope("LWT")
	will.Connectivity = "OFFLINE"
	will.EdgeHealth = "UNKNOWN"
	will.ControllerHealth = "UNKNOWN"
	broker, e := platform.Connect(ctx, c, id, []string{prefix + "+"}, &will, func(topic string, b []byte) error {
		var m contract.Message
		if json.Unmarshal(b, &m) != nil || contract.Validate(m) != nil || m.DeviceId != id {
			return nil
		}
		_, _, _, s, err := contract.ParseTopic(topic)
		if err != nil {
			return nil
		}
		if (s == "command/watch/start" && m.Kind != "WATCH_START") || (s == "command/watch/renew" && m.Kind != "WATCH_RENEW") || (s == "command/watch/stop" && m.Kind != "WATCH_STOP") {
			return nil
		}
		select {
		case commands <- m:
		default:
			logger.Error("command inbox full", "event", "command_rejected")
		}
		return nil
	}, func() {
		select {
		case born <- struct{}{}:
		default:
		}
	}, machine.Disconnect)
	if e != nil {
		return e
	}
	defer broker.CM.Disconnect(context.Background())
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastState := domain.Silent
	lastBeat, lastSnapshot, lastAlarm := time.Time{}, time.Time{}, time.Time{}
	seen := map[string]bool{}
	send := func(kind, suffix string) error { return broker.Publish(ctx, envelope(kind), suffix) }
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-born:
			machine.Connect()
			if e = send("BIRTH", "shadow/reported"); e != nil {
				logger.Warn("birth retry pending", "event", "birth_retry")
			}
			lastSnapshot = time.Now()
		case m := <-commands:
			if !seen[m.MessageId] {
				if len(seen) > 1024 {
					seen = map[string]bool{}
				}
				seen[m.MessageId] = true
				switch m.Kind {
				case "WATCH_START", "WATCH_RENEW":
					if time.Since(m.Timestamp) < time.Duration(m.TtlSeconds)*time.Second {
						machine.Watch(time.Now(), m.TtlSeconds)
					}
				case "WATCH_STOP":
					machine.Stop()
				}
			}
			ack := envelope("WATCH_ACK")
			ack.SessionId = m.SessionId
			ack.Code = m.MessageId
			_ = broker.Publish(ctx, ack, "command/ack")
		case now := <-tick.C:
			state := machine.State(now)
			if lastState == domain.Live && state == domain.Silent {
				_ = send("SNAPSHOT", "shadow/reported")
			}
			lastState = state
			if now.Sub(lastBeat) >= 15*time.Minute {
				if send("HEARTBEAT", "heartbeat") == nil {
					lastBeat = now
				}
			}
			if now.Sub(lastSnapshot) >= time.Hour {
				if send("SNAPSHOT", "shadow/reported") == nil {
					lastSnapshot = now
				}
			}
			if machine.State(now) == domain.Live {
				_ = send("LIVE", "telemetry/live")
			}
			if c.Scenario == "alarm-storm" && now.Sub(lastAlarm) >= 2*time.Second {
				m := envelope("ALARM")
				m.Code = "MOCK_OVERLOAD"
				m.Text = "Simulated alarm"
				if e = q.Push(m); e != nil {
					logger.Error("critical spool capacity reached", "event", "spool_full")
				}
				lastAlarm = now
			}
			if m, ok := q.Peek(); ok {
				machine.BeginEvent()
				suffix := "event"
				if m.Kind == "ALARM" {
					suffix = "alarm"
				}
				if broker.Publish(ctx, m, suffix) == nil {
					if e = q.Ack(m.MessageId); e != nil {
						return e
					}
				}
				machine.AckEvent()
			}
		}
	}
}
func RunFleet(ctx context.Context, c platform.Config) error {
	if c.Scenario == "offline" {
		<-ctx.Done()
		return nil
	}
	errs := make(chan error, c.Count)
	for i := 1; i <= c.Count; i++ {
		id := fmt.Sprintf("SIM-%03d", i)
		go func() {
			if c.Scenario != "intermittent" {
				errs <- Run(ctx, c, id)
				return
			}
			for {
				child, cancel := context.WithTimeout(ctx, 20*time.Second)
				e := Run(child, c, id)
				cancel()
				if e != nil {
					errs <- e
					return
				}
				select {
				case <-ctx.Done():
					errs <- nil
					return
				case <-time.After(5 * time.Second):
				}
			}
		}()
	}
	for i := 0; i < c.Count; i++ {
		if e := <-errs; e != nil {
			return e
		}
	}
	return nil
}
