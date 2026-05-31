import { beforeEach, describe, expect, it, vi } from 'vitest';

let bridgeCallback;

const backend = vi.hoisted(() => ({
  readConfig: vi.fn(),
  getWindowBootstrap: vi.fn(),
  getProjects: vi.fn(),
  setActiveProject: vi.fn(),
  addProject: vi.fn(),
  removeProject: vi.fn(),
  getSidebarState: vi.fn(),
  getThreadState: vi.fn(),
  getThreadMessages: vi.fn(),
  getPreference: vi.fn(),
  startThread: vi.fn(),
  startTurn: vi.fn(),
  interruptTurn: vi.fn(),
  compactThread: vi.fn(),
  recoverThread: vi.fn(),
  resolveThreadIdentity: vi.fn(),
  archiveThread: vi.fn(),
  unarchiveThread: vi.fn(),
  deleteThread: vi.fn(),
  getThreadConfig: vi.fn(),
  setThreadConfig: vi.fn(),
  renameThread: vi.fn(),
  setPreference: vi.fn(),
  selectProjectDir: vi.fn(),
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
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
    backend.addProject.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
    backend.removeProject.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });
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
    backend.archiveThread.mockResolvedValue({ ok: true });
    backend.unarchiveThread.mockResolvedValue({ ok: true });
    backend.deleteThread.mockResolvedValue({ ok: true });
    backend.getThreadConfig.mockResolvedValue({
      threadId: 'thread-1',
      provider: 'codex',
      supportsThreadOverride: true,
      override: {},
      effective: { model: 'gpt-5.4', effort: 'medium' },
    });
    backend.setThreadConfig.mockResolvedValue({
      threadId: 'thread-1',
      provider: 'codex',
      supportsThreadOverride: true,
      override: { model: 'gpt-5.4', effort: 'medium' },
      effective: { model: 'gpt-5.4', effort: 'medium' },
    });
    backend.setPreference.mockResolvedValue({ ok: true });
    backend.selectProjectDir.mockResolvedValue('/repo/new');
  });

  it('bootstraps through config, window, projects, sidebar, then thread snapshot', async () => {
    await useClientStore.getState().bootstrap();

    expect(backend.readConfig).toHaveBeenCalledBefore(backend.getWindowBootstrap);
    expect(backend.getWindowBootstrap).toHaveBeenCalledBefore(backend.getProjects);
    expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.active' });
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
    expect(state.provider).toBe('codex');
    expect(state.threads).toHaveLength(1);
    expect(state.tokenUsageByThread['thread-1'].usedTokens).toBe(42);
  });

  it('toggles the active provider preference for the top toolbar', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      provider: 'codex',
    });

    await expect(useClientStore.getState().toggleProviderMode()).resolves.toBe(true);

    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'settings.provider.active',
      value: 'claude',
    });
    expect(useClientStore.getState().provider).toBe('claude');
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '已切换为 Claude',
      tone: 'success',
    }));
  });

  it('routes project selector actions through the project RPC contract', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      projects: ['/repo/app', '/repo/other'],
    });
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    backend.addProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other', '/repo/new'], active: '/repo/other' });
    backend.removeProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });

    await expect(useClientStore.getState().setActiveProjectPath('/repo/other')).resolves.toBe(true);
    expect(backend.setActiveProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/other' });
    expect(useClientStore.getState().activeProject).toBe('/repo/other');

    await expect(useClientStore.getState().addProjectFromPicker()).resolves.toBe(true);
    expect(backend.selectProjectDir).toHaveBeenCalledWith('/repo/other');
    expect(backend.addProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/new' });
    expect(useClientStore.getState().activeProject).toBe('/repo/other');

    await expect(useClientStore.getState().removeProjectPath('/repo/new')).resolves.toBe(true);
    expect(backend.removeProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/new' });
    expect(useClientStore.getState().projects).toEqual(['/repo/app', '/repo/other']);
  });

  it('loads and saves global composer model preferences when no thread is selected', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      provider: 'codex',
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.codex.model': 'gpt-5.4',
      'settings.provider.codex.effort': 'medium',
      'settings.provider.codex.codexModelProvider': 'openai',
    }[key] ?? null));

    await expect(useClientStore.getState().refreshProviderConfig()).resolves.toEqual(expect.objectContaining({
      provider: 'codex',
      model: 'gpt-5.4',
      effort: 'medium',
      codexModelProvider: 'openai',
    }));

    await expect(useClientStore.getState().saveComposerModelConfig({ model: 'gpt-5.5', effort: 'xhigh' })).resolves.toBe(true);
    await expect(useClientStore.getState().saveComposerModelProvider('openrouter')).resolves.toBe(true);

    expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.model', value: 'gpt-5.5' });
    expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.effort', value: 'xhigh' });
    expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.codexModelProvider', value: 'openrouter' });
    expect(useClientStore.getState().providerConfig).toEqual(expect.objectContaining({
      model: 'gpt-5.5',
      effort: 'xhigh',
      codexModelProvider: 'openrouter',
    }));
  });

  it('saves active thread model overrides through thread config RPCs', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      provider: 'codex',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'idle' }],
    });
    backend.getThreadConfig.mockResolvedValue({
      threadId: 'thread-1',
      provider: 'codex',
      supportsThreadOverride: true,
      override: {},
      effective: { model: 'gpt-5.4', effort: 'medium' },
    });
    backend.setThreadConfig.mockResolvedValue({
      threadId: 'thread-1',
      provider: 'codex',
      supportsThreadOverride: true,
      override: { model: 'gpt-5.5', effort: '' },
      effective: { model: 'gpt-5.5', effort: 'medium' },
    });

    await expect(useClientStore.getState().loadThreadConfig('thread-1')).resolves.toEqual(expect.objectContaining({
      supportsThreadOverride: true,
    }));
    await expect(useClientStore.getState().saveComposerModelConfig({ model: 'gpt-5.5', effort: '' })).resolves.toBe(true);

    expect(backend.setThreadConfig).toHaveBeenCalledWith({
      threadId: 'thread-1',
      model: 'gpt-5.5',
      effort: '',
    });
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({ key: 'settings.provider.codex.model' }));
    expect(useClientStore.getState().threadConfigByThread['thread-1']).toEqual(expect.objectContaining({
      override: { model: 'gpt-5.5', effort: '' },
    }));
  });

  it('canonicalizes backend thread ids before scoped thread RPCs', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'agent_123',
      threads: [{
        id: 'agent_123',
        thread_id: 'thread-canonical',
        agent_id: 'agent_123',
        name: 'Runtime thread',
        provider: 'codex',
        status: 'running',
      }],
    });
    backend.getThreadState.mockResolvedValue({ activeThreadId: 'thread-canonical', timelinesByThread: {} });

    await useClientStore.getState().bootstrap();
    await useClientStore.getState().compactActiveThread();

    const state = useClientStore.getState();
    expect(state.activeThreadId).toBe('thread-canonical');
    expect(state.threads[0]).toEqual(expect.objectContaining({
      id: 'thread-canonical',
      agentId: 'agent_123',
    }));
    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-canonical',
      includeDiff: true,
    });
    expect(backend.compactThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-canonical' });
  });

  it('never sends unknown runtime agent ids to thread-scoped RPCs', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_123',
      threads: [],
    });

    await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(false);
    await expect(useClientStore.getState().compactActiveThread()).resolves.toBe(false);
    await expect(useClientStore.getState().recoverActiveThread()).resolves.toBe(false);
    await expect(useClientStore.getState().archiveThread('agent_123', true)).resolves.toBe(false);

    expect(backend.interruptTurn).not.toHaveBeenCalled();
    expect(backend.compactThread).not.toHaveBeenCalled();
    expect(backend.recoverThread).not.toHaveBeenCalled();
    expect(backend.setPreference).not.toHaveBeenCalled();
  });

  it('treats status archived threads as inactive for scoped chat actions', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'essay_agent_15',
      threads: [{
        id: 'essay_agent_15',
        name: '作文Agent-15',
        provider: 'codex',
        status: 'archived',
      }],
    });

    await useClientStore.getState().bootstrap();
    await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(false);

    const state = useClientStore.getState();
    expect(state.activeThreadId).toBe('');
    expect(state.threads[0]).toEqual(expect.objectContaining({ id: 'essay_agent_15', archived: true }));
    expect(state.hasActiveThreadActions()).toBe(false);
    expect(backend.getThreadState).not.toHaveBeenCalledWith(expect.objectContaining({ threadId: 'essay_agent_15' }));
    expect(backend.interruptTurn).not.toHaveBeenCalled();
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

  it('prefers nested thread_id over agent-like ids in thread/start responses', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Hello canonical nested backend',
      attachments: [],
    });
    backend.startThread.mockResolvedValue({ thread: { id: 'agent_123', thread_id: 'thread-nested', agent_id: 'agent_123' } });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-nested',
      input: [{ type: 'text', text: 'Hello canonical nested backend' }],
      manualSkillSelection: false,
    });
    expect(useClientStore.getState().activeThreadId).toBe('thread-nested');
    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({
      id: 'thread-nested',
      agentId: 'agent_123',
    }));
  });

  it('accepts non-placeholder agent_id as a thread/start fallback id', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Use agent id fallback',
      attachments: [],
    });
    backend.startThread.mockResolvedValue({ agent_id: 'essay_agent_16' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'essay_agent_16',
      input: [{ type: 'text', text: 'Use agent id fallback' }],
      manualSkillSelection: false,
    });
    expect(useClientStore.getState().activeThreadId).toBe('essay_agent_16');
  });

  it('accepts backend pending-launch thread ids that look like runtime agent ids', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Use pending launch id',
      attachments: [],
    });
    backend.startThread.mockResolvedValue({
      thread: { id: 'agent_1780163711518420000', status: 'created' },
      threadId: 'agent_1780163711518420000',
      thread_id: 'agent_1780163711518420000',
      sessionId: 'agent_1780163711518420000',
      session_id: 'agent_1780163711518420000',
      status: 'created',
      agentId: 'agent_1780163711518420000',
      agent_id: 'agent_1780163711518420000',
      pending_launch: true,
      pendingLaunch: true,
    });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'agent_1780163711518420000',
      input: [{ type: 'text', text: 'Use pending launch id' }],
      manualSkillSelection: false,
    });
    expect(useClientStore.getState().activeThreadId).toBe('agent_1780163711518420000');
    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({
      id: 'agent_1780163711518420000',
      agentId: 'agent_1780163711518420000',
    }));
  });

  it('starts a new thread instead of sending turns to an unknown active agent id', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_123',
      threads: [],
      draft: 'Recover from bad active id',
      attachments: [],
    });
    backend.startThread.mockResolvedValue({ threadId: 'thread-safe' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.startThread).toHaveBeenCalled();
    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-safe',
      input: [{ type: 'text', text: 'Recover from bad active id' }],
      manualSkillSelection: false,
    });
    expect(useClientStore.getState().activeThreadId).toBe('thread-safe');
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


  it('keeps a newer active thread when an older sync response returns late', async () => {
    let resolveSnapshot;
    backend.getThreadState.mockReturnValue(new Promise((resolve) => {
      resolveSnapshot = resolve;
    }));
    backend.getThreadMessages.mockResolvedValue({ messages: [] });
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-old',
      threads: [
        { id: 'thread-old', name: 'Old', provider: 'codex', status: 'running' },
        { id: 'thread-new', name: 'New', provider: 'codex', status: 'running' },
      ],
      timelinesByThread: {
        'thread-new': [{ id: 'user-new', role: 'user', text: 'new message', time: '2026-05-30T00:00:00Z' }],
      },
    });

    const sync = useClientStore.getState().syncThreadState('thread-old');
    await vi.waitFor(() => expect(backend.getThreadState).toHaveBeenCalled());
    useClientStore.setState({ activeThreadId: 'thread-new' });
    resolveSnapshot({
      activeThreadId: 'thread-old',
      threads: [{ id: 'thread-old', name: 'Old', provider: 'codex', status: 'idle' }],
      timelinesByThread: {
        'thread-old': [{ id: 'old-assistant', kind: 'assistant', text: 'old reply' }],
      },
    });

    await sync;

    const state = useClientStore.getState();
    expect(state.activeThreadId).toBe('thread-new');
    expect(state.threads).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: 'thread-new', name: 'New' }),
      expect.objectContaining({ id: 'thread-old', name: 'Old' }),
    ]));
    expect(state.timelinesByThread['thread-new']).toEqual([
      expect.objectContaining({ role: 'user', text: 'new message' }),
    ]);
  });

  it('applies runtime agent message delta and completion events to the timeline', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
      timelinesByThread: {
        'thread-1': [{ id: 'user-1', role: 'user', text: 'say ok', time: '2026-05-30T00:00:00Z' }],
      },
    });
    useClientStore.getState().initializeEvents();

    bridgeCallback({
      method: 'item/agentMessage/delta',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        delta: 'o',
        stream: 'message',
      },
    });
    bridgeCallback({
      method: 'item/agentMessage/delta',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        delta: 'k',
        stream: 'message',
      },
    });
    bridgeCallback({
      method: 'item/completed',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        item: { id: 'msg-final', type: 'agentMessage', text: 'ok' },
      },
    });

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ role: 'user', text: 'say ok' }),
      expect.objectContaining({ id: 'msg-final', role: 'assistant', text: 'ok', done: true }),
    ]);
  });

  it('keeps runtime assistant replies when later partial bridge patches omit them', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'user-1', role: 'user', text: 'say ok', done: true }],
      },
    });
    useClientStore.getState().initializeEvents();

    bridgeCallback({
      type: 'item/agentMessage/delta',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        delta: 'ok',
      },
    });
    bridgeCallback({
      type: 'item/completed',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        item: { id: 'msg-final', type: 'agentMessage', text: 'ok' },
      },
    });
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '1',
        timelineItems: [{ id: 'turn-end:turn-1', kind: 'turn_end', status: 'completed' }],
      },
    });

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ role: 'user', text: 'say ok' }),
      expect.objectContaining({ id: 'msg-final', role: 'assistant', text: 'ok', done: true }),
    ]);
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
    expect(backend.archiveThread).toHaveBeenCalledWith({ threadId: 'thread-1' });
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'archivedThreadAtById.thread-1',
      value: expect.any(Number),
    });
  });

  it('restores archived threads without enabling active thread actions', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-archived',
      threads: [{ id: 'thread-archived', name: '归档线程', provider: 'codex', status: 'archived', archived: true }],
    });

    expect(useClientStore.getState().hasActiveThreadActions()).toBe(false);
    await expect(useClientStore.getState().archiveThread('thread-archived', false)).resolves.toBe(true);

    expect(backend.unarchiveThread).toHaveBeenCalledWith({ threadId: 'thread-archived' });
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'archivedThreadAtById.thread-archived',
      value: null,
    });
    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({ archived: false }));
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '线程已恢复到列表',
      tone: 'success',
    }));
  });

  it('deletes stale archived threads through backend and clears archive preferences', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-stale',
      threads: [
        { id: 'thread-stale', name: '旧归档线程', provider: 'codex', status: 'archived', archived: true, archivedAt: Date.now() - 8 * 24 * 60 * 60 * 1000 },
        { id: 'thread-fresh', name: '近期归档线程', provider: 'codex', status: 'archived', archived: true, archivedAt: Date.now() },
      ],
    });

    await expect(useClientStore.getState().deleteStaleThreads(['thread-stale'])).resolves.toEqual({ deleted: 1, failed: 0 });

    expect(backend.deleteThread).toHaveBeenCalledWith({ threadId: 'thread-stale' });
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'archivedThreadAtById.thread-stale',
      value: null,
    });
    expect(useClientStore.getState().threads.map((thread) => thread.id)).toEqual(['thread-fresh']);
    expect(useClientStore.getState().activeThreadId).toBe('');
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '已删除 1 个无用会话',
      tone: 'success',
    }));
  });
});
