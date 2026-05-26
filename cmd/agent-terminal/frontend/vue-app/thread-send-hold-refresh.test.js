// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({ callAPI: vi.fn() }));

vi.mock('./services/api.js', () => ({ callAPI: apiMock.callAPI }));
vi.mock('./services/log.js', () => ({ logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn() }));

import { useThreadStore } from './stores/threads.js';

function sidebarSnapshot() {
  return {
    threads: [{ id: 'thread-live', name: 'Live', state: 'idle' }],
    statuses: { 'thread-live': 'idle' },
    interruptibleByThread: { 'thread-live': false },
    statusHeadersByThread: { 'thread-live': '等待指示' },
    statusDetailsByThread: { 'thread-live': '' },
    timelinesByThread: {},
    diffTextByThread: {},
    diffRevisionByThread: { 'thread-live': 0 },
    tokenUsageByThread: {},
    agentMetaById: {},
    agentRuntimeById: {},
    activityStatsByThread: {},
    alertsByThread: {},
    skillRevision: 0,
    activeThreadId: 'thread-live',
    activeCmdThreadId: '',
  };
}

describe('thread send hold refresh', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    const store = useThreadStore();
    store.state.sendHoldNoticesByThread = {};
  });

  it('clears temporary send holds after sidebar refresh succeeds', async () => {
    const store = useThreadStore();
    store.state.sendHoldNoticesByThread = { 'thread-live': '发送状态同步失败：local sync failed。请刷新会话状态后继续。' };
    apiMock.callAPI.mockResolvedValueOnce(sidebarSnapshot());

    await store.refreshSidebarState();

    expect(store.state.sendHoldNoticesByThread).toEqual({});
  });
});
