package service

import (
	"context"
	"time"

	"cpa-usage-keeper/internal/apikeytime"
	"cpa-usage-keeper/internal/repository"
)

const (
	apiKeyTaskCacheEntries = 8
	apiKeyTaskCacheDays    = 50_000 // Bound the sum of daily rows across all four idle options.
)

type apiKeyTaskCacheKey struct {
	start, end         int64
	location           *time.Location
	workStart, workEnd int
}

type apiKeyTaskCacheEntry struct {
	key      apiKeyTaskCacheKey
	revision repository.APIKeyTaskRevision
	days     map[int][]apikeytime.TaskDay
	rows     int
}

type apiKeyTaskCache struct {
	// A context-aware gate serializes cold loads as well as cache access. Rapid
	// filter changes cannot build multiple copies of the same history at once.
	gate    chan struct{}
	entries []apiKeyTaskCacheEntry
	rows    int
}

func (s *apiKeyTimeUsageService) taskDays(ctx context.Context, window repository.APIKeyTimeUsageWindow, idle int) ([]apikeytime.TaskDay, error) {
	select {
	case s.tasks.gate <- struct{}{}:
		defer func() { <-s.tasks.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	revision, err := repository.LoadAPIKeyTaskRevision(ctx, s.db)
	if err != nil {
		return nil, err
	}
	key := apiKeyTaskCacheKey{start: window.Start.UnixMilli(), end: window.End.UnixMilli(), location: window.Location, workStart: window.WorkStart, workEnd: window.WorkEnd}
	for i, entry := range s.tasks.entries {
		if entry.key != key {
			continue
		}
		// Move a hit to the end, evicting least recently used windows first.
		copy(s.tasks.entries[i:], s.tasks.entries[i+1:])
		s.tasks.entries[len(s.tasks.entries)-1] = apiKeyTaskCacheEntry{}
		s.tasks.entries = s.tasks.entries[:len(s.tasks.entries)-1]
		s.tasks.rows -= entry.rows
		if entry.revision == revision {
			s.tasks.entries = append(s.tasks.entries, entry)
			s.tasks.rows += entry.rows
			return entry.days[idle], nil
		}
		break
	}
	days, revision, err := repository.LoadAPIKeyTaskDays(ctx, s.db, window)
	if err != nil {
		return nil, err
	}
	rows := 0
	for _, daily := range days {
		rows += len(daily)
	}
	if rows <= apiKeyTaskCacheDays {
		for len(s.tasks.entries) >= apiKeyTaskCacheEntries || s.tasks.rows+rows > apiKeyTaskCacheDays {
			s.tasks.rows -= s.tasks.entries[0].rows
			s.tasks.entries[0] = apiKeyTaskCacheEntry{}
			s.tasks.entries = s.tasks.entries[1:]
		}
		s.tasks.entries = append(s.tasks.entries, apiKeyTaskCacheEntry{key: key, revision: revision, days: days, rows: rows})
		s.tasks.rows += rows
	}
	return days[idle], nil
}
