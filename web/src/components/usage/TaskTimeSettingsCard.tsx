import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { readStoredTaskTimeEnabled, writeStoredTaskTimeEnabled } from '@/features/api-key-time-usage/model';
import styles from './TaskTimeSettingsCard.module.scss';

export function TaskTimeSettingsCard() {
  const { t } = useTranslation();
  const [enabled, setEnabled] = useState(readStoredTaskTimeEnabled);
  const hintId = useId();
  const text = (key: string) => t(`api_key_time_usage.${key}`);

  return <Card className={styles.card} title={text('usage_display_title')} subtitle={text('usage_display_subtitle')} data-task-time-settings>
    <label className={styles.toggle}>
      <input type="checkbox" role="switch" data-task-time-toggle checked={enabled} aria-describedby={hintId} onChange={event => {
        setEnabled(event.target.checked);
        writeStoredTaskTimeEnabled(event.target.checked);
      }} />
      <span>{text('show_task_time')}</span>
    </label>
    <p id={hintId} className={styles.hint}>{text('task_time_settings_hint')}</p>
  </Card>;
}
