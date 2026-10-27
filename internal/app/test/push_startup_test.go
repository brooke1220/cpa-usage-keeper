package test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/entities"
)

func TestPushModeStartupWithoutCPAAndMachineRoute(t *testing.T) {
	cfg := testAppConfig(t)
	cfg.IngestMode = "push"
	cfg.SQLitePath = t.TempDir() + "/nested/data/app.db"
	cfg.CPABaseURL = ""
	cfg.CPAManagementKey = ""
	cfg.BackupEnabled = false
	token := strings.Repeat("s", 32)
	hash := sha256.Sum256([]byte(token))
	cfg.IngestSources = []config.PushSource{{SourceID: "cpa-one", TokenSHA256: hex.EncodeToString(hash[:])}}
	app := newInitializedApp(t, cfg)
	if app.RedisIngest != nil || app.CPAErrors != nil || app.MetadataSync != nil || app.QuotaService != nil || app.QuotaAutoRefresh != nil {
		t.Fatal("push mode constructed an outbound CPA task")
	}
	if app.RedisProcess == nil || app.UsageAggregation == nil || app.LocalRanking == nil || app.Maintenance == nil {
		t.Fatal("push mode lost a local background task")
	}
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	w := httptest.NewRecorder()
	app.Router.ServeHTTP(w, req)
	var status map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status["ingest_mode"] != "push" {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	payload := `{"schema_version":1,"source_id":"cpa-one","events":[{"delivery_event_id":"test-event","usage":{"timestamp":"2026-10-26T01:00:00Z","api_key":"sk-one","model":"gpt-test","executor_type":"CodexExecutor","execution_id":"exec","tokens":{"total_tokens":0},"accounting_version":2,"token_breakdown":{"schema_version":2,"quality":"complete","total_tokens":0,"unclassified_tokens":0,"input":{"total_tokens":0,"uncached_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0},"output":{"total_tokens":0,"non_reasoning_tokens":0,"reasoning_tokens":0}}}}]}`
	req = httptest.NewRequest("POST", "/api/v1/ingest/usage", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	app.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("machine route incorrectly requires browser intent: %d %s", w.Code, w.Body)
	}
	var receipts int64
	if err := app.DB.Model(&entities.PushReceipt{}).Count(&receipts).Error; err != nil || receipts != 1 {
		t.Fatalf("receipt: %d %v", receipts, err)
	}
	req = httptest.NewRequest("POST", "/api/v1/auth/api-key-login", bytes.NewBufferString(`{"apiKey":"sk-one"}`))
	req.Header.Set("X-CPA-Usage-Keeper-Request", "fetch")
	w = httptest.NewRecorder()
	app.Router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("key login not disabled: %d %s", w.Code, w.Body)
	}
	req = httptest.NewRequest("GET", "/api/v1/auth/session", nil)
	w = httptest.NewRecorder()
	app.Router.ServeHTTP(w, req)
	var session struct {
		APIKeyLoginEnabled bool   `json:"api_key_login_enabled"`
		IngestMode         string `json:"ingest_mode"`
	}
	if json.Unmarshal(w.Body.Bytes(), &session) != nil || session.APIKeyLoginEnabled {
		t.Fatal("session still advertises key login")
	}
	if session.IngestMode != "push" {
		t.Fatalf("session must report push mode without /status: %s", w.Body)
	}
	for _, path := range []string{"/api/v1/credentials", "/api/v1/quota"} {
		req = httptest.NewRequest("GET", path, nil)
		w = httptest.NewRecorder()
		app.Router.ServeHTTP(w, req)
		if w.Code == 200 {
			t.Fatalf("remote capability enabled: %s", path)
		}
	}
}

func TestPushTokensGrantNoAdministratorAccess(t *testing.T) {
	cfg := testAppConfig(t)
	cfg.IngestMode, cfg.CPABaseURL, cfg.CPAManagementKey = "push", "", ""
	cfg.AuthEnabled, cfg.LoginPassword, cfg.BackupEnabled = true, "private-test-admin-password", false
	cfg.AuthSessionTTL = time.Hour
	token := strings.Repeat("p", 32)
	hash := sha256.Sum256([]byte(token))
	cfg.IngestSources = []config.PushSource{{SourceID: "cpa-one", TokenSHA256: hex.EncodeToString(hash[:])}}
	app := newInitializedApp(t, cfg)
	for _, path := range []string{"/api/v1/status", "/api/v1/ingest/quarantine"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		app.Router.ServeHTTP(response, req)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("upload credential accessed %s: %d", path, response.Code)
		}
	}
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"password":"private-test-admin-password"}`))
	req.Header.Set("X-CPA-Usage-Keeper-Request", "fetch")
	response := httptest.NewRecorder()
	app.Router.ServeHTTP(response, req)
	if response.Code != http.StatusOK && response.Code != http.StatusNoContent {
		t.Fatalf("admin login: %d %s", response.Code, response.Body)
	}
	cookies := response.Result().Cookies()
	req = httptest.NewRequest("GET", "/api/v1/ingest/quarantine", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	response = httptest.NewRecorder()
	app.Router.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("admin cannot review retained data: %d %s", response.Code, response.Body)
	}
}
