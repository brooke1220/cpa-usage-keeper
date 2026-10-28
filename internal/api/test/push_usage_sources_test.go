package test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "cpa-usage-keeper/internal/api"
	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/ingest"
	"cpa-usage-keeper/internal/pricing"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/service"
)

func TestPushedUsageSourceNamesHistoryFiltersAndExports(t *testing.T) {
	ctx := context.Background()
	db, err := repository.OpenDatabase(config.Config{SQLitePath: filepath.Join(t.TempDir(), "sources.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	sources, err := ingest.NewSources(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	one, err := sources.Create(ctx, "Beijing CPA")
	if err != nil {
		t.Fatal(err)
	}
	two, err := sources.Create(ctx, "Shanghai CPA")
	if err != nil {
		t.Fatal(err)
	}
	if err := sources.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	receiver, err := ingest.New(db, sources)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.LoadPricingSnapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	catalog := pricing.NewCatalog(snapshot)
	processor := service.NewSyncServiceWithOptions(db, service.SyncServiceOptions{PricingCatalog: catalog})
	usage := service.NewUsageService(db, catalog)
	router := NewRouter(nil, nil, usage, nil, AuthConfig{}, nil, "", OptionalProviders{Ingest: receiver})
	push := func(token, delivery, name string) {
		t.Helper()
		var data map[string]any
		if err := json.Unmarshal([]byte(`{"provider":"codex","model":"model-a","executor_type":"CodexExecutor","auth_type":"apikey","auth_index":"same-index","execution_id":"execution","api_key":"sk-synthetic-client","reasoning_effort":"xhigh","service_tier":"auto","effective_service_tier":"priority","response_service_tier":"default","stream":true,"generate":true,"failed":false,"latency_ms":1000,"tokens":{"input_tokens":1,"output_tokens":1,"total_tokens":2},"accounting_version":2,"token_breakdown":{"schema_version":2,"quality":"complete","total_tokens":2,"unclassified_tokens":0,"input":{"total_tokens":1,"uncached_tokens":1,"cache_read_tokens":0,"cache_write_tokens":0},"output":{"total_tokens":1,"non_reasoning_tokens":1,"reasoning_tokens":0}}}`), &data); err != nil {
			t.Fatal(err)
		}
		data["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
		if name != "" {
			data["identity"] = map[string]string{"display_name": name, "provider_type": "codex"}
		}
		raw, _ := json.Marshal(data)
		batch, _ := json.Marshal(ingest.Batch{SchemaVersion: 1, Events: []ingest.Event{{ID: delivery, Usage: raw}}})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/usage", bytes.NewReader(batch))
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("push status=%d body=%s", response.Code, response.Body)
		}
		if _, err := processor.ProcessRedisUsageInbox(ctx); err != nil {
			t.Fatal(err)
		}
	}
	list := func(path string) []map[string]any {
		t.Helper()
		response := serveAPIGet(router, path)
		if response.Code != 200 {
			t.Fatalf("list status=%d body=%s", response.Code, response.Body)
		}
		var result struct {
			Events []map[string]any `json:"events"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Events
	}
	push(one.Token, "legacy", "")
	legacy := list("/api/v1/usage/events?range=24h")
	if len(legacy) != 1 || legacy[0]["source"] != "codex" || legacy[0]["isDelete"] == true || legacy[0]["source_name"] != "Beijing CPA" {
		t.Fatalf("legacy push misclassified: %+v", legacy)
	}
	push(one.Token, "named", "Channel A")
	push(two.Token, "other", "Channel B")
	all := list("/api/v1/usage/events?range=24h")
	if len(all) != 3 {
		t.Fatalf("events=%d", len(all))
	}
	var secondIndex string
	for _, row := range all {
		want := "Channel A"
		if row["source_id"] == two.SourceID {
			want = "Channel B"
			secondIndex = row["auth_index"].(string)
		}
		if row["source"] != want || row["isDelete"] == true || row["source_type"] != "codex" {
			t.Fatalf("source collision or old history not resolved: %+v", row)
		}
		if row["reasoning_effort"] != "xhigh" || row["effective_service_tier"] != "priority" || row["service_tier"] != "auto" || row["response_service_tier"] != "default" || row["stream"] != true {
			t.Fatalf("existing metadata lost: %+v", row)
		}
		if (row["endpoint"] != nil && row["endpoint"] != "") || row["request_id"] != nil {
			t.Fatalf("unavailable metadata was invented: %+v", row)
		}
	}
	query := "?range=24h&source=" + url.QueryEscape("push-source:"+one.SourceID)
	if rows := list("/api/v1/usage/events" + query); len(rows) != 2 {
		t.Fatalf("CPA filter rows=%d", len(rows))
	}
	if rows := list("/api/v1/usage/events?range=24h&source=" + url.QueryEscape(secondIndex)); len(rows) != 1 || rows[0]["source"] != "Channel B" {
		t.Fatalf("credential filter: %+v", rows)
	}
	filters := serveAPIGet(router, "/api/v1/usage/events/filters/sources")
	if filters.Code != 200 || !strings.Contains(filters.Body.String(), "Beijing CPA / Channel A") || !strings.Contains(filters.Body.String(), "push-source:"+one.SourceID) {
		t.Fatalf("push filters missing: %s", filters.Body)
	}
	if _, err := sources.Rename(ctx, one.ID, "+Renamed CPA"); err != nil {
		t.Fatal(err)
	}
	for _, row := range list("/api/v1/usage/events/export" + query + "&format=json") {
		if row["source"] != "Channel A" || row["source_id"] != one.SourceID || row["source_name"] != "+Renamed CPA" || row["is_identity_deleted"] != false {
			t.Fatalf("export mismatch: %+v", row)
		}
	}
	csvResponse := serveAPIGet(router, "/api/v1/usage/events/export"+query+"&format=csv")
	records, err := csv.NewReader(strings.NewReader(csvResponse.Body.String())).ReadAll()
	if err != nil || len(records) != 3 {
		t.Fatalf("CSV export rows=%d err=%v", len(records), err)
	}
	columns := make(map[string]int)
	for index, name := range records[0] {
		columns[name] = index
	}
	for _, record := range records[1:] {
		if record[columns["source"]] != "Channel A" || record[columns["source_id"]] != one.SourceID || record[columns["source_name"]] != "'+Renamed CPA" || record[columns["is_identity_deleted"]] != "false" {
			t.Fatalf("CSV source fields mismatch: %v", record)
		}
	}
	if _, err := sources.Revoke(ctx, one.ID); err != nil {
		t.Fatal(err)
	}
	if err := sources.Delete(ctx, one.ID); err != nil {
		t.Fatal(err)
	}
	if rows := list("/api/v1/usage/events" + query); len(rows) != 2 || rows[0]["source"] != "Channel A" || rows[0]["isDelete"] == true {
		t.Fatalf("removing registration destroyed identity history: %+v", rows)
	}
	// 推送名称目录不可读时：没有推送行的列表页不读目录，筛选项只去掉推送部分。
	if err := db.Migrator().DropTable(&entities.PushUsageIdentity{}); err != nil {
		t.Fatal(err)
	}
	if rows := list("/api/v1/usage/events?range=24h&source=authidx-local-only"); len(rows) != 0 {
		t.Fatalf("unexpected rows for local-only filter: %+v", rows)
	}
	degraded := serveAPIGet(router, "/api/v1/usage/events/filters/sources")
	if degraded.Code != http.StatusOK || strings.Contains(degraded.Body.String(), "push-source:") || !strings.Contains(degraded.Body.String(), `"sources":[]`) {
		t.Fatalf("push catalog failure broke local source filters: %d %s", degraded.Code, degraded.Body)
	}
}
