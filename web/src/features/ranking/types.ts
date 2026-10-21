export const RANKING_PERIODS = ['today', 'yesterday', 'current_month', 'previous_month'] as const;
export type RankingPeriod = (typeof RANKING_PERIODS)[number];

export const RANKING_SCOPES = ['local', 'community'] as const;
export type RankingScope = (typeof RANKING_SCOPES)[number];

export const RANKING_METRICS = [
  'overall',
  'total_tokens',
  'request_count',
  'cache_read_rate',
  'ttft_average',
  'latency_average',
  'peak_tpm',
  'peak_rpm',
] as const;
export type RankingMetric = (typeof RANKING_METRICS)[number];
export type RankingDetailMetric = Exclude<RankingMetric, 'overall'>;

export interface RankingLeaderboardEntry {
  rank: number;
  participant_id: string;
  display_name: string;
  key_alias?: string;
  avatar_id: number;
  value: number;
  rate_numerator?: number;
  rate_denominator?: number;
  metrics?: Partial<Record<RankingDetailMetric, number>>;
}

export interface LocalRankingProfileRequest {
  key_alias: string;
  avatar_id: number;
}

export interface LocalRankingProfileResponse extends LocalRankingProfileRequest {
  participant_id: string;
  display_name: string;
}

export interface RankingScoreExplanation {
  version: number;
  texts?: Partial<Record<'en' | 'zh' | 'zh-TW', string>> | null;
}

export interface RankingLeaderboardResponse {
  period: RankingPeriod;
  period_key: string;
  metric: RankingMetric;
  generated_at: string;
  stale: boolean;
  score_explanation?: RankingScoreExplanation;
  entries: RankingLeaderboardEntry[];
}

