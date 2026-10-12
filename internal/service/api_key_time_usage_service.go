package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"cpa-usage-keeper/internal/apikeytime"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/helper"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/service/dto"
	"cpa-usage-keeper/internal/timeutil"
	"cpa-usage-keeper/internal/workhours"
	"gorm.io/gorm"
)

var (
	ErrInvalidAPIKeyTimeUsageQuery = errors.New("invalid_api_key_time_usage_query")
	ErrAPIKeyTimeUsageKeyNotFound  = errors.New("api_key_time_usage_key_not_found")
)

type APIKeyTimeUsageProvider interface {
	GetAPIKeyTimeUsage(context.Context, dto.APIKeyTimeUsageQuery) (*dto.APIKeyTimeUsageReport, error)
}

type APIKeyTimeUsageOptions struct {
	// WorkStart and WorkEnd are only used when Clock is nil (tests and tools).
	WorkStart string
	WorkEnd   string
	Now       func() time.Time
	Clock     *workhours.Clock
	// Location overrides the project time zone; nil reads time.Local on every request.
	Location *time.Location
}

type apiKeyTimeUsageService struct {
	db       *gorm.DB
	clock    *workhours.Clock
	now      func() time.Time
	location *time.Location
}

func NewAPIKeyTimeUsageService(db *gorm.DB, options APIKeyTimeUsageOptions) (APIKeyTimeUsageProvider, error) {
	if db == nil {
		return nil, fmt.Errorf("database is nil")
	}
	clock := options.Clock
	if clock == nil {
		if options.WorkStart == "" {
			options.WorkStart = "09:00"
		}
		if options.WorkEnd == "" {
			options.WorkEnd = "18:00"
		}
		hours, err := workhours.Parse(options.WorkStart, options.WorkEnd)
		if err != nil {
			return nil, err
		}
		clock = workhours.NewClock(hours)
	}
	if hours := clock.Get(); hours.StartMinute >= hours.EndMinute {
		return nil, fmt.Errorf("invalid work hours clock")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &apiKeyTimeUsageService{db: db, clock: clock, now: options.Now, location: options.Location}, nil
}

func (s *apiKeyTimeUsageService) projectLocation() *time.Location {
	if s.location != nil {
		return s.location
	}
	return time.Local
}

func (s *apiKeyTimeUsageService) GetAPIKeyTimeUsage(ctx context.Context, query dto.APIKeyTimeUsageQuery) (*dto.APIKeyTimeUsageReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	location := s.projectLocation()
	now := s.now().In(location)
	hours := s.clock.Get()
	workStart, workEnd := hours.StartMinute, hours.EndMinute
	// Dates are civil labels; all date arithmetic stays in UTC so DST never shifts a label.
	dayStart := timeutil.StatisticalDayStart(now, location, workStart)
	today := time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), 0, 0, 0, 0, time.UTC)
	start, startErr := time.Parse(time.DateOnly, query.StartDate)
	last, endErr := time.Parse(time.DateOnly, query.EndDate)
	if startErr != nil || endErr != nil {
		return nil, ErrInvalidAPIKeyTimeUsageQuery
	}
	// Before the work start, or with a slightly fast browser clock, a client may
	// already ask for the next date. Treat that as the current statistical day.
	tomorrow := today.AddDate(0, 0, 1)
	if last.After(today) && !last.After(tomorrow) {
		last = today
	}
	if start.After(today) && !start.After(tomorrow) {
		start = today
	}
	minDate := today.AddDate(0, 0, -(apikeytime.ReportMaxDays - 1))
	if start.After(last) || start.Before(minDate) || last.After(today) {
		return nil, ErrInvalidAPIKeyTimeUsageQuery
	}
	var selectedID int64
	if query.APIKeyID != "" {
		var err error
		selectedID, err = strconv.ParseInt(query.APIKeyID, 10, 64)
		if err != nil || selectedID <= 0 || strconv.FormatInt(selectedID, 10) != query.APIKeyID {
			return nil, ErrInvalidAPIKeyTimeUsageQuery
		}
	}
	// Labels are exact, so a one-day pad only guards zone transitions at the edges.
	window := repository.APIKeyTimeUsageWindow{
		Start:     timeutil.ClockOnDate(start, workStart, location).Add(-24 * time.Hour),
		End:       timeutil.ClockOnDate(last.AddDate(0, 0, 1), workStart, location).Add(24 * time.Hour),
		Location:  location,
		WorkStart: workStart,
		WorkEnd:   workEnd,
	}
	keys, rows, err := repository.LoadAPIKeyTimeUsage(ctx, s.db, window)
	if err != nil {
		return nil, err
	}
	report := &dto.APIKeyTimeUsageReport{
		Timezone:     location.String(),
		DaytimeStart: hours.Start, DaytimeEnd: hours.End,
		StartDate: start.Format(time.DateOnly), EndDate: last.Format(time.DateOnly), MinDate: minDate.Format(time.DateOnly), MaxDate: today.Format(time.DateOnly),
		APIKeyID: query.APIKeyID, GeneratedAt: now,
		Keys: make([]dto.APIKeyTimeUsageKey, 0), Daily: make([]dto.APIKeyTimeUsageDaily, 0), KeySummaries: make([]dto.APIKeyTimeUsageKeySummary, 0),
	}
	days := make(map[string]int)
	for day := start; !day.After(last); day = day.AddDate(0, 0, 1) {
		date := day.Format(time.DateOnly)
		days[date] = len(report.Daily)
		isToday := day.Equal(today)
		if !isToday {
			report.CompleteDays++
		}
		report.Daily = append(report.Daily, dto.APIKeyTimeUsageDaily{Date: date, IsToday: isToday})
	}
	type labeledRow struct {
		row      repository.APIKeyTimeUsageDay
		dayIndex int
	}
	inRange := make([]labeledRow, 0, len(rows))
	hasUsage := make(map[int64]bool)
	for _, row := range rows {
		dayIndex, ok := days[row.Date()]
		if !ok {
			continue
		}
		inRange = append(inRange, labeledRow{row: row, dayIndex: dayIndex})
		if row.APIKeyID > 0 {
			hasUsage[row.APIKeyID] = true
		}
	}
	keyIndexes := make(map[int64]int)
	found := selectedID == 0
	for _, key := range keys {
		if key.ID == selectedID {
			found = true
		}
		if key.IsDeleted && !hasUsage[key.ID] && key.ID != selectedID {
			continue
		}
		option := apiKeyTimeUsageKey(key)
		report.Keys = append(report.Keys, option)
		if selectedID != 0 && key.ID != selectedID {
			continue
		}
		keyIndexes[key.ID] = len(report.KeySummaries)
		daily := make([]dto.APIKeyTimeUsageDaily, len(report.Daily))
		copy(daily, report.Daily)
		report.KeySummaries = append(report.KeySummaries, dto.APIKeyTimeUsageKeySummary{Key: option, Daily: daily})
	}
	if !found {
		return nil, ErrAPIKeyTimeUsageKeyNotFound
	}
	completeTotals := dto.APIKeyTimeUsageTotals{}
	keyComplete := make(map[int64]dto.APIKeyTimeUsageTotals)
	for _, item := range inRange {
		row := item.row
		totals := dto.APIKeyTimeUsageTotals{
			TotalTokens: row.TotalTokens, DaytimeTokens: row.DaytimeTokens, AfterHoursTokens: row.AfterHoursTokens,
			MessageCount: row.MessageCount, DaytimeMessageCount: row.DaytimeMessageCount, AfterHoursMessageCount: row.AfterHoursMessageCount,
			DaytimeLatencyMS: row.DaytimeLatencyMS, AfterHoursLatencyMS: row.AfterHoursLatencyMS,
			DaytimeLatencySamples: row.DaytimeLatencySamples, AfterHoursLatencySamples: row.AfterHoursLatencySamples,
		}
		if row.APIKeyID == 0 {
			// Usage from keys that were never synced is reported separately so the table total stays the sum of its rows.
			if selectedID == 0 {
				addAPIKeyTimeUsage(&report.Unattributed, totals)
			}
			continue
		}
		keyIndex, ok := keyIndexes[row.APIKeyID]
		if !ok {
			continue
		}
		report.HasRecords = true
		addAPIKeyTimeUsage(&report.Daily[item.dayIndex].APIKeyTimeUsageTotals, totals)
		addAPIKeyTimeUsage(&report.Summary.APIKeyTimeUsageTotals, totals)
		addAPIKeyTimeUsage(&report.KeySummaries[keyIndex].APIKeyTimeUsageTotals, totals)
		addAPIKeyTimeUsage(&report.KeySummaries[keyIndex].Daily[item.dayIndex].APIKeyTimeUsageTotals, totals)
		if !report.Daily[item.dayIndex].IsToday {
			addAPIKeyTimeUsage(&completeTotals, totals)
			completed := keyComplete[row.APIKeyID]
			addAPIKeyTimeUsage(&completed, totals)
			keyComplete[row.APIKeyID] = completed
		}
	}
	finishAPIKeyTimeUsage(&report.Summary, completeTotals, report.CompleteDays)
	for id, index := range keyIndexes {
		finishAPIKeyTimeUsage(&report.KeySummaries[index].APIKeyTimeUsageStats, keyComplete[id], report.CompleteDays)
	}
	sort.SliceStable(report.KeySummaries, func(i, j int) bool { return report.KeySummaries[i].TotalTokens > report.KeySummaries[j].TotalTokens })
	return report, nil
}

