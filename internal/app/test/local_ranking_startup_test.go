package test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/ranking"
	"cpa-usage-keeper/internal/repository"
)

func TestInitializeInstallsLocalRankingRunnerOnRunningApp(t *testing.T) {
	application := newInitializedApp(t, testAppConfig(t))
	defer application.Close()
	if application.LocalRanking == nil {
		t.Fatal("v2 installReadyApp discarded the constructed local ranking runner; background aggregation will never start")
	}
}

func TestProductionStartupMigratesAndAggregatesExistingLocalRankingUsage(t *testing.T) {
	cfg := pricingRuntimeConfig(t)
	seedDB, err := repository.OpenDatabase(cfg)
	if err != nil {
		t.Fatal(err)
	}
	key := entities.CPAAPIKey{APIKey: "sk-existing-local-ranking", KeyAlias: "Existing usage"}
	if err := seedDB.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(location)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	if _, _, err := repository.InsertUsageEvents(seedDB, []entities.UsageEvent{storedAppUsageEventFee(entities.UsageEvent{
		EventKey: "existing-startup-ranking-usage", APIGroupKey: key.APIKey, Model: "model-a",
		Timestamp: today, InputTokens: 1000, TotalTokens: 1234, LatencyMS: 900,
	}, 0, false)}); err != nil {
		t.Fatal(err)
	}
	// Simulate a deployed v2 database before the restoration migration. The
	// production startup must restore both structures without a manual command.
	for _, query := range []string{
		"DROP TABLE local_ranking_period_stats",
		"ALTER TABLE cpa_api_keys DROP COLUMN local_ranking_avatar_id",
		"DELETE FROM schema_migrations WHERE version = '20261021_restore_local_ranking'",
	} {
		if err := seedDB.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	closeDatabasePoolTestDB(t, seedDB)
	application, baseURL, _ := startPricingRuntime(t, cfg)
	waitPricingRuntimePhase(t, baseURL, "ready")
	if application.LocalRanking == nil {
		t.Fatal("production startup lost the local ranking background task")
	}
	if !application.DB.Migrator().HasColumn(&entities.CPAAPIKey{}, "LocalRankingAvatarID") {
		t.Fatal("startup did not automatically restore the avatar column")
	}
	var migrationCount int64
	if err := application.DB.Table("schema_migrations").Where("version = ?", "20261021_restore_local_ranking").Count(&migrationCount).Error; err != nil || migrationCount != 1 {
		t.Fatalf("restoration migration was not automatically applied: count=%d err=%v", migrationCount, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, body := pricingRuntimeRequest(t, http.MethodGet, baseURL+"/api/v1/ranking/local/leaderboards?period=today&metric=overall", nil)
		var board ranking.Leaderboard
		if status != http.StatusOK || json.Unmarshal(body, &board) != nil {
			t.Fatalf("ranking HTTP response: %d %s", status, body)
		}
		if len(board.Entries) == 1 {
			entry := board.Entries[0]
			if entry.DisplayName != "Existing usage" || entry.Metrics[ranking.MetricTotalTokens] != 1234 || entry.Metrics[ranking.MetricLatencyAverage] != 900000 {
				t.Fatalf("existing event did not reach the real ranking API: %+v", entry)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("real startup never aggregated existing usage: %s", body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
