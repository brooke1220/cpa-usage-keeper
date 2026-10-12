import { describe, expect, it } from 'vitest';
import { addDateDays, addMonths, alignTimeUsageDays, formatUsageMonth, isWeekendDate, monthToDateRange, parseStoredTimeUsageRange, presetDates, previousMonthToDateRange, resolveTimeZone, selectableUsageMonths, sortTimeUsageKeys, statisticalDate, statisticalWeekday, timeUsageAvatarID, usageMonthRange, validTimeUsageDates, validWorkHours, zonedDate } from '../model';
import { sampleReport } from './fixtures';
import { apiKeyTimeUsageMessages } from '../messages';

describe('API Key time usage calendar and sorting', () => {
  it('uses the project time zone calendar even around UTC midnight', () => {
    expect(zonedDate(new Date('2026-10-08T16:01:00Z'), 'Asia/Shanghai')).toBe('2026-10-09');
    expect(zonedDate(new Date('2026-10-08T15:59:59Z'), 'Asia/Shanghai')).toBe('2026-10-08');
    expect(zonedDate(new Date('2026-10-09T03:00:00Z'), 'America/New_York')).toBe('2026-10-08');
    expect(addDateDays('2024-03-01', -1)).toBe('2024-02-29');
    expect(presetDates('7d', '2026-10-03')).toEqual({ start: '2026-09-27', end: '2026-10-03' });
    expect(presetDates('30d', '2026-10-09')).toEqual({ start: '2026-09-10', end: '2026-10-09' });
    expect(presetDates('yesterday', '2026-10-01')).toEqual({ start: '2026-09-30', end: '2026-09-30' });
    expect(presetDates('today', '2026-10-09')).toEqual({ start: '2026-10-09', end: '2026-10-09' });
    expect(statisticalDate(new Date('2026-10-08T23:59:00Z'), '09:00', 'Asia/Shanghai')).toBe('2026-10-08');
    expect(statisticalDate(new Date('2026-10-09T01:00:00Z'), '09:00', 'Asia/Shanghai')).toBe('2026-10-09');
    expect(statisticalDate(new Date('2026-10-08T23:00:00Z'), '23:30', 'Asia/Shanghai')).toBe('2026-10-08');
    // 13:30 UTC is 09:30 in New York but already 21:30 in Shanghai.
    expect(statisticalDate(new Date('2026-10-09T13:30:00Z'), '10:00', 'America/New_York')).toBe('2026-10-08');
    expect(statisticalDate(new Date('2026-10-09T13:30:00Z'), '10:00', 'Asia/Shanghai')).toBe('2026-10-09');
    expect(monthToDateRange('2026-10-09')).toEqual({ start: '2026-10-01', end: '2026-10-09' });
    expect(previousMonthToDateRange('2026-10-01')).toEqual({ start: '2026-09-01', end: '2026-10-01' });
    expect(previousMonthToDateRange('2026-03-01')).toEqual({ start: '2026-02-01', end: '2026-03-01' });
  });
  it('offers only complete months inside the statistical window', () => {
    expect(selectableUsageMonths('2026-07-12', '2026-10-09')).toEqual(['2026-08', '2026-09', '2026-10']);
    expect(selectableUsageMonths('2026-07-03', '2026-09-30')).toEqual(['2026-08', '2026-09']);
    expect(usageMonthRange('2026-10', '2026-10-09')).toEqual({ start: '2026-10-01', end: '2026-10-09' });
    expect(usageMonthRange('2026-09', '2026-10-09')).toEqual({ start: '2026-09-01', end: '2026-09-30' });
    expect(usageMonthRange('2026-02', '2026-03-15')).toEqual({ start: '2026-02-01', end: '2026-02-28' });
    expect(addMonths('2026-03', -1)).toBe('2026-02');
    expect(addMonths('2026-01', -1)).toBe('2025-12');
    expect(formatUsageMonth('2026-10', 'zh')).toBe('2026年10月');
    expect(formatUsageMonth('2026-10', 'en')).toBe('October 2026');
  });
  it('rejects malformed dates, reversed ranges, future dates and dates beyond 90 days', () => {
    const today = '2026-10-09';
    expect(validTimeUsageDates(addDateDays(today, -89), today, today)).toBe(true);
    for (const [start, end] of [[addDateDays(today, -90), today], [today, '2026-10-10'], [today, '2026-10-08'], ['', today], ['2026-09-31', today], ['2026-10-9', today]]) {
      expect(validTimeUsageDates(start, end, today)).toBe(false);
    }
  });
  it('sorts without mutating responses and leaves undefined averages last', () => {
    const rows = sampleReport().key_summaries;
    expect(sortTimeUsageKeys(rows, 'after_hours_share', true)[0].key.id).toBe('2');
    expect(sortTimeUsageKeys(rows, 'total_tokens', false)[0].key.id).toBe('2');
    expect(rows[0].key.id).toBe('1');
    rows[0].daily_average = null;
    expect(sortTimeUsageKeys(rows, 'daily_average', false)[1].key.id).toBe('1');
    expect(sortTimeUsageKeys(rows, 'daily_average', true)[1].key.id).toBe('1');
    expect(sortTimeUsageKeys(rows, 'message_count', true)[0].key.id).toBe('2');
    expect(sortTimeUsageKeys(rows, 'request_duration', true)[0].key.id).toBe('1');
    expect(parseStoredTimeUsageRange('{', '2026-10-09')).toBeNull();
    expect(parseStoredTimeUsageRange(JSON.stringify({ start: '2026-10-09', end: '2026-10-01' }), '2026-10-09')).toBeNull();
    expect(parseStoredTimeUsageRange(JSON.stringify({ start: '2026-10-01', end: '2026-10-09' }), '2026-10-09')).toEqual({ start: '2026-10-01', end: '2026-10-09' });
  });
  it('uses a saved avatar and otherwise matches the ranking default', () => {
    expect(timeUsageAvatarID('4', 12)).toBe(12);
    expect(timeUsageAvatarID('1')).toBe(1);
    expect(timeUsageAvatarID('2')).toBe(2);
    expect(timeUsageAvatarID('67')).toBe(1);
    expect(timeUsageAvatarID('3', 0)).toBe(3);
  });

  it('falls back to the browser zone for zone names Intl does not know', () => {
    expect(resolveTimeZone('America/New_York')).toBe('America/New_York');
    expect(resolveTimeZone('Local')).toBe(Intl.DateTimeFormat().resolvedOptions().timeZone);
    expect(resolveTimeZone(undefined)).toBe(Intl.DateTimeFormat().resolvedOptions().timeZone);
  });

  it('labels a statistical day as a civil date and fills missing key days with zero', () => {
    expect(statisticalWeekday('2026-10-01', 'en-US')).toBe('Thu');
    expect(statisticalWeekday('2026-10-09', 'en-US')).toBe('Fri');
    expect(isWeekendDate('2026-10-10')).toBe(true);
    expect(isWeekendDate('2026-10-12')).toBe(false);
    const calendar = [{ date: '2026-10-01', is_today: false }, { date: '2026-10-02', is_today: true }];
    const metrics = { message_count: 0, daytime_message_count: 0, after_hours_message_count: 0, daytime_latency_ms: 0, after_hours_latency_ms: 0, daytime_latency_samples: 0, after_hours_latency_samples: 0 };
    expect(alignTimeUsageDays(calendar, [{ date: '2026-10-02T00:00:00Z', is_today: false, total_tokens: 3, daytime_tokens: 2, after_hours_tokens: 1, ...metrics }])).toEqual([
      { date: '2026-10-01', is_today: false, total_tokens: 0, daytime_tokens: 0, after_hours_tokens: 0, ...metrics },
      { date: '2026-10-02', is_today: true, total_tokens: 3, daytime_tokens: 2, after_hours_tokens: 1, ...metrics },
    ]);
  });

  it('accepts only a same-day HH:mm work window on 15-minute steps, including 24:00', () => {
    expect(validWorkHours('09:00', '18:00')).toBe(true);
    expect(validWorkHours('09:30', '24:00')).toBe(true);
    expect(validWorkHours('09:15', '17:45')).toBe(true);
    expect(validWorkHours('9:00', '18:00')).toBe(false);
    expect(validWorkHours('18:00', '09:00')).toBe(false);
    expect(validWorkHours('09:00', '09:00')).toBe(false);
    expect(validWorkHours('09:10', '18:00')).toBe(false);
    expect(validWorkHours('09:00', '23:59')).toBe(false);
  });

  it('provides the same translation keys in all supported locales', () => {
    expect(Object.keys(apiKeyTimeUsageMessages.zh).sort()).toEqual(Object.keys(apiKeyTimeUsageMessages.en).sort());
    expect(Object.keys(apiKeyTimeUsageMessages['zh-TW']).sort()).toEqual(Object.keys(apiKeyTimeUsageMessages.en).sort());
  });
});
