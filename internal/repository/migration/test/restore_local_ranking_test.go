package test

import (
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository/migration"
	"testing"
	"time"
)

func TestRestoreLocalRankingAfterV2RemovalIsAdditiveAndIdempotent(t *testing.T) {
	db := openUnmigratedTestDatabase(t)
	// Reproduce a deployed v2 database whose removal migration already ran.
	if err := db.Exec("CREATE TABLE cpa_api_keys (id integer PRIMARY KEY, api_key text, display_key text, key_alias text, key_alias_source text)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO cpa_api_keys VALUES (42, 'sk-private', 'sk-***', 'Existing name', 'manual')").Error; err != nil {
		t.Fatal(err)
	}
	if err := migration.MarkAllAsApplied(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version = ?", "20261021_restore_local_ranking").Error; err != nil {
		t.Fatal(err)
	}
	if err := migration.Run(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&entities.LocalRankingPeriodStat{}) || !db.Migrator().HasColumn(&entities.CPAAPIKey{}, "LocalRankingAvatarID") {
		t.Fatal("local ranking schema not restored")
	}
	var key struct {
		KeyAlias       string
		KeyAliasSource string
	}
	if err := db.Table("cpa_api_keys").Where("id = 42").Scan(&key).Error; err != nil {
		t.Fatal(err)
	}
	if key.KeyAlias != "Existing name" || key.KeyAliasSource != "manual" {
		t.Fatalf("key was changed: %+v", key)
	}
	stat := entities.LocalRankingPeriodStat{PeriodKind: entities.LocalRankingPeriodDay, PeriodKey: "2026-10-21", APIKeyID: 42, TotalTokens: 123, UpdatedAt: time.Now()}
	if err := db.Create(&stat).Error; err != nil {
		t.Fatal(err)
	}
	if err := migration.Run(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&entities.LocalRankingPeriodStat{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("restart lost ranking data: count=%d err=%v", count, err)
	}
}
