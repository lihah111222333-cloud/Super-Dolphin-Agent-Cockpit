import { describe, expect, it } from 'vitest';
import { normalizeThread } from './clientStoreThreadModel.js';
import { normalizeThreadTimestamp } from '../../../../../shared/time/threadTimestamp.js';

describe('clientStoreThreadModel timestamps', () => {
  it('maps persisted epoch milliseconds to ISO for sidebar relative time', () => {
    expect(normalizeThread({ id: 'thread-1', updated_at: 1784719357000 }).updatedAt).toBe('2026-07-22T11:22:37.000Z');
  });

  it('preserves valid ISO timestamps from non-persistent thread sources', () => {
    expect(normalizeThreadTimestamp('2026-07-22T11:22:37.000Z')).toBe('2026-07-22T11:22:37.000Z');
  });

  it('rejects second timestamps instead of silently converting them in the UI', () => {
    expect(() => normalizeThreadTimestamp(1784719357)).toThrow('thread updatedAt 必须是毫秒时间戳：1784719357');
  });

  it('rejects invalid non-empty timestamps', () => {
    expect(() => normalizeThreadTimestamp('not-a-timestamp')).toThrow('thread updatedAt 时间戳无效：not-a-timestamp');
  });
});
