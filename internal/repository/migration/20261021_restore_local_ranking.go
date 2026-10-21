package migration

import (
	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

// Restore after the published v2 removal migration; never edit its historical version.
func restoreLocalRankingMigration(tx *gorm.DB) error {
	if err := tx.AutoMigrate(&entities.LocalRankingPeriodStat{}); err != nil {
		return err
	}
	if !tx.Migrator().HasColumn(&entities.CPAAPIKey{}, "LocalRankingAvatarID") {
		return tx.Migrator().AddColumn(&entities.CPAAPIKey{}, "LocalRankingAvatarID")
	}
	return nil
}
