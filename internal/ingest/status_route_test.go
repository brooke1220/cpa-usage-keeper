package ingest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"cpa-usage-keeper/internal/api"
	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/ingest"
	"cpa-usage-keeper/internal/repository"
)

func statusRouter(t *testing.T, mode string) (http.Handler, func()) {
	t.Helper()
	db, err := repository.OpenDatabase(config.Config{SQLitePath: filepath.Join(t.TempDir(), "status.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sources, err := ingest.NewSources(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := ingest.New(db, sources)
	if err != nil {
		t.Fatal(err)
	}
	router := api.NewRouter(nil, nil, nil, nil, api.AuthConfig{}, nil, "", api.OptionalProviders{Ingest: receiver, Status: api.StatusRouteConfig{IngestMode: mode, Ingest: receiver}})
	// 删掉推送统计依赖的表，模拟统计查询失败。
	breakIngestStatus := func() {
		if err := db.Exec("DROP TABLE push_receipts").Error; err != nil {
			t.Fatal(err)
		}
	}
	return router, breakIngestStatus
}

func getStatus(router http.Handler) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/status", nil))
	return w
}

// 拉取实例的 /status 不能因推送统计出错整体失败；只接收推送时接收状态就是全部状态。
func TestStatusSurvivesIngestErrorsInPullMode(t *testing.T) {
	router, breakIngestStatus := statusRouter(t, "pull")
	if w := getStatus(router); w.Code != 200 || !strings.Contains(w.Body.String(), `"ingest":`) {
		t.Fatalf("healthy pull status: %d %s", w.Code, w.Body)
	}
	breakIngestStatus()
	w := getStatus(router)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"ingest":`) || !strings.Contains(w.Body.String(), `"ingest_mode":"pull"`) {
		t.Fatalf("pull status must degrade without ingest stats: %d %s", w.Code, w.Body)
	}

	pushRouter, breakPush := statusRouter(t, "push")
	breakPush()
	if w := getStatus(pushRouter); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("push-only status must report the failure: %d %s", w.Code, w.Body)
	}
}
