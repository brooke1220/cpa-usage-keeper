package repository

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"cpa-usage-keeper/internal/apikeytime"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/timeutil"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

// APIKeyTaskRevision invalidates compact result caches on append, archival,
// retention cleanup, or key registration. Aliases do not affect task durations.
type APIKeyTaskRevision struct {
	HotFirst, HotLast         int64
	ArchiveFirst, ArchiveLast int64
	KeyFirst, KeyLast, Keys   int64
}

func LoadAPIKeyTaskRevision(ctx context.Context, db *gorm.DB) (APIKeyTaskRevision, error) {
	if db == nil {
		return APIKeyTaskRevision{}, fmt.Errorf("database is nil")
	}
	var revision APIKeyTaskRevision
	err := db.Clauses(dbresolver.Read).WithContext(ctx).Raw(`SELECT
		COALESCE((SELECT MIN(id) FROM usage_events), 0) AS hot_first,
		COALESCE((SELECT MAX(id) FROM usage_events), 0) AS hot_last,
		COALESCE((SELECT MIN(id) FROM usage_events_archive), 0) AS archive_first,
		COALESCE((SELECT MAX(id) FROM usage_events_archive), 0) AS archive_last,
		COALESCE((SELECT MIN(id) FROM cpa_api_keys), 0) AS key_first,
		COALESCE((SELECT MAX(id) FROM cpa_api_keys), 0) AS key_last,
		(SELECT COUNT(*) FROM cpa_api_keys) AS keys`).Scan(&revision).Error
	return revision, err
}

// LoadAPIKeyTaskDays reads all idle options in one ordered pass, retaining only
// active tasks and daily totals. Parent metadata includes hot AND archive history
// without date, key, or generate filters. The caller applies report filters last.
func LoadAPIKeyTaskDays(ctx context.Context, db *gorm.DB, window APIKeyTimeUsageWindow) (map[int][]apikeytime.TaskDay, APIKeyTaskRevision, error) {
	if db == nil {
		return nil, APIKeyTaskRevision{}, fmt.Errorf("database is nil")
	}
	var result map[int][]apikeytime.TaskDay
	var revision APIKeyTaskRevision
	err := db.Clauses(dbresolver.Read).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		revision, err = LoadAPIKeyTaskRevision(ctx, tx)
		if err != nil {
			return err
		}
		// The partial covering indexes contain only parent-bearing events. MAX(id)
		// selects the newest known parent if malformed clients supplied conflicts.
		rows, err := tx.Raw(`SELECT TRIM(session_id), TRIM(parent_session_id), MAX(id)
			FROM (
				SELECT id, session_id, parent_session_id FROM usage_events
				WHERE parent_session_id IS NOT NULL AND parent_session_id <> '' AND session_id <> ''
				UNION ALL
				SELECT id, session_id, parent_session_id FROM usage_events_archive
				WHERE parent_session_id IS NOT NULL AND parent_session_id <> '' AND session_id <> ''
			) GROUP BY TRIM(session_id)`).Rows()
		if err != nil {
			return err
		}
		parents := make(map[string]string)
		var session, parent string
		var id int64
		for rows.Next() {
			if err = rows.Scan(&session, &parent, &id); err != nil {
				break
			}
			if session != "" && parent != "" && session != parent {
				parents[session] = parent
			}
		}
		if err == nil {
			err = rows.Err()
		}
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		roots := apikeytime.TaskRoots(parents)
		accumulators := make([]*apikeytime.TaskAccumulator, len(apikeytime.TaskIdleMinuteOptions))
		for i, minutes := range apikeytime.TaskIdleMinuteOptions {
			accumulators[i] = apikeytime.NewTaskAccumulator(time.Duration(minutes)*time.Minute, window.Location, window.WorkStart, window.WorkEnd)
		}
		err = streamAPIKeyTaskRequests(ctx, tx, window, roots, func(request apikeytime.TaskRequest) {
			for _, accumulator := range accumulators {
				accumulator.Add(request)
			}
		})
		if err != nil {
			return err
		}
		result = make(map[int][]apikeytime.TaskDay, len(accumulators))
		for i, accumulator := range accumulators {
			result[apikeytime.TaskIdleMinuteOptions[i]] = accumulator.Days()
		}
		return nil
	})
	if err != nil {
		return nil, revision, fmt.Errorf("load api key task days: %w", err)
	}
	return result, revision, nil
}

