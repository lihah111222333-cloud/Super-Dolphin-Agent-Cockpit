// @ts-nocheck
// Phase 1.7f / 1.8a 单测：useAutoContinueStatePersistence
// 覆盖 load / scheduleWrite（节流）/ flushWrite / delete / 错误处理 / 多 thread 互不影响。

import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock("./services/log.js", () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
  logError: vi.fn(),
}));
vi.mock("./services/api.js", () => ({ callAPI: vi.fn() }));

import {
  useAutoContinueStatePersistence,
  _AUTO_CONTINUE_STATE_PERSISTENCE_CONSTANTS as K,
} from './composables/useAutoContinueStatePersistence.js';

function makeCallAPI(routes = {}) {
  return vi.fn(async (method, params) => {
    if (typeof routes[method] === 'function') return routes[method](params);
    return null;
  });
}

describe('useAutoContinueStatePersistence · loadStateForThread', () => {
  it('合法 schema 触发 applyStateSnapshot 回填', async () => {
    const callAPI = makeCallAPI({
      'ui/auto-continue/state/get': () => ({
        path: '_internal/auto-continue/state/t1.json',
        content: JSON.stringify({
          schemaVersion: 1, threadId: 't1',
          manualAbortAt: 1000, manualAbortSource: 'ui_stop',
          watchdogPokeCount: 3, lastUpdatedAt: 2000,
        }),
      }),
    });
    const apply = vi.fn();
    const persistence = useAutoContinueStatePersistence({ callAPIFn: callAPI, applyStateSnapshot: apply });
    const result = await persistence.loadStateForThread('t1');
    expect(result.threadId).toBe('t1');
    expect(apply).toHaveBeenCalledWith('t1', expect.objectContaining({ manualAbortAt: 1000, watchdogPokeCount: 3 }));
    expect(persistence._isLoadedForTest('t1')).toBe(true);
  });

  it('schemaVersion 不匹配 → reject + log + 标记已加载（不再重复 get）', async () => {
    const callAPI = makeCallAPI({
      'ui/auto-continue/state/get': () => ({
        path: '_internal/auto-continue/state/t1.json',
        content: JSON.stringify({ schemaVersion: 2, threadId: 't1' }),
      }),
    });
    const apply = vi.fn();
    const persistence = useAutoContinueStatePersistence({ callAPIFn: callAPI, applyStateSnapshot: apply });
    expect(await persistence.loadStateForThread('t1')).toBeNull();
    expect(apply).not.toHaveBeenCalled();
    // 第二次调用不会再触发 get
    await persistence.loadStateForThread('t1');
    expect(callAPI).toHaveBeenCalledTimes(1);
  });

  it('threadId 不匹配 → reject', async () => {
    const callAPI = makeCallAPI({
      'ui/auto-continue/state/get': () => ({
        content: JSON.stringify({ schemaVersion: 1, threadId: 'other' }),
      }),
    });
    const apply = vi.fn();
    const persistence = useAutoContinueStatePersistence({ callAPIFn: callAPI, applyStateSnapshot: apply });
    expect(await persistence.loadStateForThread('t1')).toBeNull();
    expect(apply).not.toHaveBeenCalled();
  });

  it('content 非合法 JSON → reject + log，不抛', async () => {
    const callAPI = makeCallAPI({
      'ui/auto-continue/state/get': () => ({ content: 'not json at all' }),
    });
    const apply = vi.fn();
    const persistence = useAutoContinueStatePersistence({ callAPIFn: callAPI, applyStateSnapshot: apply });
    expect(await persistence.loadStateForThread('t1')).toBeNull();
    expect(apply).not.toHaveBeenCalled();
  });

  it('文件不存在（not found）→ 标记已尝试，不打 warn', async () => {
    const callAPI = vi.fn().mockRejectedValue(new Error('shared file not found: ...'));
    const persistence = useAutoContinueStatePersistence({ callAPIFn: callAPI });
    expect(await persistence.loadStateForThread('t1')).toBeNull();
    expect(persistence._isLoadedForTest('t1')).toBe(true);
  });

  it('inflight 去重：同 thread 并发调用复用同一 Promise', async () => {
    let resolveGet;
    const getPromise = new Promise((resolve) => { resolveGet = resolve; });
    const callAPI = vi.fn().mockImplementation(() => getPromise);
    const persistence = useAutoContinueStatePersistence({ callAPIFn: callAPI });
    const p1 = persistence.loadStateForThread('t1');
    const p2 = persistence.loadStateForThread('t1');
    expect(callAPI).toHaveBeenCalledTimes(1);
    resolveGet({ content: '' });
    await Promise.all([p1, p2]);
  });

  it('空 threadId 直接 return null', async () => {
    const callAPI = vi.fn();
    const persistence = useAutoContinueStatePersistence({ callAPIFn: callAPI });
    expect(await persistence.loadStateForThread('')).toBeNull();
    expect(await persistence.loadStateForThread(null)).toBeNull();
    expect(callAPI).not.toHaveBeenCalled();
  });
});

