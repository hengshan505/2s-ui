package core

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/shenaba/2s-ui/core/usersession"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
)

// fakeKicker is an inbound with a session registry (fakeInbound is in
// managers_test.go), answering a kick with a fixed result.
type fakeKicker struct {
	fakeInbound
	typ    string
	result usersession.Result
	// onKick, when set, runs as the kick does -- to close what a cut carrier
	// would take with it.
	onKick func()
	kicked []string
}

func (f *fakeKicker) Type() string { return f.typ }

func (f *fakeKicker) KickUser(user string) usersession.Result {
	f.kicked = append(f.kicked, user)
	if f.onKick != nil {
		f.onKick()
	}
	return f.result
}

func kicker(tag string, typ string, result usersession.Result) *fakeKicker {
	return &fakeKicker{fakeInbound: fakeInbound{name: tag}, typ: typ, result: result}
}

// What a kick reports, end to end on a real tracker: only a QUIC inbound, and
// only one the user had a routed connection on before anything was closed.
func TestDisconnectReportsLiveQUICInbounds(t *testing.T) {
	tr := NewConnTracker()
	track(tr, "alice", "1.1.1.1", 100, 100, t).Inbound = "hy2"
	track(tr, "alice", "1.1.1.1", 100, 100, t).Inbound = "vless"
	track(tr, "alice", "1.1.1.1", 100, 100, t).Inbound = "anytls"
	track(tr, "bob", "2.2.2.2", 100, 100, t).Inbound = "tuic"

	// Kicked first, and takes alice's hy2 streams with it: a snapshot read
	// after the kicks, or after CloseConnByUser, would no longer show hy2.
	vless := kicker("vless", C.TypeVLESS, usersession.Result{Cut: 1})
	vless.onKick = func() { tr.CloseConnByInbound("hy2") }
	// Connected now: reported.
	hy2 := kicker("hy2", C.TypeHysteria2, usersession.Result{Unclosable: 1})
	// Seen once, nothing routed now -- the false warning of #234.
	tuic := kicker("tuic", C.TypeTUIC, usersession.Result{Unclosable: 1})
	// A stray stream left in anytls's seen set: live, but never a session.
	anytls := kicker("anytls", C.TypeAnyTLS, usersession.Result{Unclosable: 1})
	// Live, with no registry to ask.
	plain := &fakeInbound{name: "direct-in"}

	result, live := disconnect(tr, []adapter.Inbound{vless, hy2, tuic, anytls, plain}, "alice")

	for _, k := range []*fakeKicker{vless, hy2, tuic, anytls} {
		if !reflect.DeepEqual(k.kicked, []string{"alice"}) {
			t.Errorf("%s kicked %v, want [alice] -- every registry is asked, live or not", k.name, k.kicked)
		}
	}
	wantLive := map[string]struct{}{"hy2": {}, "vless": {}, "anytls": {}}
	if !reflect.DeepEqual(live, wantLive) {
		t.Errorf("live = %v, want %v", live, wantLive)
	}
	want := []UnclosableInbound{{Tag: "hy2", Reason: UnclosableQUIC}}
	if !reflect.DeepEqual(result.UnclosableInbounds, want) {
		t.Errorf("UnclosableInbounds = %+v, want %+v", result.UnclosableInbounds, want)
	}
	if result.Unclosable != 1 {
		t.Errorf("Unclosable = %d, want 1 -- it is the list's length", result.Unclosable)
	}
	if result.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1", result.Sessions)
	}
	// vless and anytls; hy2's went with the carrier, bob's are not alice's.
	if result.Connections != 2 {
		t.Errorf("Connections = %d, want 2", result.Connections)
	}
	if got := tr.UserInbounds("bob"); len(got) != 1 {
		t.Errorf("bob's connections = %v, want his tuic one left alone", got)
	}
}

func TestUnclosableReason(t *testing.T) {
	for typ, want := range map[string]string{
		C.TypeHysteria:    UnclosableQUIC,
		C.TypeHysteria2:   UnclosableQUIC,
		C.TypeTUIC:        UnclosableQUIC,
		C.TypeAnyTLS:      "",
		C.TypeVLESS:       "",
		C.TypeVMess:       "",
		C.TypeTrojan:      "",
		C.TypeShadowsocks: "",
	} {
		if got := unclosableReason(typ); got != want {
			t.Errorf("unclosableReason(%s) = %q, want %q", typ, got, want)
		}
	}
}

// The count stays on the wire for masters that predate the list, and the list
// is left out when empty.
func TestDisconnectResultJSON(t *testing.T) {
	result := DisconnectResult{Connections: 3}
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
