import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ApiError, fetchIngestQuarantine, retryIngestQuarantine, type IngestQuarantineItem } from '@/lib/api';
import type { StatusResponse } from '@/lib/types';
import { Button } from '@/components/ui/Button';
import styles from './IngestStatusPanel.module.scss';

type Props = { status: NonNullable<StatusResponse['ingest']>; onAuthRequired?: () => void };

export function IngestStatusPanel({ status, onAuthRequired }: Props) {
  const { t } = useTranslation();
  const [items, setItems] = useState<IngestQuarantineItem[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const load = async () => {
    setBusy(true);
    setError('');
    try { setItems((await fetchIngestQuarantine()).items); }
    catch (err) {
      if (err instanceof ApiError && (err.status === 401 || err.status === 403)) onAuthRequired?.();
      setError(t('push_ingest.load_error'));
    } finally { setBusy(false); }
  };
  const retry = async (id: number) => {
    setBusy(true);
    setError('');
    try { await retryIngestQuarantine(id); setItems(current => current?.filter(item => item.id !== id) ?? null); }
    catch (err) {
      if (err instanceof ApiError && (err.status === 401 || err.status === 403)) onAuthRequired?.();
      setError(t('push_ingest.retry_error'));
    } finally { setBusy(false); }
  };
  const activeSources = (status.sources ?? []).filter(source => !source.revoked);
  return <section className={styles.panel} aria-label={t('push_ingest.title')}>
    <div className={styles.summary}>
      <strong>{t('push_ingest.title')}</strong>
      <span>{t('push_ingest.pending', { count: status.pending })}</span>
      <span>{t('push_ingest.quarantined', { count: status.quarantined })}</span>
      <span>{t('push_ingest.last_received')}: {status.last_received_at ? new Date(status.last_received_at).toLocaleString() : '—'}</span>
      <Button variant="secondary" disabled={busy} onClick={() => { if (items === null) void load(); else setItems(null); }}>{t('push_ingest.inspect')}</Button>
    </div>
    {status.enabled === false && <p className={styles.notice}>{t('push_ingest.receiving_off')}</p>}
    {activeSources.length > 0 && <ul className={styles.sources}>
      {activeSources.map((source, index) => <li key={`${index}-${source.name}`}>
        <span>{source.name}</span>
        <span>{source.last_used_at ? new Date(source.last_used_at).toLocaleString() : t('push_ingest.source_never')}</span>
      </li>)}
    </ul>}
    {error && <p role="alert">{error}</p>}
    {items !== null && <div className={styles.list}>
      <p>{t('push_ingest.quarantine_help')}</p>
      {items.length === 0 && <p>{t('push_ingest.empty')}</p>}
      {items.map(item => <div key={item.id} className={styles.row}>
        <span>{item.source_name || item.source_id}</span><code>{item.delivery_event_id}</code>
        <span>{t('push_ingest.attempts', { count: item.attempts })}</span>
        <Button variant="secondary" disabled={busy} onClick={() => void retry(item.id)}>{t('push_ingest.retry')}</Button>
      </div>)}
    </div>}
  </section>;
}
