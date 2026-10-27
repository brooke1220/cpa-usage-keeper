package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/service"
	"gorm.io/gorm"
)

// sendWithoutSource 模拟新插件：请求体不再携带 source_id，来源完全由 token 决定。
func sendWithoutSource(t *testing.T, r *Receiver, token string, events []Event) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"schema_version": 1, "events": events})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/ingest/usage", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestIssuedTokenIdentifiesSourceAndSurvivesRename(t *testing.T) {
	db := openIngestTestDB(t)
	sources, err := NewSources(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(db, sources)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := sources.Create(context.Background(), "  北京机房 CPA  ")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(issued.Token) || !regexp.MustCompile(`^src_[0-9a-f]{16}$`).MatchString(issued.SourceID) {
		t.Fatalf("issued credential shape: %+v", issued)
	}
	if issued.Name != "北京机房 CPA" || issued.TokenHint != issued.Token[60:] {
		t.Fatalf("name or hint not normalized: %+v", issued)
	}
	var stored entities.PushSource
	if err := db.First(&stored, issued.ID).Error; err != nil || strings.Contains(stored.TokenSHA256, issued.Token) || stored.TokenSHA256 == issued.Token {
		t.Fatalf("token stored in plaintext: %+v %v", stored, err)
	}

	// 开关默认关闭：插件保留数据并重试，打开后补传。
	if w := sendWithoutSource(t, r, issued.Token, []Event{{"delivery-1", fixture()}}); w.Code != 503 || !strings.Contains(w.Body.String(), "push_ingest_disabled") {
		t.Fatalf("disabled receiver: %d %s", w.Code, w.Body)
	}
	if err := sources.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if w := sendWithoutSource(t, r, issued.Token, []Event{{"delivery-1", fixture()}}); w.Code != 200 {
		t.Fatalf("token without source_id: %d %s", w.Code, w.Body)
	}
	// 旧插件仍发送 source_id，只要与 token 一致即可。
	if w := send(t, r, issued.SourceID, issued.Token, []Event{{"delivery-2", fixture()}}); w.Code != 200 {
		t.Fatalf("legacy envelope: %d %s", w.Code, w.Body)
	}
	if w := send(t, r, "someone-else", issued.Token, []Event{{"delivery-3", fixture()}}); w.Code != 403 {
		t.Fatalf("spoofed legacy source: %d %s", w.Code, w.Body)
	}

	renamed, err := sources.Rename(context.Background(), issued.ID, "上海机房")
	if err != nil || renamed.SourceID != issued.SourceID || renamed.Name != "上海机房" {
		t.Fatalf("rename changed identity: %+v %v", renamed, err)
	}
	// 改名后重放同一事件仍按原来源去重，不会重复计数。
	if w := sendWithoutSource(t, r, issued.Token, []Event{{"delivery-1", fixture()}}); w.Code != 200 {
		t.Fatalf("replay after rename: %d %s", w.Code, w.Body)
	}
	if count(t, db, &entities.PushReceipt{}) != 2 || count(t, db, &entities.RedisUsageInbox{}) != 2 {
		t.Fatal("rename broke delivery deduplication")
	}
	var receipt entities.PushReceipt
	if err := db.First(&receipt).Error; err != nil || receipt.SourceID != issued.SourceID {
		t.Fatalf("receipt not attributed to token source: %+v %v", receipt, err)
	}
	if _, err := sources.Create(context.Background(), "   "); !errors.Is(err, ErrInvalidSourceName) {
		t.Fatalf("blank name accepted: %v", err)
	}
	if _, err := sources.Create(context.Background(), strings.Repeat("名", 65)); !errors.Is(err, ErrInvalidSourceName) {
		t.Fatalf("overlong name accepted: %v", err)
	}
}

