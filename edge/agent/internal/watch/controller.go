// Package watch implements a single device's durable generation fence.
package watch

import (
	"encoding/json"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/platform/atomicfile"
	"os"
	"time"
)

type record struct {
	ID     string `json:"id"`
	Result string `json:"result"`
}
type state struct {
	Generation int64    `json:"generation"`
	LeaseID    string   `json:"leaseId"`
	Seen       []record `json:"seen"`
}

// Controller is owned by the Agent loop. Startup restores fencing, never a LIVE timer.
type Controller struct {
	path    string
	state   state
	Until   time.Time
	Effects int
}

func Open(path string) (*Controller, error) {
	c := &Controller{path: path}
	b, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if len(b) > 0 {
		if e = json.Unmarshal(b, &c.state); e != nil {
			return nil, e
		}
	}
	return c, nil
}
func (c *Controller) Generation() int64         { return c.state.Generation }
func (c *Controller) LeaseID() string           { return c.state.LeaseID }
func (c *Controller) Active(now time.Time) bool { return now.Before(c.Until) }
func (c *Controller) Disconnect()               { c.Until = time.Time{} }

// deadline is derived from the broker's remaining Message Expiry on receipt,
// retaining Go's monotonic clock. Device UTC / payload expiresAt is not authoritative.
func (c *Controller) Apply(m contract.Envelope, now, deadline time.Time) (contract.WatchAckPayload, error) {
	p := contract.Payload[contract.WatchCommandPayload](m)
	ack := contract.WatchAckPayload{CommandId: m.MessageId, LeaseId: p.LeaseId, Generation: p.Generation, Action: m.Kind, Result: "APPLIED", Reason: "", EdgeState: "SILENT", AppliedAt: now.UTC()}
	if c.Active(now) {
		ack.EdgeState = "LIVE"
	}
	for _, r := range c.state.Seen {
		if r.ID == m.MessageId {
			ack.Result = r.Result
			if ack.Result == "APPLIED" {
				ack.Result = "DUPLICATE"
			}
			return ack, nil
		}
	}
	next := c.state
	next.Seen = append([]record{}, next.Seen...)
	until := c.Until
	switch {
	case p.Generation < c.state.Generation:
		ack.Result = "STALE"
		ack.Reason = "older generation"
	case !now.Before(deadline):
		ack.Result = "EXPIRED"
		ack.Reason = "command deadline elapsed"
	case m.Kind == "WATCH_START" && p.Generation > c.state.Generation:
		next.Generation = p.Generation
		next.LeaseID = p.LeaseId
		until = now.Add(time.Duration(p.TtlSeconds) * time.Second)
	case (m.Kind == "WATCH_RENEW" || m.Kind == "WATCH_STOP") && p.Generation == c.state.Generation && p.LeaseId == c.state.LeaseID:
		until = time.Time{}
		if m.Kind == "WATCH_RENEW" {
			until = now.Add(time.Duration(p.TtlSeconds) * time.Second)
		}
	default:
		ack.Result = "REJECTED"
		ack.Reason = "invalid generation or lease transition"
	}
	next.Seen = append(next.Seen, record{ID: m.MessageId, Result: ack.Result})
	if len(next.Seen) > 1024 {
		next.Seen = next.Seen[len(next.Seen)-1024:]
	}
	b, e := json.Marshal(next)
	if e != nil {
		return ack, e
	}
	if e = atomicfile.Write(c.path, b); e != nil {
		return ack, e
	}
	c.state = next
	if ack.Result == "APPLIED" {
		c.Until = until
		c.Effects++
	}
	if c.Active(now) {
		ack.EdgeState = "LIVE"
	} else {
		ack.EdgeState = "SILENT"
	}
	return ack, nil
}
