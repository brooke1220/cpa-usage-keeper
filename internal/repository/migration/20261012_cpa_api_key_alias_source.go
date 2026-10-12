package migration

import (
	"fmt"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

// addCPAAPIKeyAliasSourceMigration 只增加默认空值的来源列；历史行保持“来源未知”，行为与升级前一致。
func addCPAAPIKeyAliasSourceMigration(tx *gorm.DB) error {
	if tx == nil {
		return fmt.Errorf("database is nil")
	}
	if !tx.Migrator().HasTable(&entities.CPAAPIKey{}) {
		return nil
	}
	if tx.Migrator().HasColumn(&entities.CPAAPIKey{}, "KeyAliasSource") {
		return nil
	}
	if err := tx.Migrator().AddColumn(&entities.CPAAPIKey{}, "KeyAliasSource"); err != nil {
		return fmt.Errorf("add CPA API key alias source: %w", err)
	}
	return nil
}
