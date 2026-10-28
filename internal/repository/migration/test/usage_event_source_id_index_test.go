package test

import (
	"path/filepath"
	"strings"
	"testing"

	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/repository/migration"

	"gorm.io/gorm"
)

const (
	usageEventSourceIDIndexName      = "idx_usage_events_source_id_timestamp_id"
	usageEventSourceIDIndexMigration = "20261028_usage_event_source_id_index"
)

func TestUsageEventSourceIDIndexMigration(t *testing.T) {
	for _, test := range []struct {
		name  string
		fresh bool
	}{{name: "fresh database", fresh: true}, {name: "upgraded database"}} {
		t.Run(test.name, func(t *testing.T) {
			var db *gorm.DB
			if test.fresh {
				var err error
				db, err = repository.OpenDatabase(config.Config{SQLitePath: filepath.Join(t.TempDir(), "app.db")})
				if err != nil {
					t.Fatalf("open database: %v", err)
				}
				closeMigrationTestDatabase(t, db)
			} else {
				db = openUnmigratedTestDatabase(t)
				// 旧库只保留本次迁移需要的列，历史版本标记为完成。
				if err := db.Exec(`CREATE TABLE usage_events (
					id INTEGER PRIMARY KEY,
					timestamp DATETIME NOT NULL,
					source_id TEXT NOT NULL DEFAULT ''
				)`).Error; err != nil {
					t.Fatalf("create usage_events table: %v", err)
				}
				if err := db.Exec(`INSERT INTO usage_events (id, timestamp, source_id) VALUES (1, '2026-10-28T09:00:00Z', ''), (2, '2026-10-28T09:01:00Z', 'cpa-one')`).Error; err != nil {
					t.Fatalf("seed usage events: %v", err)
				}
				if err := migration.MarkAllAsApplied(db); err != nil {
					t.Fatalf("mark historical migrations applied: %v", err)
				}
			}
			// 第二轮显式重跑，验证索引已存在时安全且不会重复记录版本。
			for pass := 0; pass < 2; pass++ {
				if err := db.Table("schema_migrations").Where("version = ?", usageEventSourceIDIndexMigration).Delete(nil).Error; err != nil {
					t.Fatalf("make index migration pending: %v", err)
				}
				if err := migration.Run(db); err != nil {
					t.Fatalf("Run returned error: %v", err)
				}
				var indexSQL string
				if err := db.Raw("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?", usageEventSourceIDIndexName).Scan(&indexSQL).Error; err != nil {
					t.Fatalf("load index: %v", err)
				}
				if !strings.Contains(indexSQL, "(`source_id`,`timestamp`,`id`) WHERE source_id <> ''") {
					t.Fatalf("expected partial source/time index, got %q", indexSQL)
				}
				var count int64
				if err := db.Table("schema_migrations").Where("version = ?", usageEventSourceIDIndexMigration).Count(&count).Error; err != nil || count != 1 {
					t.Fatalf("expected one migration record, got %d, error %v", count, err)
				}
			}
		})
	}
}
