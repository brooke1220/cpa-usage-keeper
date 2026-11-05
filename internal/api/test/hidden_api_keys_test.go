package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	keeperapi "cpa-usage-keeper/internal/api"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/service"
)

func TestHiddenAPIKeyRoutesSaveExistingKeysOnly(t *testing.T) {
	db := openAPITestDatabase(t)
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha123456", "sk-beta654321"}, time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed API keys: %v", err)
	}
	// 已删除的 Key 仍可能有历史用量，必须允许保持隐藏。
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha123456"}, time.Date(2026, 5, 13, 11, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("mark deleted: %v", err)
	}
	router := keeperapi.NewRouter(nil, nil, nil, nil, keeperapi.AuthConfig{}, nil, "/keeper", keeperapi.OptionalProviders{CPAAPIKeys: service.NewCPAAPIKeyService(db)})
	type deletedKey struct {
		ID         string `json:"id"`
		DisplayKey string `json:"displayKey"`
		APIKey     string `json:"apiKey"`
	}
	readResponse := func() ([]string, []deletedKey, string) {
		t.Helper()
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/keeper/api/v1/usage/api-keys/hidden", nil))
		if resp.Code != http.StatusOK {
			t.Fatalf("GET hidden: status %d body=%s", resp.Code, resp.Body.String())
		}
		var body struct {
			IDs         []string     `json:"ids"`
			DeletedKeys []deletedKey `json:"deletedKeys"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode hidden: %v", err)
		}
		return body.IDs, body.DeletedKeys, resp.Body.String()
	}
	readHidden := func() []string {
		t.Helper()
		ids, _, _ := readResponse()
		return ids
	}

	ids, deleted, raw := readResponse()
	if ids == nil || len(ids) != 0 {
		t.Fatalf("expected empty non-null ids before saving, got %#v", ids)
	}
	// 已删除的 Key 仍有历史用量，设置页需要列出它们；只返回脱敏信息。
	if len(deleted) != 1 || deleted[0].ID != "2" || deleted[0].DisplayKey == "" || deleted[0].APIKey != "" {
		t.Fatalf("expected the deleted key with masked display data only, got %+v", deleted)
	}
	if strings.Contains(raw, "sk-beta654321") {
		t.Fatalf("hidden key response leaked a full API key: %s", raw)
	}

	resp := serveCredentialMutation(router, http.MethodPut, "/keeper/api/v1/usage/api-keys/hidden", `{"ids":["2","1","2","999"]}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("PUT hidden: status %d body=%s", resp.Code, resp.Body.String())
	}
	if got := readHidden(); !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Fatalf("expected deduplicated existing ids, got %#v", got)
	}

	resp = serveCredentialMutation(router, http.MethodPut, "/keeper/api/v1/usage/api-keys/hidden", `{"ids":[]}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("PUT empty: status %d body=%s", resp.Code, resp.Body.String())
	}
	if got := readHidden(); len(got) != 0 {
		t.Fatalf("expected cleared ids, got %#v", got)
	}

	for _, body := range []string{`{}`, `{"ids":["abc"]}`, `{"ids":["0"]}`, `not-json`} {
		resp := serveCredentialMutation(router, http.MethodPut, "/keeper/api/v1/usage/api-keys/hidden", body)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("body %s: expected 400, got %d body=%s", body, resp.Code, resp.Body.String())
		}
	}
}
