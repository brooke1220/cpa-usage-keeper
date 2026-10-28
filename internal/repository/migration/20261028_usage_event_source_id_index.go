package migration

import (
	"fmt"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

const migrationUsageEventSourceIDIndex = "20261028_usage_event_source_id_index"

// usageEventSourceIDIndexMigration 给已有库补上按 CPA 筛选请求事件的部分索引，新库由实体声明直接创建。
func usageEventSourceIDIndexMigration(tx *gorm.DB) error {
	migrator := tx.Migrator()
	if !migrator.HasTable(&entities.UsageEvent{}) {
		return nil
	}
	const indexName = "idx_usage_events_source_id_timestamp_id"
	if migrator.HasIndex(&entities.UsageEvent{}, indexName) {
		return nil
	}
	if err := migrator.CreateIndex(&entities.UsageEvent{}, indexName); err != nil {
		return fmt.Errorf("create %s: %w", indexName, err)
	}
	return nil
}
