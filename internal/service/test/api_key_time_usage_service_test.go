package test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"cpa-usage-keeper/internal/apikeytime"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/service"
	"cpa-usage-keeper/internal/service/dto"
	"cpa-usage-keeper/internal/workhours"
	"gorm.io/gorm"
)

func TestAPIKeyTimeUsageCountsMessagesAndSuccessfulLatencyByWorkHours(t *testing.T) {
	db := openUsageServiceTestDatabase(t)
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 10, 9, 20, 0, 0, 0, location)
	key := entities.CPAAPIKey{APIKey: "sk-time-usage-test"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	warmup := false
	generate := true
	events := []entities.UsageEvent{
		{EventKey: "warmup", APIGroupKey: key.APIKey, Timestamp: time.Date(2026, 10, 8, 10, 0, 0, 0, location), TotalTokens: 1, Generate: &warmup, LatencyMS: 5000},
		{EventKey: "failed", APIGroupKey: key.APIKey, Timestamp: time.Date(2026, 10, 8, 11, 0, 0, 0, location), TotalTokens: 2, Generate: &generate, Failed: true, LatencyMS: 8000},
		{EventKey: "work", APIGroupKey: key.APIKey, Timestamp: time.Date(2026, 10, 8, 12, 0, 0, 0, location), TotalTokens: 4, Generate: &generate, LatencyMS: 3000},
		{EventKey: "off", APIGroupKey: key.APIKey, Timestamp: time.Date(2026, 10, 8, 19, 0, 0, 0, location), TotalTokens: 8, Generate: &generate, LatencyMS: 7000},
	}
	if err := db.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{
		Now:       func() time.Time { return now },
		WorkStart: "09:00",
		WorkEnd:   "18:00",
		Location:  location,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{StartDate: "2026-10-08", EndDate: "2026-10-08"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.TotalTokens != 15 || report.Summary.DaytimeTokens != 7 || report.Summary.AfterHoursTokens != 8 {
		t.Fatalf("unexpected token totals: %+v", report.Summary)
	}
	if report.Summary.MessageCount != 3 || report.Summary.DaytimeMessageCount != 2 || report.Summary.AfterHoursMessageCount != 1 {
		t.Fatalf("unexpected message totals: %+v", report.Summary)
	}
	if report.Summary.DaytimeLatencyMS != 3000 || report.Summary.AfterHoursLatencyMS != 7000 || report.Summary.DaytimeLatencySamples != 1 || report.Summary.AfterHoursLatencySamples != 1 {
		t.Fatalf("unexpected latency totals: %+v", report.Summary)
	}
}

func TestAPIKeyTimeUsageReadsUpdatedWorkHoursClock(t *testing.T) {
	db := openUsageServiceTestDatabase(t)
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 10, 9, 20, 0, 0, 0, location)
	key := entities.CPAAPIKey{APIKey: "sk-time-usage-clock"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&entities.UsageEvent{EventKey: "clock", APIGroupKey: key.APIKey, Timestamp: time.Date(2026, 10, 9, 18, 15, 0, 0, location), TotalTokens: 10}).Error; err != nil {
		t.Fatal(err)
	}
	initial, err := workhours.Parse("09:00", "18:00")
	if err != nil {
		t.Fatal(err)
	}
	clock := workhours.NewClock(initial)
	provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Now: func() time.Time { return now }, Clock: clock, Location: location})
	if err != nil {
		t.Fatal(err)
	}
	report, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{StartDate: "2026-10-09", EndDate: "2026-10-09"})
	if err != nil || report.Summary.AfterHoursTokens != 10 {
		t.Fatalf("initial schedule: %+v err=%v", report, err)
	}
	updated, err := workhours.Parse("09:00", "18:30")
	if err != nil {
		t.Fatal(err)
	}
	clock.Set(updated)
	report, err = provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{StartDate: "2026-10-09", EndDate: "2026-10-09"})
	if err != nil || report.Summary.DaytimeTokens != 10 || report.Summary.AfterHoursTokens != 0 {
		t.Fatalf("updated schedule: %+v err=%v", report, err)
	}
}

