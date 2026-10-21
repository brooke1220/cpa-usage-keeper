package test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"cpa-usage-keeper/internal/apikeytime"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/repository/migration"
	"gorm.io/gorm"
)

const (
	usageAPIKeyTimeCheckpointMigrationVersion = "20261012_usage_api_key_time_checkpoint"
	usageAPIKeyTimeStatsMigrationVersion      = "20261012_usage_api_key_time_stats"
	cpaAPIKeyAliasSourceMigrationVersion      = "20261012_cpa_api_key_alias_source"
)

// legacyUsageAggregationCheckpoint 复刻 20261012 之前的表结构：CHECK 只允许三种名称。
type legacyUsageAggregationCheckpoint struct {
	Name                       string     `gorm:"type:text;primaryKey;check:chk_usage_aggregation_checkpoints_name,name IN ('overview','activity','latency')"`
	LastAggregatedUsageEventID int64      `gorm:"not null;default:0"`
	StatsUpdatedAt             *time.Time `gorm:"serializer:storageTime"`
	CreatedAt                  time.Time  `gorm:"serializer:storageTime;not null"`
	UpdatedAt                  time.Time  `gorm:"serializer:storageTime;not null"`
}

func (legacyUsageAggregationCheckpoint) TableName() string {
	return "usage_aggregation_checkpoints"
}

func prepareUsageAPIKeyTimeMigrationDatabase(t *testing.T, now time.Time, events []entities.UsageEvent) *gorm.DB {
	t.Helper()
	db := openUnmigratedTestDatabase(t)
	db.NowFunc = func() time.Time { return now }
	if err := db.AutoMigrate(&entities.UsageEvent{}, &legacyUsageAggregationCheckpoint{}); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	statsAt := now.Add(-time.Hour)
	if err := db.Create(&[]legacyUsageAggregationCheckpoint{
		{Name: "overview", LastAggregatedUsageEventID: 11, StatsUpdatedAt: &statsAt, CreatedAt: now, UpdatedAt: now},
		{Name: "activity", LastAggregatedUsageEventID: 22, CreatedAt: now, UpdatedAt: now},
		{Name: "latency", LastAggregatedUsageEventID: 33, CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("seed legacy checkpoints: %v", err)
	}
	if len(events) > 0 {
		if err := db.CreateInBatches(&events, 200).Error; err != nil {
			t.Fatalf("seed events: %v", err)
		}
	}
	if err := migration.MarkAllAsApplied(db); err != nil {
		t.Fatalf("mark migrations applied: %v", err)
	}
	for _, version := range []string{usageAPIKeyTimeCheckpointMigrationVersion, usageAPIKeyTimeStatsMigrationVersion} {
		if err := db.Exec("DELETE FROM schema_migrations WHERE version = ?", version).Error; err != nil {
			t.Fatalf("mark %s pending: %v", version, err)
		}
	}
	return db
}

func TestUsageAPIKeyTimeMigrationRebuildsCheckpointConstraintWithoutLosingCursors(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.Local)
	db := prepareUsageAPIKeyTimeMigrationDatabase(t, now, nil)
	// 旧 CHECK 必须真的拒绝新名称，否则本测试没有覆盖重建路径。
	if err := db.Exec("INSERT INTO usage_aggregation_checkpoints (name, last_aggregated_usage_event_id, created_at, updated_at) VALUES ('api_key_time', 0, '2026-10-12', '2026-10-12')").Error; err == nil {
		t.Fatal("legacy CHECK unexpectedly accepted api_key_time")
	}
	if err := migration.Run(db); err != nil {
		t.Fatalf("run api key time migrations: %v", err)
	}
	var rows []entities.UsageAggregationCheckpoint
	if err := db.Order("name asc").Find(&rows).Error; err != nil {
		t.Fatalf("load checkpoints: %v", err)
	}
	cursors := map[entities.UsageAggregationCheckpointName]int64{}
	for _, row := range rows {
		cursors[row.Name] = row.LastAggregatedUsageEventID
	}
	want := map[entities.UsageAggregationCheckpointName]int64{"activity": 22, "api_key_time": 0, "latency": 33, "overview": 11}
	if !reflect.DeepEqual(cursors, want) {
		t.Fatalf("checkpoint cursors changed: got %v want %v", cursors, want)
	}
	var overview entities.UsageAggregationCheckpoint
	if err := db.Where("name = ?", "overview").Take(&overview).Error; err != nil || overview.StatsUpdatedAt == nil || !overview.StatsUpdatedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("overview stats_updated_at was not preserved: %+v err=%v", overview, err)
	}
	if err := db.Exec("INSERT INTO usage_aggregation_checkpoints (name, last_aggregated_usage_event_id, created_at, updated_at) VALUES ('bogus', 0, '2026-10-12', '2026-10-12')").Error; err == nil {
		t.Fatal("rebuilt CHECK must still reject unknown checkpoint names")
	}
	if !db.Migrator().HasTable(&entities.UsageAPIKeyTimeStat{}) {
		t.Fatal("usage_api_key_time_stats was not created")
	}
	if db.Migrator().HasTable("usage_aggregation_checkpoints_rebuild") {
		t.Fatal("temporary rebuild table was left behind")
	}
}

