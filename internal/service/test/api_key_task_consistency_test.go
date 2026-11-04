package test

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/service"
	"cpa-usage-keeper/internal/service/dto"
	"gorm.io/gorm"
)

func TestAPIKeyTaskTimeSwitchSkipsTaskReadsAndPreservesLegacyReport(t *testing.T) {
	db := openUsageServiceTestDatabase(t)
	loc := withUsageServiceLocation(t, "Asia/Shanghai")
	key := entities.CPAAPIKey{APIKey: "task-switch-key"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 8, hour, minute, 0, 0, loc) }
	if err := db.Create(&[]entities.UsageEvent{
		{EventKey: "switch-first", APIGroupKey: key.APIKey, SessionID: "s", Timestamp: at(10, 0), TotalTokens: 12, LatencyMS: 60000},
		{EventKey: "switch-next", APIGroupKey: key.APIKey, SessionID: "s", Timestamp: at(10, 5), TotalTokens: 23, LatencyMS: 60000},
		{EventKey: "switch-off", APIGroupKey: key.APIKey, SessionID: "s", Timestamp: at(19, 0), TotalTokens: 34, LatencyMS: 30000},
		{EventKey: "switch-unsynced", APIGroupKey: "unsynced-switch-key", Timestamp: at(11, 0), TotalTokens: 45, LatencyMS: 15000},
	}).Error; err != nil {
		t.Fatal(err)
	}
	var taskReads int
	const callback = "test:count_switch_task_reads"
	if err := db.Callback().Row().After("gorm:row").Register(callback, func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		if strings.Contains(sql, "AS hot_first") || strings.Contains(sql, "GROUP BY TRIM(session_id)") || strings.Contains(sql, "events.timestamp ASC") {
			taskReads++
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Row().Remove(callback) })
	provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Location: loc, Now: func() time.Time { return at(20, 0).Add(24 * time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	query := dto.APIKeyTimeUsageQuery{StartDate: "2026-10-08", EndDate: "2026-10-08"}
	legacy, err := provider.GetAPIKeyTimeUsage(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if taskReads != 0 || legacy.TaskTimeEnabled || legacy.Summary.DaytimeTaskMS != 0 || legacy.Summary.TotalTokens != 69 {
		t.Fatalf("default must use legacy mode without task reads: reads=%d report=%+v", taskReads, legacy)
	}
	query.TaskTimeEnabled = "true"
	enabled, err := provider.GetAPIKeyTimeUsage(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if taskReads == 0 || !enabled.TaskTimeEnabled || enabled.Summary.DaytimeTaskMS != 360000 {
		t.Fatalf("switch did not enable task aggregation: reads=%d report=%+v", taskReads, enabled)
	}
	before := taskReads
	query.TaskTimeEnabled = "false"
	disabled, err := provider.GetAPIKeyTimeUsage(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if taskReads != before || !reflect.DeepEqual(legacy, disabled) {
		t.Fatalf("switching off must restore the default report and skip even cached task reads")
	}
	clearTasks := func(totals *dto.APIKeyTimeUsageTotals) { totals.DaytimeTaskMS, totals.AfterHoursTaskMS = 0, 0 }
	enabled.TaskTimeEnabled = false
	clearTasks(&enabled.Summary.APIKeyTimeUsageTotals)
	clearTasks(&enabled.Unattributed)
	for i := range enabled.Daily {
		clearTasks(&enabled.Daily[i].APIKeyTimeUsageTotals)
	}
	for i := range enabled.KeySummaries {
		clearTasks(&enabled.KeySummaries[i].APIKeyTimeUsageTotals)
		for j := range enabled.KeySummaries[i].Daily {
			clearTasks(&enabled.KeySummaries[i].Daily[j].APIKeyTimeUsageTotals)
		}
	}
	if !reflect.DeepEqual(legacy, enabled) {
		t.Fatal("task switch changed non-task report fields")
	}
	for _, value := range []string{"1", "0", "TRUE", "yes", " false"} {
		query.TaskTimeEnabled = value
		if _, err := provider.GetAPIKeyTimeUsage(context.Background(), query); err != service.ErrInvalidAPIKeyTimeUsageQuery {
			t.Fatalf("switch value %q should be rejected, got %v", value, err)
		}
	}
}

func TestAPIKeyTaskDayDoesNotDependOnSelectedRange(t *testing.T) {
	db := openUsageServiceTestDatabase(t)
	loc := withUsageServiceLocation(t, "Asia/Shanghai")
	key := entities.CPAAPIKey{APIKey: "review-key"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	root, child := "review-root", "review-child"
	events := []entities.UsageEvent{
		{EventKey: "old-child", APIGroupKey: key.APIKey, SessionID: child, ParentSessionID: &root, Timestamp: time.Date(2026, 10, 6, 10, 0, 0, 0, loc), LatencyMS: 60000},
		{EventKey: "root", APIGroupKey: key.APIKey, SessionID: root, Timestamp: time.Date(2026, 10, 8, 10, 0, 0, 0, loc), LatencyMS: 300000},
		{EventKey: "grandchild", APIGroupKey: key.APIKey, SessionID: "review-grandchild", ParentSessionID: &child, Timestamp: time.Date(2026, 10, 8, 10, 2, 0, 0, loc), LatencyMS: 240000},
	}
	if err := db.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Now: func() time.Time { return time.Date(2026, 10, 9, 20, 0, 0, 0, loc) }, Location: loc})
	if err != nil {
		t.Fatal(err)
	}
	daily := func(start string) int64 {
		report, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{TaskTimeEnabled: "true", StartDate: start, EndDate: "2026-10-08"})
		if err != nil {
			t.Fatal(err)
		}
		return report.Daily[len(report.Daily)-1].DaytimeTaskMS
	}
	short, wide := daily("2026-10-08"), daily("2026-10-06")
	if short != wide {
		t.Fatalf("same 2026-10-08: single-day=%d ms, wider-range=%d ms; want 360000 ms in both", short, wide)
	}
	if short != 360000 {
		t.Fatalf("both reports returned an incorrect task duration: %d", short)
	}
}

func TestAPIKeyTaskConcurrentFiltersShareOneColdRead(t *testing.T) {
	db := openUsageServiceTestDatabase(t)
	loc := withUsageServiceLocation(t, "Asia/Shanghai")
	key := entities.CPAAPIKey{APIKey: "concurrent-task-key"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 8, 10, 0, 0, 0, loc)
	if err := db.Create(&entities.UsageEvent{EventKey: "concurrent-task", APIGroupKey: key.APIKey, SessionID: "s", Timestamp: start, LatencyMS: 60000}).Error; err != nil {
		t.Fatal(err)
	}
	var rawLoads atomic.Int32
	const callback = "test:count_concurrent_task_streams"
	if err := db.Callback().Row().After("gorm:row").Register(callback, func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "events.timestamp ASC") {
			rawLoads.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Row().Remove(callback) })
	provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Now: func() time.Time { return start.Add(24 * time.Hour) }, Location: loc})
	if err != nil {
		t.Fatal(err)
	}
	begin := make(chan struct{})
	errors := make(chan error, 8)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-begin
			report, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{TaskTimeEnabled: "true", StartDate: "2026-10-08", EndDate: "2026-10-08", TaskIdleMinutes: []string{"5", "10", "30", "60"}[i%4]})
			if err == nil && report.Summary.DaytimeTaskMS != 60000 {
				err = fmt.Errorf("unexpected concurrent total: %d", report.Summary.DaytimeTaskMS)
			}
			errors <- err
		}(i)
	}
	close(begin)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Error(err)
		}
	}
	if got := rawLoads.Load(); got != 1 {
		t.Fatalf("concurrent idle changes triggered %d history reads", got)
	}
}

