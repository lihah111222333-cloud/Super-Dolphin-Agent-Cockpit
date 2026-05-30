// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({ callAPI: vi.fn() }));
const logMock = vi.hoisted(() => ({ logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn() }));

vi.mock('./services/api.js', () => ({ callAPI: apiMock.callAPI }));
vi.mock('./services/log.js', () => ({
  logDebug: logMock.logDebug,
  logInfo: logMock.logInfo,
  logWarn: logMock.logWarn,
}));

import { useThreadStore } from './stores/threads.js';

function buildSnapshot(threadId = 'thread-skill-source') {
  return {
    threads: [{ id: threadId, name: threadId, state: 'idle' }],
    statuses: { [threadId]: 'idle' },
    interruptibleByThread: { [threadId]: false },
    statusHeadersByThread: { [threadId]: '' },
    statusDetailsByThread: { [threadId]: '' },
    timelinesByThread: { [threadId]: [] },
    diffTextByThread: {},
    diffRevisionByThread: { [threadId]: 0 },
    tokenUsageByThread: {},
    agentMetaById: {},
    agentRuntimeById: {},
    activityStatsByThread: {},
    alertsByThread: {},
    skillRevision: 0,
    activeThreadId: '',
    activeCmdThreadId: '',
  };
}

function resetThreadStore(store) {
  Object.assign(store.state, {
    activeThreadId: '',
    activeCmdThreadId: '',
    sendBlockedNoticesByThread: {},
    sendHoldNoticesByThread: {},
    threads: [],
    statuses: {},
    interruptibleByThread: {},
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

describe('thread store skill source payload', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    logMock.logDebug.mockReset();
    logMock.logInfo.mockReset();
    logMock.logWarn.mockReset();
    resetThreadStore(useThreadStore());
  });

  it('preserves launch selected skill ref source in thread/start payload', async () => {
    const store = useThreadStore();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active') return 'codex';
        return undefined;
      }
      if (method === 'config/builtinTools/read') return {};
      if (method === 'thread/start') return { thread: { id: 'thread-skill-source' } };
      if (method === 'ui/state/get') return buildSnapshot();
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {
      selectedSkillRefs: [
        { key: 'project::planner:/repo/.agent/skills/planner', name: 'planner', scope: 'project', path: '/repo/.agent/skills/planner', source: 'manual' },
      ],
      manualSkillSelection: true,
    });

    expect(apiMock.callAPI).toHaveBeenCalledWith('thread/start', expect.objectContaining({
      cwd: '/repo',
      modelProvider: 'codex',
      selectedSkillRefs: [
        { key: 'project::planner:/repo/.agent/skills/planner', name: 'planner', scope: 'project', personalType: '', path: '/repo/.agent/skills/planner', source: 'manual' },
      ],
    }));
    const [, startPayload] = apiMock.callAPI.mock.calls.find(([method]) => method === 'thread/start');
    expect(startPayload.config).not.toHaveProperty('codexHome');
    expect(startPayload.config).not.toHaveProperty('codexModelProvider');
  });
});
