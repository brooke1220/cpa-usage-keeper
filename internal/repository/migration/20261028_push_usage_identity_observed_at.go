package migration

import (
	"fmt"

	"cpa-usage-keeper/internal/timeutil"
	"gorm.io/gorm"
)

const migrationPushUsageIdentityObservedAt = "20261028_push_usage_identity_observed_at"

// pushUsageIdentityObservedAtMigration keeps previously stored observations
// comparable with new sortableTime values without changing their instants.
func pushUsageIdentityObservedAtMigration(tx *gorm.DB) error {
	var rows []struct {
		AuthIndex  string
		ObservedAt string
	}
	// CAST avoids the SQLite driver's implicit DATETIME parsing of legacy values.
	if err := tx.Raw("SELECT auth_index, CAST(observed_at AS TEXT) AS observed_at FROM push_usage_identities WHERE observed_at IS NOT NULL ORDER BY auth_index").Scan(&rows).Error; err != nil {
		return fmt.Errorf("list pushed identity observation times: %w", err)
	}
	for _, row := range rows {
		observedAt, err := timeutil.ParseStorageTime(row.ObservedAt)
		if err != nil {
			return fmt.Errorf("parse pushed identity %q observation time: %w", row.AuthIndex, err)
		}
		normalized := timeutil.FormatSortableStorageTime(observedAt)
		if normalized == row.ObservedAt {
			continue
		}
		if err := tx.Exec("UPDATE push_usage_identities SET observed_at = ? WHERE auth_index = ?", normalized, row.AuthIndex).Error; err != nil {
			return fmt.Errorf("normalize pushed identity %q observation time: %w", row.AuthIndex, err)
		}
	}
	return nil
}
