// @vitest-environment happy-dom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import {
  ApiError,
  createPushSource,
  fetchPushSettings,
  rotatePushSource,
  setPushSourceRevoked,
  updatePushEnabled,
  type PushSettingsResponse,
} from '@/lib/api';
import { buildPushUploadURL, PushIngestSettingsCard } from './PushIngestSettingsCard';
import { copyTextToClipboard } from '@/utils/clipboard';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en' } }) }));
vi.mock('@/utils/clipboard', () => ({ copyTextToClipboard: vi.fn().mockResolvedValue(undefined) }));
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchPushSettings: vi.fn(),
  updatePushEnabled: vi.fn(),
  createPushSource: vi.fn(),
  renamePushSource: vi.fn(),
  rotatePushSource: vi.fn(),
  setPushSourceRevoked: vi.fn(),
  deletePushSource: vi.fn(),
}));

const source = { id: 7, source_id: 'src_0123456789abcdef', name: 'Beijing CPA', token_hint: 'beef', revoked: false, created_at: '2026-10-27T01:00:00Z' };
const settings = (overrides: Partial<PushSettingsResponse> = {}): PushSettingsResponse => ({ enabled: false, upload_path: '/keeper/api/v1/ingest/usage', sources: [source], ...overrides });
const token = 'a'.repeat(60) + 'cafe';

let container: HTMLDivElement;
let root: Root;
beforeEach(() => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  vi.clearAllMocks();
  vi.mocked(copyTextToClipboard).mockReset().mockResolvedValue(undefined);
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  document.body.innerHTML = '';
  vi.useRealTimers();
});

const button = (label: string) => {
  const found = [...document.querySelectorAll<HTMLButtonElement>('button')].find(node => node.textContent?.trim() === label);
  if (!found) throw new Error(`missing button ${label}`);
  return found;
};

it('builds the upload URL from the browser origin and base path', () => {
  expect(buildPushUploadURL('/keeper/api/v1/ingest/usage', 'https://keeper.example.com')).toBe('https://keeper.example.com/keeper/api/v1/ingest/usage');
});

it('toggles receiving and lists sources without token material', async () => {
  vi.mocked(fetchPushSettings).mockResolvedValue(settings());
  vi.mocked(updatePushEnabled).mockResolvedValue(settings({ enabled: true }));
  const onNotice = vi.fn();
  await act(async () => root.render(<PushIngestSettingsCard onNotice={onNotice} />));
  expect(container.textContent).toContain('Beijing CPA');
  expect(container.textContent).toContain('push_ingest.disabled_hint');
  expect(container.textContent).toContain('/keeper/api/v1/ingest/usage');
  const toggle = container.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
  expect(toggle.checked).toBe(false);
  await act(async () => toggle.click());
  expect(updatePushEnabled).toHaveBeenCalledWith(true);
  expect(container.querySelector<HTMLInputElement>('input[type="checkbox"]')!.checked).toBe(true);
  expect(onNotice).toHaveBeenCalledWith('success', 'push_ingest.saved_enabled');
});

it('hides a new token after closing and allows copying it again from the source row', async () => {
  vi.mocked(fetchPushSettings).mockResolvedValue(settings({ sources: [] }));
  vi.mocked(createPushSource).mockResolvedValue({ ...source, id: 8, name: 'Shanghai CPA', token, token_hint: 'cafe' });
  await act(async () => root.render(<PushIngestSettingsCard />));
  const name = container.querySelector<HTMLInputElement>('input:not([type="checkbox"])')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(name, '  Shanghai CPA  ');
    name.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await act(async () => button('push_ingest.create').click());
  expect(createPushSource).toHaveBeenCalledWith('Shanghai CPA');
  expect(document.querySelector('[data-testid="issued-token"]')?.textContent).toBe(token);
  expect(container.textContent).toContain('Shanghai CPA');
  await act(async () => button('push_ingest.done').click());
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 400)); });
  expect(document.body.textContent).not.toContain(token);
  await act(async () => button('push_ingest.copy_token').click());
  expect(copyTextToClipboard).toHaveBeenCalledWith(token);
  expect(container.querySelector('li')?.textContent).toContain('push_ingest.copied');
  expect(container.querySelector('[role="status"]')?.textContent).toBe('push_ingest.token_copied');
});

it('copies the full upload URL and resets successful copy feedback', async () => {
  vi.mocked(fetchPushSettings).mockResolvedValue(settings());
  await act(async () => root.render(<PushIngestSettingsCard />));
  vi.useFakeTimers();
  await act(async () => button('push_ingest.copy_url').click());
  expect(copyTextToClipboard).toHaveBeenCalledWith(buildPushUploadURL(settings().upload_path));
  expect(container.querySelector('[role="status"]')?.textContent).toBe('push_ingest.url_copied');
  await act(async () => { vi.advanceTimersByTime(2000); });
  expect(button('push_ingest.copy_url')).toBeDefined();
  expect(container.querySelector('[role="status"]')?.textContent).toBe('');
});