func TestAPIKeyTaskCacheReusesIdleOptionsAndReloadsNewAncestry(t *testing.T) {
	db := openUsageServiceTestDatabase(t)
	loc := withUsageServiceLocation(t, "Asia/Shanghai")
	key := entities.CPAAPIKey{APIKey: "cache-task-key"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	root, child := "cache-root", "cache-child"
	at := func(minute int) time.Time { return time.Date(2026, 10, 8, 10, minute, 0, 0, loc) }
	if err := db.Create(&[]entities.UsageEvent{
		{EventKey: "cache-root", APIGroupKey: key.APIKey, SessionID: root, Timestamp: at(0), LatencyMS: 300000},
		{EventKey: "cache-grandchild", APIGroupKey: key.APIKey, SessionID: "grandchild", ParentSessionID: &child, Timestamp: at(2), LatencyMS: 240000},
	}).Error; err != nil {
		t.Fatal(err)
	}
	var rawLoads atomic.Int32
	const callback = "test:count_task_streams"
	if err := db.Callback().Row().After("gorm:row").Register(callback, func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "events.timestamp ASC") {
			rawLoads.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Row().Remove(callback) })
	provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Now: func() time.Time { return time.Date(2026, 10, 9, 20, 0, 0, 0, loc) }, Location: loc})
	if err != nil {
		t.Fatal(err)
	}
	load := func(idle string, selected bool) int64 {
		query := dto.APIKeyTimeUsageQuery{TaskTimeEnabled: "true", StartDate: "2026-10-08", EndDate: "2026-10-08", TaskIdleMinutes: idle}
		if selected {
			query.APIKeyID = strconv.FormatInt(key.ID, 10)
		}
		report, err := provider.GetAPIKeyTimeUsage(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		return report.Summary.DaytimeTaskMS
	}
	for _, idle := range []string{"5", "10", "30", "60"} {
		if got := load(idle, idle == "30"); got != 540000 {
			t.Fatalf("idle %s: got %d", idle, got)
		}
	}
	if got := rawLoads.Load(); got != 1 {
		t.Fatalf("idle/key changes read the same history %d times", got)
	}
	// An ancestor arriving outside the selected range must invalidate all cached
	// options; an archived warmup can provide lineage without contributing time.
	warmup := false
	if err := db.Create(&entities.UsageEventArchive{ID: 100, SessionID: child, ParentSessionID: &root, Generate: &warmup, Timestamp: at(0).AddDate(0, 0, -95)}).Error; err != nil {
		t.Fatal(err)
	}
	if got := load("10", false); got != 360000 {
		t.Fatalf("new archived ancestry not reflected: %d", got)
	}
	if got := rawLoads.Load(); got != 2 {
		t.Fatalf("expected one reload for new ancestry, got %d", got)
	}
	if err := db.Create(&entities.UsageEvent{EventKey: "cache-new", APIGroupKey: key.APIKey, SessionID: root, Timestamp: at(7), LatencyMS: 60000}).Error; err != nil {
		t.Fatal(err)
	}
	if got := load("10", false); got != 480000 {
		t.Fatalf("new usage not reflected: %d", got)
	}
}

