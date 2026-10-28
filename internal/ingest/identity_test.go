package ingest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/helper"
	"cpa-usage-keeper/internal/repository/migration"
)

func identityFixture(t *testing.T, name string, when time.Time) json.RawMessage {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(fixture(), &data); err != nil {
		t.Fatal(err)
	}
	data["timestamp"] = when.UTC().Format(time.RFC3339Nano)
	data["identity"] = map[string]string{"display_name": name, "provider_type": "codex"}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPushIdentityDisplayMetadataIsScopedAndKeepsNewestName(t *testing.T) {
	r, db := testReceiver(t)
	now := time.Now()
	for _, item := range []struct {
		source, token, delivery, name string
		at                            time.Time
	}{
		{"cpa-one", strings.Repeat("a", 32), "new", "New label", now},
		{"cpa-one", strings.Repeat("a", 32), "old", "Old label", now.Add(-time.Hour)},
		{"cpa-two", strings.Repeat("b", 32), "other", "Other CPA", now},
	} {
		if response := send(t, r, item.source, item.token, []Event{{item.delivery, identityFixture(t, item.name, item.at)}}); response.Code != 200 {
			t.Fatalf("receive: %d %s", response.Code, response.Body)
		}
	}
	var identities []entities.PushUsageIdentity
	if err := db.Find(&identities).Error; err != nil {
		t.Fatal(err)
	}
	if len(identities) != 2 {
		t.Fatalf("identities=%d", len(identities))
	}
	for _, row := range identities {
		want := "New label"
		if row.SourceID == "cpa-two" {
			want = "Other CPA"
		}
		if row.DisplayName != want || row.AuthIndex != helper.ScopedPushAuthIndex(row.SourceID, "same-credential") {
			t.Fatalf("identity collision or stale overwrite: %+v", row)
		}
	}
}

func TestPushIdentityKeepsNewestNameWithUTCFractionalSeconds(t *testing.T) {
	previous := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previous })
	for _, tc := range []struct {
		name         string
		older, newer int
	}{
		{"whole_second", 0, 100_000_000},
		{"trailing_zero", 100_000_000, 110_000_000},
		{"nanosecond", 1, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db := testReceiver(t)
			older := time.Date(2026, 10, 27, 10, 0, 0, tc.older, time.UTC)
			newer := time.Date(2026, 10, 27, 10, 0, 0, tc.newer, time.UTC)
			for _, item := range []struct {
				delivery, name string
				at             time.Time
			}{
				{"old", "Old label", older},
				{"new", "New label", newer},
				{"late-old", "Old label", older},
			} {
				if response := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{item.delivery, identityFixture(t, item.name, item.at)}}); response.Code != 200 {
					t.Fatalf("receive: %d %s", response.Code, response.Body)
				}
			}
			var identity entities.PushUsageIdentity
			if err := db.First(&identity).Error; err != nil {
				t.Fatal(err)
			}
			if identity.DisplayName != "New label" || !identity.ObservedAt.Equal(newer) {
				t.Fatalf("UTC fractional seconds changed name order: %+v", identity)
			}
		})
	}
}

func TestPushIdentityKeepsNewestNameAcrossUTCOffsetChanges(t *testing.T) {
	previous := time.Local
	t.Cleanup(func() { time.Local = previous })
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	time.Local = newYork
	r, db := testReceiver(t)
	// 01:50 EDT is earlier than 01:05 EST; RFC3339 text with local offsets sorts them the other way.
	fallBack := time.Date(2026, 11, 1, 5, 50, 0, 0, time.UTC)
	later := fallBack.Add(15 * time.Minute)
	for _, item := range []struct {
		delivery, name string
		at             time.Time
	}{
		{"before-fall-back", "Old label", fallBack},
		{"after-fall-back", "New label", later},
		{"late-before-fall-back", "Old label", fallBack},
	} {
		if response := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{item.delivery, identityFixture(t, item.name, item.at)}}); response.Code != 200 {
			t.Fatalf("receive: %d %s", response.Code, response.Body)
		}
	}
	var identity entities.PushUsageIdentity
	if err := db.First(&identity).Error; err != nil {
		t.Fatal(err)
	}
	if identity.DisplayName != "New label" || !identity.ObservedAt.Equal(later) {
		t.Fatalf("newer name lost across offset change: %+v", identity)
	}
}

