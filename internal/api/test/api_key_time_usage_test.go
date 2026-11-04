package test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	keeperapi "cpa-usage-keeper/internal/api"
	"cpa-usage-keeper/internal/service/dto"
)

type apiKeyTimeUsageProviderStub struct {
	query dto.APIKeyTimeUsageQuery
}

func (s *apiKeyTimeUsageProviderStub) GetAPIKeyTimeUsage(_ context.Context, query dto.APIKeyTimeUsageQuery) (*dto.APIKeyTimeUsageReport, error) {
	s.query = query
	return &dto.APIKeyTimeUsageReport{}, nil
}

func TestAPIKeyTimeUsageRoutePassesTaskIdleMinutes(t *testing.T) {
	provider := &apiKeyTimeUsageProviderStub{}
	router := keeperapi.NewRouter(nil, nil, nil, nil, keeperapi.AuthConfig{}, nil, "", keeperapi.OptionalProviders{APIKeyTimeUsage: provider})
	ok := httptest.NewRecorder()
	router.ServeHTTP(ok, httptest.NewRequest(http.MethodGet, "/api/v1/usage/api-key-time-usage?start_date=2026-10-01&end_date=2026-10-07&task_idle_minutes=30", nil))
	if ok.Code != http.StatusOK || provider.query.TaskIdleMinutes != "30" || provider.query.StartDate != "2026-10-01" {
		t.Fatalf("unexpected response: status=%d body=%s query=%+v", ok.Code, ok.Body.String(), provider.query)
	}
	repeated := httptest.NewRecorder()
	router.ServeHTTP(repeated, httptest.NewRequest(http.MethodGet, "/api/v1/usage/api-key-time-usage?start_date=2026-10-01&end_date=2026-10-07&task_idle_minutes=30&task_idle_minutes=10", nil))
	if repeated.Code != http.StatusBadRequest {
		t.Fatalf("repeated parameter: status=%d body=%s", repeated.Code, repeated.Body.String())
	}
}

func TestAPIKeyTimeUsageRoutePassesTaskSwitchAndRejectsDuplicates(t *testing.T) {
	provider := &apiKeyTimeUsageProviderStub{}
	router := keeperapi.NewRouter(nil, nil, nil, nil, keeperapi.AuthConfig{}, nil, "", keeperapi.OptionalProviders{APIKeyTimeUsage: provider})
	for _, value := range []string{"true", "false"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/usage/api-key-time-usage?start_date=2026-10-01&end_date=2026-10-07&task_time_enabled="+value, nil))
		if response.Code != http.StatusOK || provider.query.TaskTimeEnabled != value {
			t.Fatalf("switch %q not forwarded: status=%d query=%+v", value, response.Code, provider.query)
		}
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/usage/api-key-time-usage?task_time_enabled=true&task_time_enabled=false", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate switch should be rejected: status=%d", response.Code)
	}
}
