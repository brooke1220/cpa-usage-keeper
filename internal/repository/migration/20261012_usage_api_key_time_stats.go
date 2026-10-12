package migration

import (
	"fmt"
	"strings"
	"time"

	"cpa-usage-keeper/internal/apikeytime"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository/apikeytimestore"
	"cpa-usage-keeper/internal/timeutil"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	usageAggregationCheckpointsTable        = "usage_aggregation_checkpoints"
	usageAggregationCheckpointsRebuildTable = "usage_aggregation_checkpoints_rebuild"
	// usageAPIKeyTimeMigrationBatchSize 与运行时事件页上限一致，避免回填长时间占用唯一 writer。
	usageAPIKeyTimeMigrationBatchSize = 1000
)

// usageAggregationCheckpointRebuild 冻结本 migration 写入的目标 schema，不随实体后续演进而变化。
type usageAggregationCheckpointRebuild struct {
	Name                       string     `gorm:"type:text;primaryKey;check:chk_usage_aggregation_checkpoints_name,name IN ('overview','activity','latency','api_key_time')"`
	LastAggregatedUsageEventID int64      `gorm:"not null;default:0"`
	StatsUpdatedAt             *time.Time `gorm:"serializer:storageTime"`
	CreatedAt                  time.Time  `gorm:"serializer:storageTime;not null"`
	UpdatedAt                  time.Time  `gorm:"serializer:storageTime;not null"`
}

func (usageAggregationCheckpointRebuild) TableName() string {
	return usageAggregationCheckpointsRebuildTable
}

// usageAPIKeyTimeCheckpointMigration 在默认事务内放宽 checkpoint CHECK、创建 15 分钟预聚合表并预置零水位。
// SQLite 不能修改已有 CHECK，只能按“新表、复制、删旧表、改名”重建；三行旧水位原样保留。
func usageAPIKeyTimeCheckpointMigration(tx *gorm.DB) error {
	if tx == nil {
		return fmt.Errorf("database is nil")
	}
	if err := rebuildUsageAggregationCheckpointsForAPIKeyTime(tx); err != nil {
		return err
	}
	if err := tx.AutoMigrate(&entities.UsageAPIKeyTimeStat{}); err != nil {
		return fmt.Errorf("create usage api key time stats: %w", err)
	}
	now := timeutil.NormalizeStorageTime(tx.NowFunc())
	seed := entities.UsageAggregationCheckpoint{
		Name:      entities.UsageAggregationCheckpointAPIKeyTime,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return fmt.Errorf("seed usage api key time checkpoint: %w", err)
	}
	return nil
}

func rebuildUsageAggregationCheckpointsForAPIKeyTime(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(usageAggregationCheckpointsTable) {
		// 缺表只出现在最小化测试 schema；按当前实体直接创建即可获得新 CHECK。
		if err := tx.AutoMigrate(&entities.UsageAggregationCheckpoint{}); err != nil {
			return fmt.Errorf("create usage aggregation checkpoints: %w", err)
		}
		return nil
	}
	var tableSQL string
	if err := tx.Raw("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", usageAggregationCheckpointsTable).Scan(&tableSQL).Error; err != nil {
		return fmt.Errorf("inspect usage aggregation checkpoints schema: %w", err)
	}
	if strings.Contains(tableSQL, "'api_key_time'") {
		return nil
	}
	var before int64
	if err := tx.Table(usageAggregationCheckpointsTable).Count(&before).Error; err != nil {
		return fmt.Errorf("count usage aggregation checkpoints: %w", err)
	}
	if tx.Migrator().HasTable(usageAggregationCheckpointsRebuildTable) {
		if err := tx.Migrator().DropTable(usageAggregationCheckpointsRebuildTable); err != nil {
			return fmt.Errorf("drop stale usage aggregation checkpoint rebuild table: %w", err)
		}
	}
	if err := tx.Migrator().CreateTable(&usageAggregationCheckpointRebuild{}); err != nil {
		return fmt.Errorf("create usage aggregation checkpoint rebuild table: %w", err)
	}
	columns := "name, last_aggregated_usage_event_id, stats_updated_at, created_at, updated_at"
	if err := tx.Exec("INSERT INTO " + usageAggregationCheckpointsRebuildTable + " (" + columns + ") SELECT " + columns + " FROM " + usageAggregationCheckpointsTable).Error; err != nil {
		return fmt.Errorf("copy usage aggregation checkpoints: %w", err)
	}
	var after int64
	if err := tx.Table(usageAggregationCheckpointsRebuildTable).Count(&after).Error; err != nil {
		return fmt.Errorf("verify copied usage aggregation checkpoints: %w", err)
	}
	if after != before {
		return fmt.Errorf("verify copied usage aggregation checkpoints: expected %d rows, got %d", before, after)
	}
	if err := tx.Exec("DROP TABLE " + usageAggregationCheckpointsTable).Error; err != nil {
		return fmt.Errorf("drop legacy usage aggregation checkpoints: %w", err)
	}
	if err := tx.Exec("ALTER TABLE " + usageAggregationCheckpointsRebuildTable + " RENAME TO " + usageAggregationCheckpointsTable).Error; err != nil {
		return fmt.Errorf("rename rebuilt usage aggregation checkpoints: %w", err)
	}
	return nil
}

