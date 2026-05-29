// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

const logMock = vi.hoisted(() => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./services/log.js', () => ({
  logDebug: logMock.logDebug,
  logInfo: logMock.logInfo,
  logWarn: logMock.logWarn,
}));

import { useThreadStore } from './stores/threads.js';

async function flushAsync() {
  await Promise.resolve();
  await Promise.resolve();
}

describe('thread store stale prompt cleanup', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    logMock.logDebug.mockReset();
    logMock.logInfo.mockReset();
    logMock.logWarn.mockReset();
    const store = useThreadStore();
    Object.assign(store.state, {
      promptStaleNotice: '',
      sendBlockedNoticesByThread: {},
      sendHoldNoticesByThread: {},
      threads: [],
      timelinesByThread: {},
      kickoffByThread: {},
    });
  });

  it('clears the activePromptKey pref when turn/start reports a 0105 legacy prompt key stale', async () => {
    const store = useThreadStore();
    const prefSetCalls = [];
    store.state.threads = [{ id: 'thread-pending-stale', name: 'thread-pending-stale', state: 'idle' }];
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'turn/start') {
        return {
          turn_id: 'turn-stale',
          prompt_key: 'main/claude-style-zh',
          prompt_key_stale: true,
        };
      }
      if (method === 'ui/preferences/set') {
        prefSetCalls.push(payload);
        return {};
      }
      return {};
    });

    await store.sendMessage('thread-pending-stale', 'hello', [], { cwd: '/repo-stale-turn' });
    await flushAsync();

    expect(prefSetCalls).toContainEqual({
      key: 'settings.activePromptKey',
      value: '',
      cwd: '/repo-stale-turn',
    });
    expect(store.state.promptStaleNotice).toMatch(/已自动取消激活/);
  });
});
