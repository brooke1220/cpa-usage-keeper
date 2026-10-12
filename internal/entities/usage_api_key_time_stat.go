package entities

import "time"

// UsageAPIKeyTimeStat 是 API Key 用量页的 15 分钟预聚合。
// bucket 按 UTC 对齐保存，查询时再按项目时区折算墙钟时间，因此修改 TZ 或工作时段都不需要重建。
type UsageAPIKeyTimeStat struct {
	ID int64 `gorm:"primaryKey"`
	// BucketStart 使用固定宽度 UTC 文本，范围查询和保留期清理都能直接按文本比较。
	BucketStart time.Time `gorm:"serializer:sortableTime;not null;uniqueIndex:uniq_usage_api_key_time_stats_bucket_api,priority:1"`
	// APIGroupKey 与其它派生聚合使用同一规范化（去空白，空值记为 unknown）。
	APIGroupKey string `gorm:"not null;uniqueIndex:uniq_usage_api_key_time_stats_bucket_api,priority:2"`
	// TotalTokens 累计全部事件的 total_tokens。
	TotalTokens int64 `gorm:"not null;default:0"`
	// MessageCount 累计 generate 为空或为 true 的请求数，失败请求也计入。
	MessageCount int64 `gorm:"not null;default:0"`
	// LatencyMS 累计成功消息的耗时毫秒数。
	LatencyMS int64 `gorm:"not null;default:0"`
	// LatencySamples 是计入 LatencyMS 的成功消息数。
	LatencySamples int64     `gorm:"not null;default:0"`
	CreatedAt      time.Time `gorm:"serializer:storageTime;not null"`
	UpdatedAt      time.Time `gorm:"serializer:storageTime;not null"`
}

// TableName 固定表名，migration 与运行时共用同一张表。
func (UsageAPIKeyTimeStat) TableName() string {
	return "usage_api_key_time_stats"
}
