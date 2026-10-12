import { useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ApiError } from '@/lib/api';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Modal } from '@/components/ui/Modal';
import { IconTimer } from '@/components/ui/icons';
import { fetchWorkHours, updateWorkHours } from '@/features/api-key-time-usage/api';
import { validWorkHours, WORK_HOURS_STEP_MINUTES } from '@/features/api-key-time-usage/model';
import styles from './WorkHoursSettingsCard.module.scss';

const DAY_MINUTES = 24 * 60;
const TICKS = ['00', '06', '12', '18', '24'];
// Native time pickers step in seconds; boundaries must match the 15-minute rollup buckets.
const TIME_STEP_SECONDS = WORK_HOURS_STEP_MINUTES * 60;
// The latest end a time picker can show on the 15-minute grid; 24:00 has its own toggle.
const LATEST_REGULAR_END = '23:45';

const clockMinutes = (value: string, allowEnd = false) => {
  if (!/^\d{2}:\d{2}$/.test(value)) return null;
  const hour = Number(value.slice(0, 2));
  const minute = Number(value.slice(3));
  if (allowEnd && hour === 24 && minute === 0) return DAY_MINUTES;
  if (hour > 23 || minute > 59) return null;
  return hour * 60 + minute;
};

const percent = (minutes: number) => `${(minutes / DAY_MINUTES * 100).toFixed(4)}%`;

interface Props {
  refreshToken?: number;
  onAuthRequired?: () => void;
  onNotice?: (kind: 'success' | 'error', message: string) => void;
}

const textKey = (key: string) => `api_key_time_usage.${key}`;

