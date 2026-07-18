import { beforeEach, expect, it, vi } from 'vitest';
import {
  clearFrontendHealth,
  createFrontendHealthStore,
  frontendHealthIdentity,
  frontendHealthStateSnapshot,
  FRONTEND_HEALTH_STORAGE_KEY,
  recordFrontendHealth,
  resetFrontendHealthForTest,
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

function publicError(diagnosticId) {
  return {
    code: 'PROMPT_HISTORY_UNAVAILABLE',
    title: '无法浏览提示历史',
    message: '提示历史暂时不可用，草稿与光标位置已保留。',
    diagnosticId,
  };
}

beforeEach(() => resetFrontendHealthForTest());

it('persists and deduplicates safe Health records with first and last occurrence times', () => {
  const storage = storagePort();
  const times = ['2026-07-17T10:00:00.000Z', '2026-07-17T10:01:00.000Z'];
  const store = createFrontendHealthStore({ storage, now: () => times.shift() });

  store.record({ actionId: 'prompt-history.previous', publicError: publicError('diagnostic-1') });
  store.record({ actionId: 'prompt-history.previous', publicError: publicError('diagnostic-2') });

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

it('makes malformed or field-expanded persisted Health data observable in memory-only mode', () => {
  const storage = storagePort();
  storage.set(FRONTEND_HEALTH_STORAGE_KEY, JSON.stringify([{ rawCause: 'must not load' }]));
  expect(createFrontendHealthStore({ storage }).getState()).toEqual({
    records: [],
    persistence: expect.objectContaining({ status: 'failed', code: 'HEALTH_PERSISTENCE_FAILED' }),
  });
  storage.set(FRONTEND_HEALTH_STORAGE_KEY, '{invalid');
  expect(createFrontendHealthStore({ storage }).getState()).toEqual({
    records: [],
    persistence: expect.objectContaining({ status: 'failed', code: 'HEALTH_PERSISTENCE_FAILED' }),
  });
});

it('exposes one finite observable persistence failure state without a fallback store', () => {
  const storage = storagePort();
  const rawPersistenceCause = new Error('raw storage path=/Users/private');
  storage.set = vi.fn(() => { throw rawPersistenceCause; });
  const store = createFrontendHealthStore({ storage, now: () => '2026-07-17T10:00:00.000Z' });

  const result = store.record({
    actionId: 'fixture.persistence',
    publicError: publicError('diagnostic-persistence', 'raw provider token=secret'),
  });

  expect(result.persisted).toBe(false);
  expect(store.getState()).toEqual({
    records: [expect.objectContaining({ actionId: 'fixture.persistence' })],
    persistence: expect.objectContaining({ status: 'failed', code: 'HEALTH_PERSISTENCE_FAILED' }),
  });
  expect(JSON.stringify(store.getState())).not.toContain('raw storage');
  expect(JSON.stringify(store.getState())).not.toContain('raw provider');
});

it('makes a storage read exception observable even when the storage throws TypeError', () => {
  const rawReadCause = new TypeError('raw storage read failure');
  const store = createFrontendHealthStore({
    storage: {
      get: () => { throw rawReadCause; },
      remove: () => undefined,
      set: () => undefined,
    },
  });
  expect(store.getState()).toEqual({
    records: [],
    persistence: expect.objectContaining({ status: 'failed', code: 'HEALTH_PERSISTENCE_FAILED' }),
  });
  expect(JSON.stringify(store.getState())).not.toContain('raw storage');
});

it('uses the exact same identity for merge semantics and list keys', () => {
  const storage = storagePort();
  const store = createFrontendHealthStore({ storage, now: () => '2026-07-17T10:00:00.000Z' });
  store.record({ actionId: 'same', publicError: publicError('diagnostic-1') });
  store.record({ actionId: 'same', publicError: publicError('diagnostic-2') });
  store.record({
    actionId: 'same',
    publicError: { ...publicError('diagnostic-3'), message: '不同安全消息' },
  });

  const records = store.getSnapshot();
  expect(records).toHaveLength(2);
  expect(new Set(records.map(frontendHealthIdentity)).size).toBe(records.length);
});

it('makes default persistence failure observable and clear does not silently report success', () => {
  const originalStorage = window.localStorage;
  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    value: {
      getItem: () => null,
      removeItem: () => undefined,
      setItem: () => { throw new Error('quota raw'); },
    },
  });
  const result = recordFrontendHealth({
    actionId: 'fixture.default-persistence',
    publicError: publicError('diagnostic-default'),
  });
  expect(result.persisted).toBe(false);
  expect(frontendHealthStateSnapshot().persistence).toEqual(expect.objectContaining({ status: 'failed' }));

  const clearResult = clearFrontendHealth();
  expect(clearResult.persisted).toBe(true);
  Object.defineProperty(window, 'localStorage', { configurable: true, value: originalStorage });
});
