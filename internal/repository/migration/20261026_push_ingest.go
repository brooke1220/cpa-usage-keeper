package migration

import (
	"cpa-usage-keeper/internal/entities"
	"fmt"
	"gorm.io/gorm"
)

func pushIngestMigration(tx *gorm.DB) error {
	if err := tx.AutoMigrate(&entities.PushReceipt{}); err != nil {
		return err
	}
	for _, model := range []any{&entities.UsageEvent{}, &entities.UsageEventArchive{}, &entities.RedisUsageInbox{}} {
		if !tx.Migrator().HasTable(model) {
			continue
		}
		for _, field := range []string{"SourceID", "DeliveryEventID"} {
			if !tx.Migrator().HasColumn(model, field) {
				if err := tx.Migrator().AddColumn(model, field); err != nil {
					return fmt.Errorf("add push provenance: %w", err)
				}
			}
		}
	}
	return nil
}
