import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { ApiError, fetchHiddenCpaApiKeyIds, updateHiddenCpaApiKeyIds } from '@/lib/api';
import type { CpaApiKeyDisplayItem, CpaApiKeySettingsItem } from '@/lib/types';
import styles from './HiddenApiKeysSettingsCard.module.scss';

interface Props {
  apiKeys: CpaApiKeySettingsItem[];
  loading?: boolean;
  refreshToken?: number;
  onAuthRequired?: () => void;
  onNotice?: (kind: 'success' | 'error', message: string) => void;
}

interface HiddenKeyOption {
  id: string;
  name: string;
  detail: string;
}

const sameIds = (left: ReadonlySet<string>, right: ReadonlySet<string>) => (
  left.size === right.size && [...left].every((id) => right.has(id))
);

// 选中的 Key 在“用量”和“排行”页不显示，也不计入合计；原始数据照常保留。
export function HiddenApiKeysSettingsCard({ apiKeys, loading = false, refreshToken = 0, onAuthRequired, onNotice }: Props) {
  const { t } = useTranslation();
  const [saved, setSaved] = useState<ReadonlySet<string>>(new Set());
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [deletedKeys, setDeletedKeys] = useState<CpaApiKeyDisplayItem[]>([]);
  const [ready, setReady] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const changed = ready && !sameIds(saved, selected);

  useEffect(() => {
    const controller = new AbortController();
    setReady(false);
    void fetchHiddenCpaApiKeyIds(controller.signal).then((response) => {
      if (controller.signal.aborted) return;
      const ids = new Set(response.ids ?? []);
      setSaved(ids);
      setSelected(ids);
      setDeletedKeys(response.deletedKeys ?? []);
      setError('');
      setReady(true);
    }).catch((loadError) => {
      if (controller.signal.aborted) return;
      if (loadError instanceof ApiError && loadError.status === 401) onAuthRequired?.();
      else setError(t('usage_stats.hidden_api_keys_load_failed'));
    });
    return () => controller.abort();
  // 与工作时段卡片一致：t 在应用内稳定，不因语言适配器重建而重复请求。
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onAuthRequired, refreshToken]);

  const toOption = (item: CpaApiKeyDisplayItem): HiddenKeyOption => {
    const identity = item.sourceType === 'plugin' ? t('usage_stats.api_key_settings_plugin_identity', { id: item.id }) : item.displayKey;
    return { id: item.id, name: item.keyAlias || identity, detail: item.keyAlias ? identity : '' };
  };
  const options = apiKeys.map(toOption);
  // 已从 CPA 删除的 Key 仍有历史用量，单独分组列出，使它们也能被隐藏或取消隐藏。
  const listed = new Set(apiKeys.map((item) => item.id));
  const deletedOptions = deletedKeys.filter((item) => !listed.has(item.id)).map(toOption);
  for (const item of deletedOptions) listed.add(item.id);
  // 加载后才被删除、尚未出现在已删除列表中的隐藏 Key，也保留取消隐藏的入口。
  for (const id of [...saved].filter((value) => !listed.has(value)).sort((a, b) => Number(a) - Number(b))) {
    deletedOptions.push({ id, name: t('usage_stats.hidden_api_keys_deleted', { id }), detail: '' });
  }
  const renderOption = (option: HiddenKeyOption) => (
    <label key={option.id} className={styles.option} data-hidden-api-key={option.id}>
      <input type="checkbox" checked={selected.has(option.id)} onChange={(event) => toggle(option.id, event.target.checked)} />
      <span className={styles.name}>{option.name}</span>
      {option.detail && <span className={styles.detail}>{option.detail}</span>}
    </label>
  );

  const toggle = (id: string, checked: boolean) => {
    setSelected((current) => {
      const next = new Set(current);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
    setError('');
  };

  const save = async () => {
    if (!changed || saving) return;
    setSaving(true);
    setError('');
    try {
      const response = await updateHiddenCpaApiKeyIds([...selected]);
      const ids = new Set(response.ids ?? []);
      setSaved(ids);
      setSelected(ids);
      onNotice?.('success', t('usage_stats.hidden_api_keys_saved'));
    } catch (saveError) {
      const message = t('usage_stats.hidden_api_keys_save_failed');
      setError(message);
      onNotice?.('error', message);
      if (saveError instanceof ApiError && saveError.status === 401) onAuthRequired?.();
    } finally {
      setSaving(false);
    }
  };

  const disabled = !ready || saving;
  return <Card className={styles.card} title={t('usage_stats.hidden_api_keys_title')} subtitle={t('usage_stats.hidden_api_keys_subtitle')}
    titleMeta={ready && selected.size > 0 ? <span className={styles.count}>{t('usage_stats.hidden_api_keys_count', { count: selected.size })}</span> : undefined}
    data-hidden-api-keys-card>
    <form className={styles.form} aria-busy={!ready} onSubmit={(event) => { event.preventDefault(); void save(); }}>
      {loading && options.length === 0 && deletedOptions.length === 0 ? (
        <p className={styles.hint}>{t('common.loading')}</p>
      ) : options.length === 0 && deletedOptions.length === 0 ? (
        <p className={styles.hint}>{t('usage_stats.api_key_settings_empty')}</p>
      ) : (
        <fieldset className={styles.list} disabled={disabled}>
          <legend className={styles.srOnly}>{t('usage_stats.hidden_api_keys_title')}</legend>
          {options.map(renderOption)}
          {deletedOptions.length > 0 && <>
            <p className={styles.groupLabel} data-hidden-api-keys-deleted-group>{t('usage_stats.hidden_api_keys_deleted_group')}</p>
            {deletedOptions.map(renderOption)}
          </>}
        </fieldset>
      )}
      <div className={styles.actions}>
        <Button type="submit" size="sm" data-hidden-api-keys-save disabled={!changed || saving} loading={saving}>{t('common.save')}</Button>
      </div>
      {error && <p className={styles.error} role="alert">{error}</p>}
    </form>
  </Card>;
}
