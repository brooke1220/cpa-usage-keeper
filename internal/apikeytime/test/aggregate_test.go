package test

import (
	"testing"
	"time"

	"cpa-usage-keeper/internal/apikeytime"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
)

func TestBuildRowsBucketsByUTCQuarterHourAndAppliesReportRules(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	generate := true
	warmup := false
	events := []entities.UsageEvent{
		{ID: 1, APIGroupKey: " key-a ", Timestamp: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), TotalTokens: 1, Generate: &generate, LatencyMS: 100},
		{ID: 2, APIGroupKey: "key-a", Timestamp: time.Date(2026, 10, 12, 9, 14, 59, 0, time.UTC), TotalTokens: 2, Generate: nil, LatencyMS: 200},
		{ID: 3, APIGroupKey: "key-a", Timestamp: time.Date(2026, 10, 12, 9, 15, 0, 0, time.UTC), TotalTokens: 4, Generate: &generate, Failed: true, LatencyMS: 400},
		{ID: 4, APIGroupKey: "", Timestamp: time.Date(2026, 10, 12, 9, 15, 0, 0, time.UTC), TotalTokens: 8, Generate: &warmup, LatencyMS: 800},
		{ID: 5, APIGroupKey: "key-a", Timestamp: time.Date(2026, 10, 12, 9, 20, 0, 0, time.UTC), TotalTokens: 0, Generate: &warmup},
		// 超出保留期的迟到事件不写入。
		{ID: 6, APIGroupKey: "key-a", Timestamp: now.AddDate(0, 0, -(apikeytime.RetentionDays + 1)), TotalTokens: 16, Generate: &generate},
	}
	rows := apikeytime.BuildRows(events, now)
	if len(rows) != 3 {
		t.Fatalf("expected three rows, got %+v", rows)
	}
	first, second, third := rows[0], rows[1], rows[2]
	if !first.BucketStart.Equal(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)) || first.APIGroupKey != "key-a" || first.TotalTokens != 3 || first.MessageCount != 2 || first.LatencyMS != 300 || first.LatencySamples != 2 {
		t.Fatalf("unexpected first bucket: %+v", first)
	}
	// 失败消息计入消息数，但不计入耗时。
	if second.APIGroupKey != "key-a" || second.TotalTokens != 4 || second.MessageCount != 1 || second.LatencyMS != 0 || second.LatencySamples != 0 {
		t.Fatalf("unexpected second bucket: %+v", second)
	}
	// 预热请求只贡献 Token，不算消息；空 key 与其它聚合一样归入 unknown。
	if third.APIGroupKey != "unknown" || third.TotalTokens != 8 || third.MessageCount != 0 || third.LatencySamples != 0 {
		t.Fatalf("unexpected third bucket: %+v", third)
	}
	if events[0].APIGroupKey != " key-a " {
		t.Fatal("BuildRows must not mutate the shared event page")
	}
}

func TestReportWindowFitsInsideHotRetention(t *testing.T) {
	// 回填只读 hot 表；报表窗口超过 hot 保留期时，升级后最早几天会缺数据。
	if apikeytime.ReportMaxDays > repository.UsageEventsHotRetentionDays {
		t.Fatalf("report window %d exceeds hot retention %d", apikeytime.ReportMaxDays, repository.UsageEventsHotRetentionDays)
	}
	if apikeytime.RetentionDays < apikeytime.ReportMaxDays+1 {
		t.Fatalf("rollup retention %d must cover the report window plus the statistical-day offset", apikeytime.RetentionDays)
	}
}
