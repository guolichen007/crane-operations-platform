// Package spool keeps bounded critical events; telemetry is never spooled.
package spool

import (
	"encoding/json"
	"errors"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"os"
	"path/filepath"
	"sync"
)

type Queue struct {
	mu    sync.Mutex
	path  string
	items []contract.Message
	limit int
}

func Open(path string, limit int) (*Queue, error) {
	q := &Queue{path: path, limit: limit}
	b, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if len(b) > 0 {
		if e = json.Unmarshal(b, &q.items); e != nil {
			return nil, e
		}
	}
	if len(q.items) > limit {
		return nil, errors.New("spool over limit")
	}
	return q, nil
}
func (q *Queue) save() error {
	if e := os.MkdirAll(filepath.Dir(q.path), 0700); e != nil {
		return e
	}
	b, e := json.Marshal(q.items)
	if e != nil {
		return e
	}
	if e = os.WriteFile(q.path+".tmp", b, 0600); e != nil {
		return e
	}
	return os.Rename(q.path+".tmp", q.path)
}
func (q *Queue) Push(m contract.Message) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if m.Kind != "ALARM" && m.Kind != "EVENT" {
		return errors.New("only critical events may be queued")
	}
	for _, v := range q.items {
		if v.MessageId == m.MessageId {
			return nil
		}
	}
	if len(q.items) >= q.limit {
		return errors.New("critical spool full: operator action required")
	}
	q.items = append(q.items, m)
	return q.save()
}
func (q *Queue) Peek() (contract.Message, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return contract.Message{}, false
	}
	return q.items[0], true
}
func (q *Queue) Ack(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) > 0 && q.items[0].MessageId == id {
		q.items = q.items[1:]
	}
	return q.save()
}
