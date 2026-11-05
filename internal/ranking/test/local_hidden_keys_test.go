package test

import (
	"context"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/ranking"
	"cpa-usage-keeper/internal/repository"
)

func TestLocalRankingLeaderboardExcludesHiddenKeysWithoutDroppingSnapshots(t *testing.T) {
	location := localRankingLocation(t)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, location)
	db := openRankingDatabase(t)
	keys := seedLocalRankingAPIKeys(t, db)
	insertLocalRankingEvents(t, db, []entities.UsageEvent{
		{EventKey: "alpha", APIGroupKey: keys[0].APIKey, Model: "gpt-5", Timestamp: now.Add(-3 * time.Minute), LatencyMS: 900, InputTokens: 100, TotalTokens: 300},
		{EventKey: "beta", APIGroupKey: keys[1].APIKey, Model: "gpt-5", Timestamp: now.Add(-2 * time.Minute), LatencyMS: 900, InputTokens: 100, TotalTokens: 900},
	})
	service := newLocalRankingService(t, db, func() time.Time { return now })
	if err := service.AggregateOnce(context.Background()); err != nil {
		t.Fatalf("aggregate local ranking: %v", err)
	}
	if _, err := repository.SaveHiddenAPIKeyIDs(context.Background(), db, []int64{keys[1].ID}); err != nil {
		t.Fatalf("hide beta: %v", err)
	}

	for _, metric := range []ranking.LeaderboardMetric{ranking.MetricTotalTokens, ranking.MetricOverall} {
		board := loadLocalBoard(t, service, ranking.LeaderboardToday, metric)
		if len(board.Entries) != 1 || board.Entries[0].ParticipantID != "1" || board.Entries[0].Rank != 1 {
			t.Fatalf("%s board should only contain the visible key ranked first: %+v", metric, board.Entries)
		}
	}
	var snapshots int64
	if err := db.Model(&entities.LocalRankingPeriodStat{}).Where("api_key_id = ?", keys[1].ID).Count(&snapshots).Error; err != nil {
		t.Fatalf("count hidden key snapshots: %v", err)
	}
	if snapshots == 0 {
		t.Fatal("hiding a key must not delete its ranking snapshots")
	}

	if _, err := repository.SaveHiddenAPIKeyIDs(context.Background(), db, nil); err != nil {
		t.Fatalf("unhide: %v", err)
	}
	board := loadLocalBoard(t, service, ranking.LeaderboardToday, ranking.MetricTotalTokens)
	if len(board.Entries) != 2 || board.Entries[0].ParticipantID != "2" {
		t.Fatalf("unhiding should restore the key immediately: %+v", board.Entries)
	}
}
