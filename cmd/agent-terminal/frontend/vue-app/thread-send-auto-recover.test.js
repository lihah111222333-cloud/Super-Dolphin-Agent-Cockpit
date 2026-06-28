// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({ callAPI: vi.fn() }));
const logMock = vi.hoisted(() => ({ logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn() }));

vi.mock('./services/api.js', () => ({ callAPI: apiMock.callAPI }));
vi.mock('./services/log.js', () => ({ logDebug: logMock.logDebug, logInfo: logMock.logInfo, logWarn: logMock.logWarn }));

import { sendMessage, recoverThread } from './stores/thread-actions-helpers.js';

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

describe('sendMessage auto-recover', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    logMock.logInfo.mockReset();
    logMock.logWarn.mockReset();
  });

  it('auto-recovers and retries turn/start when session is not available', async () => {
    let turnStartCallCount = 0;
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'turn/start') {
        turnStartCallCount++;
        if (turnStartCallCount === 1) {
          // First call: session not available (simulates stale session after app restart)
          const err = new Error('[-31002] thread session is not available; start or resume the thread first');
          err.cause = { code: -31002, message: 'thread session is not available; start or resume the thread first' };
          throw err;
        }
        // Second call (after recover): success
        return {};
      }
      if (method === 'thread/recover') return { recovered: true, mode: 'relaunch_resume' };
      if (method === 'ui/state/get') return { threads: [], statuses: {}, timelinesByThread: {} };
      return {};
    });

    const ctx = buildCtx();
    await sendMessage(ctx, 'thread-stale', 'hello');

    // Must have called thread/recover between first and second turn/start
    const calls = apiMock.callAPI.mock.calls.map(([method]) => method);
    expect(calls.filter((m) => m === 'turn/start')).toHaveLength(2);
    expect(calls).toContain('thread/recover');

    // thread/recover must be called before the second turn/start
    const recoverIndex = calls.indexOf('thread/recover');
    const secondTurnStart = calls.indexOf('turn/start', calls.indexOf('turn/start') + 1);
    expect(recoverIndex).toBeLessThan(secondTurnStart);
  });
});
