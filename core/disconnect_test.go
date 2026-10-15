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
	// onKick, when set, runs as the kick does.
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
	track(tr, "alice", "1.1.1.1", 100, 100, t).Inbound = "ss"
	track(tr, "bob", "2.2.2.2", 100, 100, t).Inbound = "tuic"

	vless := kicker("vless", C.TypeVLESS, usersession.Result{Cut: 1})
	// By the time any registry is asked, the routed connections are closed and
	// their inbounds recorded: nothing a kick does can hide where alice was.
	vless.onKick = func() {
		if n, _ := tr.CloseConnByUser("alice"); n != 0 {
			t.Errorf("%d of alice's connections still open when the kicks ran", n)
		}
	}
	// Connected now: reported.
	hy2 := kicker("hy2", C.TypeHysteria2, usersession.Result{Unclosable: 1})
	// Seen once, nothing routed now -- the false warning of #234.
	tuic := kicker("tuic", C.TypeTUIC, usersession.Result{Unclosable: 1})
	// A stray stream left in anytls's seen set: live, but never a session.
	anytls := kicker("anytls", C.TypeAnyTLS, usersession.Result{Unclosable: 1})
	// Live, with no registry to ask; handed back for the service to judge.
	ss := &typedInbound{fakeInbound: fakeInbound{name: "ss"}, typ: C.TypeShadowsocks}

	result, live := disconnect(tr, []adapter.Inbound{vless, hy2, tuic, anytls, ss}, "alice")

	for _, k := range []*fakeKicker{vless, hy2, tuic, anytls} {
		if !reflect.DeepEqual(k.kicked, []string{"alice"}) {
			t.Errorf("%s kicked %v, want [alice] -- every registry is asked, live or not", k.name, k.kicked)
		}
	}
	wantLive := map[string]string{
		"hy2":    C.TypeHysteria2,
		"vless":  C.TypeVLESS,
		"anytls": C.TypeAnyTLS,
		"ss":     C.TypeShadowsocks,
	}
	if !reflect.DeepEqual(live, wantLive) {
		t.Errorf("live = %v, want %v", live, wantLive)
	}
	want := []UnclosableInbound{{Tag: "hy2", Reason: UnclosableQUIC}}
	if !reflect.DeepEqual(result.UnclosableInbounds, want) {
		t.Errorf("UnclosableInbounds = %+v, want %+v", result.UnclosableInbounds, want)
	}
	if result.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1", result.Sessions)
	}
	if result.Connections != 4 {
		t.Errorf("Connections = %d, want 4 -- alice's, not bob's", result.Connections)
	}
	if n, _ := tr.CloseConnByUser("bob"); n != 1 {
		t.Errorf("bob had %d connections left, want his tuic one left alone", n)
	}
}

// typedInbound is an inbound with no registry, of a given type.
type typedInbound struct {
	fakeInbound
	typ string
}

func (f *typedInbound) Type() string { return f.typ }

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

// The count stays on the wire for masters that predate the list, derived from
// the list; the list is left out when empty. Reading back an answer from such
// a master's era -- a count and no list -- keeps nothing of the count.
func TestDisconnectResultJSON(t *testing.T) {
	result := DisconnectResult{Connections: 3}
	result.AddUnclosable("hy2", UnclosableQUIC)
	for name, v := range map[string]any{"value": result, "pointer": &result} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		const want = `{"connections":3,"sessions":0,"unclosable":1,"unclosableInbounds":[{"tag":"hy2","reason":"quic"}]}`
		if string(raw) != want {
			t.Errorf("%s: json = %s\nwant   %s", name, raw, want)
		}
	}

	raw, err := json.Marshal(DisconnectResult{Connections: 1})
	if err != nil {
		t.Fatal(err)
	}
	const wantEmpty = `{"connections":1,"sessions":0,"unclosable":0}`
	if string(raw) != wantEmpty {
		t.Errorf("json = %s\nwant   %s", raw, wantEmpty)
	}

	var old DisconnectResult
	if err := json.Unmarshal([]byte(`{"connections":2,"sessions":1,"unclosable":3}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Connections != 2 || old.Sessions != 1 || len(old.UnclosableInbounds) != 0 || old.Wire().Unclosable != 0 {
		t.Errorf("decoded an old node's answer as %+v, want its count dropped", old)
	}
}
