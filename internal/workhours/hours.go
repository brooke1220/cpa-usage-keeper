package workhours

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cpa-usage-keeper/internal/apikeytime"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/timeutil"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const (
	SettingKey    = "usage.work_hours"
	SourceSaved   = "saved"
	SourceDefault = "default"
	// StepMinutes 与 API Key 用量预聚合的 bucket 粒度一致，边界必须落在 bucket 上才能精确拆分。
	StepMinutes = apikeytime.BucketMinutes
	// DefaultStart 和 DefaultEnd 是没有保存设置时的产品默认值。
	DefaultStart = "09:00"
	DefaultEnd   = "18:00"
)

var (
	ErrInvalid = errors.New("invalid_work_hours")
)

// Hours is the canonical same-day work window in the project time zone.
type Hours struct {
	Start       string
	End         string
	StartMinute int
	EndMinute   int
	Source      string
}

// Schedule is the admin API representation.
type Schedule struct {
	Timezone string `json:"timezone"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Source   string `json:"source"`
}

func (h Hours) Schedule() Schedule {
	source := h.Source
	if source == "" {
		source = SourceDefault
	}
	return Schedule{Timezone: time.Local.String(), Start: h.Start, End: h.End, Source: source}
}

type storedHours struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// Clock is the process-wide work window. Usage queries read it on every request.
type Clock struct {
	mu      sync.RWMutex
	current Hours
}

func NewClock(hours Hours) *Clock {
	return &Clock{current: hours}
}

func (c *Clock) Get() Hours {
	if c == nil {
		return Hours{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

func (c *Clock) Set(hours Hours) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.current = hours
	c.mu.Unlock()
}

// Parse accepts a half-open same-day HH:mm window on 15-minute boundaries.
func Parse(start, end string) (Hours, error) {
	first, last, err := timeutil.ParseWorkHours(start, end)
	if err != nil || first%StepMinutes != 0 || last%StepMinutes != 0 {
		return Hours{}, ErrInvalid
	}
	return Hours{
		Start: timeutil.FormatClockMinute(first), End: timeutil.FormatClockMinute(last),
		StartMinute: first, EndMinute: last,
	}, nil
}

// Default returns the product default window.
func Default() Hours {
	hours, err := Parse(DefaultStart, DefaultEnd)
	if err != nil {
		panic(fmt.Sprintf("default work hours are invalid: %v", err))
	}
	hours.Source = SourceDefault
	return hours
}

// Load uses a saved setting when present. A missing or unreadable row falls
// back to the product default so a bad display preference never blocks startup.
func Load(ctx context.Context, db *gorm.DB) (*Clock, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	setting, found, err := repository.GetAppSetting(ctx, db, SettingKey)
	if err != nil {
		return nil, err
	}
	if !found {
		return NewClock(Default()), nil
	}
	parsed, err := decodeSetting(setting)
	if err != nil {
		logrus.WithError(err).WithField("setting_key", SettingKey).Warn("stored work hours are invalid; using the default 09:00-18:00")
		return NewClock(Default()), nil
	}
	parsed.Source = SourceSaved
	return NewClock(parsed), nil
}

func decodeSetting(setting entities.AppSetting) (Hours, error) {
	if setting.Value == nil || strings.TrimSpace(*setting.Value) == "" {
		return Hours{}, fmt.Errorf("work hours setting is empty")
	}
	var stored storedHours
	if err := json.Unmarshal([]byte(*setting.Value), &stored); err != nil {
		return Hours{}, fmt.Errorf("work hours setting is invalid JSON: %w", err)
	}
	parsed, err := Parse(stored.Start, stored.End)
	if err != nil {
		return Hours{}, fmt.Errorf("stored work hours %q-%q are invalid", stored.Start, stored.End)
	}
	return parsed, nil
}

func save(ctx context.Context, db *gorm.DB, hours Hours) error {
	payload, err := json.Marshal(storedHours{Start: hours.Start, End: hours.End})
	if err != nil {
		return fmt.Errorf("encode work hours: %w", err)
	}
	value := string(payload)
	if _, err := repository.UpsertAppSetting(ctx, db, entities.AppSetting{
		SettingKey: SettingKey,
		Value:      &value,
		ValueType:  entities.AppSettingValueTypeJSON,
	}); err != nil {
		return err
	}
	return nil
}

// Service persists the shared window and updates the in-process Usage clock.
type Service struct {
	db    *gorm.DB
	clock *Clock
	// mu keeps the compare, the database write and the clock update in one order,
	// so concurrent saves cannot leave the clock different from the stored row.
	mu sync.Mutex
}

func NewService(db *gorm.DB, clock *Clock) *Service {
	return &Service{db: db, clock: clock}
}

func (s *Service) GetWorkHours(context.Context) (Schedule, error) {
	if s == nil || s.clock == nil {
		return Schedule{}, fmt.Errorf("work hours are not configured")
	}
	return s.clock.Get().Schedule(), nil
}

func (s *Service) UpdateWorkHours(ctx context.Context, start, end string) (Schedule, error) {
	if s == nil || s.clock == nil || s.db == nil {
		return Schedule{}, fmt.Errorf("work hours are not configured")
	}
	next, err := Parse(start, end)
	if err != nil {
		return Schedule{}, ErrInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.clock.Get()
	if current.StartMinute == next.StartMinute && current.EndMinute == next.EndMinute {
		return current.Schedule(), nil
	}
	next.Source = SourceSaved
	if err := save(ctx, s.db, next); err != nil {
		return Schedule{}, err
	}
	s.clock.Set(next)
	return next.Schedule(), nil
}
