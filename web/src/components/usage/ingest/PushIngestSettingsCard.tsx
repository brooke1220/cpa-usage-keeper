import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Modal } from '@/components/ui/Modal';
import { IconCheck, IconCopy } from '@/components/ui/icons';
import {
  ApiError,
  createPushSource,
  deletePushSource,
  fetchPushSettings,
  renamePushSource,
  rotatePushSource,
  setPushSourceRevoked,
  updatePushEnabled,
  type IssuedPushSource,
  type PushSettingsResponse,
  type PushSource,
} from '@/lib/api';
import { copyTextToClipboard } from '@/utils/clipboard';
import styles from './PushIngestSettingsCard.module.scss';

type Notice = (kind: 'success' | 'error', message: string) => void;
type PendingAction = { kind: 'rotate' | 'revoke' | 'delete'; source: PushSource };

const MAX_NAME_LENGTH = 64;

interface Props {
  refreshToken?: number;
  onAuthRequired?: () => void;
  onNotice?: Notice;
  // 开关或来源变化后通知页面刷新接收状态面板，拉取实例无需整页刷新就能看到面板。
  onSettingsChanged?: () => void;
}

// 上传地址按浏览器访问 Keeper 的地址拼出；CPA 走内网或代理时管理员需要换成 CPA 能访问的地址。
export const buildPushUploadURL = (uploadPath: string, origin = typeof window === 'undefined' ? '' : window.location.origin) => `${origin}${uploadPath}`;

const withoutToken = (source: PushSource | IssuedPushSource): PushSource => {
  const { token: _token, ...rest } = source as IssuedPushSource;
  return rest;
};

const validName = (name: string) => {
  const trimmed = name.trim();
  return trimmed.length > 0 && [...trimmed].length <= MAX_NAME_LENGTH;
};

