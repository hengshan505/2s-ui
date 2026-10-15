package service

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/shenaba/2s-ui/database/model"

	"github.com/sagernet/sing-box/adapter"
)

// Package state outlives one test.
func resetClientRates(t *testing.T) {
	t.Helper()
	reset := func() {
		drainMu.Lock()
		lastDrain = time.Time{}
		drainMu.Unlock()
		setClientRates(map[string]ClientRate{})
	}
	reset()
	t.Cleanup(reset)
}

// A rate is one drain's bytes over the window it covered, per client and per
// direction; the drain's inbound and outbound rows are not clients.
func TestRatesOfDividesTheDrainByItsWindow(t *testing.T) {
	drained := []model.Stats{
		{Resource: "user", Tag: "alice", Direction: true, Traffic: 2000},
		{Resource: "user", Tag: "alice", Direction: false, Traffic: 4000},
		{Resource: "user", Tag: "bob", Direction: false, Traffic: 100},
		{Resource: "inbound", Tag: "alice", Direction: true, Traffic: 9999},
		{Resource: "outbound", Tag: "direct", Direction: false, Traffic: 9999},
	}
	got := ratesOf(drained, 2*time.Second)
	want := map[string]ClientRate{
		"alice": {Up: 1000, Down: 2000},
		"bob":   {Up: 0, Down: 50},
	}
	if len(got) != len(want) {
		t.Fatalf("rates %v, want %v", got, want)
	}
	for name, rate := range want {
		if got[name] != rate {
			t.Errorf("%s = %+v, want %+v", name, got[name], rate)
		}
	}
}

// The first drain since start has no window to divide by -- its counters ran
// from whenever the core started -- and one that closes a few milliseconds
// after carryOverStats must not turn its bytes into a burst.
func TestRatesOfWithoutAUsableWindow(t *testing.T) {
	drained := []model.Stats{{Resource: "user", Tag: "alice", Direction: true, Traffic: 2000}}

	got := ratesOf(drained, 0)
	if got == nil || len(got) != 0 {
		t.Errorf("no window = %v, want an empty map (it is published, and nil marshals as null)", got)
	}
	if got := ratesOf(drained, 10*time.Millisecond); got["alice"].Up != 2000 {
		t.Errorf("a 10ms window = %+v, want it floored to %v", got["alice"], minRateWindow)
	}
}

func TestMarkDrainMeasuresFromThePreviousDrain(t *testing.T) {
	resetClientRates(t)
	start := time.Now()
	if got := markDrain(start); got != 0 {
		t.Errorf("first drain = %v, want 0: there is nothing to measure from", got)
	}
	if got := markDrain(start.Add(10 * time.Second)); got != 10*time.Second {
		t.Errorf("second drain = %v, want 10s", got)
	}
}

// End to end against a real core: a flush publishes what the drain held over
// the time since the previous one, and what a stop carried over -- bytes from
// an earlier window, committed by the next flush -- is not counted again as
// that flush's speed.
func TestFlushPublishesClientRates(t *testing.T) {
	s := runningPanel(t)
	resetClientRates(t)
	takePending()
	t.Cleanup(func() { takePending() })

	box := corePtr.GetInstance()
	send := func(n int) {
		t.Helper()
		near, far := net.Pipe()
		counted := box.StatsTracker().RoutedConnection(context.Background(), near, adapter.InboundContext{User: "alice"}, nil, box.Outbound().Default())
		go func() { _, _ = io.Copy(io.Discard, far) }()
		if _, err := counted.Write(make([]byte, n)); err != nil {
			t.Fatalf("write: %v", err)
		}
		_ = counted.Close()
	}
	var stats StatsService

	// The window is set by hand rather than slept through.
	markDrain(time.Now().Add(-2 * time.Second))
	send(1000)
	if err := stats.SaveStats(true, 10); err != nil {
		t.Fatalf("flush: %v", err)
	}
	rate := stats.GetClientRates()["alice"]
	// Writes toward the client are its download. A little over 2s has passed,
	// so the rate is a little under 500.
	if rate.Down < 400 || rate.Down > 500 || rate.Up != 0 {
		t.Errorf("alice = %+v, want ~500 B/s down over a 2s window", rate)
	}

	send(1000)
	if err := s.StopCore(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := stats.SaveStats(true, 10); err != nil {
		t.Fatalf("flush with the core down: %v", err)
	}
	if rates := stats.GetClientRates(); len(rates) != 0 {
		t.Errorf("rates with the core down = %v, want none: the carried bytes are an earlier window's", rates)
	}
}
