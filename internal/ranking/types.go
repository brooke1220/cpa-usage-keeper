package ranking

import (
	"cpa-usage-keeper/internal/timeutil"
	"time"
)

const (
	MinAvatarID = 1
	MaxAvatarID = 66
)

type LeaderboardPeriod string

const (
	LeaderboardToday         LeaderboardPeriod = "today"
	LeaderboardYesterday     LeaderboardPeriod = "yesterday"
	LeaderboardCurrentMonth  LeaderboardPeriod = "current_month"
	LeaderboardPreviousMonth LeaderboardPeriod = "previous_month"
)

type LeaderboardMetric string

const (
	MetricTotalTokens    LeaderboardMetric = "total_tokens"
	MetricRequestCount   LeaderboardMetric = "request_count"
	MetricCacheReadRate  LeaderboardMetric = "cache_read_rate"
	MetricTTFTAverage    LeaderboardMetric = "ttft_average"
	MetricLatencyAverage LeaderboardMetric = "latency_average"
	MetricPeakTPM        LeaderboardMetric = "peak_tpm"
	MetricPeakRPM        LeaderboardMetric = "peak_rpm"
	MetricOverall        LeaderboardMetric = "overall"
)

type LeaderboardEntry struct {
	Rank            uint16                      `json:"rank"`
	ParticipantID   string                      `json:"participant_id"`
	DisplayName     string                      `json:"display_name"`
	KeyAlias        string                      `json:"key_alias,omitempty"`
	AvatarID        uint8                       `json:"avatar_id"`
	Value           int64                       `json:"value"`
	RateNumerator   int64                       `json:"rate_numerator,omitempty"`
	RateDenominator int64                       `json:"rate_denominator,omitempty"`
	Metrics         map[LeaderboardMetric]int64 `json:"metrics,omitempty"`
}

type ScoreExplanation struct {
	Version int               `json:"version"`
	Texts   map[string]string `json:"texts"`
}

type Leaderboard struct {
	Period           LeaderboardPeriod  `json:"period"`
	PeriodKey        string             `json:"period_key"`
	Metric           LeaderboardMetric  `json:"metric"`
	GeneratedAt      time.Time          `json:"generated_at"`
	Stale            bool               `json:"stale"`
	Entries          []LeaderboardEntry `json:"entries"`
	ScoreExplanation *ScoreExplanation  `json:"score_explanation,omitempty"`
}

func validLeaderboardPeriod(period LeaderboardPeriod) bool {
	switch period {
	case LeaderboardToday, LeaderboardYesterday, LeaderboardCurrentMonth, LeaderboardPreviousMonth:
		return true
	default:
		return false
	}
}

func validLeaderboardMetric(metric LeaderboardMetric) bool {
	switch metric {
	case MetricTotalTokens, MetricRequestCount, MetricCacheReadRate, MetricTTFTAverage, MetricLatencyAverage, MetricPeakTPM, MetricPeakRPM, MetricOverall:
		return true
	default:
		return false
	}
}

func rankingTimeRangePredicate(start, end time.Time) (string, []any) {
	// storageTime 使用 Keeper 本地 offset，DST 回拨时文本顺序不等于 instant 顺序。
	// 外层宽范围继续使用 timestamp 索引，strftime 只负责候选行的真实时间复核。
	coarseStart := timeutil.FormatStorageTime(start.Add(-24 * time.Hour))
	coarseEnd := timeutil.FormatStorageTime(end.Add(24 * time.Hour))
	epochExpression := "CAST(strftime('%s', timestamp) AS INTEGER)"
	if end.Nanosecond() == 0 {
		return "timestamp >= ? AND timestamp < ? AND " + epochExpression + " >= ? AND " + epochExpression + " < ?", []any{
			coarseStart, coarseEnd, start.Unix(), end.Unix(),
		}
	}
	return "timestamp >= ? AND timestamp < ? AND " + epochExpression + " >= ? AND (" + epochExpression + " < ? OR (" + epochExpression + " = ? AND timestamp < ?))", []any{
		coarseStart, coarseEnd, start.Unix(), end.Unix(), end.Unix(), timeutil.FormatStorageTime(end),
	}
}
