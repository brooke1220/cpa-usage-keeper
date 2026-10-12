// @vitest-environment happy-dom

import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { APIKeyTimeUsagePage } from '../APIKeyTimeUsagePage';
import { fetchAPIKeyTimeUsage, fetchWorkHours } from '../api';
import { TIME_USAGE_RANGE_STORAGE_KEY } from '../model';
import { ApiError } from '@/lib/api';
import { sampleReport } from './fixtures';
import type { TimeUsageReport } from '../types';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;
vi.mock('../api', () => ({ fetchAPIKeyTimeUsage: vi.fn(), fetchWorkHours: vi.fn() }));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string, params?: Record<string, string | number>) => params ? `${key}:${JSON.stringify(params)}` : key, i18n: { language: 'en' } }),
  initReactI18next: { type: '3rdParty', init: () => undefined },
}));

let root: Root;
let container: HTMLDivElement;
const api = vi.mocked(fetchAPIKeyTimeUsage);
const workHours = vi.mocked(fetchWorkHours);
const weekQuery = { start: '2026-10-03', end: '2026-10-09', apiKeyId: '' };
const click = async (selector: string) => { await act(async () => container.querySelector<HTMLButtonElement>(selector)!.click()); };
const setDate = async (selector: string, value: string) => {
  const input = container.querySelector<HTMLInputElement>(selector)!;
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
  await act(async () => {
    setter?.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
  });
};

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2026-10-09T04:00:00Z'));
  localStorage.removeItem(TIME_USAGE_RANGE_STORAGE_KEY);
  api.mockReset().mockResolvedValue(sampleReport());
  workHours.mockReset().mockResolvedValue({ timezone: 'Asia/Shanghai', start: '09:00', end: '18:00', source: 'default' });
  container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container);
});
afterEach(async () => { await act(async () => root.unmount()); container.remove(); localStorage.removeItem(TIME_USAGE_RANGE_STORAGE_KEY); vi.useRealTimers(); vi.restoreAllMocks(); });

