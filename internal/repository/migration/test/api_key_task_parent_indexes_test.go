package test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/repository/migration"
	"gorm.io/gorm"
)

func TestAPIKeyTaskParentIndexesFreshAndUpgrade(t *testing.T) {
	for _, fresh := range []bool{true, false} {
		t.Run(fmt.Sprintf("fresh=%t", fresh), func(t *testing.T) {
			var db *gorm.DB
			if fresh {
				var err error
				db, err = repository.OpenDatabase(config.Config{SQLitePath: filepath.Join(t.TempDir(), "tasks.db")})
				if err != nil {
					t.Fatal(err)
				}
				closeMigrationTestDatabase(t, db)
			} else {
				db = openUnmigratedTestDatabase(t)
				for _, table := range []string{"usage_events", "usage_events_archive"} {
					if err := db.Exec("CREATE TABLE " + table + "(id INTEGER PRIMARY KEY, session_id TEXT, parent_session_id TEXT)").Error; err != nil {
						t.Fatal(err)
					}
					if err := db.Exec("INSERT INTO " + table + " VALUES(1,'root',NULL),(2,'child','root')").Error; err != nil {
						t.Fatal(err)
					}
				}
				if err := migration.MarkAllAsApplied(db); err != nil {
					t.Fatal(err)
				}
			}
			const version = "20261104_api_key_task_parent_indexes"
			for pass := 0; pass < 2; pass++ {
				if err := db.Exec("DELETE FROM schema_migrations WHERE version = ?", version).Error; err != nil {
					t.Fatal(err)
				}
				if err := migration.Run(db); err != nil {
					t.Fatal(err)
				}
				for _, table := range []string{"usage_events", "usage_events_archive"} {
					name := "idx_" + table + "_task_parent"
					var indexSQL string
					if err := db.Raw("SELECT sql FROM sqlite_master WHERE name = ?", name).Scan(&indexSQL).Error; err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(indexSQL, "(`session_id`,`parent_session_id`,`id`) WHERE parent_session_id IS NOT NULL AND parent_session_id <> '' AND session_id <> ''") {
						t.Fatalf("unexpected index: %s", indexSQL)
					}
					var plan []struct{ Detail string }
					if err := db.Raw("EXPLAIN QUERY PLAN SELECT id, session_id, parent_session_id FROM " + table + " WHERE parent_session_id IS NOT NULL AND parent_session_id <> '' AND session_id <> ''").Scan(&plan).Error; err != nil {
						t.Fatal(err)
					}
					if len(plan) != 1 || !strings.Contains(plan[0].Detail, "COVERING INDEX "+name) {
						t.Fatalf("parent history did not use covering index: %+v", plan)
					}
					if !fresh {
						var count int64
						if err := db.Table(table).Count(&count).Error; err != nil || count != 2 {
							t.Fatalf("migration changed %s data: %d %v", table, count, err)
						}
					}
				}
			}
		})
	}
}