func TestAPIKeyTimeUsageKeepsEveningAndOvernightInTheSameStatisticalDay(t *testing.T) {
	db := openUsageServiceTestDatabase(t)
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	key := entities.CPAAPIKey{APIKey: "sk-time-usage-boundary"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]entities.UsageEvent{
		{EventKey: "clock-out", APIGroupKey: key.APIKey, Timestamp: time.Date(2026, 10, 8, 18, 0, 0, 0, location), TotalTokens: 10},
		{EventKey: "overnight", APIGroupKey: key.APIKey, Timestamp: time.Date(2026, 10, 9, 8, 59, 0, 0, location), TotalTokens: 20},
		{EventKey: "next-workday", APIGroupKey: key.APIKey, Timestamp: time.Date(2026, 10, 9, 9, 0, 0, 0, location), TotalTokens: 30},
	}).Error; err != nil {
		t.Fatal(err)
	}
	provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{
		Now:       func() time.Time { return time.Date(2026, 10, 9, 10, 0, 0, 0, location) },
		WorkStart: "09:00",
		WorkEnd:   "18:00",
		Location:  location,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{StartDate: "2026-10-08", EndDate: "2026-10-09"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Daily) != 2 || report.Daily[0].AfterHoursTokens != 30 || report.Daily[0].DaytimeTokens != 0 || report.Daily[1].DaytimeTokens != 30 || report.Daily[1].AfterHoursTokens != 0 {
		t.Fatalf("boundary events were split across the wrong statistical days: %+v", report.Daily)
	}
}

