package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	keeperapp "cpa-usage-keeper/internal/app"
	"cpa-usage-keeper/internal/entities"
)

const pushTestUsage = `{"timestamp":"2026-10-26T01:00:00Z","api_key":"sk-one","model":"gpt-test","executor_type":"CodexExecutor","execution_id":"exec","tokens":{"total_tokens":0},"accounting_version":2,"token_breakdown":{"schema_version":2,"quality":"complete","total_tokens":0,"unclassified_tokens":0,"input":{"total_tokens":0,"uncached_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0},"output":{"total_tokens":0,"non_reasoning_tokens":0,"reasoning_tokens":0}}}`

func serveApp(app *keeperapp.App, method, path, body string, header http.Header, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	for key, values := range header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	app.Router.ServeHTTP(w, req)
	return w
}

var adminIntent = http.Header{"X-CPA-Usage-Keeper-Request": {"fetch"}, "Content-Type": {"application/json"}}

// enablePushSource 走管理员接口打开开关并创建来源，返回只出现一次的 token。
func enablePushSource(t *testing.T, app *keeperapp.App, cookies ...*http.Cookie) string {
	t.Helper()
	if w := serveApp(app, "PUT", "/api/v1/ingest/settings", `{"enabled":true}`, adminIntent, cookies...); w.Code != 200 {
		t.Fatalf("enable push: %d %s", w.Code, w.Body)
	}
	w := serveApp(app, "POST", "/api/v1/ingest/sources", `{"name":"CPA 一号"}`, adminIntent, cookies...)
	if w.Code != http.StatusCreated {
		t.Fatalf("create source: %d %s", w.Code, w.Body)
	}
	var issued struct {
		Token    string `json:"token"`
		SourceID string `json:"source_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil || len(issued.Token) != 64 || issued.SourceID == "" {
		t.Fatalf("issued source: %s %v", w.Body, err)
	}
	return issued.Token
}

func pushUsage(app *keeperapp.App, token, deliveryID string) *httptest.ResponseRecorder {
	body := `{"schema_version":1,"events":[{"delivery_event_id":"` + deliveryID + `","usage":` + pushTestUsage + `}]}`
	return serveApp(app, "POST", "/api/v1/ingest/usage", body, http.Header{"Authorization": {"Bearer " + token}})
}

func TestPushOnlyStartupWithoutCPA(t *testing.T) {
	cfg := testAppConfig(t)
	cfg.SQLitePath = t.TempDir() + "/nested/data/app.db"
	cfg.CPABaseURL = ""
	cfg.CPAManagementKey = ""
	cfg.BackupEnabled = false
	app := newInitializedApp(t, cfg)
	if app.RedisIngest != nil || app.CPAErrors != nil || app.MetadataSync != nil || app.QuotaService != nil || app.QuotaAutoRefresh != nil {
		t.Fatal("push-only startup constructed an outbound CPA task")
	}
	if app.RedisProcess == nil || app.UsageAggregation == nil || app.LocalRanking == nil || app.Maintenance == nil {
		t.Fatal("push-only startup lost a local background task")
	}
	w := serveApp(app, "GET", "/api/v1/status", "", nil)
	var status struct {
		IngestMode string `json:"ingest_mode"`
		Ingest     *struct {
			Enabled bool `json:"enabled"`
		} `json:"ingest"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.IngestMode != "push" || status.Ingest == nil || status.Ingest.Enabled {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	// 默认关闭：没有任何来源时上传按暂时不可用处理，插件保留数据。
	if w := pushUsage(app, strings.Repeat("s", 64), "before-enable"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled receiver: %d %s", w.Code, w.Body)
	}
	token := enablePushSource(t, app)
	if w := pushUsage(app, token, "test-event"); w.Code != 200 {
		t.Fatalf("machine route incorrectly requires browser intent: %d %s", w.Code, w.Body)
	}
	var receipts int64
	if err := app.DB.Model(&entities.PushReceipt{}).Count(&receipts).Error; err != nil || receipts != 1 {
		t.Fatalf("receipt: %d %v", receipts, err)
	}
	if w := serveApp(app, "POST", "/api/v1/auth/api-key-login", `{"apiKey":"sk-one"}`, adminIntent); w.Code != http.StatusForbidden {
		t.Fatalf("key login not disabled: %d %s", w.Code, w.Body)
	}
	w = serveApp(app, "GET", "/api/v1/auth/session", "", nil)
	var session struct {
		APIKeyLoginEnabled bool   `json:"api_key_login_enabled"`
		IngestMode         string `json:"ingest_mode"`
	}
	if json.Unmarshal(w.Body.Bytes(), &session) != nil || session.APIKeyLoginEnabled || session.IngestMode != "push" {
		t.Fatalf("session must report push-only mode: %s", w.Body)
	}
	for _, path := range []string{"/api/v1/credentials", "/api/v1/quota"} {
		if w := serveApp(app, "GET", path, "", nil); w.Code == 200 {
			t.Fatalf("remote capability enabled: %s", path)
		}
	}
}

func TestPullModeAlsoReceivesPushedUsage(t *testing.T) {
	cfg := testAppConfig(t)
	// 开启登录保护：未开启时 Key 登录接口对任何输入都返回 204，无法证明推送 Key 被拒。
	cfg.AuthEnabled, cfg.LoginPassword, cfg.BackupEnabled = true, "private-test-admin-password", false
	cfg.AuthSessionTTL = time.Hour
	app := newInitializedApp(t, cfg)
	if app.MetadataSync == nil || app.QuotaService == nil {
		t.Fatal("configured CPA lost pull services")
	}
	login := serveApp(app, "POST", "/api/v1/auth/login", `{"password":"private-test-admin-password"}`, adminIntent)
	if login.Code != http.StatusOK && login.Code != http.StatusNoContent {
		t.Fatalf("admin login: %d %s", login.Code, login.Body)
	}
	token := enablePushSource(t, app, login.Result().Cookies()...)
	if w := pushUsage(app, token, "pull-and-push"); w.Code != 200 {
		t.Fatalf("pull instance rejected push: %d %s", w.Code, w.Body)
	}
	w := serveApp(app, "GET", "/api/v1/auth/session", "", nil)
	var session struct {
		APIKeyLoginEnabled bool   `json:"api_key_login_enabled"`
		IngestMode         string `json:"ingest_mode"`
	}
	if json.Unmarshal(w.Body.Bytes(), &session) != nil || !session.APIKeyLoginEnabled || session.IngestMode != "pull" {
		t.Fatalf("pull instance must keep key login and pull mode: %s", w.Body)
	}
	// 推送带来的 Key 只给管理员展示，不能用于 Key 自助登录。
	if w := serveApp(app, "POST", "/api/v1/auth/api-key-login", `{"apiKey":"sk-one"}`, adminIntent); w.Code != http.StatusUnauthorized {
		t.Fatalf("push-only key login must be rejected: %d %s", w.Code, w.Body)
	}
	var key entities.CPAAPIKey
	if err := app.DB.Where("api_key = ?", "sk-one").First(&key).Error; err != nil || key.IsDeleted || !key.PushSeen || key.PullSeen {
		t.Fatalf("pushed key not visible to admins: %+v %v", key, err)
	}
}

func TestPushTokensGrantNoAdministratorAccess(t *testing.T) {
	cfg := testAppConfig(t)
	cfg.CPABaseURL, cfg.CPAManagementKey = "", ""
	cfg.AuthEnabled, cfg.LoginPassword, cfg.BackupEnabled = true, "private-test-admin-password", false
	cfg.AuthSessionTTL = time.Hour
	app := newInitializedApp(t, cfg)
	login := serveApp(app, "POST", "/api/v1/auth/login", `{"password":"private-test-admin-password"}`, adminIntent)
	if login.Code != http.StatusOK && login.Code != http.StatusNoContent {
		t.Fatalf("admin login: %d %s", login.Code, login.Body)
	}
	cookies := login.Result().Cookies()
	// 未登录不能管理来源或读取 token 列表。
	if w := serveApp(app, "GET", "/api/v1/ingest/settings", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous read push settings: %d", w.Code)
	}
	if w := serveApp(app, "POST", "/api/v1/ingest/sources", `{"name":"x"}`, adminIntent); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous created push source: %d", w.Code)
	}
	// 管理员写操作仍要求请求意图头，防止跨站表单提交。
	if w := serveApp(app, "POST", "/api/v1/ingest/sources", `{"name":"x"}`, http.Header{"Content-Type": {"application/json"}}, cookies...); w.Code != http.StatusForbidden {
		t.Fatalf("missing request intent accepted: %d", w.Code)
	}
	token := enablePushSource(t, app, cookies...)
	for _, path := range []string{"/api/v1/status", "/api/v1/ingest/quarantine", "/api/v1/ingest/settings"} {
		if w := serveApp(app, "GET", path, "", http.Header{"Authorization": {"Bearer " + token}}); w.Code != http.StatusUnauthorized {
			t.Fatalf("upload credential accessed %s: %d", path, w.Code)
		}
	}
	w := serveApp(app, "GET", "/api/v1/ingest/settings", "", nil, cookies...)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "token_sha256") {
		t.Fatalf("settings leaked credential: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"upload_path":"/api/v1/ingest/usage"`) {
		t.Fatalf("upload path missing: %s", w.Body)
	}
	if w := serveApp(app, "GET", "/api/v1/ingest/quarantine", "", nil, cookies...); w.Code != http.StatusOK {
		t.Fatalf("admin cannot review retained data: %d %s", w.Code, w.Body)
	}
}
