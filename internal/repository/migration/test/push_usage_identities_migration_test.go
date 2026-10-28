package test

import (
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository/migration"
)

func TestPushUsageIdentityMigrationPreservesExistingHistoryAndRanking(t *testing.T) {
	db := openUnmigratedTestDatabase(t)
	if err := db.AutoMigrate(&entities.UsageEvent{}, &entities.CPAAPIKey{}, &entities.LocalRankingPeriodStat{}); err != nil {
		t.Fatal(err)
	}
	if err := migration.MarkAllAsApplied(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version IN ?", []string{"20261027_push_usage_identities", pushIdentityTimeMigration}).Error; err != nil {
		t.Fatal(err)
	}
	avatar := uint8(7)
	key := entities.CPAAPIKey{ID: 42, APIKey: "sk-synthetic", KeyAlias: "Manual name", KeyAliasSource: "manual", LocalRankingAvatarID: &avatar}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&entities.UsageEvent{EventKey: "history", SourceID: "cpa-one", AuthIndex: "push.legacy", Model: "model-a", Timestamp: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	stat := entities.LocalRankingPeriodStat{PeriodKind: entities.LocalRankingPeriodDay, PeriodKey: "2026-10-27", APIKeyID: key.ID, TotalTokens: 123, UpdatedAt: time.Now()}
	if err := db.Create(&stat).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migration.Run(db); err != nil {
			t.Fatal(err)
		}
	}
	if !db.Migrator().HasTable(&entities.PushUsageIdentity{}) {
		t.Fatal("display metadata table was not created")
	}
	if err := db.First(&key, key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if key.KeyAlias != "Manual name" || key.KeyAliasSource != "manual" || key.LocalRankingAvatarID == nil || *key.LocalRankingAvatarID != avatar {
		t.Fatal("migration changed local ranking profile")
	}
	var events, stats int64
	db.Model(&entities.UsageEvent{}).Count(&events)
	db.Model(&entities.LocalRankingPeriodStat{}).Count(&stats)
	if events != 1 || stats != 1 {
		t.Fatalf("migration changed history: events=%d rankings=%d", events, stats)
	}
}
