// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({ callAPI: vi.fn() }));
const logMock = vi.hoisted(() => ({ logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn() }));

vi.mock('./services/api.js', () => ({ callAPI: apiMock.callAPI }));
vi.mock('./services/log.js', () => ({ logDebug: logMock.logDebug, logInfo: logMock.logInfo, logWarn: logMock.logWarn }));

import { handleBridgeEvent } from './stores/thread-sync-helpers.js';

function buildCtx(overrides = {}) {
  return {
    callAPI: apiMock.callAPI,
    logInfo: logMock.logInfo,
    logWarn: logMock.logWarn,
    logDebug: logMock.logDebug,
    state: {
      activeThreadId: 'thread-OTHER',  // different from event thread
      activeCmdThreadId: '',
      timelinesByThread: {},
      tokenUsageByThread: {},
      skillRevision: 0,
    },
    syncRuntimeState: vi.fn(async () => ({})),
    syncThreadState: vi.fn(async () => ({})),
    loadMessages: vi.fn(async () => ({})),
    syncThreadDiffState: vi.fn(async () => ({})),
    saveScrollPosition: vi.fn(),
    restoreScrollPosition: vi.fn(),
    applyRuntimeSnapshot: vi.fn(),
    normalizeProviderThreadID: (v) => v || '',
    getPreferenceScopeCwd: () => '',
    withPreferenceScope: (payload) => ({ ...payload }),
    markLocalActiveThreadDirty: vi.fn(),
    persistPreferenceAndSync: vi.fn(),
    runtimeSyncPromise: null,
    runtimeSyncPending: false,
    runtimeSnapshotRequestSeq: 0,
    latestRuntimeSnapshotRequestSeqByScope: new Map(),
    threadHistoryLoadedAtByThread: new Map(),
    threadHistoryProviderThreadIDByThread: new Map(),
    threadDiffLoadedRevisionByThread: new Map(),
    threadStateSyncPromiseByThread: new Map(),
    threadStateSyncPendingByThread: new Map(),
    messageLoadPromiseByThread: new Map(),
    threadPatchSeqByThread: new Map(),
    threadPatchMetaByThread: new Map(),
    compactWaitersByThread: new Map(),
    getCompactWaiter: () => null,
    settleCompactWaiter: () => false,
    setCompactResult: vi.fn(),
    sidebarSyncThrottleLastRun: 0,
    SIDEBAR_SYNC_THROTTLE_MS: 1000,
    syncThrottleLastRun: 0,
    SYNC_THROTTLE_MS: 1000,
    THREAD_PATCH_RECENT_WINDOW_MS: 250,
    sidebarSyncDebounceTimer: null,
    syncDebounceTimer: null,
    freezeTimelineItemsAtomic: (items) => ({ changed: items.length > 0, items }),
    ...overrides,
  };
}

describe('thread/messages/page history hydration', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset().mockResolvedValue({});
    logMock.logInfo.mockReset();
    logMock.logWarn.mockReset();
  });

  it('triggers background history sync for thread/messages/page even when not active thread', () => {
    // Simulate: ui/thread/changed with source=thread/messages/page for a thread
    // that is NOT the activeThreadId. This happens when a user sends a message
    // to a claude thread and syncRuntimeState overwrites activeThreadId.
    const ctx = buildCtx();

    handleBridgeEvent(ctx, {
      type: 'ui/thread/changed',
      payload: { source: 'thread/messages/page', threadId: 'thread-CLAUDE' },
    });

    // Should trigger either syncThreadHistoryAtomic or loadMessages for thread-CLAUDE.
    // Check that logInfo was called with a sync signal for thread-CLAUDE.
    const syncCalls = logMock.logInfo.mock.calls.filter(
      ([scope, event, fields]) => scope === 'thread' && event?.includes?.('sync') && fields?.thread_id === 'thread-CLAUDE'
    );
    expect(syncCalls.length).toBeGreaterThan(0);
  });
});
