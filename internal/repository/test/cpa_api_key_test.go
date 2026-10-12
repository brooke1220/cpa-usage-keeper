package test

import (
	"errors"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"

	"gorm.io/gorm"
)

func TestSyncCPAAPIKeysCreatesRowsWithDisplayKeyAndEmptyAlias(t *testing.T) {
	db := openTestDatabase(t)
	syncedAt := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)

	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha123456"}, syncedAt); err != nil {
		t.Fatalf("SyncCPAAPIKeys returned error: %v", err)
	}

	var row entities.CPAAPIKey
	if err := db.Where("api_key = ?", "sk-alpha123456").First(&row).Error; err != nil {
		t.Fatalf("expected synced key row: %v", err)
	}
	if row.DisplayKey != "sk-*********123456" || row.KeyAlias != "" || row.IsDeleted {
		t.Fatalf("unexpected row after sync: %+v", row)
	}
	if row.LastSyncedAt == nil || !row.LastSyncedAt.Equal(syncedAt) {
		t.Fatalf("unexpected last synced at: %+v", row.LastSyncedAt)
	}
}

func TestSyncCPAAPIKeysPreservesAliasAndMarksMissingRowsDeleted(t *testing.T) {
	db := openTestDatabase(t)
	firstSync := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	secondSync := firstSync.Add(time.Hour)

	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha123456", "sk-beta654321"}, firstSync); err != nil {
		t.Fatalf("initial sync returned error: %v", err)
	}
	if err := repository.UpdateCPAAPIKeyAlias(db, 1, "Primary Key"); err != nil {
		t.Fatalf("UpdateCPAAPIKeyAlias returned error: %v", err)
	}
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha123456"}, secondSync); err != nil {
		t.Fatalf("second sync returned error: %v", err)
	}

	var active entities.CPAAPIKey
	if err := db.Where("api_key = ?", "sk-alpha123456").First(&active).Error; err != nil {
		t.Fatalf("expected active key: %v", err)
	}
	if active.KeyAlias != "Primary Key" || active.IsDeleted {
		t.Fatalf("expected alias to be preserved on active row, got %+v", active)
	}

	var deleted entities.CPAAPIKey
	if err := db.Where("api_key = ?", "sk-beta654321").First(&deleted).Error; err != nil {
		t.Fatalf("expected deleted key: %v", err)
	}
	if !deleted.IsDeleted {
		t.Fatalf("expected missing key to be marked deleted: %+v", deleted)
	}
}

func TestSyncCPAAPIKeysRestoresDeletedRowsAndDeduplicatesInput(t *testing.T) {
	db := openTestDatabase(t)

	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha123456"}, time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("initial sync returned error: %v", err)
	}
	if err := repository.UpdateCPAAPIKeyAlias(db, 1, "Primary Key"); err != nil {
		t.Fatalf("UpdateCPAAPIKeyAlias returned error: %v", err)
	}
	if err := repository.SyncCPAAPIKeys(db, nil, time.Date(2026, 5, 13, 11, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("empty sync returned error: %v", err)
	}
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha123456", "sk-alpha123456"}, time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("restore sync returned error: %v", err)
	}

	var rows []entities.CPAAPIKey
	if err := db.Where("api_key = ?", "sk-alpha123456").Find(&rows).Error; err != nil {
		t.Fatalf("query rows returned error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected deduplicated row count 1, got %d", len(rows))
	}
	if rows[0].IsDeleted || rows[0].KeyAlias != "Primary Key" {
		t.Fatalf("expected restored row to preserve alias, got %+v", rows[0])
	}
}

func TestCPAAPIKeyQueriesFilterDeletedRows(t *testing.T) {
	db := openTestDatabase(t)

	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha123456", "sk-beta654321"}, time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("sync returned error: %v", err)
	}
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha123456"}, time.Date(2026, 5, 13, 11, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("second sync returned error: %v", err)
	}

	rows, err := repository.ListActiveCPAAPIKeys(db)
	if err != nil {
		t.Fatalf("ListActiveCPAAPIKeys returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].APIKey != "sk-alpha123456" {
		t.Fatalf("unexpected active rows: %+v", rows)
	}

	_, err = repository.FindActiveCPAAPIKeyByID(db, 2)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected deleted id to be hidden, got %v", err)
	}

	row, err := repository.FindActiveCPAAPIKeyByValue(db, "sk-alpha123456")
	if err != nil || row.ID != 1 {
		t.Fatalf("expected active key lookup by value to return row 1, got %+v err=%v", row, err)
	}
	_, err = repository.FindActiveCPAAPIKeyByValue(db, "sk-beta654321")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected deleted value lookup to be hidden, got %v", err)
	}
}

