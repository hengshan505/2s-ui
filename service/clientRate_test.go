package service

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/shenaba/2s-ui/core"

	"github.com/sagernet/sing-box/adapter"
)

// Package state outlives one test.
func resetClientRates(t *testing.T) {
	t.Helper()
	setClientRates(map[string]ClientRate{})
	t.Cleanup(func() { setClientRates(map[string]ClientRate{}) })
}

// A rate is one sample's bytes over the window it covered, per client and per
// direction.
func TestRatesOfDividesTheSampleByItsWindow(t *testing.T) {
	moved := map[string]core.UserBytes{
		"alice": {Up: 2000, Down: 4000},
		"bob":   {Down: 100},
	}
	got := ratesOf(moved, 2*time.Second)
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

// A window of nothing has no speed, and one that spans a pause in sampling is
// no current speed. (Too short a window never gets here: SampleUsers leaves it
// open.)
func TestRatesOfWithoutAUsableWindow(t *testing.T) {
	moved := map[string]core.UserBytes{"alice": {Up: 2000}}

	got := ratesOf(moved, 0)
	if got == nil || len(got) != 0 {
		t.Errorf("no window = %v, want an empty map (it is published, and nil marshals as null)", got)
	}
	if got := ratesOf(moved, time.Minute); len(got) != 0 {
		t.Errorf("a window over a pause = %v, want none: an average over it is no current speed", got)
	}
}

// End to end against a real core: a sample publishes what moved since the
// previous one even when a stats flush emptied the counters in between, a tick
// too soon after the last leaves the rates alone, the first subscriber after a
// pause samples at once and a second one does not, and with the core stopped
// they go empty.
func TestSampleClientRatesFromTheRunningCore(t *testing.T) {
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

	// The windows are set by hand rather than slept through, so how long the
	// flush between them takes does not move the rate. The first one starts a
	// second out, past the minimum window from the core's start.
	base := time.Now().Add(time.Second)
	sampleClientRates(base)
	if rates := stats.GetClientRates(); len(rates) != 0 {
		t.Fatalf("first sample = %v, want none: nothing has moved", rates)
	}
	send(1000)
	if err := stats.SaveStats(true, 10); err != nil {
		t.Fatalf("flush: %v", err)
	}
	setClientRates(map[string]ClientRate{"bob": {Up: 7}})
	sampleClientRates(base.Add(500 * time.Millisecond))
	if rates := stats.GetClientRates(); rates["bob"].Up != 7 || len(rates) != 1 {
		t.Errorf("rates after a 500ms tick = %v, want the published ones left alone", rates)
	}
	sampleClientRates(base.Add(2 * time.Second))
	// Writes toward the client are its download.
	if rate := stats.GetClientRates()["alice"]; rate != (ClientRate{Down: 500}) {
		t.Errorf("alice = %+v, want 500 B/s down over 2s: the flush between the samples must not hide it", rate)
	}

	ratesLive.Store(false)
	t.Cleanup(func() { ratesLive.Store(false) })
	send(1000)
	resumeClientRates(base.Add(3 * time.Second))
	if rate := stats.GetClientRates()["alice"]; rate != (ClientRate{Down: 1000}) {
		t.Errorf("alice = %+v after the first subscriber, want 1000 B/s over 1s: it must sample at once", rate)
	}
	send(500)
	resumeClientRates(base.Add(4500 * time.Millisecond))
	sampleClientRates(base.Add(6 * time.Second))
	// Over the 3s since the first subscriber's sample; had the second one
	// sampled too, this window would be 1.5s and hold nothing.
	if rate := stats.GetClientRates()["alice"]; rate != (ClientRate{Down: 166}) {
		t.Errorf("alice = %+v, want 166 B/s over 3s: a second subscriber must not start another window", rate)
	}

	send(1000)
	if err := s.StopCore(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	sampleClientRates(base.Add(8 * time.Second))
	if rates := stats.GetClientRates(); len(rates) != 0 {
		t.Errorf("rates with the core down = %v, want none", rates)
	}
}