// usageAPIKeyTimeStatsMigration 从当前仍保留的 hot usage_events 回填 15 分钟预聚合。
// 报表窗口不超过 hot 保留期，因此不需要读取 archive；每页独立提交，重启后从 checkpoint 继续。
func usageAPIKeyTimeStatsMigration(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database is nil")
	}
	// 只追启动时已经可见的最大 ID，避免持续写入时永远无法完成升级。
	var targetEventID int64
	if db.Migrator().HasTable(&entities.UsageEvent{}) {
		if err := db.Model(&entities.UsageEvent{}).Select("COALESCE(MAX(id), 0)").Scan(&targetEventID).Error; err != nil {
			return fmt.Errorf("load usage api key time migration target: %w", err)
		}
	}
	now := timeutil.NormalizeStorageTime(db.NowFunc())
	for {
		processed, err := migrateUsageAPIKeyTimeBatch(db, now, targetEventID)
		if err != nil {
			return err
		}
		if processed == 0 {
			break
		}
	}
	var checkpoint entities.UsageAggregationCheckpoint
	if err := db.Where("name = ?", entities.UsageAggregationCheckpointAPIKeyTime).Take(&checkpoint).Error; err != nil {
		return fmt.Errorf("verify usage api key time migration checkpoint: %w", err)
	}
	if checkpoint.LastAggregatedUsageEventID < targetEventID {
		return fmt.Errorf("usage api key time migration checkpoint %d did not reach target %d", checkpoint.LastAggregatedUsageEventID, targetEventID)
	}
	return nil
}

func migrateUsageAPIKeyTimeBatch(db *gorm.DB, now time.Time, targetEventID int64) (int, error) {
	processed := 0
	err := db.Transaction(func(tx *gorm.DB) error {
		// 前一个 migration 已预置零水位；缺行说明升级前置条件不完整，不能猜测 cursor。
		var checkpoint entities.UsageAggregationCheckpoint
		if err := tx.Where("name = ?", entities.UsageAggregationCheckpointAPIKeyTime).Take(&checkpoint).Error; err != nil {
			return fmt.Errorf("load usage api key time migration checkpoint: %w", err)
		}
		if checkpoint.LastAggregatedUsageEventID >= targetEventID {
			return nil
		}
		var events []entities.UsageEvent
		if err := tx.Select(entities.UsageAggregationEventProjectionColumns).
			Where("id > ? AND id <= ?", checkpoint.LastAggregatedUsageEventID, targetEventID).
			Order("id asc").
			Limit(usageAPIKeyTimeMigrationBatchSize).
			Find(&events).Error; err != nil {
			return fmt.Errorf("load usage api key time migration events: %w", err)
		}
		if len(events) == 0 {
			return nil
		}
		// 分桶与口径只由 apikeytime 包实现，migration 与运行时不会分叉。
		if err := apikeytimestore.ApplyRows(tx, apikeytime.BuildRows(events, now), now); err != nil {
			return err
		}
		nextCursor := events[len(events)-1].ID
		result := tx.Model(&entities.UsageAggregationCheckpoint{}).
			Where("name = ? AND last_aggregated_usage_event_id = ?", entities.UsageAggregationCheckpointAPIKeyTime, checkpoint.LastAggregatedUsageEventID).
			Updates(map[string]any{
				"last_aggregated_usage_event_id": nextCursor,
				"stats_updated_at":               timeutil.FormatStorageTime(now),
				"updated_at":                     timeutil.FormatStorageTime(now),
			})
		if result.Error != nil {
			return fmt.Errorf("advance usage api key time migration checkpoint: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("advance usage api key time migration checkpoint: expected cursor %d changed", checkpoint.LastAggregatedUsageEventID)
		}
		processed = len(events)
		return nil
	})
	return processed, err
}
