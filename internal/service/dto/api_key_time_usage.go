package dto

import "time"

type APIKeyTimeUsageQuery struct {
	StartDate string
	EndDate   string
	APIKeyID  string
}

type APIKeyTimeUsageKey struct {
	ID          string `json:"id"`
	KeyAlias    string `json:"key_alias"`
	DisplayKey  string `json:"display_key"`
	DisplayName string `json:"display_name"`
	AvatarID    uint8  `json:"avatar_id"`
	IsDeleted   bool   `json:"is_deleted"`
}

type APIKeyTimeUsageTotals struct {
	TotalTokens              int64 `json:"total_tokens"`
	DaytimeTokens            int64 `json:"daytime_tokens"`
	AfterHoursTokens         int64 `json:"after_hours_tokens"`
	MessageCount             int64 `json:"message_count"`
	DaytimeMessageCount      int64 `json:"daytime_message_count"`
	AfterHoursMessageCount   int64 `json:"after_hours_message_count"`
	DaytimeLatencyMS         int64 `json:"daytime_latency_ms"`
	AfterHoursLatencyMS      int64 `json:"after_hours_latency_ms"`
	DaytimeLatencySamples    int64 `json:"daytime_latency_samples"`
	AfterHoursLatencySamples int64 `json:"after_hours_latency_samples"`
}

type APIKeyTimeUsageStats struct {
	APIKeyTimeUsageTotals
	DailyAverage           *float64 `json:"daily_average"`
	DaytimeDailyAverage    *float64 `json:"daytime_daily_average"`
	AfterHoursDailyAverage *float64 `json:"after_hours_daily_average"`
	DaytimeShare           *float64 `json:"daytime_share"`
	AfterHoursShare        *float64 `json:"after_hours_share"`
}

type APIKeyTimeUsageDaily struct {
	APIKeyTimeUsageTotals
	Date    string `json:"date"`
	IsToday bool   `json:"is_today"`
}

type APIKeyTimeUsageKeySummary struct {
	APIKeyTimeUsageStats
	Key   APIKeyTimeUsageKey     `json:"key"`
	Daily []APIKeyTimeUsageDaily `json:"daily"`
}

type APIKeyTimeUsageReport struct {
	Timezone     string               `json:"timezone"`
	DaytimeStart string               `json:"daytime_start"`
	DaytimeEnd   string               `json:"daytime_end"`
	StartDate    string               `json:"start_date"`
	EndDate      string               `json:"end_date"`
	MinDate      string               `json:"min_date"`
	MaxDate      string               `json:"max_date"`
	APIKeyID     string               `json:"api_key_id,omitempty"`
	GeneratedAt  time.Time            `json:"generated_at"`
	CompleteDays int                  `json:"complete_days"`
	HasRecords   bool                 `json:"has_records"`
	Keys         []APIKeyTimeUsageKey `json:"keys"`
	Summary      APIKeyTimeUsageStats `json:"summary"`
	// Unattributed is usage whose API key was never synced to Keeper; it is not part of Summary.
	Unattributed APIKeyTimeUsageTotals       `json:"unattributed"`
	Daily        []APIKeyTimeUsageDaily      `json:"daily"`
	KeySummaries []APIKeyTimeUsageKeySummary `json:"key_summaries"`
}
