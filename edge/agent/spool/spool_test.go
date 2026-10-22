package spool

import (
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"path/filepath"
	"testing"
)

func TestBoundedDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	q, _ := Open(path, 1)
	m := contract.Message{Kind: "ALARM", MessageId: "one"}
	if q.Push(m) != nil {
		t.Fatal("push")
	}
	if q.Push(m) != nil {
		t.Fatal("duplicate push")
	}
	if q.Push(contract.Message{Kind: "LIVE"}) == nil {
		t.Fatal("telemetry queued")
	}
	if q.Push(contract.Message{Kind: "EVENT", MessageId: "two"}) == nil {
		t.Fatal("unbounded")
	}
	q, e := Open(path, 1)
	if e != nil {
		t.Fatal(e)
	}
	v, ok := q.Peek()
	if !ok || v.MessageId != "one" {
		t.Fatal("lost event")
	}
	if q.Ack("one") != nil {
		t.Fatal("ack")
	}
}
