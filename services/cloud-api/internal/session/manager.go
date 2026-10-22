package session

import (
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"sync"
	"time"
)

type Manager struct {
	mu         sync.Mutex
	leases     map[string]contract.LiveSession
	upstream   map[string]time.Time
	TTL, Grace time.Duration
}

func New(ttl, grace time.Duration) *Manager {
	return &Manager{leases: map[string]contract.LiveSession{}, upstream: map[string]time.Time{}, TTL: ttl, Grace: grace}
}
func (m *Manager) Open(id, device, user string, now time.Time) (contract.LiveSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, exists := m.upstream[device]
	s := contract.LiveSession{SessionId: id, DeviceId: device, UserId: user, ExpiresAt: now.Add(m.TTL), TtlSeconds: int(m.TTL / time.Second), TelemetryHz: 1}
	m.leases[id] = s
	m.upstream[device] = s.ExpiresAt.Add(m.Grace)
	return s, !exists
}
func (m *Manager) Renew(id, user string, now time.Time) (contract.LiveSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.leases[id]
	if !ok || s.UserId != user || !now.Before(s.ExpiresAt) {
		return s, false
	}
	s.ExpiresAt = now.Add(m.TTL)
	m.leases[id] = s
	m.extend(s.DeviceId, now)
	return s, true
}
func (m *Manager) Close(id, user string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.leases[id]
	if !ok || s.UserId != user {
		return false
	}
	delete(m.leases, id)
	m.extend(s.DeviceId, now)
	return true
}
func (m *Manager) extend(device string, now time.Time) {
	end := now
	for _, s := range m.leases {
		if s.DeviceId == device && s.ExpiresAt.After(end) {
			end = s.ExpiresAt
		}
	}
	m.upstream[device] = end.Add(m.Grace)
}
func (m *Manager) Sweep(now time.Time) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.leases {
		if !now.Before(s.ExpiresAt) {
			delete(m.leases, id)
		}
	}
	stopped := []string{}
	for d, end := range m.upstream {
		if !now.Before(end) {
			delete(m.upstream, d)
			stopped = append(stopped, d)
		}
	}
	return stopped
}
func (m *Manager) Count() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.leases) }
