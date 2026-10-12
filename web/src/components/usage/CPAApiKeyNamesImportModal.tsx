import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import {
  CPA_API_KEY_NAMES_EXPORT_SCRIPT,
  CPAApiKeyNamesExportError,
  describeCPAApiKeyNameSources,
  filterCPAApiKeyNameSources,
  matchCPAApiKeyNames,
  parseCPAApiKeyNamesExport,
  resolveCPAApiBase,
  type CPAApiKeyAliasImport,
  type CPAApiKeyNameSource,
} from '@/lib/cpaApiKeyNames';
import type { CpaApiKeySettingsItem } from '@/lib/types';
import styles from '@/pages/UsagePage.module.scss';

interface Props {
  apiKeys: CpaApiKeySettingsItem[];
  cpaURL: string;
  onClose: () => void;
  onCopyScript: () => Promise<void>;
  onImport: (items: CPAApiKeyAliasImport[]) => Promise<number>;
  onNotice?: (kind: 'success' | 'info' | 'error', message: string) => void;
}

export function CPAApiKeyNamesImportModal({ apiKeys, cpaURL, onClose, onCopyScript, onImport, onNotice }: Props) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  const [error, setError] = useState('');
  const [importing, setImporting] = useState(false);

  const handleCopyScript = async () => {
    try {
      await onCopyScript();
      onNotice?.('success', t('usage_stats.api_key_names_script_copied'));
    } catch {
      setError(t('usage_stats.api_key_names_script_copy_failed'));
    }
  };

  const handleImport = async () => {
    setError('');
    let sources: CPAApiKeyNameSource[];
    try {
      sources = parseCPAApiKeyNamesExport(text);
    } catch (parseError) {
      const undecodable = parseError instanceof CPAApiKeyNamesExportError && parseError.reason === 'undecodable';
      setError(t(undecodable ? 'usage_stats.api_key_names_import_undecodable' : 'usage_stats.api_key_names_import_invalid'));
      return;
    }
    if (!sources.length) {
      setError(t('usage_stats.api_key_names_import_no_names'));
      return;
    }
    // Tell the user which address the names belong to; otherwise an address
    // mismatch looks the same as "no names".
    if (!filterCPAApiKeyNameSources(sources, cpaURL).length) {
      setError(t('usage_stats.api_key_names_import_base_mismatch', {
        found: describeCPAApiKeyNameSources(sources).join(', '),
        target: resolveCPAApiBase(cpaURL) || cpaURL,
      }));
      return;
    }
    const items = matchCPAApiKeyNames(apiKeys, sources, cpaURL);
    if (!items.length) {
      setError(t('usage_stats.api_key_names_import_no_matches'));
      return;
    }
    setImporting(true);
    try {
      const count = await onImport(items);
      onNotice?.(count ? 'success' : 'info', t('usage_stats.api_key_names_import_success', { count }));
      onClose();
    } catch {
      setError(t('usage_stats.api_key_names_import_failed'));
    } finally {
      setImporting(false);
    }
  };

  return (
    <Modal
      open
      title={t('usage_stats.api_key_names_import_title')}
      onClose={onClose}
      closeDisabled={importing}
      width={720}
      footer={<>
        <Button variant="secondary" onClick={onClose} disabled={importing}>{t('common.cancel')}</Button>
        <Button onClick={() => void handleImport()} disabled={importing || !text.trim()} loading={importing}>
          {t('usage_stats.api_key_names_import_action')}
        </Button>
      </>}
    >
      <div className={styles.apiKeyNamesImportBody}>
        <p className={styles.hint}>{t('usage_stats.api_key_names_import_hint')}</p>
        <ol className={styles.apiKeyNamesImportSteps}>
          <li>
            {cpaURL ? <a href={cpaURL} target="_blank" rel="noopener noreferrer">{t('usage_stats.api_key_names_import_open_cpa')}</a>
              : t('usage_stats.api_key_names_import_open_cpa')}
            {' '}{t('usage_stats.api_key_names_import_step_console')}
          </li>
          <li>{t('usage_stats.api_key_names_import_step_export')}</li>
          <li>{t('usage_stats.api_key_names_import_step_paste')}</li>
        </ol>
        <p className={styles.hint} data-console-warning>{t('usage_stats.api_key_names_import_console_warning')}</p>
        <label className={styles.apiKeyNamesImportField}>
          <span>{t('usage_stats.api_key_names_export_script')}</span>
          <textarea className={`input ${styles.apiKeyNamesImportCode}`} readOnly value={CPA_API_KEY_NAMES_EXPORT_SCRIPT} rows={8} spellCheck={false} />
        </label>
        <Button variant="secondary" size="sm" onClick={() => void handleCopyScript()}>{t('usage_stats.api_key_names_copy_script')}</Button>
        <label className={styles.apiKeyNamesImportField}>
          <span>{t('usage_stats.api_key_names_import_data')}</span>
          <textarea
            className="input" value={text} onChange={(event) => setText(event.target.value)}
            rows={5} maxLength={1 << 20} spellCheck={false} disabled={importing}
          />
        </label>
        {error && <div className={styles.errorBox} role="alert">{error}</div>}
      </div>
    </Modal>
  );
}
