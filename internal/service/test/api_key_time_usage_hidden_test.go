package test

import (
	"context"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/service"
	"cpa-usage-keeper/internal/service/dto"
)

func TestAPIKeyTimeUsageExcludesHiddenKeysFromRowsAndTotals(t *testing.T) {
	db := openUsageServiceTestDatabase(t)
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 10, 9, 20, 0, 0, 0, location)
	keys := []entities.CPAAPIKey{{APIKey: "sk-visible-key"}, {APIKey: "sk-hidden-key"}}
	if err := db.Create(&keys).Error; err != nil {
		t.Fatal(err)
	}
	events := []entities.UsageEvent{
		{EventKey: "visible", APIGroupKey: keys[0].APIKey, Timestamp: time.Date(2026, 10, 8, 12, 0, 0, 0, location), TotalTokens: 4},
		{EventKey: "hidden", APIGroupKey: keys[1].APIKey, Timestamp: time.Date(2026, 10, 8, 13, 0, 0, 0, location), TotalTokens: 100},
	}
	if err := db.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SaveHiddenAPIKeyIDs(context.Background(), db, []int64{keys[1].ID}); err != nil {
		t.Fatal(err)
	}
	provider, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Now: func() time.Time { return now }, Location: location})
	if err != nil {
		t.Fatal(err)
	}

	report, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{StartDate: "2026-10-08", EndDate: "2026-10-08"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.TotalTokens != 4 || len(report.KeySummaries) != 1 || len(report.Keys) != 1 || report.Keys[0].ID != "1" {
		t.Fatalf("hidden key leaked into the report: summary=%+v keys=%+v summaries=%d", report.Summary, report.Keys, len(report.KeySummaries))
	}
	if report.Unattributed.TotalTokens != 0 {
		t.Fatalf("hidden key usage must not move to unattributed: %+v", report.Unattributed)
	}

	// 筛选框里残留的隐藏 Key 返回空结果，而不是 404。
	selected, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{StartDate: "2026-10-08", EndDate: "2026-10-08", APIKeyID: "2"})
	if err != nil {
		t.Fatalf("selecting a hidden key should not fail: %v", err)
	}
	if selected.HasRecords || selected.Summary.TotalTokens != 0 || len(selected.KeySummaries) != 0 {
		t.Fatalf("selecting a hidden key must return no data: %+v", selected)
	}

	if _, err := repository.SaveHiddenAPIKeyIDs(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	restored, err := provider.GetAPIKeyTimeUsage(context.Background(), dto.APIKeyTimeUsageQuery{StartDate: "2026-10-08", EndDate: "2026-10-08"})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Summary.TotalTokens != 104 || len(restored.KeySummaries) != 2 {
		t.Fatalf("unhiding should restore the key immediately: %+v", restored.Summary)
	}
}
