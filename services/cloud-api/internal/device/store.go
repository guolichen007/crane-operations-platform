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
func (s *Store) Apply(ctx context.Context, m contract.Envelope) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.cache[m.DeviceId]
	next, accepted := shadoworder.Merge(old, m, time.Now().UTC())
	if !accepted {
		return false, nil
	}
	b, e := json.Marshal(next)
	if e != nil {
		return false, e
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "INSERT INTO devices(id,project_id,source_mode) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", m.DeviceId, m.ProjectId, legacySource(m.DeviceId)); e != nil {
		return false, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO device_shadow(device_id,payload_json,updated_at,version) VALUES($1,$2,$3,$4) ON CONFLICT(device_id) DO UPDATE SET payload_json=excluded.payload_json,updated_at=excluded.updated_at,version=excluded.version", m.DeviceId, b, next.UpdatedAt, next.Version); e != nil {
		return false, e
	}
	if m.Kind == "HEARTBEAT" {
		_, e = tx.ExecContext(ctx, "INSERT INTO device_heartbeats VALUES($1,$2) ON CONFLICT(device_id) DO UPDATE SET observed_at=excluded.observed_at", m.DeviceId, next.UpdatedAt)
		if e != nil {
			return false, e
		}
	}
	if m.Kind == "LIVE" && time.Since(s.sample[m.DeviceId]) >= telemetry.SampleInterval {
		payload, _ := json.Marshal(m)
		if _, e = tx.ExecContext(ctx, "INSERT INTO telemetry_samples(device_id,sampled_at,payload_json) VALUES($1,$2,$3)", m.DeviceId, next.UpdatedAt, payload); e != nil {
			return false, e
		}
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	if m.Kind == "LIVE" && time.Since(s.sample[m.DeviceId]) >= telemetry.SampleInterval {
		s.sample[m.DeviceId] = time.Now()
	}
	s.cache[m.DeviceId] = next
	return true, nil
}

// Only the retained count-mode fixture can infer simulation provenance without inventory.
func legacySource(id string) string {
	if len(id) == 7 && id[:4] == "SIM-" && id[4] >= '0' && id[4] <= '9' && id[5] >= '0' && id[5] <= '9' && id[6] >= '0' && id[6] <= '9' {
		return "SIMULATION"
	}
	return "TEST"
}