// RFC3339Nano text is ordered by whole second within a fixed offset, but not by
// fraction (e.g. .1Z sorts after .11Z, and Z after both). Collapse each task's
// requests within that second, then order this small buffer by actual instant.
// All supported idle gaps exceed one second, so this preserves interval unions.
func streamAPIKeyTaskRequests(ctx context.Context, tx *gorm.DB, window APIKeyTimeUsageWindow, roots map[string]string, consume func(apikeytime.TaskRequest)) error {
	type secondKey struct {
		id            int64
		root, unknown string
	}
	buffer := make(map[secondKey]apikeytime.TaskRequest)
	ordered := make([]apikeytime.TaskRequest, 0, 16)
	second := int64(math.MinInt64)
	flush := func() {
		ordered = ordered[:0]
		for _, request := range buffer {
			ordered = append(ordered, request)
		}
		slices.SortFunc(ordered, func(a, b apikeytime.TaskRequest) int {
			if a.StartMS < b.StartMS {
				return -1
			}
			if a.StartMS > b.StartMS {
				return 1
			}
			return 0
		})
		for _, request := range ordered {
			consume(request)
		}
		clear(buffer)
		clear(ordered)
	}
	for _, read := range usageEventTimeReads(window.Start, window.End, false) {
		events := read.query(tx.Model(&entities.UsageEvent{})).Where("generate IS NULL OR generate <> 0").
			Select("api_group_key, session_id, timestamp, latency_ms")
		rows, err := tx.Table("(?) AS events", events).
			Joins("LEFT JOIN cpa_api_keys AS keys ON keys.api_key = TRIM(COALESCE(events.api_group_key, ''))").
			Select(`COALESCE(keys.id, 0), COALESCE(events.session_id, ''), events.timestamp, COALESCE(events.latency_ms, 0),
				CASE WHEN keys.id IS NULL THEN TRIM(COALESCE(events.api_group_key, '')) ELSE '' END`).
			Order("events.timestamp ASC").Rows()
		if err != nil {
			return err
		}
		var keyID, latency int64
		var session, stamp, unknown string
		for rows.Next() {
			if err = ctx.Err(); err != nil {
				break
			}
			if err = rows.Scan(&keyID, &session, &stamp, &latency, &unknown); err != nil {
				break
			}
			var at time.Time
			at, err = timeutil.ParseStorageTime(stamp)
			if err != nil {
				break
			}
			if !read.contains(at) {
				continue
			}
			if at.Unix() != second {
				flush()
				second = at.Unix()
			}
			session = strings.TrimSpace(session)
			if root, ok := roots[session]; ok {
				session = root
			}
			start := at.UnixMilli()
			// Clip before addition to avoid overflow from an invalid upstream latency.
			end := start + min(max(latency, 0), max(window.End.UnixMilli()-start, 0))
			key := secondKey{id: keyID, root: session, unknown: unknown}
			if previous, ok := buffer[key]; ok {
				previous.StartMS, previous.EndMS = min(previous.StartMS, start), max(previous.EndMS, end)
				buffer[key] = previous
			} else {
				buffer[key] = apikeytime.TaskRequest{APIKeyID: keyID, APIGroupKey: unknown, SessionID: session, StartMS: start, EndMS: end}
			}
		}
		if err == nil {
			err = rows.Err()
		}
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		// Offset segments are ordered by instant; flush before the clock changes.
		flush()
	}
	return nil
}
