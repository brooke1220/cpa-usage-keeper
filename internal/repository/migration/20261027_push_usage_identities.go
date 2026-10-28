package migration

import (
	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

const migrationPushUsageIdentities = "20261027_push_usage_identities"

func pushUsageIdentitiesMigration(tx *gorm.DB) error {
	return tx.AutoMigrate(&entities.PushUsageIdentity{})
}
