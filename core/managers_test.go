package core

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing/common/observable"
	"github.com/sagernet/sing/service"
)

// sing-box 1.14.3 dropped Remove from its managers and stopped their Create
// from starting anything, so the hot-reload path now runs entirely through
// managers.go and scopes.go. These tests pin what the service layer relies on.

func mixedInbound(tag string, port int) []byte {
	return []byte(fmt.Sprintf(`{"type":"mixed","tag":%q,"listen":"127.0.0.1","listen_port":%d}`, tag, port))
}

// addMixedInbound hot-adds a mixed inbound on a fresh port and returns it. A
// probed port is free only until the probe closes, and anything running in
// parallel can take it before the core binds it, so a bind failure gets a few
// fresh ports rather than failing the test.
func addMixedInbound(t *testing.T, c *Core, tag string) int {
	t.Helper()
	var lastErr error
	for range 5 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("probe port: %v", err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		if lastErr = c.AddInbound(mixedInbound(tag, port)); lastErr == nil {
			return port
		}
	}
	t.Fatalf("AddInbound %s: %v", tag, lastErr)
	return 0
}

func listening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// released reports whether the core let go of port, by binding it: a dial
// could be answered by whoever took the port next, a bind cannot succeed while
// the core still holds it.
func released(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// An inbound added to a running box has to be listening when AddInbound
// returns -- 1.14.3's Create alone would only have put it in a list -- and
// RemoveInbound has to free the port, which is what lets an edit re-add it on
// the same one.
func TestHotAddedInboundListensAndRemoveFreesThePort(t *testing.T) {
	c := startedCore(t)
	port := addMixedInbound(t, c, "in")
	if !listening(port) {
		t.Fatal("a hot-added inbound must be started, not just registered")
	}
	if err := c.RemoveInbound("in"); err != nil {
		t.Fatalf("RemoveInbound: %v", err)
	}
	if !released(port) {
		t.Fatal("RemoveInbound must close the listener")
	}
	if err := c.AddInbound(mixedInbound("in", port)); err != nil {
		t.Fatalf("re-adding on the freed port: %v", err)
	}
	if !listening(port) {
		t.Fatal("the re-added inbound is not listening")
	}
}

// The callers in service/ treat os.ErrInvalid as "already gone" and anything
// else as a failed save, so an unknown tag must come back as exactly that.
func TestRemovingAnUnknownTagReportsErrInvalid(t *testing.T) {
	c := startedCore(t)
	for name, remove := range map[string]func(string) error{
		"Inbound":  c.RemoveInbound,
		"Outbound": c.RemoveOutbound,
		"Endpoint": c.RemoveEndpoint,
		"Service":  c.RemoveService,
	} {
		if err := remove("nope"); !errors.Is(err, os.ErrInvalid) {
			t.Errorf("Remove%s(unknown) = %v, want os.ErrInvalid", name, err)
		}
	}
}

// Creating over a tag that is already taken replaces it -- and closes the old
// one first, which is what makes a replacement on the same port possible.
func TestCreatingOverATagReplacesAndClosesTheOldOne(t *testing.T) {
	c := startedCore(t)
	first := addMixedInbound(t, c, "in")
	second := addMixedInbound(t, c, "in")
	if !released(first) {
		t.Error("the replaced inbound still holds its port")
	}
	if !listening(second) {
		t.Error("the replacement is not listening")
	}
	count := 0
	for _, inbound := range c.GetInstance().Inbound().Inbounds() {
		if inbound.Tag() == "in" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected one inbound tagged in, got %d", count)
	}
}

// A replacement that fails to start must not take the working inbound down
// with it: the old one has already been closed by then (it has to be, or a
// replacement on the same port could never bind), so it is rebuilt.
func TestAFailedReplacementRestoresTheOldOne(t *testing.T) {
	c := startedCore(t)
	port := addMixedInbound(t, c, "in")

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer busy.Close()
	taken := busy.Addr().(*net.TCPAddr).Port

	if err := c.AddInbound(mixedInbound("in", taken)); err == nil {
		t.Fatal("a replacement on a port that is in use must fail")
	}
	if _, found := c.GetInstance().Inbound().Get("in"); !found {
		t.Fatal("the old inbound must be back after a failed replacement")
	}
	if !listening(port) {
		t.Fatal("the restored inbound is not listening on its own port")
	}
}

// The reference manager only recomputes on url-test history and clash-mode
// events, so every hot change has to raise one or it never hears about it.
func TestHotChangesNotifyTheReferenceManager(t *testing.T) {
	c := startedCore(t)
	history := service.PtrFromContext[urltest.HistoryStorage](c.GetInstance().ctx)
	if history == nil {
		t.Fatal("the box must carry a url-test history")
	}
	subscriber := observable.NewSubscriber[struct{}](8)
	defer subscriber.Close()
	history.AddUpdateHook(subscriber)
	updates, _ := subscriber.Subscription()
	// Each step starts from an empty channel, so an event one step raised
	// twice -- addMixedInbound retrying, say -- cannot stand in for the next.
	drain := func() {
		for {
			select {
			case <-updates:
			default:
				return
			}
		}
	}
	expect := func(what string) {
		t.Helper()
		select {
		case <-updates:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not notify the reference manager", what)
		}
	}

	drain()
	addMixedInbound(t, c, "in")
	expect("AddInbound")
	drain()
	if err := c.RemoveInbound("in"); err != nil {
		t.Fatalf("RemoveInbound: %v", err)
	}
	expect("RemoveInbound")
}

// fakeInbound records what its scope did to it and can be told to fail one
// stage, to panic when closed, or to hold its Start until released.
type fakeInbound struct {
	name    string
	failAt  adapter.StartStage
	failing bool
	panics  bool
	stages  []adapter.StartStage
	closed  *[]string
	// entered and release, when set, make Start signal and then wait before
	// it registers its cleanup -- the window a concurrent teardown falls in.
	entered chan struct{}
	release chan struct{}
}

func (f *fakeInbound) Type() string { return "fake" }
func (f *fakeInbound) Tag() string  { return f.name }

func (f *fakeInbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	f.stages = append(f.stages, stage)
	if f.failing && stage == f.failAt {
		return errors.New("refused")
	}
	if stage == adapter.StartStateInitialize {
		if f.entered != nil {
			close(f.entered)
			<-f.release
		}
		scope.Add(func() error {
			if f.panics {
				panic("close " + f.name)
			}
			*f.closed = append(*f.closed, f.name)
			return nil
		})
	}
	return nil
}

func startedScopes(t *testing.T) (*scopeSet, *recordingLogger) {
	t.Helper()
	logger := &recordingLogger{}
	return newScopeSet(t.Context(), logger), logger
}

// fakeManager is an inboundManager whose inbounds come from build instead of a
// registry, started to stage on parent.
func fakeManager(t *testing.T, build func(recipe) (adapter.Inbound, error), stages ...adapter.StartStage) (*inboundManager, *adapter.Scope) {
	t.Helper()
	logger := &recordingLogger{}
	m := &inboundManager{componentTable: newComponentTable("inbound", logger, build)}
	parent := adapter.NewScope(t.Context(), logger)
	for _, stage := range stages {
		if err := m.Start(stage, parent); err != nil {
			t.Fatalf("start %v: %v", stage, err)
		}
	}
	return m, parent
}

// One component can be closed alone, the rest close in reverse order with the
// box, and a panic in one cleanup does not skip the others.
func TestScopeSetClosesOneAloneAndContainsAPanic(t *testing.T) {
	scopes, logger := startedScopes(t)
	var closed []string
	a := &fakeInbound{name: "a", closed: &closed}
	b := &fakeInbound{name: "b", closed: &closed, panics: true}
	c := &fakeInbound{name: "c", closed: &closed}
	d := &fakeInbound{name: "d", closed: &closed}
	for _, it := range []*fakeInbound{a, b, c, d} {
		if err := scopes.Start(it.name, it, adapter.StartStateInitialize); err != nil {
			t.Fatalf("start %s: %v", it.name, err)
		}
	}

	if err := scopes.Remove(c); err != nil {
		t.Fatalf("Remove(c): %v", err)
	}
	if strings.Join(closed, ",") != "c" {
		t.Fatalf("Remove must close exactly that component, closed %v", closed)
	}

	err := scopes.Close()
	if err == nil || !strings.Contains(err.Error(), "close b") {
		t.Errorf("the panic must come back as an error, got %v", err)
	}
	if strings.Join(closed, ",") != "c,d,a" {
		t.Errorf("close order = %v, want c then d,a (reverse, b panicking in between)", closed)
	}
	if len(logger.errors) != 1 {
		t.Errorf("the panic must be logged once, got %v", logger.errors)
	}

	if err := scopes.Close(); err != nil {
		t.Errorf("a second Close must be a no-op, got %v", err)
	}
	if err := scopes.Start("late", &fakeInbound{name: "late", closed: &closed}, adapter.StartStateInitialize); err == nil {
		t.Error("starting on a closed set must be refused")
	}
	ran := false
	scopes.Add("late", func() error { ran = true; return nil })
	if !ran {
		t.Error("a cleanup added after Close must run at once, not leak")
	}
}

// The core can be torn down while a save is still starting an inbound. What
// that Start registers afterwards lands on a scope that has already run its
// cleanups; it must still run, or the inbound's listener holds its port until
// the process exits and every later core start fails on it.
func TestTeardownDuringStartStillRunsLateCleanups(t *testing.T) {
	scopes, _ := startedScopes(t)
	var closed []string
	slow := &fakeInbound{name: "slow", closed: &closed, entered: make(chan struct{}), release: make(chan struct{})}
	started := make(chan error, 1)
	go func() { started <- scopes.Start("slow", slow, adapter.StartStateInitialize) }()

	<-slow.entered
	if err := scopes.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	close(slow.release)
	if err := <-started; err == nil {
		t.Error("a Start overtaken by teardown must report it")
	}
	if strings.Join(closed, ",") != "slow" {
		t.Errorf("the cleanup registered after teardown must run, closed %v", closed)
	}
}

// A Remove that arrives while a Create for the same tag is still starting has
// to wait for it and then remove it. Interleaved, it found nothing, reported
// os.ErrInvalid -- which the callers read as "already gone" and delete the row
// -- and the Create then registered a running inbound nobody owned.
func TestRemoveWaitsForACreateStillStarting(t *testing.T) {
	var closed []string
	slow := &fakeInbound{name: "x", closed: &closed, entered: make(chan struct{}), release: make(chan struct{})}
	m, _ := fakeManager(t, func(recipe) (adapter.Inbound, error) { return slow, nil }, adapter.StartStateInitialize)

	created := make(chan error, 1)
	go func() { created <- m.Create(t.Context(), nil, nil, "x", "fake", nil) }()
	<-slow.entered

	removed := make(chan error, 1)
	go func() { removed <- m.Remove("x") }()
	select {
	case err := <-removed:
		t.Fatalf("Remove must wait for the Create, returned %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(slow.release)
	if err := <-created; err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := <-removed; err != nil {
		t.Fatalf("Remove after the Create finished = %v, want it removed", err)
	}
	if _, found := m.byTag["x"]; found {
		t.Error("the inbound must be gone")
	}
	if strings.Join(closed, ",") != "x" {
		t.Errorf("the removed inbound must be closed, closed %v", closed)
	}
}

// An inbound added late is brought through exactly the stages the box has
// passed, and one that fails part-way is closed again rather than left half
// started. The manager's own scope still closes whatever is left, which is how
// the box tears every inbound down as one.
func TestCatchUpRunsPassedStagesAndUndoesAFailure(t *testing.T) {
	var closed []string
	ok := &fakeInbound{name: "ok", closed: &closed}
	bad := &fakeInbound{name: "bad", closed: &closed, failing: true, failAt: adapter.StartStateStart}
	next := map[string]adapter.Inbound{"ok": ok, "bad": bad}
	m, parent := fakeManager(t, func(r recipe) (adapter.Inbound, error) { return next[r.tag], nil },
		adapter.StartStateInitialize, adapter.StartStateStart, adapter.StartStatePostStart)

	if err := m.Create(t.Context(), nil, nil, "ok", "fake", nil); err != nil {
		t.Fatalf("create ok: %v", err)
	}
	if fmt.Sprint(ok.stages) != fmt.Sprint([]adapter.StartStage{adapter.StartStateInitialize, adapter.StartStateStart, adapter.StartStatePostStart}) {
		t.Errorf("stages run = %v, want up to post-start and no further", ok.stages)
	}

	if err := m.Create(t.Context(), nil, nil, "bad", "fake", nil); err == nil {
		t.Fatal("a failing stage must fail the create")
	}
	if strings.Join(closed, ",") != "bad" {
		t.Errorf("a failed create must close what it started, closed %v", closed)
	}
	if _, found := m.byTag["bad"]; found {
		t.Error("a failed create must not be registered")
	}

	if err := parent.Close(); err != nil {
		t.Fatalf("parent close: %v", err)
	}
	if strings.Join(closed, ",") != "bad,ok" {
		t.Errorf("closing the parent must close the rest, closed %v", closed)
	}
}

// A box that failed to start is closed by Box.Start and then once more by
// Core.Start; the second call must not do anything.
func TestBoxCloseIsIdempotent(t *testing.T) {
	c := startedCore(t)
	box := c.GetInstance()
	if err := c.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := box.Close(); err != nil {
		t.Errorf("closing a closed box must be a no-op, got %v", err)
	}
}

// withDeadline fails the test instead of hanging it when fn deadlocks.
func withDeadline(t *testing.T, what string, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not return: deadlocked?", what)
		return nil
	}
}

func addOutbound(t *testing.T, c *Core, config string) {
	t.Helper()
	err := withDeadline(t, "AddOutbound", func() error { return c.AddOutbound([]byte(config)) })
	if err != nil {
		t.Fatalf("AddOutbound %s: %v", config, err)
	}
}

// A selector resolves its members through the outbound manager from inside its
// own Start, so a hot-added one must be started outside the manager's lookup
// lock. Neither removing nor replacing an outbound something depends on is
// allowed: the selector resolved it once and would go on dialing through the
// closed instance.
func TestASelectorsMemberCanBeNeitherRemovedNorReplaced(t *testing.T) {
	c := startedCore(t)
	addOutbound(t, c, `{"type":"direct","tag":"member"}`)
	addOutbound(t, c, `{"type":"selector","tag":"sel","outbounds":["member"]}`)
	member, _ := c.GetInstance().Outbound().Outbound("member")

	if err := c.RemoveOutbound("member"); err == nil || !strings.Contains(err.Error(), "depended by sel") {
		t.Fatalf("removing a selector's member must be refused, got %v", err)
	}
	if err := c.AddOutbound([]byte(`{"type":"direct","tag":"member"}`)); err == nil || !strings.Contains(err.Error(), "depended by sel") {
		t.Fatalf("replacing a selector's member must be refused, got %v", err)
	}
	if now, found := c.GetInstance().Outbound().Outbound("member"); !found || now != member {
		t.Fatal("a refused removal or replacement must leave the outbound as it was")
	}
	if err := c.RemoveOutbound("sel"); err != nil {
		t.Fatalf("RemoveOutbound sel: %v", err)
	}
	if err := c.RemoveOutbound("member"); err != nil {
		t.Fatalf("once nothing depends on it, member must go: %v", err)
	}
}

// With no route.final the default is the first outbound, as on a restart. An
// edit -- the same tag, new options -- replaces it in place, so it stays the
// default and keeps its place; removing it hands the role on rather than leave
// it empty, which the router would trip over on the next connection.
func TestAnEditedDefaultStaysTheDefault(t *testing.T) {
	c := startedCore(t)
	box := c.GetInstance()
	addOutbound(t, c, `{"type":"direct","tag":"other"}`)
	first := box.Outbound().Default()
	if first == nil || first.Tag() == "other" {
		t.Fatalf("unexpected starting default %v", first)
	}
	tag := first.Tag()

	addOutbound(t, c, `{"type":"direct","tag":"`+tag+`"}`)
	edited := box.Outbound().Default()
	if edited == nil || edited.Tag() != tag || edited == first {
		t.Fatalf("default after editing it = %v, want the new %s", edited, tag)
	}
	if outbounds := box.Outbound().Outbounds(); outbounds[0] != edited {
		t.Errorf("the edited outbound must keep its place, first is now %s", outbounds[0].Tag())
	}

	if err := c.RemoveOutbound(tag); err != nil {
		t.Fatalf("RemoveOutbound(default): %v", err)
	}
	if got := box.Outbound().Default(); got == nil || got.Tag() != "other" {
		t.Fatalf("default after removal = %v, want other", got)
	}
}

// A replacement that fails to start must not take the working outbound with
// it: the old one is rebuilt from what it was created with.
func TestAFailedOutboundReplacementRestoresTheOldOne(t *testing.T) {
	c := startedCore(t)
	addOutbound(t, c, `{"type":"direct","tag":"member"}`)
	addOutbound(t, c, `{"type":"selector","tag":"sel","outbounds":["member"]}`)

	// Fails at start: the member does not exist.
	err := withDeadline(t, "AddOutbound", func() error {
		return c.AddOutbound([]byte(`{"type":"selector","tag":"sel","outbounds":["gone"]}`))
	})
	if err == nil {
		t.Fatal("a selector naming a missing member must fail to start")
	}
	sel, found := c.GetInstance().Outbound().Outbound("sel")
	if !found || fmt.Sprint(sel.Dependencies()) != "[member]" {
		t.Fatalf("the old selector must be back, got %v", sel)
	}
}

// route.final names the default by tag. Replacing that outbound -- an edit --
// must move the default to the new instance; removing it is refused, since the
// core would then route everything unmatched elsewhere and fail to start on the
// next restart.
func TestRouteFinalFollowsAnEditAndCannotBeRemoved(t *testing.T) {
	c := NewCore()
	if err := c.Start([]byte(`{"log":{"disabled":true},"outbounds":[{"type":"direct","tag":"final"},{"type":"direct","tag":"other"}],"route":{"final":"final"}}`)); err != nil {
		t.Fatalf("start core: %v", err)
	}
	t.Cleanup(func() { _ = c.Stop() })
	box := c.GetInstance()
	before := box.Outbound().Default()

	addOutbound(t, c, `{"type":"direct","tag":"final"}`)
	if got := box.Outbound().Default(); got == nil || got == before || got.Tag() != "final" {
		t.Fatalf("default after editing route.final = %v, want the new instance", got)
	}
	if err := c.RemoveOutbound("final"); err == nil || !strings.Contains(err.Error(), "route.final") {
		t.Fatalf("removing route.final must be refused, got %v", err)
	}
	if err := c.RemoveOutbound("other"); err != nil {
		t.Fatalf("removing another outbound: %v", err)
	}
}

// With no route.final the default is the first outbound, and the router calls
// it on every unmatched connection without a nil check. Removing the last one
// used to leave no default at all, so the next such connection panicked; it is
// replaced by the same direct outbound a box built with none gets.
func TestRemovingTheLastOutboundLeavesADirectDefault(t *testing.T) {
	c := NewCore()
	if err := c.Start([]byte(`{"log":{"disabled":true},"outbounds":[{"type":"direct","tag":"only"}]}`)); err != nil {
		t.Fatalf("start core: %v", err)
	}
	t.Cleanup(func() { _ = c.Stop() })
	box := c.GetInstance()

	if err := c.RemoveOutbound("only"); err != nil {
		t.Fatalf("remove the last outbound: %v", err)
	}
	if got := box.Outbound().Default(); got == nil || got.Tag() != "direct" {
		t.Fatalf("default after removing the last outbound = %v, want the direct fallback", got)
	}
	if got := box.Outbound().Outbounds(); len(got) != 1 {
		t.Fatalf("outbounds = %d, want only the fallback", len(got))
	}
	if _, found := box.Outbound().Outbound("only"); found {
		t.Error("the removed outbound is still listed")
	}

	// An outbound the user then names "direct" replaces the fallback.
	addOutbound(t, c, `{"type":"direct","tag":"direct"}`)
	if got := box.Outbound().Default(); got == nil || got.Tag() != "direct" {
		t.Fatalf("default after replacing the fallback = %v", got)
	}
	addOutbound(t, c, `{"type":"direct","tag":"second"}`)
	if err := c.RemoveOutbound("direct"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := box.Outbound().Default(); got == nil || got.Tag() != "second" {
		t.Fatalf("default with another outbound left = %v, want second and no fallback", got)
	}
}

// A Create the table refuses -- a duplicate tag before the box starts, or a
// replacement refuse vetoes -- must not build anything first: a component can
// hold resources from its constructor on that only its Start registers for
// cleanup (a wireguard endpoint's gVisor stack), so a refused one leaked them.
func TestARefusedCreateBuildsNothing(t *testing.T) {
	var closed []string
	builds := 0
	build := func(r recipe) (adapter.Inbound, error) {
		builds++
		return &fakeInbound{name: r.tag, closed: &closed}, nil
	}

	unstarted, _ := fakeManager(t, build)
	if err := unstarted.Create(t.Context(), nil, nil, "x", "fake", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := unstarted.Create(t.Context(), nil, nil, "x", "fake", nil); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("a duplicate tag before start = %v, want it refused", err)
	}
	if builds != 1 {
		t.Errorf("a refused duplicate was built: %d builds, want 1", builds)
	}

	builds = 0
	started, _ := fakeManager(t, build, adapter.StartStateInitialize)
	if err := started.Create(t.Context(), nil, nil, "x", "fake", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	started.refuse = func(string, bool) error { return errors.New("refused") }
	if err := started.Create(t.Context(), nil, nil, "x", "fake", nil); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a vetoed replacement = %v, want it refused", err)
	}
	if builds != 1 {
		t.Errorf("a vetoed replacement was built: %d builds, want 1", builds)
	}
}

// Vetting an edit applied by a restart must not build the outbound, for the
// same reason; it reads the dependencies off the config instead.
func TestValidateOutboundBuildsNothing(t *testing.T) {
	c := startedCore(t)
	addOutbound(t, c, `{"type":"direct","tag":"member"}`)
	box := c.GetInstance()
	build := box.outbound.build
	builds := 0
	box.outbound.build = func(r recipe) (adapter.Outbound, error) {
		builds++
		return build(r)
	}

	if err := c.ValidateOutbound([]byte(`{"type":"selector","tag":"sel","outbounds":["member"]}`)); err != nil {
		t.Fatalf("validate a sound selector: %v", err)
	}
	if err := c.ValidateOutbound([]byte(`{"type":"selector","tag":"sel","outbounds":["gone"]}`)); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("validate a selector with a missing member = %v, want it refused", err)
	}
	if err := c.ValidateOutbound([]byte(`{"type":"socks","tag":"member","server":"127.0.0.1","server_port":1080,"detour":"member"}`)); err == nil || !strings.Contains(err.Error(), "circular") {
		t.Errorf("validate an outbound that is its own detour = %v, want it refused", err)
	}
	if builds != 0 {
		t.Errorf("ValidateOutbound built %d outbounds, want none", builds)
	}
}

// A rename is a replacement under another tag: it takes the old one's slot, a
// tag the core already holds is refused, and one that does not start leaves
// the old one running. Remove then Create, which the callers did, left none.
func TestARenameRestoresTheOldOneOnFailure(t *testing.T) {
	c := startedCore(t)
	for _, tag := range []string{"a", "b"} {
		if err := c.AddInbound(mixedInbound(tag, 0)); err != nil {
			t.Fatalf("add %s: %v", tag, err)
		}
	}
	box := c.GetInstance()
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer taken.Close()

	if err := c.RenameInbound("a", mixedInbound("a2", taken.Addr().(*net.TCPAddr).Port)); err == nil {
		t.Fatal("a rename onto a taken port must fail")
	}
	if _, found := box.Inbound().Get("a"); !found {
		t.Fatal("the old inbound must be running again after a failed rename")
	}
	if _, found := box.Inbound().Get("a2"); found {
		t.Error("the failed rename was left in the core")
	}

	if err := c.RenameInbound("a", mixedInbound("b", 0)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("a rename onto a tag the core holds = %v, want it refused", err)
	}

	if err := c.RenameInbound("a", mixedInbound("a2", 0)); err != nil {
		t.Fatalf("rename: %v", err)
	}
	var tags []string
	for _, inbound := range box.Inbound().Inbounds() {
		tags = append(tags, inbound.Tag())
	}
	if got := strings.Join(tags, ","); !strings.Contains(got, "a2,b") || strings.Contains(got, "a,") {
		t.Errorf("inbounds after the rename = %s, want a2 in a's place before b", got)
	}
}
