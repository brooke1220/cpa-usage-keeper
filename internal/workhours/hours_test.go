package workhours

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
	"gorm.io/gorm"
)

func openWorkHoursTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := repository.OpenDatabase(config.Config{SQLitePath: filepath.Join(t.TempDir(), "work-hours.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestLoadUsesFixedDefaultAndSavedSetting(t *testing.T) {
	db := openWorkHoursTestDatabase(t)
	clock, err := Load(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if got := clock.Get(); got.Start != "09:00" || got.End != "18:00" || got.Source != SourceDefault {
		t.Fatalf("unexpected fixed default: %+v", got)
	}
	service := NewService(db, clock)
	saved, err := service.UpdateWorkHours(context.Background(), "10:30", "19:15")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Start != "10:30" || saved.End != "19:15" || saved.Source != SourceSaved {
		t.Fatalf("unexpected saved schedule: %+v", saved)
	}
	reloaded, err := Load(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get(); got.Start != "10:30" || got.End != "19:15" || got.Source != SourceSaved {
		t.Fatalf("saved setting was not loaded: %+v", got)
	}
}

func TestLoadFallsBackToDefaultForUnreadableSetting(t *testing.T) {
	for _, value := range []string{`{`, `{"start":"18:00","end":"09:00"}`, `{"start":"09:10","end":"18:00"}`, ` `} {
		t.Run(value, func(t *testing.T) {
			db := openWorkHoursTestDatabase(t)
			stored := value
			if _, err := repository.UpsertAppSetting(context.Background(), db, entities.AppSetting{SettingKey: SettingKey, Value: &stored, ValueType: entities.AppSettingValueTypeJSON}); err != nil {
				t.Fatal(err)
			}
			clock, err := Load(context.Background(), db)
			if err != nil {
				t.Fatalf("a bad display preference must not block startup: %v", err)
			}
			if got := clock.Get(); got.Start != DefaultStart || got.End != DefaultEnd || got.Source != SourceDefault {
				t.Fatalf("unexpected fallback: %+v", got)
			}
		})
	}
}

func TestParseRequiresQuarterHourBoundaries(t *testing.T) {
	for _, window := range [][2]string{{"09:00", "18:00"}, {"09:15", "18:45"}, {"00:00", "24:00"}} {
		if _, err := Parse(window[0], window[1]); err != nil {
			t.Fatalf("expected %v to be valid: %v", window, err)
		}
	}
	for _, window := range [][2]string{{"18:00", "09:00"}, {"09:10", "18:00"}, {"09:00", "18:01"}, {"09:00", "23:59"}, {"9:00", "18:00"}} {
		if _, err := Parse(window[0], window[1]); err != ErrInvalid {
			t.Fatalf("expected %v to be rejected, got %v", window, err)
		}
	}
}

func TestUpdateRejectsInvalidWindowWithoutChangingClock(t *testing.T) {
	db := openWorkHoursTestDatabase(t)
	clock := NewClock(Default())
	service := NewService(db, clock)
	if _, err := service.UpdateWorkHours(context.Background(), "18:00", "09:00"); err != ErrInvalid {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
	if got := clock.Get(); got.Start != "09:00" || got.End != "18:00" {
		t.Fatalf("initial clock changed: %+v", got)
	}
}

func TestConcurrentUpdatesLeaveClockMatchingStoredSetting(t *testing.T) {
	db := openWorkHoursTestDatabase(t)
	clock := NewClock(Default())
	service := NewService(db, clock)
	windows := [][2]string{{"08:00", "17:00"}, {"10:00", "19:00"}, {"07:30", "16:30"}, {"11:00", "20:00"}}
	var wg sync.WaitGroup
	for _, window := range windows {
		wg.Add(1)
		go func(start, end string) {
			defer wg.Done()
			if _, err := service.UpdateWorkHours(context.Background(), start, end); err != nil {
				t.Errorf("update %s-%s: %v", start, end, err)
			}
		}(window[0], window[1])
	}
	wg.Wait()
	reloaded, err := Load(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := clock.Get(), reloaded.Get(); got.StartMinute != want.StartMinute || got.EndMinute != want.EndMinute {
		t.Fatalf("clock %+v differs from stored setting %+v", got, want)
	}
}
