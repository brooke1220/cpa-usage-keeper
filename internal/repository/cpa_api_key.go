package repository

import (
	"strings"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/helper"

	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

func SyncCPAAPIKeys(db *gorm.DB, keys []string, syncedAt time.Time) error {
	seen := make(map[string]struct{}, len(keys))
	uniqueKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		uniqueKeys = append(uniqueKeys, key)
	}

	var existingRows []struct {
		ID         int64
		APIKey     string
		DisplayKey string
		IsDeleted  bool
	}
	// 先在独立 reader 上比较完整 key 集合，只有变化才进入 writer 事务。
	if err := db.Clauses(dbresolver.Read).Model(&entities.CPAAPIKey{}).
		Select("id, api_key, display_key, is_deleted").Find(&existingRows).Error; err != nil {
		return err
	}
	type keyUpdate struct {
		id     int64
		fields map[string]any
	}
	existingByKey := make(map[string]int, len(existingRows))
	for index, row := range existingRows {
		existingByKey[row.APIKey] = index
	}
	incoming := make(map[string]struct{}, len(uniqueKeys))
	toCreate := make([]entities.CPAAPIKey, 0)
	toUpdate := make([]keyUpdate, 0)
	for _, key := range uniqueKeys {
		incoming[key] = struct{}{}
		if index, ok := existingByKey[key]; ok {
			row := existingRows[index]
			fields := make(map[string]any)
			if display := helper.RedactSensitiveValue(key); row.DisplayKey != display {
				fields["display_key"] = display
			}
			if row.IsDeleted {
				fields["is_deleted"] = false
			}
			if len(fields) > 0 {
				fields["last_synced_at"] = &syncedAt
				fields["updated_at"] = syncedAt
				toUpdate = append(toUpdate, keyUpdate{id: row.ID, fields: fields})
			}
			continue
		}
		toCreate = append(toCreate, entities.CPAAPIKey{
			APIKey:       key,
			DisplayKey:   helper.RedactSensitiveValue(key),
			IsDeleted:    false,
			LastSyncedAt: &syncedAt,
		})
	}
	staleIDs := make([]int64, 0)
	for _, row := range existingRows {
		if row.IsDeleted {
			continue
		}
		if _, ok := incoming[row.APIKey]; !ok {
			staleIDs = append(staleIDs, row.ID)
		}
	}
	if len(toCreate) == 0 && len(toUpdate) == 0 && len(staleIDs) == 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, change := range toUpdate {
			if err := tx.Model(&entities.CPAAPIKey{}).Where("id = ?", change.id).Updates(change.fields).Error; err != nil {
				return err
			}
		}
		if len(toCreate) > 0 {
			if err := tx.Create(&toCreate).Error; err != nil {
				return err
			}
		}
		if len(staleIDs) == 0 {
			return nil
		}
		return tx.Model(&entities.CPAAPIKey{}).Where("id IN ?", staleIDs).Updates(map[string]any{"is_deleted": true, "updated_at": syncedAt}).Error
	})
}

func ListActiveCPAAPIKeys(db *gorm.DB) ([]entities.CPAAPIKey, error) {
	var rows []entities.CPAAPIKey
	err := db.Where("is_deleted = ?", false).Order("id asc").Find(&rows).Error
	return rows, err
}

func FindActiveCPAAPIKeyByID(db *gorm.DB, id int64) (entities.CPAAPIKey, error) {
	var row entities.CPAAPIKey
	err := db.Where("id = ? AND is_deleted = ?", id, false).First(&row).Error
	return row, err
}

func FindActiveCPAAPIKeyByValue(db *gorm.DB, apiKey string) (entities.CPAAPIKey, error) {
	var row entities.CPAAPIKey
	err := db.Where("api_key = ? AND is_deleted = ?", apiKey, false).First(&row).Error
	return row, err
}

