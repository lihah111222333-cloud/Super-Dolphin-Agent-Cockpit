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

  it('preserves image attachments on optimistic user messages', async () => {
    const ctx = buildCtx();

    await sendMessage(ctx, 'thread-1', '', [{
      kind: 'image',
      name: 'shot.png',
      path: '/tmp/shot.png',
      previewUrl: 'file:///tmp/shot.png',
    }]);

    expect(ctx.state.timelinesByThread['thread-1']).toEqual([expect.objectContaining({
      kind: 'user',
      text: '',
      attachments: [expect.objectContaining({
        kind: 'image',
        name: 'shot.png',
        path: '/tmp/shot.png',
        previewUrl: 'file:///tmp/shot.png',
      })],
    })]);
  });

  it('merges optimistic attachments into an existing matching user item instead of duplicating it', async () => {
    const ctx = buildCtx({
      state: {
        timelinesByThread: {
          'thread-1': [{ id: 'existing-user', kind: 'user', text: 'hello world', ts: '2026-03-10T12:00:00Z' }],
        },
      },
    });

    await sendMessage(ctx, 'thread-1', 'hello world', [{
      kind: 'image',
      name: 'shot.png',
      path: '/tmp/shot.png',
      previewUrl: 'file:///tmp/shot.png',
    }]);

    expect(ctx.state.timelinesByThread['thread-1']).toEqual([expect.objectContaining({
      id: 'existing-user',
      kind: 'user',
      text: 'hello world',
      attachments: [expect.objectContaining({
        kind: 'image',
        name: 'shot.png',
        path: '/tmp/shot.png',
        previewUrl: 'file:///tmp/shot.png',
      })],
    })]);
  });
});
