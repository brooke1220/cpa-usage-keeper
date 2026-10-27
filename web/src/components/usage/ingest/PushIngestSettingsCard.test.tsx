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
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  document.body.innerHTML = '';
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

it('shows a new token once and forgets it after closing', async () => {
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
