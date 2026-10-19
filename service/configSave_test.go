package service

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shenaba/2s-ui/core"
	"github.com/shenaba/2s-ui/database"
	"github.com/shenaba/2s-ui/database/model"

	"github.com/sagernet/sing-box/adapter"
)

// What ConfigService.Save does to a running core, against a real database and
// a real core: inbounds, outbounds, endpoints and services are all changed in
// place (core/managers.go), and a change the core refuses fails the save.

func runningPanel(t *testing.T) *ConfigService {
	t.Helper()
	s := maintenanceEnv(t)
	if err := s.SettingService.SetConfig(`{"log":{"disabled":true}}`); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := s.StartCore(); err != nil {
		t.Fatalf("start core: %v", err)
	}
	if !corePtr.IsRunning() {
		t.Fatal("core did not start")
	}
	return s
}

func save(t *testing.T, s *ConfigService, obj, act, data string) error {
	t.Helper()
	_, err := s.Save(obj, act, []byte(data), "", "test", "localhost")
	return err
}

// outboundRows counts the outbound rows; InitDB seeds a default one, so tests
// compare against a count taken first.
func outboundRows(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := database.GetDB().Model(model.Outbound{}).Count(&n).Error; err != nil {
		t.Fatalf("count outbounds: %v", err)
	}
	return n
}

// A saved outbound is in the running core when the save returns, and nothing
// was restarted to get it there: connections on every other outbound and
// inbound carry on.
func TestASavedOutboundIsLiveWithoutARestart(t *testing.T) {
	s := runningPanel(t)
	before := corePtr.GetInstance()
	if err := save(t, s, "outbounds", "new", `{"type":"direct","tag":"out1"}`); err != nil {
		t.Fatalf("save: %v", err)
	}
	if corePtr.GetInstance() != before {
		t.Fatal("the core was restarted for an outbound")
	}
	if _, found := before.Outbound().Outbound("out1"); !found {
		t.Fatal("the saved outbound is not in the running core")
	}
}

// An edit the core cannot start is refused, nothing is written, and the
// outbound it was meant to replace is still there -- rebuilt, since replacing
// had already closed it. A selector naming an outbound that does not exist
// only fails once it starts.
func TestAFailedOutboundEditKeepsTheOldOne(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "outbounds", "new", `{"type":"direct","tag":"member"}`); err != nil {
		t.Fatalf("save member: %v", err)
	}
	if err := save(t, s, "outbounds", "new", `{"type":"selector","tag":"sel","outbounds":["member"]}`); err != nil {
		t.Fatalf("save selector: %v", err)
	}
	var id uint
	if err := database.GetDB().Model(model.Outbound{}).Select("id").Where("tag = ?", "sel").Scan(&id).Error; err != nil {
		t.Fatalf("find selector: %v", err)
	}

	err := save(t, s, "outbounds", "edit", `{"id":`+strconv.FormatUint(uint64(id), 10)+`,"type":"selector","tag":"sel","outbounds":["gone"]}`)
	if err == nil {
		t.Fatal("an edit the core cannot start must be refused")
	}
	sel, found := corePtr.GetInstance().Outbound().Outbound("sel")
	if !found || !strings.Contains(strings.Join(sel.Dependencies(), ","), "member") {
		t.Fatalf("the old selector must still be running, got %v", sel)
	}
	var stored model.Outbound
	if err := database.GetDB().Where("tag = ?", "sel").First(&stored).Error; err != nil {
		t.Fatalf("read selector: %v", err)
	}
	if strings.Contains(string(stored.Options), "gone") {
		t.Error("a refused edit must not be written")
	}
}

// Deleting an outbound a selector still uses is refused by the core, so the
// save fails and the row stays.
func TestDeletingAnOutboundASelectorUsesIsRefused(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "outbounds", "new", `{"type":"direct","tag":"member"}`); err != nil {
		t.Fatalf("save member: %v", err)
	}
	if err := save(t, s, "outbounds", "new", `{"type":"selector","tag":"sel","outbounds":["member"]}`); err != nil {
		t.Fatalf("save selector: %v", err)
	}
	rows := outboundRows(t)

	if err := save(t, s, "outbounds", "del", `"member"`); err == nil || !strings.Contains(err.Error(), "depended by") {
		t.Fatalf("deleting a selector's member = %v, want it refused", err)
	}
	if n := outboundRows(t); n != rows {
		t.Errorf("outbound rows = %d after a refused delete, want %d", n, rows)
	}
	if _, found := corePtr.GetInstance().Outbound().Outbound("member"); !found {
		t.Error("the member must still be in the running core")
	}
}

