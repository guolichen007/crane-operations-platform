// Package spool keeps bounded critical events; telemetry is never spooled.
package spool

import (
	"encoding/json"
	"errors"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/platform/atomicfile"
	"os"
	"sync"
)

type Queue struct {
	mu      sync.Mutex
	path    string
	items   []contract.Envelope
	limit   int
	retries int64
}

var ErrFull = errors.New("critical spool full: operator action required")

func Open(path string, limit int) (*Queue, error) {
	q := &Queue{path: path, limit: limit}
	b, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if e == nil {
		if e = json.Unmarshal(b, &q.items); e != nil {
			return nil, e
		}
	}
	if len(q.items) > limit {
		return nil, errors.New("spool over limit")
	}
	for _, m := range q.items {
		if e = contract.Validate(m); e != nil {
			return nil, e
		}
		if m.Kind != "ALARM" && m.Kind != "EVENT" {
			return nil, errors.New("non-critical spool record")
		}
	}
	return q, nil
}
func (q *Queue) save(items []contract.Envelope) error {
	b, e := json.Marshal(items)
	if e != nil {
		return e
	}
	if e = atomicfile.Write(q.path, b); e != nil {
		return e
	}
	q.items = items
	return nil
}
func (q *Queue) Push(m contract.Envelope) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if m.Kind != "ALARM" && m.Kind != "EVENT" {
		return errors.New("only critical events may be queued")
	}
	if err := contract.Validate(m); err != nil {
		return err
	}
	for _, v := range q.items {
		if v.MessageId == m.MessageId {
			return nil
		}
	}
	if len(q.items) >= q.limit {
		return ErrFull
	}
	items := append(append([]contract.Envelope{}, q.items...), m)
	return q.save(items)
}
func (q *Queue) Peek() (contract.Envelope, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return contract.Envelope{}, false
	}
	return q.items[0], true
}
func (q *Queue) Ack(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) > 0 && q.items[0].MessageId == id {
		return q.save(append([]contract.Envelope{}, q.items[1:]...))
	}
	return nil
}
func (q *Queue) Retry() { q.mu.Lock(); defer q.mu.Unlock(); q.retries++ }
func (q *Queue) Metrics() (depth, full int, retries int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	depth = len(q.items)
	if depth >= q.limit {
		full = 1
	}
	return depth, full, q.retries
}