func TestUsageAPIKeyTimeMigrationBackfillMatchesRuntimeAggregation(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.Local)
	generate := true
	events := make([]entities.UsageEvent, 0, 1205)
	for id := int64(1); id <= 1205; id++ {
		events = append(events, entities.UsageEvent{
			ID: id, EventKey: fmt.Sprintf("api-key-time-%d", id), APIGroupKey: fmt.Sprintf("key-%d", id%3),
			Timestamp: now.Add(-time.Duration(id) * 7 * time.Minute), TotalTokens: id, Generate: &generate, LatencyMS: id * 10, Failed: id%10 == 0,
		})
	}
	db := prepareUsageAPIKeyTimeMigrationDatabase(t, now, events)
	if err := migration.Run(db); err != nil {
		t.Fatalf("run api key time migrations: %v", err)
	}
	var checkpoint entities.UsageAggregationCheckpoint
	if err := db.Where("name = ?", entities.UsageAggregationCheckpointAPIKeyTime).Take(&checkpoint).Error; err != nil || checkpoint.LastAggregatedUsageEventID != 1205 {
		t.Fatalf("backfill did not reach the target: %+v err=%v", checkpoint, err)
	}
	// 运行时数据库用同一批事件走 BuildRows + Apply，两边的 15 分钟行必须完全一致。
	runtimeDB := openUnmigratedTestDatabase(t)
	if err := runtimeDB.AutoMigrate(&entities.UsageAggregationCheckpoint{}, &entities.UsageAPIKeyTimeStat{}); err != nil {
		t.Fatalf("create runtime schema: %v", err)
	}
	cursor := int64(0)
	for start := 0; start < len(events); start += 1000 {
		page := events[start:min(start+1000, len(events))]
		next := page[len(page)-1].ID
		if err := repository.ApplyUsageAPIKeyTimeAggregationPage(context.Background(), runtimeDB, cursor, next, apikeytime.BuildRows(page, now), now); err != nil {
			t.Fatalf("apply runtime page: %v", err)
		}
		cursor = next
	}
	if got, want := loadUsageAPIKeyTimeRows(t, db), loadUsageAPIKeyTimeRows(t, runtimeDB); !reflect.DeepEqual(got, want) {
		t.Fatalf("migration rows differ from runtime rows:\n migration=%+v\n runtime=%+v", got, want)
	}
}

func TestUsageAPIKeyTimeMigrationBackfillsWithoutPricingColumns(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.Local)
	db := prepareUsageAPIKeyTimeMigrationDatabase(t, now, []entities.UsageEvent{{
		ID: 1, EventKey: "legacy-api-key-time", APIGroupKey: "sk-legacy", Timestamp: now, TotalTokens: 42,
	}})
	for _, column := range []string{"cost_usd", "cost_available"} {
		if err := db.Migrator().DropColumn(&entities.UsageEvent{}, column); err != nil {
			t.Fatalf("remove unpublished pricing column %s: %v", column, err)
		}
	}
	if err := migration.Run(db); err != nil {
		t.Fatalf("backfill legacy events without pricing columns: %v", err)
	}
	rows := loadUsageAPIKeyTimeRows(t, db)
	if len(rows) != 1 || rows[0].TotalTokens != 42 {
		t.Fatalf("legacy usage was not backfilled: %+v", rows)
	}
}

func TestCPAAPIKeyAliasSourceMigrationAddsEmptySource(t *testing.T) {
	db := openUnmigratedTestDatabase(t)
	if err := db.Exec("CREATE TABLE cpa_api_keys (id integer PRIMARY KEY AUTOINCREMENT, api_key text, display_key text, key_alias text, local_ranking_avatar_id integer, is_deleted numeric, last_synced_at datetime, created_at datetime, updated_at datetime)").Error; err != nil {
		t.Fatalf("create legacy cpa_api_keys: %v", err)
	}
	if err := db.Exec("INSERT INTO cpa_api_keys (api_key, display_key, key_alias, is_deleted) VALUES ('sk-legacy', 'sk-***', 'Legacy', 0)").Error; err != nil {
		t.Fatalf("seed legacy key: %v", err)
	}
	runOnlyMigration(t, db, cpaAPIKeyAliasSourceMigrationVersion)
	var source string
	if err := db.Raw("SELECT key_alias_source FROM cpa_api_keys WHERE api_key = 'sk-legacy'").Scan(&source).Error; err != nil || source != "" {
		t.Fatalf("expected an empty source for legacy rows, got %q err=%v", source, err)
	}
}

func loadUsageAPIKeyTimeRows(t *testing.T, db *gorm.DB) []entities.UsageAPIKeyTimeStat {
	t.Helper()
	var rows []entities.UsageAPIKeyTimeStat
	if err := db.Order("bucket_start asc, api_group_key asc").Find(&rows).Error; err != nil {
		t.Fatalf("load api key time rows: %v", err)
	}
	for index := range rows {
		rows[index].ID = 0
		rows[index].CreatedAt = time.Time{}
		rows[index].UpdatedAt = time.Time{}
	}
	return rows
}
