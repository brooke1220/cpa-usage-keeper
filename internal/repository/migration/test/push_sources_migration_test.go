package test

import (
	"path/filepath"
	"testing"

	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
	"gorm.io/gorm"
)

// openPushSourcesTestDB 打开带完整生产结构的库；重复打开会补跑被清除记录的迁移。
func openPushSourcesTestDB(t *testing.T, dbPath string) *gorm.DB {
	t.Helper()
	db, err := repository.OpenDatabase(config.Config{SQLitePath: dbPath})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	return db
}

func closePushSourcesTestDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
}

// prepareLegacyPushDatabase 模拟只跑过 20261026_push_ingest 的旧库：删掉新列、清除迁移记录并写入旧数据。
// 必须在同一次打开里完成，否则下一次打开会先补跑迁移，回填就落在空表上。
func prepareLegacyPushDatabase(t *testing.T, dbPath string, pulled bool) {
	t.Helper()
	db := openPushSourcesTestDB(t, dbPath)
	defer closePushSourcesTestDB(t, db)
	for _, column := range []string{"pull_seen", "push_seen"} {
		if err := db.Exec("ALTER TABLE cpa_api_keys DROP COLUMN " + column).Error; err != nil {
			t.Fatalf("drop %s: %v", column, err)
		}
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version = ?", "20261027_push_sources").Error; err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sk-pushed-only", "sk-pending-push", "sk-pulled"} {
		if err := db.Exec("INSERT INTO cpa_api_keys (api_key, display_key, key_alias, key_alias_source, is_deleted) VALUES (?, ?, '', '', 0)", key, key).Error; err != nil {
			t.Fatal(err)
		}
	}
	events := []struct{ key, sourceID string }{{"sk-pushed-only", "cpa-one"}}
	if pulled {
		events = append(events, struct{ key, sourceID string }{"sk-pulled", ""})
	}
	for i, event := range events {
		if err := db.Exec("INSERT INTO usage_events (event_key, api_group_key, model, timestamp, source_id, delivery_event_id, created_at) VALUES (?, ?, 'm', '2026-10-26T10:00:00+08:00', ?, '', '2026-10-26T10:00:00+08:00')", "legacy-"+string(rune('a'+i)), event.key, event.sourceID).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 尚未处理的推送只在 inbox 原始 JSON 里出现。
	if err := db.Exec("INSERT INTO redis_usage_inboxes (source, source_id, delivery_event_id, message_hash, raw_message, status, popped_at) VALUES ('https_push:cpa-one', 'cpa-one', 'd-1', 'h', ?, 'pending', '2026-10-26T10:00:00+08:00')", `{"api_key":" sk-pending-push "}`).Error; err != nil {
		t.Fatal(err)
	}
}

func keyProvenance(t *testing.T, dbPath string) map[string][2]bool {
	t.Helper()
	db := openPushSourcesTestDB(t, dbPath)
	defer closePushSourcesTestDB(t, db)
	var rows []entities.CPAAPIKey
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	result := make(map[string][2]bool, len(rows))
	for _, row := range rows {
		result[row.APIKey] = [2]bool{row.PullSeen, row.PushSeen}
	}
	return result
}

func TestPushSourcesMigrationMarksLegacyPushOnlyKeys(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "push-only.db")
	prepareLegacyPushDatabase(t, dbPath, false)
	got := keyProvenance(t, dbPath)
	// 旧库从未拉取：推送 Key 撤销登录资格，避免首轮拉取同步前可被用于 Key 登录、同步后又被删除。
	if got["sk-pushed-only"] != [2]bool{false, true} || got["sk-pending-push"] != [2]bool{false, true} {
		t.Fatalf("legacy pushed keys kept pull provenance: %+v", got)
	}
	if got["sk-pulled"] != [2]bool{true, false} {
		t.Fatalf("key without push usage changed: %+v", got)
	}
}

func TestPushSourcesMigrationKeepsPullProvenanceOnPullDatabases(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pull.db")
	prepareLegacyPushDatabase(t, dbPath, true)
	got := keyProvenance(t, dbPath)
	// 拉取库无法从历史判断 Key 是否仍在 CPA：保留拉取标记，交给下一轮拉取同步校正。
	if got["sk-pushed-only"] != [2]bool{true, true} || got["sk-pulled"] != [2]bool{true, false} {
		t.Fatalf("pull database provenance wrong: %+v", got)
	}
}

func TestPushSourcesMigrationIsRerunnable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rerun.db")
	db := openPushSourcesTestDB(t, dbPath)
	if err := db.Exec("DELETE FROM schema_migrations WHERE version = ?", "20261027_push_sources").Error; err != nil {
		t.Fatal(err)
	}
	closePushSourcesTestDB(t, db)
	db = openPushSourcesTestDB(t, dbPath)
	defer closePushSourcesTestDB(t, db)
	if !db.Migrator().HasTable(&entities.PushSource{}) || !db.Migrator().HasColumn(&entities.CPAAPIKey{}, "PullSeen") {
		t.Fatal("rerun lost push source schema")
	}
}