it('disables copying historical tokens rather than copying their suffix or rotating them', async () => {
  vi.mocked(fetchPushSettings).mockResolvedValue(settings());
  await act(async () => root.render(<PushIngestSettingsCard />));
  const copyButton = button('push_ingest.copy_token');
  expect(copyButton.disabled).toBe(true);
  expect(copyButton.title).toBe('push_ingest.token_unavailable');
  expect(container.textContent).toContain('push_ingest.token_unavailable');
  await act(async () => copyButton.click());
  expect(copyTextToClipboard).not.toHaveBeenCalled();
  expect(rotatePushSource).not.toHaveBeenCalled();
});

it('reports a copy failure and allows retrying the issued token', async () => {
  vi.mocked(fetchPushSettings).mockResolvedValue(settings());
  vi.mocked(rotatePushSource).mockResolvedValue({ ...source, token, token_hint: 'cafe' });
  vi.mocked(copyTextToClipboard).mockRejectedValueOnce(new Error('clipboard denied'));
  const onNotice = vi.fn();
  await act(async () => root.render(<PushIngestSettingsCard onNotice={onNotice} />));
  await act(async () => button('push_ingest.rotate').click());
  await act(async () => button('push_ingest.confirm').click());
  const dialogCopy = () => [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')]
    .find(node => node.textContent?.trim() === 'push_ingest.copy_token')!;
  await act(async () => dialogCopy().click());
  expect(onNotice).toHaveBeenCalledWith('error', 'push_ingest.copy_failed');
  expect(dialogCopy().disabled).toBe(false);
  await act(async () => dialogCopy().click());
  expect(copyTextToClipboard).toHaveBeenLastCalledWith(token);
  expect(document.querySelector('[data-testid="issued-token"]')?.closest('[role="dialog"]')?.textContent).toContain('push_ingest.copied');
});

it('replaces cached tokens on rotation and clears them when a source is revoked', async () => {
  const replacement = 'b'.repeat(60) + 'feed';
  vi.mocked(fetchPushSettings).mockResolvedValue(settings());
  vi.mocked(rotatePushSource)
    .mockResolvedValueOnce({ ...source, token, token_hint: 'cafe' })
    .mockResolvedValueOnce({ ...source, token: replacement, token_hint: 'feed' });
  vi.mocked(setPushSourceRevoked)
    .mockResolvedValueOnce({ ...source, token_hint: 'feed', revoked: true })
    .mockResolvedValueOnce({ ...source, token_hint: 'feed', revoked: false });
  await act(async () => root.render(<PushIngestSettingsCard />));
  for (let index = 0; index < 2; index++) {
    await act(async () => button('push_ingest.rotate').click());
    await act(async () => button('push_ingest.confirm').click());
    await act(async () => button('push_ingest.done').click());
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 400)); });
  }
  await act(async () => button('push_ingest.copy_token').click());
  expect(copyTextToClipboard).toHaveBeenCalledWith(replacement);
  expect(copyTextToClipboard).not.toHaveBeenCalledWith(token);
  await act(async () => button('push_ingest.revoke').click());
  await act(async () => button('push_ingest.confirm').click());
  expect(button('push_ingest.copy_token').disabled).toBe(true);
  await act(async () => button('push_ingest.restore').click());
  expect(container.querySelector('li button')?.getAttribute('disabled')).not.toBeNull();
});

it('shows the cached full token for manual copying when inline copying fails', async () => {
  vi.mocked(fetchPushSettings).mockResolvedValue(settings());
  vi.mocked(rotatePushSource).mockResolvedValue({ ...source, token, token_hint: 'cafe' });
  const onNotice = vi.fn();
  await act(async () => root.render(<PushIngestSettingsCard onNotice={onNotice} />));
  await act(async () => button('push_ingest.rotate').click());
  await act(async () => button('push_ingest.confirm').click());
  await act(async () => button('push_ingest.done').click());
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 400)); });
  vi.mocked(copyTextToClipboard).mockRejectedValueOnce(new Error('clipboard denied'));
  await act(async () => button('push_ingest.copy_token').click());
  expect(copyTextToClipboard).toHaveBeenCalledWith(token);
  expect(onNotice).toHaveBeenCalledWith('error', 'push_ingest.copy_failed');
  expect(document.querySelector('[data-testid="issued-token"]')?.textContent).toBe(token);
  expect(rotatePushSource).toHaveBeenCalledTimes(1);
});

