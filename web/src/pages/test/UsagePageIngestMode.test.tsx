// @vitest-environment happy-dom

import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';

const api = vi.hoisted(() => ({
  fetchStatus: vi.fn(),
  fetchUsageIdentitiesPage: vi.fn(),
}));

vi.mock('react-chartjs-2', () => ({ Bar: () => null, Chart: () => null, Doughnut: () => null, Line: () => null, Scatter: () => null }));

vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  ...api,
  fetchVersion: async () => ({ version: 'test' }),
  fetchCpaApiKeyOptions: async () => ({ options: [] }),
  fetchUsageQuotaCache: async () => ({ results: [] }),
  fetchUsageQuotaInspectionStatus: async () => ({}),
  fetchQuotaAutoRefreshSettings: async () => ({}),
  fetchUsageQuotaResetCredits: async () => ({}),
}));

import { UsagePage } from '../UsagePage';

describe('UsagePage ingest mode gating', () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    globalThis.IS_REACT_ACT_ENVIRONMENT = true;
    await i18n.changeLanguage('en');
    localStorage.clear();
    Object.values(api).forEach((mock) => mock.mockReset());
    api.fetchUsageIdentitiesPage.mockResolvedValue({ identities: [], total_count: 0, total_pages: 0 });
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    localStorage.clear();
    vi.restoreAllMocks();
  });

  const tabLabels = () => Array.from(container.querySelectorAll('[role="tab"]')).map((node) => node.textContent?.trim());

  it('still loads pull-mode credentials when the status request fails', async () => {
    // 回归：凭证页曾等待 /status 成功才加载，一次状态请求失败就会整页无数据。
    api.fetchStatus.mockRejectedValue(new Error('status offline'));
    window.history.replaceState(null, '', '/credentials');
    await act(async () => root.render(<UsagePage />));
    expect(api.fetchUsageIdentitiesPage).toHaveBeenCalled();
    expect(tabLabels()).toContain('Credentials');
  });

  it('hides remote credential tabs in push mode without waiting for status', async () => {
    api.fetchStatus.mockRejectedValue(new Error('status offline'));
    window.history.replaceState(null, '', '/credentials');
    await act(async () => root.render(<UsagePage ingestMode="push" />));
    expect(api.fetchUsageIdentitiesPage).not.toHaveBeenCalled();
    expect(tabLabels()).not.toContain('Credentials');
    expect(tabLabels()).not.toContain('AI Provider');
    expect(window.location.pathname).toBe('/overview');
    expect(window.location.search).toBe('');
  });

  it('keeps the CPAMC embed marker when push mode leaves a credential tab', async () => {
    api.fetchStatus.mockRejectedValue(new Error('status offline'));
    window.history.replaceState(null, '', '/credentials?embed=cpamc');
    await act(async () => root.render(<UsagePage ingestMode="push" />));
    expect(window.location.pathname).toBe('/overview');
    expect(window.location.search).toBe('?embed=cpamc');
  });
});
