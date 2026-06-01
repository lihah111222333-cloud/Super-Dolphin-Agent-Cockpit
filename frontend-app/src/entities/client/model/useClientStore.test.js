import { beforeEach, describe, expect, it, vi } from 'vitest';

let bridgeCallback;

const backend = vi.hoisted(() => ({
  readConfig: vi.fn(),
  getWindowBootstrap: vi.fn(),
  openNewWindow: vi.fn(),
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
  beginTextClipboardWrite: vi.fn(),
  copyTextToClipboard: vi.fn(),
  onBridgeEvent: vi.fn((callback) => {
    bridgeCallback = callback;
    return () => {
      bridgeCallback = null;
    };
  }),
}));

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

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
    backend.openNewWindow.mockResolvedValue({ ok: true });
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
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexHome': '~/.codex',
      'settings.provider.codex.codexInstanceKey': 'default',
      'settings.provider.codex.codexModelProvider': 'openai',
      'settings.provider.claude.model': 'sonnet',
      'settings.provider.claude.effort': 'high',
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
    backend.beginTextClipboardWrite.mockReturnValue(null);
    backend.copyTextToClipboard.mockResolvedValue(true);
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

  it('fails bootstrap when the active provider preference is missing', async () => {
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.codex.codexHome': '~/.codex',
      'settings.provider.codex.codexInstanceKey': 'default',
      'settings.provider.codex.codexModelProvider': 'openai',
    }[key] ?? null));

    await expect(useClientStore.getState().bootstrap()).rejects.toThrow(
      'frontend-app bootstrap: settings.provider.active preference is required',
    );

    expect(backend.getProjects).not.toHaveBeenCalled();
    expect(useClientStore.getState().bootstrapStatus).toBe('failed');
  });

  it('fails bootstrap when the selected provider model preference is missing', async () => {
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexModelProvider': 'openai',
    }[key] ?? null));

    await expect(useClientStore.getState().bootstrap()).rejects.toThrow(
      'provider.config: settings.provider.codex.model preference is required',
    );

    expect(backend.getProjects).not.toHaveBeenCalled();
    expect(useClientStore.getState().bootstrapStatus).toBe('failed');
  });

  it('hydrates thread providers from sidebar runtime metadata', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-claude',
      threads: [{ id: 'thread-claude', name: 'Claude runtime thread', status: 'running' }],
      agentRuntimeById: {
        'thread-claude': { provider: 'claude', providerThreadId: 'provider-1' },
      },
    });

    await useClientStore.getState().bootstrap();

    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({
      id: 'thread-claude',
      provider: 'claude',
    }));
  });

  it('hydrates pinned chat threads from the backend threadPins preference', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: 'Existing', provider: 'codex', status: 'running' },
        { id: 'thread-2', name: 'Pinned', provider: 'codex', status: 'idle' },
      ],
      'threadPins.chat': { 'thread-2': 1735689600000 },
    });

    await useClientStore.getState().bootstrap();

    const state = useClientStore.getState();
    expect(state.pinnedThreadAtById).toEqual({ 'thread-2': 1735689600000 });
    expect(state.threads.find((thread) => thread.id === 'thread-2')).toEqual(expect.objectContaining({
      pinned: true,
      pinnedAt: 1735689600000,
    }));
  });

  it('toggles thread pins through the backend threadPins chat preference map', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'running' }],
      pinnedThreadAtById: {},
    });

    await expect(useClientStore.getState().toggleThreadPin('thread-1')).resolves.toBe(true);

    const pinnedAt = useClientStore.getState().pinnedThreadAtById['thread-1'];
    expect(pinnedAt).toBeGreaterThan(0);
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'threadPins.chat',
      value: { 'thread-1': pinnedAt },
    });
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

  it('does not change the active provider while an opened chat is selected', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      provider: 'codex',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'running' }],
    });

    await expect(useClientStore.getState().toggleProviderMode()).resolves.toBe(false);

    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.active',
    }));
    expect(useClientStore.getState().provider).toBe('codex');
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '已开启的聊天不能更改 provider，请新建对话后切换',
      tone: 'warning',
    }));
  });

  it('keeps provider toggle fail-fast when cwd is missing', async () => {
    resetClientStoreForTests({
      cwd: '',
      activeProject: '',
      provider: 'codex',
    });

    await expect(useClientStore.getState().toggleProviderMode()).rejects.toThrow(
      'frontend-app: cwd is required for provider.toggle',
    );

    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.active',
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

  it('opens an independent app window from the selected directory', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/other',
      projects: ['/repo/app', '/repo/other'],
    });
    backend.selectProjectDir.mockResolvedValue('/repo/window');

    await expect(useClientStore.getState().openNewWindow()).resolves.toBe(true);

    expect(backend.selectProjectDir).toHaveBeenCalledWith('/repo/other');
    expect(backend.openNewWindow).toHaveBeenCalledWith({ cwd: '/repo/window' });
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '已打开新窗口：repo/window',
      tone: 'success',
    }));
  });

  it('registers a visible fallback project before switching to it', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '.',
      projects: [],
    });
    backend.addProject.mockResolvedValue({ projects: ['/repo/app'], active: '.' });
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app'], active: '/repo/app' });

    await expect(useClientStore.getState().setActiveProjectPath('/repo/app')).resolves.toBe(true);

    expect(backend.addProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/app' });
    expect(backend.setActiveProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/app' });
    expect(useClientStore.getState().activeProject).toBe('/repo/app');
  });

  it('reloads the sidebar threads for the selected project after switching directories', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      projects: ['/repo/app', '/repo/other'],
      activeThreadId: 'thread-old',
      threads: [{ id: 'thread-old', name: 'Old project thread', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-old': [{ id: 'old-user', role: 'user', text: 'old cwd message' }] },
      tokenUsageByThread: { 'thread-old': { usedTokens: 8, contextWindowTokens: 100, usedPercent: 8 } },
      activityStatsByThread: { 'thread-old': { lspCalls: 1, commands: 0, fileEdits: 0, toolCalls: {} } },
      diffTextByThread: { 'thread-old': 'old cwd diff' },
    });
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-new',
      threads: [{ id: 'thread-new', name: 'Other project thread', provider: 'claude', status: 'idle' }],
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-new',
      timelinesByThread: { 'thread-new': [] },
      diffTextByThread: { 'thread-new': '' },
    });
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    await expect(useClientStore.getState().setActiveProjectPath('/repo/other')).resolves.toBe(true);

    expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/other' });
    expect(useClientStore.getState().activeThreadId).toBe('');
    expect(useClientStore.getState().threads).toEqual([
      expect.objectContaining({ id: 'thread-new', name: 'Other project thread', provider: 'claude' }),
    ]);
    expect(backend.getThreadState).not.toHaveBeenCalledWith({
      cwd: '/repo/other',
      threadId: 'thread-new',
      includeDiff: true,
    });
    expect(useClientStore.getState().threads.some((thread) => thread.id === 'thread-old')).toBe(false);
    expect(useClientStore.getState().timelinesByThread).not.toHaveProperty('thread-old');
    expect(useClientStore.getState().tokenUsageByThread).not.toHaveProperty('thread-old');
    expect(useClientStore.getState().activityStatsByThread).not.toHaveProperty('thread-old');
    expect(useClientStore.getState().diffTextByThread).not.toHaveProperty('thread-old');

    await useClientStore.getState().setActiveThread('thread-new');
    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/other',
      threadId: 'thread-new',
      includeDiff: false,
    });
    expect(useClientStore.getState().activeThreadId).toBe('thread-new');
  });

  it('switches project immediately while the sidebar refresh continues in the background', async () => {
    const projectChange = deferred();
    const sidebarRefresh = deferred();
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      projects: ['/repo/app', '/repo/other'],
      activeThreadId: 'thread-old',
      threads: [{ id: 'thread-old', name: 'Old project thread', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-old': [{ id: 'old-user', role: 'user', text: 'old cwd message' }] },
    });
    backend.setActiveProject.mockReturnValue(projectChange.promise);
    backend.getSidebarState.mockReturnValue(sidebarRefresh.promise);

    const switchPromise = useClientStore.getState().setActiveProjectPath('/repo/other');
    await Promise.resolve();

    expect(useClientStore.getState()).toEqual(expect.objectContaining({
      activeProject: '/repo/other',
      activeThreadId: '',
      chatSurfaceLoadingCwd: '/repo/other',
    }));
    expect(useClientStore.getState().threads).toEqual([]);
    expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/other' });

    sidebarRefresh.resolve({
      activeThreadId: 'thread-other',
      threads: [{ id: 'thread-other', name: 'Other project thread', provider: 'claude', status: 'idle' }],
    });
    await Promise.resolve();
    await Promise.resolve();

    expect(useClientStore.getState().threads).toEqual([
      expect.objectContaining({ id: 'thread-other', name: 'Other project thread' }),
    ]);
    expect(useClientStore.getState().activeThreadId).toBe('');
    expect(useClientStore.getState().chatSurfaceLoadingCwd).toBe('');

    projectChange.resolve({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    await expect(switchPromise).resolves.toBe(true);
  });

  it('filters mixed sidebar snapshots to the selected project cwd', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      projects: ['/repo/app', '/repo/other'],
    });
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-other',
      threads: [
        { id: 'thread-app', cwd: '/repo/app', name: 'App thread', provider: 'codex', status: 'idle' },
        { id: 'thread-other', cwd: '/repo/other', name: 'Other thread', provider: 'claude', status: 'running' },
      ],
    });

    await expect(useClientStore.getState().setActiveProjectPath('/repo/app')).resolves.toBe(true);

    expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(useClientStore.getState().threads).toEqual([
      expect.objectContaining({ id: 'thread-app', name: 'App thread', cwd: '/repo/app' }),
    ]);
    expect(useClientStore.getState().activeThreadId).toBe('');
  });

  it('keeps composer drafts isolated by selected thread and project cwd', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      projects: ['/repo/app', '/repo/other'],
      activeThreadId: 'thread-a',
      threads: [
        { id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' },
        { id: 'thread-b', cwd: '/repo/app', name: 'Thread B', provider: 'codex', status: 'idle' },
      ],
      draft: 'draft for A',
      attachments: [{ path: '/tmp/a.txt', name: 'a.txt' }],
    });
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      threads: [
        { id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' },
        { id: 'thread-b', cwd: '/repo/app', name: 'Thread B', provider: 'codex', status: 'idle' },
      ],
      timelinesByThread: { [threadId]: [] },
    }));

    await useClientStore.getState().setActiveThread('thread-b');
    expect(useClientStore.getState().draft).toBe('');
    expect(useClientStore.getState().attachments).toEqual([]);

    useClientStore.getState().setDraft('draft for B');

    await useClientStore.getState().setActiveThread('thread-a');
    expect(useClientStore.getState().draft).toBe('draft for A');
    expect(useClientStore.getState().attachments).toEqual([
      expect.objectContaining({ path: '/tmp/a.txt', name: 'a.txt' }),
    ]);

    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: '',
      threads: [{ id: 'thread-other', cwd: '/repo/other', name: 'Other project thread', provider: 'claude', status: 'idle' }],
    });
    await useClientStore.getState().setActiveProjectPath('/repo/other');

    expect(useClientStore.getState().draft).toBe('');
    expect(useClientStore.getState().attachments).toEqual([]);
  });

  it('does not keep the old active thread when the selected project sidebar has no active thread', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      projects: ['/repo/app', '/repo/other'],
      activeThreadId: 'thread-old',
      threads: [{ id: 'thread-old', name: 'Old project thread', provider: 'codex', status: 'running' }],
    });
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: '',
      threads: [{ id: 'thread-new', name: 'Other project thread', provider: 'claude', status: 'idle' }],
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-new',
      timelinesByThread: { 'thread-new': [] },
      diffTextByThread: { 'thread-new': '' },
    });
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    await expect(useClientStore.getState().setActiveProjectPath('/repo/other')).resolves.toBe(true);

    expect(backend.getThreadState).not.toHaveBeenCalledWith({
      cwd: '/repo/other',
      threadId: 'thread-new',
      includeDiff: true,
    });
    expect(useClientStore.getState().activeThreadId).toBe('');
    expect(useClientStore.getState().threads).toEqual([
      expect.objectContaining({ id: 'thread-new', name: 'Other project thread' }),
    ]);
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

  it('uses global model preferences when the selector has no thread config target', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent-failed',
      provider: 'codex',
      providerConfig: { provider: 'codex', model: 'gpt-5.5', effort: 'xhigh' },
      threads: [{ id: 'agent-failed', name: 'Failed runtime', provider: 'codex', status: 'error' }],
    });

    await expect(useClientStore.getState().saveComposerModelConfig({
      threadId: '',
      model: 'gpt-5.4',
      effort: 'medium',
    })).resolves.toBe(true);

    expect(backend.getThreadConfig).not.toHaveBeenCalled();
    expect(backend.setThreadConfig).not.toHaveBeenCalled();
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'settings.provider.codex.model',
      value: 'gpt-5.4',
    });
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'settings.provider.codex.effort',
      value: 'medium',
    });
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

  it('does not query thread config for codex runtime agent ids during chat switches', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_1780223999392305000',
      provider: 'codex',
      threads: [{
        id: 'agent_1780223999392305000',
        agentId: 'agent_1780223999392305000',
        name: 'Runtime codex thread',
        provider: 'codex',
        status: 'running',
      }],
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'agent_1780223999392305000',
      threads: [{
        id: 'agent_1780223999392305000',
        agent_id: 'agent_1780223999392305000',
        name: 'Runtime codex thread',
        provider: 'codex',
        status: 'running',
      }],
      timelinesByThread: {},
    });
    backend.getThreadMessages.mockResolvedValueOnce({ messages: [] });

    await useClientStore.getState().syncThreadState('agent_1780223999392305000');

    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'agent_1780223999392305000',
      includeDiff: true,
    });
    expect(backend.getThreadConfig).not.toHaveBeenCalled();
    expect(useClientStore.getState().warningEntries).toEqual([]);
  });

  it('does not query thread config for agent-only runtime threads', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_1780223389861443000',
      provider: 'codex',
      threads: [{
        id: 'agent_1780223389861443000',
        agentId: 'agent_1780223389861443000',
        name: 'Runtime codex thread',
        provider: 'codex',
        status: 'running',
      }],
    });

    await expect(useClientStore.getState().loadThreadConfig('agent_1780223389861443000')).resolves.toBeNull();
    await expect(useClientStore.getState().saveComposerModelConfig({
      threadId: 'agent_1780223389861443000',
      model: 'gpt-5.5',
      effort: 'xhigh',
    })).resolves.toBe(true);

    expect(backend.getThreadConfig).not.toHaveBeenCalled();
    expect(backend.setThreadConfig).not.toHaveBeenCalled();
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'settings.provider.codex.model',
      value: 'gpt-5.5',
    });
    expect(useClientStore.getState().warningEntries).toEqual([]);
  });

  it('does not auto-retry thread config after a failed auto-load for the same thread', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      provider: 'codex',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'idle' }],
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'idle' }],
      timelinesByThread: {},
    });
    backend.getThreadConfig.mockRejectedValue(new Error('thread session is not available'));

    await useClientStore.getState().syncThreadState('thread-1');
    await useClientStore.getState().syncThreadState('thread-1');

    expect(backend.getThreadConfig).toHaveBeenCalledTimes(1);
    expect(useClientStore.getState().warningEntries.filter((entry) => entry.event === 'thread.config.get.failed')).toHaveLength(1);
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

  it('copies thread info as backend-resolved JSON and treats dot project as current cwd', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '.',
      activeThreadId: 'thread-1',
      provider: 'codex',
      providerConfig: { provider: 'codex', model: 'gpt-5.5', effort: 'xhigh' },
      threads: [{
        id: 'thread-1',
        agentId: 'agent-1',
        name: 'Thread 1',
        provider: 'codex',
        status: 'running',
      }],
    });
    backend.resolveThreadIdentity.mockResolvedValue({
      id: 'thread-1',
      agent_id: 'agent-1',
      providerThreadId: 'provider-thread-1',
      sessionId: 'session-uuid-1',
      provider: 'codex',
      port: 4512,
      cwd: '/repo/app',
    });
    backend.getThreadConfig.mockResolvedValue({
      threadId: 'thread-1',
      provider: 'codex',
      supportsThreadOverride: true,
      effective: { model: 'gpt-5.4', effort: 'medium' },
    });
    await expect(useClientStore.getState().copyActiveThreadInfo()).resolves.toBe(true);

    expect(backend.resolveThreadIdentity).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
    const payload = JSON.parse(backend.copyTextToClipboard.mock.calls[0][0]);
    expect(payload).toEqual(expect.objectContaining({
      agentId: 'agent-1',
      providerThreadId: 'provider-thread-1',
      uuid: 'session-uuid-1',
      name: 'Thread 1',
      status: 'running',
      provider: 'codex',
      model: 'gpt-5.4',
      effort: 'medium',
      port: 4512,
      cwd: '/repo/app',
      'log-path': '~/.multi-agent/log/app/',
    }));
    expect(payload.copiedAt).toContain('UTC+8');
  });

  it('commits a prepared clipboard write when browser clipboard would lose activation after async calls', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '.',
      activeThreadId: 'thread-1',
      provider: 'codex',
      providerConfig: { provider: 'codex', model: 'gpt-5.5', effort: 'xhigh' },
      threads: [{ id: 'thread-1', agentId: 'agent-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
    });
    backend.resolveThreadIdentity.mockResolvedValue({
      id: 'thread-1',
      agent_id: 'agent-1',
      providerThreadId: 'provider-thread-1',
      provider: 'codex',
      cwd: '/repo/app',
    });
    Object.assign(globalThis.navigator, {
      clipboard: { writeText: vi.fn().mockRejectedValue(new Error('The request is not allowed')) },
    });
    const preparedClipboardWrite = {
      commit: vi.fn().mockResolvedValue(true),
      cancel: vi.fn(),
    };
    backend.beginTextClipboardWrite.mockReturnValue(preparedClipboardWrite);
    backend.copyTextToClipboard.mockResolvedValue(false);

    await expect(useClientStore.getState().copyActiveThreadInfo()).resolves.toBe(true);

    expect(globalThis.navigator.clipboard.writeText).not.toHaveBeenCalled();
    expect(backend.beginTextClipboardWrite).toHaveBeenCalledTimes(1);
    expect(preparedClipboardWrite.commit).toHaveBeenCalledTimes(1);
    expect(backend.copyTextToClipboard).not.toHaveBeenCalled();
    expect(JSON.parse(preparedClipboardWrite.commit.mock.calls[0][0])).toEqual(expect.objectContaining({
      agentId: 'agent-1',
      providerThreadId: 'provider-thread-1',
    }));
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

  it('preserves the optimistic first user message when a fresh thread sync has an empty timeline', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Hello backend',
      attachments: [],
    });
    backend.startThread.mockResolvedValue({ threadId: 'thread-new' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-new',
      threads: [{ id: 'thread-new', name: 'Hello backend', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-new': [] },
    });

    await useClientStore.getState().syncThreadState('thread-new');

    expect(useClientStore.getState().timelinesByThread['thread-new']).toEqual([
      expect.objectContaining({ role: 'user', text: 'Hello backend' }),
    ]);
  });

  it('loads selected thread messages in chronological order when the backend returns latest first', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
      timelinesByThread: {},
    });
    backend.getThreadMessages.mockResolvedValueOnce({
      messages: [
        { id: 'assistant-new', role: 'assistant', content: 'latest reply', createdAt: '2026-05-30T00:03:00Z' },
        { id: 'user-old', role: 'user', content: 'first prompt', createdAt: '2026-05-30T00:01:00Z' },
        { id: 'assistant-old', role: 'assistant', content: 'first reply', createdAt: '2026-05-30T00:02:00Z' },
      ],
    });

    await useClientStore.getState().syncThreadState('thread-1');

    expect(useClientStore.getState().timelinesByThread['thread-1'].map((message) => message.text)).toEqual([
      'first prompt',
      'first reply',
      'latest reply',
    ]);
  });

  it('keeps thread/state assistant text when thread/messages later returns empty assistant rows', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{
        id: 'thread-1',
        agentId: 'agent_1780323743107010000',
        providerThreadId: '019e8390-77dc-7951-960f-246fac8780bd',
        name: 'Existing',
        provider: 'codex',
        status: 'idle',
      }],
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [{
        id: 'thread-1',
        agentId: 'agent_1780323743107010000',
        providerThreadId: '019e8390-77dc-7951-960f-246fac8780bd',
        name: 'Existing',
        provider: 'codex',
        status: 'idle',
      }],
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-1', role: 'assistant', text: '1', createdAt: '2026-06-01T14:22:00Z' }],
      },
    });
    backend.getThreadMessages.mockResolvedValueOnce({
      messages: [
        { id: 'assistant-1', role: 'assistant', content: '', createdAt: '2026-06-01T14:26:00Z' },
        { id: 'assistant-2', role: 'assistant', content: '', createdAt: '2026-06-01T14:27:00Z' },
      ],
    });

    await useClientStore.getState().syncThreadState('thread-1');

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ id: 'assistant-1', role: 'assistant', text: '1' }),
    ]);
  });

  it('reads thread/messages text fields instead of rendering blank assistant bubbles', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
      timelinesByThread: {},
    });
    backend.getThreadMessages.mockResolvedValueOnce({
      messages: [
        { id: 'assistant-text', role: 'assistant', text: 'loaded from text field', createdAt: '2026-06-01T14:26:00Z' },
      ],
    });

    await useClientStore.getState().syncThreadState('thread-1');

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ id: 'assistant-text', role: 'assistant', text: 'loaded from text field' }),
    ]);
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

  it('fails thread/start launch when provider runtime preferences are missing', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Use configured launch',
      attachments: [],
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexHome': '/Users/test/.codex-alt',
      'settings.provider.codex.codexInstanceKey': 'desktop-main',
      'settings.provider.codex.codexModelProvider': 'openrouter',
    }[key] ?? null));

    await expect(useClientStore.getState().sendDraft()).rejects.toThrow(
      'startThread: settings.provider.codex.model preference is required',
    );

    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).not.toHaveBeenCalled();
  });

  it('exposes the same launch preferences for non-chat thread launches', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexHome': '/Users/test/.codex-alt',
      'settings.provider.codex.codexInstanceKey': 'desktop-main',
      'settings.provider.codex.codexModelProvider': 'openrouter',
      'settings.activePromptKey': 'main/reviewer',
    }[key] ?? null));

    await expect(useClientStore.getState().resolveLaunchPreferences('/repo/app')).resolves.toEqual({
      modelProvider: 'codex',
      model: 'gpt-5.5',
      effort: 'xhigh',
      prompt_key: 'main/reviewer',
      config: {
        codexHome: '/Users/test/.codex-alt',
        codexInstanceKey: 'desktop-main',
        codexModelProvider: 'openrouter',
      },
    });
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

  it('starts a selected-provider thread instead of sending into a failed active session', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-failed',
      provider: 'claude',
      draft: 'Retry through selected provider',
      attachments: [],
      threads: [{ id: 'thread-failed', name: 'Broken', provider: 'codex', status: 'failed' }],
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'claude',
      'settings.provider.claude.model': 'sonnet',
      'settings.provider.claude.effort': 'high',
    }[key] ?? null));
    backend.startThread.mockResolvedValue({ threadId: 'thread-claude' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      modelProvider: 'claude',
      model: 'sonnet',
      effort: 'high',
      deferSpawn: true,
    }));
    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-claude',
      input: [{ type: 'text', text: 'Retry through selected provider' }],
      manualSkillSelection: false,
    });
    expect(useClientStore.getState().activeThreadId).toBe('thread-claude');
  });

  it('does not auto-recover or retry turn/start when the backend session is missing', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      draft: 'Retry on missing session',
      attachments: [],
    });
    backend.startTurn
      .mockRejectedValueOnce(new Error('session not found for agent "agent_123"'));
    backend.recoverThread.mockResolvedValue({ recovered: true });

    await expect(useClientStore.getState().sendDraft()).rejects.toThrow('session not found for agent "agent_123"');

    expect(backend.recoverThread).not.toHaveBeenCalled();
    expect(backend.startTurn).toHaveBeenCalledTimes(1);
    expect(useClientStore.getState().draft).toBe('Retry on missing session');
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

  it('preserves the selected Claude provider when runtime patches omit provider metadata', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      provider: 'claude',
      activeThreadId: 'thread-claude',
      threads: [{ id: 'thread-claude', name: 'Claude chat', provider: 'claude', status: 'running' }],
    });
    useClientStore.getState().initializeEvents();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-claude',
        sequence: '1',
        status: 'error',
        statusDetails: 'API Error: Unable to connect to API (ConnectionRefused)',
      },
    });

    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({
      id: 'thread-claude',
      provider: 'claude',
      status: 'error',
    }));
  });

  it('ignores late bridge patches from a different cwd after project switching', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/other',
      activeThreadId: 'thread-other',
      threads: [{ id: 'thread-other', name: 'Other project chat', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-other': [{ id: 'other-user', role: 'user', text: 'other cwd message' }] },
    });
    useClientStore.getState().initializeEvents();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-old',
        source: 'turn/completed',
        sequence: '1',
        status: 'running',
        thread: { id: 'thread-old', name: 'Old cwd chat' },
        agentRuntime: { cwd: '/repo/app', provider: 'codex' },
        timelineItems: [{ id: 'old-assistant', kind: 'assistant', text: 'old cwd reply' }],
      },
    });

    const state = useClientStore.getState();
    expect(state.activeThreadId).toBe('thread-other');
    expect(state.threads).toEqual([
      expect.objectContaining({ id: 'thread-other', name: 'Other project chat' }),
    ]);
    expect(state.timelinesByThread).not.toHaveProperty('thread-old');
    expect(state.warningEntries[0]).toEqual(expect.objectContaining({
      level: 'warn',
      event: 'thread.patch.cwd_mismatch',
    }));
  });

  it('increments workflow revision from task and cron bridge events', () => {
    useClientStore.getState().initializeEvents();

    expect(useClientStore.getState().workflowRevision).toBe(0);
    bridgeCallback({
      type: 'task/node/statusChanged',
      payload: { dag_key: 'flow-a', node_key: 'step', new_status: 'running' },
    });
    expect(useClientStore.getState().workflowRevision).toBe(1);

    bridgeCallback({
      method: 'cron/job/runStateChanged',
      payload: { job_id: 'job-1', run_id: 'run-1', status: 'running' },
    });
    expect(useClientStore.getState().workflowRevision).toBe(2);
  });

  it('increments prompt revision from prompt and active-prompt preference bridge events', () => {
    useClientStore.getState().initializeEvents();

    expect(useClientStore.getState().promptRevision).toBe(0);
    bridgeCallback({
      type: 'ui/preferences/changed',
      payload: { key: 'settings.activePromptKey', value: 'main/reviewer' },
    });
    expect(useClientStore.getState().promptRevision).toBe(1);

    bridgeCallback({
      type: 'ui/preferences/changed',
      payload: { key: 'settings.provider.active', value: 'codex' },
    });
    expect(useClientStore.getState().promptRevision).toBe(1);

    bridgeCallback({
      type: 'prompts/changed',
      payload: { cwd: '/repo/app' },
    });
    expect(useClientStore.getState().promptRevision).toBe(2);
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

  it('deletes a provisional backend thread when the first turn fails', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Clean up provisional thread',
      attachments: [],
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexHome': '/Users/test/.codex-alt',
      'settings.provider.codex.codexInstanceKey': 'desktop-main',
      'settings.provider.codex.codexModelProvider': 'openrouter',
    }[key] ?? null));
    backend.startThread.mockResolvedValue({ threadId: 'thread-provisional' });
    backend.startTurn.mockRejectedValue(new Error('turn/start failed'));

    await expect(useClientStore.getState().sendDraft()).rejects.toThrow('turn/start failed');

    expect(backend.deleteThread).toHaveBeenCalledWith({ threadId: 'thread-provisional' });
    expect(useClientStore.getState().draft).toBe('Clean up provisional thread');
  });

  it('keeps sending fail-fast when cwd is missing', async () => {
    resetClientStoreForTests({
      cwd: '',
      activeProject: '',
      activeThreadId: '',
      draft: 'Do not send without cwd',
      attachments: [],
    });

    await expect(useClientStore.getState().sendDraft()).rejects.toThrow(
      'frontend-app: cwd is required for send message',
    );

    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).not.toHaveBeenCalled();
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

  it('does not duplicate an assistant reply when patch and completion carry the same answer with different ids', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
      timelinesByThread: {
        'thread-1': [{ id: 'user-1', role: 'user', text: '怎么没有内容了', time: '2026-05-30T00:00:00Z' }],
      },
    });
    useClientStore.getState().initializeEvents();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '1',
        timelineItems: [{
          id: 'assistant-from-patch',
          kind: 'assistant',
          text: '你是指：\n\n1. 页面/应用里没有内容了？\n2. 某个文件被清空了？',
          createdAt: '2026-05-30T00:01:00Z',
        }],
      },
    });
    bridgeCallback({
      method: 'item/completed',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        item: {
          id: 'assistant-from-completion',
          type: 'agentMessage',
          text: '你是指：1.页面/应用里没有内容了？2.某个文件被清空了？',
        },
      },
    });

    const assistantMessages = useClientStore.getState().timelinesByThread['thread-1']
      .filter((message) => message.role === 'assistant');
    expect(assistantMessages).toHaveLength(1);
    expect(assistantMessages[0]).toEqual(expect.objectContaining({
      id: 'assistant-from-patch',
      text: '你是指：\n\n1. 页面/应用里没有内容了？\n2. 某个文件被清空了？',
    }));
  });

  it('removes a compact runtime duplicate when a later patch carries the formatted assistant reply', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [
          { id: 'user-1', role: 'user', text: '怎么没有内容了', time: '2026-05-30T00:00:00Z' },
          {
            id: 'assistant-from-completion',
            role: 'assistant',
            text: '你是指：1.页面/应用里没有内容了？2.某个文件被清空了？',
            time: '2026-05-30T00:01:00Z',
            done: true,
            runtime: true,
          },
        ],
      },
    });
    useClientStore.getState().initializeEvents();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '1',
        timelineItems: [{
          id: 'assistant-from-patch',
          kind: 'assistant',
          text: '你是指：\n\n1. 页面/应用里没有内容了？\n2. 某个文件被清空了？',
          createdAt: '2026-05-30T00:01:00Z',
        }],
      },
    });

    const timeline = useClientStore.getState().timelinesByThread['thread-1'];
    expect(timeline).toEqual([
      expect.objectContaining({ id: 'user-1', role: 'user' }),
      expect.objectContaining({
        id: 'assistant-from-patch',
        role: 'assistant',
        text: '你是指：\n\n1. 页面/应用里没有内容了？\n2. 某个文件被清空了？',
      }),
    ]);
  });

  it('deduplicates compact assistant replies that arrive in the same backend timeline patch', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'user-1', role: 'user', text: '怎么没有内容了', time: '2026-05-30T00:00:00Z' }],
      },
    });
    useClientStore.getState().initializeEvents();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '1',
        timelineItems: [
          {
            id: 'assistant-compact',
            kind: 'assistant',
            text: '你是指：1.页面/应用里没有内容了？2.某个文件被清空了？',
            createdAt: '2026-05-30T00:01:00Z',
          },
          {
            id: 'assistant-formatted',
            kind: 'assistant',
            text: '你是指：\n\n1. 页面/应用里没有内容了？\n2. 某个文件被清空了？',
            createdAt: '2026-05-30T00:01:01Z',
          },
        ],
      },
    });

    const assistantMessages = useClientStore.getState().timelinesByThread['thread-1']
      .filter((message) => message.role === 'assistant');
    expect(assistantMessages).toEqual([
      expect.objectContaining({
        id: 'assistant-formatted',
        text: '你是指：\n\n1. 页面/应用里没有内容了？\n2. 某个文件被清空了？',
      }),
    ]);
  });

  it('applies fallback turn output deltas with empty stream as assistant message text', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'claude', status: 'running' }],
      timelinesByThread: {
        'thread-1': [{ id: 'user-1', role: 'user', text: 'say ok', time: '2026-05-30T00:00:00Z' }],
      },
    });
    useClientStore.getState().initializeEvents();

    bridgeCallback({
      method: 'turn/output/delta',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        delta: 'o',
        stream: '',
      },
    });
    bridgeCallback({
      method: 'turn/output/delta',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        delta: 'k',
        stream: '',
      },
    });
    bridgeCallback({
      method: 'turn/output/delta',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        delta: ' hidden reasoning',
        stream: 'reasoning',
      },
    });

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ role: 'user', text: 'say ok' }),
      expect.objectContaining({ id: 'assistant-stream-turn-1', role: 'assistant', text: 'ok', done: false }),
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
        activityStats: { lspCalls: 2, commands: 1, fileEdits: 1, toolCalls: { edit: 2, shell: 1 } },
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
    expect(state.activityStatsByThread['thread-1']).toEqual({
      lspCalls: 2,
      commands: 1,
      fileEdits: 1,
      toolCalls: { edit: 2, shell: 1 },
    });
    expect(state.diffTextByThread['thread-1']).toContain('diff --git');
    expect(state.warningEntries).toEqual([
      expect.objectContaining({ level: 'error', event: 'rpc.failed' }),
    ]);
  });

  it('keys bridge diff patches by nested thread id before agent id', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-1': [] },
    });
    useClientStore.getState().initializeEvents();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        agentId: 'agent-1',
        thread: { threadId: 'thread-1', agentId: 'agent-1' },
        diffText: 'diff --git a/src/App.jsx b/src/App.jsx',
      },
    });

    const state = useClientStore.getState();
    expect(state.diffTextByThread['thread-1']).toContain('diff --git');
    expect(state.diffTextByThread['agent-1']).toBeUndefined();
    expect(state.warningEntries).not.toEqual([
      expect.objectContaining({ event: 'thread.patch.unknown_thread' }),
    ]);
  });

  it('records tool result timeline items for the runtime log while preserving warnings', async () => {
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
        sequence: '9007199254740993124',
        timelineItems: [{
          id: 'tool-grep',
          kind: 'tool',
          tool: 'mcp__lsp__grep',
          status: 'completed',
          preview: '{"total":3,"files":{"src/App.jsx":2}}',
          output: 'src/App.jsx: found runtime log',
          ts: '2026-05-30T08:00:00Z',
        }],
      },
    });
    bridgeCallback({
      type: 'api.rpc.failed',
      payload: { method: 'thread/config/get', threadId: 'thread-1', error: 'backend unavailable' },
    });

    const state = useClientStore.getState();
    expect(state.warningEntries).toEqual([
      expect.objectContaining({ event: 'api.rpc.failed' }),
    ]);
    expect(state.runtimeResultEntries).toEqual([
      expect.objectContaining({
        event: 'tool.result',
        threadId: 'thread-1',
        message: expect.stringContaining('grep'),
        detail: expect.stringContaining('src/App.jsx: found runtime log'),
      }),
    ]);
  });

  it('does not surface stale or sparse thread patch sequences as warnings', () => {
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
        sequence: '611',
        timelineItems: [{ id: 'assistant-new', kind: 'assistant', text: 'new patch' }],
      },
    });
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '609',
        timelineItems: [{ id: 'assistant-stale', kind: 'assistant', text: 'stale patch' }],
      },
    });
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '789',
        tokenUsage: { usedTokens: 10, contextWindowTokens: 100 },
      },
    });

    const state = useClientStore.getState();
    expect(state.warningEntries.map((entry) => entry.event)).not.toContain('thread.patch.stale');
    expect(state.warningEntries.map((entry) => entry.event)).not.toContain('thread.patch.gap');
    expect(state.timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ id: 'assistant-new', text: 'new patch' }),
    ]);
    expect(state.tokenUsageByThread['thread-1']).toEqual(expect.objectContaining({
      usedTokens: 10,
    }));
  });

  it('coalesces repeated RPC warning entries while preserving occurrence count', () => {
    useClientStore.getState().addLog('error', 'api.rpc.failed', {
      method: 'thread/config/get',
      threadId: 'thread-1',
      req_id: 1,
      error: { message: 'backend unavailable' },
    });
    useClientStore.getState().addLog('error', 'api.rpc.failed', {
      method: 'thread/config/get',
      threadId: 'thread-1',
      req_id: 2,
      error: { message: 'backend unavailable' },
    });

    const warnings = useClientStore.getState().warningEntries;
    expect(warnings).toHaveLength(1);
    expect(warnings[0]).toEqual(expect.objectContaining({
      event: 'api.rpc.failed',
      occurrenceCount: 2,
      fields: expect.objectContaining({
        method: 'thread/config/get',
        req_id: 2,
      }),
    }));
  });

  it('coalesces repeated backend RPC return entries while preserving occurrence count', () => {
    useClientStore.getState().addLog('debug', 'api.rpc.done', {
      method: 'thread/messages',
      threadId: 'thread-1',
      req_id: 1,
      result_preview: '{"messages":[{"id":1}]}',
    });
    useClientStore.getState().addLog('debug', 'api.rpc.done', {
      method: 'thread/messages',
      threadId: 'thread-1',
      req_id: 2,
      result_preview: '{"messages":[{"id":1}]}',
    });

    const results = useClientStore.getState().runtimeResultEntries;
    expect(results).toHaveLength(1);
    expect(results[0]).toEqual(expect.objectContaining({
      event: 'api.rpc.done',
      occurrenceCount: 2,
      fields: expect.objectContaining({
        req_id: 2,
      }),
    }));
  });

  it('connects conversation card actions to backend RPCs with explicit cwd', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      activeTurnByThread: {
        'thread-1': { id: 'turn-1', threadId: 'thread-1', status: 'running' },
      },
    });

    await useClientStore.getState().interruptActiveThread();
    await useClientStore.getState().compactActiveThread();
    await useClientStore.getState().recoverActiveThread();
    await useClientStore.getState().renameThread('thread-1', 'Renamed');
    await useClientStore.getState().archiveThread('thread-1', true);

    expect(backend.interruptTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1', source: 'ui_stop' });
    expect(backend.compactThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
    expect(backend.recoverThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
    expect(backend.renameThread).toHaveBeenCalledWith({ threadId: 'thread-1', name: 'Renamed' });
    expect(backend.archiveThread).toHaveBeenCalledWith({ threadId: 'thread-1' });
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'archivedThreadAtById.thread-1',
      value: expect.any(Number),
    });
  });

  it('calls interrupt for the selected running thread without cached active turn', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '运行线程', provider: 'codex', status: 'running' }],
    });

    await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(true);

    expect(backend.interruptTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1', source: 'ui_stop' });
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
