package domain

import (
	"testing"
	"time"
)

func TestStateTransitions(t *testing.T) {
	m := Machine{}
	now := time.Now()
	if m.State(now) != Disconnected {
		t.Fatal("initial")
	}
	m.Connect()
	if m.State(now) != Silent {
		t.Fatal("connect")
	}
	m.BeginEvent()
	if m.State(now) != Event {
		t.Fatal("alarm suppressed")
	}
	m.AckEvent()
	if m.State(now) != Silent {
		t.Fatal("event return")
	}
	m.Watch(now, 5)
	m.BeginEvent()
	m.AckEvent()
	if m.State(now) != Live {
		t.Fatal("return live")
	}
	if m.State(now.Add(6*time.Second)) != Silent {
		t.Fatal("lease did not expire")
	}
	m.Disconnect()
	if m.State(now) != Disconnected {
		t.Fatal("disconnect")
	}
}
