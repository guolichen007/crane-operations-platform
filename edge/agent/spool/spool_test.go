package spool

import (
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"os"
	"path/filepath"
	"testing"
)

func TestBoundedDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	q, _ := Open(path, 1)
	m := contract.Envelope{Kind: "ALARM", MessageId: "one"}
	if q.Push(m) != nil {
		t.Fatal("push")
	}
	if q.Push(m) != nil {
		t.Fatal("duplicate push")
	}
	if q.Push(contract.Envelope{Kind: "LIVE"}) == nil {
		t.Fatal("telemetry queued")
	}
	if q.Push(contract.Envelope{Kind: "EVENT", MessageId: "two"}) == nil {
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
	if err := q.Ack("unknown-ack"); err != nil {
		t.Fatal(err)
	}
	if depth, full, _ := q.Metrics(); depth != 1 || full != 1 {
		t.Fatal("unknown ACK removed pending event")
	}
	if q.Ack("one") != nil {
		t.Fatal("ack")
	}
	q, e = Open(path, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok = q.Peek(); ok {
		t.Fatal("application ACK was not durable")
	}
}
func TestFailedPersistenceDoesNotAcknowledgeOrAppend(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "events")
	path := filepath.Join(parent, "queue.json")
	q, err := Open(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Push(contract.Envelope{Kind: "ALARM", MessageId: "one"}); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(parent, parent+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(parent, []byte("blocked parent"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = q.Ack("one"); err == nil {
		t.Fatal("ACK succeeded without durable write")
	}
	if err = q.Push(contract.Envelope{Kind: "EVENT", MessageId: "two"}); err == nil {
		t.Fatal("append succeeded without durable write")
	}
	head, ok := q.Peek()
	depth, _, _ := q.Metrics()
	if !ok || head.MessageId != "one" || depth != 1 {
		t.Fatal("failed write changed queue")
	}
}
