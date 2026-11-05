// @vitest-environment happy-dom

import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchHiddenCpaApiKeyIds, updateHiddenCpaApiKeyIds } from '@/lib/api';
import type { CpaApiKeySettingsItem } from '@/lib/types';
import { HiddenApiKeysSettingsCard } from '../HiddenApiKeysSettingsCard';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;
vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchHiddenCpaApiKeyIds: vi.fn(),
  updateHiddenCpaApiKeyIds: vi.fn(),
}));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key), i18n: { language: 'zh' } }),
}));

let root: Root;
let container: HTMLDivElement;
const load = vi.mocked(fetchHiddenCpaApiKeyIds);
const save = vi.mocked(updateHiddenCpaApiKeyIds);
const settle = async () => { await act(async () => { await Promise.resolve(); }); };
const apiKeys: CpaApiKeySettingsItem[] = [
  { id: '1', sourceType: 'native', accessProvider: '', keyAlias: 'Alpha', displayKey: 'sk-***alpha', label: 'Alpha', lastSyncedAt: null },
  { id: '2', sourceType: 'native', accessProvider: '', keyAlias: '', displayKey: 'sk-***beta', label: 'sk-***beta', lastSyncedAt: null },
];
const checkbox = (id: string) => container.querySelector<HTMLInputElement>(`[data-hidden-api-key="${id}"] input`)!;
const saveButton = () => container.querySelector<HTMLButtonElement>('[data-hidden-api-keys-save]')!;

beforeEach(() => {
  load.mockReset().mockResolvedValue({ ids: ['1'] });
  save.mockReset().mockImplementation(async (ids) => ({ ids: [...ids].sort() }));
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

describe('HiddenApiKeysSettingsCard', () => {
  it('checks saved keys and keeps save disabled until the selection changes', async () => {
    await act(async () => root.render(<HiddenApiKeysSettingsCard apiKeys={apiKeys} />));
    await settle();
    expect(checkbox('1').checked).toBe(true);
    expect(checkbox('2').checked).toBe(false);
    expect(container.querySelector('[data-hidden-api-key="2"]')?.textContent).toContain('sk-***beta');
    expect(saveButton().disabled).toBe(true);
    await act(async () => checkbox('2').click());
    expect(saveButton().disabled).toBe(false);
    await act(async () => checkbox('2').click());
    expect(saveButton().disabled).toBe(true);
  });

  it('saves multiple selected keys', async () => {
    const onNotice = vi.fn();
    await act(async () => root.render(<HiddenApiKeysSettingsCard apiKeys={apiKeys} onNotice={onNotice} />));
    await settle();
    await act(async () => checkbox('2').click());
    await act(async () => saveButton().click());
    await settle();
    expect(save).toHaveBeenCalledTimes(1);
    expect([...save.mock.calls[0][0]].sort()).toEqual(['1', '2']);
    expect(onNotice).toHaveBeenCalledWith('success', 'usage_stats.hidden_api_keys_saved');
    expect(saveButton().disabled).toBe(true);
  });

  it('lists keys deleted from CPA in their own group so their history can be hidden', async () => {
    load.mockResolvedValueOnce({
      ids: ['1'],
      deletedKeys: [{ id: '7', sourceType: 'native', accessProvider: '', keyAlias: 'Old', displayKey: 'sk-***old', label: 'Old', lastSyncedAt: null }],
    });
    await act(async () => root.render(<HiddenApiKeysSettingsCard apiKeys={apiKeys} />));
    await settle();
    expect(container.querySelector('[data-hidden-api-keys-deleted-group]')).not.toBeNull();
    expect(container.querySelector('[data-hidden-api-key="7"]')?.textContent).toContain('Old');
    expect(checkbox('7').checked).toBe(false);
    await act(async () => checkbox('7').click());
    await act(async () => saveButton().click());
    await settle();
    expect([...save.mock.calls[0][0]].sort()).toEqual(['1', '7']);
  });

  it('keeps an unhide entry for hidden IDs missing from both lists', async () => {
    load.mockResolvedValueOnce({ ids: ['1', '9'], deletedKeys: [] });
    await act(async () => root.render(<HiddenApiKeysSettingsCard apiKeys={apiKeys} />));
    await settle();
    expect(checkbox('9').checked).toBe(true);
    expect(container.querySelector('[data-hidden-api-key="9"]')?.textContent).toContain('usage_stats.hidden_api_keys_deleted');
    await act(async () => checkbox('9').click());
    await act(async () => saveButton().click());
    await settle();
    expect(save).toHaveBeenCalledWith(['1']);
  });

  it('hides the deleted group when no key was deleted', async () => {
    await act(async () => root.render(<HiddenApiKeysSettingsCard apiKeys={apiKeys} />));
    await settle();
    expect(container.querySelector('[data-hidden-api-keys-deleted-group]')).toBeNull();
  });

  it('does not allow saving when the current setting failed to load', async () => {
    load.mockRejectedValueOnce(new Error('boom'));
    await act(async () => root.render(<HiddenApiKeysSettingsCard apiKeys={apiKeys} />));
    await settle();
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('usage_stats.hidden_api_keys_load_failed');
    expect(container.querySelector<HTMLFieldSetElement>('fieldset')?.disabled).toBe(true);
    expect(saveButton().disabled).toBe(true);
  });
});
