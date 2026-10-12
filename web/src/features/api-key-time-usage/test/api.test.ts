// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchAPIKeyTimeUsage } from '../api';
import { sampleReport } from './fixtures';

beforeEach(() => { window.__APP_BASE_PATH__ = '/keeper'; });
afterEach(() => { vi.unstubAllGlobals(); delete window.__APP_BASE_PATH__; });

describe('time usage API', () => {
  it('uses an authenticated no-store GET with dates and key IDs, respecting base path', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(sampleReport()), { status: 200 }));
    vi.stubGlobal('fetch', fetch);
    const controller = new AbortController();
    expect(await fetchAPIKeyTimeUsage({ start: '2026-10-03', end: '2026-10-09', apiKeyId: '42' }, controller.signal)).toEqual(sampleReport());
    expect(fetch).toHaveBeenCalledWith('/keeper/api/v1/usage/api-key-time-usage?start_date=2026-10-03&end_date=2026-10-09&api_key_id=42', expect.objectContaining({ credentials: 'include', cache: 'no-store', signal: controller.signal }));
  });
  it('omits an empty key selector and preserves API errors', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'invalid_api_key_time_usage_query' }), { status: 400 }));
    vi.stubGlobal('fetch', fetch);
    await expect(fetchAPIKeyTimeUsage({ start: '', end: '', apiKeyId: '' })).rejects.toMatchObject({ status: 400, message: 'invalid_api_key_time_usage_query' });
    expect(fetch.mock.calls[0][0]).not.toContain('api_key_id');
    fetch.mockResolvedValue(new Response('unavailable', { status: 503 }));
    await expect(fetchAPIKeyTimeUsage({ start: '', end: '', apiKeyId: '' })).rejects.toMatchObject({ status: 503, message: 'api_key_time_usage_failed' });
  });
});
