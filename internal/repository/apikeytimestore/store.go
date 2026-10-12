package apikeytimestore

import (
	"fmt"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/timeutil"

	"gorm.io/gorm"
)

// ApplyRows 把一页增量行累加到 usage_api_key_time_stats；migration 与运行时共用同一实现。
func ApplyRows(tx *gorm.DB, rows []entities.UsageAPIKeyTimeStat, now time.Time) error {
	for _, row := range rows {
		// 任一行失败都交给调用方事务整体回滚，checkpoint 不会单独前进。
		if err := applyRow(tx, row, now); err != nil {
			return err
		}
	}
	return nil
}

func applyRow(tx *gorm.DB, row entities.UsageAPIKeyTimeStat, now time.Time) error {
	normalizedNow := timeutil.NormalizeStorageTime(now)
	// 与其它派生聚合一致：先按唯一键做增量 UPDATE，不依赖 SQLite 专属 upsert 语法。
	result := tx.Model(&entities.UsageAPIKeyTimeStat{}).
		Where("bucket_start = ? AND api_group_key = ?", timeutil.FormatSortableStorageTime(row.BucketStart), row.APIGroupKey).
		Updates(map[string]any{
			"total_tokens":    gorm.Expr("total_tokens + ?", row.TotalTokens),
			"message_count":   gorm.Expr("message_count + ?", row.MessageCount),
			"latency_ms":      gorm.Expr("latency_ms + ?", row.LatencyMS),
			"latency_samples": gorm.Expr("latency_samples + ?", row.LatencySamples),
			"updated_at":      timeutil.FormatStorageTime(normalizedNow),
		})
	if result.Error != nil {
		return fmt.Errorf("update usage api key time row: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}
	row.ID = 0
	row.CreatedAt = normalizedNow
	row.UpdatedAt = normalizedNow
	if err := tx.Create(&row).Error; err != nil {
		return fmt.Errorf("insert usage api key time row: %w", err)
	}
	return nil
}