// 同一批事件分别以“全部未聚合”“部分聚合”“全部聚合”三种状态读取，报表必须逐字段一致。
func TestAPIKeyTimeUsageIsExactWhetherEventsAreAggregatedOrNot(t *testing.T) {
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 10, 9, 20, 0, 0, 0, location)
	generate := true
	warmup := false
	build := func(t *testing.T) (*gorm.DB, service.APIKeyTimeUsageProvider) {
		t.Helper()
		db := openUsageServiceTestDatabase(t)
		if err := db.Create(&[]entities.CPAAPIKey{{APIKey: "sk-exact-a"}, {APIKey: "sk-exact-b"}}).Error; err != nil {
			t.Fatal(err)
		}
		events := []entities.UsageEvent{
			{EventKey: "a-before-work", APIGroupKey: "sk-exact-a", Timestamp: time.Date(2026, 10, 8, 8, 59, 59, 0, location), TotalTokens: 3, Generate: &generate, LatencyMS: 100},
			{EventKey: "a-work", APIGroupKey: " sk-exact-a ", Timestamp: time.Date(2026, 10, 8, 9, 0, 0, 0, location), TotalTokens: 5, Generate: &generate, LatencyMS: 200},
			{EventKey: "a-failed", APIGroupKey: "sk-exact-a", Timestamp: time.Date(2026, 10, 8, 17, 59, 0, 0, location), TotalTokens: 7, Generate: &generate, Failed: true, LatencyMS: 900},
			{EventKey: "a-off", APIGroupKey: "sk-exact-a", Timestamp: time.Date(2026, 10, 8, 18, 0, 0, 0, location), TotalTokens: 11, Generate: &generate, LatencyMS: 300},
			{EventKey: "b-warmup", APIGroupKey: "sk-exact-b", Timestamp: time.Date(2026, 10, 9, 8, 0, 0, 0, location), TotalTokens: 13, Generate: &warmup, LatencyMS: 400},
			{EventKey: "b-today", APIGroupKey: "sk-exact-b", Timestamp: time.Date(2026, 10, 9, 12, 0, 0, 0, location), TotalTokens: 17, Generate: &generate, LatencyMS: 500},
			{EventKey: "unsynced", APIGroupKey: "sk-never-synced", Timestamp: time.Date(2026, 10, 9, 12, 30, 0, 0, location), TotalTokens: 19, Generate: &generate, LatencyMS: 600},
		}
		if err := db.Create(&events).Error; err != nil {
			t.Fatal(err)
		}
		provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Now: func() time.Time { return now }, Location: location})
		if err != nil {
			t.Fatal(err)
		}
		return db, provider
	}
	load := func(t *testing.T, provider service.APIKeyTimeUsageProvider) *dto.APIKeyTimeUsageReport {
		t.Helper()
		report, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{StartDate: "2026-10-07", EndDate: "2026-10-09"})
		if err != nil {
			t.Fatal(err)
		}
		report.GeneratedAt = time.Time{}
		return report
	}

	_, rawProvider := build(t)
	raw := load(t, rawProvider)
	if raw.Summary.TotalTokens != 3+5+7+11+13+17 || raw.Unattributed.TotalTokens != 19 || raw.Unattributed.MessageCount != 1 {
		t.Fatalf("unexpected raw totals: summary=%+v unattributed=%+v", raw.Summary, raw.Unattributed)
	}
	// 08:59:59 属于前一统计日的非工作时段；09:00 起才是当天工作时段。
	if raw.Daily[0].AfterHoursTokens != 3 || raw.Daily[1].DaytimeTokens != 5+7 || raw.Daily[1].AfterHoursTokens != 11+13 || raw.Daily[2].DaytimeTokens != 17 {
		t.Fatalf("unexpected statistical-day split: %+v", raw.Daily)
	}
	if raw.Summary.MessageCount != 5 || raw.Summary.DaytimeLatencySamples != 2 || raw.Summary.AfterHoursLatencySamples != 2 {
		t.Fatalf("unexpected message or latency totals: %+v", raw.Summary)
	}

	partialDB, partialProvider := build(t)
	var firstEvents []entities.UsageEvent
	if err := partialDB.Order("id asc").Limit(3).Find(&firstEvents).Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.ApplyUsageAPIKeyTimeAggregationPage(context.Background(), partialDB, 0, firstEvents[len(firstEvents)-1].ID, apikeytime.BuildRows(firstEvents, now), now); err != nil {
		t.Fatal(err)
	}
	if partial := load(t, partialProvider); !reflect.DeepEqual(partial, raw) {
		t.Fatalf("partially aggregated report differs:\n raw=%+v\n got=%+v", raw, partial)
	}

	fullDB, fullProvider := build(t)
	if err := repository.AggregateUsageAPIKeyTimeStats(context.Background(), fullDB, now); err != nil {
		t.Fatal(err)
	}
	if full := load(t, fullProvider); !reflect.DeepEqual(full, raw) {
		t.Fatalf("fully aggregated report differs:\n raw=%+v\n got=%+v", raw, full)
	}
}

// 美东时区跨越夏令时结束日：统计日按当地墙钟划分，回拨的那一小时不会被拆到别的日期。
func TestAPIKeyTimeUsageUsesProjectZoneAcrossDaylightSavingChange(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	db := openUsageServiceTestDatabase(t)
	if err := db.Create(&entities.CPAAPIKey{APIKey: "sk-dst"}).Error; err != nil {
		t.Fatal(err)
	}
	// 2026-11-01 02:00 EDT 回拨到 01:00 EST；01:30 出现两次，都早于 09:00，属于 10-31 统计日。
	firstOneThirty := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)
	secondOneThirty := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)
	events := []entities.UsageEvent{
		{EventKey: "edt-work", APIGroupKey: "sk-dst", Timestamp: time.Date(2026, 10, 31, 9, 0, 0, 0, location), TotalTokens: 1},
		{EventKey: "edt-repeat", APIGroupKey: "sk-dst", Timestamp: firstOneThirty, TotalTokens: 2},
		{EventKey: "est-repeat", APIGroupKey: "sk-dst", Timestamp: secondOneThirty, TotalTokens: 4},
		{EventKey: "est-work", APIGroupKey: "sk-dst", Timestamp: time.Date(2026, 11, 1, 9, 0, 0, 0, location), TotalTokens: 8},
		{EventKey: "est-off", APIGroupKey: "sk-dst", Timestamp: time.Date(2026, 11, 1, 18, 0, 0, 0, location), TotalTokens: 16},
	}
	if err := db.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, location)
	provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Now: func() time.Time { return now }, Location: location})
	if err != nil {
		t.Fatal(err)
	}
	for _, aggregate := range []bool{false, true} {
		if aggregate {
			if err := repository.AggregateUsageAPIKeyTimeStats(context.Background(), db, now); err != nil {
				t.Fatal(err)
			}
		}
		report, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{StartDate: "2026-10-31", EndDate: "2026-11-01"})
		if err != nil {
			t.Fatal(err)
		}
		if report.Timezone != "America/New_York" || len(report.Daily) != 2 {
			t.Fatalf("unexpected report frame: %+v", report)
		}
		if report.Daily[0].DaytimeTokens != 1 || report.Daily[0].AfterHoursTokens != 2+4 || report.Daily[1].DaytimeTokens != 8 || report.Daily[1].AfterHoursTokens != 16 {
			t.Fatalf("aggregated=%v: DST split is wrong: %+v", aggregate, report.Daily)
		}
	}
}