export function PushIngestSettingsCard({ refreshToken = 0, onAuthRequired, onNotice, onSettingsChanged }: Props) {
  const { t, i18n } = useTranslation();
  const text = useCallback((key: string, options?: Record<string, unknown>) => t(`push_ingest.${key}`, options), [t]);
  const [settings, setSettings] = useState<PushSettingsResponse | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [newName, setNewName] = useState('');
  const [nameError, setNameError] = useState('');
  const [editing, setEditing] = useState<{ id: number; name: string } | null>(null);
  const [pending, setPending] = useState<PendingAction | null>(null);
  const [issued, setIssued] = useState<IssuedPushSource | null>(null);
  const [copied, setCopied] = useState<'url' | 'token' | null>(null);

  const handleError = useCallback((error: unknown, fallbackKey: string) => {
    if (error instanceof ApiError && error.status === 401) {
      onAuthRequired?.();
      return;
    }
    const message = error instanceof ApiError && error.message === 'invalid_push_source_name' ? text('invalid_name') : text(fallbackKey);
    onNotice?.('error', message);
  }, [onAuthRequired, onNotice, text]);

  // 加载只随刷新信号重跑；不依赖翻译函数，切换语言不会重新请求或覆盖未保存的编辑。
  useEffect(() => {
    const controller = new AbortController();
    setLoadFailed(false);
    fetchPushSettings(controller.signal)
      .then((next) => { if (!controller.signal.aborted) setSettings(next); })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof ApiError && error.status === 401) {
          onAuthRequired?.();
          return;
        }
        setLoadFailed(true);
      });
    return () => controller.abort();
  }, [onAuthRequired, refreshToken]);

  // 列表只保存来源信息；token 明文只留在一次性弹窗状态里，关闭后不再驻留内存。
  const replaceSource = (source: PushSource) => {
    const listed = withoutToken(source);
    setSettings((current) => current && {
      ...current,
      sources: current.sources.map((item) => (item.id === listed.id ? listed : item)),
    });
  };

  const run = async (task: () => Promise<void>, fallbackKey = 'settings_save_error') => {
    setBusy(true);
    try {
      await task();
    } catch (error) {
      handleError(error, fallbackKey);
    } finally {
      setBusy(false);
    }
  };

  const toggleEnabled = (enabled: boolean) => run(async () => {
    const next = await updatePushEnabled(enabled);
    setSettings(next);
    onNotice?.('success', text(enabled ? 'saved_enabled' : 'saved_disabled'));
    onSettingsChanged?.();
  });

  const create = (event: FormEvent) => {
    event.preventDefault();
    if (!validName(newName)) {
      setNameError(text('invalid_name'));
      return;
    }
    setNameError('');
    void run(async () => {
      const next = await createPushSource(newName.trim());
      setSettings((current) => current && { ...current, sources: [...current.sources, withoutToken(next)] });
      setNewName('');
      setIssued(next);
      onSettingsChanged?.();
    });
  };

  const saveName = () => {
    if (!editing) return;
    if (!validName(editing.name)) {
      onNotice?.('error', text('invalid_name'));
      return;
    }
    void run(async () => {
      replaceSource(await renamePushSource(editing.id, editing.name.trim()));
      setEditing(null);
      onNotice?.('success', text('source_saved'));
    });
  };

  const confirmPending = () => {
    if (!pending) return;
    const { kind, source } = pending;
    void run(async () => {
      if (kind === 'rotate') {
        const next = await rotatePushSource(source.id);
        replaceSource(next);
        setIssued(next);
      } else if (kind === 'revoke') {
        replaceSource(await setPushSourceRevoked(source.id, true));
      } else {
        await deletePushSource(source.id);
        setSettings((current) => current && { ...current, sources: current.sources.filter((item) => item.id !== source.id) });
      }
      setPending(null);
      onSettingsChanged?.();
    });
  };

  const restore = (source: PushSource) => run(async () => {
    replaceSource(await setPushSourceRevoked(source.id, false));
    onSettingsChanged?.();
  });

  const copy = async (value: string, target: 'url' | 'token') => {
    try {
      await copyTextToClipboard(value);
      setCopied(target);
    } catch {
      onNotice?.('error', text('copy_failed'));
    }
  };

  const formatTime = (value?: string) => (value ? new Date(value).toLocaleString(i18n.language) : text('never_used'));
  const uploadURL = settings ? buildPushUploadURL(settings.upload_path) : '';
  const closeIssued = () => { setIssued(null); setCopied(null); };

  return (
    <Card className={styles.card} title={text('settings_title')} subtitle={text('settings_subtitle')}>
      {loadFailed && <p className={styles.error} role="alert">{text('settings_load_error')}</p>}
      {settings && (
        <div className={styles.body}>
          <label className={styles.switch}>
            <span>{text('enabled_label')}</span>
            <input type="checkbox" checked={settings.enabled} disabled={busy} onChange={(event) => void toggleEnabled(event.target.checked)} />
            <span className={styles.track} aria-hidden="true"><span className={styles.thumb} /></span>
          </label>
          {!settings.enabled && <p className={styles.hint}>{text('disabled_hint')}</p>}

          <div className={styles.field}>
            <span className={styles.label}>{text('upload_url')}</span>
            <div className={styles.copyRow}>
              <code className={styles.value}>{uploadURL}</code>
              <Button variant="secondary" size="sm" onClick={() => void copy(uploadURL, 'url')}>
                {copied === 'url' ? <IconCheck size={14} /> : <IconCopy size={14} />}
                {text(copied === 'url' ? 'copied' : 'copy')}
              </Button>
            </div>
            <p className={styles.hint}>{text('upload_url_hint')}</p>
          </div>

          <form className={styles.createRow} onSubmit={create}>
            <label className={styles.field}>
              <span className={styles.label}>{text('new_source_name')}</span>
              <input
                className="input"
                value={newName}
                maxLength={MAX_NAME_LENGTH}
                placeholder={text('new_source_placeholder')}
                aria-invalid={Boolean(nameError)}
                onChange={(event) => { setNewName(event.target.value); setNameError(''); }}
              />
            </label>
            <Button type="submit" loading={busy} disabled={busy}>{text('create')}</Button>
          </form>
          {nameError && <p className={styles.error} role="alert">{nameError}</p>}

          {settings.sources.length === 0
            ? <p className={styles.hint}>{text('sources_empty')}</p>
            : (
              <ul className={styles.sources}>
                {settings.sources.map((source) => (
                  <li key={source.id} className={styles.source} data-revoked={source.revoked || undefined}>
                    <div className={styles.sourceMain}>
                      {editing?.id === source.id
                        ? (
                          <input
                            className="input"
                            value={editing.name}
                            maxLength={MAX_NAME_LENGTH}
                            aria-label={text('new_source_name')}
                            onChange={(event) => setEditing({ id: source.id, name: event.target.value })}
                          />
                        )
                        : <strong className={styles.sourceName}>{source.name}</strong>}
                      <span className={styles.meta}>
                        {text('token_hint', { hint: source.token_hint })}
                        {' · '}
                        {text('last_used')}: {formatTime(source.last_used_at)}
                        {source.revoked && <span className={styles.badge}>{text('revoked')}</span>}
                      </span>
                    </div>
                    <div className={styles.actions}>
                      {editing?.id === source.id
                        ? (
                          <>
                            <Button size="sm" disabled={busy} onClick={saveName}>{text('save')}</Button>
                            <Button size="sm" variant="ghost" disabled={busy} onClick={() => setEditing(null)}>{text('cancel')}</Button>
                          </>
                        )
                        : (
                          <>
                            <Button size="sm" variant="ghost" disabled={busy} onClick={() => setEditing({ id: source.id, name: source.name })}>{text('rename')}</Button>
                            {source.revoked
                              ? (
                                <>
                                  <Button size="sm" variant="secondary" disabled={busy} onClick={() => void restore(source)}>{text('restore')}</Button>
                                  <Button size="sm" variant="danger" disabled={busy} onClick={() => setPending({ kind: 'delete', source })}>{text('delete')}</Button>
                                </>
                              )
                              : (
                                <>
                                  <Button size="sm" variant="secondary" disabled={busy} onClick={() => setPending({ kind: 'rotate', source })}>{text('rotate')}</Button>
                                  <Button size="sm" variant="danger" disabled={busy} onClick={() => setPending({ kind: 'revoke', source })}>{text('revoke')}</Button>
                                </>
                              )}
                          </>
                        )}
                    </div>
                  </li>
                ))}
              </ul>
            )}
        </div>
      )}

      <Modal
        open={pending !== null}
        title={pending ? text(`confirm_${pending.kind}_title`) : ''}
        onClose={() => { if (!busy) setPending(null); }}
        closeDisabled={busy}
        footer={(
          <>
            <Button variant="ghost" disabled={busy} onClick={() => setPending(null)}>{text('cancel')}</Button>
            <Button variant={pending?.kind === 'rotate' ? 'primary' : 'danger'} loading={busy} disabled={busy} onClick={confirmPending}>{text('confirm')}</Button>
          </>
        )}
      >
        {pending && <p>{text(`confirm_${pending.kind}`)}</p>}
      </Modal>

      <Modal
        open={issued !== null}
        title={issued ? text('token_title', { name: issued.name }) : ''}
        onClose={closeIssued}
        footer={<Button onClick={closeIssued}>{text('done')}</Button>}
      >
        {issued && (
          <div className={styles.issued}>
            <p className={styles.warning}>{text('token_once')}</p>
            <div className={styles.copyRow}>
              <code className={styles.value} data-testid="issued-token">{issued.token}</code>
              <Button variant="secondary" size="sm" onClick={() => void copy(issued.token, 'token')}>
                {copied === 'token' ? <IconCheck size={14} /> : <IconCopy size={14} />}
                {text(copied === 'token' ? 'copied' : 'copy')}
              </Button>
            </div>
            <p className={styles.hint}>{text('token_steps')}</p>
          </div>
        )}
      </Modal>
    </Card>
  );
}
