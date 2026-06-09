package notifier

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// gateChannel blocks every Send until the gate closes, simulating a dead
// webhook mid-delivery.
type gateChannel struct {
	gate    chan struct{}
	entered int32
}

func (g *gateChannel) Name() string         { return "gate" }
func (g *gateChannel) Wants(EventType) bool { return true }
func (g *gateChannel) Send(context.Context, Event) error {
	atomic.AddInt32(&g.entered, 1)
	<-g.gate
	return nil
}

// TestSetGlobalDoesNotBlockOnSlowChannelDrain guards against the reload stall:
// App.Reload holds its mutex while calling SetGlobal, and SetGlobal stops the
// previous notifier. Stop drains the queue, where a single wedged webhook send
// can block for the full send timeout — so a synchronous Stop turns a dead
// webhook into a minutes-long app-wide lockup on every settings save. SetGlobal
// must hand the drain off and return promptly.
func TestSetGlobalDoesNotBlockOnSlowChannelDrain(t *testing.T) {
	prev := Global()
	t.Cleanup(func() { SetGlobal(prev) })

	gate := &gateChannel{gate: make(chan struct{})}
	t.Cleanup(func() { close(gate.gate) })

	old := New(Options{
		Enabled:     true,
		Events:      map[EventType]bool{EventPlaybackFailure: true},
		Channels:    []Channel{gate},
		SendTimeout: 30 * time.Second,
	})
	old.Start()
	SetGlobal(old)
	old.Emit(Event{Type: EventPlaybackFailure, Title: "wedge"})
	waitFor(t, func() bool { return atomic.LoadInt32(&gate.entered) == 1 })

	done := make(chan struct{})
	go func() {
		SetGlobal(nil) // stops old, whose worker is wedged in Send
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("SetGlobal blocked on draining the old notifier; the drain must not run synchronously")
	}
}
