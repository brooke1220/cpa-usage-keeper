import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { readStoredTaskIdleMinutes, readStoredTaskTimeEnabled, TASK_IDLE_MINUTE_OPTIONS, writeStoredTaskIdleMinutes, writeStoredTaskTimeEnabled } from '@/features/api-key-time-usage/model';
import styles from './TaskTimeSettingsCard.module.scss';

export function TaskTimeSettingsCard() {
  const { t } = useTranslation();
  const [enabled, setEnabled] = useState(readStoredTaskTimeEnabled);
  const [idleMinutes, setIdleMinutes] = useState(readStoredTaskIdleMinutes);
  const hintId = useId();
  const idleHintId = useId();
  const text = (key: string, params?: Record<string, string | number>) => t(`api_key_time_usage.${key}`, params);

  return <Card className={styles.card} title={text('usage_display_title')} subtitle={text('usage_display_subtitle')} data-task-time-settings>
    <label className={styles.toggle}>
      <input type="checkbox" role="switch" data-task-time-toggle checked={enabled} aria-describedby={hintId} onChange={event => {
        setEnabled(event.target.checked);
        writeStoredTaskTimeEnabled(event.target.checked);
      }} />
      <span>{text('show_task_time')}</span>
    </label>
    <p id={hintId} className={styles.hint}>{text('task_time_settings_hint')}</p>
    {enabled && <fieldset className={styles.idleField} aria-describedby={idleHintId}>
      <legend>{text('task_idle')}</legend>
      <div className={styles.options}>
        {TASK_IDLE_MINUTE_OPTIONS.map(option => <button key={option} type="button" className={styles.option} data-task-idle={option} aria-pressed={idleMinutes === option} onClick={() => {
          setIdleMinutes(option);
          writeStoredTaskIdleMinutes(option);
        }}>
          {text('task_idle_option', { count: option })}
        </button>)}
      </div>
      <p id={idleHintId} className={styles.hint}>{text('task_idle_hint')}</p>
    </fieldset>}
  </Card>;
}
