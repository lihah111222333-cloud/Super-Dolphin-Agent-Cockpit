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

import { withCodexLspToolDefaults } from './stores/codex-lsp-defaults.js';
import { useThreadStore } from './stores/threads.js';
import { cancelCompactWaiter, compactPendingByThread, compactResultByThread, compactSuccessCountByThread, compactWaitersByThread } from './stores/thread-compact.js';


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

function mockStartPreference(payload, {
  provider = 'codex',
  activePromptKey = '',
  model = '',
  effort = '',
  sandbox = undefined,
  codexHome = undefined,
  codexInstanceKey = undefined,
  codexModelProvider = undefined,
} = {}) {
  const key = payload?.key;
  if (key === 'settings.provider.active') return provider;
  if (key === 'settings.activePromptKey') return activePromptKey;
  if (key === 'settings.provider.codex.codexHome') return codexHome;
  if (key === 'settings.provider.codex.codexInstanceKey') return codexInstanceKey;
  if (key === 'settings.provider.codex.codexModelProvider') return codexModelProvider;
  if (typeof key === 'string' && key.startsWith('settings.provider.') && key.endsWith('.sandbox')) return sandbox;
  if (typeof key === 'string' && key.startsWith('settings.provider.') && key.endsWith('.model')) return model;
  if (typeof key === 'string' && key.startsWith('settings.provider.') && key.endsWith('.effort')) return effort;
  return undefined;
}

function codexIdentityConfig(overrides = {}) {
  return { ...overrides };
}

function codexStartConfig(overrides = {}, cwd = '/repo') {
  const config = { ...withCodexLspToolDefaults(codexIdentityConfig(overrides)) };
  const writableRoots = cwd && cwd !== '.' ? [cwd] : [];
  if (!Object.prototype.hasOwnProperty.call(config, 'sandbox')) config.sandbox = { mode: 'workspace-write', writable_roots: writableRoots, network_access: false };
  return config;
}