export function WorkHoursSettingsCard({ refreshToken = 0, onAuthRequired, onNotice }: Props) {
  const { t } = useTranslation();
  const text = (key: string) => t(textKey(key));
  const fieldId = useId();
  const [workStart, setWorkStart] = useState('09:00');
  const [workEnd, setWorkEnd] = useState('18:00');
  const [endOfDay, setEndOfDay] = useState(false);
  const [endBefore24, setEndBefore24] = useState('18:00');
  const [savedStart, setSavedStart] = useState('09:00');
  const [savedEnd, setSavedEnd] = useState('18:00');
  const [ready, setReady] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [confirmStart, setConfirmStart] = useState(false);
  const hoursValid = validWorkHours(workStart, workEnd);
  const changed = ready && (workStart !== savedStart || workEnd !== savedEnd);
  const startMinute = clockMinutes(workStart);
  const endMinute = clockMinutes(workEnd, true);
  const windowRange = hoursValid && startMinute != null && endMinute != null && endMinute > startMinute
    ? { startMinute, endMinute }
    : null;
  const spanMinutes = windowRange ? windowRange.endMinute - windowRange.startMinute : 0;
  const spanHours = Math.floor(spanMinutes / 60);
  const spanRemainder = spanMinutes % 60;
  const spanLabel = windowRange
    ? spanRemainder === 0
      ? t(textKey('work_span_hours'), { count: spanHours })
      : t(textKey('work_span'), { hours: spanHours, minutes: spanRemainder })
    : '';
  const feedbackId = error ? `${fieldId}-error` : !hoursValid ? `${fieldId}-invalid` : undefined;

  const applySchedule = (start: string, end: string) => {
    const isEndOfDay = end === '24:00';
    setWorkStart(start);
    setWorkEnd(end);
    setEndOfDay(isEndOfDay);
    setEndBefore24(isEndOfDay ? LATEST_REGULAR_END : end);
    setSavedStart(start);
    setSavedEnd(end);
  };

  useEffect(() => {
    const controller = new AbortController();
    setReady(false);
    void fetchWorkHours(controller.signal).then(schedule => {
      if (controller.signal.aborted) return;
      applySchedule(schedule.start, schedule.end);
      setError('');
      setReady(true);
    }).catch(loadError => {
      if (controller.signal.aborted) return;
      if (loadError instanceof ApiError && loadError.status === 401) onAuthRequired?.();
      else setError(t(textKey('work_save_failed')));
      setReady(true);
    });
    return () => controller.abort();
  // The translation function is stable in the app; omit it so a locale adapter
  // that recreates `t` does not restart the settings request on every render.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onAuthRequired, refreshToken]);

  const save = async () => {
    if (!hoursValid || saving || !changed) return;
    setSaving(true);
    setError('');
    try {
      const saved = await updateWorkHours(workStart, workEnd);
      applySchedule(saved.start, saved.end);
      setConfirmStart(false);
      onNotice?.('success', text('work_saved'));
    } catch (saveError) {
      const message = text('work_save_failed');
      setError(message);
      onNotice?.('error', message);
      if (saveError instanceof ApiError && saveError.status === 401) onAuthRequired?.();
    } finally {
      setSaving(false);
    }
  };
  const requestSave = () => {
    if (!hoursValid || saving || !changed) return;
    if (workStart !== savedStart) {
      setConfirmStart(true);
      return;
    }
    void save();
  };

  return <Card className={styles.card} title={text('work_hours_title')} subtitle={text('work_hours_subtitle')} data-work-hours-card>
    <form className={styles.form} aria-busy={!ready} data-work-hours onSubmit={event => { event.preventDefault(); requestSave(); }}>
      <div
        className={styles.schedule}
        data-work-preview={windowRange ? `${workStart}-${workEnd}` : undefined}
        data-dirty={changed ? 'true' : undefined}
        data-invalid={!hoursValid ? 'true' : undefined}
      >
        <div className={styles.scheduleHead}>
          <p className={styles.clock}>
            <IconTimer size={16} />
            <strong>{windowRange ? t(textKey('work_clock'), { start: workStart, end: workEnd }) : '—'}</strong>
          </p>
          {spanLabel && <span className={styles.duration}>{spanLabel}</span>}
        </div>
        <div className={styles.timeline} aria-hidden="true">
          <div className={styles.track}>
            {windowRange && <span className={styles.work} style={{ left: percent(windowRange.startMinute), width: percent(windowRange.endMinute - windowRange.startMinute) }} />}
          </div>
          <div className={styles.ticks}>
            {TICKS.map(tick => <span key={tick}>{tick}</span>)}
          </div>
        </div>
        <p className={styles.summary}>
          <span><i className={styles.workSwatch} aria-hidden="true" />{text('daytime_tokens')}</span>
          <span><i className={styles.offSwatch} aria-hidden="true" />{text('after_hours_tokens')}{windowRange && <b>{t(textKey('off_clock'), { start: workStart, end: workEnd })}</b>}</span>
        </p>
      </div>
      <fieldset className={styles.fields}>
        <legend className={styles.srOnly}>{text('work_hours_title')}</legend>
        <div className={styles.editor}>
          <label className={styles.field} htmlFor={`${fieldId}-start`}>
            <span>{text('work_start')}</span>
            <input id={`${fieldId}-start`} className={styles.time} type="time" step={TIME_STEP_SECONDS} data-work-start value={workStart} disabled={!ready || saving} aria-invalid={!hoursValid} aria-describedby={feedbackId} onChange={event => { setWorkStart(event.target.value); setError(''); }} />
          </label>
          <span className={styles.range} aria-hidden="true"><span /></span>
          <label className={styles.field} htmlFor={`${fieldId}-end`}>
            <span>{text('work_end')}</span>
            <div className={styles.endControl}>
              <input id={`${fieldId}-end`} className={styles.time} type={endOfDay ? 'text' : 'time'} step={TIME_STEP_SECONDS} data-work-end value={endOfDay ? '24:00' : workEnd} readOnly={endOfDay} disabled={!ready || saving} aria-invalid={!hoursValid} aria-describedby={feedbackId} onChange={event => { if (event.target.value) { setEndOfDay(false); setWorkEnd(event.target.value); setEndBefore24(event.target.value); setError(''); } }} />
              <button type="button" className={styles.endOfDayToggle} data-work-end-24 aria-pressed={endOfDay} disabled={!ready || saving} onClick={() => { if (endOfDay) { setEndOfDay(false); setWorkEnd(endBefore24 === '24:00' ? LATEST_REGULAR_END : endBefore24); } else { setEndBefore24(workEnd); setEndOfDay(true); setWorkEnd('24:00'); } setError(''); }}>
                {endOfDay ? text('work_end_regular') : text('work_end_of_day')}
              </button>
            </div>
          </label>
          <Button className={styles.save} type="submit" data-work-save disabled={!changed || !hoursValid || saving} loading={saving}>{text(saving ? 'work_saving' : 'work_save')}</Button>
        </div>
      </fieldset>
      {!hoursValid && <p className={styles.hint} id={`${fieldId}-invalid`} data-work-invalid>{text('work_invalid')}</p>}
      {error && <p className={styles.error} id={`${fieldId}-error`} role="alert" data-work-hours-error>{error}</p>}
    </form>
    <Modal
      open={confirmStart}
      title={text('work_confirm_title')}
      onClose={() => { if (!saving) setConfirmStart(false); }}
      closeDisabled={saving}
      width={440}
      footer={<>
        <Button variant="secondary" data-work-cancel disabled={saving} onClick={() => setConfirmStart(false)}>{text('work_cancel')}</Button>
        <Button data-work-confirm disabled={saving} loading={saving} onClick={() => void save()}>{text('work_confirm_save')}</Button>
      </>}
    >
      <p className={styles.confirmCopy}>{text('work_confirm_body')}</p>
    </Modal>
  </Card>;
}
