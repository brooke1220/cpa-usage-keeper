package test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	keeperapi "cpa-usage-keeper/internal/api"
	"cpa-usage-keeper/internal/workhours"
)

type workHoursProviderStub struct {
	schedule workhours.Schedule
	start    string
	end      string
}

func (s *workHoursProviderStub) GetWorkHours(context.Context) (workhours.Schedule, error) {
	return s.schedule, nil
}

func (s *workHoursProviderStub) UpdateWorkHours(_ context.Context, start, end string) (workhours.Schedule, error) {
	s.start, s.end = start, end
	s.schedule.Start, s.schedule.End = start, end
	return s.schedule, nil
}

func TestWorkHoursRoutesReadAndUpdateWithNoStoreHeaders(t *testing.T) {
	provider := &workHoursProviderStub{schedule: workhours.Schedule{Timezone: "Asia/Shanghai", Start: "09:00", End: "18:00", Source: workhours.SourceDefault}}
	router := keeperapi.NewRouter(nil, nil, nil, nil, keeperapi.AuthConfig{}, nil, "", keeperapi.OptionalProviders{WorkHours: provider})
	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/usage/work-hours", nil))
	if get.Code != http.StatusOK || get.Header().Get("Cache-Control") != "no-store" || get.Body.String() == "" {
		t.Fatalf("unexpected GET response: status=%d headers=%v body=%s", get.Code, get.Header(), get.Body.String())
	}
	put := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/usage/work-hours", strings.NewReader(`{"start":"10:00","end":"19:00"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CPA-Usage-Keeper-Request", "fetch")
	router.ServeHTTP(put, request)
	if put.Code != http.StatusOK || provider.start != "10:00" || provider.end != "19:00" {
		t.Fatalf("unexpected PUT response: status=%d body=%s provider=%+v", put.Code, put.Body.String(), provider)
	}
}

func TestWorkHoursRoutesMapInvalidJSON(t *testing.T) {
	router := keeperapi.NewRouter(nil, nil, nil, nil, keeperapi.AuthConfig{}, nil, "", keeperapi.OptionalProviders{WorkHours: &workHoursProviderStub{}})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/usage/work-hours", strings.NewReader("{"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CPA-Usage-Keeper-Request", "fetch")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
