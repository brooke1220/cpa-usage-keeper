package test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/helper"
)

func TestPushOnlyRuntimeRegistersPluginIdentityAfterUsageCommit(t *testing.T) {
	cfg := pricingRuntimeConfig(t)
	cfg.CPABaseURL, cfg.CPAManagementKey, cfg.AppBasePath = "", "", ""
	app, baseURL, _ := startPricingRuntime(t, cfg)
	waitPricingRuntimePhase(t, baseURL, "ready")
	token := enablePushSource(t, app)
	usage := strings.Replace(pushTestUsage, `"api_key":"sk-one"`, `"api_key":"private-plugin-principal","is_native_key":false,"access_provider":"plugin:example:auth"`, 1)
	body := `{"schema_version":1,"events":[{"delivery_event_id":"plugin-usage","usage":` + usage + `}]}`
	w := serveApp(app, http.MethodPost, "/api/v1/ingest/usage", body, http.Header{"Authorization": {"Bearer " + token}})
	if w.Code != http.StatusOK {
		t.Fatalf("push rejected source metadata: %d %s", w.Code, w.Body)
	}
	group := helper.PluginAPIKeyGroupKey("plugin:example:auth", "private-plugin-principal")
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		var rows []entities.CPAAPIKey
		if err := app.DB.Where("api_key = ?", group).Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 {
			key := rows[0]
			if key.SourceType != entities.CPAAPIKeySourcePlugin || key.AccessProvider != "plugin:example:auth" {
				t.Fatalf("wrong registered plugin source: %+v", key)
			}
			var rawPrincipalCount, committedUsageCount int64
			if err := app.DB.Model(&entities.CPAAPIKey{}).Where("api_key = ?", "private-plugin-principal").Count(&rawPrincipalCount).Error; err != nil || rawPrincipalCount != 0 {
				t.Fatalf("principal registered as a native Key: count=%d err=%v", rawPrincipalCount, err)
			}
			if err := app.DB.Model(&entities.UsageEvent{}).Where("api_group_key = ? AND source_id <> '' AND is_native_key = ?", group, false).Count(&committedUsageCount).Error; err != nil || committedUsageCount != 1 {
				t.Fatalf("plugin usage lost its source: count=%d err=%v", committedUsageCount, err)
			}
			settings := serveApp(app, http.MethodGet, "/api/v1/usage/api-keys/settings", "", nil)
			if settings.Code != http.StatusOK || strings.Contains(settings.Body.String(), "private-plugin-principal") || !strings.Contains(settings.Body.String(), `"sourceType":"plugin"`) {
				t.Fatalf("unsafe plugin settings: %d %s", settings.Code, settings.Body)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("push-only production startup did not run plugin identity registration")
}
