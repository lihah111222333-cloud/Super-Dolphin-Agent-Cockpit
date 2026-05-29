import { describe, expect, it } from 'vitest';

import { buildVisibleChatThreadCards } from './utils/thread-page-utils.js';

describe('buildVisibleChatThreadCards archive timestamp handling', () => {
  it('renders lifecycle archived cards as archived without a preference timestamp', () => {
    const result = buildVisibleChatThreadCards({
      threads: [{ id: 'thread-archived', name: 'Archived', lifecycleStatus: 'archived', state: 'idle', threadStatus: 'idle' }],
      archivedMap: {},
      showArchived: true,
      displayNameOf: (t) => t.name,
    });

    expect(result.cards[0]).toEqual(expect.objectContaining({
      id: 'thread-archived',
      isArchived: true,
      status: 'idle',
      statusHeader: '已归档',
      isStale: false,
      staleReason: '',
    }));
  });

  it('does not treat legacy archive timestamp sentinel as expired', () => {
    const result = buildVisibleChatThreadCards({
      threads: [{ id: 'thread-recent', name: 'Recent Thread', lifecycleStatus: 'archived' }],
      archivedMap: { 'thread-recent': 1 },
      showArchived: true,
      displayNameOf: (t) => t.name,
    });

    expect(result.cards[0].isArchived).toBe(true);
    expect(result.cards[0].isStale).toBe(false);
    expect(result.cards[0].staleReason).toBe('');
  });
});