describe('API Key time usage page', () => {
  it('uses the previous statistical day before work starts', async () => {
    vi.setSystemTime(new Date('2026-10-11T23:00:00Z')); // 07:00 on Oct 12 in Shanghai.
    await act(async () => root.render(<APIKeyTimeUsagePage />));
    expect(api).toHaveBeenCalledWith({ start: '2026-10-05', end: '2026-10-11', apiKeyId: '' }, expect.any(AbortSignal));
    expect(container.querySelector<HTMLInputElement>('[data-range-end]')?.value).toBe('2026-10-11');
    expect(JSON.parse(localStorage.getItem(TIME_USAGE_RANGE_STORAGE_KEY) ?? '{}').end).toBe('2026-10-11');
  });

  it('loads the last 7 days and stacks message count and duration under the token figures', async () => {
    await act(async () => root.render(<APIKeyTimeUsagePage />));
    expect(api).toHaveBeenCalledWith(weekQuery, expect.any(AbortSignal));
    expect(container.querySelector<HTMLInputElement>('[data-range-start]')?.value).toBe('2026-10-03');
    expect(container.querySelector<HTMLInputElement>('[data-range-end]')?.value).toBe('2026-10-09');
    expect(container.querySelector('[data-range-preset="7d"]')?.getAttribute('aria-pressed')).toBe('true');
    expect(container.querySelector('tfoot [data-stat="total_tokens"] [data-figure="tokens"]')?.textContent).toBe('900');
    expect(container.querySelector('tfoot [data-stat="daytime_tokens"] [data-figure="tokens"]')?.textContent).toBe('600');
    expect(container.querySelector('tfoot [data-stat="total_tokens"] [data-metric="messages"]')?.textContent).toContain('13');
    expect(container.querySelector('tfoot [data-stat="daytime_tokens"] [data-metric="messages"]')?.textContent).toContain('8');
    expect(container.querySelector('tfoot [data-stat="after_hours_tokens"] [data-metric="duration"]')?.textContent).not.toBe('');
    expect(container.querySelector('[data-range-message]')?.textContent).toContain('13');
    expect(container.querySelector('[data-range-duration="work"]')?.getAttribute('title')).toContain('5,001,000 ms');
    expect(container.querySelector('[data-range-duration="off"]')?.getAttribute('title')).toContain('api_key_time_usage.duration_average');
    expect(container.querySelector('[data-range-duration="off"]')?.getAttribute('title')).toContain('api_key_time_usage.duration_samples');
    expect(container.querySelector('[data-ranking-avatar-id="12"]')).not.toBeNull();
    expect([...container.querySelectorAll('strong')].some(node => node.textContent === 'Team Alpha')).toBe(true);
    expect(container.textContent).toContain('api_key_time_usage.deleted');
    expect(container.querySelector('[data-day-rule]')?.textContent).toContain('api_key_time_usage.day_note');
    expect(container.querySelector('[data-day-rule]')?.textContent).toContain('09:00');
    expect(container.querySelector('[data-day-rule]')?.textContent).toContain('18:00');
    expect(container.querySelector('[data-time-usage-legend]')?.textContent).toContain('api_key_time_usage.daytime_tokens');
    expect(container.querySelector('[data-time-usage-legend]')?.textContent).toContain('api_key_time_usage.after_hours_tokens');
    expect(container.textContent).not.toContain('api_key_time_usage.trend');
    const days = [...container.querySelectorAll<HTMLElement>('[data-daily-ledger] thead [data-day]')].map(node => node.getAttribute('data-day'));
    expect(days).toEqual(['2026-10-01', '2026-10-02', '2026-10-03', '2026-10-04', '2026-10-05', '2026-10-06', '2026-10-07', '2026-10-08', '2026-10-09']);
    expect(container.querySelector('[data-day="2026-10-09"]')?.textContent).not.toContain('in_progress');
    expect(container.querySelector('tbody tr')?.getAttribute('data-time-usage-key')).toBe('1');
    expect(container.querySelector('[data-time-usage-key="1"] [data-date="2026-10-01"] [data-figure="tokens"][data-period="work"]')?.textContent).toBe('280');
    expect(container.querySelector('[data-time-usage-key="1"] [data-date="2026-10-01"] [data-figure="tokens"][data-period="off"]')?.textContent).toBe('120');
    expect(container.querySelector('[data-time-usage-key="1"] [data-date="2026-10-01"] [data-metric="messages"]')).not.toBeNull();
    expect(container.querySelector('[data-time-usage-key="1"] [data-date="2026-10-01"] [data-metric="duration"][data-period="work"]')).not.toBeNull();
    expect(container.querySelector('[data-time-usage-key="1"] [data-date="2026-10-01"] [data-metric="duration"][data-period="off"]')).not.toBeNull();
    expect(container.querySelector('[data-time-usage-key="2"] [data-date="2026-10-01"] [data-figure="tokens"][data-period="work"]')?.textContent).toBe('140');
    expect([...container.querySelectorAll('[data-sort]')].map(node => node.getAttribute('data-sort'))).toEqual(['total_tokens', 'daytime_tokens', 'after_hours_tokens']);
    expect([...container.querySelectorAll('[data-time-usage-key="1"] [data-key-total]')].map(node => node.getAttribute('data-key-total'))).toEqual(['total', 'daytime', 'after_hours']);
    expect(container.querySelector('[data-time-usage-key="1"] [data-key-total="daytime"] [data-figure="tokens"]')?.textContent).toBe('420');
    expect(container.querySelector('[data-time-usage-key="1"] [data-key-total="total"] [data-metric="messages"]')?.textContent).toContain('4');
    expect(container.querySelector('[data-time-usage-key="1"] [data-key-total="daytime"] [data-metric="duration"]')?.getAttribute('title')).toContain('5,000,000 ms');
    expect(container.querySelector('[data-time-usage-key="1"] [data-key-total="after_hours"] [data-metric="duration"]')?.getAttribute('title')).toContain('0 ms');
    expect(container.querySelector('tfoot [data-metric="messages"]')).not.toBeNull();
    expect(container.querySelector('tfoot [data-metric="duration"]')).not.toBeNull();
    expect(container.querySelector('[data-ranking-time-usage-cell]')).toBeNull();
    expect(container.querySelector('[role="tab"]')).toBeNull();
    expect(container.querySelector('[data-usage-month]')).toBeNull();
    expect(container.querySelector('[data-key-id]')).toBeNull();
    expect(container.querySelector('[data-chart-scope]')).toBeNull();
    expect(container.querySelector('[data-chart]')).toBeNull();
    expect(container.textContent).not.toContain('sk-secret');
    await click('[data-sort="total_tokens"]');
    expect(container.querySelector('tbody tr')?.getAttribute('data-time-usage-key')).toBe('2');
  });

  it('computes the statistical day in the project time zone from the schedule', async () => {
    // 04:00 UTC is 00:00 in New York, before the 09:00 work start: the current statistical day is still Oct 8.
    workHours.mockResolvedValue({ timezone: 'America/New_York', start: '09:00', end: '18:00', source: 'default' });
    await act(async () => root.render(<APIKeyTimeUsagePage />));
    expect(api).toHaveBeenCalledWith({ start: '2026-10-02', end: '2026-10-08', apiKeyId: '' }, expect.any(AbortSignal));
  });

  it('notes usage from API keys that were never synced instead of hiding it', async () => {
    const data = sampleReport();
    data.unattributed = { total_tokens: 1234, daytime_tokens: 1000, after_hours_tokens: 234, message_count: 3, daytime_message_count: 2, after_hours_message_count: 1, daytime_latency_ms: 0, after_hours_latency_ms: 0, daytime_latency_samples: 0, after_hours_latency_samples: 0 };
    api.mockResolvedValue(data);
    await act(async () => root.render(<APIKeyTimeUsagePage />));
    expect(container.querySelector('[data-unattributed-note]')?.textContent).toContain('api_key_time_usage.unattributed_note');
    expect(container.querySelector('[data-unattributed-note]')?.textContent).toContain('"messages":"3"');
    api.mockResolvedValue(sampleReport());
    await act(async () => root.render(<APIKeyTimeUsagePage refreshToken={1} />));
    expect(container.querySelector('[data-unattributed-note]')).toBeNull();
  });

  it('shows the server-clamped end date without a second request', async () => {
    const clamped = sampleReport();
    clamped.start_date = '2026-10-03';
    clamped.end_date = '2026-10-08';
    clamped.max_date = '2026-10-08';
    api.mockResolvedValue(clamped);
    await act(async () => root.render(<APIKeyTimeUsagePage />));
    expect(api).toHaveBeenCalledTimes(1);
    expect(api).toHaveBeenCalledWith(weekQuery, expect.any(AbortSignal));
    expect(container.querySelector<HTMLInputElement>('[data-range-end]')?.value).toBe('2026-10-08');
    expect(container.querySelector('[role="alert"]')).toBeNull();
  });

  it('shows an empty range without fake percentages', async () => {
    const data = sampleReport();
    data.complete_days = 0; data.has_records = false;
    data.summary = { total_tokens: 0, daytime_tokens: 0, after_hours_tokens: 0, message_count: 0, daytime_message_count: 0, after_hours_message_count: 0, daytime_latency_ms: 0, after_hours_latency_ms: 0, daytime_latency_samples: 0, after_hours_latency_samples: 0, daily_average: null, daytime_daily_average: null, after_hours_daily_average: null, daytime_share: null, after_hours_share: null };
    data.key_summaries = [];
    api.mockResolvedValue(data);
    await act(async () => root.render(<APIKeyTimeUsagePage />));
    expect(container.querySelector('[data-stat]')).toBeNull();
    expect(container.textContent).not.toContain('api_key_time_usage.empty');
    expect(container.textContent).not.toContain('NaN');
    expect(container.textContent).not.toContain('%');
  });

  it('shows errors and retries manually, including the parent header refresh', async () => {
    api.mockRejectedValueOnce(new Error('failure'));
    await act(async () => root.render(<APIKeyTimeUsagePage refreshToken={0} />));
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('api_key_time_usage.error');
    await click('[role="alert"] button');
    expect(container.querySelector('tfoot [data-stat="total_tokens"]')).not.toBeNull();
    await act(async () => root.render(<APIKeyTimeUsagePage refreshToken={1} />));
    expect(api.mock.calls.length).toBeGreaterThanOrEqual(3);
    expect(api).toHaveBeenLastCalledWith(weekQuery, expect.any(AbortSignal));
  });

  it('aborts the in-flight range request and ignores its late response', async () => {
    let resolveOld!: (report: TimeUsageReport) => void;
    api.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve; }));
    await act(async () => root.render(<APIKeyTimeUsagePage refreshToken={0} />));
    const oldSignal = api.mock.calls[0][1]!;
    await act(async () => root.render(<APIKeyTimeUsagePage refreshToken={1} />));
    expect(oldSignal.aborted).toBe(true);
    expect(container.querySelector('tfoot [data-stat="total_tokens"] [data-figure="tokens"]')?.textContent).toBe('900');
    const oldReport = sampleReport(); oldReport.summary.total_tokens = 99999;
    await act(async () => resolveOld(oldReport));
    expect(container.querySelector('tfoot [data-stat="total_tokens"] [data-figure="tokens"]')?.textContent).toBe('900');
  });

  it('opens when a key summary has no daily series', async () => {
    const data = sampleReport();
    data.key_summaries = data.key_summaries.map(row => ({ ...row, daily: undefined as never }));
    api.mockResolvedValue(data);
    await act(async () => root.render(<APIKeyTimeUsagePage />));
    expect(container.querySelector('tfoot [data-stat="total_tokens"] [data-figure="tokens"]')?.textContent).toBe('900');
    expect(container.querySelector('[data-time-usage-key="1"] [data-date="2026-10-01"] [data-figure="tokens"][data-period="work"]')?.textContent).toBe('0');
    expect(container.querySelector('[data-time-usage-key="1"] [data-key-total="daytime"] [data-figure="tokens"]')?.textContent).toBe('420');
  });

  it('uses ranking units for large counts and keeps the exact value on hover', async () => {
    const data = sampleReport();
    data.summary.total_tokens = 74_707_157;
    data.summary.daytime_tokens = 74_707_157;
    data.key_summaries[0].total_tokens = 74_707_157;
    data.key_summaries[0].daytime_tokens = 74_707_157;
    data.key_summaries[0].daily[0] = { ...data.key_summaries[0].daily[0], total_tokens: 74_707_157, daytime_tokens: 74_707_157, after_hours_tokens: 0 };
    api.mockResolvedValue(data);
    await act(async () => root.render(<APIKeyTimeUsagePage />));
    expect(container.querySelector('tfoot [data-stat="total_tokens"] [data-figure="tokens"]')?.textContent).toBe('74.71M');
    expect(container.querySelector('tfoot [data-stat="total_tokens"] [data-figure="tokens"]')?.getAttribute('title')).toBe('74,707,157');
    expect(container.querySelector('[data-time-usage-key="1"] [data-date="2026-10-01"] [data-figure="tokens"][data-period="work"]')?.textContent).toBe('74.71M');
    expect(container.querySelector('[data-time-usage-key="1"] [data-date="2026-10-01"] [data-figure="tokens"][data-period="work"]')?.getAttribute('title')).toBe('74,707,157');
  });

  it('changes the range from presets or dates and keeps that range on refresh', async () => {
    await act(async () => root.render(<APIKeyTimeUsagePage />));
    await click('[data-range-preset="30d"]');
    expect(api).toHaveBeenLastCalledWith({ start: '2026-09-10', end: '2026-10-09', apiKeyId: '' }, expect.any(AbortSignal));
    expect(container.querySelector('[data-range-preset="30d"]')?.getAttribute('aria-pressed')).toBe('true');
    await click('[data-range-preset="today"]');
    expect(api).toHaveBeenLastCalledWith({ start: '2026-10-09', end: '2026-10-09', apiKeyId: '' }, expect.any(AbortSignal));
    await setDate('[data-range-start]', '2026-10-05');
    expect(api).toHaveBeenLastCalledWith({ start: '2026-10-05', end: '2026-10-09', apiKeyId: '' }, expect.any(AbortSignal));
    expect(container.querySelector('[data-range-preset="7d"]')?.getAttribute('aria-pressed')).toBe('false');
    await act(async () => root.render(<APIKeyTimeUsagePage refreshToken={1} />));
    expect(api).toHaveBeenLastCalledWith({ start: '2026-10-05', end: '2026-10-09', apiKeyId: '' }, expect.any(AbortSignal));
    await act(async () => root.unmount());
    root = createRoot(container);
    await act(async () => root.render(<APIKeyTimeUsagePage />));
    expect(api).toHaveBeenLastCalledWith({ start: '2026-10-05', end: '2026-10-09', apiKeyId: '' }, expect.any(AbortSignal));
  });

  it('does not query an invalid hand-edited range and ignores corrupt storage', async () => {
    localStorage.setItem(TIME_USAGE_RANGE_STORAGE_KEY, '{');
    await act(async () => root.render(<APIKeyTimeUsagePage />));
    expect(api).toHaveBeenLastCalledWith(weekQuery, expect.any(AbortSignal));
    const calls = api.mock.calls.length;
    await setDate('[data-range-start]', '2026-10-20');
    expect(api).toHaveBeenCalledTimes(calls);
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('api_key_time_usage.invalid_range');
    expect(container.querySelector('[data-daily-ledger]')).toBeNull();
  });

  it('keeps the current table mounted while the next range is loading', async () => {
    await act(async () => root.render(<APIKeyTimeUsagePage />));
    let resolveNext!: (report: TimeUsageReport) => void;
    api.mockImplementationOnce(() => new Promise(resolve => { resolveNext = resolve; }));
    await click('[data-range-preset="30d"]');
    expect(container.querySelector('[data-daily-ledger]')).not.toBeNull();
    expect(container.querySelector('[data-day="2026-10-09"]')).not.toBeNull();
    expect(container.querySelector('.loading, [class*="loading"]')).toBeNull();
    const next = sampleReport();
    next.daily = next.daily.map(day => ({ ...day, date: day.date.replace('2026-10', '2026-09'), is_today: false }));
    next.start_date = '2026-09-10';
    next.end_date = '2026-10-09';
    await act(async () => resolveNext(next));
    expect(container.querySelector('[data-day="2026-09-01"]')).not.toBeNull();
    expect(container.querySelector('[data-day="2026-10-09"]')).toBeNull();
  });

  it('does not query another range when an explicitly selected range is rejected', async () => {
    await act(async () => root.render(<APIKeyTimeUsagePage />));
    api.mockRejectedValueOnce(new ApiError('invalid_api_key_time_usage_query', 400));
    await click('[data-range-preset="30d"]');
    expect(api).toHaveBeenCalledTimes(2);
    expect(api).toHaveBeenLastCalledWith({ start: '2026-09-10', end: '2026-10-09', apiKeyId: '' }, expect.any(AbortSignal));
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('api_key_time_usage.invalid_range');
    await click('[role="alert"] button');
    expect(api).toHaveBeenCalledTimes(3);
    expect(api).toHaveBeenLastCalledWith({ start: '2026-09-10', end: '2026-10-09', apiKeyId: '' }, expect.any(AbortSignal));
  });

  it('returns to authentication on 401 without querying another range', async () => {
    const onAuthRequired = vi.fn();
    api.mockRejectedValue(new ApiError('auth_required', 401));
    await act(async () => root.render(<APIKeyTimeUsagePage onAuthRequired={onAuthRequired} />));
    expect(onAuthRequired).toHaveBeenCalledTimes(1);
    expect(api).toHaveBeenCalledTimes(1);
    expect(api).toHaveBeenCalledWith(weekQuery, expect.any(AbortSignal));
  });
});