func TestPushIdentityNamesAfterLegacyObservationMigration(t *testing.T) {
	previous := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previous })
	r, db := testReceiver(t)
	index := helper.ScopedPushAuthIndex("cpa-one", "same-credential")
	// Simulate a database written in Shanghai time before sortableTime was used.
	if err := db.Exec("INSERT INTO push_usage_identities (auth_index, source_id, display_name, provider_type, observed_at) VALUES (?, ?, ?, ?, ?)", index, "cpa-one", "Saved label", "codex", "2026-10-27T18:00:00.1+08:00").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version = ?", "20261028_push_usage_identity_observed_at").Error; err != nil {
		t.Fatal(err)
	}
	if err := migration.Run(db); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		delivery, name, want string
		nanoseconds          int
	}{
		{"late-old", "Old label", "Saved label", 0},
		{"new", "New label", "New label", 200_000_000},
		{"late-saved", "Saved label", "New label", 100_000_000},
	} {
		at := time.Date(2026, 10, 27, 10, 0, 0, item.nanoseconds, time.UTC)
		if response := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{item.delivery, identityFixture(t, item.name, at)}}); response.Code != 200 {
			t.Fatalf("receive: %d %s", response.Code, response.Body)
		}
		var identity entities.PushUsageIdentity
		if err := db.Where("auth_index = ?", index).First(&identity).Error; err != nil {
			t.Fatal(err)
		}
		if identity.DisplayName != item.want {
			t.Fatalf("after %s, got name %q want %q", item.delivery, identity.DisplayName, item.want)
		}
	}
}

func TestPushIdentityStripsInvisibleFormattingWithoutRejectingUsage(t *testing.T) {
	r, db := testReceiver(t)
	now := time.Now()
	for index, item := range []struct{ displayName, providerType, wantName, wantType string }{
		{"safe\u202eevil", "co\u2066dex", "safeevil", "codex"},
		{"zero\u200bwidth\u2028name", "codex", "zerowidth name", "codex"},
		// 只剩格式字符的名称不登记，用量仍然入库。
		{"\u200b\u202e", "codex", "", ""},
	} {
		raw := identityFixture(t, "Display", now)
		raw = json.RawMessage(strings.Replace(string(raw), `"display_name":"Display"`, `"display_name":"`+item.displayName+`"`, 1))
		raw = json.RawMessage(strings.Replace(string(raw), `"provider_type":"codex"`, `"provider_type":"`+item.providerType+`"`, 1))
		raw = json.RawMessage(strings.Replace(string(raw), `"auth_index":"same-credential"`, `"auth_index":"credential-`+string(rune('a'+index))+`"`, 1))
		if response := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"format-" + string(rune('a'+index)), raw}}); response.Code != 200 {
			t.Fatalf("formatting characters rejected usage: %d %s", response.Code, response.Body)
		}
		var identity entities.PushUsageIdentity
		err := db.Where("auth_index = ?", helper.ScopedPushAuthIndex("cpa-one", "credential-"+string(rune('a'+index)))).Limit(1).Find(&identity).Error
		if err != nil {
			t.Fatal(err)
		}
		if identity.DisplayName != item.wantName || identity.ProviderType != item.wantType {
			t.Fatalf("display metadata not sanitized: got %+v want %q/%q", identity, item.wantName, item.wantType)
		}
	}
	if count(t, db, &entities.PushReceipt{}) != 3 {
		t.Fatal("sanitized events were not all accepted")
	}
}

func TestPushIdentityMetadataValidationAndAtomicReceipt(t *testing.T) {
	r, db := testReceiver(t)
	raw := identityFixture(t, "Display", time.Now())
	for _, invalid := range []json.RawMessage{
		json.RawMessage(strings.Replace(string(raw), `"display_name":"Display"`, `"display_name":"Display","account":"sk-upstream-secret"`, 1)),
		json.RawMessage(strings.Replace(string(raw), `"display_name":"Display"`, `"display_name":""`, 1)),
		json.RawMessage(strings.Replace(string(raw), `"display_name":"Display"`, `"display_name":"bad\nname"`, 1)),
		json.RawMessage(strings.Replace(string(raw), `"auth_index":"same-credential"`, `"auth_index":""`, 1)),
	} {
		if response := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"invalid", invalid}}); response.Code != 422 {
			t.Fatalf("metadata validation status=%d", response.Code)
		}
	}
	if count(t, db, &entities.PushReceipt{}) != 0 {
		t.Fatal("rejected metadata was acknowledged")
	}
	if err := db.Exec("CREATE TRIGGER reject_identity BEFORE INSERT ON push_usage_identities BEGIN SELECT RAISE(ABORT, 'test failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if response := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"retryable", raw}}); response.Code != 503 {
		t.Fatalf("metadata persistence status=%d", response.Code)
	}
	if count(t, db, &entities.PushReceipt{}) != 0 || count(t, db, &entities.RedisUsageInbox{}) != 0 {
		t.Fatal("metadata failure partially committed a receipt")
	}
	if err := db.Exec("DROP TRIGGER reject_identity").Error; err != nil {
		t.Fatal(err)
	}
	if response := send(t, r, "cpa-one", strings.Repeat("a", 32), []Event{{"retryable", raw}}); response.Code != 200 {
		t.Fatal("retry did not recover")
	}
}
