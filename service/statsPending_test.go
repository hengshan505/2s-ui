package service

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/shenaba/2s-ui/database"
	"github.com/shenaba/2s-ui/database/model"
)

func resetPending() {
	pendingMu.Lock()
	pendingStats = nil
	pendingMu.Unlock()
}

func pendingLen() int {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	return len(pendingStats)
}

// GetStats is destructive, so a flush whose transaction fails has already taken
// the bytes out of the core and nothing else remembers them. The rows owed to
// the database are carried to the next cycle instead.
func TestPendingStatsCarryAndClear(t *testing.T) {
	resetPending()
	t.Cleanup(resetPending)

	if got := takePending(); got != nil {
		t.Errorf("a fresh buffer should hand back nothing, got %d rows", len(got))
	}

	batch := []model.Stats{
		{Resource: "user", Tag: "alice", Direction: true, Traffic: 100},
		{Resource: "user", Tag: "alice", Direction: false, Traffic: 200},
	}
	holdPending(batch)
	if pendingLen() != 2 {
		t.Fatalf("held %d rows, want 2", pendingLen())
	}

	got := takePending()
	if len(got) != 2 {
		t.Errorf("took %d rows, want 2", len(got))
	}
	if pendingLen() != 0 {
		t.Error("taking the buffer must clear it, or the next cycle writes the rows twice")
	}
}

// A panel whose database has been refusing writes for hours should lose the
// oldest accounting rather than grow until it is killed for it.
func TestPendingStatsAreBounded(t *testing.T) {
	resetPending()
	t.Cleanup(resetPending)

	over := make([]model.Stats, maxPendingStats+1000)
	for i := range over {
		over[i] = model.Stats{Resource: "user", Tag: "t", Traffic: int64(i)}
	}
	holdPending(over)

	if got := pendingLen(); got != maxPendingStats {
		t.Fatalf("held %d rows, want the cap of %d", got, maxPendingStats)
	}
	// The oldest go, not the newest: the newest are the ones the counters and
	// the table are furthest behind on.
	held := takePending()
	if held[0].Traffic != 1000 {
		t.Errorf("first retained row has traffic %d, want the oldest 1000 dropped", held[0].Traffic)
	}
	if held[len(held)-1].Traffic != int64(maxPendingStats+1000-1) {
		t.Errorf("last retained row has traffic %d, want the newest kept", held[len(held)-1].Traffic)
	}
}

// The retention purge filters on date_time alone, which the composite bucket
// index cannot serve from its third column -- so it needs one of its own.
func TestStatsHasADateTimeIndex(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "stats.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if err := database.CloseDBForTest(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})

	var names []string
	if err := database.GetDB().Raw(
		"SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'stats'").
		Scan(&names).Error; err != nil {
		t.Fatalf("read indexes: %v", err)
	}
	for _, n := range names {
		if n == "idx_stats_date_time" {
			return
		}
	}
	t.Errorf("stats indexes = %v, want one on date_time alone", names)
}

// The purge deletes in bounded chunks so the write lock is released in between,
// and it has to keep going until everything past the window is gone.
func TestDelOldStatsRemovesEverythingPastTheWindow(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "stats.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if err := database.CloseDBForTest(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})

	db := database.GetDB()
	old := int64(1000)       // far outside any retention window
	recent := int64(1 << 40) // far inside it
	// More than one chunk, so the loop has to run at least twice.
	rows := make([]model.Stats, 0, delOldStatsChunk+50)
	for i := 0; i < delOldStatsChunk+50; i++ {
		rows = append(rows, model.Stats{
			Resource: "user", Tag: "t", Direction: i%2 == 0, DateTime: old + int64(i),
		})
	}
	rows = append(rows, model.Stats{Resource: "user", Tag: "keep", DateTime: recent})
	if err := db.CreateInBatches(rows, 500).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := (&StatsService{}).DelOldStats(1); err != nil {
		t.Fatalf("DelOldStats: %v", err)
	}

	var left []model.Stats
	if err := db.Find(&left).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(left) != 1 {
		t.Fatalf("%d rows left, want only the recent one", len(left))
	}
	if left[0].DateTime != recent {
		t.Errorf("the surviving row has date_time %d, want %d", left[0].DateTime, recent)
	}
}

