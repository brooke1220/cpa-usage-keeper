package test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/helper"
	"cpa-usage-keeper/internal/ranking"
)

func TestLocalRankingKeepsPluginLabelsAndManualProfiles(t *testing.T) {
	db := openRankingDatabase(t)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, localRankingLocation(t))
	key := entities.CPAAPIKey{
		APIKey:     helper.PluginAPIKeyGroupKey("plugin:example:auth", "private-team-principal"),
		SourceType: entities.CPAAPIKeySourcePlugin, AccessProvider: "plugin:example:auth",
	}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	insertLocalRankingEvents(t, db, []entities.UsageEvent{{EventKey: "plugin-ranking-usage", APIGroupKey: key.APIKey, Timestamp: now.Add(-time.Minute), TotalTokens: 500}})
	service := newLocalRankingService(t, db, func() time.Time { return now })
	if err := service.AggregateOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	board := loadLocalBoard(t, service, ranking.LeaderboardToday, ranking.MetricTotalTokens)
	if len(board.Entries) != 1 || board.Entries[0].DisplayName != fmt.Sprintf("Third-party Identity #%d", key.ID) || board.Entries[0].Value != 500 {
		t.Fatalf("plugin label or usage lost in ranking: %+v", board)
	}
	if _, err := service.UpdateProfile(context.Background(), key.ID, "Local team", 42); err != nil {
		t.Fatal(err)
	}
	board = loadLocalBoard(t, service, ranking.LeaderboardToday, ranking.MetricTotalTokens)
	if len(board.Entries) != 1 || board.Entries[0].DisplayName != "Local team" || board.Entries[0].AvatarID != 42 {
		t.Fatalf("plugin profile lost in ranking: %+v", board)
	}
	if err := db.First(&key, key.ID).Error; err != nil || key.KeyAliasSource != entities.CPAAPIKeyAliasSourceManual {
		t.Fatalf("plugin profile must retain manual alias source: %+v %v", key, err)
	}
}
