package domain

import (
	"sync"
	"time"
)

type State string

const (
	Disconnected State = "DISCONNECTED"
	Silent       State = "SILENT"
	Live         State = "LIVE"
	Event        State = "EVENT"
)

type Machine struct {
	mu        sync.Mutex
	connected bool
	lease     time.Time
	events    int
}

func (m *Machine) Connect() { m.mu.Lock(); defer m.mu.Unlock(); m.connected = true }
func (m *Machine) Disconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = false
	m.lease = time.Time{}
}
func (m *Machine) Watch(now time.Time, ttl int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ttl < 1 || ttl > 300 {
		return
	}
	m.lease = now.Add(time.Duration(ttl) * time.Second)
}
func (m *Machine) Stop()       { m.mu.Lock(); defer m.mu.Unlock(); m.lease = time.Time{} }
func (m *Machine) BeginEvent() { m.mu.Lock(); defer m.mu.Unlock(); m.events++ }
func (m *Machine) AckEvent() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.events > 0 {
		m.events--
	}
}
func (m *Machine) State(now time.Time) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.connected {
		return Disconnected
	}
	if m.events > 0 {
		return Event
	}
	if now.Before(m.lease) {
		return Live
	}
	return Silent
}
