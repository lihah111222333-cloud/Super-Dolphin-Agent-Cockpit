import { beforeEach, expect, it, vi } from 'vitest';
import {
  clearFrontendHealth,
  createFrontendHealthStore,
  diagnosticCauseForTest,
  frontendHealthIdentity,
  frontendHealthStateSnapshot,
  FRONTEND_HEALTH_LIMIT,
  FRONTEND_HEALTH_STORAGE_KEY,
  recordFrontendHealth,
  retainDiagnosticCause,
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

it('caps retained diagnostic causes at the Health record limit', () => {
  const causes = Array.from(
    { length: FRONTEND_HEALTH_LIMIT + 1 },
    (_, index) => new Error(`raw diagnostic cause ${index}`),
  );

  causes.forEach((cause, index) => retainDiagnosticCause(`diagnostic-${index}`, cause));

  expect(diagnosticCauseForTest('diagnostic-0')).toBeUndefined();
  expect(diagnosticCauseForTest('diagnostic-1')).toBe(causes[1]);
  expect(diagnosticCauseForTest(`diagnostic-${FRONTEND_HEALTH_LIMIT}`)).toBe(causes[FRONTEND_HEALTH_LIMIT]);
});

it('clears retained raw diagnostic causes with Health records', () => {
  const cause = new Error('raw diagnostic cause must be released');
  retainDiagnosticCause('diagnostic-clear', cause);

  expect(diagnosticCauseForTest('diagnostic-clear')).toBe(cause);
  clearFrontendHealth();
  expect(diagnosticCauseForTest('diagnostic-clear')).toBeUndefined();
});
