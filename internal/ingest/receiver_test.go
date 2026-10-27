package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/pricing"
	"cpa-usage-keeper/internal/repository"
	repodto "cpa-usage-keeper/internal/repository/dto"
	"cpa-usage-keeper/internal/service"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func testReceiver(t *testing.T) (*Receiver, *gorm.DB) {
	t.Helper()
	cfg := config.Config{SQLitePath: filepath.Join(t.TempDir(), "push.db"), IngestMode: "push"}
	db, err := repository.OpenDatabase(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sources := []config.PushSource{}
	for _, v := range []struct{ id, token string }{{"cpa-one", strings.Repeat("a", 32)}, {"cpa-two", strings.Repeat("b", 32)}} {
		hash := sha256.Sum256([]byte(v.token))
		sources = append(sources, config.PushSource{SourceID: v.id, TokenSHA256: hex.EncodeToString(hash[:])})
	}
	receiver, err := New(db, sources)
	if err != nil {
		t.Fatal(err)
	}
	return receiver, db
}

func fixture() json.RawMessage {
	return json.RawMessage(`{"timestamp":"2026-10-26T01:00:00Z","provider":"codex","executor_type":"CodexExecutor","auth_type":"api_key","auth_index":"same-credential","source":"upstream-account","model":"model-a","api_key":"sk-shared-client-key","request_id":"same-parent-request","execution_id":"execution-1","tokens":{"input_tokens":100,"output_tokens":40,"reasoning_tokens":10,"cached_tokens":20,"cache_read_tokens":20,"cache_read_tokens_present":true,"cache_creation_tokens":0,"total_tokens":140},"generate":true,"failed":false,"stream":true,"latency_ms":1000,"ttft_ms":200,"fail":{"status_code":200},"accounting_version":2,"token_breakdown":{"schema_version":2,"quality":"complete","total_tokens":140,"unclassified_tokens":0,"input":{"total_tokens":100,"uncached_tokens":80,"cache_read_tokens":20,"cache_write_tokens":0},"output":{"total_tokens":40,"non_reasoning_tokens":30,"reasoning_tokens":10}}}`)
}
func send(t *testing.T, r *Receiver, source, token string, events []Event) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(Batch{1, source, events})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/ingest/usage", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
func count(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var n int64
	if err := db.Model(model).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReceiverIdempotencyAndAtomicConflict(t *testing.T) {
	r, db := testReceiver(t)
	e := Event{"delivery-1", fixture()}
	for i := 0; i < 3; i++ {
		w := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{e, e})
		if w.Code != 200 {
			t.Fatalf("accept: %d %s", w.Code, w.Body)
		}
	}
	if count(t, db, &entities.PushReceipt{}) != 1 || count(t, db, &entities.RedisUsageInbox{}) != 1 {
		t.Fatal("duplicate transport created another inbox")
	}
	changed := Event{e.ID, bytes.ReplaceAll(e.Usage, []byte("model-a"), []byte("model-b"))}
	w := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"new-before-conflict", fixture()}, changed})
	if w.Code != 409 {
		t.Fatalf("conflict response: %d %s", w.Code, w.Body)
	}
	if count(t, db, &entities.PushReceipt{}) != 1 || count(t, db, &entities.RedisUsageInbox{}) != 1 {
		t.Fatal("partial batch committed before conflict")
	}
}

func TestReceiverConcurrentRetryAndSourceNamespaces(t *testing.T) {
	r, db := testReceiver(t)
	e := Event{"delivery-1", fixture()}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{e})
			if w.Code != 200 {
				t.Errorf("concurrent response: %d %s", w.Code, w.Body)
			}
		}()
	}
	close(start)
	wg.Wait()
	w := send(t, r, "cpa-two", strings.Repeat("b", 32), []Event{e})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if count(t, db, &entities.RedisUsageInbox{}) != 2 || count(t, db, &entities.CPAAPIKey{}) != 1 {
		t.Fatal("source dedup or shared key merge failed")
	}
}

func TestReceiverAuthenticationValidationAndWhitelist(t *testing.T) {
	r, db := testReceiver(t)
	cases := []struct {
		name, source, token string
		raw                 json.RawMessage
		status              int
	}{
		{"unauthorized", "cpa-one", strings.Repeat("x", 32), fixture(), 401},
		{"spoofed-source", "cpa-two", strings.Repeat("a", 32), fixture(), 403},
		{"missing-execution", "cpa-one", strings.Repeat("a", 32), bytes.ReplaceAll(fixture(), []byte(`"execution-1"`), []byte(`""`)), 422},
		{"negative-tokens", "cpa-one", strings.Repeat("a", 32), bytes.ReplaceAll(fixture(), []byte(`"total_tokens":140`), []byte(`"total_tokens":-1`)), 422},
		{"unknown-secret-field", "cpa-one", strings.Repeat("a", 32), append([]byte(`{"response_headers":{"Authorization":"secret"},`), fixture()[1:]...), 422},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := send(t, r, tc.source, tc.token, []Event{{"delivery", tc.raw}})
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
	if count(t, db, &entities.PushReceipt{}) != 0 {
		t.Fatal("rejected events persisted")
	}
	req := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat(" ", MaxBatchBytes)+"{}"))
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 413 {
		t.Fatalf("oversize: %d", w.Code)
	}
}

