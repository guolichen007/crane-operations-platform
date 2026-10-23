package runner

import (
	"context"
	"errors"
	"fmt"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/edge/agent/adapters/mock"
	"github.com/guolichen007/crane-operations-platform/edge/agent/internal/boot"
	"github.com/guolichen007/crane-operations-platform/edge/agent/internal/domain"
	"github.com/guolichen007/crane-operations-platform/edge/agent/internal/mapper"
	"github.com/guolichen007/crane-operations-platform/edge/agent/internal/watch"
	"github.com/guolichen007/crane-operations-platform/edge/agent/spool"
	"github.com/guolichen007/crane-operations-platform/internal/fleet"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

var queues sync.Map
var eventACKs atomic.Int64

type pendingCommand struct {
	Message  contract.Envelope
	Deadline time.Time
}

// MetricsServer is loopback-only and has no management routes or host port mapping.
func MetricsServer(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		depth, full := 0, 0
		var retries int64
		queues.Range(func(_, value any) bool {
			d, f, r := value.(*spool.Queue).Metrics()
			depth += d
			full += f
			retries += r
			return true
		})
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "critical_spool_depth %d\ncritical_spool_full %d\ncritical_spool_retries_total %d\nevent_application_acks_total %d\nmqtt_reconnects_total %d\n", depth, full, retries, eventACKs.Load(), platform.MQTTReconnects.Load())
	})
	srv := &http.Server{Addr: "127.0.0.1:8083", Handler: mux, ReadHeaderTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		done, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(done)
	}()
	if e := srv.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
		platform.Logger("edge-agent").Error("metrics unavailable", "event", "metrics_failed")
	}
}

