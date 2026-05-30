import { beforeEach, describe, expect, it, vi } from 'vitest';

let bridgeCallback;

const backend = vi.hoisted(() => ({
  readConfig: vi.fn(),
  getWindowBootstrap: vi.fn(),
  getProjects: vi.fn(),
  getSidebarState: vi.fn(),
  getThreadState: vi.fn(),
  getThreadMessages: vi.fn(),
  getPreference: vi.fn(),
  startThread: vi.fn(),
  startTurn: vi.fn(),
  interruptTurn: vi.fn(),
  compactThread: vi.fn(),
  recoverThread: vi.fn(),
  renameThread: vi.fn(),
  setPreference: vi.fn(),
  selectFiles: vi.fn(),
  onBridgeEvent: vi.fn((callback) => {
    bridgeCallback = callback;
    return () => {
      bridgeCallback = null;
    };
  }),
}));

vi.mock('../../../shared/api/backendApi.js', () => ({
  ...backend,
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
}));

import { resetClientStoreForTests, useClientStore } from './useClientStore.js';

describe('useClientStore backend contract', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    bridgeCallback = null;
    resetClientStoreForTests();
    backend.readConfig.mockResolvedValue({ cwd: '/repo/app' });
    backend.getWindowBootstrap.mockResolvedValue({ snapshot: null });
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'running' }],
      tokenUsageByThread: {
        'thread-1': { usedTokens: 42, contextWindowTokens: 100, usedPercent: 42 },
      },
    });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });
    backend.getThreadMessages.mockResolvedValue({ messages: [] });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.codexHome': '~/.codex',
      'settings.provider.codex.codexInstanceKey': 'default',
      'settings.provider.codex.codexModelProvider': 'openai',
    }[key] ?? null));
  });

  it('bootstraps through config, window, projects, sidebar, then thread snapshot', async () => {
    await useClientStore.getState().bootstrap();

    expect(backend.readConfig).toHaveBeenCalledBefore(backend.getWindowBootstrap);
    expect(backend.getWindowBootstrap).toHaveBeenCalledBefore(backend.getProjects);
    expect(backend.getProjects).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-1',
      includeDiff: true,
    });

    const state = useClientStore.getState();
    expect(state.cwd).toBe('/repo/app');
    expect(state.activeProject).toBe('/repo/app');
    expect(state.threads).toHaveLength(1);
    expect(state.tokenUsageByThread['thread-1'].usedTokens).toBe(42);
  });

  it('sends an empty-thread message through thread/start before turn/start and keeps the user message visible', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Hello backend',
      attachments: [{ path: '/tmp/a.txt', name: 'a.txt' }],
    });
    backend.startThread.mockResolvedValue({ threadId: 'thread-new' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      name: 'Hello backend',
      modelProvider: 'codex',
      deferSpawn: true,
    }));
    const startPayload = backend.startThread.mock.calls[0][0];
    expect(startPayload).not.toHaveProperty('prompt');
    expect(startPayload).not.toHaveProperty('optimisticUserMessage');
    expect(startPayload).not.toHaveProperty('skipInitialRuntimeSync');
    expect(backend.startThread).toHaveBeenCalledBefore(backend.startTurn);
    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-new',
      input: [
        { type: 'text', text: 'Hello backend' },
        { type: 'mention', name: 'a.txt', path: '/tmp/a.txt' },
      ],
      manualSkillSelection: false,
    });

    const timeline = useClientStore.getState().timelinesByThread['thread-new'];
    expect(timeline).toEqual([
      expect.objectContaining({ role: 'user', text: 'Hello backend' }),
    ]);
    expect(useClientStore.getState().draft).toBe('');
  });

  it('builds thread/start launch payload from provider preferences', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Use configured launch',
      attachments: [],
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexHome': '/Users/test/.codex-alt',
      'settings.provider.codex.codexInstanceKey': 'desktop-main',
      'settings.provider.codex.codexModelProvider': 'openrouter',
      'settings.activePromptKey': 'main/dag_designer_zh',
    }[key] ?? null));
    backend.startThread.mockResolvedValue({ threadId: 'thread-configured' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.active' });
    expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.activePromptKey' });
    expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      modelProvider: 'codex',
      model: 'gpt-5.5',
      effort: 'xhigh',
      prompt_key: 'main/dag_designer_zh',
      config: {
        codexHome: '/Users/test/.codex-alt',
        codexInstanceKey: 'desktop-main',
        codexModelProvider: 'openrouter',
      },
    }));
  });

  it('canonicalizes object-shaped provider preferences before thread/start', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Use object prefs',
      attachments: [],
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': { value: 'codex', label: 'Codex' },
      'settings.provider.codex.model': { value: 'gpt-5.5', label: 'GPT' },
      'settings.provider.codex.effort': { id: 'medium', label: 'Medium' },
      'settings.provider.codex.codexHome': '/Users/test/.codex-alt',
      'settings.provider.codex.codexInstanceKey': 'desktop-main',
      'settings.provider.codex.codexModelProvider': 'openrouter',
    }[key] ?? null));
    backend.startThread.mockResolvedValue({ threadId: 'thread-object-prefs' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
      modelProvider: 'codex',
      model: 'gpt-5.5',
      effort: 'medium',
    }));
  });

  it('recovers and retries turn/start when the backend session is missing', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      draft: 'Retry on missing session',
      attachments: [],
    });
    backend.startTurn
      .mockRejectedValueOnce(new Error('session not found for agent "agent_123"'))
      .mockResolvedValueOnce({ ok: true });
    backend.recoverThread.mockResolvedValue({ recovered: true });

    await useClientStore.getState().sendDraft();

    expect(backend.recoverThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
    expect(backend.startTurn).toHaveBeenCalledTimes(2);
    expect(backend.startTurn.mock.invocationCallOrder[0]).toBeLessThan(backend.recoverThread.mock.invocationCallOrder[0]);
    expect(backend.recoverThread.mock.invocationCallOrder[0]).toBeLessThan(backend.startTurn.mock.invocationCallOrder[1]);
  });

  it('applies window bootstrap snapshot before scoped RPCs', async () => {
    backend.getWindowBootstrap.mockResolvedValue({
      snapshot: { cwd: '/repo/other', page: 'skills' },
    });
    backend.getProjects.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    backend.getSidebarState.mockResolvedValue({ threads: [] });

    await useClientStore.getState().bootstrap();

    expect(backend.getProjects).toHaveBeenCalledWith({ cwd: '/repo/other' });
    expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/other' });
    expect(useClientStore.getState()).toEqual(expect.objectContaining({
      cwd: '/repo/app',
      activeProject: '/repo/other',
      activePage: 'skills',
    }));
  });

  it('accepts the real backend nested thread/start response shape', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Hello nested backend',
      attachments: [],
    });
    backend.startThread.mockResolvedValue({ thread: { id: 'thread-nested' }, pending_launch: true });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-nested',
      input: [{ type: 'text', text: 'Hello nested backend' }],
      manualSkillSelection: false,
    });
    expect(useClientStore.getState().activeThreadId).toBe('thread-nested');
  });

  it('preserves the optimistic user message when a backend patch only contains assistant output', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Keep my message visible',
      attachments: [],
    });
    backend.startThread.mockResolvedValue({ threadId: 'thread-new' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();
    useClientStore.getState().initializeEvents();
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-new',
        sequence: '1',
        timelineItems: [{ id: 'assistant-1', kind: 'assistant', text: 'AI reply' }],
      },
    });

    expect(useClientStore.getState().timelinesByThread['thread-new']).toEqual([
      expect.objectContaining({ role: 'user', text: 'Keep my message visible' }),
      expect.objectContaining({ role: 'assistant', text: 'AI reply' }),
    ]);
  });

  it('restores draft and attachments when backend send fails', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Do not lose this',
      attachments: [{ path: '/tmp/a.txt', name: 'a.txt' }],
    });
    backend.startThread.mockRejectedValue(new Error('thread/start failed'));

    await expect(useClientStore.getState().sendDraft()).rejects.toThrow('thread/start failed');

    const state = useClientStore.getState();
    expect(state.draft).toBe('Do not lose this');
    expect(state.attachments).toEqual([{ path: '/tmp/a.txt', name: 'a.txt' }]);
    expect(state.warningEntries[0]).toEqual(expect.objectContaining({
      level: 'error',
      event: 'thread.send.failed',
    }));
  });

  it('starts a new composer draft from a shared file continuation action', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      activePage: 'files',
      draft: 'old draft',
      attachments: [{ path: 'reports/final.md', name: 'final.md' }],
    });

    expect(useClientStore.getState().continueWithSharedFile('reports/final.md')).toBe(true);

    const state = useClientStore.getState();
    expect(state.activePage).toBe('chat');
    expect(state.activeThreadId).toBe('');
    expect(state.draft).toContain('reports/final.md');
    expect(state.attachments).toEqual([{ path: 'reports/final.md', name: 'final.md' }]);
  });

  it('applies bridge patches for timeline, token usage, diff and warnings', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: { 'thread-1': [] },
    });
    useClientStore.getState().initializeEvents();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '9007199254740993123',
        timelineItems: [{ id: 'assistant-1', kind: 'assistant', text: 'pong' }],
        tokenUsage: { usedTokens: 12, contextWindowTokens: 100, usedPercent: 12 },
        diffText: 'diff --git a/file b/file',
      },
    });
    bridgeCallback({
      type: 'rpc.failed',
      payload: { method: 'turn/start', threadId: 'thread-1', traceId: 'trace-123' },
    });

    const state = useClientStore.getState();
    expect(state.timelinesByThread['thread-1'][0]).toEqual(expect.objectContaining({
      role: 'assistant',
      text: 'pong',
    }));
    expect(state.tokenUsageByThread['thread-1']).toEqual({
      usedTokens: 12,
      contextWindowTokens: 100,
      usedPercent: 12,
    });
    expect(state.diffTextByThread['thread-1']).toContain('diff --git');
    expect(state.warningEntries).toEqual([
      expect.objectContaining({ level: 'error', event: 'rpc.failed' }),
    ]);
  });

  it('connects conversation card actions to backend RPCs with explicit cwd', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
    });

    await useClientStore.getState().interruptActiveThread();
    await useClientStore.getState().compactActiveThread();
    await useClientStore.getState().recoverActiveThread();
    await useClientStore.getState().renameThread('thread-1', 'Renamed');
    await useClientStore.getState().archiveThread('thread-1', true);

    expect(backend.interruptTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
    expect(backend.compactThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
    expect(backend.recoverThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
    expect(backend.renameThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1', name: 'Renamed' });
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'archivedThreadAtById.thread-1',
      value: expect.any(String),
    });
  });
});
