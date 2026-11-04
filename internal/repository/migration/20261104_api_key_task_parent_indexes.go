package migration

import (
	"fmt"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

// Add only partial covering indexes. Published migrations and raw events remain
// unchanged; archived parent links can be resolved without scanning event bodies.
func apiKeyTaskParentIndexesMigration(tx *gorm.DB) error {
	for _, index := range []struct {
		model any
		name  string
	}{
		{&entities.UsageEvent{}, "idx_usage_events_task_parent"},
		{&entities.UsageEventArchive{}, "idx_usage_events_archive_task_parent"},
	} {
		if tx.Migrator().HasTable(index.model) && !tx.Migrator().HasIndex(index.model, index.name) {
			if err := tx.Migrator().CreateIndex(index.model, index.name); err != nil {
				return fmt.Errorf("create %s: %w", index.name, err)
			}
		}
	}
	return nil
}
