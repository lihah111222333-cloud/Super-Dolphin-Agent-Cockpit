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
});