// With the core down the save only writes the row; the next start reads it.
func TestAnOutboundSavedWhileTheCoreIsDownIsKept(t *testing.T) {
	s := runningPanel(t)
	if err := s.StopCore(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	rows := outboundRows(t)
	if err := save(t, s, "outbounds", "new", `{"type":"direct","tag":"out1"}`); err != nil {
		t.Fatalf("save with the core down: %v", err)
	}
	if n := outboundRows(t); n != rows+1 {
		t.Errorf("outbound rows = %d, want %d: the save kept", n, rows+1)
	}
}

// Tags are unique in the database, but the core is changed before the row is
// written. A new component on a taken tag has to be refused before it reaches
// the core, or it replaces the one that holds the tag there.
func TestANewComponentOnATakenTagLeavesTheOtherAlone(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "inbounds", "new", `{"type":"mixed","tag":"in","listen":"127.0.0.1","listen_port":0}`); err != nil {
		t.Fatalf("save first inbound: %v", err)
	}
	if err := save(t, s, "outbounds", "new", `{"type":"direct","tag":"out"}`); err != nil {
		t.Fatalf("save first outbound: %v", err)
	}
	box := corePtr.GetInstance()
	inbound, _ := box.Inbound().Get("in")
	outbound, _ := box.Outbound().Outbound("out")

	if err := save(t, s, "inbounds", "new", `{"type":"socks","tag":"in","listen":"127.0.0.1","listen_port":0}`); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("new inbound on a taken tag = %v, want it refused", err)
	}
	if err := save(t, s, "outbounds", "new", `{"type":"block","tag":"out"}`); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("new outbound on a taken tag = %v, want it refused", err)
	}
	if now, _ := box.Inbound().Get("in"); now != inbound {
		t.Error("the refused inbound replaced the one holding the tag in the core")
	}
	if now, _ := box.Outbound().Outbound("out"); now != outbound {
		t.Error("the refused outbound replaced the one holding the tag in the core")
	}
}

