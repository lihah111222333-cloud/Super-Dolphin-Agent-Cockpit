// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';
import { createThreadActions } from './stores/thread-actions.js';
import { createSyncManager } from './stores/thread-sync.js';

function buildRuntimeState(overrides = {}) {
  return {
    activeThreadId: '',
    activeCmdThreadId: '',
    sendBlockedNoticesByThread: {},
    sendHoldNoticesByThread: {},

    pinnedThreadAtById: {},
    archivedThreadAtById: {},
    threads: [],
    statuses: {},
    interruptibleByThread: {},
    viewPrefsChat: null,
    viewPrefsCmd: null,
    statusHeadersByThread: {},
    statusDetailsByThread: {},
    timelinesByThread: {},
    diffTextByThread: {},
    diffRevisionByThread: {},
    tokenUsageByThread: {},
    agentMetaById: {},
    agentRuntimeById: {},
    activityStatsByThread: {},
    alertsByThread: {},
    skillRevision: 0,
    ...overrides,
  };
}

describe('thread store dependency injection', () => {
  it('routes sync manager api calls through injected deps', async () => {
    const state = buildRuntimeState({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', state: 'idle' }],
      statuses: { 'thread-1': 'idle' },
      interruptibleByThread: { 'thread-1': false },
      diffRevisionByThread: { 'thread-1': 0 },
    });
    const callAPI = vi.fn(async (method, payload) => {
      if (method !== 'ui/state/get') throw new Error(`unexpected api method: ${method}`);
      if (payload?.includeDiff) {
        return {
          diffTextByThread: { 'thread-1': '@@ -1 +1 @@' },
          diffRevisionByThread: { 'thread-1': 1 },
        };
      }
      return buildRuntimeState({
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', name: 'Thread 1', state: 'idle' }],
        statuses: { 'thread-1': 'idle' },
        interruptibleByThread: { 'thread-1': false },
        diffRevisionByThread: { 'thread-1': 0 },
      });
    });
    const applyRuntimeSnapshot = vi.fn((target, snapshot) => Object.assign(target, snapshot));
    const syncManager = createSyncManager(state, {
      callAPI,
      logDebug: vi.fn(),
      logInfo: vi.fn(),
      logWarn: vi.fn(),
      applyRuntimeSnapshot,
      withPreferenceScope: (payload) => ({ ...payload, cwd: '/repo' }),
      getPreferenceScopeCwd: () => '/repo',
      getCompactWaiter: () => null,
      settleCompactWaiter: () => false,
      setCompactResult: vi.fn(),
      compactWaitersByThread: new Map(),
      freezeTimelineItemsAtomic: (items) => items,
      normalizeProviderThreadID: (value) => (value ? String(value) : ''),
    });

    await syncManager.syncRuntimeState();
    await syncManager.syncThreadDiffState('thread-1', { force: true });

    expect(callAPI).toHaveBeenNthCalledWith(1, 'ui/state/get', { threadId: 'thread-1', includeDiff: false, cwd: '/repo' });
    expect(callAPI).toHaveBeenNthCalledWith(2, 'ui/state/get', { threadId: 'thread-1', includeDiff: true, knownDiffRevision: 0, cwd: '/repo' });
    expect(applyRuntimeSnapshot).toHaveBeenCalledTimes(2);
  });

  it('routes agent-event compatibility traffic through the same sync handler', async () => {
    const state = buildRuntimeState({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', state: 'idle' }],
      statuses: { 'thread-1': 'idle' },
      interruptibleByThread: { 'thread-1': false },
      diffRevisionByThread: { 'thread-1': 0 },
    });
    const callAPI = vi.fn(async (method) => {
      if (method !== 'ui/state/get') throw new Error(`unexpected api method: ${method}`);
      return buildRuntimeState({
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', name: 'Thread 1', state: 'running' }],
        statuses: { 'thread-1': 'running' },
        interruptibleByThread: { 'thread-1': true },
        diffRevisionByThread: { 'thread-1': 0 },
      });
    });
    const syncManager = createSyncManager(state, {
      callAPI,
      logDebug: vi.fn(),
      logInfo: vi.fn(),
      logWarn: vi.fn(),
      applyRuntimeSnapshot: (target, snapshot) => Object.assign(target, snapshot),
      withPreferenceScope: (payload) => ({ ...payload, cwd: '/repo' }),
      getPreferenceScopeCwd: () => '/repo',
      getCompactWaiter: () => null,
      settleCompactWaiter: () => false,
      setCompactResult: vi.fn(),
      compactWaitersByThread: new Map(),
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      normalizeProviderThreadID: (value) => (value ? String(value) : ''),
    });

    syncManager.handleAgentEvent({ method: 'ui/thread/changed', payload: { source: 'item/completed', threadId: 'thread-1' } });
    await Promise.resolve();
    await Promise.resolve();

    expect(callAPI).toHaveBeenCalledWith('ui/state/get', { threadId: 'thread-1', includeDiff: false, cwd: '/repo' });
  });

  it('routes UI stop through interrupt allowlist source', async () => {
    const state = buildRuntimeState({ threads: [{ id: 'thread-1', name: 'Thread 1', state: 'running' }] });
    const callAPI = vi.fn(async (method) => method === 'turn/interrupt' ? { confirmed: true, settled: true, interruptSent: true, mode: 'interrupt_confirmed' } : Promise.reject(new Error(`unexpected api method: ${method}`)));
    const deps = { callAPI, logInfo: vi.fn(), logWarn: vi.fn(), syncRuntimeState: vi.fn(async () => {}), syncThreadState: vi.fn(async () => {}), refreshSidebarState: vi.fn(async () => {}), persistPreferenceAndSync: vi.fn(), withPreferenceScope: (payload) => payload, getPreferenceScopeCwd: () => '/repo', markLocalActiveThreadDirty: vi.fn(), markLocalActiveCmdThreadDirty: vi.fn(), setCompactResult: vi.fn(), waitForCompactCompletion: vi.fn(), cancelCompactWaiter: vi.fn(), compactPendingByThread: {}, COMPACT_COMPLETION_TIMEOUT_MS: 1000, getThreadInterruptible: vi.fn(() => true), displayName: (item) => item?.name || item?.id || '' };
    const actions = createThreadActions(state, deps);
    const result = await actions.stopThread('thread-1', { source: 'ui_stop' });
    expect(callAPI).toHaveBeenCalledWith('turn/interrupt', { threadId: 'thread-1', source: 'ui_stop' });
    expect(deps.syncRuntimeState).toHaveBeenCalledTimes(1);
    expect(result).toMatchObject({ confirmed: true, settled: true, interruptSent: true, mode: 'interrupt_confirmed' });
  });

  it('routes action helpers through injected deps', async () => {
    globalThis.window = { ...(globalThis.window || {}), alert: vi.fn() };
    const state = buildRuntimeState({
      threads: [{ id: 'thread-1', name: 'Thread 1', state: 'idle' }],
    });
    const callAPI = vi.fn(async (method) => {
      if (method === 'thread/name/set') return {};
      if (method === 'thread/archive') return { archived: true };
      throw new Error(`unexpected api method: ${method}`);
    });
    const deps = {
      callAPI,
      logInfo: vi.fn(),
      logWarn: vi.fn(),
      syncRuntimeState: vi.fn(async () => {}),
      syncThreadState: vi.fn(async () => {}),
      refreshSidebarState: vi.fn(async () => {}),
      persistPreferenceAndSync: vi.fn(),
      withPreferenceScope: (payload) => payload,
      getPreferenceScopeCwd: () => '/repo',
      markLocalActiveThreadDirty: vi.fn(),
      markLocalActiveCmdThreadDirty: vi.fn(),
      setCompactResult: vi.fn(),
      waitForCompactCompletion: vi.fn(),
      cancelCompactWaiter: vi.fn(),
      compactPendingByThread: {},
      COMPACT_COMPLETION_TIMEOUT_MS: 1000,
      getThreadInterruptible: vi.fn(() => false),
      displayName: (item) => item?.name || item?.id || '',
    };
    const actions = createThreadActions(state, deps);

    await actions.renameThread('thread-1', 'Renamed');
    await actions.setThreadArchived('thread-1', true);

    expect(callAPI).toHaveBeenNthCalledWith(1, 'thread/name/set', { threadId: 'thread-1', name: 'Renamed' });
    expect(callAPI).toHaveBeenNthCalledWith(2, 'thread/archive', { threadId: 'thread-1' });
    expect(deps.refreshSidebarState).toHaveBeenCalledTimes(1);
    expect(deps.persistPreferenceAndSync).toHaveBeenCalledTimes(1);
    expect(state.archivedThreadAtById['thread-1']).toBeGreaterThan(0);
  });

  it('waits for stale delete preference writes before refreshing sidebar', async () => {
    const calls = [];
    const state = buildRuntimeState({
      threads: [{ id: 'thread-empty', name: 'thread-empty', state: 'archived' }],
      archivedThreadAtById: { 'thread-empty': Date.now() },
      pinnedThreadAtById: { 'thread-empty': Date.now() },
    });
    let resolvePersist;
    const persistDone = new Promise((resolve) => { resolvePersist = resolve; });
    const deps = {
      callAPI: vi.fn(async (method) => {
        if (method === 'thread/delete') return {};
        throw new Error(`unexpected api method: ${method}`);
      }),
      logInfo: vi.fn(),
      logWarn: vi.fn(),
      syncRuntimeState: vi.fn(async () => {}),
      syncThreadState: vi.fn(async () => {}),
      refreshSidebarState: vi.fn(async () => { calls.push('refresh'); }),
      persistPreferenceAndSync: vi.fn(() => persistDone.then(() => { calls.push('persist'); })),
      withPreferenceScope: (payload) => payload,
      getPreferenceScopeCwd: () => '/repo',
      markLocalActiveThreadDirty: vi.fn(),
      markLocalActiveCmdThreadDirty: vi.fn(),
      setCompactResult: vi.fn(),
      waitForCompactCompletion: vi.fn(),
      cancelCompactWaiter: vi.fn(),
      compactPendingByThread: {},
      COMPACT_COMPLETION_TIMEOUT_MS: 1000,
      getThreadInterruptible: vi.fn(() => false),
      displayName: (item) => item?.name || item?.id || '',
    };
    const actions = createThreadActions(state, deps);

    const deletePromise = actions.batchDeleteStaleThreads(['thread-empty']);
    await Promise.resolve();
    await Promise.resolve();
    expect(deps.refreshSidebarState).not.toHaveBeenCalled();
    resolvePersist();
    await deletePromise;

    expect(calls).toEqual(['persist', 'persist', 'refresh']);
    expect(deps.persistPreferenceAndSync).toHaveBeenCalledTimes(2);
    expect(deps.refreshSidebarState).toHaveBeenCalledTimes(1);
  });

  it('shows unarchive partial warnings via injected action helpers', async () => {
    globalThis.window = { ...(globalThis.window || {}), alert: vi.fn() };

    const state = buildRuntimeState({ threads: [{ id: 'thread-1', name: 'Thread 1', state: 'idle' }], archivedThreadAtById: { 'thread-1': Date.now() } });
    const callAPI = vi.fn(async (method) => {
      if (method === 'thread/unarchive') return { partial: true, warnings: ['restore skipped'], restoreSkippedFiles: ['/tmp/a'] };
      return {};
    });
    const deps = {
      callAPI,
      logInfo: vi.fn(),
      logWarn: vi.fn(),
      syncRuntimeState: vi.fn(async () => {}),
      syncThreadState: vi.fn(async () => {}),
      refreshSidebarState: vi.fn(async () => {}),
      persistPreferenceAndSync: vi.fn(),
      withPreferenceScope: (payload) => payload,
      getPreferenceScopeCwd: () => '/repo',
      markLocalActiveThreadDirty: vi.fn(),
      markLocalActiveCmdThreadDirty: vi.fn(),
      setCompactResult: vi.fn(),
      waitForCompactCompletion: vi.fn(),
      cancelCompactWaiter: vi.fn(),
      compactPendingByThread: {},
      COMPACT_COMPLETION_TIMEOUT_MS: 1000,
      getThreadInterruptible: vi.fn(() => false),
      displayName: (item) => item?.name || item?.id || '',
    };
    const actions = createThreadActions(state, deps);

    await actions.setThreadArchived('thread-1', false);

    expect(deps.logWarn).toHaveBeenCalledWith('thread', 'unarchive.partial_warning', expect.objectContaining({ thread_id: 'thread-1', partial: true }));
    expect(globalThis.window.alert).toHaveBeenCalledWith('restore skipped');
  });
});
