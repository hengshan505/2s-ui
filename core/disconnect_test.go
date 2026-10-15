package core

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/shenaba/2s-ui/core/usersession"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
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

// typedInbound is an inbound with no registry, of a given type.
type typedInbound struct {
	fakeInbound
	typ string
}

func (f *typedInbound) Type() string { return f.typ }

func ssInbound(tag string) *typedInbound {
	return &typedInbound{fakeInbound: fakeInbound{name: tag}, typ: C.TypeShadowsocks}
}

func ssOptions(multiplex bool) *option.ShadowsocksInboundOptions {
	return &option.ShadowsocksInboundOptions{Multiplex: &option.InboundMultiplexOptions{Enabled: multiplex}}
}

// What a kick reports, end to end on a real tracker: a QUIC inbound or a
// Shadowsocks one running with multiplex, and only one the user had a routed
// connection on before anything was closed. Sorted by tag.
func TestDisconnectReportsLiveInbounds(t *testing.T) {
	tr := NewConnTracker()
	for _, tag := range []string{"hy2", "vless", "anytls", "ss-mux", "ss-plain"} {
		track(tr, "alice", "1.1.1.1", 100, 100, t).Inbound = tag
	}
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
	options := map[string]any{
		"ss-mux":   ssOptions(true),
		"ss-plain": ssOptions(false),
		// Multiplex on, but alice is not on it.
		"ss-idle": ssOptions(true),
	}

	inbounds := []adapter.Inbound{vless, ssInbound("ss-mux"), hy2, tuic, anytls, ssInbound("ss-plain"), ssInbound("ss-idle")}
	result := disconnect(tr, inbounds, "alice", func(tag string) any { return options[tag] })

	for _, k := range []*fakeKicker{vless, hy2, tuic, anytls} {
		if !reflect.DeepEqual(k.kicked, []string{"alice"}) {
			t.Errorf("%s kicked %v, want [alice] -- every registry is asked, live or not", k.name, k.kicked)
		}
	}
	want := []UnclosableInbound{
		{Tag: "hy2", Reason: UnclosableQUIC},
		{Tag: "ss-mux", Reason: UnclosableMux},
	}
	if !reflect.DeepEqual(result.UnclosableInbounds, want) {
		t.Errorf("UnclosableInbounds = %+v, want %+v", result.UnclosableInbounds, want)
	}
	if result.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1", result.Sessions)
	}
	if result.Connections != 5 {
		t.Errorf("Connections = %d, want 5 -- alice's, not bob's", result.Connections)
	}
	if n, _ := tr.CloseConnByUser("bob"); n != 1 {
		t.Errorf("bob had %d connections left, want his tuic one left alone", n)
	}
}

func TestQuicInbound(t *testing.T) {
	for typ, want := range map[string]bool{
		C.TypeHysteria:    true,
		C.TypeHysteria2:   true,
		C.TypeTUIC:        true,
		C.TypeAnyTLS:      false,
		C.TypeVLESS:       false,
		C.TypeVMess:       false,
		C.TypeTrojan:      false,
		C.TypeShadowsocks: false,
	} {
		if got := quicInbound(typ); got != want {
			t.Errorf("quicInbound(%s) = %v, want %v", typ, got, want)
		}
	}
}

// The registry hands constructors a pointer; a value or anything else must not
// panic, and only an enabled multiplex counts.
func TestShadowsocksMultiplex(t *testing.T) {
	var nilOptions *option.ShadowsocksInboundOptions
	for name, tc := range map[string]struct {
		options any
		want    bool
	}{
		"on":           {ssOptions(true), true},
		"off":          {ssOptions(false), false},
		"no multiplex": {&option.ShadowsocksInboundOptions{}, false},
		"value":        {*ssOptions(true), true},
		"nil pointer":  {nilOptions, false},
		"no recipe":    {nil, false},
		"other type":   {&option.VLESSInboundOptions{}, false},
	} {
		if got := shadowsocksMultiplex(tc.options); got != tc.want {
			t.Errorf("%s: shadowsocksMultiplex = %v, want %v", name, got, tc.want)
		}
	}
}