func Run(ctx context.Context, c platform.Config, id string) error {
	return RunDevice(ctx, c, fleet.LegacyDescriptor(id))
}
func RunDevice(ctx context.Context, c platform.Config, descriptor fleet.Device) error {
	id := descriptor.RuntimeDeviceId
	logger := platform.Logger("edge-agent").With("deviceId", id)
	identity, e := boot.Start(filepath.Join(c.DataDir, id, "boot.json"))
	if e != nil {
		return e
	}
	factory := &mapper.Factory{Tenant: descriptor.TenantID, Project: descriptor.ProjectId, Device: id, Boot: identity}
	controller, e := watch.Open(filepath.Join(c.DataDir, id, "watch.json"))
	if e != nil {
		return e
	}
	q, e := spool.Open(filepath.Join(c.DataDir, id+".json"), 256)
	if e != nil {
		return e
	}
	queues.Store(id, q)
	defer queues.Delete(id)
	machine := &domain.Machine{}
	adapter := mock.Telemetry{}
	inbox := make(chan pendingCommand, 32)
	born := make(chan struct{}, 1)
	start, _ := contract.Topic(descriptor.TenantID, descriptor.ProjectId, id, "command/watch/start")
	ackTopic, _ := contract.Topic(descriptor.TenantID, descriptor.ProjectId, id, "event/ack")
	mqttCtx, mqttCancel := context.WithCancel(context.Background())
	defer mqttCancel()
	broker, e := platform.Connect(mqttCtx, c, id, []string{start[:len(start)-len("start")] + "+", ackTopic}, factory.ConnectionWill, func(topic string, b []byte, expiry time.Duration) error {
		m, err := contract.Decode(topic, b)
		if err != nil || m.TenantId != descriptor.TenantID || m.ProjectId != descriptor.ProjectId || m.DeviceId != id {
			logger.Warn("invalid message", "event", "message_rejected")
			return nil
		}
		if m.Kind == "EVENT_STORED_ACK" {
			ack := contract.Payload[contract.EventStoredAckPayload](m)
			head, ok := q.Peek()
			if ok && head.MessageId == ack.SourceMessageId {
				if err = q.Ack(ack.SourceMessageId); err != nil {
					return err
				}
				eventACKs.Add(1)
				logger.Info("critical event stored", "event", "event_application_ack", "messageId", ack.SourceMessageId)
			}
			return nil
		}
		deadline := time.Time{}
		if expiry > 0 {
			p := contract.Payload[contract.WatchCommandPayload](m)
			expiry = min(expiry, time.Duration(p.TtlSeconds)*time.Second)
			deadline = time.Now().Add(expiry)
		}
		select {
		case inbox <- pendingCommand{Message: m, Deadline: deadline}:
			return nil
		default:
			return errors.New("command inbox full")
		}
	}, func() {
		select {
		case born <- struct{}{}:
		default:
		}
	}, func() {
		machine.Disconnect()
	})
	if e != nil {
		return e
	}
	defer func() {
		done, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = broker.CM.Disconnect(done)
	}()
	send := func(kind, suffix string) error {
		snapshot, err := adapter.Read(ctx)
		if err != nil {
			return err
		}
		var payload any
		switch kind {
		case "BIRTH":
			payload = mapper.Birth(snapshot, machine.State(time.Now()))
		case "SNAPSHOT":
			payload = mapper.Snapshot(snapshot, machine.State(time.Now()))
		case "HEARTBEAT":
			payload = contract.HeartbeatPayload{EdgeHealth: snapshot.Health.Edge, ControllerHealth: snapshot.Health.Controller, SensorHealth: snapshot.Health.Sensor}
		case "LIVE":
			payload = contract.LiveTelemetryPayload{LeaseId: controller.LeaseID(), Generation: controller.Generation(), Reported: mapper.Reported(snapshot, machine.State(time.Now()))}
		}
		return broker.Publish(ctx, factory.New(kind, payload), suffix)
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastState := domain.Silent
	lastBeat, lastSnapshot, lastAlarm, lastRetry := time.Time{}, time.Time{}, time.Time{}, time.Time{}
	lastEvent := ""
	birthPending := false
	for {
		select {
		case <-ctx.Done():
			done, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := broker.Publish(done, factory.New("DEATH", contract.DeathPayload{Reason: "GRACEFUL"}), "shadow/reported")
			cancel()
			if err != nil {
				logger.Warn("graceful death unavailable", "event", "death_unsent")
			}
			return nil
		case <-born:
			controller.Disconnect()
			machine.Connect()
			birthPending = true
			if send("BIRTH", "shadow/reported") == nil {
				birthPending = false
				lastSnapshot = time.Now()
			}
		case command := <-inbox:
			m := command.Message
			ack, err := controller.Apply(m, time.Now(), command.Deadline)
			if err != nil {
				return err
			}
			// The domain timer is updated only on an applied transition, never duplicates.
			if ack.Result == "APPLIED" {
				if controller.Active(time.Now()) {
					remaining := int(time.Until(controller.Until).Seconds()) + 1
					machine.Watch(time.Now(), remaining)
				} else {
					machine.Stop()
				}
			}
			message := factory.New("WATCH_ACK", ack)
			message.CorrelationId = m.MessageId
			if err = broker.Publish(ctx, message, "command/ack"); err != nil {
				logger.Warn("watch ACK pending retry", "event", "watch_ack_unsent", "commandId", m.MessageId, "generation", ack.Generation)
			}
			logger.Info("watch processed", "event", "watch_ack", "commandId", m.MessageId, "generation", ack.Generation, "result", ack.Result)
		case now := <-ticker.C:
			if birthPending {
				if send("BIRTH", "shadow/reported") == nil {
					birthPending = false
					lastSnapshot = now
				}
				continue
			}
			if !controller.Active(now) {
				machine.Stop()
			}
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
			if state == domain.Live {
				_ = send("LIVE", "telemetry/live")
			}
			if c.Scenario == "alarm-storm" && now.Sub(lastAlarm) >= 2*time.Second {
				m := factory.New("ALARM", contract.AlarmPayload{Code: "MOCK_OVERLOAD", Text: "Simulated alarm"})
				if err := q.Push(m); err != nil {
					if !errors.Is(err, spool.ErrFull) {
						return err
					}
					logger.Error("critical spool capacity reached", "event", "spool_full", "messageId", m.MessageId)
				}
				lastAlarm = now
			}
			if m, ok := q.Peek(); ok && now.Sub(lastRetry) >= 2*time.Second {
				if lastEvent == m.MessageId {
					q.Retry()
				}
				lastEvent = m.MessageId
				lastRetry = now
				machine.BeginEvent()
				suffix := "event"
				if m.Kind == "ALARM" {
					suffix = "alarm"
				}
				// Broker PUBACK deliberately does not remove the critical record.
				if err := broker.Publish(ctx, m, suffix); err != nil {
					logger.Warn("critical event retry", "event", "event_retry", "messageId", m.MessageId)
				}
				machine.AckEvent()
			}
		}
	}
}

func RunFleet(ctx context.Context, c platform.Config) error {
	manifest, err := fleet.Load(c.FleetFile, c.Count, false, true)
	if err != nil {
		return err
	}
	descriptors := fleet.Devices(manifest)
	go MetricsServer(ctx)
	if c.Scenario == "offline" {
		<-ctx.Done()
		return nil
	}
	fleetCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, len(descriptors))
	for _, descriptor := range descriptors {
		go func() {
			if c.Scenario != "intermittent" {
				errs <- RunDevice(fleetCtx, c, descriptor)
				return
			}
			// This scenario models process restarts, distinct from MQTT-only fault tests.
			for {
				child, stop := context.WithTimeout(fleetCtx, 20*time.Second)
				e := RunDevice(child, c, descriptor)
				stop()
				if e != nil {
					errs <- e
					return
				}
				select {
				case <-fleetCtx.Done():
					errs <- nil
					return
				case <-time.After(5 * time.Second):
				}
			}
		}()
	}
	var first error
	for i := 0; i < len(descriptors); i++ {
		if e := <-errs; e != nil && first == nil {
			first = e
			cancel()
		}
	}
	return first
}
