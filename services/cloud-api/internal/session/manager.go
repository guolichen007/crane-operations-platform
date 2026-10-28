// Package session persists viewer leases and one desired upstream per device.
package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"sync"
	"sync/atomic"
	"time"
)

var ErrNotFound = errors.New("viewer lease not found or expired")

type Manager struct {
	DB                          *sql.DB
	mu                          sync.Mutex
	TTL, Grace                  time.Duration
	boot                        string
	seq                         int64
	Sent, Acked, Stale, Expired atomic.Int64
}
type upstream struct {
	Device, Lease, Status, Last string
	Generation                  int64
	Desired, Updated            time.Time
}

func New(db *sql.DB, ttl, grace time.Duration) *Manager {
	return &Manager{DB: db, TTL: ttl, Grace: grace, boot: platform.ID()}
}
func load(ctx context.Context, tx *sql.Tx, device string) (upstream, error) {
	s := upstream{Device: device, Status: "SILENT"}
	e := tx.QueryRowContext(ctx, `SELECT lease_id,generation,status,desired_until,last_command_id,updated_at FROM device_live_state WHERE device_id=$1`, device).Scan(&s.Lease, &s.Generation, &s.Status, &s.Desired, &s.Last, &s.Updated)
	if errors.Is(e, sql.ErrNoRows) {
		return s, nil
	}
	return s, e
}
func save(ctx context.Context, tx *sql.Tx, s upstream) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO device_live_state(device_id,lease_id,generation,status,desired_until,last_command_id,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT(device_id) DO UPDATE SET lease_id=excluded.lease_id,generation=excluded.generation,status=excluded.status,desired_until=excluded.desired_until,last_command_id=excluded.last_command_id,updated_at=excluded.updated_at`, s.Device, s.Lease, s.Generation, s.Status, s.Desired, s.Last, s.Updated)
	return e
}
func (m *Manager) desired(ctx context.Context, tx *sql.Tx, s *upstream) error {
	var end sql.NullTime
	if e := tx.QueryRowContext(ctx, `SELECT max(expires_at) FROM live_sessions WHERE device_id=$1`, s.Device).Scan(&end); e != nil {
		return e
	}
	if end.Valid {
		s.Desired = end.Time.Add(m.Grace)
	}
	return nil
}
func (m *Manager) issue(ctx context.Context, tx *sql.Tx, s *upstream, kind string, now time.Time) error {
	if kind == "WATCH_START" {
		s.Generation++
		s.Lease = platform.ID()
		s.Status = "STARTING"
	}
	if kind == "WATCH_STOP" {
		s.Status = "STOPPING"
	}
	ttl := int((m.TTL + m.Grace) / time.Second)
	if ttl > 300 {
		ttl = 300
	}
	delivery := min(ttl, 10)
	var tenant, project string
	if e := tx.QueryRowContext(ctx, `SELECT p.tenant_id,d.project_id FROM devices d JOIN projects p ON p.id=d.project_id WHERE d.id=$1`, s.Device).Scan(&tenant, &project); e != nil {
		return e
	}
	m.seq++
	command := contract.WithPayload(contract.Envelope{SchemaVersion: 1, MessageId: platform.ID(), TenantId: tenant, ProjectId: project, DeviceId: s.Device, BootId: m.boot, BootSeq: 1, Sequence: m.seq, SentAt: now.UTC(), Kind: kind}, contract.WatchCommandPayload{LeaseId: s.Lease, Generation: s.Generation, TtlSeconds: ttl, TelemetryHz: 1, ExpiresAt: now.Add(time.Duration(delivery) * time.Second).UTC()})
	b, e := json.Marshal(command)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO device_commands(command_id,device_id,kind,generation,status,created_at,expires_at,payload_json) VALUES($1,$2,$3,$4,'PENDING',$5,$6,$7)`, command.MessageId, s.Device, kind, s.Generation, now, now.Add(time.Duration(delivery)*time.Second), b)
	if e != nil {
		return e
	}
	s.Last = command.MessageId
	s.Updated = now
	return save(ctx, tx, *s)
}
func (m *Manager) Open(ctx context.Context, id, device, user string, now time.Time) (contract.LiveSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, e := m.DB.BeginTx(ctx, nil)
	if e != nil {
		return contract.LiveSession{}, e
	}
	defer tx.Rollback()
	result := contract.LiveSession{SessionId: id, DeviceId: device, UserId: user, ExpiresAt: now.Add(m.TTL).UTC(), TtlSeconds: int(m.TTL / time.Second), TelemetryHz: 1}
	if e = identity.RuntimeTransaction(ctx, tx, device, user); e != nil {
		return result, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO live_sessions(session_id,device_id,user_id,expires_at) VALUES($1,$2,$3,$4)`, id, device, user, result.ExpiresAt); e != nil {
		return result, e
	}
	s, e := load(ctx, tx, device)
	if e != nil {
		return result, e
	}
	if e = m.desired(ctx, tx, &s); e != nil {
		return result, e
	}
	if s.Status == "SILENT" || s.Status == "STOPPING" {
		e = m.issue(ctx, tx, &s, "WATCH_START", now)
	} else {
		s.Updated = now
		e = save(ctx, tx, s)
	}
	if e != nil {
		return result, e
	}
	result.UpstreamStatus = s.Status
	return result, auditlog.Commit(ctx, tx)
}
func (m *Manager) Renew(ctx context.Context, id, user string, now time.Time) (contract.LiveSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, e := m.DB.BeginTx(ctx, nil)
	if e != nil {
		return contract.LiveSession{}, e
	}
	defer tx.Rollback()
	s := contract.LiveSession{SessionId: id, UserId: user, ExpiresAt: now.Add(m.TTL).UTC(), TtlSeconds: int(m.TTL / time.Second), TelemetryHz: 1}
	e = tx.QueryRowContext(ctx, `UPDATE live_sessions SET expires_at=$3 WHERE session_id=$1 AND user_id=$2 AND expires_at>$4 RETURNING device_id`, id, user, s.ExpiresAt, now).Scan(&s.DeviceId)
	if errors.Is(e, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	if e != nil {
		return s, e
	}
	if e = identity.RuntimeTransaction(ctx, tx, s.DeviceId, user); e != nil {
		return s, e
	}
	u, e := load(ctx, tx, s.DeviceId)
	if e != nil {
		return s, e
	}
	if e = m.desired(ctx, tx, &u); e != nil {
		return s, e
	}
	if e = save(ctx, tx, u); e != nil {
		return s, e
	}
	s.UpstreamStatus = u.Status
	return s, auditlog.Commit(ctx, tx)
}
func (m *Manager) Close(ctx context.Context, id, user string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, e := m.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var device string
	e = tx.QueryRowContext(ctx, `DELETE FROM live_sessions WHERE session_id=$1 AND user_id=$2 RETURNING device_id`, id, user).Scan(&device)
	if errors.Is(e, sql.ErrNoRows) {
		return ErrNotFound
	}
	if e != nil {
		return e
	}
	if e = identity.RuntimeTransaction(ctx, tx, device, user); e != nil {
		return e
	}
	s, e := load(ctx, tx, device)
	if e != nil {
		return e
	}
	s.Desired = now.Add(m.Grace)
	if e = m.desired(ctx, tx, &s); e != nil {
		return e
	}
	if e = save(ctx, tx, s); e != nil {
		return e
	}
	return auditlog.Commit(ctx, tx)
}

// Reconcile runs after BIRTH and after Cloud's MQTT reconnect. PostgreSQL is the
// authority; a fresh generation fences all queued commands from the old connection.
func (m *Manager) Reconcile(ctx context.Context, device string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, e := m.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	s, e := load(ctx, tx, device)
	if e != nil {
		return e
	}
	if s.Generation == 0 {
		return nil
	}
	if e = m.desired(ctx, tx, &s); e != nil {
		return e
	}
	if s.Desired.After(now) {
		e = m.issue(ctx, tx, &s, "WATCH_START", now)
	} else if s.Status != "SILENT" {
		e = m.issue(ctx, tx, &s, "WATCH_STOP", now)
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (m *Manager) Devices(ctx context.Context) ([]string, error) {
	rows, e := m.DB.QueryContext(ctx, `SELECT device_id FROM device_live_state`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Tick is a small durable outbox/reconciler, not a general command bus.
func (m *Manager) Tick(ctx context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, e := m.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	expired, e := tx.ExecContext(ctx, `UPDATE device_commands SET status='EXPIRED',result='EXPIRED' WHERE status='PENDING' AND expires_at<=$1`, now)
	if e != nil {
		return e
	}
	n, _ := expired.RowsAffected()
	if _, e = tx.ExecContext(ctx, `DELETE FROM live_sessions WHERE expires_at<=$1`, now); e != nil {
		return e
	}
	rows, e := tx.QueryContext(ctx, `SELECT device_id,lease_id,generation,status,desired_until,last_command_id,updated_at FROM device_live_state`)
	if e != nil {
		return e
	}
	var all []upstream
	for rows.Next() {
		var s upstream
		if e = rows.Scan(&s.Device, &s.Lease, &s.Generation, &s.Status, &s.Desired, &s.Last, &s.Updated); e != nil {
			rows.Close()
			return e
		}
		all = append(all, s)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, s := range all {
		var kind, status string
		var created, deadline time.Time
		if e = tx.QueryRowContext(ctx, `SELECT kind,status,created_at,expires_at FROM device_commands WHERE command_id=$1`, s.Last).Scan(&kind, &status, &created, &deadline); e != nil {
			return e
		}
		if !s.Desired.After(now) {
			if s.Status != "SILENT" && kind != "WATCH_STOP" {
				if e = m.issue(ctx, tx, &s, "WATCH_STOP", now); e != nil {
					return e
				}
			}
			if kind == "WATCH_STOP" && status == "EXPIRED" {
				s.Status = "SILENT"
				s.Updated = now
				if e = save(ctx, tx, s); e != nil {
					return e
				}
			}
		} else if status == "FAILED" || (status == "EXPIRED" && now.Sub(deadline) >= 5*time.Second) {
			if e = m.issue(ctx, tx, &s, "WATCH_START", now); e != nil {
				return e
			}
		} else if status == "APPLIED" && now.Sub(created) >= min(m.TTL/3, 60*time.Second) {
			if e = m.issue(ctx, tx, &s, "WATCH_RENEW", now); e != nil {
				return e
			}
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	m.Expired.Add(n)
	return nil
}
func (m *Manager) Pending(ctx context.Context, now time.Time) ([]contract.Envelope, error) {
	rows, e := m.DB.QueryContext(ctx, `SELECT c.payload_json FROM device_commands c JOIN device_live_state s ON s.last_command_id=c.command_id WHERE c.status='PENDING' AND c.expires_at>$1 AND (c.last_sent_at IS NULL OR c.last_sent_at<$1-interval '2 seconds')`, now)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []contract.Envelope{}
	for rows.Next() {
		var b []byte
		var v contract.Envelope
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (m *Manager) Published(ctx context.Context, id string, now time.Time) error {
	_, e := m.DB.ExecContext(ctx, `UPDATE device_commands SET last_sent_at=$2 WHERE command_id=$1`, id, now)
	if e == nil {
		m.Sent.Add(1)
	}
	return e
}
func (m *Manager) Ack(ctx context.Context, device string, a contract.WatchAckPayload, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, e := m.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var kind, status string
	var generation int64
	var b []byte
	e = tx.QueryRowContext(ctx, `SELECT kind,generation,status,payload_json FROM device_commands WHERE command_id=$1 AND device_id=$2`, a.CommandId, device).Scan(&kind, &generation, &status, &b)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	var original contract.Envelope
	if e = json.Unmarshal(b, &original); e != nil {
		return e
	}
	if a.Action != kind || a.Generation != generation || a.LeaseId != contract.Payload[contract.WatchCommandPayload](original).LeaseId {
		return errors.New("ACK does not match command")
	}
	if status != "PENDING" {
		return nil
	}
	result := ackStatus(kind, a)
	if _, e = tx.ExecContext(ctx, `UPDATE device_commands SET status=$2,acked_at=$3,result=$4 WHERE command_id=$1`, a.CommandId, result, now, a.Result); e != nil {
		return e
	}
	if result == "APPLIED" {
		next := "LIVE"
		if kind == "WATCH_STOP" {
			next = "SILENT"
		}
		_, e = tx.ExecContext(ctx, `UPDATE device_live_state SET status=$2,updated_at=$3 WHERE device_id=$1 AND last_command_id=$4 AND generation=$5`, device, next, now, a.CommandId, generation)
		if e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	m.Acked.Add(1)
	if a.Result == "STALE" {
		m.Stale.Add(1)
	}
	if result == "EXPIRED" {
		m.Expired.Add(1)
	}
	return nil
}
func ackStatus(kind string, a contract.WatchAckPayload) string {
	if a.Result == "EXPIRED" {
		return "EXPIRED"
	}
	if (a.Result == "APPLIED" || a.Result == "DUPLICATE") && ((kind == "WATCH_STOP" && a.EdgeState == "SILENT") || (kind != "WATCH_STOP" && a.EdgeState == "LIVE")) {
		return "APPLIED"
	}
	return "FAILED"
}
func (m *Manager) Status(ctx context.Context, device string) (string, error) {
	var state string
	var status sql.NullString
	e := m.DB.QueryRowContext(ctx, `SELECT s.status,c.status FROM device_live_state s LEFT JOIN device_commands c ON c.command_id=s.last_command_id WHERE s.device_id=$1`, device).Scan(&state, &status)
	if errors.Is(e, sql.ErrNoRows) {
		return "SILENT", nil
	}
	if e != nil {
		return "", e
	}
	if status.String == "FAILED" || status.String == "EXPIRED" {
		return status.String, nil
	}
	return state, nil
}
func (m *Manager) Counts(ctx context.Context) (viewers, active int, err error) {
	err = m.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM live_sessions WHERE expires_at>now()),(SELECT count(*) FROM device_live_state WHERE status='LIVE' AND desired_until>now())`).Scan(&viewers, &active)
	return
}
func (m *Manager) Metrics(ctx context.Context) string {
	v, a, e := m.Counts(ctx)
	if e != nil {
		return ""
	}
	return fmt.Sprintf("device_live_sessions %d\ndevice_live_upstream_active %d\nwatch_commands_sent_total %d\nwatch_commands_acked_total %d\nwatch_commands_stale_total %d\nwatch_commands_expired_total %d\n", v, a, m.Sent.Load(), m.Acked.Load(), m.Stale.Load(), m.Expired.Load())
}
func (m *Manager) AcceptLive(ctx context.Context, device, lease string, generation int64) (bool, error) {
	var allowed bool
	e := m.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM device_live_state WHERE device_id=$1 AND lease_id=$2 AND generation=$3 AND desired_until>now() AND status IN ('STARTING','LIVE'))`, device, lease, generation).Scan(&allowed)
	return allowed, e
}
func (m *Manager) Offline(ctx context.Context, device string) error {
	_, e := m.DB.ExecContext(ctx, `UPDATE device_live_state SET status='STARTING' WHERE device_id=$1 AND status='LIVE'`, device)
	return e
}
