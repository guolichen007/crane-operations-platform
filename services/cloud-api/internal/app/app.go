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
	leases := session.New(time.Duration(c.TTL)*time.Second, time.Duration(c.Grace)*time.Second)
	var mqttMessages, dbErrors, wsClients atomic.Int64
	var mqttUp atomic.Bool
	broker, e := platform.Connect(ctx, c, "cloud-api", []string{"crane/v1/demo/Project-A/+/heartbeat", "crane/v1/demo/Project-A/+/shadow/reported", "crane/v1/demo/Project-A/+/telemetry/live", "crane/v1/demo/Project-A/+/alarm", "crane/v1/demo/Project-A/+/event", "crane/v1/demo/Project-A/+/command/ack"}, nil, func(topic string, b []byte) error {
		mqttMessages.Add(1)
		var m contract.Message
		t, p, d, suffix, err := contract.ParseTopic(topic)
		if err != nil || json.Unmarshal(b, &m) != nil || contract.Validate(m) != nil || m.TenantId != t || m.ProjectId != p || m.DeviceId != d {
			logger.Warn("invalid MQTT envelope", "event", "message_rejected")
			return nil
		}
		valid := map[string]map[string]bool{"shadow/reported": {"BIRTH": true, "SNAPSHOT": true, "LWT": true}, "heartbeat": {"HEARTBEAT": true}, "telemetry/live": {"LIVE": true}, "alarm": {"ALARM": true}, "event": {"EVENT": true}, "command/ack": {"WATCH_ACK": true}}
		if !valid[suffix][m.Kind] {
			return nil
		}
		for {
			callCtx, done := context.WithTimeout(ctx, 5*time.Second)
			if m.Kind == "ALARM" || m.Kind == "EVENT" {
				err = alarm.Record(callCtx, db, m)
			} else if m.Kind != "WATCH_ACK" {
				err = store.Apply(callCtx, m)
			}
			done()
			if err == nil {
				return nil
			}
			dbErrors.Add(1)
			logger.Error("message persistence failed; acknowledgment withheld", "event", "db_write_failed", "deviceId", d)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}, func() { mqttUp.Store(true) }, func() { mqttUp.Store(false) })
	if e != nil {
		return e
	}
	defer broker.CM.Disconnect(context.Background())
	command := func(deviceID, kind, sid string) error {
		v, ok := store.Get(deviceID)
		if !ok {
			return errors.New("unknown device")
		}
		suffix := map[string]string{"WATCH_START": "command/watch/start", "WATCH_RENEW": "command/watch/renew", "WATCH_STOP": "command/watch/stop"}[kind]
		m := contract.Message{SchemaVersion: 1, MessageId: platform.ID(), TenantId: v.TenantId, ProjectId: v.ProjectId, DeviceId: deviceID, BootId: "cloud", Sequence: time.Now().UnixMilli(), Timestamp: time.Now().UTC(), Kind: kind, SessionId: sid, TtlSeconds: c.TTL + c.Grace, TelemetryHz: 1}
		if m.TtlSeconds > 300 {
			m.TtlSeconds = 300
		}
		return broker.Publish(ctx, m, suffix)
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				for _, d := range leases.Sweep(now) {
					if err := command(d, "WATCH_STOP", ""); err != nil {
						logger.Warn("watch stop failed; edge TTL remains authoritative", "event", "watch_stop_failed")
					}
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
			if v.Reported.Connectivity == "MQTT_CONNECTED" {
				connected++
			}
		}
		alarms, err := alarm.List(r.Context(), db)
		if err != nil {
			dbErrors.Add(1)
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "mqtt_connected_devices %d\ndevice_live_sessions %d\nremote_sessions 0\nmqtt_messages_total %d\nalarms_total %d\npointcloud_frames_total 0\npointcloud_frames_dropped_total 0\nwebsocket_clients %d\ndb_query_errors_total %d\n", connected, leases.Count(), mqttMessages.Load(), len(alarms), wsClients.Load(), dbErrors.Load())
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
	api.HandleFunc("GET /api/v1/devices", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, store.List()) })
	detail := func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Get(r.PathValue("id"))
		if !ok {
			respond(w, 404, map[string]string{"error": "not found"})
			return
		}
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
		s, first := leases.Open(platform.ID(), id, identity.ID, time.Now())
		kind := "WATCH_RENEW"
		if first {
			kind = "WATCH_START"
		}
		if e := command(id, kind, s.SessionId); e != nil {
			leases.Close(s.SessionId, identity.ID, time.Now())
			respond(w, 503, map[string]string{"error": "edge unavailable"})
			return
		}
		respond(w, 201, s)
	})
	api.HandleFunc("POST /api/v1/live-sessions/{sessionId}/renew", func(w http.ResponseWriter, r *http.Request) {
		identity, _ := auth.Authenticate(r, c.Token)
		s, ok := leases.Renew(r.PathValue("sessionId"), identity.ID, time.Now())
		if !ok {
			respond(w, 404, map[string]string{"error": "expired session"})
			return
		}
		if e := command(s.DeviceId, "WATCH_RENEW", s.SessionId); e != nil {
			respond(w, 503, map[string]string{"error": "edge unavailable"})
			return
		}
		respond(w, 200, s)
	})
	api.HandleFunc("DELETE /api/v1/live-sessions/{sessionId}", func(w http.ResponseWriter, r *http.Request) {
		identity, _ := auth.Authenticate(r, c.Token)
		if !leases.Close(r.PathValue("sessionId"), identity.ID, time.Now()) {
			respond(w, 404, map[string]string{"error": "not found"})
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
			b, _ := json.Marshal(v)
			writeCtx, done := context.WithTimeout(clientCtx, 2*time.Second)
			err := conn.Write(writeCtx, websocket.MessageText, b)
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
