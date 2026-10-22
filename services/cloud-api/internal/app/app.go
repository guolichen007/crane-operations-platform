package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coder/websocket"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/deploy/postgres/migrations"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/alarm"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/audit"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/remote"

	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/auth"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/device"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/session"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

func Run(args []string) error {
	c, e := platform.Load(args)
	if e != nil {
		return e
	}
	if c.Env != "development" || len(c.Token) < 32 {
		return errors.New("phase 0 requires development mode and random development token")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	logger := platform.Logger("cloud-api")
	db, e := sql.Open("pgx", c.DatabaseURL)
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(10)
	startup, stop := context.WithTimeout(ctx, 20*time.Second)
	defer stop()
	if e = db.PingContext(startup); e != nil {
		return e
	}
	if c.Migrate {
		goose.SetLogger(goose.NopLogger())
		goose.SetBaseFS(migrations.Files)
		if e = goose.SetDialect("postgres"); e != nil {
			return e
		}
		if e = goose.UpContext(startup, db, "."); e != nil {
			return e
		}
	}
	store, e := device.New(ctx, db)
	if e != nil {
		return e
	}
	leases := session.New(db, time.Duration(c.TTL)*time.Second, time.Duration(c.Grace)*time.Second)
	var mqttMessages, dbErrors, wsClients, eventAcks atomic.Int64
	var mqttUp atomic.Bool
	ackQueue := make(chan contract.Envelope, 256)
	reconcileAll := make(chan struct{}, 1)
	cloudBoot := platform.ID()
	var cloudSequence atomic.Int64
	broker, e := platform.Connect(ctx, c, "cloud-api", []string{"crane/v1/demo/Project-A/+/heartbeat", "crane/v1/demo/Project-A/+/shadow/reported", "crane/v1/demo/Project-A/+/telemetry/live", "crane/v1/demo/Project-A/+/alarm", "crane/v1/demo/Project-A/+/event", "crane/v1/demo/Project-A/+/command/ack"}, nil, func(topic string, b []byte) error {
		mqttMessages.Add(1)
		m, err := contract.Decode(topic, b)
		if err != nil {
			logger.Warn("invalid MQTT envelope", "event", "message_rejected")
			return nil
		}
		for {
			err = nil
			callCtx, done := context.WithTimeout(ctx, 5*time.Second)
			switch m.Kind {
			case "ALARM", "EVENT":
				err = alarm.Record(callCtx, db, m)
				if err == nil {
					ack := contract.WithPayload(contract.Envelope{SchemaVersion: 1, MessageId: platform.ID(), TenantId: m.TenantId, ProjectId: m.ProjectId, DeviceId: m.DeviceId, BootId: cloudBoot, BootSeq: 1, Sequence: cloudSequence.Add(1), SentAt: time.Now().UTC(), Kind: "EVENT_STORED_ACK", CorrelationId: m.MessageId}, contract.EventStoredAckPayload{SourceMessageId: m.MessageId, Result: "STORED"})
					select {
					case ackQueue <- ack:
					default:
						err = errors.New("application ACK queue full")
					}
				}
			case "WATCH_ACK":
				current, ok := store.Get(m.DeviceId)
				if ok && m.BootSeq == current.Presence.BootSeq && m.BootId == current.Presence.BootId {
					a := contract.Payload[contract.WatchAckPayload](m)
					err = leases.Ack(callCtx, m.DeviceId, a, time.Now())
					if err == nil {
						logger.Info("watch application ACK", "event", "watch_ack", "deviceId", m.DeviceId, "commandId", a.CommandId, "generation", a.Generation, "result", a.Result)
					}
				}
			default:
				allowed := true
				if m.Kind == "LIVE" {
					p := contract.Payload[contract.LiveTelemetryPayload](m)
					allowed, err = leases.AcceptLive(callCtx, m.DeviceId, p.LeaseId, p.Generation)
				}
				if err == nil && allowed {
					var accepted bool
					accepted, err = store.Apply(callCtx, m)
					if err == nil && accepted && m.Kind == "BIRTH" {
						err = leases.Reconcile(callCtx, m.DeviceId, time.Now())
					}
					if err == nil && accepted && (m.Kind == "DEATH" || m.Kind == "LWT") {
						err = leases.Offline(callCtx, m.DeviceId)
					}
				}
			}
			done()
			if err == nil {
				return nil
			}
			dbErrors.Add(1)
			logger.Error("message persistence failed; acknowledgment withheld", "event", "db_write_failed", "deviceId", m.DeviceId, "messageId", m.MessageId)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}, func() {
		mqttUp.Store(true)
		select {
		case reconcileAll <- struct{}{}:
		default:
		}
	}, func() { mqttUp.Store(false) })
	if e != nil {
		return e
	}
	defer broker.CM.Disconnect(context.Background())
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case ack := <-ackQueue:
				if err := broker.Publish(ctx, ack, "event/ack"); err == nil {
					eventAcks.Add(1)
				} else {
					logger.Warn("event ACK unsent; Edge retains spool", "event", "event_ack_unsent", "deviceId", ack.DeviceId, "messageId", ack.CorrelationId)
				}
			case <-reconcileAll:
				ids, err := leases.Devices(ctx)
				if err == nil {
					for _, id := range ids {
						if err = leases.Reconcile(ctx, id, time.Now()); err != nil {
							break
						}
					}
				}
				if err != nil {
					dbErrors.Add(1)
					logger.Error("desired-state reconciliation failed", "event", "reconcile_failed")
				}
			case now := <-ticker.C:
				if err := leases.Tick(ctx, now); err != nil {
					dbErrors.Add(1)
					continue
				}
				pending, err := leases.Pending(ctx, now)
				if err != nil {
					dbErrors.Add(1)
					continue
				}
				for _, m := range pending {
					suffix := map[string]string{"WATCH_START": "command/watch/start", "WATCH_RENEW": "command/watch/renew", "WATCH_STOP": "command/watch/stop"}[m.Kind]
					if err = broker.Publish(ctx, m, suffix); err != nil {
						continue
					}
					if err = leases.Published(ctx, m.MessageId, time.Now()); err != nil {
						dbErrors.Add(1)
					}
					logger.Info("watch command published; application ACK pending", "event", "watch_sent", "deviceId", m.DeviceId, "commandId", m.MessageId, "generation", contract.Payload[contract.WatchCommandPayload](m).Generation)
				}
			}
		}
	}()
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "alive"}) })
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		check, done := context.WithTimeout(r.Context(), 2*time.Second)
		defer done()
		if !mqttUp.Load() || db.PingContext(check) != nil {
			respond(w, 503, map[string]string{"status": "not-ready"})
			return
		}
		checkMQTT, doneMQTT := context.WithTimeout(r.Context(), time.Second)
		defer doneMQTT()
		if broker.CM.AwaitConnection(checkMQTT) != nil {
			respond(w, 503, map[string]string{"status": "mqtt-down"})
			return
		}
		respond(w, 200, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		connected := 0
		for _, v := range store.List() {
			if v.Presence.Connectivity == "MQTT_CONNECTED" {
				connected++
			}
		}
		alarms, err := alarm.List(r.Context(), db)
		if err != nil {
			dbErrors.Add(1)
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "mqtt_connected_devices %d\nremote_sessions 0\nmqtt_messages_total %d\nalarms_total %d\npointcloud_frames_total 0\npointcloud_frames_dropped_total 0\nwebsocket_clients %d\ndb_query_errors_total %d\n", connected, mqttMessages.Load(), len(alarms), wsClients.Load(), dbErrors.Load())
		fmt.Fprint(w, leases.Metrics(r.Context()))
		fmt.Fprintf(w, "event_application_acks_total %d\nmqtt_reconnects_total %d\n", eventAcks.Load(), platform.MQTTReconnects.Load())
	})
	mux.HandleFunc("POST /api/v1/auth/dev-session", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.Authenticate(r, c.Token); !ok {
			respond(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "crane_dev", Value: c.Token, Path: "/api/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 3600})
		respond(w, 200, map[string]string{"mode": "development-only"})
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/devices", func(w http.ResponseWriter, r *http.Request) {
		values := store.List()
		for i := range values {
			status, err := leases.Status(r.Context(), values[i].DeviceId)
			if err != nil {
				respond(w, 503, map[string]string{"error": "database unavailable"})
				return
			}
			values[i].UpstreamStatus = status
		}
		respond(w, 200, values)
	})
	detail := func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Get(r.PathValue("id"))
		if !ok {
			respond(w, 404, map[string]string{"error": "not found"})
			return
		}
		status, err := leases.Status(r.Context(), v.DeviceId)
		if err != nil {
			respond(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		v.UpstreamStatus = status
		respond(w, 200, v)
	}
	api.HandleFunc("GET /api/v1/devices/{id}", detail)
	api.HandleFunc("GET /api/v1/devices/{id}/shadow", detail)
	api.HandleFunc("GET /api/v1/alarms", func(w http.ResponseWriter, r *http.Request) {
		a, e := alarm.List(r.Context(), db)
		if e != nil {
			respond(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respond(w, 200, a)
	})
	api.HandleFunc("POST /api/v1/devices/{id}/live-sessions", func(w http.ResponseWriter, r *http.Request) {
		identity, _ := auth.Authenticate(r, c.Token)
		id := r.PathValue("id")
		if _, ok := store.Get(id); !ok {
			respond(w, 404, map[string]string{"error": "not found"})
			return
		}
		s, err := leases.Open(r.Context(), platform.ID(), id, identity.ID, time.Now())
		if err != nil {
			respond(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		logger.Info("viewer lease created", "event", "viewer_opened", "deviceId", id, "sessionId", s.SessionId)
		respond(w, 201, s)
	})
	api.HandleFunc("POST /api/v1/live-sessions/{sessionId}/renew", func(w http.ResponseWriter, r *http.Request) {
		identity, _ := auth.Authenticate(r, c.Token)
		s, err := leases.Renew(r.Context(), r.PathValue("sessionId"), identity.ID, time.Now())
		if errors.Is(err, session.ErrNotFound) {
			respond(w, 404, map[string]string{"error": "expired session"})
			return
		}
		if err != nil {
			respond(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		respond(w, 200, s)
	})
	api.HandleFunc("DELETE /api/v1/live-sessions/{sessionId}", func(w http.ResponseWriter, r *http.Request) {
		identity, _ := auth.Authenticate(r, c.Token)
		err := leases.Close(r.Context(), r.PathValue("sessionId"), identity.ID, time.Now())
		if errors.Is(err, session.ErrNotFound) {
			respond(w, 404, map[string]string{"error": "not found"})
			return
		}
		if err != nil {
			respond(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		w.WriteHeader(204)
	})
	api.HandleFunc("POST /api/v1/devices/{id}/remote-sessions", remote.Disabled)
	api.HandleFunc("DELETE /api/v1/remote-sessions/{sessionId}", remote.Disabled)
	api.HandleFunc("GET /api/v1/devices/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := store.Get(id); !ok {
			http.NotFound(w, r)
			return
		}
		conn, e := websocket.Accept(w, r, nil)
		if e != nil {
			return
		}
		defer conn.CloseNow()
		wsClients.Add(1)
		defer wsClients.Add(-1)
		clientCtx := conn.CloseRead(r.Context())
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			v, _ := store.Get(id)
			status, err := leases.Status(clientCtx, id)
			if err != nil {
				return
			}
			v.UpstreamStatus = status
			b, _ := json.Marshal(v)
			writeCtx, done := context.WithTimeout(clientCtx, 2*time.Second)
			err = conn.Write(writeCtx, websocket.MessageText, b)
			done()
			if err != nil {
				return
			}
			select {
			case <-clientCtx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := auth.Authenticate(r, c.Token)
		action := "read"
		if r.Method != "GET" {
			action = "watch"
		}
		if strings.Contains(r.URL.Path, "remote-sessions") {
			action = "remote"
		}
		if !ok {
			respond(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		if !auth.Allowed(identity.Role, action) {
			respond(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		if r.Method != "GET" {
			origin := r.Header.Get("Origin")
			if origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
				respond(w, 403, map[string]string{"error": "origin rejected"})
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 16384)
			defer func() {
				auditCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
				defer done()
				if err := audit.Attempt(auditCtx, db, identity.ID, r.PathValue("id"), r.Method+" "+r.URL.Path); err != nil {
					dbErrors.Add(1)
				}
			}()
		}
		api.ServeHTTP(w, r)
	}))
	srv := &http.Server{Addr: c.HTTP, Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = srv.Shutdown(shutdown)
	}()
	logger.Info("cloud API ready", "event", "listening")
	e = srv.ListenAndServe()
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
