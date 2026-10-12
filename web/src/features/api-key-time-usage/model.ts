import type { TimeUsageDay, TimeUsageKeySummary, TimeUsagePreset, TimeUsageSort, TimeUsageTotals } from './types';

// Work-hour boundaries must sit on the server's 15-minute rollup buckets.
export const WORK_HOURS_STEP_MINUTES = 15;

const browserTimeZone = (): string => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
};

// The server reports the project time zone (TZ). Fall back to the browser zone when
// Intl does not know that name, so the page keeps working instead of throwing.
export const resolveTimeZone = (timeZone?: string | null): string => {
  if (timeZone) {
    try {
      new Intl.DateTimeFormat('en-US', { timeZone }).format(0);
      return timeZone;
    } catch {
      // Unknown zone names (for example "Local") fall through to the browser zone.
    }
  }
  return browserTimeZone();
};

export const zonedDate = (now = new Date(), timeZone?: string | null): string => {
  const parts = new Intl.DateTimeFormat('en-US', { timeZone: resolveTimeZone(timeZone), year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(now);
  const part = (type: string) => parts.find(item => item.type === type)!.value;
  return `${part('year')}-${part('month')}-${part('day')}`;
};
const startMinute = (value: string) => {
  if (!/^\d{2}:\d{2}$/.test(value)) return 9 * 60;
  const hour = Number(value.slice(0, 2));
  const minute = Number(value.slice(3));
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour > 23 || minute > 59) return 9 * 60;
  return hour * 60 + minute;
};
// Statistical days begin at the configured work start in the project time zone.
// Before that clock time, the current statistical day is still labelled with
// yesterday's calendar date.
export const statisticalDate = (now = new Date(), workStart = '09:00', timeZone?: string | null): string => {
  const zone = resolveTimeZone(timeZone);
  const date = zonedDate(now, zone);
  const parts = new Intl.DateTimeFormat('en-GB', { timeZone: zone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(now);
  const part = (type: string) => Number(parts.find(item => item.type === type)?.value ?? 0);
  const currentMinute = part('hour') * 60 + part('minute');
  return currentMinute < startMinute(workStart) ? addDateDays(date, -1) : date;
};
// Calendar arithmetic is deliberately independent of the browser timezone/DST.
export const addDateDays = (date: string, days: number): string => {
  const value = new Date(`${date}T00:00:00Z`);
  value.setUTCDate(value.getUTCDate() + days);
  return value.toISOString().slice(0, 10);
};
export const monthKey = (date: string) => date.slice(0, 7);
export const addMonths = (month: string, delta: number): string => {
  const [year, monthNumber] = month.split('-').map(Number);
  const date = new Date(Date.UTC(year, monthNumber - 1 + delta, 1));
  return `${date.getUTCFullYear()}-${String(date.getUTCMonth() + 1).padStart(2, '0')}`;
};
export const lastDateOfMonth = (month: string) => addDateDays(`${addMonths(month, 1)}-01`, -1);
export const selectableUsageMonths = (minDate: string, maxDate: string): string[] => {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(minDate) || !/^\d{4}-\d{2}-\d{2}$/.test(maxDate) || minDate > maxDate) return [];
  const months: string[] = [];
  let month = monthKey(maxDate);
  for (let guard = 0; guard < 6 && `${month}-01` >= minDate; guard += 1) {
    months.push(month);
    month = addMonths(month, -1);
  }
  return months.reverse();
};
export const usageMonthRange = (month: string, maxDate: string) => ({
  start: `${month}-01`,
  end: month === monthKey(maxDate) ? maxDate : lastDateOfMonth(month),
});
// Labels are civil dates; format them in UTC so no zone can shift the day.
export const formatUsageMonth = (month: string, locale: string) =>
  new Intl.DateTimeFormat(locale, { timeZone: 'UTC', year: 'numeric', month: 'long' }).format(new Date(`${month}-01T12:00:00Z`));
export const monthToDateRange = (today: string) => ({ start: `${today.slice(0, 7)}-01`, end: today });
export const previousMonthToDateRange = (today: string) => {
  const previousLastDay = addDateDays(`${today.slice(0, 7)}-01`, -1);
  return { start: `${previousLastDay.slice(0, 7)}-01`, end: today };
};
export const presetDates = (preset: Exclude<TimeUsagePreset, 'custom'>, today: string) => {
  const end = preset === 'yesterday' ? addDateDays(today, -1) : today;
  return { start: addDateDays(end, preset === '7d' ? -6 : preset === '30d' ? -29 : 0), end };
};
export const validTimeUsageDates = (start: string, end: string, today: string): boolean => {
  const valid = (value: string) => /^\d{4}-\d{2}-\d{2}$/.test(value) && Number.isFinite(Date.parse(`${value}T00:00:00Z`)) && new Date(`${value}T00:00:00Z`).toISOString().slice(0, 10) === value;
  return valid(start) && valid(end) && start <= end && start >= addDateDays(today, -89) && end <= today;
};
export const TIME_USAGE_RANGE_STORAGE_KEY = 'api-key-time-usage-range';
export const parseStoredTimeUsageRange = (raw: string | null, today: string): { start: string; end: string } | null => {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as { start?: unknown; end?: unknown };
    if (typeof parsed.start !== 'string' || typeof parsed.end !== 'string' || !validTimeUsageDates(parsed.start, parsed.end, today)) return null;
    return { start: parsed.start, end: parsed.end };
  } catch {
    return null;
  }
};
export const readStoredTimeUsageRange = (today: string, storage?: Pick<Storage, 'getItem'> | null) => {
  try {
    const target = storage !== undefined ? storage : (typeof window === 'undefined' ? null : window.localStorage);
    return parseStoredTimeUsageRange(target?.getItem(TIME_USAGE_RANGE_STORAGE_KEY) ?? null, today);
  } catch {
    return null;
  }
};
export const writeStoredTimeUsageRange = (range: { start: string; end: string }, storage?: Pick<Storage, 'setItem'> | null) => {
  try {
    const target = storage !== undefined ? storage : (typeof window === 'undefined' ? null : window.localStorage);
    target?.setItem(TIME_USAGE_RANGE_STORAGE_KEY, JSON.stringify({ start: range.start, end: range.end }));
  } catch {
    // Private mode or a full quota should not block the selected range.
  }
};
const sortMetric = (row: TimeUsageKeySummary, column: TimeUsageSort): number | null => (
  column === 'request_duration' ? (row.daytime_latency_ms ?? 0) + (row.after_hours_latency_ms ?? 0) : row[column]
);
export const sortTimeUsageKeys = (rows: readonly TimeUsageKeySummary[], column: TimeUsageSort, descending: boolean) => [...rows].sort((a, b) => {
  const left = sortMetric(a, column);
  const right = sortMetric(b, column);
  if (left === null && right !== null) return 1;
  if (right === null && left !== null) return -1;
  const difference = (left ?? 0) - (right ?? 0);
  return (descending ? -difference : difference) || a.key.id.localeCompare(b.key.id, undefined, { numeric: true });
});

export const timeUsageAvatarID = (id: string, avatarID?: number | null) => {
  if (avatarID != null && avatarID >= 1 && avatarID <= 66) return Math.trunc(avatarID);
  const numeric = Number(id);
  if (!Number.isInteger(numeric) || numeric <= 0) return 1;
  return 1 + ((numeric - 1) % 66);
};

export const statisticalWeekday = (date: string, locale: string) =>
  new Intl.DateTimeFormat(locale, { timeZone: 'UTC', weekday: 'short' }).format(new Date(`${date}T12:00:00Z`));

export const isWeekendDate = (date: string) => {
  const day = new Date(`${date}T12:00:00Z`).getUTCDay();
  return day === 0 || day === 6;
};

const usageDate = (value: string | undefined) => value?.slice(0, 10) ?? '';

const emptyTotals = (): TimeUsageTotals => ({
  total_tokens: 0, daytime_tokens: 0, after_hours_tokens: 0, message_count: 0, daytime_message_count: 0, after_hours_message_count: 0,
  daytime_latency_ms: 0, after_hours_latency_ms: 0, daytime_latency_samples: 0, after_hours_latency_samples: 0,
});
export const alignTimeUsageDays = (calendar: readonly Pick<TimeUsageDay, 'date' | 'is_today'>[], daily: readonly TimeUsageDay[] | undefined): TimeUsageDay[] => {
  const rows = daily ?? [];
  const byDate = new Map(rows.map(day => [usageDate(day.date), day]));
  // The API returns one entry per calendar day in the same order. Match by date first,
  // then by index so a datetime suffix or a missing date cannot zero out a populated day.
  return calendar.map((day, index) => {
    const matched = byDate.get(usageDate(day.date)) ?? (rows.length === calendar.length ? rows[index] : undefined);
    if (!matched) return { date: day.date, is_today: day.is_today, ...emptyTotals() };
    return {
      date: day.date,
      is_today: day.is_today,
      ...emptyTotals(),
      total_tokens: matched.total_tokens ?? 0,
      daytime_tokens: matched.daytime_tokens ?? 0,
      after_hours_tokens: matched.after_hours_tokens ?? 0,
      message_count: matched.message_count ?? 0,
      daytime_message_count: matched.daytime_message_count ?? 0,
      after_hours_message_count: matched.after_hours_message_count ?? 0,
      daytime_latency_ms: matched.daytime_latency_ms ?? 0,
      after_hours_latency_ms: matched.after_hours_latency_ms ?? 0,
      daytime_latency_samples: matched.daytime_latency_samples ?? 0,
      after_hours_latency_samples: matched.after_hours_latency_samples ?? 0,
    };
  });
};

const workMinute = (value: string, allowEndOfDay: boolean): number | null => {
  if (!/^\d{2}:\d{2}$/.test(value)) return null;
  const hour = Number(value.slice(0, 2));
  const minute = Number(value.slice(3));
  if (allowEndOfDay && hour === 24 && minute === 0) return 1440;
  if (hour > 23 || minute > 59) return null;
  return hour * 60 + minute;
};
export const validWorkHours = (start: string, end: string) => {
  const first = workMinute(start, false);
  const last = workMinute(end, true);
  return first != null && last != null && first < last
    && first % WORK_HOURS_STEP_MINUTES === 0 && last % WORK_HOURS_STEP_MINUTES === 0;
};
