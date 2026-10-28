import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { RequestEventsTestCard } from './requestEventsFixtures';
import type { UsageEvent } from '@/lib/types';

const event: UsageEvent = {
  id: '1', timestamp: '2026-10-27T01:00:00Z', model: 'model-a',
  source: 'Channel A', source_type: 'codex', source_id: 'src_one', source_name: 'Beijing CPA',
  isDelete: true, failed: false, tokens: { total_tokens: 10 },
};

describe('pushed request event sources', () => {
  it('shows the credential name and a neutral CPA origin instead of a deletion warning', () => {
    const html = renderToStaticMarkup(<RequestEventsTestCard events={[event]} visibleColumnIds={['source']} />);
    expect(html).toContain('Channel A');
    expect(html).toContain('Pushed');
    expect(html).toContain('Beijing CPA');
    expect(html).not.toContain('Deleted');
  });

  it('keeps the push marker when a CPA registration no longer exists', () => {
    const html = renderToStaticMarkup(<RequestEventsTestCard events={[{ ...event, source_name: undefined }]} visibleColumnIds={['source']} />);
    expect(html).toContain('Pushed');
    expect(html).not.toContain('undefined');
    expect(html).not.toContain('Deleted');
  });

  it('preserves deletion warnings for local credentials', () => {
    const html = renderToStaticMarkup(<RequestEventsTestCard events={[{ ...event, source_id: undefined, source_name: undefined }]} visibleColumnIds={['source']} />);
    expect(html).toContain('Deleted');
    expect(html).not.toContain('Pushed');
  });
});