func addAPIKeyTimeUsage(to *dto.APIKeyTimeUsageTotals, from dto.APIKeyTimeUsageTotals) {
	to.TotalTokens += from.TotalTokens
	to.DaytimeTokens += from.DaytimeTokens
	to.AfterHoursTokens += from.AfterHoursTokens
	to.MessageCount += from.MessageCount
	to.DaytimeMessageCount += from.DaytimeMessageCount
	to.AfterHoursMessageCount += from.AfterHoursMessageCount
	to.DaytimeLatencyMS += from.DaytimeLatencyMS
	to.AfterHoursLatencyMS += from.AfterHoursLatencyMS
	to.DaytimeLatencySamples += from.DaytimeLatencySamples
	to.AfterHoursLatencySamples += from.AfterHoursLatencySamples
}

func finishAPIKeyTimeUsage(stats *dto.APIKeyTimeUsageStats, completed dto.APIKeyTimeUsageTotals, days int) {
	ratio := func(value int64, divisor float64) *float64 { n := float64(value) / divisor; return &n }
	if days > 0 {
		stats.DailyAverage = ratio(completed.TotalTokens, float64(days))
		stats.DaytimeDailyAverage = ratio(completed.DaytimeTokens, float64(days))
		stats.AfterHoursDailyAverage = ratio(completed.AfterHoursTokens, float64(days))
	}
	if stats.TotalTokens > 0 {
		stats.DaytimeShare = ratio(stats.DaytimeTokens, float64(stats.TotalTokens))
		stats.AfterHoursShare = ratio(stats.AfterHoursTokens, float64(stats.TotalTokens))
	}
}

func apiKeyTimeUsageKey(key entities.CPAAPIKey) dto.APIKeyTimeUsageKey {
	return dto.APIKeyTimeUsageKey{
		ID: strconv.FormatInt(key.ID, 10), KeyAlias: key.KeyAlias, DisplayKey: helper.CPAAPIKeyMaskedDisplayKey(key),
		DisplayName: helper.CPAAPIKeyDisplayName(key), AvatarID: apiKeyTimeUsageAvatarID(key.ID, key.LocalRankingAvatarID), IsDeleted: key.IsDeleted,
	}
}

// Matches local ranking: a saved 1–66 avatar wins; otherwise the key ID picks a stable catalog avatar.
func apiKeyTimeUsageAvatarID(id int64, stored *uint8) uint8 {
	if stored != nil && *stored >= 1 && *stored <= 66 {
		return *stored
	}
	if id <= 0 {
		return 1
	}
	return uint8(1 + ((id - 1) % 66))
}