func TestAPIKeyTaskStreamOrdersMixedUTCFractions(t *testing.T) {
	db := openUsageServiceTestDatabase(t)
	loc := withUsageServiceLocation(t, "UTC")
	key := entities.CPAAPIKey{APIKey: "fraction-task-key"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 8, 10, 0, 0, 0, loc)
	// Lexical order visits 10:05:00.001Z before 10:05:00Z. The latter
	// request must still bridge the exact five-minute idle boundary.
	if err := db.Create(&[]entities.UsageEvent{
		{EventKey: "fraction-first", APIGroupKey: key.APIKey, SessionID: "s", Timestamp: start},
		{EventKey: "fraction-whole", APIGroupKey: key.APIKey, SessionID: "s", Timestamp: start.Add(5 * time.Minute)},
		{EventKey: "fraction-part", APIGroupKey: key.APIKey, SessionID: "s", Timestamp: start.Add(5*time.Minute + time.Millisecond), LatencyMS: 1000},
	}).Error; err != nil {
		t.Fatal(err)
	}
	provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Now: func() time.Time { return start.Add(24 * time.Hour) }, Location: loc})
	if err != nil {
		t.Fatal(err)
	}
	report, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{TaskTimeEnabled: "true", StartDate: "2026-10-08", EndDate: "2026-10-08", TaskIdleMinutes: "5"})
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Summary.DaytimeTaskMS; got != 301001 {
		t.Fatalf("mixed timestamp precision broke task continuity: %d", got)
	}
}

func TestAPIKeyTaskDayDoesNotDependOnKeyFilter(t *testing.T) {
	db := openUsageServiceTestDatabase(t)
	loc := withUsageServiceLocation(t, "Asia/Shanghai")
	keys := []entities.CPAAPIKey{{APIKey: "review-key-a"}, {APIKey: "review-key-b"}}
	if err := db.Create(&keys).Error; err != nil {
		t.Fatal(err)
	}
	root, child := "review-root", "review-child"
	at := func(min int) time.Time { return time.Date(2026, 10, 8, 10, min, 0, 0, loc) }
	events := []entities.UsageEvent{
		{EventKey: "child-key-a", APIGroupKey: keys[0].APIKey, SessionID: child, ParentSessionID: &root, Timestamp: at(0), LatencyMS: 60000},
		{EventKey: "root-key-b", APIGroupKey: keys[1].APIKey, SessionID: root, Timestamp: at(0), LatencyMS: 300000},
		{EventKey: "grandchild-key-b", APIGroupKey: keys[1].APIKey, SessionID: "review-grandchild", ParentSessionID: &child, Timestamp: at(2), LatencyMS: 240000},
	}
	if err := db.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Now: func() time.Time { return time.Date(2026, 10, 9, 20, 0, 0, 0, loc) }, Location: loc})
	if err != nil {
		t.Fatal(err)
	}
	amount := func(filter string) int64 {
		report, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{TaskTimeEnabled: "true", StartDate: "2026-10-08", EndDate: "2026-10-08", APIKeyID: filter})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range report.KeySummaries {
			if row.Key.ID == strconv.FormatInt(keys[1].ID, 10) {
				return row.DaytimeTaskMS
			}
		}
		t.Fatal("missing key B")
		return 0
	}
	all, single := amount(""), amount(strconv.FormatInt(keys[1].ID, 10))
	if all != single {
		t.Fatalf("same key B/day: all-keys=%d ms, selected-key=%d ms; want 360000 ms in both", all, single)
	}
}
