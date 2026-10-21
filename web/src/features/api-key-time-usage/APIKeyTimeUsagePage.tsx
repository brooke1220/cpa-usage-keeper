import { memo, useCallback, useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { ApiError } from '@/lib/api';
import { Button } from '@/components/ui/Button';
import { IconTimer } from '@/components/ui/icons';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { formatCompactTokenValue } from '@/utils/usage';
import { formatDurationMs } from '@/utils/usage/latency';
import {
  addDateDays,
  alignTimeUsageDays,
  isWeekendDate,
  presetDates,
  readStoredTimeUsageRange,
  resolveTimeZone,
  sortTimeUsageKeys,
  statisticalDate,
  statisticalWeekday,
  timeUsageAvatarID,
  validTimeUsageDates,
  writeStoredTimeUsageRange,
} from './model';
import { fetchWorkHours } from './api';
import { useAPIKeyTimeUsage } from './useAPIKeyTimeUsage';
import type { TimeUsageDay, TimeUsageKey, TimeUsageSort, TimeUsageTotals } from './types';
import avatarCatalogURL from './avatar-catalog.webp';
import styles from './APIKeyTimeUsagePage.module.scss';

interface Props { apiKeyId?: string; refreshToken?: number; onAuthRequired?: () => void }

type RangePreset = 'today' | '7d' | '30d';
type SummaryColumn = 'total_tokens' | 'daytime_tokens' | 'after_hours_tokens';
type Period = 'work' | 'off';

const RANGE_PRESETS: RangePreset[] = ['today', '7d', '30d'];
const SUMMARY_COLUMNS: SummaryColumn[] = ['total_tokens', 'daytime_tokens', 'after_hours_tokens'];
const presetLabel = (preset: RangePreset) => preset === '7d' ? 'last_7_days' : preset === '30d' ? 'last_30_days' : 'today';
const totalAttribute = (column: SummaryColumn) => column === 'total_tokens' ? 'total' : column === 'daytime_tokens' ? 'daytime' : 'after_hours';
const columnPeriod = (column: SummaryColumn): Period | undefined => column === 'daytime_tokens' ? 'work' : column === 'after_hours_tokens' ? 'off' : undefined;

const keyName = (key: TimeUsageKey) => key.display_name || key.key_alias || key.display_key;

function TimeUsageAvatar({ avatarID }: { avatarID: number }) {
  const index = avatarID - 1;
  return <span
    className={styles.avatar}
    style={{
      backgroundImage: `url(${avatarCatalogURL})`,
      backgroundPosition: `${(index % 10) * 100 / 9}% ${Math.floor(index / 10) * 100 / 9}%`,
      backgroundSize: '1000% 1000%',
    }}
    aria-hidden="true"
    data-time-usage-avatar-id={avatarID}
  />;
}

const compactUnits = (locale: string) => {
  const lang = locale.toLowerCase();
  if (lang.startsWith('zh-tw') || lang.startsWith('zh-hk') || lang.startsWith('zh-hant')) return { d: '天', h: '時', m: '分', s: '秒', ms: 'ms' };
  if (lang.startsWith('zh')) return { d: '天', h: '时', m: '分', s: '秒', ms: 'ms' };
  return { d: 'd', h: 'h', m: 'm', s: 's', ms: 'ms' };
};

const fullUnits = (locale: string) => {
  const lang = locale.toLowerCase();
  if (lang.startsWith('zh-tw') || lang.startsWith('zh-hk') || lang.startsWith('zh-hant')) return { d: '天', h: '小時', m: '分鐘', s: '秒', ms: 'ms' };
  if (lang.startsWith('zh')) return { d: '天', h: '小时', m: '分钟', s: '秒', ms: 'ms' };
  return { d: 'd', h: 'h', m: 'm', s: 's', ms: 'ms' };
};

function compactDuration(value: number, locale: string) {
  const units = compactUnits(locale);
  if (!Number.isFinite(value) || value <= 0) return `0${units.ms}`;
  if (value < 1000) return `${Math.round(value)}${units.ms}`;
  const totalSeconds = Math.floor(value / 1000);
  if (totalSeconds < 60) return `${totalSeconds}${units.s}`;
  const totalMinutes = Math.floor(totalSeconds / 60);
  if (totalMinutes < 60) return `${totalMinutes}${units.m}`;
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (hours < 24) return minutes ? `${hours}${units.h}${minutes}${units.m}` : `${hours}${units.h}`;
  const days = Math.floor(hours / 24);
  const remHours = hours % 24;
  return remHours ? `${days}${units.d}${remHours}${units.h}` : `${days}${units.d}`;
}

const countOf = (row: TimeUsageTotals) => row.message_count ?? 0;
const latencyOf = (row: TimeUsageTotals, period: 'work' | 'off') => period === 'work' ? (row.daytime_latency_ms ?? 0) : (row.after_hours_latency_ms ?? 0);
const samplesOf = (row: TimeUsageTotals, period: 'work' | 'off') => period === 'work' ? (row.daytime_latency_samples ?? 0) : (row.after_hours_latency_samples ?? 0);

function IconCalendar() {
  return <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
    <rect x="2.2" y="3.2" width="11.6" height="10.6" rx="1.8" stroke="currentColor" strokeWidth="1.4" />
    <path d="M2.2 6.5h11.6M5.4 1.8v2.6M10.6 1.8v2.6" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
  </svg>;
}

function Amount({ value, period, format, exact, label, figure }: { value: number; period?: 'work' | 'off'; format: (value: number) => string; exact: string; label?: string; figure?: string }) {
  const tone = !value ? styles.zero : !period ? styles.totalNum : period === 'work' ? styles.workNum : styles.offNum;
  return <span data-figure={figure} data-period={period} className={tone} title={exact} aria-label={label && exact ? `${label} ${exact}` : undefined}>{format(value)}</span>;
}

function IconMessage({ size = 12 }: { size?: number }) {
  return <svg width={size} height={size} viewBox="0 0 16 16" fill="none" aria-hidden="true">
    <path d="M2.8 3.4h10.4a1 1 0 0 1 1 1v6.1a1 1 0 0 1-1 1H7.2L4.2 14V11.5H2.8a1 1 0 0 1-1-1V4.4a1 1 0 0 1 1-1Z" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
  </svg>;
}

function SubLine({ metric, period, label, value, title, plain = false }: { metric: 'messages' | 'duration'; period?: 'work' | 'off'; label: string; value: string; title?: string; plain?: boolean }) {
  const hint = title && title !== value ? `${label} · ${title}` : `${label} ${value}`;
  return <span className={styles.subLine} data-metric={metric} data-period={period} title={hint} aria-label={hint}>
    {!plain && <em aria-hidden="true">{metric === 'messages' ? <IconMessage /> : <IconTimer size={12} />}</em>}
    <strong>{value}</strong>
  </span>;
}

function MetaRow({ children }: { children: ReactNode }) {
  return <span className={styles.metaRow}>{children}</span>;
}

function SplitBar({ work, off, compact = false }: { work: number; off: number; compact?: boolean }) {
  const total = work + off;
  const share = (value: number) => `${value / total * 100}%`;
  return <span className={compact ? styles.splitCompact : styles.split} data-empty={total <= 0 ? 'true' : undefined} aria-hidden="true">
    {total > 0 && <>
      <i className={styles.splitWork} style={{ width: share(work) }} />
      <i className={styles.splitOff} style={{ width: share(off) }} />
    </>}
  </span>;
}

function DateField({ label, value, min, max, point, onChange }: { label: string; value: string; min: string; max: string; point: 'start' | 'end'; onChange: (value: string) => void }) {
  return <label className={styles.dateField}>
    <input
      type="date"
      className={styles.dateInput}
      aria-label={label}
      min={min}
      max={max}
      value={value}
      data-range-start={point === 'start' ? '' : undefined}
      data-range-end={point === 'end' ? '' : undefined}
      onChange={event => onChange(event.target.value)}
    />
    <span className={styles.calendarIcon} aria-hidden="true"><IconCalendar /></span>
  </label>;
}

export function APIKeyTimeUsagePage({ apiKeyId = '', refreshToken = 0, onAuthRequired }: Props) {
  const { t, i18n } = useTranslation();
  const text = useCallback((key: string, params?: Record<string, string | number>) => t(`api_key_time_usage.${key}`, params), [t]);
  const headingId = useId();
  const [refresh, setRefresh] = useState(0);
  const [sort, setSort] = useState<TimeUsageSort>('total_tokens');
  const [descending, setDescending] = useState(true);
  // The usage API uses statistical-day labels in the project time zone. Before
  // the work start, the current statistical day is yesterday even though the
  // calendar date has already changed. Use the default start and the browser
  // zone until the saved schedule arrives, then recalculate the automatic range.
  // Keep the synchronous storage read inside a lazy initializer: this page
  // re-renders while requests settle and localStorage must not be parsed on
  // every one of those renders.
  const [initialRangeState] = useState(() => {
    const initialToday = statisticalDate(new Date(), '09:00');
    const storedRange = readStoredTimeUsageRange(initialToday);
    return {
      range: storedRange ?? presetDates('7d', initialToday),
      wasStored: storedRange !== null,
    };
  });
  const [range, setRange] = useState(initialRangeState.range);
  const rangeWasStored = initialRangeState.wasStored;
  const automaticRangeApplied = useRef(false);
  const [workStart, setWorkStart] = useState('09:00');
  const [timeZone, setTimeZone] = useState(() => resolveTimeZone());
  const [workHoursReady, setWorkHoursReady] = useState(false);
  // A parent refresh invalidates the old schedule immediately during render.
  // This token gates the usage request until the refreshed work hours have
  // settled, preventing a request with the previous boundary.
  const [workHoursLoadedForRefresh, setWorkHoursLoadedForRefresh] = useState<number | null>(null);
  const today = statisticalDate(new Date(), workStart, timeZone);
  useEffect(() => {
    const controller = new AbortController();
    void fetchWorkHours(controller.signal).then(schedule => {
      if (controller.signal.aborted) return;
      const scheduleZone = resolveTimeZone(schedule.timezone);
      setWorkStart(schedule.start);
      setTimeZone(scheduleZone);
      if (!rangeWasStored && !automaticRangeApplied.current) {
        automaticRangeApplied.current = true;
        setRange(presetDates('7d', statisticalDate(new Date(), schedule.start, scheduleZone)));
      }
      setWorkHoursLoadedForRefresh(refreshToken);
      setWorkHoursReady(true);
    }).catch(() => {
      if (!controller.signal.aborted) {
        // Preserve the previous/default schedule when the refresh endpoint is
        // unavailable, but still release the request gate for this refresh.
        setWorkHoursLoadedForRefresh(refreshToken);
        setWorkHoursReady(true);
      }
    });
    return () => controller.abort();
  }, [rangeWasStored, refreshToken]);
  const scheduleReady = workHoursReady && workHoursLoadedForRefresh === refreshToken;
  const rangeValid = scheduleReady && validTimeUsageDates(range.start, range.end, today);
  const { data, loading, error } = useAPIKeyTimeUsage({ ...range, apiKeyId }, rangeValid, `${refreshToken}:${refresh}`, onAuthRequired);
  // The server clamps an end date that is already the next calendar date but still the current statistical day.
  // Show that clamped date without issuing another request.
  const visibleEnd = data && data.start_date === range.start && data.end_date < range.end ? data.end_date : range.end;
  useEffect(() => {
    if (validTimeUsageDates(range.start, visibleEnd, today)) writeStoredTimeUsageRange({ start: range.start, end: visibleEnd });
  }, [range.start, today, visibleEnd]);
  const locale = i18n.resolvedLanguage || i18n.language || 'en';
  const exactNumbers = useMemo(() => new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 }), []);
  const tokens = useCallback((value: number | null | undefined) => value == null ? '—' : formatCompactTokenValue(value), []);
  const messages = useCallback((value: number | null | undefined) => value == null ? '—' : exactNumbers.format(value), [exactNumbers]);
  const exact = useCallback((value: number | null | undefined) => value == null ? '' : exactNumbers.format(value), [exactNumbers]);
  const durationUnitLabels = useMemo(() => fullUnits(locale), [locale]);
  const duration = useCallback((value: number) => formatDurationMs(value, { locale, unitLabels: durationUnitLabels }), [durationUnitLabels, locale]);
  const cellDuration = useCallback((value: number) => compactDuration(value, locale), [locale]);
  const durationTitle = useCallback((sum: number, samples: number) => {
    const exactMs = `${exactNumbers.format(sum)} ms`;
    if (samples <= 0) return exactMs;
    return `${exactMs} · ${text('duration_average', { value: duration(sum / samples) })} · ${text('duration_samples', { count: samples })}`;
  }, [duration, exactNumbers, text]);
  const ranked = useMemo(
    () => (data ? sortTimeUsageKeys(data.key_summaries ?? [], sort, descending) : []),
    [data, descending, sort],
  );
  const calendar = useMemo(() => data?.daily ?? [], [data]);
  const calendarMeta = useMemo(
    () => calendar.map(day => ({
      day,
      weekend: isWeekendDate(day.date),
      weekday: statisticalWeekday(day.date, locale),
    })),
    [calendar, locale],
  );
  const alignedDaysByKey = useMemo(() => {
    const aligned = new Map<string, TimeUsageDay[]>();
    for (const row of data?.key_summaries ?? []) {
      aligned.set(row.key.id, alignTimeUsageDays(calendar, row.daily));
    }
    return aligned;
  }, [calendar, data]);
  const rankedRows = useMemo(
    () => ranked.map(row => ({ row, days: alignedDaysByKey.get(row.key.id) ?? [] })),
    [alignedDaysByKey, ranked],
  );
  const chooseSort = (column: TimeUsageSort) => {
    if (sort === column) setDescending(value => !value);
    else {
      setSort(column);
      setDescending(true);
    }
  };
  const commitRange = (next: { start: string; end: string }) => {
    setRange(next);
    if (validTimeUsageDates(next.start, next.end, today)) writeStoredTimeUsageRange(next);
  };
  const applyPreset = (preset: RangePreset) => commitRange(presetDates(preset, today));
  const activePreset = RANGE_PRESETS.find(preset => {
    const dates = presetDates(preset, today);
    return dates.start === range.start && dates.end === range.end;
  });
  const errorKey = error instanceof ApiError
    ? error.status === 400 ? 'invalid_range' : error.status === 403 ? 'forbidden' : error.status === 404 ? 'key_missing' : 'error'
    : 'error';
  const rule = data ? text('day_note', { timezone: data.timezone, start: data.daytime_start, end: data.daytime_end }) : '';
  // Usage from keys that were never synced is not part of any row, so the total row
  // stays the sum of the table; say how much was left out instead of hiding it.
  const unattributed = data?.unattributed;
  const unattributedNote = unattributed && ((unattributed.total_tokens ?? 0) > 0 || (unattributed.message_count ?? 0) > 0)
    ? text('unattributed_note', { tokens: tokens(unattributed.total_tokens ?? 0), messages: messages(unattributed.message_count ?? 0) })
    : '';
  const workLabel = text('daytime_tokens');
  const offLabel = text('after_hours_tokens');
  const boundMin = data?.min_date ?? addDateDays(today, -89);
  const boundMax = data?.max_date ?? today;
  // Keep the current value inside the native min/max so the browser does not clear a clamped date.
  const minDate = [boundMin, range.start, visibleEnd].sort()[0];
  const maxDate = [boundMax, range.start, visibleEnd].sort().at(-1) ?? boundMax;
  const renderSummary = (row: TimeUsageTotals, column: SummaryColumn) => {
    const period = columnPeriod(column);
    const value = column === 'daytime_tokens' ? row.daytime_tokens : column === 'after_hours_tokens' ? row.after_hours_tokens : row.total_tokens;
    const messageCount = period === 'work' ? (row.daytime_message_count ?? 0) : period === 'off' ? (row.after_hours_message_count ?? 0) : countOf(row);
    const durationMs = period === 'work' ? latencyOf(row, 'work') : period === 'off' ? latencyOf(row, 'off') : latencyOf(row, 'work') + latencyOf(row, 'off');
    const samples = period === 'work' ? samplesOf(row, 'work') : period === 'off' ? samplesOf(row, 'off') : samplesOf(row, 'work') + samplesOf(row, 'off');
    const quiet = value <= 0 && messageCount <= 0 && durationMs <= 0;
    return <div className={styles.summaryStack} data-period={period} data-empty={quiet ? 'true' : undefined}>
      <Amount value={value} period={period} format={tokens} exact={exact(value)} label={period ? text(column) : undefined} figure="tokens" />
      {column === 'total_tokens' && <SplitBar work={row.daytime_tokens} off={row.after_hours_tokens} />}
      <MetaRow>
        <SubLine plain metric="messages" period={period} label={text('message_count')} value={messages(messageCount)} title={exact(messageCount)} />
        <span className={styles.metaDot} aria-hidden="true">·</span>
        <SubLine plain metric="duration" period={period} label={text('request_duration')} value={cellDuration(durationMs)} title={durationTitle(durationMs, samples)} />
      </MetaRow>
    </div>;
  };

  return <section className={styles.page} aria-labelledby={headingId} data-api-key-time-usage>
    <div className={styles.ledger}>
      <header className={styles.heading}>
        <div className={styles.titleRow}>
          <div className={styles.titleBlock}>
            <h2 id={headingId}>{text('title')}{data && <span className={styles.count}>{ranked.length}</span>}</h2>
            <p>{text('subtitle')}</p>
          </div>
          <div className={styles.filters}>
            <div className={styles.presets} role="group" aria-label={text('range_label')}>
              {RANGE_PRESETS.map(preset => <button key={preset} type="button" className={styles.preset} data-range-preset={preset} aria-pressed={activePreset === preset} onClick={() => applyPreset(preset)}>{text(presetLabel(preset))}</button>)}
            </div>
            <div className={styles.dateRange}>
              <DateField label={text('start_date')} value={range.start} min={minDate} max={maxDate} point="start" onChange={start => commitRange({ start, end: visibleEnd })} />
              <span className={styles.dateTo}>{text('range_to')}</span>
              <DateField label={text('end_date')} value={visibleEnd} min={minDate} max={maxDate} point="end" onChange={end => commitRange({ start: range.start, end })} />
            </div>
          </div>
        </div>
        {data && <div className={styles.meta}>
          <div className={styles.rangeSummary} data-range-summary>
            <span data-range-message title={`${text('message_count')} ${exact(countOf(data.summary))}`}><IconMessage /><strong>{messages(countOf(data.summary))}</strong></span>
            <span data-range-duration="work" title={`${text('work_duration')} · ${durationTitle(latencyOf(data.summary, 'work'), samplesOf(data.summary, 'work'))}`}><i className={styles.workSwatch} aria-hidden="true" /><IconTimer size={12} /><strong>{duration(latencyOf(data.summary, 'work'))}</strong></span>
            <span data-range-duration="off" title={`${text('off_duration')} · ${durationTitle(latencyOf(data.summary, 'off'), samplesOf(data.summary, 'off'))}`}><i className={styles.offSwatch} aria-hidden="true" /><IconTimer size={12} /><strong>{duration(latencyOf(data.summary, 'off'))}</strong></span>
          </div>
          <div className={styles.metaTop}>
            <span className={styles.legend} data-time-usage-legend>
              <span><i className={styles.workSwatch} aria-hidden="true" />{workLabel}</span>
              <span><i className={styles.offSwatch} aria-hidden="true" />{offLabel}</span>
              <span><IconMessage />{text('message_count')}</span>
              <span><IconTimer size={12} />{text('request_duration')}</span>
            </span>
            <span className={styles.hint}>{text('cell_order')}</span>
          </div>
          <p className={styles.rule} data-day-rule>{rule}</p>
          {unattributedNote && <p className={styles.rule} data-unattributed-note>{unattributedNote}</p>}
        </div>}
      </header>

      {!scheduleReady && <div className={styles.loading} role="status"><LoadingSpinner /> {t('common.loading')}</div>}
      {scheduleReady && !rangeValid && <div className={styles.error} role="alert">{text('invalid_range')}</div>}
      {rangeValid && error && <div className={styles.error} role="alert">{text(errorKey)} <Button variant="secondary" size="sm" onClick={() => setRefresh(value => value + 1)}>{t('common.retry')}</Button></div>}
      {rangeValid && loading && !data && !error && <div className={styles.loading} role="status"><LoadingSpinner /> {t('common.loading')}</div>}
      {rangeValid && data && ranked.length === 0 && <p className={styles.note}>{text('no_keys')}</p>}
      {rangeValid && data && ranked.length > 0 && <div className={styles.tableScroll} aria-busy={loading} data-refreshing={loading ? 'true' : undefined}>
        <table className={`${styles.table} ${styles.matrix}`} data-daily-ledger>
          <thead><tr>
            <th scope="col" className={styles.nameCol}>{text('api_key')}</th>
            {SUMMARY_COLUMNS.map(column => {
              const period = columnPeriod(column);
              return <th key={column} scope="col" className={styles.totalCol} data-summary={column} aria-sort={sort === column ? (descending ? 'descending' : 'ascending') : 'none'}>
                <button type="button" className={styles.headSort} data-sort={column} aria-pressed={sort === column} onClick={() => chooseSort(column)}>
                  {period && <i className={period === 'work' ? styles.workSwatch : styles.offSwatch} aria-hidden="true" />}
                  <span>{text(column)}</span>
                  <i className={styles.sortMark} data-direction={sort === column ? (descending ? 'desc' : 'asc') : 'none'} aria-hidden="true" />
                </button>
              </th>;
            })}
            {calendarMeta.map(({ day, weekend, weekday }) => <th scope="col" key={day.date} data-day={day.date} title={day.date} data-today={day.is_today ? 'true' : undefined} data-weekend={weekend ? 'true' : undefined} className={styles.dayHead}>
              <span className={styles.dayHeadStack}>
                <strong>{day.date.slice(8)}</strong>
                <span>{weekday}</span>
              </span>
            </th>)}
          </tr></thead>
          <tbody>{rankedRows.map(({ row, days }) => {
            const name = keyName(row.key);
            return <tr key={row.key.id} data-time-usage-key={row.key.id}>
              <th scope="row" className={styles.nameCol} title={name}>
                <div className={styles.participant}>
                  <span className={styles.avatarFrame}><TimeUsageAvatar avatarID={timeUsageAvatarID(row.key.id, row.key.avatar_id)} /></span>
                  <span className={styles.identity}>
                    <strong>{name}</strong>
                    {row.key.is_deleted && <em>{text('deleted')}</em>}
                  </span>
                </div>
              </th>
              {SUMMARY_COLUMNS.map(column => <td key={column} className={styles.totalCol} data-summary={column} data-key-total={totalAttribute(column)} data-sorted={sort === column ? 'true' : undefined}>{renderSummary(row, column)}</td>)}
              {days.map((day, index) => <td key={day.date} data-date={day.date} data-today={day.is_today ? 'true' : undefined} data-weekend={calendarMeta[index]?.weekend ? 'true' : undefined} className={styles.dayCell}>
                <DayPair day={day} format={tokens} messages={messages} duration={cellDuration} exact={exact} durationTitle={durationTitle} workLabel={workLabel} offLabel={offLabel} messageLabel={text('message_count')} durationLabel={text('request_duration')} />
              </td>)}
            </tr>;
          })}</tbody>
          <tfoot><tr>
            <th scope="row" className={styles.nameCol}>{text('total')}</th>
            {SUMMARY_COLUMNS.map(column => <td key={column} className={styles.totalCol} data-summary={column} data-key-total={totalAttribute(column)} data-stat={column} data-sorted={sort === column ? 'true' : undefined}>{renderSummary(data.summary, column)}</td>)}
            {calendarMeta.map(({ day, weekend }) => <td key={day.date} data-today={day.is_today ? 'true' : undefined} data-weekend={weekend ? 'true' : undefined} className={styles.dayCell}>
              <DayPair day={day} format={tokens} messages={messages} duration={cellDuration} exact={exact} durationTitle={durationTitle} workLabel={workLabel} offLabel={offLabel} messageLabel={text('message_count')} durationLabel={text('request_duration')} />
            </td>)}
          </tr></tfoot>
        </table>
      </div>}
    </div>
  </section>;
}

