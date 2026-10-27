// @vitest-environment happy-dom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ApiError, fetchIngestQuarantine, retryIngestQuarantine } from '@/lib/api';
import { IngestStatusPanel } from './IngestStatusPanel';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/lib/api', async (original) => ({ ...await original<typeof import('@/lib/api')>(), fetchIngestQuarantine: vi.fn(), retryIngestQuarantine: vi.fn() }));
let container: HTMLDivElement;
let root: Root;
beforeEach(() => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  vi.clearAllMocks();
  container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container);
});
afterEach(async () => { await act(async () => root.unmount()); container.remove(); });

it('shows per-source uploads and explains when receiving is off', async () => {
  await act(async () => root.render(<IngestStatusPanel status={{ enabled: false, pending: 2, quarantined: 0, sources: [
    { name: 'Beijing CPA', revoked: false, last_used_at: '2026-10-27T01:00:00Z' },
    { name: 'Idle CPA', revoked: false },
    { name: 'Retired CPA', revoked: true },
  ] }} />));
  expect(container.textContent).toContain('push_ingest.receiving_off');
  expect(container.textContent).toContain('Beijing CPA');
  expect(container.textContent).toContain('push_ingest.source_never');
  expect(container.textContent).not.toContain('Retired CPA');
});

it('lets administrators review and retry retained records', async () => {
  vi.mocked(fetchIngestQuarantine).mockResolvedValue({ items: [{ id: 12, source_id: 'cpa-one', delivery_event_id: 'delivery-one', attempts: 5 }] });
  vi.mocked(retryIngestQuarantine).mockResolvedValue(undefined);
  await act(async () => root.render(<IngestStatusPanel status={{ pending: 0, quarantined: 1 }} />));
  await act(async () => container.querySelector('button')!.click());
  expect(container.textContent).toContain('cpa-one');
  expect(container.textContent).toContain('delivery-one');
  const retry = [...container.querySelectorAll('button')].find(button => button.textContent === 'push_ingest.retry')!;
  await act(async () => retry.click());
  expect(retryIngestQuarantine).toHaveBeenCalledWith(12);
  expect(container.textContent).not.toContain('delivery-one');
  expect(container.textContent).toContain('push_ingest.empty');
});

it('requests authentication when the review session expires', async () => {
  const onAuthRequired = vi.fn();
  vi.mocked(fetchIngestQuarantine).mockRejectedValue(new ApiError('expired', 401));
  await act(async () => root.render(<IngestStatusPanel status={{ pending: 0, quarantined: 1 }} onAuthRequired={onAuthRequired} />));
  await act(async () => container.querySelector('button')!.click());
  expect(onAuthRequired).toHaveBeenCalledOnce();
  expect(container.querySelector('[role="alert"]')?.textContent).toBe('push_ingest.load_error');
});
