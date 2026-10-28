package test

import (
	"strings"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
	repositorydto "cpa-usage-keeper/internal/repository/dto"

	"gorm.io/gorm"
)

const usageEventSourceIDIndexName = "idx_usage_events_source_id_timestamp_id"

type capturedUsageEventQuery struct {
	sql  string
	vars []any
}

// captureUsageEventSourceQueries 记录带参数的原始 SQL，EXPLAIN 时与生产查询使用同样的绑定参数。
func captureUsageEventSourceQueries(t *testing.T, db *gorm.DB) *[]capturedUsageEventQuery {
	t.Helper()
	captured := &[]capturedUsageEventQuery{}
	capture := func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		if strings.Contains(sql, "usage_events") && strings.Contains(sql, "source_id = ?") {
			*captured = append(*captured, capturedUsageEventQuery{sql: sql, vars: append([]any(nil), tx.Statement.Vars...)})
		}
	}
	if err := db.Callback().Query().After("gorm:query").Register("test:capture_source_query", capture); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Row().After("gorm:row").Register("test:capture_source_row", capture); err != nil {
		t.Fatal(err)
	}
	return captured
}

func TestUsageEventSourceFilterUsesPushedSourceIndex(t *testing.T) {
	db := openTestDatabase(t)
	base := time.Date(2026, 10, 28, 9, 0, 0, 0, time.Local)
	events := []entities.UsageEvent{
		{EventKey: "local", APIGroupKey: "key", Model: "model-a", AuthIndex: "local", Timestamp: base},
		{EventKey: "pushed-old", APIGroupKey: "key", Model: "model-a", AuthIndex: "push.a", SourceID: "cpa-one", Timestamp: base.Add(time.Minute)},
		{EventKey: "pushed-new", APIGroupKey: "key", Model: "model-a", AuthIndex: "push.b", SourceID: "cpa-one", Timestamp: base.Add(2 * time.Minute)},
		{EventKey: "other-cpa", APIGroupKey: "key", Model: "model-a", AuthIndex: "push.c", SourceID: "cpa-two", Timestamp: base.Add(3 * time.Minute)},
	}
	if err := db.Create(requestEventFixtureWithZeroFees(events)).Error; err != nil {
		t.Fatal(err)
	}
	captured := captureUsageEventSourceQueries(t, db)
	start, end := base.Add(-time.Hour), base.Add(time.Hour)
	page, err := repository.ListUsageEventsWithFilter(db, repositorydto.UsageQueryFilter{SourceID: "cpa-one", StartTime: &start, EndTime: &end, PageSize: 20}, emptyPricingSnapshotForTest())
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalCount != 2 || len(page.Events) != 2 || page.Events[0].SourceID != "cpa-one" || page.Events[0].Timestamp.Before(page.Events[1].Timestamp) {
		t.Fatalf("source filter returned wrong rows: %+v", page)
	}
	if len(*captured) != 2 {
		t.Fatalf("expected count and page queries, captured %d", len(*captured))
	}
	for _, query := range *captured {
		var plan []struct {
			Detail string `gorm:"column:detail"`
		}
		if err := db.Raw("EXPLAIN QUERY PLAN "+query.sql, query.vars...).Scan(&plan).Error; err != nil {
			t.Fatal(err)
		}
		details := make([]string, 0, len(plan))
		for _, row := range plan {
			details = append(details, row.Detail)
		}
		joined := strings.Join(details, "\n")
		if !strings.Contains(joined, usageEventSourceIDIndexName) || strings.Contains(joined, "TEMP B-TREE") {
			t.Fatalf("source filter does not use %s without sorting:\n%s\n%s", usageEventSourceIDIndexName, query.sql, joined)
		}
	}
	var indexSQL string
	if err := db.Raw("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?", usageEventSourceIDIndexName).Scan(&indexSQL).Error; err != nil {
		t.Fatal(err)
	}
	// 只为推送行建索引，纯拉取部署的写入不增加索引维护。
	if !strings.Contains(indexSQL, "WHERE source_id <> ''") {
		t.Fatalf("expected partial pushed-source index, got %q", indexSQL)
	}
}