const DayPair = memo(function DayPair({ day, format, messages, duration, exact, durationTitle, workLabel, offLabel, messageLabel, durationLabel }: {
  day: TimeUsageDay;
  format: (value: number) => string;
  messages: (value: number) => string;
  duration: (value: number) => string;
  exact: (value: number) => string;
  durationTitle: (sum: number, samples: number) => string;
  workLabel: string;
  offLabel: string;
  messageLabel: string;
  durationLabel: string;
}) {
  const band = (period: 'work' | 'off', tokens: number, label: string) => {
    const messageCount = period === 'work' ? (day.daytime_message_count ?? 0) : (day.after_hours_message_count ?? 0);
    const durationMs = latencyOf(day, period);
    const quiet = tokens <= 0 && messageCount <= 0 && durationMs <= 0;
    return <div className={styles.band} data-period={period} data-empty={quiet ? 'true' : undefined}>
      <Amount value={tokens} period={period} format={format} exact={exact(tokens)} label={label} figure="tokens" />
      <MetaRow>
        <SubLine plain metric="messages" period={period} label={messageLabel} value={messages(messageCount)} title={exact(messageCount)} />
        <span className={styles.metaDot} aria-hidden="true">·</span>
        <SubLine plain metric="duration" period={period} label={durationLabel} value={duration(durationMs)} title={durationTitle(durationMs, samplesOf(day, period))} />
      </MetaRow>
    </div>;
  };
  return <div className={styles.pair}>
    {band('work', day.daytime_tokens, workLabel)}
    <SplitBar work={day.daytime_tokens} off={day.after_hours_tokens} compact />
    {band('off', day.after_hours_tokens, offLabel)}
  </div>;
});