// Stopping the core used to throw away whatever it had counted since the last
// stats flush -- Box.Close resets the tracker. What was counted has to be
// waiting for the next flush instead.
func TestStoppingTheCoreKeepsItsUnflushedTraffic(t *testing.T) {
	s := runningPanel(t)
	takePending()
	t.Cleanup(func() { takePending() })

	box := corePtr.GetInstance()
	near, far := net.Pipe()
	counted := box.StatsTracker().RoutedConnection(context.Background(), near, adapter.InboundContext{User: "alice"}, nil, box.Outbound().Default())
	go func() { _, _ = io.Copy(io.Discard, far) }()
	if _, err := counted.Write(make([]byte, 1000)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = counted.Close()

	if err := s.StopCore(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	var carried int64
	for _, stat := range takePending() {
		if stat.Resource == "user" && stat.Tag == "alice" {
			carried += stat.Traffic
		}
	}
	if carried != 1000 {
		t.Errorf("traffic left for the next flush = %d, want 1000", carried)
	}
}

// A flush that fails puts its batch back -- in front of anything carried over
// while it ran, not over it.
func TestAFailedFlushKeepsWhatWasCarriedOverMeanwhile(t *testing.T) {
	takePending()
	t.Cleanup(func() { takePending() })
	holdPending([]model.Stats{{Resource: "user", Tag: "carried", Traffic: 1}})
	holdPending([]model.Stats{{Resource: "user", Tag: "failed", Traffic: 2}})

	got := map[string]bool{}
	for _, stat := range takePending() {
		got[stat.Tag] = true
	}
	if !got["carried"] || !got["failed"] {
		t.Errorf("pending = %v, want both the carried and the failed rows", got)
	}
}

// A restart that finds another start under way waits for it rather than give
// up: the start in progress read its config before the save committed, so a
// restart that skipped would leave the core without the change until the next
// one.
func TestARestartWaitsForAStartInProgress(t *testing.T) {
	s := runningPanel(t)
	before := corePtr.GetInstance()

	startMu.Lock()
	done := make(chan error, 1)
	go func() { done <- s.restartQueuedCore() }()
	select {
	case err := <-done:
		startMu.Unlock()
		t.Fatalf("the restart gave up instead of waiting: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	startMu.Unlock()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("restart: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the restart never ran")
	}
	if box := corePtr.GetInstance(); box == nil || box == before {
		t.Error("the core was not restarted")
	}
}

// A shutdown or an in-place update flushes once more through the stats job's
// own run before the core stops: carryOverStats only leaves the traffic for a
// next flush, and those exits have none.
func TestFlushStatsRunsTheRegisteredFlush(t *testing.T) {
	ran := false
	SetStatsFlush(func() { ran = true })
	t.Cleanup(func() { SetStatsFlush(nil) })
	FlushStats()
	if !ran {
		t.Error("FlushStats did not run the flush the stats job registered")
	}
	SetStatsFlush(nil)
	FlushStats() // nothing registered: must not panic
}

func rowId(t *testing.T, table any, tag string) uint {
	t.Helper()
	var id uint
	if err := database.GetDB().Model(table).Select("id").Where("tag = ?", tag).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("find %s: id %d, %v", tag, id, err)
	}
	return id
}

// waitForRestart waits for the restart a save schedules after its commit.
func waitForRestart(t *testing.T, before *core.Box) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if now := corePtr.GetInstance(); now != nil && now != before {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the core was not restarted")
}

// An outbound a group uses cannot be swapped in under the group, so an edit to
// it is committed and applied by a restart rather than refused -- after it was
// parsed, so a malformed one is still refused before anything is written.
func TestEditingAnOutboundAGroupUsesRestartsTheCore(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "outbounds", "new", `{"type":"direct","tag":"member"}`); err != nil {
		t.Fatalf("save member: %v", err)
	}
	if err := save(t, s, "outbounds", "new", `{"type":"selector","tag":"sel","outbounds":["member"]}`); err != nil {
		t.Fatalf("save selector: %v", err)
	}
	id := strconv.FormatUint(uint64(rowId(t, model.Outbound{}, "member")), 10)
	before := corePtr.GetInstance()

	if err := save(t, s, "outbounds", "edit", `{"id":`+id+`,"type":"direct","tag":"member","connect_timeout":"bogus"}`); err == nil {
		t.Fatal("a malformed edit must be refused")
	}

	if err := save(t, s, "outbounds", "edit", `{"id":`+id+`,"type":"direct","tag":"member","connect_timeout":"7s"}`); err != nil {
		t.Fatalf("editing a selector's member = %v, want it saved", err)
	}
	waitForRestart(t, before)
	var stored model.Outbound
	if err := database.GetDB().Where("tag = ?", "member").First(&stored).Error; err != nil {
		t.Fatalf("read member: %v", err)
	}
	if !strings.Contains(string(stored.Options), "7s") {
		t.Error("the edit was not written")
	}
	if _, found := corePtr.GetInstance().Outbound().Outbound("sel"); !found {
		t.Error("the restarted core lost the selector")
	}
}

// A rename starts the new outbound before removing the old one, so a rename the
// core cannot start leaves the old one running.
func TestARenameThatCannotStartKeepsTheOldOutbound(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "outbounds", "new", `{"type":"direct","tag":"member"}`); err != nil {
		t.Fatalf("save member: %v", err)
	}
	if err := save(t, s, "outbounds", "new", `{"type":"selector","tag":"sel","outbounds":["member"]}`); err != nil {
		t.Fatalf("save selector: %v", err)
	}
	id := strconv.FormatUint(uint64(rowId(t, model.Outbound{}, "sel")), 10)

	if err := save(t, s, "outbounds", "edit", `{"id":`+id+`,"type":"selector","tag":"sel2","outbounds":["gone"]}`); err == nil {
		t.Fatal("a rename the core cannot start must be refused")
	}
	box := corePtr.GetInstance()
	if _, found := box.Outbound().Outbound("sel"); !found {
		t.Error("the old outbound must still be running")
	}
	if _, found := box.Outbound().Outbound("sel2"); found {
		t.Error("the refused rename must not be left in the core")
	}
}

