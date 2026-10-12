export interface TimeUsageKey {
  id: string;
  key_alias: string;
  display_key: string;
  display_name: string;
  avatar_id: number;
  is_deleted: boolean;
}
export interface TimeUsageTotals {
  total_tokens: number;
  daytime_tokens: number;
  after_hours_tokens: number;
  message_count: number;
  daytime_message_count: number;
  after_hours_message_count: number;
  daytime_latency_ms: number;
  after_hours_latency_ms: number;
  daytime_latency_samples: number;
  after_hours_latency_samples: number;
}
export interface TimeUsageStats extends TimeUsageTotals {
  daily_average: number | null;
  daytime_daily_average: number | null;
  after_hours_daily_average: number | null;
  daytime_share: number | null;
  after_hours_share: number | null;
}
export interface TimeUsageDay extends TimeUsageTotals {
  date: string;
  is_today: boolean;
}
export interface TimeUsageKeySummary extends TimeUsageStats { key: TimeUsageKey; daily: TimeUsageDay[] }
export interface TimeUsageReport {
  /** Project time zone (server TZ) used for statistical-day labels. */
  timezone: string;
  daytime_start: string;
  daytime_end: string;
  start_date: string;
  end_date: string;
  min_date: string;
  max_date: string;
  api_key_id?: string;
  generated_at: string;
  complete_days: number;
  has_records: boolean;
  keys: TimeUsageKey[];
  summary: TimeUsageStats;
  /** Usage from API keys that were never synced to Keeper; not included in summary. */
  unattributed?: TimeUsageTotals;
  daily: TimeUsageDay[];
  key_summaries: TimeUsageKeySummary[];
}
export interface TimeUsageQuery { start: string; end: string; apiKeyId: string }
export type TimeUsagePreset = 'today' | 'yesterday' | '7d' | '30d' | 'custom';
export type TimeUsageSort = 'total_tokens' | 'daily_average' | 'daytime_tokens' | 'after_hours_tokens' | 'after_hours_share' | 'message_count' | 'request_duration';
