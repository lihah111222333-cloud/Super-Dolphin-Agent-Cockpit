// @ts-nocheck
import { describe, expect, it } from 'vitest';

import {
  ensureThreadOrderIndex,
  sortThreadsByStableFirstSeen,
  normalizeEpochMillis,
  parseEpochMillis,
  parseThreadCreatedAtFromID,
} from './stores/thread-time-utils.js';

describe('thread-time-utils', () => {
  it('reuses stable order indexes for the same thread id', () => {
    const a1 = ensureThreadOrderIndex('thread-a');
    const a2 = ensureThreadOrderIndex('thread-a');
    const b = ensureThreadOrderIndex('thread-b');

    expect(a2).toBe(a1);
    expect(b).toBeGreaterThanOrEqual(a1);
  });

  it('sorts threads by stable first-seen order instead of incoming order', () => {
    ensureThreadOrderIndex('thread-sort-a');
    ensureThreadOrderIndex('thread-sort-b');

    const sorted = sortThreadsByStableFirstSeen([
      { id: 'thread-sort-b', name: 'B' },
      { id: 'thread-sort-a', name: 'A' },
    ]);

    expect(sorted.map((item) => item.id)).toEqual(['thread-sort-a', 'thread-sort-b']);
  });

  it('normalizes and parses epoch millis from numbers and strings', () => {
    expect(normalizeEpochMillis(1710000000)).toBe(1710000000000);
    expect(normalizeEpochMillis(1710000000123)).toBe(1710000000123);
    expect(normalizeEpochMillis(-1)).toBe(0);

    expect(parseEpochMillis('1710000000')).toBe(1710000000000);
    expect(parseEpochMillis('2026-03-10T00:00:00Z')).toBe(Date.parse('2026-03-10T00:00:00Z'));
    expect(parseEpochMillis('not-a-date')).toBe(0);
  });

  it('extracts plausible creation timestamps from thread ids', () => {
    expect(parseThreadCreatedAtFromID('thread-1710028800000-worker')).toBe(1710028800000);
    expect(parseThreadCreatedAtFromID('thread-without-timestamp')).toBe(0);
  });
});
