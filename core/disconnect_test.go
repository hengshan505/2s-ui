package core

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/shenaba/2s-ui/core/usersession"

	"github.com/sagernet/sing-box/adapter"
)

// fakeKicker is an inbound with a session registry (fakeInbound is in
// managers_test.go), answering a kick with a fixed result.
type fakeKicker struct {
	fakeInbound
	result usersession.Result
	kicked []string
}

func (f *fakeKicker) KickUser(user string) usersession.Result {
	f.kicked = append(f.kicked, user)
	return f.result
}

// #234: a QUIC registry reports every user it has ever seen on the inbound, so
// only an inbound the user has a routed connection on may be reported.
func TestKickInboundsReportsOnlyLiveInbounds(t *testing.T) {
	// Connected now: reported.
	hy2 := &fakeKicker{fakeInbound: fakeInbound{name: "hy2"}, result: usersession.Result{Unclosable: 1}}
	// Seen once, nothing routed now: the false warning this guards against.
	tuic := &fakeKicker{fakeInbound: fakeInbound{name: "tuic"}, result: usersession.Result{Unclosable: 1}}
	// A mux carrier cut outright, counted whether or not anything was routed.
	vless := &fakeKicker{fakeInbound: fakeInbound{name: "vless"}, result: usersession.Result{Cut: 2}}
	// Live, but with no registry to ask.
	plain := &fakeInbound{name: "direct-in"}

	var result DisconnectResult
	live := map[string]struct{}{"hy2": {}, "vless": {}, "direct-in": {}}
	kickInbounds(&result, []adapter.Inbound{hy2, tuic, vless, plain}, "alice", live)

	for _, k := range []*fakeKicker{hy2, tuic, vless} {
		if !reflect.DeepEqual(k.kicked, []string{"alice"}) {
			t.Errorf("%s kicked %v, want [alice] -- every registry is asked, live or not", k.name, k.kicked)
		}
	}
	if result.Sessions != 2 {
		t.Errorf("Sessions = %d, want 2", result.Sessions)
	}
	want := []UnclosableInbound{{Tag: "hy2", Reason: UnclosableQUIC}}
	if !reflect.DeepEqual(result.UnclosableInbounds, want) {
		t.Errorf("UnclosableInbounds = %+v, want %+v", result.UnclosableInbounds, want)
	}
	if result.Unclosable != 1 {
		t.Errorf("Unclosable = %d, want 1 -- it is the list's length", result.Unclosable)
	}
}

// The count stays on the wire for masters that predate the list, the list is
// left out when empty, and the live inbounds are never sent.
func TestDisconnectResultJSON(t *testing.T) {
	result := DisconnectResult{Connections: 3, LiveInbounds: []string{"hy2"}}
	result.AddUnclosable("hy2", UnclosableQUIC)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"connections":3,"sessions":0,"unclosable":1,"unclosableInbounds":[{"tag":"hy2","reason":"quic"}]}`
	if string(raw) != want {
		t.Errorf("json = %s\nwant   %s", raw, want)
	}

	raw, err = json.Marshal(DisconnectResult{Connections: 1})
	if err != nil {
		t.Fatal(err)
	}
	const wantEmpty = `{"connections":1,"sessions":0,"unclosable":0}`
	if string(raw) != wantEmpty {
		t.Errorf("json = %s\nwant   %s", raw, wantEmpty)
	}
}
