package test

import (
	"context"
	"testing"
	"time"

	"cpa-usage-keeper/internal/service"
	"cpa-usage-keeper/internal/service/dto"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Reproduce the review's one-million-event workload. Cold includes all four idle
// options; warm measures full reports while changing idle and key selection.
func BenchmarkAPIKeyTaskReportMillion(b *testing.B) {
	previous := time.Local
	time.Local = time.UTC
	b.Cleanup(func() { time.Local = previous })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		b.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = sqlDB.Close() })
	for _, statement := range []string{
		"CREATE TABLE cpa_api_keys(id INTEGER PRIMARY KEY,api_key TEXT,display_key TEXT,key_alias TEXT,is_deleted INTEGER)",
		"INSERT INTO cpa_api_keys VALUES(1,'review-key','masked','review',0)",
		"CREATE UNIQUE INDEX review_key_index ON cpa_api_keys(api_key)",
		"CREATE TABLE usage_events(id INTEGER PRIMARY KEY,api_group_key TEXT,session_id TEXT,parent_session_id TEXT,timestamp TEXT,latency_ms INTEGER,generate INTEGER,total_tokens INTEGER,failed INTEGER)",
		"CREATE INDEX review_time_index ON usage_events(timestamp)",
		"CREATE TABLE usage_events_archive(id INTEGER PRIMARY KEY,session_id TEXT,parent_session_id TEXT)",
		"CREATE INDEX review_hot_parents ON usage_events(session_id,parent_session_id,id) WHERE parent_session_id IS NOT NULL AND parent_session_id <> '' AND session_id <> ''",
		"CREATE INDEX review_archive_parents ON usage_events_archive(session_id,parent_session_id,id) WHERE parent_session_id IS NOT NULL AND parent_session_id <> '' AND session_id <> ''",
		"CREATE TABLE usage_api_key_time_stats(api_group_key TEXT,bucket_start TEXT,total_tokens INTEGER,message_count INTEGER,latency_ms INTEGER,latency_samples INTEGER)",
		"CREATE TABLE usage_aggregation_checkpoints(name TEXT,last_aggregated_usage_event_id INTEGER)",
		"INSERT INTO usage_aggregation_checkpoints VALUES('api_key_time',1000000)",
		"WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1000000) INSERT INTO usage_events SELECT x,'review-key','session-' || (x%1000),NULL,strftime('%Y-%m-%dT%H:%M:%SZ','2026-09-10T01:00:00Z','+' || (x*2) || ' seconds'),1000,1,1,0 FROM n",
		"INSERT INTO usage_api_key_time_stats SELECT 'review-key',strftime('%Y-%m-%dT%H:%M:00.000000000Z',timestamp,'-' || (CAST(strftime('%M',timestamp) AS INTEGER)%15) || ' minutes'),COUNT(*),COUNT(*),SUM(latency_ms),COUNT(*) FROM usage_events GROUP BY 2",
	} {
		if err := db.Exec(statement).Error; err != nil {
			b.Fatal(err)
		}
	}
	newProvider := func() service.APIKeyTimeUsageProvider {
		provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Now: func() time.Time { return time.Date(2026, 10, 10, 20, 0, 0, 0, time.UTC) }, Location: time.UTC})
		if err != nil {
			b.Fatal(err)
		}
		return provider
	}
	query := dto.APIKeyTimeUsageQuery{TaskTimeEnabled: "true", StartDate: "2026-09-10", EndDate: "2026-10-09"}
	b.Run("cold", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			report, err := newProvider().GetAPIKeyTimeUsage(context.Background(), query)
			if err != nil || report.Summary.DaytimeTaskMS == 0 {
				b.Fatalf("cold report: %v", err)
			}
		}
	})
	provider := newProvider()
	if _, err := provider.GetAPIKeyTimeUsage(context.Background(), query); err != nil {
		b.Fatal(err)
	}
	b.Run("warm_four_reports", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, idle := range []string{"5", "10", "30", "60"} {
				query.TaskIdleMinutes = idle
				query.APIKeyID = "1"
				report, err := provider.GetAPIKeyTimeUsage(context.Background(), query)
				if err != nil || report.Summary.DaytimeTaskMS == 0 {
					b.Fatalf("warm report: %v", err)
				}
			}
		}
	})
}
