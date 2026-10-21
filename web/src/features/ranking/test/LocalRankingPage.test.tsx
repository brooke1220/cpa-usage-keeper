// @vitest-environment happy-dom

import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { LocalRankingPage } from '../LocalRankingPage';
import { fetchLocalRankingLeaderboard } from '../api';
import type { RankingLeaderboardResponse } from '../types';

vi.mock('../api', async importOriginal => ({
  ...await importOriginal<typeof import('../api')>(),
  fetchLocalRankingLeaderboard: vi.fn(),
}));

globalThis.IS_REACT_ACT_ENVIRONMENT = true;
let root: Root;
let container: HTMLDivElement;
const fetchBoard = vi.mocked(fetchLocalRankingLeaderboard);
const sample = (): RankingLeaderboardResponse => ({
  period: 'today', period_key: '2026-10-21', metric: 'overall', generated_at: '2026-10-21T08:44:00Z', stale: false,
  entries: [{ rank: 1, participant_id: '1', display_name: 'Existing usage', avatar_id: 1, value: 60,
    metrics: { total_tokens: 1600, request_count: 2, cache_read_rate: 800000, latency_average: 2400000, peak_tpm: 1600, peak_rpm: 2 } }],
});

beforeEach(async () => {
  localStorage.clear();
  await i18n.changeLanguage('zh');
  fetchBoard.mockReset().mockResolvedValue(sample());
  container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount()); container.remove(); localStorage.clear(); vi.restoreAllMocks();
});

it('shows existing usage with a missing TTFT cell instead of an empty overall board', async () => {
  await act(async () => root.render(<LocalRankingPage />));
  expect(container.querySelector('[data-ranking-row]')?.textContent).toContain('Existing usage');
  expect(container.querySelector('[data-ranking-row]')?.textContent).toContain('—');
  expect(container.querySelector('[data-ranking-row]')?.textContent).not.toContain('0ms');
  expect(container.querySelector('[data-ranking-missing-measurements]')?.textContent).toContain('中性贡献');
  expect(container.textContent).not.toContain('暂时没有本地排名数据');
});

it('does not warn about missing measurements when every metric was reported', async () => {
  const board = sample(); board.entries[0].metrics!.ttft_average = 125000;
  fetchBoard.mockResolvedValue(board);
  await act(async () => root.render(<LocalRankingPage />));
  expect(container.querySelector('[data-ranking-missing-measurements]')).toBeNull();
  expect(container.querySelector('[data-ranking-row]')?.textContent).toContain('125ms');
});

it('explains missing metric samples without promising the next aggregation will create them', async () => {
  localStorage.setItem('cli-proxy-usage-ranking-preferences-v1', JSON.stringify({ version: 1, period: 'today', metric: 'ttft_average' }));
  fetchBoard.mockResolvedValue({ ...sample(), metric: 'ttft_average', entries: [] });
  await act(async () => root.render(<LocalRankingPage />));
  expect(container.textContent).toContain('暂无该指标的有效样本');
  expect(container.textContent).toContain('总 Token');
  expect(container.textContent).not.toContain('下一轮聚合后会自动显示');
});