it('clears issued tokens after reloading settings without persisting them in browser storage', async () => {
  vi.mocked(fetchPushSettings).mockResolvedValue(settings());
  vi.mocked(rotatePushSource).mockResolvedValue({ ...source, token, token_hint: 'cafe' });
  const localWrite = vi.spyOn(localStorage, 'setItem');
  const sessionWrite = vi.spyOn(sessionStorage, 'setItem');
  await act(async () => root.render(<PushIngestSettingsCard />));
  await act(async () => button('push_ingest.rotate').click());
  await act(async () => button('push_ingest.confirm').click());
  await act(async () => button('push_ingest.done').click());
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 400)); });
  expect(button('push_ingest.copy_token').disabled).toBe(false);
  await act(async () => root.render(<PushIngestSettingsCard refreshToken={1} />));
  expect(button('push_ingest.copy_token').disabled).toBe(true);
  expect(localWrite).not.toHaveBeenCalled();
  expect(sessionWrite).not.toHaveBeenCalled();
  localWrite.mockRestore();
  sessionWrite.mockRestore();
});

it('rejects an empty name without calling the server', async () => {
  vi.mocked(fetchPushSettings).mockResolvedValue(settings({ sources: [] }));
  await act(async () => root.render(<PushIngestSettingsCard />));
  await act(async () => button('push_ingest.create').click());
  expect(createPushSource).not.toHaveBeenCalled();
  expect(container.querySelector('[role="alert"]')?.textContent).toBe('push_ingest.invalid_name');
});

it('asks for confirmation before replacing or revoking a token', async () => {
  vi.mocked(fetchPushSettings).mockResolvedValue(settings({ enabled: true }));
  vi.mocked(rotatePushSource).mockResolvedValue({ ...source, token, token_hint: 'cafe' });
  vi.mocked(setPushSourceRevoked).mockResolvedValue({ ...source, token_hint: 'cafe', revoked: true });
  await act(async () => root.render(<PushIngestSettingsCard />));
  await act(async () => button('push_ingest.rotate').click());
  expect(rotatePushSource).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain('push_ingest.confirm_rotate');
  await act(async () => button('push_ingest.confirm').click());
  expect(rotatePushSource).toHaveBeenCalledWith(7);
  expect(document.querySelector('[data-testid="issued-token"]')?.textContent).toBe(token);
  await act(async () => button('push_ingest.done').click());
  await act(async () => button('push_ingest.revoke').click());
  expect(setPushSourceRevoked).not.toHaveBeenCalled();
  await act(async () => button('push_ingest.confirm').click());
  expect(setPushSourceRevoked).toHaveBeenCalledWith(7, true);
  expect(container.textContent).toContain('push_ingest.revoked');
  expect(container.textContent).toContain('push_ingest.restore');
});

it('keeps issued tokens out of the source list and reports setting changes', async () => {
  vi.mocked(fetchPushSettings).mockResolvedValue(settings({ sources: [] }));
  vi.mocked(createPushSource).mockResolvedValue({ ...source, id: 9, name: 'Remote CPA', token, token_hint: 'cafe' });
  vi.mocked(updatePushEnabled).mockResolvedValue(settings({ enabled: true, sources: [] }));
  const onSettingsChanged = vi.fn();
  await act(async () => root.render(<PushIngestSettingsCard onSettingsChanged={onSettingsChanged} />));
  await act(async () => container.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());
  expect(onSettingsChanged).toHaveBeenCalledTimes(1);
  const name = container.querySelector<HTMLInputElement>('input:not([type="checkbox"])')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(name, 'Remote CPA');
    name.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await act(async () => button('push_ingest.create').click());
  expect(onSettingsChanged).toHaveBeenCalledTimes(2);
  await act(async () => button('push_ingest.done').click());
  // 列表行渲染的是来源信息；关闭弹窗后 token 只应来自已关闭的弹窗，不应残留在列表状态里。
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 400)); });
  expect(document.body.innerHTML).not.toContain(token);
  expect(container.textContent).toContain('Remote CPA');
});

it('requests authentication when the session expires', async () => {
  const onAuthRequired = vi.fn();
  vi.mocked(fetchPushSettings).mockRejectedValue(new ApiError('expired', 401));
  await act(async () => root.render(<PushIngestSettingsCard onAuthRequired={onAuthRequired} />));
  expect(onAuthRequired).toHaveBeenCalledOnce();
});

it('reports a load failure inline', async () => {
  vi.mocked(fetchPushSettings).mockRejectedValue(new ApiError('boom', 500));
  await act(async () => root.render(<PushIngestSettingsCard />));
  expect(container.querySelector('[role="alert"]')?.textContent).toBe('push_ingest.settings_load_error');
});