// A failed cycle is retried together with the next drain, which has a row for
// most of the same keys. Folded, a run of failures costs nothing in size: the
// batch stays one row per (resource, tag, direction) however long it lasts.
func TestMergeStatsFoldsByKey(t *testing.T) {
	rows := []model.Stats{
		{Id: 7, Resource: "user", Tag: "alice", Direction: true, Traffic: 100, DateTime: 10},
		{Resource: "user", Tag: "alice", Direction: false, Traffic: 1},
		{Resource: "inbound", Tag: "alice", Direction: true, Traffic: 5},
		{Id: 9, Resource: "user", Tag: "alice", Direction: true, Traffic: 23, DateTime: 20},
		{Resource: "user", Tag: "bob", Direction: true, Traffic: 4},
		{Resource: "user", Tag: "alice", Direction: false, Traffic: 2},
	}
	got := mergeStats(rows)

	want := []model.Stats{
		{Resource: "user", Tag: "alice", Direction: true, Traffic: 123},
		{Resource: "user", Tag: "alice", Direction: false, Traffic: 3},
		{Resource: "inbound", Tag: "alice", Direction: true, Traffic: 5},
		{Resource: "user", Tag: "bob", Direction: true, Traffic: 4},
	}
	if len(got) != len(want) {
		t.Fatalf("merged into %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g := got[i]
		g.DateTime = 0 // restamped with the current bucket before the insert
		if g != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if rows[0].Traffic != 100 {
		t.Error("merging must not write through to the rows it was handed")
	}
}

// One INSERT binds five variables per row and SQLite allows 32766, so a batch
// past ~6500 rows -- a few thousand active users -- cannot be written in one
// statement, and the client counters, written in the same transaction, stop
// with it. The rows go in split by the CreateBatchSize database.OpenDB sets.
func TestSaveStatsCommitsABatchPastTheVariableLimit(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "stats.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if err := database.CloseDBForTest(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	resetPending()
	t.Cleanup(resetPending)

	const tags = 8000
	// Two failed cycles' worth for every key, as a retry carries them.
	var held []model.Stats
	for cycle := 0; cycle < 2; cycle++ {
		for i := 0; i < tags; i++ {
			held = append(held, model.Stats{
				Resource: "outbound", Tag: fmt.Sprintf("out-%d", i), Direction: true, Traffic: 10,
			})
		}
	}
	// A bucket wider than the test, so the second save lands on the rows the
	// first one wrote: every batch then takes the upsert's conflict branch.
	const bucket = 1 << 40
	for save := 0; save < 2; save++ {
		holdPending(held)
		if err := (&StatsService{}).SaveStats(true, bucket); err != nil {
			t.Fatalf("SaveStats #%d: %v", save+1, err)
		}
		if n := pendingLen(); n != 0 {
			t.Fatalf("%d rows still pending after save #%d succeeded", n, save+1)
		}
	}

	var count, total int64
	db := database.GetDB()
	if err := db.Model(model.Stats{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if err := db.Model(model.Stats{}).Select("SUM(traffic)").Scan(&total).Error; err != nil {
		t.Fatalf("sum: %v", err)
	}
	if count != tags {
		t.Errorf("%d rows written, want one per key (%d)", count, tags)
	}
	if total != 2*2*tags*10 {
		t.Errorf("total traffic %d, want %d", total, 2*2*tags*10)
	}
}

// What a failed save holds back is the folded batch, so a database that keeps
// refusing writes does not make the next attempt any larger -- the point of
// folding, as an ever-growing batch is what crossed the variable limit.
func TestSaveStatsHoldsTheFoldedBatchAcrossFailures(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "stats.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if err := database.CloseDBForTest(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	resetPending()
	t.Cleanup(resetPending)

	const tags = 50
	cycle := func() []model.Stats {
		rows := make([]model.Stats, 0, tags)
		for i := 0; i < tags; i++ {
			rows = append(rows, model.Stats{
				Resource: "outbound", Tag: fmt.Sprintf("out-%d", i), Direction: true, Traffic: 10,
			})
		}
		return rows
	}

	db := database.GetDB()
	// Nothing to write into: every save fails until the table is back.
	if err := db.Exec("DROP TABLE stats").Error; err != nil {
		t.Fatalf("drop stats: %v", err)
	}
	const failures = 3
	for i := 0; i < failures; i++ {
		// A cycle's worth arrives the way a stopped core hands it over.
		carryPending(cycle())
		if err := (&StatsService{}).SaveStats(true, 1<<40); err == nil {
			t.Fatalf("save #%d succeeded with no stats table", i+1)
		}
		if n := pendingLen(); n != tags {
			t.Fatalf("after failure #%d %d rows held, want one per key (%d)", i+1, n, tags)
		}
	}

	if err := db.AutoMigrate(&model.Stats{}); err != nil {
		t.Fatalf("recreate stats: %v", err)
	}
	if err := (&StatsService{}).SaveStats(true, 1<<40); err != nil {
		t.Fatalf("SaveStats once the table is back: %v", err)
	}
	var total int64
	if err := db.Model(model.Stats{}).Select("SUM(traffic)").Scan(&total).Error; err != nil {
		t.Fatalf("sum: %v", err)
	}
	if total != failures*tags*10 {
		t.Errorf("total traffic %d, want every failed cycle's %d", total, failures*tags*10)
	}
}
