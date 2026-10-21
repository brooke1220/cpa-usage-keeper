package test

import (
	"context"
	keeperapi "cpa-usage-keeper/internal/api"
	"cpa-usage-keeper/internal/ranking"
	"net/http"
	"net/http/httptest"
	"testing"
)

type restoredLocalRankingStub struct{}

func (*restoredLocalRankingStub) Leaderboard(context.Context, ranking.LeaderboardPeriod, ranking.LeaderboardMetric) (ranking.Leaderboard, error) {
	return ranking.Leaderboard{Period: ranking.LeaderboardToday, Metric: ranking.MetricOverall, Entries: []ranking.LeaderboardEntry{}}, nil
}
func (*restoredLocalRankingStub) UpdateProfile(context.Context, int64, string, uint8) (ranking.LocalProfile, error) {
	return ranking.LocalProfile{}, nil
}

func TestRestoredLocalRankingRoutesRespectBasePathAndAuthentication(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		router := keeperapi.NewRouter(nil, nil, nil, nil, keeperapi.AuthConfig{Enabled: authenticated, LoginPassword: "test-password"}, nil, "/keeper", keeperapi.OptionalProviders{LocalRanking: &restoredLocalRankingStub{}})
		request := httptest.NewRequest(http.MethodGet, "/keeper/api/v1/ranking/local/leaderboards?period=today&metric=overall", nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		expected := http.StatusOK
		if authenticated {
			expected = http.StatusUnauthorized
		}
		if response.Code != expected {
			t.Fatalf("auth=%v status=%d body=%s", authenticated, response.Code, response.Body.String())
		}
		// Community participation endpoints stay absent from the restored application.
		missing := httptest.NewRecorder()
		router.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/keeper/api/v1/ranking/status", nil))
		if missing.Code != http.StatusNotFound {
			t.Fatalf("community status exposed: %d", missing.Code)
		}
	}
}
