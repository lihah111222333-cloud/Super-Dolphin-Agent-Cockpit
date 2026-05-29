// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

import { shouldReloadThreadHistory } from './stores/thread-sync-selectors.js';

function selectorContext(status, loadedAt) {
  return {
    state: {
      statuses: { 'thread-archived': status },
      agentRuntimeById: {},
    },
    threadHistoryLoadedAtByThread: new Map([['thread-archived', loadedAt]]),
    threadHistoryProviderThreadIDByThread: new Map(),
    THREAD_HISTORY_FRESH_TTL_MS: 30_000,
    normalizeProviderThreadID: (value) => (value || '').toString().trim(),
    logDebug: vi.fn(),
    logWarn: vi.fn(),
  };
}

describe('terminal thread statuses', () => {
  it('does not poll archived history using streaming ttl', () => {
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date('2026-03-10T00:00:00Z'));
      const ctx = selectorContext('archived', Date.now());

      vi.setSystemTime(new Date('2026-03-10T00:00:01.500Z'));
      expect(shouldReloadThreadHistory(ctx, 'thread-archived')).toBe(false);

      vi.setSystemTime(new Date('2026-03-10T00:00:30.001Z'));
      expect(shouldReloadThreadHistory(ctx, 'thread-archived')).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });
});
