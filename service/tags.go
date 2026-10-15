package service

import (
	"bytes"

	"github.com/shenaba/2s-ui/database/model"
	"github.com/shenaba/2s-ui/util/common"

	"gorm.io/gorm"
)

// sameConfig reports whether the stored row (marshal, its sing-box shape)
// gives the core exactly config. Both come out of json.Marshal over a map, so
// the keys are in the same order; anything that still differs only costs an
// unneeded reload.
func sameConfig(marshal func() ([]byte, error), config []byte) bool {
	stored, err := marshal()
	return err == nil && bytes.Equal(stored, config)
}

// The core looks an outbound up among the endpoints too, and an inbound as
// well (outboundManager.Outbound, inboundManager.Get), and counts an
// endpoint's traffic under its tag as an inbound's. So a tag must be free
// across those tables, not only in its own: an outbound named like an endpoint
// silently took over every rule, group member and detour naming it.
// Inbounds and outbounds stay separate namespaces.
//
// only narrows the rows that count to the ones in the local core: a node
// replica sits in the inbounds table but runs on its node, so an endpoint
// named like one collides with nothing here.
var sharedTags = map[string][]struct {
	table any
	kind  string
	only  string
}{
	"inbound":  {{table: model.Endpoint{}, kind: "endpoint"}},
	"outbound": {{table: model.Endpoint{}, kind: "endpoint"}},
	"endpoint": {{table: model.Outbound{}, kind: "outbound"}, {table: model.Inbound{}, kind: "inbound", only: "node_id IS NULL"}},
}

// tagFreeAcross is tagFree, and then the same against every table kind shares
// tags with (sharedTags).
func tagFreeAcross(tx *gorm.DB, table any, kind string, tag string, id uint) error {
	if err := tagFree(tx, table, kind, tag, id); err != nil {
		return err
	}
	for _, other := range sharedTags[kind] {
		rows := tx.Model(other.table)
		if other.only != "" {
			rows = rows.Where(other.only)
		}
		// id 0: no row of another table is the one being saved.
		if err := tagFreeIn(rows, other.kind, tag, 0); err != nil {
			return err
		}
	}
	return nil
}

// tagFree refuses a tag another row of table already holds. Tags are unique in
// the database, but a save changes the running core before it writes the row:
// a new inbound, outbound, endpoint or service -- or a rename -- onto a tag
// another one holds would replace that one in the core, and only then fail on
// the constraint, leaving the core on a config the database rolled back. id is
// the row being saved, 0 for a new one.
func tagFree(tx *gorm.DB, table any, kind string, tag string, id uint) error {
	return tagFreeIn(tx.Model(table), kind, tag, id)
}

// tagFreeIn is tagFree over rows, a query already narrowed to a table.
func tagFreeIn(rows *gorm.DB, kind string, tag string, id uint) error {
	var taken int64
	err := rows.Where("tag = ? AND id <> ?", tag, id).Count(&taken).Error
	if err != nil {
		return err
	}
	if taken > 0 {
		return common.NewErrorf("%s tag already in use: %s", kind, tag)
	}
	return nil
}
