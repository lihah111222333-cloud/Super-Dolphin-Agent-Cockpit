import { beforeEach, expect, it, vi } from 'vitest';
import {
  clearFrontendHealth,
  createFrontendHealthStore,
  frontendHealthSnapshot,
  FRONTEND_HEALTH_STORAGE_KEY,
  recordLastResortFrontendHealth,
  resetFrontendHealthForTest,
  subscribeFrontendHealth,
} from './frontendHealthStore.js';

function storagePort() {
  const values = new Map();
  return {
    get: (key) => values.get(key) ?? null,
    remove: (key) => values.delete(key),
    set: (key, value) => values.set(key, value),
    values,
  };
}

function publicError(diagnosticId, rawCause) {
  return {
    code: 'PROMPT_HISTORY_UNAVAILABLE',
    title: '无法浏览提示历史',
    message: '提示历史暂时不可用，草稿与光标位置已保留。',
    diagnosticId,
    rawCause,
  };
}

beforeEach(() => resetFrontendHealthForTest());

it('persists and deduplicates safe Health records with first and last occurrence times', () => {
  const storage = storagePort();
  const times = ['2026-07-17T10:00:00.000Z', '2026-07-17T10:01:00.000Z'];
  const store = createFrontendHealthStore({ storage, now: () => times.shift() });

  store.record({ actionId: 'prompt-history.previous', publicError: publicError('diagnostic-1', 'raw provider cause') });
  store.record({ actionId: 'prompt-history.previous', publicError: publicError('diagnostic-2', 'raw provider cause') });

  expect(store.getSnapshot()).toEqual([expect.objectContaining({
    diagnosticId: 'diagnostic-2',
    firstOccurredAt: '2026-07-17T10:00:00.000Z',
    lastOccurredAt: '2026-07-17T10:01:00.000Z',
    occurrences: 2,
  })]);
  const persisted = storage.values.get(FRONTEND_HEALTH_STORAGE_KEY);
  expect(persisted).not.toContain('raw provider cause');
  expect(createFrontendHealthStore({ storage }).getSnapshot()).toEqual(store.getSnapshot());
});

it('fails fast on malformed or field-expanded persisted Health data', () => {
  const storage = storagePort();
  storage.set(FRONTEND_HEALTH_STORAGE_KEY, JSON.stringify([{ rawCause: 'must not load' }]));
  expect(() => createFrontendHealthStore({ storage })).toThrow('fields are invalid');
  storage.set(FRONTEND_HEALTH_STORAGE_KEY, '{invalid');
  expect(() => createFrontendHealthStore({ storage })).toThrow();
});

it('publishes and clears safe last-resort Health records', () => {
  const listener = vi.fn();
  const unsubscribe = subscribeFrontendHealth(listener);
  recordLastResortFrontendHealth({
    actionId: 'fixture.last-resort',
    publicError: publicError('diagnostic-last-resort', 'raw provider token=secret'),
  });

  expect(listener).toHaveBeenCalled();
  expect(frontendHealthSnapshot()).toEqual([
    expect.objectContaining({ actionId: 'fixture.last-resort', diagnosticId: 'diagnostic-last-resort' }),
  ]);
  expect(JSON.stringify(frontendHealthSnapshot())).not.toContain('raw provider token=secret');

  listener.mockClear();
  clearFrontendHealth();
  expect(listener).toHaveBeenCalled();
  expect(frontendHealthSnapshot()).toEqual([]);
  unsubscribe();
});
