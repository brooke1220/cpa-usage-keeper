import type { KeyboardEvent } from 'react';
import { RankingAvatar } from './RankingAvatar';
import styles from '../RankingPage.module.scss';
const AVATAR_IDS = Array.from({ length: 66 }, (_, index) => index + 1);
type Translate = (key: string, params?: Record<string, string | number>) => string;

export function AvatarPicker({ value, onChange, t, disabled = false }: {
  value: number;
  onChange: (value: number) => void;
  t: Translate;
  disabled?: boolean;
}) {
  const handleAvatarKeyDown = (event: KeyboardEvent<HTMLButtonElement>, currentID: number) => {
    const currentIndex = AVATAR_IDS.indexOf(currentID);
    let nextIndex = currentIndex;
    switch (event.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        nextIndex = (currentIndex + 1) % AVATAR_IDS.length;
        break;
      case 'ArrowLeft':
      case 'ArrowUp':
        nextIndex = (currentIndex - 1 + AVATAR_IDS.length) % AVATAR_IDS.length;
        break;
      case 'Home':
        nextIndex = 0;
        break;
      case 'End':
        nextIndex = AVATAR_IDS.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    const nextID = AVATAR_IDS[nextIndex];
    onChange(nextID);
    event.currentTarget.parentElement
      ?.querySelector<HTMLButtonElement>(`[data-ranking-avatar-option="${nextID}"]`)
      ?.focus();
  };

  return (
    <div className={styles.avatarGrid} role="radiogroup" aria-label={t('ranking.avatar')}>
      {AVATAR_IDS.map((id) => (
        <button
          key={id}
          type="button"
          role="radio"
          aria-checked={value === id}
          aria-label={t('ranking.avatar_option', { id })}
          tabIndex={value === id ? 0 : -1}
          className={`${styles.avatarOption} ${value === id ? styles.avatarOptionActive : ''}`.trim()}
          onClick={() => onChange(id)}
          onKeyDown={(event) => handleAvatarKeyDown(event, id)}
          data-ranking-avatar-option={id}
          disabled={disabled}
        >
          <RankingAvatar avatarID={id} name={t('ranking.avatar_option', { id })} decorative />
        </button>
      ))}
    </div>
  );
}