func TestSyncCPAAPIKeysDoesNotConsumeIDsForExistingKeys(t *testing.T) {
	db := openTestDatabase(t)

	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha123456"}, time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("initial sync returned error: %v", err)
	}
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha123456"}, time.Date(2026, 5, 13, 11, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("repeat sync returned error: %v", err)
	}
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha123456", "sk-beta654321"}, time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("new key sync returned error: %v", err)
	}

	var row entities.CPAAPIKey
	if err := db.Where("api_key = ?", "sk-beta654321").First(&row).Error; err != nil {
		t.Fatalf("expected new key row: %v", err)
	}
	if row.ID != 2 {
		t.Fatalf("expected second key id to be 2 without upsert sequence burn, got %d", row.ID)
	}
}

func TestImportCPAAPIKeyAliasesPreservesExistingAliasesAndDeletedRows(t *testing.T) {
	db := openTestDatabase(t)
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha", "sk-beta", "sk-deleted"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateCPAAPIKeyAlias(db, 2, "Keeper alias"); err != nil {
		t.Fatal(err)
	}
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha", "sk-beta"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	aliases := []entities.CPAAPIKeyAlias{{ID: 1, KeyAlias: "  彭波庭  "}, {ID: 2, KeyAlias: "CPA replacement"}, {ID: 3, KeyAlias: "Deleted"}, {ID: 999, KeyAlias: "Missing"}}
	rows, err := repository.ImportCPAAPIKeyAliases(db, aliases, false)
	if err != nil || len(rows) != 1 || rows[0].ID != 1 || rows[0].KeyAlias != "彭波庭" || rows[0].KeyAliasSource != entities.CPAAPIKeyAliasSourceCPA {
		t.Fatalf("unexpected imported aliases: %+v err=%v", rows, err)
	}
	row, err := repository.FindActiveCPAAPIKeyByID(db, 2)
	if err != nil || row.KeyAlias != "Keeper alias" {
		t.Fatalf("existing alias overwritten: %+v err=%v", row, err)
	}
	rows, err = repository.ImportCPAAPIKeyAliases(db, aliases, false)
	if err != nil || len(rows) != 0 {
		t.Fatalf("repeated import should be a no-op: %+v err=%v", rows, err)
	}
}

func TestImportCPAAPIKeyAliasesBackgroundModeKeepsManuallyClearedAliases(t *testing.T) {
	db := openTestDatabase(t)
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha", "sk-beta"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	// 管理员清空 key 1 的别名；key 2 从未设置过。
	if err := repository.UpdateCPAAPIKeyAlias(db, 1, "  "); err != nil {
		t.Fatal(err)
	}
	cleared, err := repository.FindActiveCPAAPIKeyByID(db, 1)
	if err != nil || cleared.KeyAlias != "" || cleared.KeyAliasSource != entities.CPAAPIKeyAliasSourceManual {
		t.Fatalf("clearing an alias must be recorded as a manual choice: %+v err=%v", cleared, err)
	}
	aliases := []entities.CPAAPIKeyAlias{{ID: 1, KeyAlias: "CPA alpha"}, {ID: 2, KeyAlias: "CPA beta"}}
	rows, err := repository.ImportCPAAPIKeyAliases(db, aliases, true)
	if err != nil || len(rows) != 1 || rows[0].ID != 2 || rows[0].KeyAlias != "CPA beta" {
		t.Fatalf("background import must skip the manually cleared alias: %+v err=%v", rows, err)
	}
	if row, err := repository.FindActiveCPAAPIKeyByID(db, 1); err != nil || row.KeyAlias != "" {
		t.Fatalf("manually cleared alias was refilled: %+v err=%v", row, err)
	}
	// 用户在导入弹窗中主动确认时，空别名仍然可以填充。
	rows, err = repository.ImportCPAAPIKeyAliases(db, aliases, false)
	if err != nil || len(rows) != 1 || rows[0].ID != 1 || rows[0].KeyAlias != "CPA alpha" || rows[0].KeyAliasSource != entities.CPAAPIKeyAliasSourceCPA {
		t.Fatalf("explicit import should fill the cleared alias: %+v err=%v", rows, err)
	}
}

func TestImportCPAAPIKeyAliasesRollsBackAllChangesOnFailure(t *testing.T) {
	db := openTestDatabase(t)
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha", "sk-beta"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TRIGGER reject_second_alias BEFORE UPDATE OF key_alias ON cpa_api_keys WHEN NEW.id = 2 BEGIN SELECT RAISE(ABORT, 'test alias failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	rows, err := repository.ImportCPAAPIKeyAliases(db, []entities.CPAAPIKeyAlias{{ID: 1, KeyAlias: "Alpha"}, {ID: 2, KeyAlias: "Beta"}}, false)
	if err == nil || rows != nil {
		t.Fatalf("expected atomic import failure, got %+v err=%v", rows, err)
	}
	row, err := repository.FindActiveCPAAPIKeyByID(db, 1)
	if err != nil || row.KeyAlias != "" {
		t.Fatalf("failed import was not rolled back: %+v err=%v", row, err)
	}
}
