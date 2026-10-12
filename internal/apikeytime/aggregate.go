package apikeytime

import (
	"sort"
	"time"

	"cpa-usage-keeper/internal/activity"
	"cpa-usage-keeper/internal/entities"
)

const (
	// ReportMaxDays 是 API Key 用量页可查询的最大统计日数。
	ReportMaxDays = 90
	// BucketMinutes 是预聚合粒度；所有现行时区偏移和夏令时切换都是 15 分钟的整数倍，
	// 因此 UTC 对齐的 15 分钟 bucket 在任意项目时区下都对应完整的墙钟 15 分钟。
	BucketMinutes = 15
	// BucketDuration 是 BucketMinutes 的 time.Duration 形式。
	BucketDuration = BucketMinutes * time.Minute
	// RetentionDays 覆盖完整报表窗口，并为统计日跨自然日和查询边界余量多留两天。
	RetentionDays = ReportMaxDays + 2
)

// BucketStart 把时间截断到 UTC 15 分钟边界。
func BucketStart(t time.Time) time.Time {
	return t.UTC().Truncate(BucketDuration)
}

// RetentionCutoff 返回仍需保留的最早 bucket 起点；更早的 bucket 不再写入，并由每日维护清理。
func RetentionCutoff(now time.Time) time.Time {
	return BucketStart(now.Add(-RetentionDays * 24 * time.Hour))
}

// IsMessage 与报表口径一致：generate 为空或为 true 的请求都算一条消息，失败请求也计入。
func IsMessage(event entities.UsageEvent) bool {
	return event.Generate == nil || *event.Generate
}

// IsLatencySample 只把成功且耗时大于零的消息计入累计耗时。
func IsLatencySample(event entities.UsageEvent) bool {
	return IsMessage(event) && !event.Failed && event.LatencyMS > 0
}

type rowKey struct {
	bucket      time.Time
	apiGroupKey string
}

// BuildRows 把一页事件合并成按 (bucket, api_group_key) 唯一的增量行。
// 输入页由多个聚合共享，函数只读不改；输出按唯一键排序，保证 migration 与运行时结果一致。
func BuildRows(events []entities.UsageEvent, now time.Time) []entities.UsageAPIKeyTimeStat {
	cutoff := RetentionCutoff(now)
	rows := make(map[rowKey]*entities.UsageAPIKeyTimeStat)
	for _, event := range events {
		message := IsMessage(event)
		// 既没有 Token 也不算消息的事件不会改变任何统计值，不为它创建空行。
		if event.TotalTokens == 0 && !message {
			continue
		}
		bucket := BucketStart(event.Timestamp)
		// 超出保留期的迟到事件不进入报表窗口；checkpoint 仍由调用方照常推进。
		if bucket.Before(cutoff) {
			continue
		}
		key := rowKey{bucket: bucket, apiGroupKey: activity.NormalizeAPIGroupKey(event.APIGroupKey)}
		row := rows[key]
		if row == nil {
			row = &entities.UsageAPIKeyTimeStat{BucketStart: key.bucket, APIGroupKey: key.apiGroupKey}
			rows[key] = row
		}
		row.TotalTokens += event.TotalTokens
		if message {
			row.MessageCount++
		}
		if IsLatencySample(event) {
			row.LatencyMS += event.LatencyMS
			row.LatencySamples++
		}
	}
	result := make([]entities.UsageAPIKeyTimeStat, 0, len(rows))
	for _, row := range rows {
		result = append(result, *row)
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].BucketStart.Equal(result[j].BucketStart) {
			return result[i].BucketStart.Before(result[j].BucketStart)
		}
		return result[i].APIGroupKey < result[j].APIGroupKey
	})
	return result
}
