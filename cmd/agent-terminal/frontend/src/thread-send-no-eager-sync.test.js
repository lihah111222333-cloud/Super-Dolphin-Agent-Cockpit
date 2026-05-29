// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({ callAPI: vi.fn() }));
const logMock = vi.hoisted(() => ({ logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn() }));

vi.mock('./services/api.js', () => ({ callAPI: apiMock.callAPI }));
vi.mock('./services/log.js', () => ({ logDebug: logMock.logDebug, logInfo: logMock.logInfo, logWarn: logMock.logWarn }));

import { sendMessage } from './stores/thread-actions-helpers.js';

describe('sendMessage does not eagerly sync after turn/start', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset().mockResolvedValue({});
    logMock.logInfo.mockReset();
    logMock.logWarn.mockReset();
  });

  it('does not call syncThreadState immediately after turn/start (would overwrite optimistic message)', async () => {
    const syncThreadState = vi.fn(async () => {});
    const loadMessages = vi.fn(async () => {});
    const ctx = {
      callAPI: apiMock.callAPI,
      logInfo: logMock.logInfo,
      logWarn: logMock.logWarn,
      state: { timelinesByThread: {} },
      syncThreadState,
      loadMessages,
      syncRuntimeState: vi.fn(async () => {}),
      threadHistoryLoadedAtByThread: new Map(),
      messageLoadPromiseByThread: new Map(),
    };

    await sendMessage(ctx, 'thread-1', 'hello');

    // syncThreadState and loadMessages should NOT be called inside sendMessage.
    // They overwrite the optimistic user message with backend data that doesn't
    // have the user's text yet. Refreshing is handled by event-driven hydration.
    expect(syncThreadState).not.toHaveBeenCalled();
    expect(loadMessages).not.toHaveBeenCalled();
  });

  it('records a local send block when turn/start rejects', async () => {
    const ctx = {
      callAPI: apiMock.callAPI,
      logInfo: logMock.logInfo,
      logWarn: logMock.logWarn,
      state: {
        statuses: { 'thread-1': 'idle', 'thread-2': 'idle' },
        sendBlockedNoticesByThread: {},
        sendHoldNoticesByThread: {},
        timelinesByThread: {},
      },
      syncThreadState: vi.fn(async () => {}),
      loadMessages: vi.fn(async () => {}),
      syncRuntimeState: vi.fn(async () => {}),
      threadHistoryLoadedAtByThread: new Map(),
      messageLoadPromiseByThread: new Map(),
    };
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'turn/start') throw new Error('backend boom');
      return {};
    });

    await expect(sendMessage(ctx, 'thread-1', 'hello')).rejects.toThrow('backend boom');

    expect(ctx.state.statuses).toEqual({ 'thread-1': 'idle', 'thread-2': 'idle' });
    expect(ctx.state.sendBlockedNoticesByThread['thread-1']).toContain('backend boom');
    expect(ctx.state.sendBlockedNoticesByThread['thread-2']).toBeUndefined();

    ctx.state.statuses = { 'thread-1': 'idle', 'thread-2': 'idle' };
    expect(ctx.state.sendBlockedNoticesByThread['thread-1']).toContain('backend boom');

    apiMock.callAPI.mockClear();
    await expect(sendMessage(ctx, 'thread-1', 'again')).rejects.toThrow('backend boom');
    expect(apiMock.callAPI).not.toHaveBeenCalled();
  });

  it('holds the thread when turn/start succeeds but the send pipeline later fails', async () => {
    const ctx = {
      callAPI: apiMock.callAPI,
      logInfo: logMock.logInfo,
      logWarn: logMock.logWarn,
      state: {
        sendBlockedNoticesByThread: {},
        sendHoldNoticesByThread: {},
        timelinesByThread: {},
      },
      syncThreadState: vi.fn(async () => {}),
      loadMessages: vi.fn(async () => {}),
      syncRuntimeState: vi.fn(async () => { throw new Error('local sync failed'); }),
      threadHistoryLoadedAtByThread: new Map(),
      messageLoadPromiseByThread: new Map(),
    };
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'turn/start') return { agent_key: 'agent-a' };
      return {};
    });

    await expect(sendMessage(ctx, 'thread-1', 'hello')).rejects.toThrow('local sync failed');

    expect(ctx.state.sendBlockedNoticesByThread['thread-1']).toBeUndefined();
    expect(ctx.state.sendHoldNoticesByThread['thread-1']).toContain('local sync failed');

    apiMock.callAPI.mockClear();
    ctx.syncRuntimeState.mockResolvedValueOnce(undefined);
    await expect(sendMessage(ctx, 'thread-1', 'again')).rejects.toThrow('local sync failed');
    expect(apiMock.callAPI).not.toHaveBeenCalled();
  });
});
