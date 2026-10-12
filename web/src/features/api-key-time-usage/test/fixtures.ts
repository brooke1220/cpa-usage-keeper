import type { TimeUsageDay, TimeUsageReport, TimeUsageTotals } from '../types';
import { addDateDays } from '../model';

const zeroMetrics = {
  message_count: 0,
  daytime_message_count: 0,
  after_hours_message_count: 0,
  daytime_latency_ms: 0,
  after_hours_latency_ms: 0,
  daytime_latency_samples: 0,
  after_hours_latency_samples: 0,
} satisfies Omit<TimeUsageTotals, 'total_tokens' | 'daytime_tokens' | 'after_hours_tokens'>;

const day = (date: string, total: number, daytime: number, today = false): TimeUsageDay => ({
  date, is_today: today, total_tokens: total, daytime_tokens: daytime, after_hours_tokens: total - daytime, ...zeroMetrics,
});

export const sampleReport = (): TimeUsageReport => {
  const daily = Array.from({ length: 9 }, (_, index) => day(
    addDateDays('2026-10-01', index),
    index === 0 ? 600 : index === 8 ? 300 : 0,
    index === 0 ? 420 : index === 8 ? 180 : 0,
    index === 8,
  ));
  const split = (numerator: number, denominator: number) => daily.map(item => day(
    item.date,
    item.total_tokens * numerator / denominator,
    item.daytime_tokens * numerator / denominator,
    item.is_today,
  ));
  return {
    timezone: 'Asia/Shanghai', daytime_start: '09:00', daytime_end: '18:00', start_date: '2026-10-01', end_date: '2026-10-09',
    min_date: '2026-07-12', max_date: '2026-10-09', generated_at: '2026-10-09T12:00:00+08:00', complete_days: 8, has_records: true,
    keys: [
      { id: '1', key_alias: 'Team Alpha', display_key: 'sk-*********123456', display_name: 'Team Alpha', avatar_id: 12, is_deleted: false },
      { id: '2', key_alias: 'Team Beta', display_key: 'sk-*********654321', display_name: 'Team Beta', avatar_id: 2, is_deleted: true },
    ],
    summary: {
      total_tokens: 900, daytime_tokens: 600, after_hours_tokens: 300,
      message_count: 13, daytime_message_count: 8, after_hours_message_count: 5, daytime_latency_ms: 5_001_000, after_hours_latency_ms: 1_000, daytime_latency_samples: 2, after_hours_latency_samples: 1,
      daily_average: 75, daytime_daily_average: 52.5, after_hours_daily_average: 22.5, daytime_share: 2 / 3, after_hours_share: 1 / 3,
    },
    daily,
    key_summaries: [
      { key: { id: '1', key_alias: 'Team Alpha', display_key: 'sk-*********123456', display_name: 'Team Alpha', avatar_id: 12, is_deleted: false }, total_tokens: 600, daytime_tokens: 420, after_hours_tokens: 180, message_count: 4, daytime_message_count: 3, after_hours_message_count: 1, daytime_latency_ms: 5_000_000, after_hours_latency_ms: 0, daytime_latency_samples: 1, after_hours_latency_samples: 0, daily_average: 50, daytime_daily_average: 35, after_hours_daily_average: 15, daytime_share: 0.7, after_hours_share: 0.3, daily: split(2, 3) },
      { key: { id: '2', key_alias: 'Team Beta', display_key: 'sk-*********654321', display_name: 'Team Beta', avatar_id: 2, is_deleted: true }, total_tokens: 300, daytime_tokens: 180, after_hours_tokens: 120, message_count: 9, daytime_message_count: 5, after_hours_message_count: 4, daytime_latency_ms: 1_000, after_hours_latency_ms: 1_000, daytime_latency_samples: 1, after_hours_latency_samples: 1, daily_average: 25, daytime_daily_average: 17.5, after_hours_daily_average: 7.5, daytime_share: 0.6, after_hours_share: 0.4, daily: split(1, 3) },
    ],
  };
};
