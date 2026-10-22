package model

import "gorm.io/gorm"

// InsertBatch is how many rows one INSERT carries. SQLite binds at most 32766
// variables per statement, one per column per row, so a slice written in one
// statement fails outright past a few thousand rows -- ~6500 stats or changes
// rows, ~1300 clients. 500 keeps a model of up to 65 columns under it.
//
// database.OpenDB sets it as gorm's CreateBatchSize, which splits every Create
// of a slice on the panel's own connection. Save of a slice does not go through
// Create, so it needs SaveAll wherever it runs. A DB opened with a gorm.Config
// of its own (the backup file, cmd/migration) has no CreateBatchSize, so a
// Create of a slice there has to batch explicitly.
const InsertBatch = 500

// SaveAll is tx.Save(rows) split into InsertBatch-row statements. Lives here
// rather than in database because cmd/migration needs it, and database imports
// cmd/migration.
func SaveAll[T any](tx *gorm.DB, rows []T) error {
	for start := 0; start < len(rows); start += InsertBatch {
		end := min(start+InsertBatch, len(rows))
		if err := tx.Save(rows[start:end]).Error; err != nil {
			return err
		}
	}
	return nil
}
