package test

import (
	"strings"
	"testing"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository/migration"
)

const pushIdentityTimeMigration = "20261028_push_usage_identity_observed_at"

func TestPushIdentityObservationMigrationNormalizesExistingTimes(t *testing.T) {
	db := openUnmigratedTestDatabase(t)
	if err := db.AutoMigrate(&entities.PushUsageIdentity{}); err != nil {
		t.Fatal(err)
	}
	if err := migration.MarkAllAsApplied(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version = ?", pushIdentityTimeMigration).Error; err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		index, before, after string
	}{
		{"whole-second", "2026-10-27T10:00:00Z", "2026-10-27T10:00:00.000000000Z"},
		{"fraction", "2026-10-27T10:00:00.1Z", "2026-10-27T10:00:00.100000000Z"},
		{"nanosecond", "2026-10-27T10:00:00.000000001Z", "2026-10-27T10:00:00.000000001Z"},
		{"local-offset", "2026-10-27T18:00:00.11+08:00", "2026-10-27T10:00:00.110000000Z"},
		{"daylight", "2026-11-01T01:50:00-04:00", "2026-11-01T05:50:00.000000000Z"},
		{"standard", "2026-11-01T01:05:00-05:00", "2026-11-01T06:05:00.000000000Z"},
		{"canonical", "2026-10-27T10:00:00.110000000Z", "2026-10-27T10:00:00.110000000Z"},
	}
	for _, tc := range cases {
		if err := db.Exec("INSERT INTO push_usage_identities (auth_index, source_id, display_name, provider_type, observed_at) VALUES (?, ?, ?, ?, ?)", tc.index, "cpa-one", "Saved label", "codex", tc.before).Error; err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := migration.Run(db); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range cases {
		var row struct {
			SourceID, DisplayName, ProviderType, ObservedAt string
		}
		if err := db.Raw("SELECT source_id, display_name, provider_type, CAST(observed_at AS TEXT) AS observed_at FROM push_usage_identities WHERE auth_index = ?", tc.index).Scan(&row).Error; err != nil {
			t.Fatal(err)
		}
		if row.ObservedAt != tc.after || row.SourceID != "cpa-one" || row.DisplayName != "Saved label" || row.ProviderType != "codex" {
			t.Errorf("identity %q changed incorrectly: %+v; want observation %q", tc.index, row, tc.after)
		}
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", pushIdentityTimeMigration).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration records=%d, want 1 after two runs", count)
	}
}

func TestPushIdentityObservationMigrationRollsBackInvalidTime(t *testing.T) {
	db := openUnmigratedTestDatabase(t)
	if err := db.AutoMigrate(&entities.PushUsageIdentity{}); err != nil {
		t.Fatal(err)
	}
	if err := migration.MarkAllAsApplied(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version = ?", pushIdentityTimeMigration).Error; err != nil {
		t.Fatal(err)
	}
	const original = "2026-10-27T18:00:00.1+08:00"
	for _, row := range []struct{ index, observed string }{{"a-valid", original}, {"z-invalid", "invalid-time"}} {
		if err := db.Exec("INSERT INTO push_usage_identities (auth_index, source_id, display_name, observed_at) VALUES (?, ?, ?, ?)", row.index, "cpa-one", "Saved label", row.observed).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := migration.Run(db); err == nil || !strings.Contains(err.Error(), "z-invalid") {
		t.Fatalf("expected invalid observation to fail migration, got %v", err)
	}
	var raw string
	if err := db.Raw("SELECT CAST(observed_at AS TEXT) FROM push_usage_identities WHERE auth_index = ?", "a-valid").Row().Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != original {
		t.Fatalf("failed migration partially changed valid row: got %q want %q", raw, original)
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", pushIdentityTimeMigration).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed migration was marked applied")
	}
}