// Renaming an outbound a group uses is refused -- the group names the old tag
// -- and the new one, already started, is taken out again.
func TestRenamingAnOutboundAGroupUsesIsRefusedAndUndone(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "outbounds", "new", `{"type":"direct","tag":"member"}`); err != nil {
		t.Fatalf("save member: %v", err)
	}
	if err := save(t, s, "outbounds", "new", `{"type":"selector","tag":"sel","outbounds":["member"]}`); err != nil {
		t.Fatalf("save selector: %v", err)
	}
	id := strconv.FormatUint(uint64(rowId(t, model.Outbound{}, "member")), 10)

	if err := save(t, s, "outbounds", "edit", `{"id":`+id+`,"type":"direct","tag":"member2"}`); err == nil || !strings.Contains(err.Error(), "depended by") {
		t.Fatalf("renaming a selector's member = %v, want it refused", err)
	}
	box := corePtr.GetInstance()
	if _, found := box.Outbound().Outbound("member"); !found {
		t.Error("the member must still be running")
	}
	if _, found := box.Outbound().Outbound("member2"); found {
		t.Error("the new tag must be taken out again")
	}
}

// Reloading an inbound -- a TLS or client edit -- replaces it in place, so one
// that cannot start leaves the old one running instead of none.
func TestAnInboundReloadThatCannotStartKeepsTheOldOne(t *testing.T) {
	s := runningPanel(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer taken.Close()
	port := taken.Addr().(*net.TCPAddr).Port

	if err := save(t, s, "inbounds", "new", `{"type":"mixed","tag":"in","listen":"127.0.0.1","listen_port":0}`); err != nil {
		t.Fatalf("save inbound: %v", err)
	}
	id := rowId(t, model.Inbound{}, "in")
	err = database.GetDB().Model(model.Inbound{}).Where("id = ?", id).
		Update("options", []byte(`{"listen":"127.0.0.1","listen_port":`+strconv.Itoa(port)+`}`)).Error
	if err != nil {
		t.Fatalf("move the inbound onto a taken port: %v", err)
	}

	before, _ := corePtr.GetInstance().Inbound().Get("in")
	if err := s.InboundService.RestartInbounds(database.GetDB(), []uint{id}); err == nil || !strings.Contains(err.Error(), strconv.Itoa(port)) {
		t.Fatalf("a reload onto a taken port = %v, want it to fail on the port", err)
	}
	now, found := corePtr.GetInstance().Inbound().Get("in")
	if !found {
		t.Fatal("the old inbound must still be running")
	}
	if now == before {
		t.Error("the old inbound was never closed: the reload did not reach the core")
	}
}

// What a stop carried over is committed by the next flush even when the core
// does not come back: it used to wait for a running core, and was lost with
// the process if none came.
func TestCarriedTrafficIsFlushedWithTheCoreDown(t *testing.T) {
	s := runningPanel(t)
	takePending()
	t.Cleanup(func() { takePending() })

	box := corePtr.GetInstance()
	near, far := net.Pipe()
	counted := box.StatsTracker().RoutedConnection(context.Background(), near, adapter.InboundContext{User: "alice"}, nil, box.Outbound().Default())
	go func() { _, _ = io.Copy(io.Discard, far) }()
	if _, err := counted.Write(make([]byte, 1000)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = counted.Close()
	if err := s.StopCore(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if err := (&StatsService{}).SaveStats(true, 10); err != nil {
		t.Fatalf("flush: %v", err)
	}
	var stored int64
	err := database.GetDB().Model(model.Stats{}).Select("COALESCE(SUM(traffic), 0)").
		Where("resource = ? AND tag = ?", "user", "alice").Scan(&stored).Error
	if err != nil {
		t.Fatalf("read stats: %v", err)
	}
	if stored != 1000 {
		t.Errorf("traffic committed with the core down = %d, want 1000", stored)
	}
	if left := takePending(); len(left) != 0 {
		t.Errorf("%d rows still pending after the flush", len(left))
	}
}

// A save that lands while a restart is under way used to find the core "not
// running", skip the hot change, and -- committed after the restart had read
// its config -- be missing from the new core. It waits for the restart now,
// and then goes into the running core.
func TestASaveDuringARestartReachesTheNewCore(t *testing.T) {
	s := runningPanel(t)

	// A restart, by hand, up to the point where it has read its config.
	startMu.Lock()
	locked := true
	t.Cleanup(func() {
		if locked {
			startMu.Unlock()
		}
	})
	if err := stopLocked(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	rawConfig, err := s.GetConfig("")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	saved := make(chan error, 1)
	go func() { saved <- save(t, s, "outbounds", "new", `{"type":"direct","tag":"late"}`) }()
	select {
	case err := <-saved:
		t.Fatalf("the save went ahead in the middle of a restart: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	if _, err := startBox(*rawConfig); err != nil {
		t.Fatalf("start: %v", err)
	}
	locked = false
	startMu.Unlock()

	select {
	case err := <-saved:
		if err != nil {
			t.Fatalf("save: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the save never finished")
	}
	if _, found := corePtr.GetInstance().Outbound().Outbound("late"); !found {
		t.Error("the outbound saved during the restart is not in the new core")
	}
}

// Restarts requested while one already waits are folded into it: it reads the
// config only once it runs.
func TestRestartsRequestedTogetherRunOnce(t *testing.T) {
	s := runningPanel(t)
	before := corePtr.GetInstance()
	ran := queuedRestarts.Load()

	startMu.Lock()
	for range 3 {
		s.requestRestart()
	}
	startMu.Unlock()
	waitForRestart(t, before)

	// Give a second restart, had one been queued, the time to run.
	time.Sleep(500 * time.Millisecond)
	startMu.Lock()
	n := queuedRestarts.Load() - ran
	startMu.Unlock()
	if n != 1 {
		t.Errorf("three requests made while one was waiting ran %d restarts, want 1", n)
	}
}

// An edit to a group's member is vetted before it is committed: what the core
// would refuse at start -- a cycle back through the group, a detour that does
// not exist -- would otherwise take the whole core down on the restart.
func TestAnEditToAGroupMemberTheCoreCannotStartIsRefused(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "outbounds", "new", `{"type":"direct","tag":"member"}`); err != nil {
		t.Fatalf("save member: %v", err)
	}
	if err := save(t, s, "outbounds", "new", `{"type":"selector","tag":"sel","outbounds":["member"]}`); err != nil {
		t.Fatalf("save selector: %v", err)
	}
	id := strconv.FormatUint(uint64(rowId(t, model.Outbound{}, "member")), 10)
	before := corePtr.GetInstance()

	for _, c := range []struct{ name, detour, want string }{
		{"a cycle through the group", "sel", "circular"},
		{"a missing detour", "gone", "not found"},
	} {
		err := save(t, s, "outbounds", "edit", `{"id":`+id+`,"type":"socks","tag":"member","server":"127.0.0.1","server_port":1080,"detour":"`+c.detour+`"}`)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: save = %v, want it refused (%q)", c.name, err, c.want)
		}
	}
	var stored model.Outbound
	if err := database.GetDB().Where("tag = ?", "member").First(&stored).Error; err != nil {
		t.Fatalf("read member: %v", err)
	}
	if strings.Contains(string(stored.Options), "detour") {
		t.Error("a refused edit was written")
	}
	time.Sleep(200 * time.Millisecond)
	if corePtr.GetInstance() != before {
		t.Error("a refused edit restarted the core")
	}
}

// Saving an outbound as it was leaves the core alone: no replacement, and for
// a group's member no restart dropping every connection on the panel.
func TestAnUnchangedSaveLeavesTheCoreAlone(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "outbounds", "new", `{"type":"direct","tag":"member","connect_timeout":"5s"}`); err != nil {
		t.Fatalf("save member: %v", err)
	}
	if err := save(t, s, "outbounds", "new", `{"type":"selector","tag":"sel","outbounds":["member"]}`); err != nil {
		t.Fatalf("save selector: %v", err)
	}
	before := corePtr.GetInstance()
	member, _ := before.Outbound().Outbound("member")
	sel, _ := before.Outbound().Outbound("sel")

	memberId := strconv.FormatUint(uint64(rowId(t, model.Outbound{}, "member")), 10)
	selId := strconv.FormatUint(uint64(rowId(t, model.Outbound{}, "sel")), 10)
	if err := save(t, s, "outbounds", "edit", `{"id":`+memberId+`,"type":"direct","tag":"member","connect_timeout":"5s"}`); err != nil {
		t.Fatalf("resave member: %v", err)
	}
	if err := save(t, s, "outbounds", "edit", `{"id":`+selId+`,"type":"selector","tag":"sel","outbounds":["member"]}`); err != nil {
		t.Fatalf("resave selector: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if corePtr.GetInstance() != before {
		t.Fatal("an unchanged save restarted the core")
	}
	if now, _ := before.Outbound().Outbound("member"); now != member {
		t.Error("an unchanged save replaced the member")
	}
	if now, _ := before.Outbound().Outbound("sel"); now != sel {
		t.Error("an unchanged save replaced the selector")
	}
}

// Saving one unchanged is how an operator puts back an outbound the running
// core lost; the unchanged-save shortcut must not skip that.
func TestAnUnchangedSaveRestoresOneTheCoreLost(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "outbounds", "new", `{"type":"direct","tag":"x"}`); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := corePtr.RemoveOutbound("x"); err != nil {
		t.Fatalf("lose it: %v", err)
	}
	id := strconv.FormatUint(uint64(rowId(t, model.Outbound{}, "x")), 10)
	if err := save(t, s, "outbounds", "edit", `{"id":`+id+`,"type":"direct","tag":"x"}`); err != nil {
		t.Fatalf("resave: %v", err)
	}
	if !corePtr.HasOutbound("x") {
		t.Error("saving it unchanged did not put it back in the core")
	}
}

// StartCore -- maintenance off, a save finding the core down -- waits for a
// save holding startMu instead of giving up: trying, as the watchdog does,
// returned success having started nothing.
func TestStartCoreWaitsForASaveInProgress(t *testing.T) {
	s := runningPanel(t)
	if err := s.StopCore(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	startMu.RLock() // a save in progress
	if err := s.TryStartCore(); err != nil || corePtr.IsRunning() {
		startMu.RUnlock()
		t.Fatalf("TryStartCore with a save in progress = %v, running %v: the watchdog should give up", err, corePtr.IsRunning())
	}
	started := make(chan error, 1)
	go func() { started <- s.StartCore() }()
	select {
	case err := <-started:
		startMu.RUnlock()
		t.Fatalf("StartCore returned (%v) while a save held startMu", err)
	case <-time.After(200 * time.Millisecond):
	}
	startMu.RUnlock()

	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("start: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("StartCore never returned")
	}
	if !corePtr.IsRunning() {
		t.Error("StartCore returned without starting the core")
	}
}

// Renaming an inbound to a config that does not start leaves the old one
// running: it is renamed in place, and put back when the new one fails.
func TestARenamedInboundThatCannotStartKeepsTheOldOne(t *testing.T) {
	s := runningPanel(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer taken.Close()
	port := strconv.Itoa(taken.Addr().(*net.TCPAddr).Port)

	if err := save(t, s, "inbounds", "new", `{"type":"mixed","tag":"in","listen":"127.0.0.1","listen_port":0}`); err != nil {
		t.Fatalf("save inbound: %v", err)
	}
	id := strconv.FormatUint(uint64(rowId(t, model.Inbound{}, "in")), 10)
	if err := save(t, s, "inbounds", "edit", `{"id":`+id+`,"type":"mixed","tag":"in2","listen":"127.0.0.1","listen_port":`+port+`}`); err == nil {
		t.Fatal("a rename onto a taken port must fail")
	}
	box := corePtr.GetInstance()
	if _, found := box.Inbound().Get("in"); !found {
		t.Error("the old inbound must still be running")
	}
	if _, found := box.Inbound().Get("in2"); found {
		t.Error("the failed rename was left in the core")
	}
}

// Outbounds and endpoints share one namespace in the core, and so do inbounds
// and endpoints: a tag taken in one is refused in the other. Inbounds and
// outbounds stay apart.
func TestATagIsFreeOnlyIfNoSharingTableHoldsIt(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "outbounds", "new", `{"type":"direct","tag":"out"}`); err != nil {
		t.Fatalf("save outbound: %v", err)
	}
	if err := save(t, s, "inbounds", "new", `{"type":"mixed","tag":"in","listen":"127.0.0.1","listen_port":0}`); err != nil {
		t.Fatalf("save inbound: %v", err)
	}
	// Written straight to the table: refused or not is decided before the
	// core is touched, so the endpoint never needs to start.
	if err := database.GetDB().Create(&model.Endpoint{Type: "wireguard", Tag: "ep", Options: []byte(`{}`)}).Error; err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}

	for _, c := range []struct{ obj, data, want string }{
		{"endpoints", `{"type":"wireguard","tag":"out"}`, "outbound tag already in use"},
		{"endpoints", `{"type":"wireguard","tag":"in"}`, "inbound tag already in use"},
		{"outbounds", `{"type":"direct","tag":"ep"}`, "endpoint tag already in use"},
		{"inbounds", `{"type":"mixed","tag":"ep","listen":"127.0.0.1","listen_port":0}`, "endpoint tag already in use"},
	} {
		if err := save(t, s, c.obj, "new", c.data); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("save %s %s = %v, want %q", c.obj, c.data, err, c.want)
		}
	}
	if err := save(t, s, "inbounds", "new", `{"type":"mixed","tag":"out","listen":"127.0.0.1","listen_port":0}`); err != nil {
		t.Errorf("an inbound named like an outbound = %v, want it allowed", err)
	}
}

// An inbound edit the panel cannot finish -- here an out_json that is not an
// object -- is refused before the core is touched. It used to be found only
// after the core had been renamed, leaving it on a tag the save rolled back.
func TestAnInboundEditRefusedByThePanelLeavesTheCoreAlone(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "inbounds", "new", `{"type":"mixed","tag":"in","listen":"127.0.0.1","listen_port":0}`); err != nil {
		t.Fatalf("save inbound: %v", err)
	}
	box := corePtr.GetInstance()
	before, _ := box.Inbound().Get("in")
	id := strconv.FormatUint(uint64(rowId(t, model.Inbound{}, "in")), 10)

	if err := save(t, s, "inbounds", "edit", `{"id":`+id+`,"type":"mixed","tag":"in2","listen":"127.0.0.1","listen_port":0,"out_json":"x"}`); err == nil {
		t.Fatal("an out_json that is not an object must be refused")
	}
	if now, found := box.Inbound().Get("in"); !found || now != before {
		t.Error("a refused edit changed the inbound in the core")
	}
	if _, found := box.Inbound().Get("in2"); found {
		t.Error("a refused edit renamed the inbound in the core")
	}
}

// Once the panel is done with the core -- app.Stop, the exec of an in-place
// update -- nothing starts it again until it is taken back: not a restart a
// save queued just before, not the watchdog, not the restart button. A failed
// exec takes it back.
func TestARetiredCoreStaysDown(t *testing.T) {
	s := runningPanel(t)
	if err := s.RetireCore(); err != nil {
		t.Fatalf("retire: %v", err)
	}

	_ = s.restartQueuedCore() // what requestRestart runs
	_ = s.TryStartCore()      // the watchdog
	_ = s.StartCore()
	// The button has to say so: a nil here read as a restart that worked.
	if err := s.RestartCore(); err == nil {
		t.Error("restarting a retired core reported success")
	}
	if corePtr.IsRunning() {
		t.Fatal("a retired core was started again")
	}

	startCoreAfterFailedExec()
	if !corePtr.IsRunning() {
		t.Fatal("a failed exec must take the core back")
	}
}

// A SIGHUP restarts the panel as app.Stop then app.Start on the same core --
// Init does not run again -- so Start has to take back what Stop retired, or
// the core stays down for good while the watchdog says every five seconds that
// it will not start it (#251). Under maintenance as well: there the start is
// switching maintenance off, later, after Start has long returned.
func TestARestartedPanelTakesTheCoreBack(t *testing.T) {
	s := runningPanel(t)

	// app.Stop, then app.Start, then the watchdog's next tick.
	if err := s.RetireCore(); err != nil {
		t.Fatalf("retire: %v", err)
	}
	s.ReclaimCore()
	if err := s.TryStartCore(); err != nil {
		t.Fatalf("watchdog start: %v", err)
	}
	if !corePtr.IsRunning() {
		t.Fatal("the watchdog did not start a core the panel took back")
	}
	if err := s.RestartCore(); err != nil || !corePtr.IsRunning() {
		t.Fatalf("restart after the panel took the core back = %v, running %v", err, corePtr.IsRunning())
	}

	if err := s.SetMaintenance(true); err != nil {
		t.Fatalf("maintenance on: %v", err)
	}
	if err := s.RetireCore(); err != nil {
		t.Fatalf("retire under maintenance: %v", err)
	}
	s.ReclaimCore()
	if err := s.SetMaintenance(false); err != nil {
		t.Fatalf("maintenance off: %v", err)
	}
	if !corePtr.IsRunning() {
		t.Fatal("switching maintenance off after a panel restart did not start the core")
	}
}
