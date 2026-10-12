package repository

import (
	"context"
	"fmt"
	"time"

	"cpa-usage-keeper/internal/apikeytime"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository/apikeytimestore"
	"cpa-usage-keeper/internal/timeutil"

	"gorm.io/gorm"
)

const usageAPIKeyTimeAggregationBatchSize = 1000

// AggregateUsageAPIKeyTimeStats 循环执行有界页面，供没有后台 Runner 的兼容路径完整追赶。
func AggregateUsageAPIKeyTimeStats(ctx context.Context, db *gorm.DB, now time.Time) error {
	if db == nil {
		return fmt.Errorf("database is nil")
	}
	normalizedNow := timeutil.NormalizeStorageTime(now)
	for {
		processed, err := AggregateUsageAPIKeyTimeStatsBatch(ctx, db, normalizedNow)
		if err != nil {
			return err
		}
		if processed < usageAPIKeyTimeAggregationBatchSize {
			return nil
		}
	}
}

// AggregateUsageAPIKeyTimeStatsBatch 读取一页事件、构建 15 分钟增量行，再用独立事务推进自己的水位。
func AggregateUsageAPIKeyTimeStatsBatch(ctx context.Context, db *gorm.DB, now time.Time) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("database is nil")
	}
	targetEventID, err := LoadUsageAggregationTargetEventID(ctx, db)
	if err != nil {
		return 0, err
	}
	snapshot, err := LoadUsageAggregationCheckpointSnapshot(ctx, db)
	if err != nil {
		return 0, err
	}
	events, err := LoadUsageAggregationEventPage(ctx, db, snapshot.APIKeyTimeCursor, targetEventID, usageAPIKeyTimeAggregationBatchSize)
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}
	rows := apikeytime.BuildRows(events, now)
	nextCursor := events[len(events)-1].ID
	if err := ApplyUsageAPIKeyTimeAggregationPage(ctx, db, snapshot.APIKeyTimeCursor, nextCursor, rows, now); err != nil {
		return 0, err
	}
	return len(events), nil
}

// ApplyUsageAPIKeyTimeAggregationPage 在独立短事务内累加 rows 并条件推进 API Key 用量水位。
func ApplyUsageAPIKeyTimeAggregationPage(ctx context.Context, db *gorm.DB, expectedCursor, nextCursor int64, rows []entities.UsageAPIKeyTimeStat, now time.Time) error {
	if db == nil {
		return fmt.Errorf("database is nil")
	}
	normalizedNow := timeutil.NormalizeStorageTime(now)
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := apikeytimestore.ApplyRows(tx, rows, normalizedNow); err != nil {
			return err
		}
		// cursor 冲突时回滚本页全部行，防止下一轮重复累计。
		return AdvanceUsageAggregationCheckpoint(ctx, tx, entities.UsageAggregationCheckpointAPIKeyTime, expectedCursor, nextCursor, normalizedNow)
	})
}

// CleanupUsageAPIKeyTimeStats 删除报表窗口之外的 15 分钟行。
func CleanupUsageAPIKeyTimeStats(db *gorm.DB, now time.Time) error {
	if db == nil {
		return fmt.Errorf("database is nil")
	}
	cutoff := apikeytime.RetentionCutoff(now)
	if err := db.Where("bucket_start < ?", timeutil.FormatSortableStorageTime(cutoff)).Delete(&entities.UsageAPIKeyTimeStat{}).Error; err != nil {
		return fmt.Errorf("cleanup usage api key time stats: %w", err)
	}
	return nil
}
