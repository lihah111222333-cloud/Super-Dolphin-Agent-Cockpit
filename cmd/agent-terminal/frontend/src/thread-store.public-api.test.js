// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { useThreadStore } from './stores/threads.js';

const PUBLIC_API_KEYS = [
  'compactThread',
  'clearThreadSendBlockedNotice',
  'displayName',
  'forceCompleteThread',
  'getCmdCardCols',
  'getCurrentThreadId',
  'getLayout',
  'getPreferenceScopeCwd',
  'getSplitRatio',
  'getThreadConfig',
  'getThreadActivityStats',
  'getThreadAlerts',
  'getThreadArchivedAt',
  'getThreadCompactResult',
  'getThreadCompactSuccessCount',
  'getThreadCompacting',
  'getThreadDiff',
  'getThreadInterruptible',
  'getThreadPinnedAt',
  'getThreadRailWidth',
  'getThreadSendBlockedNotice',
  'getThreadStatus',
  'getThreadStatusDetails',
  'getThreadStatusHeader',
  'getThreadTimeline',
  'getThreadTokenUsage',
  'getThreadsByMode',
  'handleAgentEvent',
  'handleBridgeEvent',
  'isThreadSendBlocked',
  'loadMessages',
  'markHistoryLoaded',
  'promptRenameThread',
  'recoverThread',
  'refreshSidebarState',
  'renameThread',
  'saveActiveCmdThread',
  'saveActiveThread',
  'sendMessage',
  'setCmdCardCols',
  'setLayout',
  'setThreadCompactResult',

  'setPreferenceScopeCwd',
  'setScrollGuard',
  'setSplitRatio',
  'setThreadConfig',
  'setThreadArchived',
  'setThreadPinned',
  'setThreadRailWidth',
  'shouldReloadThreadHistory',
  'startThread',
  'state',
  'stopThread',
  'syncThreadDiffState',
  'syncThreadState',
  'toggleThreadArchive',
  'toggleThreadPin',
  'batchDeleteStaleThreads',
].sort();

describe('thread store public api', () => {
  it('keeps useThreadStore public field set stable', () => {
    expect(Object.keys(useThreadStore()).sort()).toEqual(PUBLIC_API_KEYS);
  });
});