// 加德满都是 UTC+05:45；15 分钟 bucket 仍与当地 15 分钟边界对齐。
func TestAPIKeyTimeUsageSupportsQuarterHourZoneOffsets(t *testing.T) {
	location, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	db := openUsageServiceTestDatabase(t)
	if err := db.Create(&entities.CPAAPIKey{APIKey: "sk-ktm"}).Error; err != nil {
		t.Fatal(err)
	}
	events := []entities.UsageEvent{
		{EventKey: "before", APIGroupKey: "sk-ktm", Timestamp: time.Date(2026, 10, 8, 9, 14, 59, 0, location), TotalTokens: 1},
		{EventKey: "start", APIGroupKey: "sk-ktm", Timestamp: time.Date(2026, 10, 8, 9, 15, 0, 0, location), TotalTokens: 2},
		{EventKey: "end", APIGroupKey: "sk-ktm", Timestamp: time.Date(2026, 10, 8, 17, 45, 0, 0, location), TotalTokens: 4},
	}
	if err := db.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, location)
	provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Now: func() time.Time { return now }, WorkStart: "09:15", WorkEnd: "17:45", Location: location})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.AggregateUsageAPIKeyTimeStats(context.Background(), db, now); err != nil {
		t.Fatal(err)
	}
	report, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{StartDate: "2026-10-07", EndDate: "2026-10-08"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Daily[0].AfterHoursTokens != 1 || report.Daily[1].DaytimeTokens != 2 || report.Daily[1].AfterHoursTokens != 4 {
		t.Fatalf("quarter-hour offset split is wrong: %+v", report.Daily)
	}
}

func TestAPIKeyTimeUsageTreatsNextDateAsCurrentStatisticalDay(t *testing.T) {
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	db := openUsageServiceTestDatabase(t)
	// 07:00 仍属于 10-08 统计日；浏览器可能已经按日历选择 10-09。
	now := time.Date(2026, 10, 9, 7, 0, 0, 0, location)
	provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Now: func() time.Time { return now }, Location: location})
	if err != nil {
		t.Fatal(err)
	}
	report, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{StartDate: "2026-10-09", EndDate: "2026-10-09"})
	if err != nil {
		t.Fatalf("a client one day ahead should get the current statistical day: %v", err)
	}
	if report.StartDate != "2026-10-08" || report.EndDate != "2026-10-08" || report.MaxDate != "2026-10-08" {
		t.Fatalf("unexpected clamp: %+v", report)
	}
	for _, query := range []dto.APIKeyTimeUsageQuery{
		{StartDate: "2026-10-10", EndDate: "2026-10-10"},
		{StartDate: "2026-07-10", EndDate: "2026-10-08"},
		{StartDate: "2026-10-08", EndDate: "2026-10-07"},
	} {
		if _, err := provider.GetAPIKeyTimeUsage(context.Background(), query); err != service.ErrInvalidAPIKeyTimeUsageQuery {
			t.Fatalf("expected %+v to be rejected, got %v", query, err)
		}
	}
}
