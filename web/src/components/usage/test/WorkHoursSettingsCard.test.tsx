// @vitest-environment happy-dom

import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@/lib/api';
import { fetchWorkHours, updateWorkHours } from '@/features/api-key-time-usage/api';
import { WorkHoursSettingsCard } from '../WorkHoursSettingsCard';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;
vi.mock('@/features/api-key-time-usage/api', () => ({ fetchWorkHours: vi.fn(), updateWorkHours: vi.fn() }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'zh' } }) }));

let root: Root;
let container: HTMLDivElement;
const load = vi.mocked(fetchWorkHours);
const save = vi.mocked(updateWorkHours);
const settle = async () => { await act(async () => { await Promise.resolve(); }); };
const setTime = async (selector: string, value: string) => {
  const input = container.querySelector<HTMLInputElement>(selector)!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
  });
};

beforeEach(() => {
  load.mockReset().mockResolvedValue({ timezone: 'Asia/Shanghai', start: '09:00', end: '18:00', source: 'default' });
  save.mockReset().mockResolvedValue({ timezone: 'Asia/Shanghai', start: '09:00', end: '18:30', source: 'saved' });
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

describe('WorkHoursSettingsCard', () => {
  it('loads the current window into time pickers', async () => {
    await act(async () => root.render(<WorkHoursSettingsCard />));
    await settle();
    expect(container.querySelector('[data-work-hours-card]')).not.toBeNull();
    expect(container.querySelector<HTMLInputElement>('[data-work-start]')?.type).toBe('time');
    expect(container.querySelector<HTMLInputElement>('[data-work-start]')?.value).toBe('09:00');
    expect(container.querySelector<HTMLInputElement>('[data-work-end]')?.value).toBe('18:00');
    expect(container.querySelector('input[type="text"]')).toBeNull();
    expect(container.querySelector('[data-work-preview]')?.getAttribute('data-work-preview')).toBe('09:00-18:00');
    expect(container.querySelector('[data-work-start]')?.getAttribute('aria-invalid')).toBe('false');
  });

  it('echoes a persisted 24:00 end without changing its boundary', async () => {
    load.mockResolvedValueOnce({ timezone: 'Asia/Shanghai', start: '09:00', end: '24:00', source: 'saved' });
    await act(async () => root.render(<WorkHoursSettingsCard />));
    await settle();
    expect(container.querySelector<HTMLInputElement>('[data-work-end]')?.type).toBe('text');
    expect(container.querySelector<HTMLInputElement>('[data-work-end]')?.value).toBe('24:00');
    expect(container.querySelector('[data-work-preview]')?.getAttribute('data-work-preview')).toBe('09:00-24:00');
    expect(container.querySelector('[data-work-save]')?.hasAttribute('disabled')).toBe(true);
  });

  it('saves an end-only picker change without confirmation', async () => {
    const onNotice = vi.fn();
    await act(async () => root.render(<WorkHoursSettingsCard onNotice={onNotice} />));
    await settle();
    await setTime('[data-work-end]', '18:30');
    await act(async () => container.querySelector<HTMLButtonElement>('[data-work-save]')!.click());
    await settle();
    expect(save).toHaveBeenCalledWith('09:00', '18:30');
    expect(document.body.querySelector('[data-work-confirm]')).toBeNull();
    expect(onNotice).toHaveBeenCalledWith('success', 'api_key_time_usage.work_saved');
  });

  it('confirms a start change and preserves the optional 24:00 boundary', async () => {
    await act(async () => root.render(<WorkHoursSettingsCard />));
    await settle();
    expect(container.querySelector('[data-work-end-24]')).not.toBeNull();
    expect(container.querySelector('[data-work-end-24]')?.getAttribute('aria-pressed')).toBe('false');
    await act(async () => container.querySelector<HTMLButtonElement>('[data-work-end-24]')!.click());
    expect(container.querySelector<HTMLInputElement>('[data-work-end]')?.value).toBe('24:00');
    expect(container.querySelector('[data-work-end-24]')?.getAttribute('aria-pressed')).toBe('true');
    await act(async () => container.querySelector<HTMLButtonElement>('[data-work-end-24]')!.click());
    expect(container.querySelector<HTMLInputElement>('[data-work-end]')?.value).toBe('18:00');
    await setTime('[data-work-start]', '09:30');
    await act(async () => container.querySelector<HTMLButtonElement>('[data-work-save]')!.click());
    await settle();
    expect(save).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain('api_key_time_usage.work_confirm_body');
    await act(async () => document.body.querySelector<HTMLButtonElement>('[data-work-cancel]')!.click());
    expect(save).not.toHaveBeenCalled();
    save.mockResolvedValue({ timezone: 'Asia/Shanghai', start: '09:30', end: '18:00', source: 'saved' });
    await act(async () => container.querySelector<HTMLButtonElement>('[data-work-save]')!.click());
    await settle();
    await act(async () => document.body.querySelector<HTMLButtonElement>('[data-work-confirm]')!.click());
    await settle();
    expect(save).toHaveBeenCalledWith('09:30', '18:00');
  });

  it('keeps boundaries on the 15-minute rollup grid', async () => {
    await act(async () => root.render(<WorkHoursSettingsCard />));
    await settle();
    expect(container.querySelector<HTMLInputElement>('[data-work-start]')?.step).toBe('900');
    expect(container.querySelector<HTMLInputElement>('[data-work-end]')?.step).toBe('900');
    await setTime('[data-work-end]', '18:10');
    expect(container.querySelector('[data-work-invalid]')?.textContent).toContain('work_invalid');
    expect(container.querySelector('[data-work-save]')?.hasAttribute('disabled')).toBe(true);
    await setTime('[data-work-end]', '18:15');
    expect(container.querySelector('[data-work-invalid]')).toBeNull();
    expect(container.querySelector('[data-work-save]')?.hasAttribute('disabled')).toBe(false);
  });

  it('returns to a valid regular end after leaving a persisted 24:00 boundary', async () => {
    load.mockResolvedValueOnce({ timezone: 'Asia/Shanghai', start: '09:00', end: '24:00', source: 'saved' });
    await act(async () => root.render(<WorkHoursSettingsCard />));
    await settle();
    await act(async () => container.querySelector<HTMLButtonElement>('[data-work-end-24]')!.click());
    expect(container.querySelector<HTMLInputElement>('[data-work-end]')?.value).toBe('23:45');
    expect(container.querySelector('[data-work-invalid]')).toBeNull();
  });

  it('shows a save failure without pretending the save was clean', async () => {
    save.mockRejectedValue(new ApiError('work_hours_save_failed', 500));
    await act(async () => root.render(<WorkHoursSettingsCard />));
    await settle();
    await setTime('[data-work-end]', '18:30');
    await act(async () => container.querySelector<HTMLButtonElement>('[data-work-save]')!.click());
    await settle();
    expect(container.querySelector('[data-work-hours-error]')?.textContent).toContain('work_save_failed');
  });
});