func TestRotateRevokeRestoreAndDelete(t *testing.T) {
	db := openIngestTestDB(t)
	sources, err := NewSources(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := sources.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	r, err := New(db, sources)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := sources.Create(context.Background(), "cpa")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := sources.Rotate(context.Background(), issued.ID)
	if err != nil || rotated.Token == issued.Token || rotated.SourceID != issued.SourceID {
		t.Fatalf("rotate: %+v %v", rotated, err)
	}
	if w := sendWithoutSource(t, r, issued.Token, []Event{{"delivery-1", fixture()}}); w.Code != 401 {
		t.Fatalf("old token still accepted: %d", w.Code)
	}
	if w := sendWithoutSource(t, r, rotated.Token, []Event{{"delivery-1", fixture()}}); w.Code != 200 {
		t.Fatalf("rotated token rejected: %d %s", w.Code, w.Body)
	}
	if err := sources.Delete(context.Background(), issued.ID); !errors.Is(err, ErrSourceActive) {
		t.Fatalf("active source deleted: %v", err)
	}
	if _, err := sources.Revoke(context.Background(), issued.ID); err != nil {
		t.Fatal(err)
	}
	if w := sendWithoutSource(t, r, rotated.Token, []Event{{"delivery-2", fixture()}}); w.Code != 401 {
		t.Fatalf("revoked token accepted: %d", w.Code)
	}
	if _, err := sources.Restore(context.Background(), issued.ID); err != nil {
		t.Fatal(err)
	}
	if w := sendWithoutSource(t, r, rotated.Token, []Event{{"delivery-2", fixture()}}); w.Code != 200 {
		t.Fatalf("restored token rejected: %d", w.Code)
	}
	if _, err := sources.Revoke(context.Background(), issued.ID); err != nil {
		t.Fatal(err)
	}
	if err := sources.Delete(context.Background(), issued.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sources.Rename(context.Background(), issued.ID, "gone"); !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("deleted source still editable: %v", err)
	}
	// 删除来源登记不删除已入库数据和去重凭据。
	if count(t, db, &entities.PushReceipt{}) != 2 || count(t, db, &entities.RedisUsageInbox{}) != 2 {
		t.Fatal("deleting a source removed received data")
	}
	items, err := sources.List(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("deleted source listed: %+v %v", items, err)
	}
}

// 修改已提交但重载失败时，旧快照不能继续放行刚吊销的 token。
func TestFailedReloadDeniesUploads(t *testing.T) {
	db := openIngestTestDB(t)
	sources, err := NewSources(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := sources.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	revoked, err := sources.Create(context.Background(), "to revoke")
	if err != nil {
		t.Fatal(err)
	}
	broken, err := sources.Create(context.Background(), "broken")
	if err != nil {
		t.Fatal(err)
	}
	// 另一个仍有效的来源数据损坏，吊销提交后的重载因此失败。
	if err := db.Model(&entities.PushSource{}).Where("id = ?", broken.ID).Update("token_sha256", "corrupt").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := sources.Revoke(context.Background(), revoked.ID); err == nil {
		t.Fatal("reload failure was not reported")
	}
	if _, _, ok := sources.authenticate(revoked.Token); ok || sources.Enabled() {
		t.Fatal("stale snapshot kept accepting a revoked token after a failed reload")
	}
}

// 管理员关闭页面会取消请求；已提交的吊销必须生效，其他来源也不能因此被全部拒收。
func TestCanceledAdminRequestStillAppliesCommittedChange(t *testing.T) {
	db := openIngestTestDB(t)
	sources, err := NewSources(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := sources.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	revoked, err := sources.Create(context.Background(), "to revoke")
	if err != nil {
		t.Fatal(err)
	}
	kept, err := sources.Create(context.Background(), "kept")
	if err != nil {
		t.Fatal(err)
	}
	// 回调在 UPDATE 提交后、回读前取消请求，模拟浏览器在提交瞬间断开。
	ctx, cancel := context.WithCancel(context.Background())
	const callback = "test:cancel_after_revoke"
	if err := db.Callback().Update().After("gorm:update").Register(callback, func(*gorm.DB) { cancel() }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
	_, _ = sources.Revoke(ctx, revoked.ID)
	if _, _, ok := sources.authenticate(revoked.Token); ok {
		t.Fatal("revoked token still accepted after the request was canceled")
	}
	if _, _, ok := sources.authenticate(kept.Token); !ok || !sources.Enabled() {
		t.Fatal("a canceled request disabled receiving for every source")
	}
}

func TestLastUploadPersistsWithTheReceipt(t *testing.T) {
	r, db := testReceiver(t)
	if w := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"delivery-1", fixture()}}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var row entities.PushSource
	if err := db.Where("source_id = ?", "cpa-one").First(&row).Error; err != nil || row.LastUsedAt == nil {
		t.Fatalf("last upload not stored with the receipt: %+v %v", row, err)
	}
	first := *row.LastUsedAt
	// 一分钟内的后续上传只合并，不再写库。
	if w := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"delivery-2", fixture()}}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := db.Where("source_id = ?", "cpa-one").First(&row).Error; err != nil || !row.LastUsedAt.Equal(first) {
		t.Fatalf("last upload rewritten within a minute: %+v %v", row.LastUsedAt, err)
	}
}

