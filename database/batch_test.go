package database

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/shenaba/2s-ui/database/model"
)

// One statement binds a variable per column per row, and SQLite allows 32766.
// A deplete or bulk add touching a few thousand clients writes past that, and
// the whole transaction -- disabling the clients included -- fails every cycle.
func TestSlicesAreWrittenPastTheVariableLimit(t *testing.T) {
	if err := InitDB(filepath.Join(t.TempDir(), "batch.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if err := CloseDBForTest(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	db := GetDB()

	// Create: covered by the CreateBatchSize OpenDB sets.
	changes := make([]model.Changes, 8000)
	for i := range changes {
		changes[i] = model.Changes{Actor: "DepleteJob", Key: "clients", Action: "disable", Obj: []byte(`"c"`)}
	}
	if err := db.Model(model.Changes{}).Create(&changes).Error; err != nil {
		t.Fatalf("create %d changes: %v", len(changes), err)
	}

	// Save: not covered by CreateBatchSize, hence model.SaveAll. Pointers, as
	// the bulk client paths hold them, so the ids the insert assigns come back.
	clients := make([]*model.Client, 3000)
	for i := range clients {
		clients[i] = &model.Client{Name: fmt.Sprintf("c%d", i), Inbounds: []byte("[]")}
	}
	if err := model.SaveAll(db, clients); err != nil {
		t.Fatalf("save %d clients: %v", len(clients), err)
	}
	for _, c := range clients {
		if c.Id == 0 {
			t.Fatalf("client %q has no id after the save", c.Name)
		}
	}

	// And as an update of rows that exist, which is what editbulk and the
	// traffic reset do.
	for _, c := range clients {
		c.Up = 7
	}
	if err := model.SaveAll(db, clients); err != nil {
		t.Fatalf("re-save %d clients: %v", len(clients), err)
	}

	var nChanges, nClients, upTotal int64
	if err := db.Model(model.Changes{}).Count(&nChanges).Error; err != nil {
		t.Fatalf("count changes: %v", err)
	}
	if err := db.Model(model.Client{}).Count(&nClients).Error; err != nil {
		t.Fatalf("count clients: %v", err)
	}
	if err := db.Model(model.Client{}).Select("SUM(up)").Scan(&upTotal).Error; err != nil {
		t.Fatalf("sum up: %v", err)
	}
	if nChanges != int64(len(changes)) || nClients != int64(len(clients)) {
		t.Errorf("wrote %d changes and %d clients, want %d and %d", nChanges, nClients, len(changes), len(clients))
	}
	if upTotal != 7*int64(len(clients)) {
		t.Errorf("up totals %d after the update, want %d", upTotal, 7*len(clients))
	}
}
