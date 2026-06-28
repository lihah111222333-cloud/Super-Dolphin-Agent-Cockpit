// @ts-nocheck
import { describe, expect, it } from 'vitest';

import { ensureThreadSelectionFresh } from './utils/thread-page-utils.js';

describe('ensureThreadSelectionFresh stale selection handling', () => {
  it('propagates stale selection errors during visible selection refresh', async () => {
    let synced = false;
    const store = {
      loadMessages: async () => {
        throw new Error('session not found for agent "agent-stale"');
      },
      getThreadTimeline: () => [],
      shouldReloadThreadHistory: () => false,
      syncThreadState: async () => { synced = true; },
    };

    await expect(ensureThreadSelectionFresh(store, 'agent-stale', {
      reason: 'selection',
      previousThreadId: 'thread-0',
    })).rejects.toThrow('session not found for agent "agent-stale"');
    expect(synced).toBe(false);
  });
});
