package repository

import (
	"strings"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/helper"
	"cpa-usage-keeper/internal/timeutil"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
		PullSeen   bool
		PushSeen   bool
		IsDeleted  bool
	}
	// 先在独立 reader 上比较完整 key 集合，只有变化才进入 writer 事务。
	if err := db.Clauses(dbresolver.Read).Model(&entities.CPAAPIKey{}).
		Select("id, api_key, display_key, pull_seen, push_seen, is_deleted").Find(&existingRows).Error; err != nil {
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
			if !row.PullSeen {
				fields["pull_seen"] = true
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
			PullSeen:     true,
			IsDeleted:    false,
			LastSyncedAt: &syncedAt,
		})
	}
	// 拉取清单只对自己负责：从 CPA 消失的 Key 先撤掉拉取标记；推送带来的其他 CPA 的 Key 仍保留可见，
	// 否则每轮同步都会把它们删掉、下一条推送又恢复，造成筛选和别名反复失效。
	staleIDs, unpulledIDs := make([]int64, 0), make([]int64, 0)
	for _, row := range existingRows {
		if !row.PullSeen {
			continue
		}
		if _, ok := incoming[row.APIKey]; ok {
			continue
		}
		if row.PushSeen {
			unpulledIDs = append(unpulledIDs, row.ID)
		} else {
			staleIDs = append(staleIDs, row.ID)
		}
	}
	if len(toCreate) == 0 && len(toUpdate) == 0 && len(staleIDs) == 0 && len(unpulledIDs) == 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, change := range toUpdate {
			if err := tx.Model(&entities.CPAAPIKey{}).Where("id = ?", change.id).Updates(change.fields).Error; err != nil {
				return err
			}
		}
		if len(toCreate) > 0 {
			// 快照之后推送可能刚登记了同一个 Key；冲突时只补拉取标记，不能让整轮同步因唯一键失败回滚。
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "api_key"}},
				DoUpdates: clause.Assignments(map[string]any{"pull_seen": true, "is_deleted": false, "last_synced_at": &syncedAt, "updated_at": syncedAt}),
			}).Create(&toCreate).Error; err != nil {
				return err
			}
		}
		if len(unpulledIDs) > 0 {
			if err := tx.Model(&entities.CPAAPIKey{}).Where("id IN ?", unpulledIDs).Updates(map[string]any{"pull_seen": false, "updated_at": syncedAt}).Error; err != nil {
				return err
			}
		}
		if len(staleIDs) == 0 {
			return nil
		}
		// 候选来自 reader 快照；写入前再确认 push_seen，期间刚被推送登记的 Key 只撤拉取标记，不删除。
		if err := tx.Model(&entities.CPAAPIKey{}).Where("id IN ? AND push_seen = ?", staleIDs, true).Updates(map[string]any{"pull_seen": false, "updated_at": syncedAt}).Error; err != nil {
			return err
		}
		return tx.Model(&entities.CPAAPIKey{}).Where("id IN ? AND push_seen = ?", staleIDs, false).Updates(map[string]any{"pull_seen": false, "is_deleted": true, "updated_at": syncedAt}).Error
	})
}

// RecordPushedCPAAPIKey 在推送入库的同一事务里登记 Key。只设置推送标记并恢复可见，
// 不改别名、不授予拉取标记：只经推送出现的 Key 不能用于 Key 自助登录。
func RecordPushedCPAAPIKey(tx *gorm.DB, key string, seenAt time.Time) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	at := timeutil.FormatStorageTime(seenAt)
	// 用显式 SQL：GORM 会把带 default:true 的 bool 零值替换成默认值，结构体写入无法保存 pull_seen=false。
	// 时间按 storageTime 序列化格式写入，与结构体读写保持一致。
	return tx.Exec(`INSERT INTO cpa_api_keys (api_key, display_key, key_alias, key_alias_source, pull_seen, push_seen, is_deleted, last_synced_at, created_at, updated_at)
VALUES (?, ?, '', '', 0, 1, 0, ?, ?, ?)
ON CONFLICT(api_key) DO UPDATE SET push_seen = 1, is_deleted = 0, last_synced_at = excluded.last_synced_at, updated_at = excluded.updated_at`,
		key, helper.RedactSensitiveValue(key), at, at, at).Error
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

// 以下两个查询只服务 Key 自助登录：只认拉取的 CPA 当前仍列出的 Key。其他 CPA 撤销 Key 时 Keeper 无从得知，
// 若放行推送登记的 Key，被撤销的 Key 仍能一直登录查看用量。

func FindLoginCPAAPIKeyByID(db *gorm.DB, id int64) (entities.CPAAPIKey, error) {
	var row entities.CPAAPIKey
	err := db.Where("id = ? AND is_deleted = ? AND pull_seen = ?", id, false, true).First(&row).Error
	return row, err
}

func FindLoginCPAAPIKeyByValue(db *gorm.DB, apiKey string) (entities.CPAAPIKey, error) {
	var row entities.CPAAPIKey
	err := db.Where("api_key = ? AND is_deleted = ? AND pull_seen = ?", apiKey, false, true).First(&row).Error
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

// UpdateCPAAPIKeyLocalRankingProfile 在同一写事务中保存并回读 Key 的本地展示资料。
func UpdateCPAAPIKeyLocalRankingProfile(db *gorm.DB, id int64, keyAlias string, avatarID uint8) (entities.CPAAPIKey, error) {
	var row entities.CPAAPIKey
	err := db.Clauses(dbresolver.Write).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&entities.CPAAPIKey{}).Where("id = ?", id).Updates(map[string]any{
			"key_alias":               strings.TrimSpace(keyAlias),
			"local_ranking_avatar_id": avatarID,
			"key_alias_source":        entities.CPAAPIKeyAliasSourceManual,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Where("id = ?", id).First(&row).Error
	})
	return row, err
}
