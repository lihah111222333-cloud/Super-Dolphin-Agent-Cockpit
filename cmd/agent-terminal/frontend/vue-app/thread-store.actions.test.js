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
import {
  cancelCompactWaiter,
  compactPendingByThread,
  compactResultByThread,
  compactSuccessCountByThread,
  compactWaitersByThread,
} from './stores/thread-compact.js';


function buildSnapshot({
  threadId = 'thread-live',
  threadName = threadId,
  status = 'idle',
  activeThreadId = threadId,
  tokenUsageByThread = {},
} = {}) {
  return {
    threads: [{ id: threadId, name: threadName, state: status }],
    statuses: { [threadId]: status },
    interruptibleByThread: { [threadId]: status !== 'idle' },
    statusHeadersByThread: { [threadId]: '' },
    statusDetailsByThread: { [threadId]: '' },
    timelinesByThread: { [threadId]: [] },
    diffTextByThread: {},
    diffRevisionByThread: { [threadId]: 0 },
    tokenUsageByThread,
    agentMetaById: {},
    agentRuntimeById: {},
    activityStatsByThread: {},
    alertsByThread: {},
    skillRevision: 0,
    activeThreadId,
    activeCmdThreadId: '',

  };
}

function resetThreadStore(store) {
  store.setPreferenceScopeCwd('');
  Object.assign(store.state, {
    activeThreadId: '',
    activeCmdThreadId: '',

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
  for (const key of Object.keys(compactPendingByThread)) delete compactPendingByThread[key];
  for (const key of Object.keys(compactResultByThread)) delete compactResultByThread[key];
  for (const key of Object.keys(compactSuccessCountByThread)) delete compactSuccessCountByThread[key];
  compactWaitersByThread.clear();
}

async function flushAsync() {
  await Promise.resolve();
  await Promise.resolve();
}

describe('thread store actions', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    logMock.logDebug.mockReset();
    logMock.logInfo.mockReset();
    logMock.logWarn.mockReset();
    globalThis.window = { ...(globalThis.window || {}), alert: vi.fn() };
    resetThreadStore(useThreadStore());
  });

  it('starts a thread and preserves the public side effects', async () => {
    const store = useThreadStore();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.activePromptKey') return '';
        return 'claude-3.7-sonnet';
      }
      if (method === 'thread/start') return { thread: { id: 'thread-new' } };
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-new', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    const id = await store.startThread('/repo', { focusMode: 'chat' });
    await flushAsync();

    expect(id).toBe('thread-new');
    expect(store.state.activeThreadId).toBe('thread-new');
    expect(store.state.threads.some((item) => item.id === 'thread-new')).toBe(true);
    expect(apiMock.callAPI).toHaveBeenCalledWith('thread/start', { cwd: '/repo', modelProvider: 'claude-3.7-sonnet' });
  });

  it('forwards explicit config payload when starting a thread', async () => {
    const store = useThreadStore();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.activePromptKey') return '';
        return 'codex';
      }
      if (method === 'thread/start') return { thread: { id: 'thread-task' } };
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-task', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {
      config: {
        taskId: 'task-demo',
        handoffFile: 'handoff/tasks/task-demo.md',
        continueTask: true,
      },
    });
    await flushAsync();

    expect(apiMock.callAPI).toHaveBeenCalledWith('thread/start', {
      cwd: '/repo',
      modelProvider: 'codex',
      config: {
        taskId: 'task-demo',
        handoffFile: 'handoff/tasks/task-demo.md',
        continueTask: true,
      },
    });
  });

  it('forwards explicit name and base instructions when starting a thread', async () => {
    const store = useThreadStore();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.activePromptKey') return '';
        return 'codex';
      }
      if (method === 'thread/start') return { thread: { id: 'thread-seeded' } };
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-seeded', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {
      name: 'Memory Center Refactor · 新任务',
      baseInstructions: '来源任务：Memory Center Refactor',
      config: {
        taskTitle: 'Memory Center Refactor · 新任务',
        autoTaskHandoff: true,
      },
    });
    await flushAsync();

    expect(apiMock.callAPI).toHaveBeenCalledWith('thread/start', {
      cwd: '/repo',
      modelProvider: 'codex',
      name: 'Memory Center Refactor · 新任务',
      baseInstructions: '来源任务：Memory Center Refactor',
      config: {
        taskTitle: 'Memory Center Refactor · 新任务',
        autoTaskHandoff: true,
      },
    });
  });

  it('startThread reads cwd-scoped activePromptKey preference and forwards prompt_key', async () => {
    const store = useThreadStore();
    const prefCalls = [];
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        prefCalls.push(payload);
        if (payload?.key === 'settings.activePromptKey') return 'main/launch-fav';
        return 'codex';
      }
      if (method === 'thread/start') return { thread: { id: 'thread-pinned' } };
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-pinned', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo-x', {});
    await flushAsync();

    expect(prefCalls).toContainEqual({ key: 'settings.activePromptKey', cwd: '/repo-x' });
    expect(apiMock.callAPI).toHaveBeenCalledWith('thread/start', {
      cwd: '/repo-x',
      modelProvider: 'codex',
      prompt_key: 'main/launch-fav',
    });
  });

  it('explicit options.promptKey wins over the persisted preference', async () => {
    const store = useThreadStore();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.activePromptKey') return 'main/should-be-ignored';
        return 'codex';
      }
      if (method === 'thread/start') return { thread: { id: 'thread-pinned-explicit' } };
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-pinned-explicit', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', { promptKey: 'main/explicit-pin' });
    await flushAsync();

    expect(apiMock.callAPI).toHaveBeenCalledWith('thread/start', {
      cwd: '/repo',
      modelProvider: 'codex',
      prompt_key: 'main/explicit-pin',
    });
  });

  it('explicit options.agentKey suppresses the preference lookup', async () => {
    const store = useThreadStore();
    let activePromptLookups = 0;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.activePromptKey') {
          activePromptLookups += 1;
          return 'main/should-be-ignored';
        }
        return 'codex';
      }
      if (method === 'thread/start') return { thread: { id: 'thread-pinned-by-agent' } };
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-pinned-by-agent', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', { agentKey: 'sql_expert' });
    await flushAsync();

    expect(activePromptLookups).toBe(0);
    expect(apiMock.callAPI).toHaveBeenCalledWith('thread/start', {
      cwd: '/repo',
      modelProvider: 'codex',
      agent_key: 'sql_expert',
    });
  });

  it('gets and sets thread config via dedicated backend RPCs', async () => {
    const store = useThreadStore();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'thread/config/get') {
        return {
          threadId: payload.threadId,
          provider: 'codex',
          supportsThreadOverride: true,
          override: { model: '', effort: '' },
          effective: { model: 'gpt-5.4', effort: 'xhigh' },
        };
      }
      if (method === 'thread/config/set') {
        return {
          threadId: payload.threadId,
          provider: 'codex',
          supportsThreadOverride: true,
          override: { model: payload.model, effort: payload.effort },
          effective: { model: payload.model || 'gpt-5.4', effort: payload.effort || 'xhigh' },
        };
      }
      return {};
    });

    const got = await store.getThreadConfig('thread-live');
    const saved = await store.setThreadConfig('thread-live', { model: 'gpt-5.2', effort: 'high' });

    expect(apiMock.callAPI).toHaveBeenNthCalledWith(1, 'thread/config/get', { threadId: 'thread-live' });
    expect(apiMock.callAPI).toHaveBeenNthCalledWith(2, 'thread/config/set', {
      threadId: 'thread-live',
      model: 'gpt-5.2',
      effort: 'high',
    });
    expect(got.effective.model).toBe('gpt-5.4');
	    expect(saved.override.effort).toBe('high');
	    expect(logMock.logInfo).toHaveBeenCalledWith('thread', 'config.get.start', { thread_id: 'thread-live', cwd: '' });
	    expect(logMock.logInfo).toHaveBeenCalledWith('thread', 'config.set.start', {
	      thread_id: 'thread-live',
	      cwd: '',
	      requested_model: 'gpt-5.2',
	      requested_effort: 'high',
	    });
	    expect(logMock.logInfo).toHaveBeenCalledWith('thread', 'config.set.done', expect.objectContaining({
	      thread_id: 'thread-live',
	      provider: 'codex',
	      effective_model: 'gpt-5.2',
	      effective_effort: 'high',
	    }));
  });

  it('sends message payloads with attachments and skill options', async () => {
    const store = useThreadStore();
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'turn/start') return {};
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-live', activeThreadId: 'thread-live' });
      if (method === 'thread/messages') {
        return {
          messages: [
            { id: 1, role: 'user', content: 'hello', createdAt: '2026-03-10T12:00:00Z' },
          ],
        };
      }
      return {};
    });

    await store.sendMessage('thread-live', 'hello', [{ path: '/tmp/a.txt' }], {
      cwd: '/repo',
      selectedSkills: ['git'],
      manualSkillSelection: true,
    });

    expect(apiMock.callAPI).toHaveBeenCalledWith('turn/start', {
      threadId: 'thread-live',
      input: [
        { type: 'text', text: 'hello' },
        { type: 'mention', name: 'a.txt', path: '/tmp/a.txt' },
      ],
      cwd: '/repo',
      selectedSkills: ['git'],
      manualSkillSelection: true,
    });
    // sendMessage no longer calls loadMessages eagerly (event-driven hydration).
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('thread/messages', expect.anything());
  });

  it('waits for compact completion via bridge-event envelope and stores success result', async () => {
    const store = useThreadStore();
    store.state.activeThreadId = 'thread-live';
    store.state.threads = [{ id: 'thread-live', name: 'thread-live', state: 'idle' }];
    store.state.tokenUsageByThread = { 'thread-live': { usedTokens: 100, contextWindowTokens: 1000, usedPercent: 0.1 } };
    store.state.timelinesByThread = {
      'thread-live': [{ id: 'thread-live-history-1', kind: 'user', text: '旧请求', ts: '2026-03-10T12:00:00Z' }],
    };
    let messageLoadCount = 0;
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/state/get') {
        return { ...buildSnapshot({ threadId: 'thread-live', activeThreadId: 'thread-live', tokenUsageByThread: { 'thread-live': { usedTokens: 80, contextWindowTokens: 1000, usedPercent: 0.08 } } }), timelinesByThread: { 'thread-live': store.state.timelinesByThread['thread-live'] } };
      }
      if (method === 'thread/messages') {
        messageLoadCount += 1;
        return messageLoadCount === 1
          ? { messages: [{ id: 1, role: 'user', content: '旧请求', createdAt: '2026-03-10T12:00:00Z' }] }
          : { messages: [{ id: 1, role: 'user', content: '旧请求', createdAt: '2026-03-10T12:00:00Z' }, { id: 2, role: 'assistant', content: '压缩后的摘要', createdAt: '2026-03-10T12:00:01Z' }] };
      }
      if (method === 'thread/compact/start') {
        queueMicrotask(() => {
          store.handleBridgeEvent({ type: 'thread/compacted', payload: { threadId: 'thread-live' } });
        });
        return {};
      }
      return {};
    });

    const compactPromise = store.compactThread('thread-live');
    try {
      const outcome = await Promise.race([
        compactPromise.then(() => 'resolved', () => 'rejected'),
        new Promise((resolve) => setTimeout(() => resolve('pending'), 50)),
      ]);
      expect(outcome).toBe('pending');
      await compactPromise;
    } finally {
      cancelCompactWaiter('thread-live', 'test_cleanup');
      await compactPromise.catch(() => {});
    }

    expect(store.getThreadCompacting('thread-live')).toBe(false);
    expect(store.getThreadCompactResult('thread-live')?.status).toBe('success');
    expect(messageLoadCount).toBeGreaterThanOrEqual(2);
    expect(apiMock.callAPI).toHaveBeenCalledWith('thread/messages', { threadId: 'thread-live', limit: 300 });
    expect(store.getThreadTimeline('thread-live').map((item) => item.kind)).toEqual(['user', 'assistant']);
  });

  it('archives threads through backend and refreshes sidebar state', async () => {
    const store = useThreadStore();
    store.setPreferenceScopeCwd('/repo');
    store.state.threads = [{ id: 'thread-live', name: 'thread-live', state: 'idle' }];
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'thread/archive') return { archived: true };
      if (method === 'ui/sidebar/get') return buildSnapshot({ threadId: 'thread-live', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.setThreadArchived('thread-live', true);
    await flushAsync();

    expect(apiMock.callAPI).toHaveBeenCalledWith('thread/archive', { threadId: 'thread-live' });
    expect(store.getThreadArchivedAt('thread-live')).toBeGreaterThan(0);
  });

  it('shows partial archive warnings returned by backend', async () => {
    const store = useThreadStore();
    store.setPreferenceScopeCwd('/repo');
    store.state.threads = [{ id: 'thread-live', name: 'thread-live', state: 'idle' }];
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'thread/archive') return { partial: true, warnings: ['copy artifact failed'], skippedCount: 1 };
      if (method === 'ui/sidebar/get') return buildSnapshot({ threadId: 'thread-live', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.setThreadArchived('thread-live', true);
    await flushAsync();

    expect(logMock.logWarn).toHaveBeenCalledWith('thread', 'archive.partial_warning', expect.objectContaining({ thread_id: 'thread-live', partial: true, skipped_count: 1 }));
    expect(globalThis.window.alert).toHaveBeenCalledWith('copy artifact failed');
  });

  it('toggles thread pin and persists the pin map', async () => {
    const store = useThreadStore();
    store.setPreferenceScopeCwd('/repo');
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/preferences/set') return {};
      if (method === 'ui/state/get') return {};
      return {};
    });

    await store.toggleThreadPin('thread-live');
    await flushAsync();

    expect(store.getThreadPinnedAt('thread-live')).toBeGreaterThan(0);
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/preferences/set', {
      key: 'threadPins.chat',
      value: { 'thread-live': store.getThreadPinnedAt('thread-live') },
      cwd: '/repo',
    });
  });
});
