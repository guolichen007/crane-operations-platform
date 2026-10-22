package device

import (
	"context"
	"database/sql"
	"encoding/json"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	shadoworder "github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/shadow"
	"github.com/guolichen007/crane-operations-platform/services/cloud-api/internal/telemetry"
	"sync"
	"time"
)

type Store struct {
	DB     *sql.DB
	mu     sync.RWMutex
	cache  map[string]contract.Shadow
	sample map[string]time.Time
}

func New(ctx context.Context, db *sql.DB) (*Store, error) {
	s := &Store{DB: db, cache: map[string]contract.Shadow{}, sample: map[string]time.Time{}}
	rows, e := db.QueryContext(ctx, "SELECT payload_json FROM device_shadow")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var v contract.Shadow
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		s.cache[v.DeviceId] = v
	}
	return s, rows.Err()
}
func (s *Store) Get(id string) (contract.Shadow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.cache[id]
	return v, ok
}
func (s *Store) List() []contract.Shadow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := make([]contract.Shadow, 0, len(s.cache))
	for _, m := range s.cache {
		v = append(v, m)
	}
	return v
}
func (s *Store) Apply(ctx context.Context, m contract.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.cache[m.DeviceId]
	if !shadoworder.Accept(old.Reported, m) {
		return nil
	}
	if m.Kind == "LWT" {
		next := old.Reported
		next.Connectivity = "OFFLINE"
		next.EdgeHealth = "UNKNOWN"
		next.ControllerHealth = "UNKNOWN"
		m = next
	}
	next := contract.Shadow{DeviceId: m.DeviceId, TenantId: m.TenantId, ProjectId: m.ProjectId, Reported: m, UpdatedAt: time.Now().UTC(), Version: old.Version + 1}
	b, e := json.Marshal(next)
	if e != nil {
		return e
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "INSERT INTO devices(id,project_id) VALUES($1,$2) ON CONFLICT DO NOTHING", m.DeviceId, m.ProjectId); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO device_shadow(device_id,payload_json,updated_at,version) VALUES($1,$2,$3,$4) ON CONFLICT(device_id) DO UPDATE SET payload_json=excluded.payload_json,updated_at=excluded.updated_at,version=excluded.version", m.DeviceId, b, next.UpdatedAt, next.Version); e != nil {
		return e
	}
	if m.Kind == "HEARTBEAT" {
		_, e = tx.ExecContext(ctx, "INSERT INTO device_heartbeats VALUES($1,$2) ON CONFLICT(device_id) DO UPDATE SET observed_at=excluded.observed_at", m.DeviceId, next.UpdatedAt)
		if e != nil {
			return e
		}
	}
	if m.Kind == "LIVE" && time.Since(s.sample[m.DeviceId]) >= telemetry.SampleInterval {
		payload, _ := json.Marshal(m)
		if _, e = tx.ExecContext(ctx, "INSERT INTO telemetry_samples(device_id,sampled_at,payload_json) VALUES($1,$2,$3)", m.DeviceId, next.UpdatedAt, payload); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	if m.Kind == "LIVE" && time.Since(s.sample[m.DeviceId]) >= telemetry.SampleInterval {
		s.sample[m.DeviceId] = time.Now()
	}
	s.cache[m.DeviceId] = next
	return nil
}
