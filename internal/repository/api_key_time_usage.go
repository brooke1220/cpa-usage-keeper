package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/timeutil"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

// APIKeyTimeUsageDay is one (API key, statistical day) aggregate, never a wire
// representation of a credential. APIKeyID is 0 for usage whose api_group_key
// does not match any synced CPA API key.
type APIKeyTimeUsageDay struct {
	APIKeyID int64 `gorm:"column:api_key_id"`
	// StatDay is the statistical-day label as days since 1970-01-01.
	StatDay                  int64 `gorm:"column:stat_day"`
	TotalTokens              int64 `gorm:"column:total_tokens"`
	DaytimeTokens            int64 `gorm:"column:daytime_tokens"`
	AfterHoursTokens         int64 `gorm:"column:after_hours_tokens"`
	MessageCount             int64 `gorm:"column:message_count"`
	DaytimeMessageCount      int64 `gorm:"column:daytime_message_count"`
	AfterHoursMessageCount   int64 `gorm:"column:after_hours_message_count"`
	DaytimeLatencyMS         int64 `gorm:"column:daytime_latency_ms"`
	AfterHoursLatencyMS      int64 `gorm:"column:after_hours_latency_ms"`
	DaytimeLatencySamples    int64 `gorm:"column:daytime_latency_samples"`
	AfterHoursLatencySamples int64 `gorm:"column:after_hours_latency_samples"`
}

// Date returns the statistical-day label as YYYY-MM-DD.
func (day APIKeyTimeUsageDay) Date() string {
	return time.Unix(day.StatDay*86400, 0).UTC().Format(time.DateOnly)
}

// APIKeyTimeUsageWindow describes one report query.
type APIKeyTimeUsageWindow struct {
	// Start and End bound the instants read from storage. Callers pad them;
	// rows are labelled exactly, so padding never changes a day's totals.
	Start time.Time
	End   time.Time
	// Location is the project time zone used for wall-clock labels.
	Location *time.Location
	// WorkStart and WorkEnd are minutes of day; WorkEnd may be 1440.
	WorkStart int
	WorkEnd   int
}

// apiKeyTimeZoneSegment is a span with one constant UTC offset. Until is the
// exclusive Unix end; the last segment is open-ended.
type apiKeyTimeZoneSegment struct {
	until  int64
	offset int
}

// LoadAPIKeyTimeUsage reads one consistent snapshot: key metadata, the
// api_key_time checkpoint, the 15-minute rollup for events at or before that
// checkpoint, and raw events after it. Rollup rows and the checkpoint commit
// together, so the union counts every event exactly once even while the
// aggregation runner is still catching up.
//
// Wall-clock labels come from integer arithmetic on epoch seconds plus the
// project zone's UTC offset. The offset is piecewise constant over the window,
// so DST zones use a short CASE over the transitions.
func LoadAPIKeyTimeUsage(ctx context.Context, db *gorm.DB, window APIKeyTimeUsageWindow) ([]entities.CPAAPIKey, []APIKeyTimeUsageDay, error) {
	if db == nil {
		return nil, nil, fmt.Errorf("database is nil")
	}
	if window.Location == nil {
		window.Location = time.Local
	}
	if window.WorkStart < 0 || window.WorkEnd > 24*60 || window.WorkStart >= window.WorkEnd {
		return nil, nil, fmt.Errorf("invalid work hours %d-%d", window.WorkStart, window.WorkEnd)
	}
	keys := make([]entities.CPAAPIKey, 0)
	days := make([]APIKeyTimeUsageDay, 0)
	err := db.Clauses(dbresolver.Read).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Recompute the masked value in SQL so stale historical display_key values
		// keep the same canonical presentation without loading raw credentials.
		const displayKeyProjection = `CASE
			WHEN TRIM(api_key) = '' THEN TRIM(display_key)
			WHEN TRIM(api_key) = 'unknown' THEN 'unknown'
			WHEN LENGTH(TRIM(api_key)) <= 9 THEN '*********'
			ELSE SUBSTR(TRIM(api_key), 1, 3) || '*********' || SUBSTR(TRIM(api_key), -6)
		END AS display_key`
		if err := tx.Select("id, " + displayKeyProjection + ", key_alias, is_deleted").Order("id").Find(&keys).Error; err != nil {
			return err
		}
		if !window.End.After(window.Start) {
			return nil
		}
		var checkpoint entities.UsageAggregationCheckpoint
		cursor := int64(0)
		if err := tx.Where("name = ?", entities.UsageAggregationCheckpointAPIKeyTime).Take(&checkpoint).Error; err == nil {
			cursor = checkpoint.LastAggregatedUsageEventID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("load api key time checkpoint: %w", err)
		}
		query, args := apiKeyTimeUsageQuery(window, cursor)
		return tx.Raw(query, args...).Scan(&days).Error
	})
	if err != nil {
		return nil, nil, err
	}
	return keys, days, nil
}

