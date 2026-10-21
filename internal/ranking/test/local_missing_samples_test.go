package test

import (
	"context"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/ranking"
)

func TestLocalRankingOverallIncludesUsageWithoutTTFT(t *testing.T) {
	now := time.Date(2026, 10, 21, 16, 44, 0, 0, localRankingLocation(t))
	db := openRankingDatabase(t)
	keys := seedLocalRankingAPIKeys(t, db)
	insertLocalRankingEvents(t, db, []entities.UsageEvent{
		{EventKey: "no-ttft-success", APIGroupKey: keys[0].APIKey, Timestamp: now.Add(-2 * time.Minute), InputTokens: 1000, CacheReadTokens: 800, TotalTokens: 1500, LatencyMS: 2400},
		{EventKey: "no-ttft-failure", APIGroupKey: keys[0].APIKey, Timestamp: now.Add(-time.Minute), Failed: true, TotalTokens: 100, LatencyMS: 100},
	})
	service := newLocalRankingService(t, db, func() time.Time { return now })
	if err := service.AggregateOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tokens := loadLocalBoard(t, service, ranking.LeaderboardToday, ranking.MetricTotalTokens); len(tokens.Entries) != 1 || tokens.Entries[0].Value != 1600 {
		t.Fatalf("usage fixture missing: %+v", tokens)
	}
	board := loadLocalBoard(t, service, ranking.LeaderboardToday, ranking.MetricOverall)
	if len(board.Entries) != 1 {
		t.Fatalf("recorded usage disappeared from overall ranking because TTFT was absent: %+v", board)
	}
	entry := board.Entries[0]
	if entry.Value < 0 || entry.Value > 100 || entry.Metrics[ranking.MetricTotalTokens] != 1600 || entry.Metrics[ranking.MetricRequestCount] != 2 {
		t.Fatalf("invalid partial-data overall entry: %+v", entry)
	}
	if _, exists := entry.Metrics[ranking.MetricTTFTAverage]; exists {
		t.Fatalf("missing TTFT must not become a zero-latency measurement: %+v", entry)
	}
	if entry.Metrics[ranking.MetricLatencyAverage] != 2400*1000 {
		t.Fatalf("successful latency must not require TTFT, or include failed latency: %+v", entry)
	}
	latency := loadLocalBoard(t, service, ranking.LeaderboardToday, ranking.MetricLatencyAverage)
	if len(latency.Entries) != 1 || latency.Entries[0].RateNumerator != 2400 || latency.Entries[0].RateDenominator != 1 {
		t.Fatalf("latency board incorrectly requires TTFT: %+v", latency)
	}
	if ttft := loadLocalBoard(t, service, ranking.LeaderboardToday, ranking.MetricTTFTAverage); len(ttft.Entries) != 0 {
		t.Fatalf("TTFT board fabricated samples: %+v", ttft)
	}
}

func TestLocalRankingOverallKeepsWorkloadWhenOptionalMeasurementsAreMissing(t *testing.T) {
	now := time.Date(2026, 10, 21, 16, 44, 0, 0, localRankingLocation(t))
	db := openRankingDatabase(t)
	keys := seedLocalRankingAPIKeys(t, db)
	insertLocalRankingEvents(t, db, []entities.UsageEvent{{EventKey: "tokens-only", APIGroupKey: keys[0].APIKey, Timestamp: now.Add(-time.Minute), TotalTokens: 500}})
	service := newLocalRankingService(t, db, func() time.Time { return now })
	if err := service.AggregateOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	board := loadLocalBoard(t, service, ranking.LeaderboardToday, ranking.MetricOverall)
	if len(board.Entries) != 1 {
		t.Fatalf("missing optional measurements hid all workload: %+v", board)
	}
	for _, metric := range []ranking.LeaderboardMetric{ranking.MetricCacheReadRate, ranking.MetricTTFTAverage, ranking.MetricLatencyAverage} {
		if _, exists := board.Entries[0].Metrics[metric]; exists {
			t.Fatalf("missing %s reported as a measured zero", metric)
		}
	}
}