function resetThreadStore(store) {
  store.setPreferenceScopeCwd('');
  Object.assign(store.state, {
    activeThreadId: '',
    activeCmdThreadId: '',
    promptStaleNotice: '',
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
  for (const key of Object.keys(compactPendingByThread)) delete compactPendingByThread[key];
  for (const key of Object.keys(compactResultByThread)) delete compactResultByThread[key];
  for (const key of Object.keys(compactSuccessCountByThread)) delete compactSuccessCountByThread[key];
  compactWaitersByThread.clear();
}

async function flushAsync() {
  await Promise.resolve();
  await Promise.resolve();
}

async function startThreadUntilSync(store, res, runtime = {}, cwd = '/repo', options = {}) {
  let releaseSync = () => {};
  const syncGate = new Promise((resolve) => { releaseSync = resolve; });
  store.state.agentRuntimeById = runtime;
  apiMock.callAPI.mockImplementation(async (method, payload) => {
    if (method === 'ui/preferences/get') {
      return mockStartPreference(payload, { provider: 'codex' });
    }
    if (method === 'config/builtinTools/read') return {};
    if (method === 'thread/start') return res;
    if (method === 'ui/state/get') { await syncGate; return buildSnapshot({ threadId: res?.thread?.id || 'thread-new', activeThreadId: '' }); }
    if (method === 'ui/preferences/set') return {};
    return {};
  });
  const pending = store.startThread(cwd, options);
  for (let i = 0; i < 10 && !apiMock.callAPI.mock.calls.some(([method]) => method === 'ui/state/get'); i += 1) await flushAsync();
  return { pending, releaseSync };
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
        return mockStartPreference(payload, { provider: 'claude-3.7-sonnet' });
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
    expect(apiMock.callAPI).toHaveBeenCalledWith('thread/start', { cwd: '/repo', provider: 'claude', modelProvider: 'claude-3.7-sonnet' });
  });

  it('scopes an optimistic started thread to its launch cwd before runtime sync returns', async () => {
    const store = useThreadStore();
    store.state.threads = [{ id: 'thread-a', name: 'Repo A', state: 'idle' }];
    store.state.agentRuntimeById = { 'thread-a': { cwd: '/repo-a' } };

    const { pending, releaseSync } = await startThreadUntilSync(store, { thread: { id: 'thread-b' } }, { 'thread-a': { cwd: '/repo-a' } }, '/repo-b');
    const repoAVisibleThreadIds = store.getThreadsByMode('chat', '/repo-a').map((thread) => thread.id);
    const repoBVisibleThreadIds = store.getThreadsByMode('chat', '/repo-b').map((thread) => thread.id);

    releaseSync();
    await pending;

    expect(repoAVisibleThreadIds).toEqual(['thread-a']);
    expect(repoBVisibleThreadIds).toEqual(['thread-b']);
  });

  it('uses the launch name for optimistic started threads before runtime sync returns', async () => {
    const store = useThreadStore();

    const { pending, releaseSync } = await startThreadUntilSync(
      store,
      { thread: { id: 'thread-design' } },
      {},
      '/repo',
      { focusMode: 'chat', name: 'AI 设计流程' },
    );
    const optimistic = store.state.threads.find((item) => item.id === 'thread-design');

    releaseSync();
    await pending;

    expect(optimistic?.name).toBe('AI 设计流程');
  });

  it('forwards explicit provider config payload when starting a thread', async () => {
    const store = useThreadStore();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        return mockStartPreference(payload, { provider: 'codex' });
      }
      if (method === 'thread/start') return { thread: { id: 'thread-task' } };
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-task', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {
      config: {
        sessionFlags: { persistentSubagentDefault: true },
      },
    });
    await flushAsync();

    expect(apiMock.callAPI).toHaveBeenCalledWith('thread/start', {
      cwd: '/repo',
      provider: 'codex', modelProvider: 'codex',
      config: codexStartConfig({
        sessionFlags: { persistentSubagentDefault: true },
      }),
    });
  });

  it('forwards explicit name and base instructions when starting a thread', async () => {
    const store = useThreadStore();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        return mockStartPreference(payload, { provider: 'codex' });
      }
      if (method === 'thread/start') return { thread: { id: 'thread-seeded' } };
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-seeded', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {
      name: 'Memory Center Refactor · 新对话',
      baseInstructions: '来源对话：Memory Center Refactor',
      config: {
        sessionFlags: { seededConversation: true },
      },
    });
    await flushAsync();

    expect(apiMock.callAPI).toHaveBeenCalledWith('thread/start', {
      cwd: '/repo',
      provider: 'codex', modelProvider: 'codex',
      name: 'Memory Center Refactor · 新对话',
      baseInstructions: '来源对话：Memory Center Refactor',
      config: codexStartConfig({
        sessionFlags: { seededConversation: true },
      }),
    });
  });


  it('normalizes object-shaped provider model preferences before thread/start', async () => {
    const store = useThreadStore();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        return mockStartPreference(payload, { provider: 'claude', model: { value: 'sonnet', label: 'Sonnet 4.7' }, effort: 'high' });
      }
      if (method === 'thread/start') return { thread: { id: 'thread-claude' } };
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-claude', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {});
    await flushAsync();

    expect(apiMock.callAPI).toHaveBeenCalledWith('thread/start', expect.objectContaining({
      cwd: '/repo',
      modelProvider: 'claude',
      model: 'sonnet',
      effort: 'high',
    }));
  });

  it('does not forward accidental model artifact strings before thread/start', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        return mockStartPreference(payload, { provider: 'claude', model: '[object Object]', effort: 'high' });
      }
      if (method === 'thread/start') { startPayload = payload; return { thread: { id: 'thread-claude-safe' } }; }
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-claude-safe', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {});
    await flushAsync();

    expect(startPayload.model).toBeUndefined();
    expect(JSON.stringify(startPayload)).not.toContain('[object Object]');
  });

  it('forwards codex identity from provider preferences before thread/start', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        return mockStartPreference(payload, {
          provider: 'codex',
          model: 'gpt-5.5',
          effort: 'xhigh',
          codexHome: '/Users/mac/.codex',
          codexInstanceKey: 'primary',
          codexModelProvider: 'openai-compatible',
        });
      }
      if (method === 'thread/start') { startPayload = payload; return { thread: { id: 'thread-codex-identity' } }; }
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-codex-identity', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {});
    await flushAsync();

    expect(startPayload).toEqual(expect.objectContaining({
      cwd: '/repo',
      modelProvider: 'codex',
      model: 'gpt-5.5',
      effort: 'xhigh',
      config: codexStartConfig({
        codexHome: '/Users/mac/.codex',
        codexInstanceKey: 'primary',
        codexModelProvider: 'openai-compatible',
      }),
    }));
  });

  it('reads Claude builtin tool filtering from the launch cwd when building thread/start config', async () => {
    const store = useThreadStore();
    const builtinReadCalls = [];
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        return mockStartPreference(payload, { provider: 'claude' });
      }
      if (method === 'config/builtinTools/read') {
        builtinReadCalls.push(payload);
        return {
          tools: [
            { id: 'Read', provider: 'claude', enabled: false },
            { id: 'WebFetch', provider: 'claude', enabled: true },
            { id: 'shell', provider: 'codex', enabled: true },
          ],
        };
      }
      if (method === 'thread/start') { startPayload = payload; return { thread: { id: 'thread-filtered' } }; }
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-filtered', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo-project', {});
    await flushAsync();

    expect(builtinReadCalls).toContainEqual({ cwd: '/repo-project' });
    expect(startPayload?.config?.claude_builtin_tools).toEqual(['WebFetch']);
  });

  it('forwards Codex disabled native tools from launch cwd into thread/start config', async () => {
    const store = useThreadStore();
    const builtinReadCalls = [];
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        return mockStartPreference(payload, { provider: 'codex' });
      }
      if (method === 'config/builtinTools/read') {
        builtinReadCalls.push(payload);
        return {
          tools: [
            { id: 'shell', provider: 'codex', enabled: false, enforcement: 'native-hard' },
            { id: 'apply_patch', provider: 'codex', enabled: false, enforcement: 'effect-hard' },
            { id: 'multi_agent', provider: 'codex', enabled: false, enforcement: 'native-hard' },
            { id: 'read_file', provider: 'codex', enabled: true },
            { id: 'WebFetch', provider: 'claude', enabled: true },
          ],
        };
      }
      if (method === 'thread/start') { startPayload = payload; return { thread: { id: 'thread-codex-filtered' } }; }
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-codex-filtered', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo-codex', {});
    await flushAsync();

    expect(builtinReadCalls).toContainEqual({ cwd: '/repo-codex' });
    expect(startPayload?.config?.codexDisabledNativeTools).toEqual(['shell', 'apply_patch', 'multi_agent']);
  });

  it('normalizes object-shaped thread config before thread/config/set', async () => {
    const store = useThreadStore();
    let configPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'thread/config/set') {
        configPayload = payload;
        return {
          threadId: 'thread-1',
          override: { model: payload.model, effort: payload.effort },
          effective: { model: payload.model, effort: payload.effort },
        };
      }
      return {};
    });

    await store.setThreadConfig('thread-1', {
      model: { value: 'sonnet', label: 'Sonnet 4.7' },
      effort: { value: 'high' },
    });

    expect(configPayload).toEqual({ threadId: 'thread-1', model: 'sonnet', effort: 'high' });
  });

  it('startThread reads cwd-scoped activePromptKey preference and forwards prompt_key', async () => {
    const store = useThreadStore();
    const prefCalls = [];
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        prefCalls.push(payload);
        return mockStartPreference(payload, { provider: 'codex', activePromptKey: 'main/launch-fav' });
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
      provider: 'codex', modelProvider: 'codex',
      config: codexStartConfig({}, '/repo-x'),
      prompt_key: 'main/launch-fav',
    });
  });

  it('explicit options.promptKey wins over the persisted preference', async () => {
    const store = useThreadStore();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        return mockStartPreference(payload, { provider: 'codex', activePromptKey: 'main/should-be-ignored' });
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
      provider: 'codex', modelProvider: 'codex',
      config: codexStartConfig(),
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
        }
        return mockStartPreference(payload, { provider: 'codex', activePromptKey: 'main/should-be-ignored' });
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
      provider: 'codex', modelProvider: 'codex',
      config: codexStartConfig(),
      agent_key: 'sql_expert',
    });
  });

  it('ignores stale classifier preference and never forwards use_classifier', async () => {
    const store = useThreadStore();
    let classifierLookups = 0;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.classifierEnabled') {
          classifierLookups += 1;
          return true;
        }
        return mockStartPreference(payload, { provider: 'codex' });
      }
      if (method === 'thread/start') return { thread: { id: 'thread-no-classifier' } };
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-no-classifier', activeThreadId: '' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {});
    await flushAsync();

    const call = apiMock.callAPI.mock.calls.find(([method]) => method === 'thread/start');
    expect(classifierLookups).toBe(0);
    expect(call).toBeDefined();
    expect(call[1]).not.toHaveProperty('use_classifier');
  });

  it('sendMessage inserts optimistic user message BEFORE turn/start resolves', async () => {
    // Regression: when turn/start blocks on first-turn launch work, the
    // optimistic insert USED to happen after the await, so the user's own
    // message didn't render until backend returned. This test pins the correct
    // ordering: timeline contains the user's text while turn/start is still
    // pending.
    const store = useThreadStore();
    const threadId = 'thread-slow-turn-start';
    store.state.threads = [{ id: threadId, name: threadId, state: 'idle' }];
    store.state.timelinesByThread[threadId] = [];

    let releaseTurnStart;
    const turnStartPending = new Promise((resolve) => {
      releaseTurnStart = resolve;
    });

    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'turn/start') {
        await turnStartPending; // simulate first-turn launch latency
        return { ok: true };
      }
      if (method === 'ui/state/get') return buildSnapshot({ threadId, activeThreadId: threadId });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    const sendPromise = store.sendMessage(threadId, 'hello from a slow turn', []);
    // Two microtask ticks are enough for sendMessage to run synchronously up
    // to the `await callAPI('turn/start', ...)` point; the optimistic insert
    // must already have landed by then.
    await flushAsync();

    const timelineMidFlight = store.state.timelinesByThread[threadId] || [];
    const optimisticItem = timelineMidFlight.find((item) => (item?.id || '').includes('-optimistic-user-'));
    expect(optimisticItem).toBeDefined();
    expect(optimisticItem.content || optimisticItem.text || '').toContain('hello from a slow turn');

    releaseTurnStart({ ok: true });
    await sendPromise;
  });

  it.each([
    ['provider', { thread: { id: 'thread-claude' }, provider: 'claude' }, 'claude', true],
    ['modelProvider fallback', { thread: { id: 'thread-codex' }, provider: '', modelProvider: 'codex' }, 'codex', true],
    ['empty provider', { thread: { id: 'thread-empty' }, provider: '', modelProvider: '' }, '', false],
  ])('startThread sync gap: %s', async (_, res, expected, written) => {
    const store = useThreadStore();
    const { pending, releaseSync } = await startThreadUntilSync(store, res);
    try {
      const id = res.thread.id;
      expect(store.state.agentRuntimeById[id]?.cwd).toBe('/repo');
      if (written) expect(store.state.agentRuntimeById[id]?.provider).toBe(expected);
      else expect(store.state.agentRuntimeById[id]).not.toHaveProperty('provider');
    } finally {
      releaseSync();
      await pending;
    }
  });

  it('startThread overwrites an existing runtime provider with fresh response data', async () => {
    const store = useThreadStore();
    const { pending, releaseSync } = await startThreadUntilSync(store, { thread: { id: 'thread-overwrite' }, provider: 'codex' }, { 'thread-overwrite': { provider: 'claude', cwd: '/repo' } });
    try {
      expect(store.state.agentRuntimeById['thread-overwrite']).toEqual(expect.objectContaining({ provider: 'codex', cwd: '/repo' }));
    } finally {
      releaseSync();
      await pending;
    }
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
          effective: { model: 'gpt-5.5', effort: 'xhigh' },
        };
      }
      if (method === 'thread/config/set') {
        return {
          threadId: payload.threadId,
          provider: 'codex',
          supportsThreadOverride: true,
          override: { model: payload.model, effort: payload.effort },
          effective: { model: payload.model || 'gpt-5.5', effort: payload.effort || 'xhigh' },
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
    expect(got.effective.model).toBe('gpt-5.5');
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
      selectedSkillRefs: [{ key: 'project::git:/repo/.agent/skills/git', name: 'git', scope: 'project', path: '/repo/.agent/skills/git' }],
      manualSkillSelection: true,
    });

    expect(apiMock.callAPI).toHaveBeenCalledWith('turn/start', {
      threadId: 'thread-live',
      input: [
        { type: 'text', text: 'hello' },
        { type: 'mention', name: 'a.txt', path: '/tmp/a.txt' },
      ],
      cwd: '/repo',
      selectedSkillRefs: [{ key: 'project::git:/repo/.agent/skills/git', name: 'git', scope: 'project', personalType: '', path: '/repo/.agent/skills/git' }],
      manualSkillSelection: true,
    });
    const turnPayload = apiMock.callAPI.mock.calls.find(([method]) => method === 'turn/start')?.[1];
    expect(turnPayload).not.toHaveProperty('selectedSkills');
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

  it('clears the active chat thread after archiving the selected thread', async () => {
    const store = useThreadStore();
    store.setPreferenceScopeCwd('/repo');
    Object.assign(store.state, { activeThreadId: 'thread-live', threads: [{ id: 'thread-live', name: 'thread-live', state: 'idle' }] });
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'thread/archive') return { archived: true };
      if (method === 'ui/sidebar/get') return buildSnapshot({ threadId: 'thread-live', activeThreadId: 'thread-live' });
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.setThreadArchived('thread-live', true);
    await flushAsync();

    expect(store.state.activeThreadId).toBe('');
    expect(store.state.archivedThreadAtById).toHaveProperty('thread-live');
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

  // Regression coverage for P1: when the backend reports the caller-supplied
  // prompt_key resolved to a deleted / disabled prompt_template row, the UI
  // must self-clear the cwd-scoped activePromptKey pref so the next launch
  // doesn't keep re-sending the stale pin (and the user is no longer misled
  // by a '已强制使用' badge that no longer takes effect).
  it('clears the activePromptKey pref when thread/start reports a 0105 legacy prompt key stale', async () => {
    const store = useThreadStore();
    const prefSetCalls = [];
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        return mockStartPreference(payload, { provider: 'codex', activePromptKey: 'main/general-en' });
      }
      if (method === 'thread/start') return {
        thread: { id: 'thread-stale' },
        prompt_key_stale: true,
        prompt_key: 'main/general-en',
      };
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-stale', activeThreadId: '' });
      if (method === 'ui/preferences/set') {
        prefSetCalls.push(payload);
        return {};
      }
      return {};
    });

    await store.startThread('/repo-stale', {});
    await flushAsync();

    expect(prefSetCalls).toContainEqual({
      key: 'settings.activePromptKey',
      value: '',
      cwd: '/repo-stale',
    });
    expect(store.state.promptStaleNotice).toMatch(/已自动取消激活/);
  });

  // Negative coverage: a successful launch must NOT clear the pref. Otherwise
  // every start wipes the user's active prompt and they have to re-pin.
  it('does not clear activePromptKey when thread/start succeeds without stale flag', async () => {
    const store = useThreadStore();
    const prefSetCalls = [];
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        return mockStartPreference(payload, { provider: 'codex', activePromptKey: 'main/launch-fav' });
      }
      if (method === 'thread/start') return {
        thread: { id: 'thread-happy' },
        prompt_key: 'main/launch-fav',
      };
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-happy', activeThreadId: '' });
      if (method === 'ui/preferences/set') {
        prefSetCalls.push(payload);
        return {};
      }
      return {};
    });

    await store.startThread('/repo-happy', {});
    await flushAsync();

    expect(prefSetCalls.some((p) => p?.key === 'settings.activePromptKey')).toBe(false);
    expect(store.state.promptStaleNotice || '').toBe('');
  });

  // Accept the camelCase alias as well — backend emits both keys with omitempty;
  // dropping either would silently break this self-clean path in deployments that
  // happen to surface the camelCase variant first.
  it('accepts camelCase promptKeyStale alias from thread/start response', async () => {
    const store = useThreadStore();
    const prefSetCalls = [];
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        return mockStartPreference(payload, { provider: 'codex', activePromptKey: 'main/claude-style' });
      }
      if (method === 'thread/start') return {
        thread: { id: 'thread-stale-camel' },
        promptKeyStale: true,
      };
      if (method === 'ui/state/get') return buildSnapshot({ threadId: 'thread-stale-camel', activeThreadId: '' });
      if (method === 'ui/preferences/set') {
        prefSetCalls.push(payload);
        return {};
      }
      return {};
    });

    await store.startThread('/repo-stale-camel', {});
    await flushAsync();

    expect(prefSetCalls).toContainEqual({
      key: 'settings.activePromptKey',
      value: '',
      cwd: '/repo-stale-camel',
    });
  });
});