func apiKeyTimeUsageQuery(window APIKeyTimeUsageWindow, cursor int64) (string, []any) {
	offsetSQL, offsetArgs := apiKeyTimeOffsetSQL(apiKeyTimeZoneSegments(window.Location, window.Start, window.End))
	args := make([]any, 0, 8+len(offsetArgs))
	// Rollup rows: the bucket_start text range keeps the unique index usable.
	args = append(args, timeutil.FormatSortableStorageTime(window.Start), timeutil.FormatSortableStorageTime(window.End))
	// Raw tail: only the rowid range is indexable, so the planner never falls back to a timestamp scan.
	args = append(args, cursor)
	args = append(args, offsetArgs...)
	args = append(args, window.Start.Unix(), window.End.Unix())
	args = append(args, window.WorkStart*60, window.WorkStart*60, window.WorkEnd*60)
	query := `WITH source AS (
		SELECT stats.api_group_key AS api_group_key,
			CAST(strftime('%s', stats.bucket_start) AS INTEGER) AS epoch,
			stats.total_tokens AS total_tokens,
			stats.message_count AS message_count,
			stats.latency_ms AS latency_ms,
			stats.latency_samples AS latency_samples
		FROM usage_api_key_time_stats AS stats
		WHERE stats.bucket_start >= ? AND stats.bucket_start < ?
		UNION ALL
		SELECT TRIM(COALESCE(events.api_group_key, '')),
			CAST(strftime('%s', events.timestamp) AS INTEGER),
			COALESCE(events.total_tokens, 0),
			CASE WHEN events.generate IS NULL OR events.generate <> 0 THEN 1 ELSE 0 END,
			CASE WHEN (events.generate IS NULL OR events.generate <> 0) AND COALESCE(events.failed, 0) = 0 AND events.latency_ms > 0 THEN events.latency_ms ELSE 0 END,
			CASE WHEN (events.generate IS NULL OR events.generate <> 0) AND COALESCE(events.failed, 0) = 0 AND events.latency_ms > 0 THEN 1 ELSE 0 END
		FROM usage_events AS events
		WHERE events.id > ?
	), localized AS (
		SELECT api_group_key, total_tokens, message_count, latency_ms, latency_samples,
			epoch + ` + offsetSQL + ` AS local_epoch
		FROM source
		WHERE epoch >= ? AND epoch < ?
	), labeled AS (
		SELECT api_group_key, total_tokens, message_count, latency_ms, latency_samples,
			(local_epoch - ?) / 86400 AS stat_day,
			CASE WHEN local_epoch % 86400 >= ? AND local_epoch % 86400 < ? THEN 1 ELSE 0 END AS is_work
		FROM localized
	), grouped AS (
		SELECT api_group_key, stat_day,
			SUM(total_tokens) AS total_tokens,
			SUM(total_tokens * is_work) AS daytime_tokens,
			SUM(message_count) AS message_count,
			SUM(message_count * is_work) AS daytime_message_count,
			SUM(latency_ms * is_work) AS daytime_latency_ms,
			SUM(latency_ms * (1 - is_work)) AS after_hours_latency_ms,
			SUM(latency_samples * is_work) AS daytime_latency_samples,
			SUM(latency_samples * (1 - is_work)) AS after_hours_latency_samples
		FROM labeled
		GROUP BY api_group_key, stat_day
	)
	SELECT COALESCE(keys.id, 0) AS api_key_id, grouped.stat_day AS stat_day,
		SUM(grouped.total_tokens) AS total_tokens,
		SUM(grouped.daytime_tokens) AS daytime_tokens,
		SUM(grouped.total_tokens - grouped.daytime_tokens) AS after_hours_tokens,
		SUM(grouped.message_count) AS message_count,
		SUM(grouped.daytime_message_count) AS daytime_message_count,
		SUM(grouped.message_count - grouped.daytime_message_count) AS after_hours_message_count,
		SUM(grouped.daytime_latency_ms) AS daytime_latency_ms,
		SUM(grouped.after_hours_latency_ms) AS after_hours_latency_ms,
		SUM(grouped.daytime_latency_samples) AS daytime_latency_samples,
		SUM(grouped.after_hours_latency_samples) AS after_hours_latency_samples
	FROM grouped
	LEFT JOIN cpa_api_keys AS keys ON keys.api_key = grouped.api_group_key
	GROUP BY COALESCE(keys.id, 0), grouped.stat_day
	ORDER BY grouped.stat_day, api_key_id`
	return query, args
}

// apiKeyTimeZoneSegments splits [start, end) at the zone's offset transitions.
func apiKeyTimeZoneSegments(location *time.Location, start, end time.Time) []apiKeyTimeZoneSegment {
	segments := make([]apiKeyTimeZoneSegment, 0, 3)
	cursor := start.In(location)
	// A 92-day window crosses at most a handful of transitions; the bound only guards malformed zone data.
	for range 64 {
		_, offset := cursor.Zone()
		_, zoneEnd := cursor.ZoneBounds()
		if zoneEnd.IsZero() || !zoneEnd.Before(end) || !zoneEnd.After(cursor) {
			return append(segments, apiKeyTimeZoneSegment{offset: offset})
		}
		segments = append(segments, apiKeyTimeZoneSegment{until: zoneEnd.Unix(), offset: offset})
		cursor = zoneEnd.In(location)
	}
	_, offset := cursor.Zone()
	return append(segments, apiKeyTimeZoneSegment{offset: offset})
}

func apiKeyTimeOffsetSQL(segments []apiKeyTimeZoneSegment) (string, []any) {
	if len(segments) == 1 {
		return "?", []any{segments[0].offset}
	}
	var builder strings.Builder
	args := make([]any, 0, len(segments)*2)
	builder.WriteString("(CASE")
	for _, segment := range segments[:len(segments)-1] {
		builder.WriteString(" WHEN epoch < ? THEN ?")
		args = append(args, segment.until, segment.offset)
	}
	builder.WriteString(" ELSE ? END)")
	args = append(args, segments[len(segments)-1].offset)
	return builder.String(), args
}
