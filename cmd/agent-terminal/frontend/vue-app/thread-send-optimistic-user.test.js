// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({ callAPI: vi.fn() }));
const logMock = vi.hoisted(() => ({ logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn() }));

vi.mock('./services/api.js', () => ({ callAPI: apiMock.callAPI }));
vi.mock('./services/log.js', () => ({ logDebug: logMock.logDebug, logInfo: logMock.logInfo, logWarn: logMock.logWarn }));

import { sendMessage } from './stores/thread-actions-helpers.js';

function buildCtx(overrides = {}) {
  return {
    callAPI: apiMock.callAPI,
    logInfo: logMock.logInfo,
    logWarn: logMock.logWarn,
    state: { timelinesByThread: {} },
    syncThreadState: vi.fn(async () => {}),
    loadMessages: vi.fn(async () => {}),
    syncRuntimeState: vi.fn(async () => {}),
    threadHistoryLoadedAtByThread: new Map(),
    messageLoadPromiseByThread: new Map(),
    ...overrides,
  };
}

describe('sendMessage optimistic user message', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset().mockResolvedValue({});
    logMock.logInfo.mockReset();
    logMock.logWarn.mockReset();
  });

  it('inserts an optimistic user message into the timeline immediately after turn/start', async () => {
    const ctx = buildCtx();

    await sendMessage(ctx, 'thread-1', 'hello world');

    // After sendMessage, the timeline should contain a user message item
    // even though loadMessages returned nothing.
    const timeline = ctx.state.timelinesByThread['thread-1'];
    expect(Array.isArray(timeline)).toBe(true);
    const userItems = timeline.filter((item) => item.kind === 'user');
    expect(userItems.length).toBeGreaterThan(0);
    expect(userItems[0].text).toBe('hello world');
  });
});