func TestPushProcessingParityKeysAndPrices(t *testing.T) {
	r, db := testReceiver(t)
	if _, err := repository.UpsertModelPriceSetting(db, repodto.ModelPriceSettingInput{Model: "model-a", PromptPricePer1M: 2, CompletionPricePer1M: 6, CacheReadPricePer1M: 0.5}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.LoadPricingSnapshot(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	catalog := pricing.NewCatalog(snapshot)
	processor := service.NewSyncServiceWithOptions(db, service.SyncServiceOptions{PricingCatalog: catalog})
	for _, source := range []struct{ id, token string }{{"cpa-one", strings.Repeat("a", 32)}, {"cpa-two", strings.Repeat("b", 32)}} {
		for i := 0; i < 2; i++ {
			w := send(t, r, source.id, source.token, []Event{{"delivery-1", fixture()}, {"delivery-2", fixture()}})
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
		}
	}
	var key entities.CPAAPIKey
	if err := db.First(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&key).Updates(map[string]any{"key_alias": "Manually named", "key_alias_source": "manual"}).Error; err != nil {
		t.Fatal(err)
	}
	w := send(t, r, "cpa-two", strings.Repeat("b", 32), []Event{{"delivery-3", bytes.ReplaceAll(fixture(), []byte("sk-shared-client-key"), []byte("sk-another-client-key"))}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, err := repository.InsertRedisUsageInboxMessages(db, []repodto.RedisInboxInsert{{Source: "redis_pull:usage", RawMessage: string(fixture()), PoppedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if _, err := processor.ProcessRedisUsageInbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	var events []entities.UsageEvent
	if err := db.Order("id").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 {
		t.Fatalf("execution records lost: %d", len(events))
	}
	expected := events[5]
	for _, e := range events[:5] {
		if e.TotalTokens != expected.TotalTokens || e.InputTokens != expected.InputTokens || e.OutputTokens != expected.OutputTokens || e.CacheReadTokens != expected.CacheReadTokens || e.ReasoningTokens != expected.ReasoningTokens || e.Failed != expected.Failed || e.CostUSD == nil || expected.CostUSD == nil || math.Abs(*e.CostUSD-*expected.CostUSD) > 1e-12 || e.CostAvailable == nil || !*e.CostAvailable {
			t.Fatalf("push/pull parity failed: push=%+v pull=%+v", e, expected)
		}
	}
	if events[0].AuthIndex == events[2].AuthIndex || events[0].Source != "upstream-account" || events[0].SourceID == "" {
		t.Fatal("upstream identity collided or source was overwritten")
	}
	if count(t, db, &entities.CPAAPIKey{}) != 2 {
		t.Fatal("cross-source key catalog incorrect")
	}
	if err := db.First(&key, key.ID).Error; err != nil || key.KeyAlias != "Manually named" || key.KeyAliasSource != "manual" {
		t.Fatal("manual alias was overwritten")
	}
	var totals struct {
		Count  int64
		Tokens int64
	}
	if err := db.Model(&entities.UsageEvent{}).Select("COUNT(*) AS count, SUM(total_tokens) AS tokens").Where("api_group_key=?", "sk-shared-client-key").Scan(&totals).Error; err != nil {
		t.Fatal(err)
	}
	if totals.Count != 5 || totals.Tokens != 700 {
		t.Fatalf("key aggregate incorrect: %+v", totals)
	}
	if _, err := repository.CleanupRedisUsageInbox(db, time.Now().AddDate(0, 0, 30)); err != nil {
		t.Fatal(err)
	}
	if count(t, db, &entities.PushReceipt{}) != 5 {
		t.Fatal("receipt removed by normal retention")
	}
	w = send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"delivery-1", fixture()}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if count(t, db, &entities.RedisUsageInbox{}) != 0 {
		t.Fatal("late replay created new inbox after cleanup")
	}
}

func TestPushQuarantineSurvivesCleanupAndReplays(t *testing.T) {
	r, db := testReceiver(t)
	w := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"delivery", fixture()}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var row entities.RedisUsageInbox
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := repository.MarkRedisUsageInboxProcessFailed(db, row.ID, errors.New("temporary storage issue")); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.First(&row, row.ID).Error; err != nil || row.Status != "quarantined" {
		t.Fatalf("failed data was not retained: %+v %v", row, err)
	}
	if _, err := repository.CleanupRedisUsageInbox(db, time.Now().AddDate(0, 0, 30)); err != nil {
		t.Fatal(err)
	}
	items, err := r.Quarantine(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("quarantine lost: %v %v", items, err)
	}
	if ok, err := r.Retry(context.Background(), row.ID); err != nil || !ok {
		t.Fatal(err)
	}
	if count(t, db, &entities.PushReceipt{}) != 1 {
		t.Fatal("retry changed transport identity")
	}
	stats, err := r.Status(context.Background())
	if err != nil || stats.Pending != 1 || stats.Quarantined != 0 {
		t.Fatalf("retry status wrong: %+v %v", stats, err)
	}
}

func TestPushDurabilityAcrossConnectionReplacement(t *testing.T) {
	_, db := testReceiver(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxIdleConns(0)
	for i := 0; i < 2; i++ {
		var journal string
		if err := db.Raw("PRAGMA journal_mode").Scan(&journal).Error; err != nil || journal != "wal" {
			t.Fatalf("replacement writer lost WAL: %s %v", journal, err)
		}
		var mode int
		if err := db.Raw("PRAGMA synchronous").Scan(&mode).Error; err != nil || mode != 2 {
			t.Fatalf("replacement writer lost FULL: %d %v", mode, err)
		}
	}
}

func TestPushSQLDiagnosticsExcludeBoundSecrets(t *testing.T) {
	_, db := testReceiver(t)
	previous := log.StandardLogger().Out
	var captured bytes.Buffer
	log.SetOutput(&captured)
	defer log.SetOutput(previous)
	if err := db.Exec("INSERT INTO missing_usage_table(api_key,raw_message) VALUES (?,?)", "sk-private-query-secret", "raw-private-prompt-body").Error; err == nil {
		t.Fatal("expected diagnostic failure")
	}
	for _, secret := range []string{"sk-private-query-secret", "raw-private-prompt-body"} {
		if strings.Contains(captured.String(), secret) {
			t.Fatalf("SQL diagnostics leaked %s", secret)
		}
	}
	if !strings.Contains(captured.String(), "missing_usage_table") {
		t.Fatal("SQL diagnostic was completely suppressed")
	}
}

func TestPushSessionTreeFieldsReachUsageEvents(t *testing.T) {
	r, db := testReceiver(t)
	raw := append([]byte(`{"node_kind":"subagent","is_fork":true,"is_compaction":true,`), fixture()[1:]...)
	if w := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"delivery-tree", raw}}); w.Code != 200 {
		t.Fatalf("session tree fields rejected: %d %s", w.Code, w.Body)
	}
	snapshot, err := repository.LoadPricingSnapshot(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	processor := service.NewSyncServiceWithOptions(db, service.SyncServiceOptions{PricingCatalog: pricing.NewCatalog(snapshot)})
	if _, err := processor.ProcessRedisUsageInbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	var event entities.UsageEvent
	if err := db.First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.NodeKind == nil || *event.NodeKind != "subagent" || event.IsFork == nil || !*event.IsFork || event.IsCompaction == nil || !*event.IsCompaction {
		t.Fatalf("session tree fields lost: %+v", event)
	}
	if stats, err := r.Status(context.Background()); err != nil || stats.LastReceivedAt == nil {
		t.Fatalf("status lost last receipt: %+v %v", stats, err)
	}
}

func TestStatusLastReceiptSurvivesOffsetChange(t *testing.T) {
	r, db := testReceiver(t)
	// The later receipt sorts first as text because the process offset changed between them.
	for _, row := range []struct{ id, at string }{{"before-tz-change", "2026-10-26T10:00:00+08:00"}, {"after-tz-change", "2026-10-26T03:00:00Z"}} {
		if err := db.Exec("INSERT INTO push_receipts(source_id,delivery_event_id,payload_hash,inbox_id,received_at) VALUES(?,?,?,?,?)", "cpa-one", row.id, "hash", 0, row.at).Error; err != nil {
			t.Fatal(err)
		}
	}
	stats, err := r.Status(context.Background())
	if err != nil || stats.LastReceivedAt == nil {
		t.Fatalf("status lost last receipt: %+v %v", stats, err)
	}
	if want := time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC); !stats.LastReceivedAt.Equal(want) {
		t.Fatalf("last receipt = %v, want %v", stats.LastReceivedAt, want)
	}
}