func TestSourcesReloadPersistedStateAfterRestart(t *testing.T) {
	db := openIngestTestDB(t)
	first, err := NewSources(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := first.Create(context.Background(), "cpa")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewSources(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.Enabled() {
		t.Fatal("enabled switch lost after restart")
	}
	if _, sourceID, ok := restarted.authenticate(issued.Token); !ok || sourceID != issued.SourceID {
		t.Fatal("issued token lost after restart")
	}
}

func TestPullSyncKeepsPushedKeysAndLoginStaysPullOnly(t *testing.T) {
	r, db := testReceiver(t)
	keys := service.NewCPAAPIKeyService(db)
	// 拉取的 CPA 只有 local-key；其他 CPA 推送带来 sk-shared-client-key。
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-local-pulled-key"}, timeNow()); err != nil {
		t.Fatal(err)
	}
	if w := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"delivery-1", fixture()}}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for i := 0; i < 3; i++ {
		if err := repository.SyncCPAAPIKeys(db, []string{"sk-local-pulled-key"}, timeNow()); err != nil {
			t.Fatal(err)
		}
	}
	active, err := repository.ListActiveCPAAPIKeys(db)
	if err != nil || len(active) != 2 {
		t.Fatalf("pull sync deleted a pushed key: %+v %v", active, err)
	}
	if _, err := keys.FindActiveCPAAPIKeyByValue(context.Background(), "sk-shared-client-key"); err == nil {
		t.Fatal("push-only key can log in")
	}
	if _, err := keys.FindActiveCPAAPIKeyByValue(context.Background(), "sk-local-pulled-key"); err != nil {
		t.Fatalf("pulled key cannot log in: %v", err)
	}

	// 同一个 Key 也出现在拉取清单里时可以登录；离开拉取清单后登录失效但仍保留展示。
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-local-pulled-key", "sk-shared-client-key"}, timeNow()); err != nil {
		t.Fatal(err)
	}
	shared, err := keys.FindActiveCPAAPIKeyByValue(context.Background(), "sk-shared-client-key")
	if err != nil {
		t.Fatalf("key listed by pulled CPA cannot log in: %v", err)
	}
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-local-pulled-key"}, timeNow()); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.FindActiveCPAAPIKeyByID(context.Background(), shared.ID); err == nil {
		t.Fatal("viewer session survived removal from the pulled CPA")
	}
	if row, err := repository.FindActiveCPAAPIKeyByID(db, shared.ID); err != nil || !row.PushSeen || row.PullSeen {
		t.Fatalf("pushed key provenance wrong: %+v %v", row, err)
	}
	// 推送不会把已离开拉取清单的 Key 重新授予登录资格。
	if w := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"delivery-2", fixture()}}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, err := keys.FindActiveCPAAPIKeyByID(context.Background(), shared.ID); err == nil {
		t.Fatal("push re-granted login")
	}

	// 只经拉取出现、又从拉取清单消失的 Key 照旧标记删除。
	if err := repository.SyncCPAAPIKeys(db, nil, timeNow()); err != nil {
		t.Fatal(err)
	}
	active, err = repository.ListActiveCPAAPIKeys(db)
	if err != nil || len(active) != 1 || active[0].APIKey != "sk-shared-client-key" {
		t.Fatalf("pull-only key not deleted or pushed key lost: %+v %v", active, err)
	}
}

// 拉取同步读取快照之后、开启写事务之前，推送刚登记了同一个 Key：整轮同步不能因唯一键冲突回滚，其他新 Key 照常创建。
// 写连接只有一条，推送无法插进同步事务内部；真实竞态窗口就在 reader 快照与写事务之间。
func TestPullSyncSurvivesKeyPushedAfterSnapshot(t *testing.T) {
	r, db := testReceiver(t)
	const callback = "test:push_after_snapshot"
	pushed := false
	if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if pushed || tx.Statement.Table != "cpa_api_keys" {
			return
		}
		pushed = true
		if w := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"delivery-race", fixture()}}); w.Code != 200 {
			t.Errorf("concurrent push: %d %s", w.Code, w.Body)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-shared-client-key", "sk-other"}, timeNow()); err != nil {
		t.Fatalf("pull sync failed on a concurrently pushed key: %v", err)
	}
	if !pushed {
		t.Fatal("test did not inject the concurrent push")
	}
	active, err := repository.ListActiveCPAAPIKeys(db)
	if err != nil || len(active) != 2 {
		t.Fatalf("sync lost keys: %+v %v", active, err)
	}
	for _, row := range active {
		if !row.PullSeen {
			t.Fatalf("pulled key lacks pull provenance: %+v", row)
		}
	}
}

func TestStatusReportsPerSourceLastUpload(t *testing.T) {
	r, _ := testReceiver(t)
	if w := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"delivery-1", fixture()}}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	stats, err := r.Status(context.Background())
	if err != nil || !stats.Enabled || len(stats.Sources) != 2 {
		t.Fatalf("status: %+v %v", stats, err)
	}
	if stats.Sources[0].Name != "cpa-one" || stats.Sources[0].LastUsedAt == nil || stats.Sources[1].LastUsedAt != nil {
		t.Fatalf("per-source last upload wrong: %+v", stats.Sources)
	}
}

func timeNow() time.Time { return time.Now() }