describe('useAutoContinueStatePersistence · scheduleWrite + flushWrite', () => {
  it('5s 节流：multiple scheduleWrite 内只 flush 一次', () => {
    vi.useFakeTimers();
    try {
      const upserts = [];
      const callAPI = vi.fn(async (method, params) => {
        if (method === 'ui/auto-continue/state/upsert') upserts.push(params);
      });
      const snapshot = { manualAbortAt: 1000, manualAbortSource: 'ui_stop', watchdogPokeCount: 2 };
      const persistence = useAutoContinueStatePersistence({
        callAPIFn: callAPI,
        getStateSnapshot: () => snapshot,
      });
      persistence.scheduleWrite('t1');
      persistence.scheduleWrite('t1');
      persistence.scheduleWrite('t1');
      expect(persistence._hasPendingForTest('t1')).toBe(true);
      vi.advanceTimersByTime(K.DEFAULT_WRITE_THROTTLE_MS - 1);
      expect(callAPI).not.toHaveBeenCalled();
      vi.advanceTimersByTime(2);
      expect(callAPI).toHaveBeenCalledTimes(1);
      expect(upserts[0].path).toBe('_internal/auto-continue/state/t1.json');
      const payload = JSON.parse(upserts[0].content);
      expect(payload.schemaVersion).toBe(1);
      expect(payload.threadId).toBe('t1');
      expect(payload.manualAbortAt).toBe(1000);
      expect(payload.manualAbortSource).toBe('ui_stop');
      expect(payload.watchdogPokeCount).toBe(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it('快照全空（abort 0 + count 0）→ 调 delete 而非 upsert', () => {
    vi.useFakeTimers();
    try {
      const calls = [];
      const callAPI = vi.fn(async (method, params) => { calls.push({ method, params }); });
      const persistence = useAutoContinueStatePersistence({
        callAPIFn: callAPI,
        getStateSnapshot: () => ({ manualAbortAt: 0, manualAbortSource: null, watchdogPokeCount: 0 }),
      });
      persistence.scheduleWrite('t1');
      vi.advanceTimersByTime(K.DEFAULT_WRITE_THROTTLE_MS + 100);
      expect(calls).toHaveLength(1);
      expect(calls[0].method).toBe('ui/auto-continue/state/delete');
    } finally {
      vi.useRealTimers();
    }
  });

  it('多 thread 节流互不影响', () => {
    vi.useFakeTimers();
    try {
      const upserts = [];
      const callAPI = vi.fn(async (method, params) => {
        if (method === 'ui/auto-continue/state/upsert') upserts.push(params);
      });
      const stateByThread = {
        t1: { manualAbortAt: 1000, watchdogPokeCount: 0 },
        t2: { manualAbortAt: 0, watchdogPokeCount: 5 },
      };
      const persistence = useAutoContinueStatePersistence({
        callAPIFn: callAPI,
        getStateSnapshot: (tid) => stateByThread[tid] || null,
      });
      persistence.scheduleWrite('t1');
      persistence.scheduleWrite('t2');
      vi.advanceTimersByTime(K.DEFAULT_WRITE_THROTTLE_MS + 100);
      expect(upserts).toHaveLength(2);
      const paths = upserts.map((u) => u.path).sort();
      expect(paths).toEqual([
        '_internal/auto-continue/state/t1.json',
        '_internal/auto-continue/state/t2.json',
      ]);
    } finally {
      vi.useRealTimers();
    }
  });

  it('upsert 错误被吞，不抛', async () => {
    vi.useFakeTimers();
    try {
      const callAPI = vi.fn().mockRejectedValue(new Error('boom'));
      const persistence = useAutoContinueStatePersistence({
        callAPIFn: callAPI,
        getStateSnapshot: () => ({ manualAbortAt: 1, manualAbortSource: 'ui_stop', watchdogPokeCount: 1 }),
      });
      persistence.scheduleWrite('t1');
      vi.advanceTimersByTime(K.DEFAULT_WRITE_THROTTLE_MS + 100);
      // 等微任务 flush
      await vi.runAllTimersAsync();
      expect(callAPI).toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('disposeAll 取消 pending timer', () => {
    vi.useFakeTimers();
    try {
      const callAPI = vi.fn();
      const persistence = useAutoContinueStatePersistence({
        callAPIFn: callAPI,
        getStateSnapshot: () => ({ manualAbortAt: 1, watchdogPokeCount: 1 }),
      });
      persistence.scheduleWrite('t1');
      persistence.disposeAll();
      vi.advanceTimersByTime(K.DEFAULT_WRITE_THROTTLE_MS + 100);
      expect(callAPI).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('useAutoContinueStatePersistence · clearStateForThread', () => {
  it('清 pending timer + 调 delete RPC', () => {
    vi.useFakeTimers();
    try {
      const calls = [];
      const callAPI = vi.fn(async (method, params) => { calls.push({ method, params }); });
      const persistence = useAutoContinueStatePersistence({
        callAPIFn: callAPI,
        getStateSnapshot: () => ({ manualAbortAt: 1, watchdogPokeCount: 1 }),
      });
      persistence.scheduleWrite('t1');
      persistence.clearStateForThread('t1');
      vi.advanceTimersByTime(K.DEFAULT_WRITE_THROTTLE_MS + 100);
      // delete 立即调，pending upsert 被取消
      const methods = calls.map((c) => c.method);
      expect(methods).toEqual(['ui/auto-continue/state/delete']);
    } finally {
      vi.useRealTimers();
    }
  });
});
