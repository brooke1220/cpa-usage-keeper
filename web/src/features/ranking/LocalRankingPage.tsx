import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { Modal } from '@/components/ui/Modal';
import { QuestionMarkHelp } from '@/components/ui/QuestionMarkHelp';
import { RankingApiError } from './api';
import { AvatarPicker } from './components/AvatarPicker';
import { RankingLeaderboardResults } from './components/RankingLeaderboardResults';
import { RankingMetricSelect, RankingToolbar } from './components/RankingToolbar';
import { useLocalRankingData } from './hooks/useLocalRankingData';
import { loadRankingPreferences, persistRankingPreferences } from './preferences';
import type { RankingLeaderboardEntry } from './types';
import styles from './RankingPage.module.scss';

interface Props {
  refreshToken?: number;
  onAuthRequired?: () => void;
  onProfileUpdated?: () => void;
}

// Keep the original local leaderboard, metric units, scoring, and editable key profiles.
export function LocalRankingPage({ refreshToken = 0, onAuthRequired, onProfileUpdated }: Props) {
  const { t, i18n } = useTranslation();
  const [preferences, setPreferences] = useState(loadRankingPreferences);
  const { period, metric } = preferences;
  const data = useLocalRankingData({ enabled: true, period, metric, onAuthRequired });
  const refreshLeaderboard = data.refreshLeaderboard;
  const previousRefresh = useRef(refreshToken);
  useEffect(() => {
    if (previousRefresh.current === refreshToken) return;
    previousRefresh.current = refreshToken;
    void refreshLeaderboard();
  }, [refreshLeaderboard, refreshToken]);

  const board = data.leaderboard?.period === period && data.leaderboard.metric === metric ? data.leaderboard : null;
  const rows = board?.entries.slice(0, 100) ?? [];
  const explanation = metric === 'overall' && data.leaderboard?.metric === metric && data.leaderboard.score_explanation?.version === 2
    ? data.leaderboard.score_explanation.texts : null;
  const language = i18n.resolvedLanguage || i18n.language;
  const scoreExplanation = explanation?.[language as 'en' | 'zh' | 'zh-TW'] || explanation?.en || '';
  const [editing, setEditing] = useState<RankingLeaderboardEntry | null>(null);
  const [alias, setAlias] = useState('');
  const [avatarID, setAvatarID] = useState(1);
  const [saving, setSaving] = useState(false);
  const [profileError, setProfileError] = useState(false);
  const openProfile = (entry: RankingLeaderboardEntry) => {
    setEditing(entry); setAlias(entry.key_alias ?? ''); setAvatarID(entry.avatar_id); setProfileError(false);
  };
  const saveProfile = async () => {
    if (!editing || saving) return;
    setSaving(true); setProfileError(false);
    try {
      await data.updateProfile(editing.participant_id, { key_alias: alias.trim(), avatar_id: avatarID });
      setEditing(null); onProfileUpdated?.();
    } catch { setProfileError(true); }
    finally { setSaving(false); }
  };
  const error = data.leaderboardError instanceof RankingApiError && data.leaderboardError.status === 429
    ? t('ranking.error_rate_limited_generic') : t('ranking.error_generic');
  const updated = board ? new Date(board.generated_at) : null;
  const updatedAt = updated && Number.isFinite(updated.getTime())
    ? new Intl.DateTimeFormat(language, { dateStyle: 'medium', timeStyle: 'short', hour12: false }).format(updated) : board?.generated_at;

  return <section className={styles.page} aria-label={t('ranking.title')} data-local-ranking>
    <article className={`card ${styles.leaderboardCard}`} aria-busy={data.leaderboardLoading}>
      <header className={styles.leaderboardHeader}>
        <div className={styles.leaderboardTitle} data-ranking-header-title>
          <div className="keeper-card-title-track">
            <div className={`${styles.metricTitleHeading} keeper-card-title`} role="heading" aria-level={2} data-ranking-metric-title>
              <RankingMetricSelect metric={metric} onMetricChange={next => {
                setPreferences(previous => ({ ...previous, metric: next })); persistRankingPreferences({ metric: next });
              }} />
            </div>
            {scoreExplanation && <span className={styles.scoreExplanationSlot} data-ranking-score-explanation-slot>
              <QuestionMarkHelp label={t('ranking.score_explanation_label')} description={scoreExplanation} portal={false}
                className={styles.profilePrivacyHelp} buttonClassName={styles.scoreExplanationHint}
                tooltipClassName={styles.profilePrivacyTooltip} tooltipVisibleClassName={styles.profilePrivacyTooltipVisible}
                buttonProps={{ 'data-ranking-score-explanation': true }} tooltipProps={{ 'data-ranking-score-explanation-tooltip': true }}>
                {scoreExplanation}
              </QuestionMarkHelp>
            </span>}
            <div className={styles.leaderboardHeaderToolbar} data-ranking-header-toolbar>
              <RankingToolbar period={period} onPeriodChange={next => {
                setPreferences(previous => ({ ...previous, period: next })); persistRankingPreferences({ period: next });
              }} />
            </div>
          </div>
          {board && <div className={styles.boardMeta}>
            {board.stale && <span className={styles.staleBadge} data-ranking-stale>{t('ranking.stale')} · {board.period_key}</span>}
            <span>{t('ranking.updated_at', { time: updatedAt ?? '' })}</span>
          </div>}
        </div>
      </header>
      {data.leaderboardLoading && !board ? <div className={styles.loadingState} role="status"><LoadingSpinner size={20} /><span>{t('ranking.leaderboard_loading')}</span></div>
        : data.leaderboardError && !board ? <div className={styles.errorState} role="alert"><p>{error}</p><Button variant="secondary" appearance="action" onClick={() => void data.refreshLeaderboard()}>{t('common.retry')}</Button></div>
          : rows.length === 0 ? <div className={styles.emptyState}><strong>{t('ranking.local_empty_title')}</strong><p>{t('ranking.local_empty_description')}</p></div>
            : <RankingLeaderboardResults scope="local" metric={metric} entries={rows} onEditLocalProfile={openProfile} />}
    </article>
    <Modal open={editing !== null} title={t('ranking.local_profile_edit')} onClose={() => { if (!saving) setEditing(null); }}
      closeDisabled={saving} width={600} className={styles.profileModal} footer={<>
        <Button variant="secondary" appearance="action" onClick={() => setEditing(null)} disabled={saving}>{t('common.cancel')}</Button>
        <Button appearance="action" onClick={() => void saveProfile()} loading={saving} data-ranking-local-profile-save>{t('ranking.local_profile_save')}</Button>
      </>}>
      {editing && <div className={styles.profileModalContent}>
        <div className={styles.joinForm}>
          <Input name="local-ranking-key-alias" value={alias} onChange={event => setAlias(event.target.value)} label={t('ranking.local_profile_alias')}
            hint={t('ranking.local_profile_alias_hint')} placeholder={editing.key_alias ? undefined : editing.display_name} autoComplete="off" disabled={saving} />
          <div className={styles.avatarField}><span className={styles.fieldLabel}>{t('ranking.avatar')}</span>
            <AvatarPicker value={avatarID} onChange={setAvatarID} t={t} disabled={saving} />
          </div>
        </div>
        {profileError && <div className={styles.errorBox} role="alert" data-ranking-local-profile-error>{t('ranking.local_profile_save_failed')}</div>}
      </div>}
    </Modal>
  </section>;
}
