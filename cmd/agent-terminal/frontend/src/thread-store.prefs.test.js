// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { useThreadStore } from './stores/threads.js';

function resetThreadStore(store) {
  store.setPreferenceScopeCwd('');
  Object.assign(store.state, {
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
  });
}

async function flushAsync(times = 6) {
  for (let index = 0; index < times; index += 1) {
    await Promise.resolve();
  }
  await new Promise((resolve) => setTimeout(resolve, 0));
}

describe('thread store prefs', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    resetThreadStore(useThreadStore());
  });

  it('reads normalized persisted layout prefs and scope cwd', () => {
    const store = useThreadStore();
    store.setPreferenceScopeCwd('/repo/');
    store.state.viewPrefsChat = { layout: 'mix', splitRatio: 74.6, threadRailWidth: 999 };
    store.state.viewPrefsCmd = { layout: 'overview', splitRatio: 29, cardCols: 2 };

    expect(store.getPreferenceScopeCwd()).toBe('/repo');
    expect(store.getLayout('chat')).toBe('mix');
    expect(store.getSplitRatio('chat')).toBe(75);
    expect(store.getThreadRailWidth()).toBe(420);
    expect(store.getLayout('cmd')).toBe('overview');
    expect(store.getSplitRatio('cmd')).toBe(30);
    expect(store.getCmdCardCols()).toBe(2);
  });

  it('persists chat prefs with scoped cwd and schedules runtime sync', async () => {
    const store = useThreadStore();
    store.setPreferenceScopeCwd('/repo');
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/preferences/set') return {};
      if (method === 'ui/state/get') return {};
      return {};
    });

    store.setLayout('chat', 'mix');
    store.setSplitRatio('chat', 73.8);
    store.setThreadRailWidth(410);
    await flushAsync();
    await flushAsync();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'viewPrefs.chat',
      value: { layout: 'mix', splitRatio: 60, threadRailWidth: 232 },
      cwd: '/repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'viewPrefs.chat',
      value: { layout: 'focus', splitRatio: 74, threadRailWidth: 232 },
      cwd: '/repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'viewPrefs.chat',
      value: { layout: 'focus', splitRatio: 60, threadRailWidth: 410 },
      cwd: '/repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/state/get', {
      threadId: '',
      includeDiff: false,
      cwd: '/repo',
    });
  });

  it('persists cmd prefs and card cols with scoped cwd', async () => {
    const store = useThreadStore();
    store.setPreferenceScopeCwd('/repo');
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/preferences/set') return {};
      if (method === 'ui/state/get') return {};
      return {};
    });

    store.setLayout('cmd', 'overview');
    store.setSplitRatio('cmd', 61.2);
    store.setCmdCardCols(2);
    await flushAsync();
    await flushAsync();

    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'viewPrefs.cmd',
      value: { layout: 'overview', splitRatio: 60, cardCols: 3 },
      cwd: '/repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'viewPrefs.cmd',
      value: { layout: 'mix', splitRatio: 61, cardCols: 3 },
      cwd: '/repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'viewPrefs.cmd',
      value: { layout: 'mix', splitRatio: 60, cardCols: 2 },
      cwd: '/repo',
    });
  });
});
