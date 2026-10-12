package repository

import (
	"context"
	"fmt"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/timeutil"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"
)

// usageAggregationCheckpointNames 是全部全局聚合水位；快照读取和归档门禁共用同一份名单。
var usageAggregationCheckpointNames = []entities.UsageAggregationCheckpointName{
	entities.UsageAggregationCheckpointOverview,
	entities.UsageAggregationCheckpointActivity,
	entities.UsageAggregationCheckpointLatency,
	entities.UsageAggregationCheckpointAPIKeyTime,
}

// UsageAggregationCheckpointSnapshot 是一次 SELECT 得到的全部已提交全局水位快照。
type UsageAggregationCheckpointSnapshot struct {
	// OverviewCursor 只代表 Overview 独立事务已经提交的进度。
	OverviewCursor int64
	// ActivityCursor 只代表 Activity 独立事务已经提交的进度。
	ActivityCursor int64
	// LatencyCursor 只代表 Latency 独立事务已经提交的进度。
	LatencyCursor int64
	// APIKeyTimeCursor 只代表 API Key 用量 15 分钟预聚合已经提交的进度。
	APIKeyTimeCursor int64
}

// Equal 只比较全部 cursor；时间戳不参与共享快路径判断。
func (snapshot UsageAggregationCheckpointSnapshot) Equal() bool {
	return snapshot.OverviewCursor == snapshot.ActivityCursor &&
		snapshot.ActivityCursor == snapshot.LatencyCursor &&
		snapshot.LatencyCursor == snapshot.APIKeyTimeCursor
}

// ReachedTarget 判断全部聚合是否都已追到 target。
func (snapshot UsageAggregationCheckpointSnapshot) ReachedTarget(target int64) bool {
	return snapshot.OverviewCursor >= target &&
		snapshot.ActivityCursor >= target &&
		snapshot.LatencyCursor >= target &&
		snapshot.APIKeyTimeCursor >= target
}

// LoadUsageAggregationCheckpointSnapshot 用一次 Reader 查询读取全部水位，缺行保持零值。
func LoadUsageAggregationCheckpointSnapshot(ctx context.Context, db *gorm.DB) (UsageAggregationCheckpointSnapshot, error) {
	if db == nil {
		return UsageAggregationCheckpointSnapshot{}, fmt.Errorf("database is nil")
	}
	// 只读路径不得 FirstOrCreate；缺失行由结构体零值表达首次聚合状态。
	var checkpoints []entities.UsageAggregationCheckpoint
	if err := db.Clauses(dbresolver.Read).WithContext(ctx).
		Where("name IN ?", usageAggregationCheckpointNames).
		Find(&checkpoints).Error; err != nil {
		return UsageAggregationCheckpointSnapshot{}, fmt.Errorf("load usage aggregation checkpoints: %w", err)
	}
	// 每行按受限 name 映射到固定字段，数据库中的未知值不影响其它合法水位。
	snapshot := UsageAggregationCheckpointSnapshot{}
	for _, checkpoint := range checkpoints {
		switch checkpoint.Name {
		case entities.UsageAggregationCheckpointOverview:
			snapshot.OverviewCursor = checkpoint.LastAggregatedUsageEventID
		case entities.UsageAggregationCheckpointActivity:
			snapshot.ActivityCursor = checkpoint.LastAggregatedUsageEventID
		case entities.UsageAggregationCheckpointLatency:
			snapshot.LatencyCursor = checkpoint.LastAggregatedUsageEventID
		case entities.UsageAggregationCheckpointAPIKeyTime:
			snapshot.APIKeyTimeCursor = checkpoint.LastAggregatedUsageEventID
		}
	}
	return snapshot, nil
}

// AdvanceUsageAggregationCheckpoint 在调用方事务内按 expected cursor 条件推进一行。
func AdvanceUsageAggregationCheckpoint(ctx context.Context, tx *gorm.DB, name entities.UsageAggregationCheckpointName, expectedCursor, nextCursor int64, now time.Time) error {
	if tx == nil {
		return fmt.Errorf("database is nil")
	}
	// 只允许实体层声明的名称，避免 typo 静默创建额外的 checkpoint。
	switch name {
	case entities.UsageAggregationCheckpointOverview, entities.UsageAggregationCheckpointActivity, entities.UsageAggregationCheckpointLatency, entities.UsageAggregationCheckpointAPIKeyTime:
	default:
		return fmt.Errorf("unsupported usage aggregation checkpoint %q", name)
	}
	// cursor 必须单调前进；相等更新会伪造 StatsUpdatedAt，新值更小则会重复累计。
	if expectedCursor < 0 || nextCursor <= expectedCursor {
		return fmt.Errorf("invalid usage aggregation checkpoint advance %q: %d -> %d", name, expectedCursor, nextCursor)
	}
	normalizedNow := timeutil.NormalizeStorageTime(now)
	// 首次推进时在同一事务内补零水位行；已有行由 ON CONFLICT 保持不变。
	checkpoint := entities.UsageAggregationCheckpoint{
		Name:                       name,
		LastAggregatedUsageEventID: 0,
		CreatedAt:                  normalizedNow,
		UpdatedAt:                  normalizedNow,
	}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&checkpoint).Error; err != nil {
		return fmt.Errorf("ensure usage aggregation checkpoint %q: %w", name, err)
	}
	// name + expectedCursor 是唯一并发保护；rows 与 cursor 由外层事务共同提交或回滚。
	result := tx.WithContext(ctx).Model(&entities.UsageAggregationCheckpoint{}).
		Where("name = ? AND last_aggregated_usage_event_id = ?", name, expectedCursor).
		Updates(map[string]any{
			"last_aggregated_usage_event_id": nextCursor,
			"stats_updated_at":               timeutil.FormatStorageTime(normalizedNow),
			"updated_at":                     timeutil.FormatStorageTime(normalizedNow),
		})
	if result.Error != nil {
		return fmt.Errorf("advance usage aggregation checkpoint %q: %w", name, result.Error)
	}
	// 没有恰好更新一行表示调用方基于过期快照，必须让外层事务回滚 rows。
	if result.RowsAffected != 1 {
		return fmt.Errorf("usage aggregation checkpoint %q conflict: expected cursor %d", name, expectedCursor)
	}
	return nil
}
