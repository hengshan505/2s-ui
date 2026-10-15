package core

import (
	"context"
	"os"
	"runtime/debug"
	"slices"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/taskmonitor"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
)

// scopeSet does adapter.Scope's job one component at a time.
//
// sing-box 1.14.3 moved every component's teardown into the adapter.Scope its
// Start is handed, and dropped Close -- and with it every manager's Remove. A
// box is built, started and closed as a whole, and an adapter.Scope keeps the
// scopes of its children private, so there is no way to close one of them on
// its own. The panel cannot work like that: saving one inbound must not drop
// the connections on every other inbound. So the box, and the inbound manager
// in managers.go, hand each component a scope of its own and keep it here,
// where Remove can close exactly one.
//
// Two more things differ from adapter.Scope, both on purpose:
//
//   - a panic in one cleanup is contained to that cleanup (closeGuarded), where
//     adapter.Scope lets it escape and skip everything registered before it;
//   - a cleanup added after Close runs at once instead of being dropped, so a
//     component racing the teardown cannot leak.
//
// Cleanups still run in reverse registration order, like adapter.Scope.
type scopeSet struct {
	ctx         context.Context
	logger      log.ContextLogger
	access      sync.Mutex
	closed      bool
	entries     []*scopeEntry
	byComponent map[adapter.Lifecycle]*scopeEntry
}

// scopeEntry is either a started component and the scope it was given, or a
// plain cleanup registered with Add (component and scope both nil).
type scopeEntry struct {
	name      string
	component adapter.Lifecycle
	scope     *adapter.Scope
	cleanup   func() error
}

func newScopeSet(ctx context.Context, logger log.ContextLogger) *scopeSet {
	return &scopeSet{
		ctx:         ctx,
		logger:      logger,
		byComponent: make(map[adapter.Lifecycle]*scopeEntry),
	}
}

func (s *scopeSet) Add(name string, cleanup func() error) {
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		_ = s.closeEntry(&scopeEntry{name: name, cleanup: cleanup})
		return
	}
	s.entries = append(s.entries, &scopeEntry{name: name, cleanup: cleanup})
	s.access.Unlock()
}

// Start runs one stage of component on the scope that belongs to it, creating
// that scope on the component's first stage.
func (s *scopeSet) Start(name string, component adapter.Lifecycle, stage adapter.StartStage) error {
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return E.Cause(os.ErrClosed, stage, " ", name)
	}
	entry, loaded := s.byComponent[component]
	if !loaded {
		entry = &scopeEntry{name: name, component: component, scope: adapter.NewScope(s.ctx, s.logger)}
		s.entries = append(s.entries, entry)
		s.byComponent[component] = entry
	}
	s.access.Unlock()
	done := adapter.LogElapsed(s.logger, stage, " ", name)
	monitor := taskmonitor.New(s.logger, C.StartTimeout)
	monitor.Start(stage, " ", name)
	err := component.Start(stage, entry.scope)
	monitor.Finish()
	done()
	// The set, or this one component, may have been closed while Start ran:
	// the core restarting under a save that is hot-adding an inbound. Whatever
	// Start registered after that went onto a scope that had already run its
	// cleanups and will never run them again -- typically the listener's
	// Close, which would hold the port until the process exits and fail every
	// later start on it. adapter.Scope.Close runs cleanups added after an
	// earlier Close, so closing it once more releases them.
	s.access.Lock()
	detached := s.closed || s.byComponent[component] != entry
	s.access.Unlock()
	if detached {
		_ = s.closeEntry(entry)
		return E.Cause(E.Errors(err, os.ErrClosed), stage, " ", name)
	}
	if err != nil {
		return E.Cause(err, stage, " ", name)
	}
	return nil
}

// Remove closes the scope of one component and forgets it. A component that
// never reached Start has no scope, and removing it reports nothing.
func (s *scopeSet) Remove(component adapter.Lifecycle) error {
	s.access.Lock()
	entry, loaded := s.byComponent[component]
	if !loaded {
		s.access.Unlock()
		return nil
	}
	delete(s.byComponent, component)
	s.entries = slices.DeleteFunc(s.entries, func(it *scopeEntry) bool { return it == entry })
	s.access.Unlock()
	return s.closeEntry(entry)
}

// Close runs every cleanup in reverse order. Calling it again does nothing:
// a failed Box.Start closes the box and Core.Start closes it once more.
func (s *scopeSet) Close() error {
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return nil
	}
	s.closed = true
	entries := s.entries
	s.entries = nil
	s.byComponent = nil
	s.access.Unlock()
	var err error
	for _, entry := range slices.Backward(entries) {
		err = E.Errors(err, s.closeEntry(entry))
	}
	return err
}

func (s *scopeSet) closeEntry(entry *scopeEntry) error {
	cleanup := entry.cleanup
	if entry.scope != nil {
		cleanup = entry.scope.Close
	}
	monitor := taskmonitor.New(s.logger, C.StopTimeout)
	monitor.Start("close ", entry.name)
	err := closeGuarded(s.logger, entry.name, cleanup)
	monitor.Finish()
	if err != nil {
		return E.Cause(err, "close ", entry.name)
	}
	return nil
}

// closeGuarded runs one cleanup, turning a panic into an error so the rest of
// the teardown still runs.
//
// Upstream lets a panic here escape, which is fine when the process is
// sing-box: it dies either way. Here the box is an object the panel tears down
// and rebuilds in-process, and Core.Stop has already published "stopped" and
// dropped its pointer to this box by the time Close runs -- so an escaping
// panic leaks every component after the failing one with nothing left holding a
// reference to them, and on the paths that restart the core off the main
// goroutine (cronjob/resetTrafficJob, service/tgbot) there is no recover above
// us, so it takes the whole panel down. The same holds for a single inbound
// removed on a hot reload, which runs on the request goroutine of a save.
func closeGuarded(logger log.ContextLogger, name string, cleanup func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			// The stack is the whole point of surviving this. Recovering keeps
			// the panel up, which also means the same panic comes back on every
			// core restart -- and a bare "invalid memory address" repeated every
			// few seconds names nothing anyone can fix. Logged separately from
			// the returned error so it reaches the log even when the caller
			// only reports err.Error() on one line.
			stack := debug.Stack()
			err = E.New("panic: ", v)
			logger.Error("panic closing ", name, ": ", v, "\n", string(stack))
		}
	}()
	done := adapter.LogElapsed(logger, "close ", name)
	defer done()
	return cleanup()
}