// The options a kick asks about are the ones the inbound runs with: recorded on
// create, replaced with it, gone with it.
func TestInboundOptionsFollowTheTable(t *testing.T) {
	var closed []string
	m, _ := fakeManager(t, func(r recipe) (adapter.Inbound, error) { return &fakeInbound{name: r.tag, closed: &closed}, nil },
		adapter.StartStateInitialize)
	first, second := ssOptions(false), ssOptions(true)

	if err := m.Create(t.Context(), nil, nil, "ss", C.TypeShadowsocks, first); err != nil {
		t.Fatal(err)
	}
	if got := m.options("ss"); got != any(first) {
		t.Errorf("options after create = %v, want the created ones", got)
	}
	if err := m.Create(t.Context(), nil, nil, "ss", C.TypeShadowsocks, second); err != nil {
		t.Fatal(err)
	}
	if got := m.options("ss"); got != any(second) {
		t.Errorf("options after replace = %v, want the new ones", got)
	}
	if err := m.Remove("ss"); err != nil {
		t.Fatal(err)
	}
	if got := m.options("ss"); got != nil {
		t.Errorf("options after remove = %v, want none", got)
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
		const want = `{"connections":3,"sessions":0,"unclosableInbounds":[{"tag":"hy2","reason":"quic"}],"unclosable":1}`
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

// Every field of DisconnectResult reaches the wire, with nothing to keep in
// step: a wire struct that copied the fields would drop a new one silently.
func TestDisconnectWireCarriesEveryField(t *testing.T) {
	result := DisconnectResult{Connections: 1, Sessions: 1}
	result.AddUnclosable("hy2", UnclosableQUIC)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatal(err)
	}
	fields := reflect.TypeFor[DisconnectResult]()
	for i := range fields.NumField() {
		name, _, _ := strings.Cut(fields.Field(i).Tag.Get("json"), ",")
		if _, ok := sent[name]; !ok {
			t.Errorf("field %s (%q) is not sent: %s", fields.Field(i).Name, name, raw)
		}
	}
}

// The one order for every report: this panel's own inbounds, then each
// node's, by tag within each.
func TestCompareUnclosable(t *testing.T) {
	list := []UnclosableInbound{
		{Tag: "tuic", Node: "b"},
		{Tag: "ss", Node: ""},
		{Tag: "ss", Node: "a"},
		{Tag: "hy2", Node: "b"},
		{Tag: "hy2", Node: ""},
	}
	slices.SortFunc(list, CompareUnclosable)
	want := []UnclosableInbound{
		{Tag: "hy2", Node: ""},
		{Tag: "ss", Node: ""},
		{Tag: "ss", Node: "a"},
		{Tag: "hy2", Node: "b"},
		{Tag: "tuic", Node: "b"},
	}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("sorted = %+v, want %+v", list, want)
	}
}

func shadowsocksInbound(t *testing.T, tag string, multiplex bool) map[string]any {
	t.Helper()
	return map[string]any{
		"type": "shadowsocks", "tag": tag, "listen": "127.0.0.1", "listen_port": 0,
		"method": "aes-128-gcm", "password": "test",
		"multiplex": map[string]any{"enabled": multiplex},
	}
}

// DisconnectUser on a real box: the options it judges multiplex by are the
// ones each inbound was really built with, whether the box started with it or
// it was added later. The tests above hand disconnect a lookup of their own;
// with the real one miswired, or an inbound added with no recipe, every mux
// warning would vanish and they would all still pass.
func TestDisconnectUserReadsRunningOptions(t *testing.T) {
	config, err := json.Marshal(map[string]any{
		"log":      map[string]any{"disabled": true},
		"inbounds": []any{shadowsocksInbound(t, "ss-boot", true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := NewCore()
	if err := c.Start(config); err != nil {
		t.Fatalf("start core: %v", err)
	}
	t.Cleanup(func() { _ = c.Stop() })
	for tag, multiplex := range map[string]bool{"ss-hot": true, "ss-plain": false} {
		raw, err := json.Marshal(shadowsocksInbound(t, tag, multiplex))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddInbound(raw); err != nil {
			t.Fatalf("add %s: %v", tag, err)
		}
	}

	box, err := c.running()
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"ss-boot", "ss-hot", "ss-plain"} {
		track(box.ConnTracker(), "alice", "1.1.1.1", 100, 100, t).Inbound = tag
	}

	result, err := c.DisconnectUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	want := []UnclosableInbound{
		{Tag: "ss-boot", Reason: UnclosableMux},
		{Tag: "ss-hot", Reason: UnclosableMux},
	}
	if !reflect.DeepEqual(result.UnclosableInbounds, want) {
		t.Errorf("UnclosableInbounds = %+v, want %+v", result.UnclosableInbounds, want)
	}
	if result.Connections != 3 {
		t.Errorf("Connections = %d, want 3", result.Connections)
	}
}
