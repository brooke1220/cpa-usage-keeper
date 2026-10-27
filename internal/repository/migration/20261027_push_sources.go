package migration

import (
	"fmt"
	"strings"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

const migrationPushSources = "20261027_push_sources"

// pushSourcesMigration 把推送来源从启动配置文件移到数据库，并给 Key 记录拉取与推送的来源标记。
// 已发布的 20261026_push_ingest 保持原样，这里只追加新表和新列。
func pushSourcesMigration(tx *gorm.DB) error {
	if err := tx.AutoMigrate(&entities.PushSource{}); err != nil {
		return fmt.Errorf("create push sources: %w", err)
	}
	if !tx.Migrator().HasTable(&entities.CPAAPIKey{}) {
		return nil
	}
	added := false
	for _, field := range []string{"PullSeen", "PushSeen"} {
		if tx.Migrator().HasColumn(&entities.CPAAPIKey{}, field) {
			continue
		}
		if err := tx.Migrator().AddColumn(&entities.CPAAPIKey{}, field); err != nil {
			return fmt.Errorf("add cpa_api_keys provenance: %w", err)
		}
		added = true
	}
	if !added {
		return nil
	}
	return backfillPushedCPAAPIKeys(tx)
}

// backfillPushedCPAAPIKeys 修正新列默认值对旧推送 Key 的误判：列默认 pull_seen=1，
// 旧版推送接收登记的 Key 会被当成拉取 Key，既能 Key 登录，又会在首次拉取同步时被删掉。
// 只按推送用量标记 push_seen；pull_seen 只在旧库从未接过拉取时撤销，下一轮拉取同步会把仍在 CPA 的 Key 补回。
func backfillPushedCPAAPIKeys(tx *gorm.DB) error {
	pushedKeys := make([]string, 0, 3)
	for _, model := range []any{&entities.UsageEvent{}, &entities.UsageEventArchive{}} {
		if !tx.Migrator().HasTable(model) || !tx.Migrator().HasColumn(model, "SourceID") {
			continue
		}
		stmt := &gorm.Statement{DB: tx}
		if err := stmt.Parse(model); err != nil {
			return fmt.Errorf("resolve usage table: %w", err)
		}
		pushedKeys = append(pushedKeys, fmt.Sprintf("SELECT api_group_key FROM %s WHERE source_id <> ''", stmt.Schema.Table))
	}
	if tx.Migrator().HasTable(&entities.RedisUsageInbox{}) && tx.Migrator().HasColumn(&entities.RedisUsageInbox{}, "SourceID") {
		// 尚未处理的推送行还没有 usage_events，只能从原始 JSON 取 Key。
		pushedKeys = append(pushedKeys, "SELECT TRIM(json_extract(raw_message, '$.api_key')) FROM redis_usage_inboxes WHERE source_id <> '' AND json_valid(raw_message)")
	}
	if len(pushedKeys) == 0 {
		return nil
	}
	union := strings.Join(pushedKeys, " UNION ")
	if err := tx.Exec("UPDATE cpa_api_keys SET push_seen = 1 WHERE api_key IN (" + union + ")").Error; err != nil {
		return fmt.Errorf("mark pushed cpa_api_keys: %w", err)
	}
	// 拉取过的库一定有非推送的入库记录；完全没有时这些 Key 全部来自推送，撤销登录资格。
	pulled, err := hasPulledUsage(tx)
	if err != nil {
		return err
	}
	if pulled {
		return nil
	}
	if err := tx.Exec("UPDATE cpa_api_keys SET pull_seen = 0 WHERE push_seen = 1").Error; err != nil {
		return fmt.Errorf("clear pull provenance of pushed cpa_api_keys: %w", err)
	}
	return nil
}

func hasPulledUsage(tx *gorm.DB) (bool, error) {
	if !tx.Migrator().HasTable(&entities.UsageEvent{}) {
		return false, nil
	}
	var marker int
	if err := tx.Model(&entities.UsageEvent{}).Select("1").Where("source_id = ''").Limit(1).Scan(&marker).Error; err != nil {
		return false, fmt.Errorf("check pulled usage: %w", err)
	}
	if marker == 1 {
		return true, nil
	}
	if !tx.Migrator().HasTable(&entities.UsageEventArchive{}) {
		return false, nil
	}
	if err := tx.Model(&entities.UsageEventArchive{}).Select("1").Where("source_id = ''").Limit(1).Scan(&marker).Error; err != nil {
		return false, fmt.Errorf("check archived pulled usage: %w", err)
	}
	return marker == 1, nil
}
