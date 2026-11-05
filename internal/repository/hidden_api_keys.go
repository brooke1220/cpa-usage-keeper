package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

// HiddenAPIKeyIDsSettingKey 保存设置页选中的 Key；用量和排行页读取时排除这些 Key，不删除原始数据。
const HiddenAPIKeyIDsSettingKey = "usage.hidden_api_key_ids"

// LoadHiddenAPIKeyIDs 返回已隐藏的 Key ID 集合；未保存设置时为空集合。
func LoadHiddenAPIKeyIDs(ctx context.Context, db *gorm.DB) (map[int64]struct{}, error) {
	ids, err := ListHiddenAPIKeyIDs(ctx, db)
	if err != nil {
		return nil, err
	}
	hidden := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		hidden[id] = struct{}{}
	}
	return hidden, nil
}

// ListHiddenAPIKeyIDs 按 ID 升序返回已隐藏的 Key。
func ListHiddenAPIKeyIDs(ctx context.Context, db *gorm.DB) ([]int64, error) {
	setting, found, err := GetAppSetting(ctx, db, HiddenAPIKeyIDsSettingKey)
	if err != nil {
		return nil, err
	}
	if !found || setting.Value == nil {
		return []int64{}, nil
	}
	var ids []int64
	if err := json.Unmarshal([]byte(*setting.Value), &ids); err != nil {
		return nil, fmt.Errorf("decode hidden api key ids: %w", err)
	}
	return normalizeHiddenAPIKeyIDs(ids), nil
}

// SaveHiddenAPIKeyIDs 只保存仍存在于 cpa_api_keys 的 ID（含已删除的 Key，它们仍可能有历史用量）。
func SaveHiddenAPIKeyIDs(ctx context.Context, db *gorm.DB, ids []int64) ([]int64, error) {
	if db == nil {
		return nil, fmt.Errorf("database is nil")
	}
	normalized := normalizeHiddenAPIKeyIDs(ids)
	existing := make([]int64, 0, len(normalized))
	if len(normalized) > 0 {
		if err := db.WithContext(ctx).Model(&entities.CPAAPIKey{}).
			Where("id IN ?", normalized).
			Order("id ASC").
			Pluck("id", &existing).Error; err != nil {
			return nil, fmt.Errorf("check hidden api key ids: %w", err)
		}
	}
	encoded, err := json.Marshal(existing)
	if err != nil {
		return nil, fmt.Errorf("encode hidden api key ids: %w", err)
	}
	value := string(encoded)
	if _, err := UpsertAppSetting(ctx, db, entities.AppSetting{
		SettingKey: HiddenAPIKeyIDsSettingKey,
		Value:      &value,
		ValueType:  entities.AppSettingValueTypeJSON,
	}); err != nil {
		return nil, err
	}
	return existing, nil
}

func normalizeHiddenAPIKeyIDs(ids []int64) []int64 {
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			result = append(result, id)
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}
