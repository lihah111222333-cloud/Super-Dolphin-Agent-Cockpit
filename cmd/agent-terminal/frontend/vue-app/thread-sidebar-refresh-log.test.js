// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';
import { refreshSidebarState } from './stores/thread-sync-helpers.js';

function deferred() {
  let resolve;
  const promise = new Promise((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

function sidebarSnapshot() {
  return {
    threads: [{ id: 'thread-live', name: 'Live', state: 'idle' }],
    statuses: { 'thread-live': 'idle' },
    activeThreadId: 'thread-live',
    activeCmdThreadId: '',
  };
}

function createRefreshContext(overrides = {}) {
  return {
    callAPI: vi.fn(async () => sidebarSnapshot()),
    logDebug: vi.fn(),
    logWarn: vi.fn(),
    state: {
      activeThreadId: 'thread-live',
      activeCmdThreadId: '',
      threads: [],
    },
    runtimeSnapshotRequestSeq: 0,
    latestRuntimeSnapshotRequestSeqByScope: new Map(),
    threadDiffLoadedRevisionByThread: new Map(),
    sidebarRefreshPromise: null,
    sidebarRefreshPending: false,
    withPreferenceScope: (payload) => payload,
    applyRuntimeSnapshot: vi.fn(),
    ...overrides,
  };
}

describe('sidebar refresh logging', () => {
  it('does not warn for successful sidebar refresh lifecycle logs', async () => {
    const ctx = createRefreshContext();

    await refreshSidebarState(ctx);

    expect(ctx.logWarn).not.toHaveBeenCalled();
  });

  it('does not warn when overlapping sidebar refresh joins the pending request', async () => {
    const sidebar = deferred();
    const ctx = createRefreshContext({
      callAPI: vi.fn(async () => sidebar.promise),
    });

    const first = refreshSidebarState(ctx);
    await Promise.resolve();
    const second = refreshSidebarState(ctx);
    sidebar.resolve(sidebarSnapshot());
    await Promise.all([first, second]);

    expect(ctx.logWarn).not.toHaveBeenCalled();
  });

  it('keeps failed sidebar refreshes at warn level', async () => {
    const error = new Error('catalog unavailable');
    const ctx = createRefreshContext({
      callAPI: vi.fn(async () => { throw error; }),
    });

    await refreshSidebarState(ctx);

    expect(ctx.logWarn).toHaveBeenCalledWith('thread', 'sidebar.refresh.failed', expect.objectContaining({ error }));
  });
});