// UpdateCPAAPIKeyAlias 保存管理员手动设置的别名；清空也记为手动，自动导入不会再回填。
func UpdateCPAAPIKeyAlias(db *gorm.DB, id int64, keyAlias string) error {
	result := db.Model(&entities.CPAAPIKey{}).Where("id = ? AND is_deleted = ?", id, false).Updates(map[string]any{
		"key_alias":        strings.TrimSpace(keyAlias),
		"key_alias_source": entities.CPAAPIKeyAliasSourceManual,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ImportCPAAPIKeyAliases fills only empty aliases, preserving existing aliases,
// deleted keys, and concurrent manual edits. The whole batch is committed
// atomically so a database error cannot leave a partial import.
// skipManuallyCleared is used by background imports: an alias an administrator
// explicitly cleared in Keeper stays empty until they import or edit it themselves.
func ImportCPAAPIKeyAliases(db *gorm.DB, aliases []entities.CPAAPIKeyAlias, skipManuallyCleared bool) ([]entities.CPAAPIKey, error) {
	// Normalize and deduplicate locally. The HTTP layer rejects duplicates, but
	// keeping the repository helper deterministic also protects direct callers.
	normalized := make([]entities.CPAAPIKeyAlias, 0, len(aliases))
	seen := make(map[int64]struct{}, len(aliases))
	for _, alias := range aliases {
		name := strings.TrimSpace(alias.KeyAlias)
		if alias.ID <= 0 || name == "" {
			continue
		}
		if _, ok := seen[alias.ID]; ok {
			continue
		}
		seen[alias.ID] = struct{}{}
		normalized = append(normalized, entities.CPAAPIKeyAlias{ID: alias.ID, KeyAlias: name})
	}
	if len(normalized) == 0 {
		return []entities.CPAAPIKey{}, nil
	}
	rows := make([]entities.CPAAPIKey, 0, len(normalized))
	err := db.Clauses(dbresolver.Write).Transaction(func(tx *gorm.DB) error {
		ids := make([]int64, 0, len(normalized))
		for _, alias := range normalized {
			ids = append(ids, alias.ID)
		}
		// Read eligibility once. The update repeats the empty-alias predicate so
		// a concurrent/manual write can never be overwritten.
		eligibility := "id IN ? AND is_deleted = ? AND (key_alias = ? OR key_alias IS NULL)"
		eligibilityArgs := []any{false, ""}
		if skipManuallyCleared {
			eligibility += " AND key_alias_source <> ?"
			eligibilityArgs = append(eligibilityArgs, entities.CPAAPIKeyAliasSourceManual)
		}
		var eligible []entities.CPAAPIKey
		if err := tx.Select("id").Where(eligibility, append([]any{ids}, eligibilityArgs...)...).Find(&eligible).Error; err != nil {
			return err
		}
		if len(eligible) == 0 {
			return nil
		}
		eligibleIDs := make([]int64, 0, len(eligible))
		eligibleSet := make(map[int64]struct{}, len(eligible))
		for _, row := range eligible {
			eligibleIDs = append(eligibleIDs, row.ID)
			eligibleSet[row.ID] = struct{}{}
		}
		caseSQL := "CASE id"
		caseArgs := make([]any, 0, len(eligible)*2)
		for _, alias := range normalized {
			if _, ok := eligibleSet[alias.ID]; !ok {
				continue
			}
			caseSQL += " WHEN ? THEN ?"
			caseArgs = append(caseArgs, alias.ID, alias.KeyAlias)
		}
		caseSQL += " ELSE key_alias END"
		result := tx.Model(&entities.CPAAPIKey{}).
			Where(eligibility, append([]any{eligibleIDs}, eligibilityArgs...)...).
			Updates(map[string]any{
				"key_alias":        gorm.Expr(caseSQL, caseArgs...),
				"key_alias_source": entities.CPAAPIKeyAliasSourceCPA,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		// Fetch the changed rows in one read instead of one query per alias.
		var loaded []entities.CPAAPIKey
		if err := tx.Where("id IN ?", eligibleIDs).Find(&loaded).Error; err != nil {
			return err
		}
		loadedByID := make(map[int64]entities.CPAAPIKey, len(loaded))
		for _, row := range loaded {
			loadedByID[row.ID] = row
		}
		for _, alias := range normalized {
			if _, ok := eligibleSet[alias.ID]; !ok {
				continue
			}
			if row, ok := loadedByID[alias.ID]; ok && row.KeyAlias == alias.KeyAlias {
				rows = append(rows, row)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}
