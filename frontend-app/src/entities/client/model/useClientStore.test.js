import { systemClockMillis, parseRequiredJsonObject } from './contractStoreModel.js';
import { beforeEach, expect, it, vi } from 'vitest';

function optionalUiArray() {
  return [];
}

function interruptSuccessResult({ expectedTurnId, requestId }) {
  return {
    ok: true,
    accepted: true,
    requestId,
    expectedTurnId,
    turnId: expectedTurnId,
    status: 'interrupted',
    confirmed: true,
    mode: 'interrupt_confirmed',
    interruptSent: true,
    stateBefore: 'running',
    stateAfter: 'idle',
    waitedMs: 1,
    activeObserved: true,
  };
}

let bridgeCallback;
let bridgeOptions;
let runtimeReconnectCallback;

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
  forkThread: vi.fn(),
  startThread: vi.fn(),
  startTurn: vi.fn(),
  interruptTurn: vi.fn(),
  forceCompleteTurn: vi.fn(),
  respondApproval: vi.fn(),
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
  saveClipboardImage: vi.fn(),
  beginTextClipboardWrite: vi.fn(),
  copyTextToClipboard: vi.fn(),
  emitFrontendTraceEvent: vi.fn(),
  listSharedFiles: vi.fn(),
  readSharedFile: vi.fn(),
  onBridgeEvent: vi.fn((callback, options = {}) => {
    bridgeCallback = callback;
    bridgeOptions = options;
    return () => {
      bridgeCallback = null;
      bridgeOptions = null;
    };
  }),
  onRuntimeReconnect: vi.fn((callback) => {
    runtimeReconnectCallback = callback;
    return () => {
      runtimeReconnectCallback = null;
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

async function flushPromises(count = 8) {
  for (let i = 0; i < count; i += 1) {
    await Promise.resolve();
  }
}

async function flushAssistantDeltaBatch() {
  vi.advanceTimersByTime(50);
  await flushPromises();
}

vi.mock('../../../shared/api/backendApi.js', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...backend,
    registerBridgeLogStore: actual.registerBridgeLogStore,
    sendFrontendLogBatch: vi.fn(),
  };
});

import { resetClientStoreForTests, setClientStoreClockMillisForTests, useClientStore } from './useClientStore.js';
import * as frontendBreadcrumbs from '../../../shared/diagnostics/frontendBreadcrumbs.js';

function diagnosticBreadcrumbs() {
  return frontendBreadcrumbs.snapshotFrontendBreadcrumbsForTests().map(({ actionCode, routeId, phase }) => ({
    actionCode,
    routeId,
    phase,
  }));
}

const boundCapabilities = [
  {
    kind: 'skill',
    key: 'skill:project::review:/repo/app/.agents/skills/review',
    name: 'review',
    label: 'Code Review',
    availability: 'ready',
    ref: {
      name: 'review',
      scope: 'project',
      personalType: '',
      path: '/repo/app/.agents/skills/review',
    },
  },
  {
    kind: 'mcp_tool',
    key: 'mcp_tool:lsp:lsp_edit',
    name: 'lsp_edit',
    label: 'LSP Edit',
    serverName: 'lsp',
    availability: 'ready',
  },
];

function registerBridgeEventHandlersForTest() {
  const initialization = useClientStore.getState().initializeEvents();
  void initialization.catch((error) => {
    if (error?.message !== 'runtime event initialization superseded') throw error;
  });
  return initialization;
}

  beforeEach(() => {
    vi.clearAllMocks();
    frontendBreadcrumbs.resetFrontendBreadcrumbsForTests?.();
    bridgeCallback = null;
    bridgeOptions = null;
    runtimeReconnectCallback = null;
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
    backend.interruptTurn.mockImplementation((params) => Promise.resolve(interruptSuccessResult(params)));
    backend.forceCompleteTurn.mockResolvedValue({ confirmed: true });
    backend.recoverThread.mockResolvedValue({ recovered: true });
    backend.respondApproval.mockResolvedValue(null);
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
    backend.listSharedFiles.mockResolvedValue({ files: [] });
    backend.readSharedFile.mockImplementation(({ path }) => Promise.resolve({ path, content: `content for ${path}` }));
  });

  it('reports log level preference save failures without changing the selected level', () => {
    const setItemSpy = vi.spyOn(window.localStorage, 'setItem').mockImplementation(() => {
      throw new Error('storage denied');
    });
    try {
      expect(() => useClientStore.getState().setLogLevel('error')).toThrow('storage denied');

      const state = useClientStore.getState();
      expect(state.logLevel).toBe('info');
      expect(state.warningEntries).toEqual(expect.arrayContaining([
        expect.objectContaining({
          level: 'error',
          event: 'log_level.preference_save.failed',
          fields: expect.objectContaining({
            status: 'storage_write_failed',
          }),
        }),
      ]));
    } finally {
      setItemSpy.mockRestore();
    }
  });

  it('keeps composer file selection on plain path arrays without picker tokens', async () => {
    backend.selectFiles.mockResolvedValue(['/tmp/plain.txt']);

    const attachments = await useClientStore.getState().selectFilesForComposer();

    expect(backend.selectFiles).toHaveBeenCalledWith();
    expect(attachments).toEqual([expect.objectContaining({
      path: '/tmp/plain.txt',
      name: 'plain.txt',
    })]);
    expect(useClientStore.getState().attachments).toEqual([expect.objectContaining({
      path: '/tmp/plain.txt',
      name: 'plain.txt',
    })]);
  });

  it('classifies composer file selection failures as attachment errors', async () => {
    backend.selectFiles.mockRejectedValue(new Error('picker unavailable'));

    await expect(useClientStore.getState().selectFilesForComposer()).rejects.toThrow('picker unavailable');

    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      category: 'attachment',
      message: '选择附件失败，请重试。',
      tone: 'error',
    }));
    expect(JSON.stringify(useClientStore.getState().actionNotice)).not.toContain('picker unavailable');
  });

  it('bootstraps through config, window, projects, and sidebar without blocking on thread snapshot', async () => {
    await useClientStore.getState().bootstrap();

    expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.active' });
    expect(backend.getProjects).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(backend.getThreadState).not.toHaveBeenCalled();

    const state = useClientStore.getState();
    expect(state.cwd).toBe('/repo/app');
    expect(state.activeProject).toBe('/repo/app');
    expect(state.provider).toBe('codex');
    expect(state.threads).toHaveLength(1);
    expect(state.tokenUsageByThread['thread-1'].usedTokens).toBe(42);
  });

  it('keeps sidebar tokenUsageByThread ahead of stale global token_usage', async () => {
    backend.getSidebarState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: 'Active', provider: 'codex', status: 'running' },
        { id: 'thread-2', name: 'Other', provider: 'codex', status: 'idle' },
      ],
      token_usage: { usedTokens: 999, contextWindowTokens: 2000, usedPercent: 50 },
      tokenUsageByThread: {
        'thread-1': { usedTokens: 42, contextWindowTokens: 100, usedPercent: 42 },
        'thread-2': { usedTokens: 70, contextWindowTokens: 400, usedPercent: 17.5 },
      },
    });

    await useClientStore.getState().bootstrap();

    const state = useClientStore.getState();
    expect(state.tokenUsageByThread['thread-1']).toEqual({
      usedTokens: 42,
      contextWindowTokens: 100,
      usedPercent: 42,
    });
    expect(state.tokenUsageByThread['thread-2']).toEqual({
      usedTokens: 70,
      contextWindowTokens: 400,
      usedPercent: 17.5,
    });
  });

  it('records each central active-page transition once and ignores same-page updates', () => {
    resetClientStoreForTests({ activePage: 'chat' });

    useClientStore.getState().setActivePage('settings');
    useClientStore.getState().setActivePage('settings');
    useClientStore.getState().setActivePage('memory');

    expect(useClientStore.getState().activePage).toBe('memory');
    expect(diagnosticBreadcrumbs()).toEqual([
      { actionCode: 'app.navigation', routeId: 'settings', phase: 'complete' },
      { actionCode: 'app.navigation', routeId: 'memory', phase: 'complete' },
    ]);
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

    expect(useClientStore.getState().bootstrapStatus).toBe('failed');
  });

  it('bootstraps when the selected provider model preference is missing', async () => {
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexModelProvider': 'openai',
    }[key] ?? null));

    await useClientStore.getState().bootstrap();

    expect(useClientStore.getState().bootstrapStatus).toBe('ready');
    expect(useClientStore.getState().providerConfig).toEqual(expect.objectContaining({
      provider: 'codex',
      model: '',
      effort: 'xhigh',
    }));
  });

  it('bootstraps when optional Codex model provider preference is absent', async () => {
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexHome': '~/.codex',
      'settings.provider.codex.codexInstanceKey': 'default',
    }[key] ?? null));

    await useClientStore.getState().bootstrap();

    expect(useClientStore.getState().bootstrapStatus).toBe('ready');
    expect(backend.getProjects).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(backend.getThreadState).not.toHaveBeenCalled();
    expect(useClientStore.getState().providerConfig).toEqual(expect.objectContaining({
      provider: 'codex',
      model: 'gpt-5.5',
      effort: 'xhigh',
      codexModelProvider: '',
    }));
  });

  it('retries bootstrap after a transient cold-start runtime reconnect', async () => {
    backend.readConfig
      .mockRejectedValueOnce(new Error('Wails runtime bridge not ready'))
      .mockResolvedValue({ cwd: '/repo/app' });

    await expect(useClientStore.getState().bootstrap()).rejects.toThrow('Wails runtime bridge not ready');
    expect(useClientStore.getState().bootstrapStatus).toBe('failed');
    expect(runtimeReconnectCallback).toEqual(expect.any(Function));

    runtimeReconnectCallback();
    await vi.waitFor(() => {
      expect(backend.readConfig).toHaveBeenCalledTimes(2);
      expect(useClientStore.getState().bootstrapStatus).toBe('ready');
    });
    expect(useClientStore.getState().activeProject).toBe('/repo/app');
  });

  it('keeps the previous bootstrap error visible while an explicit retry is loading', async () => {
    const retryConfig = deferred();
    backend.readConfig
      .mockRejectedValueOnce(new Error('event bridge unavailable'))
      .mockReturnValueOnce(retryConfig.promise);

    await expect(useClientStore.getState().bootstrap()).rejects.toThrow('event bridge unavailable');

    const retryPromise = useClientStore.getState().bootstrap();
    expect(useClientStore.getState().bootstrapStatus).toBe('loading');
    expect(useClientStore.getState().error).toBe('连接后端失败，请重试。');

    retryConfig.resolve({ cwd: '/repo/app' });
    await retryPromise;
    expect(useClientStore.getState().bootstrapStatus).toBe('ready');
    expect(useClientStore.getState().error).toBe('');
  });

  it('retries bootstrap when runtime reconnect arrives before the first cold-start RPC fails', async () => {
    const firstConfig = deferred();
    backend.readConfig
      .mockReturnValueOnce(firstConfig.promise)
      .mockResolvedValue({ cwd: '/repo/app' });

    const bootstrapPromise = useClientStore.getState().bootstrap();
    await flushPromises(2);
    expect(useClientStore.getState().bootstrapStatus).toBe('loading');
    expect(runtimeReconnectCallback).toEqual(expect.any(Function));

    runtimeReconnectCallback();
    firstConfig.reject(new Error('runtime shim: failed to connect ws://127.0.0.1:5175/wails/ws'));
    await expect(bootstrapPromise).rejects.toThrow('runtime shim: failed to connect');
    await flushPromises();

    expect(backend.readConfig).toHaveBeenCalledTimes(2);
    expect(useClientStore.getState().bootstrapStatus).toBe('ready');
    expect(useClientStore.getState().activeProject).toBe('/repo/app');
  });

  it('waits for both runtime subscriptions before the first bootstrap RPC', async () => {
    const bridgeReady = deferred();
    const reconnectReady = deferred();
    backend.onBridgeEvent.mockImplementationOnce((callback, options = {}) => {
      bridgeCallback = callback;
      bridgeOptions = options;
      return { ready: bridgeReady.promise, unsubscribe: vi.fn() };
    });
    backend.onRuntimeReconnect.mockImplementationOnce((callback) => {
      runtimeReconnectCallback = callback;
      return { ready: reconnectReady.promise, unsubscribe: vi.fn() };
    });

    const bootstrapPromise = useClientStore.getState().bootstrap();
    await flushPromises();
    expect(backend.readConfig).not.toHaveBeenCalled();
    expect(backend.getWindowBootstrap).not.toHaveBeenCalled();

    bridgeReady.resolve(true);
    await flushPromises();
    expect(backend.readConfig).not.toHaveBeenCalled();

    reconnectReady.resolve(true);
    await bootstrapPromise;
    expect(backend.readConfig).toHaveBeenCalledTimes(1);
    expect(backend.getWindowBootstrap).toHaveBeenCalledTimes(1);
  });

  it('fails bootstrap before RPCs when runtime subscription readiness is unavailable', async () => {
    backend.onBridgeEvent.mockImplementationOnce(() => ({
      ready: Promise.resolve(true),
      unsubscribe: vi.fn(),
    }));
    backend.onRuntimeReconnect.mockImplementationOnce(() => ({
      ready: Promise.resolve(false),
      unsubscribe: vi.fn(),
    }));

    await expect(useClientStore.getState().bootstrap()).rejects.toThrow(
      'runtime.reconnect.subscribe unavailable',
    );

    expect(backend.readConfig).not.toHaveBeenCalled();
    expect(backend.getWindowBootstrap).not.toHaveBeenCalled();
    expect(useClientStore.getState().bootstrapStatus).toBe('failed');
    expect(useClientStore.getState().error).toBe('连接后端失败，请重试。');
  });

  it('preserves a live bridge status over a stale bootstrap sidebar snapshot', async () => {
    const sidebar = deferred();
    resetClientStoreForTests({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
    });
    backend.getSidebarState.mockReturnValueOnce(sidebar.promise);

    const bootstrapPromise = useClientStore.getState().bootstrap();
    await vi.waitFor(() => {
      expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/app' });
    });
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: 'bootstrap-live',
        status: 'running',
        interruptible: true,
        activeTurn: { id: 'turn-live', threadId: 'thread-1', status: 'running' },
        thread: { name: 'Existing' },
      },
    });
    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({
      id: 'thread-1',
      status: 'running',
    }));

    sidebar.resolve({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
    });
    await bootstrapPromise;

    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({
      id: 'thread-1',
      status: 'running',
    }));
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

  it('keeps the desktop active provider locked to Codex', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      provider: 'codex',
    });

    await expect(useClientStore.getState().toggleProviderMode()).resolves.toBe(false);

    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.active',
    }));
    expect(useClientStore.getState().provider).toBe('codex');
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '当前桌面仅支持 Codex provider',
      tone: 'warning',
    }));
    expect(useClientStore.getState().warningEntries).toEqual([
      expect.objectContaining({
        level: 'warn',
        event: 'provider.toggle.unsupported',
        fields: { requestedProvider: 'claude' },
      }),
    ]);
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

  it('keeps provider toggle disabled without requiring cwd', async () => {
    resetClientStoreForTests({
      cwd: '',
      activeProject: '',
      provider: 'codex',
    });

    await expect(useClientStore.getState().toggleProviderMode()).resolves.toBe(false);

    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.active',
    }));
    expect(useClientStore.getState().provider).toBe('codex');
  });

  it('routes project selector actions through the project RPC contract', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      projects: ['/repo/app', '/repo/other'],
    });
    backend.setActiveProject.mockImplementation(({ path }) => Promise.resolve({
      projects: path === '/repo/new' ? ['/repo/app', '/repo/other', '/repo/new'] : ['/repo/app', '/repo/other'],
      active: path,
    }));
    backend.addProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other', '/repo/new'], active: '/repo/other' });
    backend.removeProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });

    await expect(useClientStore.getState().setActiveProjectPath('/repo/other')).resolves.toBe(true);
    expect(backend.setActiveProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/other' });
    expect(useClientStore.getState().activeProject).toBe('/repo/other');

    await expect(useClientStore.getState().addProjectFromPicker()).resolves.toBe(true);
    expect(backend.selectProjectDir).toHaveBeenCalledWith('/repo/other');
    expect(backend.addProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/new' });
    expect(backend.setActiveProject).toHaveBeenLastCalledWith({ cwd: '/repo/app', path: '/repo/new' });
    expect(useClientStore.getState().activeProject).toBe('/repo/new');

    await expect(useClientStore.getState().removeProjectPath('/repo/new')).resolves.toBe(true);
    expect(backend.removeProject).toHaveBeenCalledWith({ cwd: '/repo/app', path: '/repo/new' });
    expect(useClientStore.getState().projects).toEqual(['/repo/app', '/repo/other']);
  });

  it('restores the project selector state when setActiveProject RPC fails', async () => {
    backend.setActiveProject.mockRejectedValueOnce(new Error('project backend offline'));
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      projects: ['/repo/app', '/repo/other'],
    });

    await expect(useClientStore.getState().setActiveProjectPath('/repo/other')).rejects.toThrow('project backend offline');

    expect(useClientStore.getState().activeProject).toBe('/repo/app');
    expect(useClientStore.getState().projects).toEqual(['/repo/app', '/repo/other']);
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '切换项目失败，请重试。',
      tone: 'error',
    }));
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

  it('keeps thread state tokenUsageByThread ahead of stale global token_usage', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Active', provider: 'codex', status: 'running' }],
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Active', provider: 'codex', status: 'running' }],
      token_usage: { usedTokens: 999, contextWindowTokens: 2000, usedPercent: 50 },
      tokenUsageByThread: {
        'thread-1': { usedTokens: 42, contextWindowTokens: 100, usedPercent: 42 },
      },
      timelinesByThread: { 'thread-1': [] },
    });
    backend.getThreadMessages.mockResolvedValueOnce({ messages: [] });

    await expect(useClientStore.getState().syncThreadState('thread-1')).resolves.toBe(true);

    expect(useClientStore.getState().tokenUsageByThread['thread-1']).toEqual({
      usedTokens: 42,
      contextWindowTokens: 100,
      usedPercent: 42,
    });
  });

  it('normalizes Go UI state status maps into the realtime status entry shape', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-wire',
      threads: [{ id: 'thread-wire', agentId: 'agent-wire', name: 'Wire thread', provider: 'codex', status: 'idle' }],
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-wire',
      threads: [{ id: 'thread-wire', agent_id: 'agent-wire', name: 'Wire thread', state: 'running' }],
      agents: [{ id: 'agent-wire', thread_id: 'thread-wire', provider: 'codex', state: 'running' }],
      statuses: { 'thread-wire': 'running' },
      statusHeadersByThread: { 'thread-wire': 'Thinking' },
      statusDetailsByThread: { 'thread-wire': 'Inspecting snapshot state' },
      interruptibleByThread: { 'thread-wire': true },
      activityStatsByThread: {
        'thread-wire': { lspCalls: 2, commands: 3, fileEdits: 1, toolCalls: { read: 4 } },
      },
      agentRuntimeById: {
        'thread-wire': {
          agentId: 'agent-wire',
          state: 'running',
          provider: 'codex',
          providerThreadId: 'provider-thread-wire',
        },
      },
      timelinesByThread: { 'thread-wire': [] },
    });
    backend.getThreadMessages.mockResolvedValueOnce({ messages: [] });

    await expect(useClientStore.getState().syncThreadState('thread-wire')).resolves.toBe(true);

    expect(useClientStore.getState().statuses['thread-wire']).toEqual({
      status: 'running',
      statusHeader: 'Thinking',
      statusDetails: 'Inspecting snapshot state',
      interruptible: true,
      activityStats: { lspCalls: 2, commands: 3, fileEdits: 1, toolCalls: { read: 4 } },
      agentRuntime: {
        agentId: 'agent-wire',
        state: 'running',
        provider: 'codex',
        providerThreadId: 'provider-thread-wire',
      },
    });
  });

  it('preserves rich status fields when a same-status snapshot omits the parallel maps', async () => {
    const richStatus = {
      status: 'running',
      statusHeader: 'Thinking',
      statusDetails: 'Inspecting live state',
      interruptible: true,
      activityStats: { lspCalls: 2, commands: 3, fileEdits: 1, toolCalls: { read: 4 } },
      agentRuntime: {
        agentId: 'agent-wire',
        state: 'running',
        provider: 'codex',
        providerThreadId: 'provider-thread-wire',
      },
    };
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-wire',
      threads: [{ id: 'thread-wire', agentId: 'agent-wire', name: 'Wire thread', provider: 'codex', status: 'running' }],
      statuses: { 'thread-wire': richStatus },
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-wire',
      threads: [{ id: 'thread-wire', agent_id: 'agent-wire', name: 'Wire thread', state: 'running' }],
      agents: [{ id: 'agent-wire', thread_id: 'thread-wire', provider: 'codex', state: 'running' }],
      statuses: { 'thread-wire': 'running' },
      timelinesByThread: { 'thread-wire': [] },
    });
    backend.getThreadMessages.mockResolvedValueOnce({ messages: [] });

    await expect(useClientStore.getState().syncThreadState('thread-wire')).resolves.toBe(true);

    expect(useClientStore.getState().statuses['thread-wire']).toEqual(richStatus);
  });

  it('uses the active provider for deferred workflow designer threads without runtime metadata', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-design',
      provider: 'codex',
      threads: [],
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-design',
      threads: [{ id: 'thread-design', name: 'AI 设计流程', status: 'created', agentKey: 'dag_designer' }],
      timelinesByThread: { 'thread-design': [] },
    });
    backend.getThreadMessages.mockResolvedValueOnce({ messages: [] });

    await expect(useClientStore.getState().syncThreadState('thread-design')).resolves.toBe(true);

    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({
      id: 'thread-design',
      name: 'AI 设计流程',
      provider: 'codex',
      agentKey: 'dag_designer',
    }));
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

  it('preserves a thread selected while project switch sidebar refresh is still in flight', async () => {
    const sidebarRefresh = deferred();
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      projects: ['/repo/app', '/repo/other'],
      activeThreadId: 'thread-old',
      threads: [{ id: 'thread-old', name: 'Old project thread', provider: 'codex', status: 'idle', cwd: '/repo/app' }],
    });
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    backend.getSidebarState.mockReturnValue(sidebarRefresh.promise);
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-other',
      threads: [{ id: 'thread-other', name: 'Other project thread', provider: 'codex', status: 'idle', cwd: '/repo/other' }],
      timelinesByThread: {
        'thread-other': [{ id: 'message-thread-other', role: 'assistant', text: 'other message', time: '2026-06-18T00:00:00Z' }],
      },
    });
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    await expect(useClientStore.getState().setActiveProjectPath('/repo/other', {
      preserveActiveThreadId: true,
    })).resolves.toBe(true);
    await expect(useClientStore.getState().setActiveThread('thread-other')).resolves.toBe(true);
    expect(useClientStore.getState().activeThreadId).toBe('thread-other');

    sidebarRefresh.resolve({
      activeThreadId: '',
      threads: [{ id: 'thread-other', name: 'Other project thread', provider: 'codex', status: 'idle', cwd: '/repo/other' }],
    });
    await flushPromises();

    expect(useClientStore.getState().activeThreadId).toBe('thread-other');
  });

  it('does not shrink the sidebar project cache from a thread-scoped state sync', async () => {
    const threads = [
      { id: 'thread-a', name: 'Thread A', provider: 'codex', status: 'idle', cwd: '/repo/app' },
      { id: 'thread-b', name: 'Thread B', provider: 'codex', status: 'idle', cwd: '/repo/app' },
    ];
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      projects: ['/repo/app'],
      activeThreadId: 'thread-a',
      threads,
      sidebarThreadsByProject: {
        '/repo/app': threads,
      },
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-b',
      threads: [threads[1]],
      timelinesByThread: {
        'thread-b': [{ id: 'message-thread-b', role: 'assistant', text: 'thread b message', time: '2026-06-18T00:00:00Z' }],
      },
    });
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    await expect(useClientStore.getState().setActiveThread('thread-b')).resolves.toBe(true);

    expect(useClientStore.getState().threads).toEqual([
      expect.objectContaining({ id: 'thread-b', name: 'Thread B' }),
    ]);
    expect(useClientStore.getState().sidebarThreadsByProject['/repo/app']).toEqual([
      expect.objectContaining({ id: 'thread-a', name: 'Thread A' }),
      expect.objectContaining({ id: 'thread-b', name: 'Thread B' }),
    ]);
  });

  it('starts a clear-surface sidebar refresh without waiting for a background refresh', async () => {
    const backgroundRefresh = deferred();
    const clearSurfaceRefresh = deferred();
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/other',
      projects: ['/repo/app', '/repo/other'],
      activeThreadId: 'thread-other',
      threads: [{ id: 'thread-other', name: 'Other project thread', provider: 'codex', status: 'running' }],
    });
    backend.getSidebarState
      .mockReturnValueOnce(backgroundRefresh.promise)
      .mockReturnValueOnce(clearSurfaceRefresh.promise);
    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    registerBridgeEventHandlersForTest();

    bridgeCallback({ type: 'ui/sidebar/changed', payload: { revision: 2 } });
    expect(backend.getSidebarState).toHaveBeenCalledTimes(1);

    const switchPromise = useClientStore.getState().setActiveProjectPath('/repo/other');
    await Promise.resolve();

    expect(backend.getSidebarState).toHaveBeenCalledTimes(2);
    expect(useClientStore.getState()).toEqual(expect.objectContaining({
      activeProject: '/repo/other',
      activeThreadId: '',
      chatSurfaceLoadingCwd: '/repo/other',
    }));
    expect(useClientStore.getState().threads).toEqual([]);

    clearSurfaceRefresh.resolve({
      activeThreadId: 'thread-clear',
      threads: [{ id: 'thread-clear', name: 'Clear refresh thread', provider: 'claude', status: 'idle' }],
    });

    await vi.waitFor(() => {
      expect(useClientStore.getState().threads).toEqual([
        expect.objectContaining({ id: 'thread-clear', name: 'Clear refresh thread' }),
      ]);
    });
    expect(useClientStore.getState().chatSurfaceLoadingCwd).toBe('');

    backgroundRefresh.resolve({
      activeThreadId: 'thread-stale',
      threads: [{ id: 'thread-stale', name: 'Stale background thread', provider: 'codex', status: 'running' }],
    });
    await Promise.resolve();
    await Promise.resolve();

    expect(useClientStore.getState().threads).toEqual([
      expect.objectContaining({ id: 'thread-clear', name: 'Clear refresh thread' }),
    ]);
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

  it('keeps runtime cwd threads when Windows separators differ from the selected project path', async () => {
    resetClientStoreForTests({
      cwd: 'C:/Users/ai03/Desktop/Super-Dolphin',
      projectScopeCwd: 'C:/Users/ai03/Desktop/Super-Dolphin',
      activeProject: 'C:/Users/ai03/Desktop/Super-Dolphin',
      projects: ['C:/Users/ai03/Desktop/Super-Dolphin'],
    });
    backend.setActiveProject.mockResolvedValue({
      projects: ['C:/Users/ai03/Desktop/Super-Dolphin'],
      active: 'C:/Users/ai03/Desktop/Super-Dolphin',
    });
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'agent-win',
      threads: [
        { id: 'agent-win', agent_id: 'agent-win', name: 'Windows cwd thread', provider: 'codex', status: 'idle' },
      ],
      agentRuntimeById: {
        'agent-win': {
          cwd: 'C:\\Users\\ai03\\Desktop\\Super-Dolphin',
          provider: 'codex',
          providerThreadId: 'session-win',
        },
      },
    });

    await expect(useClientStore.getState().setActiveProjectPath('C:/Users/ai03/Desktop/Super-Dolphin')).resolves.toBe(true);

    expect(useClientStore.getState().threads).toEqual([
      expect.objectContaining({
        id: 'agent-win',
        cwd: 'C:\\Users\\ai03\\Desktop\\Super-Dolphin',
        name: 'Windows cwd thread',
      }),
    ]);
  });

  it('keeps composer drafts isolated by selected thread and project cwd', async () => {
    const reviewCapability = {
      kind: 'skill',
      key: 'skill:project::review:/repo/app/.agents/skills/review',
      name: 'review',
      label: 'Code Review',
      availability: 'ready',
      ref: {
        name: 'review',
        scope: 'project',
        personalType: '',
        path: '/repo/app/.agents/skills/review',
      },
    };
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
      composerCapabilities: [reviewCapability],
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
    expect(useClientStore.getState().composerCapabilities).toEqual([]);

    useClientStore.getState().setDraft('draft for B');

    await useClientStore.getState().setActiveThread('thread-a');
    expect(useClientStore.getState().draft).toBe('draft for A');
    expect(useClientStore.getState().attachments).toEqual([
      expect.objectContaining({ path: '/tmp/a.txt', name: 'a.txt' }),
    ]);
    expect(useClientStore.getState().composerCapabilities).toEqual([
      expect.objectContaining({
        key: reviewCapability.key,
        availability: 'unverified',
      }),
    ]);

    backend.setActiveProject.mockResolvedValue({ projects: ['/repo/app', '/repo/other'], active: '/repo/other' });
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: '',
      threads: [{ id: 'thread-other', cwd: '/repo/other', name: 'Other project thread', provider: 'claude', status: 'idle' }],
    });
    await useClientStore.getState().setActiveProjectPath('/repo/other');

    expect(useClientStore.getState().draft).toBe('');
    expect(useClientStore.getState().attachments).toEqual([]);
    expect(useClientStore.getState().composerCapabilities).toEqual([]);

    backend.setActiveProject.mockResolvedValueOnce({
      projects: ['/repo/app', '/repo/other'],
      active: '/repo/app',
    });
    backend.getSidebarState.mockResolvedValueOnce({
      activeThreadId: '',
      threads: [
        { id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' },
        { id: 'thread-b', cwd: '/repo/app', name: 'Thread B', provider: 'codex', status: 'idle' },
      ],
    });
    await useClientStore.getState().setActiveProjectPath('/repo/app');
    await useClientStore.getState().setActiveThread('thread-a');
    expect(useClientStore.getState().composerCapabilities).toEqual([
      expect.objectContaining({
        key: reviewCapability.key,
        availability: 'unverified',
      }),
    ]);
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
    expect(backend.getThreadState).not.toHaveBeenCalled();
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

  it('opens backend-resolved DAG child threads even when the id looks like an agent runtime id', async () => {
    resetClientStoreForTests({
      cwd: '/repo/main',
      activeProject: '/repo/main',
      activeThreadId: 'thread-main',
      threads: [{ id: 'thread-main', name: 'Main', provider: 'codex', status: 'running' }],
    });
    backend.resolveThreadIdentity.mockResolvedValue({
      id: 'agent_child_1',
      agent_id: 'agent_child_1',
      name: 'Review child',
      provider: 'codex',
      cwd: '/repo/main/.worktrees/review-child',
      status: 'running',
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: '',
      threads: [{ id: 'thread-main', name: 'Main', provider: 'codex', status: 'running' }],
      timelinesByThread: {},
    });
    backend.getThreadMessages.mockResolvedValue({
      messages: [{ id: 'm-child', role: 'assistant', content: '子代理评审完成' }],
      hasMore: false,
      nextBefore: '',
    });

    await expect(useClientStore.getState().openThreadById('agent_child_1', { source: 'dag-node' })).resolves.toBe(true);

    expect(backend.resolveThreadIdentity).toHaveBeenCalledWith({ cwd: '/repo/main', threadId: 'agent_child_1' });
    expect(backend.getThreadState).toHaveBeenCalledWith({ cwd: '/repo/main', threadId: 'agent_child_1', includeDiff: false });
    expect(backend.getThreadMessages).toHaveBeenCalledWith({ threadId: 'agent_child_1', limit: 300 });
    expect(useClientStore.getState().activeThreadId).toBe('agent_child_1');
    expect(useClientStore.getState().threads).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: 'agent_child_1', agentId: 'agent_child_1', name: 'Review child' }),
    ]));
    expect(useClientStore.getState().timelinesByThread.agent_child_1).toEqual([
      expect.objectContaining({ text: '子代理评审完成' }),
    ]);
  });

  it('continues backend-resolved DAG child threads with the child thread cwd', async () => {
    resetClientStoreForTests({
      cwd: '/repo/main',
      activeProject: '/repo/main',
      activeThreadId: 'thread-main',
      threads: [{ id: 'thread-main', name: 'Main', provider: 'codex', status: 'running', cwd: '/repo/main' }],
    });
    backend.resolveThreadIdentity.mockResolvedValue({
      id: 'agent_child_1',
      agent_id: 'agent_child_1',
      name: 'Review child',
      provider: 'codex',
      cwd: '/repo/main/.worktrees/review-child',
      status: 'done',
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: '',
      threads: [{ id: 'thread-main', name: 'Main', provider: 'codex', status: 'running', cwd: '/repo/main' }],
      timelinesByThread: {},
    });
    backend.getThreadMessages.mockResolvedValue({
      messages: [{ id: 'm-child', role: 'assistant', content: '子代理评审完成' }],
      hasMore: false,
      nextBefore: '',
    });
    backend.startTurn.mockResolvedValue({ ok: true });

    await expect(useClientStore.getState().openThreadById('agent_child_1', { source: 'dag-node' })).resolves.toBe(true);
    expect(useClientStore.getState().threads).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: 'agent_child_1', cwd: '/repo/main/.worktrees/review-child' }),
    ]));
    useClientStore.getState().setDraft('继续处理这个 DAG 结果');
    await useClientStore.getState().sendDraft();

    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/main/.worktrees/review-child',
      threadId: 'agent_child_1',
      input: [{ type: 'text', text: '继续处理这个 DAG 结果' }],
      manualSkillSelection: false,
    });
  });

  it('shows DAG node prompt and result when a child thread has no provider history', async () => {
    resetClientStoreForTests({
      cwd: '/repo/main',
      activeProject: '/repo/main',
      activeThreadId: 'thread-main',
      threads: [{ id: 'thread-main', name: 'Main', provider: 'codex', status: 'running' }],
    });
    backend.resolveThreadIdentity.mockResolvedValue({
      id: 'agent_child_1',
      agent_id: 'agent_child_1',
      name: 'Review child',
      provider: 'codex',
      cwd: '/repo/main/.worktrees/review-child',
      status: 'done',
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: '',
      threads: [{ id: 'thread-main', name: 'Main', provider: 'codex', status: 'running' }],
      timelinesByThread: {},
    });
    backend.getThreadMessages.mockResolvedValue({
      messages: [],
      hasMore: false,
      nextBefore: '',
    });

    await expect(useClientStore.getState().openThreadById('agent_child_1', {
      source: 'dag-node',
      dagNode: {
        nodeKey: 'review',
        title: 'Review',
        config: { prompt: '请评审这个方案' },
        result: '评审完成：可以继续。',
      },
    })).resolves.toBe(true);

    expect(useClientStore.getState().timelinesByThread.agent_child_1).toEqual([
      expect.objectContaining({ role: 'user', text: '请评审这个方案' }),
      expect.objectContaining({ role: 'assistant', text: '评审完成：可以继续。' }),
    ]);
    expect(useClientStore.getState().threadTimelineReadyByThread.agent_child_1).toBe(true);
    expect(useClientStore.getState().threadStateLoadingByThread.agent_child_1).toBe(false);
  });

  it('prefers provider history over DAG node fallback content', async () => {
    resetClientStoreForTests({
      cwd: '/repo/main',
      activeProject: '/repo/main',
      activeThreadId: 'thread-main',
      threads: [{ id: 'thread-main', name: 'Main', provider: 'codex', status: 'running' }],
    });
    backend.resolveThreadIdentity.mockResolvedValue({
      id: 'agent_child_1',
      agent_id: 'agent_child_1',
      name: 'Review child',
      provider: 'codex',
      cwd: '/repo/main/.worktrees/review-child',
      status: 'done',
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: '',
      threads: [{ id: 'thread-main', name: 'Main', provider: 'codex', status: 'running' }],
      timelinesByThread: {},
    });
    backend.getThreadMessages.mockResolvedValue({
      messages: [{ id: 'm-real', role: 'assistant', content: '真实 provider 历史' }],
      hasMore: false,
      nextBefore: '',
    });

    await expect(useClientStore.getState().openThreadById('agent_child_1', {
      source: 'dag-node',
      dagNode: {
        nodeKey: 'review',
        title: 'Review',
        config: { prompt: '请评审这个方案' },
        result: 'DAG 兜底结果',
      },
    })).resolves.toBe(true);

    expect(useClientStore.getState().timelinesByThread.agent_child_1).toEqual([
      expect.objectContaining({ role: 'assistant', text: '真实 provider 历史' }),
    ]);
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
    const payload = parseRequiredJsonObject(backend.copyTextToClipboard.mock.calls[0][0]);
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
    expect(parseRequiredJsonObject(preparedClipboardWrite.commit.mock.calls[0][0])).toEqual(expect.objectContaining({
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
    expect(startPayload).not.toHaveProperty('toolSurfaceMode');
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
    const turnPayload = backend.startTurn.mock.calls[0][0];
    expect(turnPayload).not.toHaveProperty('attachments');

    const timeline = useClientStore.getState().timelinesByThread['thread-new'];
    expect(timeline).toEqual([
      expect.objectContaining({ role: 'user', text: 'Hello backend' }),
    ]);
    expect(useClientStore.getState().draft).toBe('');
    expect(useClientStore.getState().threadTimelineReadyByThread['thread-new']).toBe(true);
  });

  it('stores a new dot-project thread under the real cwd sidebar cache', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '.',
      projects: [],
      activeThreadId: '',
      draft: 'Hello from dot project',
      attachments: [],
      sidebarThreadsByProject: {
        '/repo/app': [],
      },
    });
    backend.startThread.mockResolvedValue({ threadId: 'thread-dot' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      name: 'Hello from dot project',
    }));
    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({
      id: 'thread-dot',
      cwd: '/repo/app',
      name: 'Hello from dot project',
    }));
    expect(useClientStore.getState().sidebarThreadsByProject['/repo/app']).toEqual([
      expect.objectContaining({
        id: 'thread-dot',
        cwd: '/repo/app',
        name: 'Hello from dot project',
      }),
    ]);
  });

  it('does not classify engineering intents into a frontend tool mode', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: '请帮我看这个文件并跑一下测试',
      attachments: [],
    });
    backend.startThread.mockResolvedValue({ threadId: 'thread-agent' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      name: '请帮我看这个文件并跑一下测试',
      deferSpawn: true,
    }));
    expect(backend.startThread.mock.calls[0][0]).not.toHaveProperty('toolSurfaceMode');
  });

  it('does not classify trace diagnosis intents into a frontend tool mode', async () => {
    const drafts = [
      '这个慢请求 trace_id=abc123 帮我定位一下',
      'traceparent 是 00-abc123-def456-01，查链路追踪',
      'span_id=def456 看下观测日志',
      '请用 observability_trace_get 查本地落盘日志',
    ];

    for (const [index, draft] of drafts.entries()) {
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: '',
        draft,
        attachments: [],
      });
      backend.startThread.mockClear();
      backend.startTurn.mockClear();
      backend.startThread.mockResolvedValue({ threadId: `thread-trace-${index}` });
      backend.startTurn.mockResolvedValue({ ok: true });

      await useClientStore.getState().sendDraft();

      expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        name: draft,
        deferSpawn: true,
      }));
      expect(backend.startThread.mock.calls[0][0]).not.toHaveProperty('toolSurfaceMode');
    }
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

  it('deduplicates intermediate turns that are concatenated in final assistant message', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
      timelinesByThread: {
        'thread-1': [
          { id: 'assistant-stream-turn1', role: 'assistant', text: '我会先加载 使用超能力 技能，确认本轮技能选择规则。', done: true },
          { id: 'assistant-stream-turn2', role: 'assistant', text: 'Hi，我在。需要我帮你看代码、排查问题，还是继续当前仓库里的改动？', done: true },
        ],
      },
    });
    backend.getThreadMessages.mockResolvedValueOnce({
      messages: [
        {
          id: 'assistant-final-msg',
          role: 'assistant',
          content: '我会先加载 使用超能力 技能，确认本轮技能选择规则。Hi，我在。需要我帮你看代码、排查问题，还是继续当前仓库里的改动？',
          createdAt: '2026-06-01T14:26:00Z',
        },
      ],
    });

    await useClientStore.getState().syncThreadState('thread-1');

    const texts = useClientStore.getState().timelinesByThread['thread-1'].map((message) => message.text);
    expect(texts).toEqual([
      '我会先加载 使用超能力 技能，确认本轮技能选择规则。Hi，我在。需要我帮你看代码、排查问题，还是继续当前仓库里的改动？'
    ]);
  });

  it('deduplicates and merges in-progress assistant messages with completed backend messages', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
      timelinesByThread: {
        'thread-1': [
          { id: 'assistant-stream', role: 'assistant', text: '你好！我是 Super Dolphin。', done: false },
        ],
      },
    });
    backend.getThreadMessages.mockResolvedValueOnce({
      messages: [
        {
          id: 'assistant-final-msg',
          role: 'assistant',
          content: '你好！我是 Super Dolphin。',
          createdAt: '2026-06-01T14:26:00Z',
        },
      ],
    });

    await useClientStore.getState().syncThreadState('thread-1');

    const timeline = useClientStore.getState().timelinesByThread['thread-1'];
    expect(timeline.length).toBe(1);
    expect(timeline[0].text).toBe('你好！我是 Super Dolphin。');
    expect(timeline[0].done).toBe(true);
  });

  it('does not override activeThreadId back to the previous thread when an in-flight sync resolves after clicking newThread', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
    });

    let resolveSync;
    const syncPromise = new Promise((resolve) => {
      resolveSync = resolve;
    });

    backend.getThreadState.mockImplementationOnce(() => syncPromise);
    backend.getThreadMessages.mockResolvedValueOnce({ messages: [] });

    // Start syncThreadState (simulates in-flight sync)
    const syncCall = useClientStore.getState().syncThreadState('thread-1');

    // User clicks newThread in the meantime
    useClientStore.getState().newThread();
    expect(useClientStore.getState().activeThreadId).toBe('');

    // Resolve the in-flight sync
    resolveSync({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
      timelinesByThread: {
        'thread-1': [],
      },
    });

    await syncCall;

    // Verify that activeThreadId remains empty and was not overridden back to thread-1
    expect(useClientStore.getState().activeThreadId).toBe('');
  });

  it('supports creating consecutive new threads, sending messages, and switching between them', async () => {
    // 1. Initialize empty store
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'hi 1',
      attachments: [],
      threads: [],
    });

    // Mock first send
    backend.startThread.mockResolvedValueOnce({ threadId: 'thread-1' });
    backend.startTurn.mockResolvedValueOnce({ ok: true });

    // Send first draft
    await useClientStore.getState().sendDraft();
    expect(useClientStore.getState().activeThreadId).toBe('thread-1');

    // 2. Click New Chat again
    useClientStore.getState().newThread();
    expect(useClientStore.getState().activeThreadId).toBe('');
    useClientStore.setState({ draft: 'hi 2' }); // type "hi 2"

    // Mock second send
    backend.startThread.mockResolvedValueOnce({ threadId: 'thread-2' });
    backend.startTurn.mockResolvedValueOnce({ ok: true });

    // Send second draft
    await useClientStore.getState().sendDraft();
    expect(useClientStore.getState().activeThreadId).toBe('thread-2');

    // 3. Switch back to thread-1
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: 'hi 1', provider: 'codex', status: 'idle' },
        { id: 'thread-2', name: 'hi 2', provider: 'codex', status: 'idle' },
      ],
      timelinesByThread: {
        'thread-1': [],
      },
    });
    backend.getThreadMessages.mockResolvedValueOnce({
      messages: [
        { id: 'msg-1', role: 'user', content: 'hi 1', createdAt: '2026-06-01T12:00:00Z' },
        { id: 'msg-2', role: 'assistant', content: 'reply 1', createdAt: '2026-06-01T12:01:00Z' },
      ],
    });

    await useClientStore.getState().setActiveThread('thread-1');

    expect(useClientStore.getState().activeThreadId).toBe('thread-1');
    const texts = useClientStore.getState().timelinesByThread['thread-1'].map((message) => message.text);
    expect(texts).toEqual(['hi 1', 'reply 1']);
  });

  it('supports concurrent thread creation and preserves streaming response when switching back and loading empty backend messages', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'hi 1',
      attachments: [],
      threads: [],
    });

    // We will control startThread resolutions manually using deferred promises
    let resolveStartThread1;
    const startThreadPromise1 = new Promise((resolve) => {
      resolveStartThread1 = resolve;
    });
    backend.startThread.mockReturnValueOnce(startThreadPromise1);
    backend.startTurn.mockResolvedValueOnce({ ok: true });

    // Send first draft (async, does not await finish yet)
    const sendPromise1 = useClientStore.getState().sendDraft();
    const provisionalId1 = useClientStore.getState().activeThreadId;
    expect(provisionalId1).toMatch(/^launch_/);

    // Simulate assistant streaming replies on provisionalId1
    useClientStore.setState((state) => ({
      timelinesByThread: {
        ...state.timelinesByThread,
        [provisionalId1]: [
          { id: 'user-msg', role: 'user', text: 'hi 1' },
          { id: 'assistant-msg', role: 'assistant', text: 'streaming reply...', optimistic: false, done: false },
        ]
      }
    }));

    // User clicks New Chat while sendDraft1 is in-flight
    useClientStore.getState().newThread();
    expect(useClientStore.getState().activeThreadId).toBe('');
    useClientStore.setState({ draft: 'hi 2' });

    // Mock second send
    backend.startThread.mockResolvedValueOnce({ threadId: 'thread-2' });
    backend.startTurn.mockResolvedValueOnce({ ok: true });

    // Send second draft
    await useClientStore.getState().sendDraft();
    expect(useClientStore.getState().activeThreadId).toBe('thread-2');

    // Now, resolve the first thread creation
    resolveStartThread1({ threadId: 'thread-1' });
    await sendPromise1;

    // Verify activeThreadId is NOT hijacked (it must remain thread-2)
    expect(useClientStore.getState().activeThreadId).toBe('thread-2');

    // Check that timeline of provisionalId1 was promoted to thread-1
    expect(useClientStore.getState().timelinesByThread['thread-1']).toBeDefined();
    expect(useClientStore.getState().timelinesByThread['thread-1'].map(m => m.text)).toEqual(['hi 1', 'streaming reply...']);

    // Now switch back to thread-1
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: 'hi 1', provider: 'codex', status: 'idle' },
        { id: 'thread-2', name: 'hi 2', provider: 'codex', status: 'idle' },
      ],
      timelinesByThread: {
        'thread-1': [],
      },
    });

    // Mock backend returning empty message list for the new thread (common case)
    backend.getThreadMessages.mockResolvedValueOnce({
      messages: [],
    });

    await useClientStore.getState().setActiveThread('thread-1');

    expect(useClientStore.getState().activeThreadId).toBe('thread-1');

    // Confirm that the streaming assistant message is preserved and not cleared
    const finalTexts = useClientStore.getState().timelinesByThread['thread-1'].map((message) => message.text);
    expect(finalTexts).toEqual(['hi 1', 'streaming reply...']);
  });

  it('filters injected AGENTS instructions from restored thread history', async () => {
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
        {
          id: 'injected-agents',
          role: 'user',
          content: [
            '# AGENTS.md instructions for /home/ai01@f666.com/桌面/project/Super-Dolphin',
            '',
            '<INSTRUCTIONS>',
            '# Super Dolphin Agent Agent Context Policy',
            '</INSTRUCTIONS>',
          ].join('\n'),
          createdAt: '2026-05-30T00:00:00Z',
        },
        { id: 'real-user', role: 'user', content: '真实用户问题', createdAt: '2026-05-30T00:01:00Z' },
        { id: 'assistant-reply', role: 'assistant', content: '真实 AI 回复', createdAt: '2026-05-30T00:02:00Z' },
      ],
      total: 3,
    });

    await useClientStore.getState().syncThreadState('thread-1');

    expect(useClientStore.getState().timelinesByThread['thread-1'].map((message) => message.text)).toEqual([
      '真实用户问题',
      '真实 AI 回复',
    ]);
  });

  it('[regression] strips <image> XML placeholders and extracts image attachments from history metadata', async () => {
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
        {
          id: 'user-with-image',
          role: 'user',
          content: '能先识别这张截图内容。<image name=[Image #1]></image>',
          metadata: {
            input: [
              { type: 'text', text: '能先识别这张截图内容。' },
              { type: 'localImage', path: '/var/folders/abc/T/clipboard-123456.png' },
            ],
          },
          createdAt: '2026-05-30T00:00:00Z',
        },
        {
          id: 'assistant-reply',
          role: 'assistant',
          content: '图片内容是一段代码。',
          createdAt: '2026-05-30T00:01:00Z',
        },
      ],
    });

    await useClientStore.getState().syncThreadState('thread-1');

    const timeline = useClientStore.getState().timelinesByThread['thread-1'];
    const userMsg = timeline.find((m) => m.role === 'user');

    // XML 占位符应被剥离
    expect(userMsg.text).toBe('能先识别这张截图内容。');
    expect(userMsg.text).not.toContain('<image');
    // 图片附件应被提取
    expect(Array.isArray(userMsg.attachments)).toBe(true);
    expect(userMsg.attachments).toHaveLength(1);
    expect(userMsg.attachments[0].kind).toBe('image');
    expect(userMsg.attachments[0].path).toBe('/var/folders/abc/T/clipboard-123456.png');
    // clipboard 路径应转为 /clipboard/ HTTP 路由
    expect(userMsg.attachments[0].previewUrl).toBe('/clipboard/clipboard-123456.png');
  });


  it('applies the selected thread first message page without waiting for older history', async () => {
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
    const messages = Array.from({ length: 301 }, (_, index) => {
      const id = index + 1;
      return {
        id,
        role: id % 2 === 0 ? 'assistant' : 'user',
        content: `message ${id}`,
        createdAt: new Date(Date.UTC(2026, 4, 30, 0, id, 0)).toISOString(),
      };
    });
    backend.getThreadMessages.mockResolvedValueOnce({
      messages: messages.slice(1).reverse(),
      total: 301,
      hasMore: true,
      nextBefore: '2',
    });

    await useClientStore.getState().syncThreadState('thread-1');

    expect(backend.getThreadMessages).toHaveBeenNthCalledWith(1, { threadId: 'thread-1', limit: 300 });
    expect(backend.getThreadMessages).toHaveBeenCalledTimes(1);
    const timeline = useClientStore.getState().timelinesByThread['thread-1'];
    expect(timeline).toHaveLength(300);
    expect(timeline[0]).toEqual(expect.objectContaining({ id: '2', text: 'message 2' }));
    expect(timeline[299]).toEqual(expect.objectContaining({ id: '301', text: 'message 301' }));
    expect(useClientStore.getState().threadMessagePaginationByThread['thread-1']).toEqual(expect.objectContaining({
      hasMore: true,
      nextBefore: '2',
      loading: false,
    }));
    expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'frontend.thread_history.initial_page.load',
      thread_id: 'thread-1',
      page_size: 300,
      message_count: 300,
      has_more: true,
      next_before: 'present',
      status: 'ok',
    }));
    expect(JSON.stringify(backend.emitFrontendTraceEvent.mock.calls.at(-1)[0])).not.toContain('message 301');
  });

  it('loads older thread messages on demand with backend pagination cursors', async () => {
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
    backend.getThreadMessages
      .mockResolvedValueOnce({
        messages: [
          { id: '3', role: 'assistant', content: 'new reply', createdAt: '2026-05-30T00:03:00Z' },
          { id: '2', role: 'user', content: 'new prompt', createdAt: '2026-05-30T00:02:00Z' },
        ],
        hasMore: true,
        nextBefore: '2',
      })
      .mockResolvedValueOnce({
        messages: [
          { id: '1', role: 'user', content: 'old prompt', createdAt: '2026-05-30T00:01:00Z' },
        ],
        hasMore: false,
        nextBefore: '',
      });

    await useClientStore.getState().syncThreadState('thread-1');
    await expect(useClientStore.getState().loadOlderThreadMessages('thread-1')).resolves.toBe(true);

    expect(backend.getThreadMessages).toHaveBeenNthCalledWith(2, {
      threadId: 'thread-1',
      limit: 300,
      before: '2',
    });
    expect(useClientStore.getState().timelinesByThread['thread-1'].map((message) => message.text)).toEqual([
      'old prompt',
      'new prompt',
      'new reply',
    ]);
    expect(useClientStore.getState().threadMessagePaginationByThread['thread-1']).toEqual(expect.objectContaining({
      hasMore: false,
      nextBefore: '',
      loading: false,
    }));
  });

  it('treats string zero hasMore as false for thread message pagination', async () => {
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
        { id: '2', role: 'assistant', content: 'reply', createdAt: '2026-05-30T00:02:00Z' },
      ],
      hasMore: '0',
      nextBefore: '2',
    });

    await useClientStore.getState().syncThreadState('thread-1');

    expect(useClientStore.getState().threadMessagePaginationByThread['thread-1']).toEqual(expect.objectContaining({
      hasMore: false,
      nextBefore: '',
      loading: false,
    }));
  });

  it('does not invent an older-message cursor when backend hasMore is true without nextBefore', async () => {
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
        { id: '2', role: 'assistant', content: 'reply', createdAt: '2026-05-30T00:02:00Z' },
      ],
      hasMore: true,
    });

    await useClientStore.getState().syncThreadState('thread-1');
    await expect(useClientStore.getState().loadOlderThreadMessages('thread-1')).resolves.toBe(false);

    expect(backend.getThreadMessages).toHaveBeenCalledTimes(1);
    expect(useClientStore.getState().warningEntries).toEqual([
      expect.objectContaining({
        event: 'thread.messages.pagination.missing_cursor',
        threadId: 'thread-1',
      }),
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

  it('does not let later thread/state empty assistant rows replace visible replies', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
    });
    backend.getThreadMessages.mockResolvedValue({ messages: [] });
    backend.getThreadState
      .mockResolvedValueOnce({
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
        timelinesByThread: {
          'thread-1': [{ id: 'assistant-1', role: 'assistant', text: '1', createdAt: '2026-06-01T14:22:00Z' }],
        },
      })
      .mockResolvedValueOnce({
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
        timelinesByThread: {
          'thread-1': [
            { id: 'assistant-1', role: 'assistant', text: '', createdAt: '2026-06-01T14:34:00Z' },
            { id: 'assistant-empty-new', role: 'assistant', text: '', createdAt: '2026-06-01T14:34:01Z' },
          ],
        },
      });

    await useClientStore.getState().syncThreadState('thread-1');
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

  it('does not render turn_aborted control blocks from thread/messages history', async () => {
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
        { id: 'message-user', role: 'user', content: 'visible prompt', createdAt: '2026-06-01T14:26:00Z' },
        {
          id: 'message-aborted-control',
          role: 'user',
          content: '<turn_aborted>\nThe user interrupted the previous turn on purpose. Any running unified exec processes may still be running in the background. If any tools/commands were aborted, they may have partially executed.\n</turn_aborted>',
          createdAt: '2026-06-01T14:27:00Z',
        },
        { id: 'assistant-text', role: 'assistant', content: 'visible reply', createdAt: '2026-06-01T14:28:00Z' },
      ],
    });

    await useClientStore.getState().syncThreadState('thread-1');

    expect(useClientStore.getState().timelinesByThread['thread-1'].map((message) => message.text)).toEqual([
      'visible prompt',
      'visible reply',
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

  it('rejects a Claude active provider preference before thread/start', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Do not silently remap provider',
      attachments: [],
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'claude',
      'settings.provider.claude.model': 'sonnet',
      'settings.provider.claude.effort': 'high',
    }[key] ?? null));
    backend.startThread.mockResolvedValue({ threadId: 'thread-should-not-start' });

    await expect(useClientStore.getState().sendDraft()).rejects.toThrow(
      'invalid UI preference response for settings.provider.active',
    );

    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).not.toHaveBeenCalled();
    expect(useClientStore.getState().draft).toBe('Do not silently remap provider');
  });

  it('includes default Codex identity preferences in thread/start launch payload', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Use default Codex identity',
      attachments: [],
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexHome': '~/.codex',
      'settings.provider.codex.codexInstanceKey': 'default',
      'settings.provider.codex.codexModelProvider': 'openai',
    }[key] ?? null));
    backend.startThread.mockResolvedValue({ threadId: 'thread-default-codex' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    const payload = backend.startThread.mock.calls[0][0];
    expect(payload).toEqual(expect.objectContaining({
      cwd: '/repo/app',
      modelProvider: 'codex',
      model: 'gpt-5.5',
      effort: 'xhigh',
      config: {
        codexHome: '~/.codex',
        codexInstanceKey: 'default',
        codexModelProvider: 'openai',
      },
    }));
  });

  it('falls back to global Codex identity preferences for thread/start launch payload', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Use global Codex identity',
      attachments: [],
    });
    backend.getPreference.mockImplementation(({ cwd, key }) => {
      if (!cwd) {
        return Promise.resolve({
          'settings.provider.codex.codexHome': 'C:\\Users\\ai01\\.codex',
          'settings.provider.codex.codexInstanceKey': 'default',
          'settings.provider.codex.codexModelProvider': 'openai',
        }[key] ?? null);
      }
      return Promise.resolve({
        'settings.provider.active': 'codex',
        'settings.provider.codex.model': 'gpt-5.5',
        'settings.provider.codex.effort': 'low',
      }[key] ?? null);
    });
    backend.startThread.mockResolvedValue({ threadId: 'thread-global-codex' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.getPreference).toHaveBeenCalledWith({ key: 'settings.provider.codex.codexHome' });
    expect(backend.getPreference).toHaveBeenCalledWith({ key: 'settings.provider.codex.codexInstanceKey' });
    expect(backend.getPreference).toHaveBeenCalledWith({ key: 'settings.provider.codex.codexModelProvider' });
    expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      modelProvider: 'codex',
      model: 'gpt-5.5',
      effort: 'low',
      codexModelProvider: 'openai',
      config: {
        codexHome: 'C:\\Users\\ai01\\.codex',
        codexInstanceKey: 'default',
        codexModelProvider: 'openai',
      },
    }));
  });

  it('includes expanded local Codex home defaults in thread/start launch payload', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Use expanded default Codex home',
      attachments: [],
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexHome': 'C:\\Users\\ai01\\.codex',
      'settings.provider.codex.codexInstanceKey': 'default',
      'settings.provider.codex.codexModelProvider': 'openai',
    }[key] ?? null));
    backend.startThread.mockResolvedValue({ threadId: 'thread-expanded-default-codex' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    const payload = backend.startThread.mock.calls[0][0];
    expect(payload).toEqual(expect.objectContaining({
      cwd: '/repo/app',
      modelProvider: 'codex',
      model: 'gpt-5.5',
      effort: 'xhigh',
      config: {
        codexHome: 'C:\\Users\\ai01\\.codex',
        codexInstanceKey: 'default',
        codexModelProvider: 'openai',
      },
    }));
  });

  it('starts thread without model preference when it is missing', async () => {
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
    backend.startThread.mockResolvedValue({ threadId: 'thread-default-model' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
      effort: 'xhigh',
    }));
    expect(backend.startThread).toHaveBeenCalledWith(
      expect.not.objectContaining({ model: expect.any(String) }),
    );
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
      codexModelProvider: 'openrouter',
      prompt_key: 'main/reviewer',
      config: {
        codexHome: '/Users/test/.codex-alt',
        codexInstanceKey: 'desktop-main',
        codexModelProvider: 'openrouter',
      },
    });
  });

  it('includes Codex runtime permission preferences in launch preferences', async () => {
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
      'settings.provider.codex.sandbox': {
        type: 'workspaceWrite',
        writableRoots: ['/repo/app'],
        networkAccess: true,
      },
      'settings.provider.codex.approvalPolicy': 'on-request',
      'settings.provider.codex.personality': 'pragmatic',
      'settings.provider.codex.summary': 'concise',
    }[key] ?? null));

    await expect(useClientStore.getState().resolveLaunchPreferences('/repo/app')).resolves.toEqual({
      modelProvider: 'codex',
      model: 'gpt-5.5',
      effort: 'xhigh',
      codexModelProvider: 'openrouter',
      sandbox: {
        type: 'workspaceWrite',
        writableRoots: ['/repo/app'],
        networkAccess: true,
      },
      approvalPolicy: 'on-request',
      personality: 'pragmatic',
      summary: 'concise',
      config: {
        codexHome: '/Users/test/.codex-alt',
        codexInstanceKey: 'desktop-main',
        codexModelProvider: 'openrouter',
      },
    });
  });

  it('rejects object-shaped provider preferences before thread/start', async () => {
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
      'settings.provider.codex.sandbox': {
        type: 'workspaceWrite',
        writableRoots: ['/repo/app'],
        networkAccess: false,
      },
      'settings.provider.codex.approvalPolicy': 'never',
      'settings.provider.codex.personality': 'pragmatic',
      'settings.provider.codex.summary': 'concise',
    }[key] ?? null));
    await expect(useClientStore.getState().sendDraft()).rejects.toThrow(
      'invalid UI preference response for settings.provider.active',
    );

    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).not.toHaveBeenCalled();
    expect(useClientStore.getState().draft).toBe('Use object prefs');
  });

  it('rejects object-shaped provider preferences before thread/start without partial launch', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Reject object prefs',
      attachments: [],
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': { value: 'gpt-5.5', label: 'GPT' },
      'settings.provider.codex.effort': 'medium',
    }[key] ?? null));

    await expect(useClientStore.getState().sendDraft()).rejects.toThrow(
      'invalid UI preference response for settings.provider.codex.model',
    );

    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).not.toHaveBeenCalled();
    expect(useClientStore.getState().draft).toBe('Reject object prefs');
  });

  it('starts a selected Codex provider thread instead of sending into a failed active session', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-failed',
      provider: 'codex',
      draft: 'Retry through selected Codex provider',
      attachments: [],
      threads: [{ id: 'thread-failed', name: 'Broken', provider: 'codex', status: 'failed' }],
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
    }[key] ?? null));
    backend.startThread.mockResolvedValue({ threadId: 'thread-codex' });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().sendDraft();

    expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      modelProvider: 'codex',
      model: 'gpt-5.5',
      effort: 'xhigh',
      deferSpawn: true,
    }));
    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-codex',
      input: [{ type: 'text', text: 'Retry through selected Codex provider' }],
      manualSkillSelection: false,
    });
    expect(useClientStore.getState().activeThreadId).toBe('thread-codex');
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

  it('recovers a stopped thread and retries turn/start once', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      draft: 'Continue stopped DAG agent',
      attachments: [],
      threads: [{ id: 'thread-1', name: 'DAG agent', provider: 'codex', status: 'stopped' }],
    });
    backend.startTurn
      .mockRejectedValueOnce(new Error('{"message":"[-32098] resolve session: thread \\"thread-1\\": resolve session: thread \\"thread-1\\" is stopped"}'))
      .mockResolvedValueOnce({ ok: true });
    backend.recoverThread.mockResolvedValue({ recovered: true, mode: 'relaunch_resume' });

    await expect(useClientStore.getState().sendDraft()).resolves.toBe(true);

    expect(backend.recoverThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
    expect(backend.startTurn).toHaveBeenCalledTimes(2);
    expect(backend.startTurn).toHaveBeenNthCalledWith(2, {
      cwd: '/repo/app',
      threadId: 'thread-1',
      input: [{ type: 'text', text: 'Continue stopped DAG agent' }],
      manualSkillSelection: false,
    });
    expect(useClientStore.getState().draft).toBe('');
  });

  it('starts a fresh Codex thread when auto-resume fails because identity is missing', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-legacy',
      draft: 'Continue legacy thread',
      attachments: [],
      composerCapabilities: boundCapabilities,
      threads: [{ id: 'thread-legacy', name: 'Legacy', provider: 'codex', status: 'running' }],
    });
    backend.startTurn
      .mockRejectedValueOnce(new Error('resolve session: thread "thread-legacy": resolve session: auto-resume failed: codex identity required for resume'))
      .mockResolvedValueOnce({ ok: true });
    backend.startThread.mockResolvedValue({ threadId: 'thread-recovered', agentId: 'agent-recovered' });

    await expect(useClientStore.getState().sendDraft()).resolves.toBe(true);

    expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      modelProvider: 'codex',
      config: {
        codexHome: '~/.codex',
        codexInstanceKey: 'default',
        codexModelProvider: 'openai',
      },
    }));
    expect(backend.startTurn).toHaveBeenCalledTimes(2);
    expect(backend.startTurn).toHaveBeenNthCalledWith(1, {
      cwd: '/repo/app',
      threadId: 'thread-legacy',
      input: [{ type: 'text', text: 'Continue legacy thread' }],
      selectedSkills: ['review'],
      selectedSkillRefs: [{
        name: 'review',
        scope: 'project',
        path: '/repo/app/.agents/skills/review',
      }],
      manualSkillSelection: true,
      enabledTools: ['lsp_edit'],
    });
    expect(backend.startTurn).toHaveBeenNthCalledWith(2, {
      cwd: '/repo/app',
      threadId: 'thread-recovered',
      input: [{ type: 'text', text: 'Continue legacy thread' }],
      selectedSkills: ['review'],
      selectedSkillRefs: [{
        name: 'review',
        scope: 'project',
        path: '/repo/app/.agents/skills/review',
      }],
      manualSkillSelection: true,
      enabledTools: ['lsp_edit'],
    });
    expect(useClientStore.getState().activeThreadId).toBe('thread-recovered');
    expect(useClientStore.getState().draft).toBe('');
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

  it('keeps opened sidebar threads with zero archivedAt visible', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      threads: [
        {
          id: 'thread-existing',
          name: 'Existing chat',
          provider: 'codex',
          status: 'idle',
          cwd: '/repo/app',
          archived: false,
          archivedAt: 0,
        },
      ],
    });

    useClientStore.getState().beginOpeningThread({
      id: 'thread-existing',
      agentId: 'thread-existing',
      providerThreadId: '',
      sessionId: '',
      cwd: '/repo/app',
      name: 'Existing chat',
      provider: 'codex',
      status: 'idle',
      archived: false,
      archivedAt: 0,
    });

    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({
      id: 'thread-existing',
      archived: false,
      archivedAt: 0,
    }));
  });

  it('keeps opened sidebar threads in place when selecting an existing thread', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-a',
      threads: [
        { id: 'thread-a', name: 'Thread A', provider: 'codex', status: 'idle', cwd: '/repo/app' },
        { id: 'thread-b', name: 'Thread B', provider: 'codex', status: 'idle', cwd: '/repo/app' },
        { id: 'thread-c', name: 'Thread C', provider: 'codex', status: 'idle', cwd: '/repo/app' },
      ],
    });

    useClientStore.getState().beginOpeningThread({
      id: 'thread-b',
      name: 'Thread B updated',
      provider: 'codex',
      status: 'running',
      cwd: '/repo/app',
    });

    const state = useClientStore.getState();
    expect(state.threads.map((thread) => thread.id)).toEqual(['thread-a', 'thread-b', 'thread-c']);
    expect(state.threads[1]).toEqual(expect.objectContaining({
      id: 'thread-b',
      name: 'Thread B updated',
      status: 'running',
    }));
    expect(state.activeThreadId).toBe('thread-b');
    expect(state.pendingActiveThreadId).toBe('thread-b');
  });

  it('issues distinct monotonic selection intents when the same thread is selected again', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-a',
      threads: [
        { id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' },
        { id: 'thread-b', cwd: '/repo/app', name: 'Thread B', provider: 'codex', status: 'idle' },
      ],
    });

    const firstA = useClientStore.getState().beginOpeningThread({ id: 'thread-a' });
    const middleB = useClientStore.getState().beginOpeningThread({ id: 'thread-b' });
    const latestA = useClientStore.getState().beginOpeningThread({ id: 'thread-a' });

    expect(firstA).toEqual(expect.objectContaining({ targetThreadId: 'thread-a' }));
    expect(middleB).toEqual(expect.objectContaining({ targetThreadId: 'thread-b' }));
    expect(latestA).toEqual(expect.objectContaining({ targetThreadId: 'thread-a' }));
    expect(latestA.selectionIntentId).toBeGreaterThan(middleB.selectionIntentId);
    expect(middleB.selectionIntentId).toBeGreaterThan(firstA.selectionIntentId);
  });

  it('rejects a conditional selection after a newer user selection invalidates its snapshot', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-a',
      threads: [
        { id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' },
        { id: 'thread-b', cwd: '/repo/app', name: 'Thread B', provider: 'codex', status: 'idle' },
      ],
    });
    backend.getThreadState.mockImplementation(({ threadId }) => ({
      activeThreadId: threadId,
      threads: [{ id: threadId, cwd: '/repo/app', provider: 'codex', status: 'idle' }],
    }));
    backend.getThreadMessages.mockResolvedValue({ messages: [] });
    const snapshot = useClientStore.getState().captureThreadSelection?.();

    await expect(useClientStore.getState().setActiveThread('thread-b')).resolves.toBe(true);
    await expect(
      useClientStore.getState().setActiveThread('thread-a', { selectionSnapshot: snapshot }),
    ).resolves.toBe(false);

    expect(useClientStore.getState().activeThreadId).toBe('thread-b');
  });

  it('keeps C active when A B C thread syncs finish out of order', async () => {
    const syncA = deferred();
    const syncB = deferred();
    const syncC = deferred();
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-a',
      threads: ['a', 'b', 'c'].map((suffix) => ({
        id: `thread-${suffix}`,
        cwd: '/repo/app',
        name: `Thread ${suffix.toUpperCase()}`,
        provider: 'codex',
        status: 'idle',
      })),
    });
    backend.getThreadState.mockImplementation(({ threadId }) => ({
      'thread-a': syncA.promise,
      'thread-b': syncB.promise,
      'thread-c': syncC.promise,
    })[threadId]);
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    const intentA = useClientStore.getState().beginOpeningThread({ id: 'thread-a' });
    const openA = useClientStore.getState().setActiveThread('thread-a', { selectionIntent: intentA });
    const intentB = useClientStore.getState().beginOpeningThread({ id: 'thread-b' });
    const openB = useClientStore.getState().setActiveThread('thread-b', { selectionIntent: intentB });
    const intentC = useClientStore.getState().beginOpeningThread({ id: 'thread-c' });
    const openC = useClientStore.getState().setActiveThread('thread-c', { selectionIntent: intentC });

    syncC.resolve({ activeThreadId: 'thread-c', threads: [{ id: 'thread-c', cwd: '/repo/app' }] });
    await expect(openC).resolves.toBe(true);
    syncA.resolve({ activeThreadId: 'thread-a', threads: [{ id: 'thread-a', cwd: '/repo/app' }] });
    await expect(openA).resolves.toBe(false);
    syncB.resolve({ activeThreadId: 'thread-b', threads: [{ id: 'thread-b', cwd: '/repo/app' }] });
    await expect(openB).resolves.toBe(false);

    expect(useClientStore.getState().activeThreadId).toBe('thread-c');
  });

  it('invalidates an opening intent when newThread creates a newer user intent', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-a',
      threads: [{ id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' }],
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-a',
      threads: [{ id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' }],
    });
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    const staleIntent = useClientStore.getState().beginOpeningThread({ id: 'thread-a' });
    useClientStore.getState().newThread();
    await useClientStore.getState().setActiveThread('thread-a', { selectionIntent: staleIntent });

    expect(useClientStore.getState().activeThreadId).toBe('');
    expect(useClientStore.getState().pendingActiveThreadId).toBe('');
  });

  it('invalidates an opening intent when a shared-file fork draft takes ownership', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-a',
      activePage: 'chat',
      threads: [{ id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' }],
      draft: 'keep this draft',
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-a',
      threads: [{ id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' }],
    });
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    const staleIntent = useClientStore.getState().beginOpeningThread({ id: 'thread-a' });
    expect(useClientStore.getState().continueWithSharedFile('reports/final.md')).toBe(true);
    await expect(
      useClientStore.getState().setActiveThread('thread-a', { selectionIntent: staleIntent }),
    ).resolves.toBe(false);

    expect(useClientStore.getState().forkDraft).toEqual(expect.objectContaining({
      open: true,
      sourceThreadId: 'thread-a',
      sharedFilePaths: ['reports/final.md'],
    }));
    expect(useClientStore.getState().draft).toBe('keep this draft');
  });

  it('suppresses stale sync failure notices while clearing the target keyed loading flag', async () => {
    const staleSync = deferred();
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-a',
      threads: [
        { id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' },
        { id: 'thread-c', cwd: '/repo/app', name: 'Thread C', provider: 'codex', status: 'idle' },
      ],
      actionNotice: null,
      warningEntries: [],
    });
    backend.getThreadState.mockReturnValue(staleSync.promise);
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    const intentA = useClientStore.getState().beginOpeningThread({ id: 'thread-a' });
    const openA = useClientStore.getState().setActiveThread('thread-a', { selectionIntent: intentA });
    useClientStore.getState().beginOpeningThread({ id: 'thread-c' });
    staleSync.reject(new Error('stale thread A failed'));
    await expect(openA).resolves.toBe(false);

    const state = useClientStore.getState();
    expect(state.activeThreadId).toBe('thread-c');
    expect(state.threadStateLoadingByThread['thread-a']).toBe(false);
    expect(state.actionNotice).toBeNull();
    expect(state.warningEntries).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ event: 'thread.sync.failed' }),
    ]));
  });

  it('lets a stale successful sync update keyed cache without changing the active intent', async () => {
    const staleSync = deferred();
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-a',
      threads: [
        { id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' },
        { id: 'thread-c', cwd: '/repo/app', name: 'Thread C', provider: 'codex', status: 'idle' },
      ],
    });
    backend.getThreadState.mockReturnValue(staleSync.promise);
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    const intentA = useClientStore.getState().beginOpeningThread({ id: 'thread-a' });
    const openA = useClientStore.getState().setActiveThread('thread-a', { selectionIntent: intentA });
    useClientStore.getState().beginOpeningThread({ id: 'thread-c' });
    staleSync.resolve({
      activeThreadId: 'thread-a',
      threads: [{ id: 'thread-a', cwd: '/repo/app', name: 'Thread A refreshed', provider: 'codex', status: 'idle' }],
      timelinesByThread: {
        'thread-a': [{ id: 'a-message', role: 'assistant', text: 'stale cache is still useful' }],
      },
    });
    await openA;

    const state = useClientStore.getState();
    expect(state.activeThreadId).toBe('thread-c');
    expect(state.threads).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: 'thread-a', name: 'Thread A refreshed' }),
    ]));
    expect(state.timelinesByThread['thread-a']).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: 'a-message', text: 'stale cache is still useful' }),
    ]));
    expect(state.threadStateLoadingByThread['thread-a']).toBe(false);
  });

  it('clears stale resolve failure loading without changing the active intent or publishing a notice', async () => {
    const staleResolve = deferred();
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-a',
      threads: [
        { id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' },
        { id: 'thread-c', cwd: '/repo/app', name: 'Thread C', provider: 'codex', status: 'idle' },
      ],
      actionNotice: null,
      warningEntries: [],
    });
    backend.resolveThreadIdentity.mockReturnValue(staleResolve.promise);

    const intentA = useClientStore.getState().beginOpeningThread({ id: 'thread-a' });
    const openA = useClientStore.getState().openThreadById('thread-a', {
      source: 'sidebar',
      selectionIntent: intentA,
    });
    useClientStore.getState().beginOpeningThread({ id: 'thread-c' });
    staleResolve.reject(new Error('stale resolve failed'));
    await expect(openA).resolves.toBe(false);

    const state = useClientStore.getState();
    expect(state.activeThreadId).toBe('thread-c');
    expect(state.threadStateLoadingByThread['thread-a']).toBe(false);
    expect(state.actionNotice).toBeNull();
    expect(state.warningEntries).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ event: 'thread.open.resolve.failed' }),
    ]));
  });

  it('clears keyed loading when the current resolve fails', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-a',
      threads: [{ id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' }],
    });
    backend.resolveThreadIdentity.mockRejectedValue(new Error('current resolve failed'));

    const intentA = useClientStore.getState().beginOpeningThread({ id: 'thread-a' });
    await expect(useClientStore.getState().openThreadById('thread-a', {
      source: 'sidebar',
      selectionIntent: intentA,
    })).rejects.toThrow('current resolve failed');

    expect(useClientStore.getState().threadStateLoadingByThread['thread-a']).toBe(false);
  });

  it('does not let a stale same-target resolve failure clear the newer intent loading', async () => {
    const staleResolve = deferred();
    const currentSync = deferred();
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-a',
      threads: [{ id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' }],
      actionNotice: null,
      warningEntries: [],
    });
    backend.resolveThreadIdentity.mockReturnValue(staleResolve.promise);
    backend.getThreadState.mockReturnValue(currentSync.promise);
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    const intentA1 = useClientStore.getState().beginOpeningThread({ id: 'thread-a' });
    const openA1 = useClientStore.getState().openThreadById('thread-a', {
      source: 'sidebar',
      selectionIntent: intentA1,
    });
    const intentA2 = useClientStore.getState().beginOpeningThread({ id: 'thread-a' });
    expect(intentA2).not.toBe(intentA1);
    const openA2 = useClientStore.getState().setActiveThread('thread-a', { selectionIntent: intentA2 });
    expect(useClientStore.getState().pendingActiveThreadId).toBe('');
    expect(useClientStore.getState().threadStateLoadingByThread['thread-a']).toBe(true);
    staleResolve.reject(new Error('stale same-target resolve failed'));
    await expect(openA1).resolves.toBe(false);

    const stateAfterStaleFailure = useClientStore.getState();
    expect(stateAfterStaleFailure.activeThreadId).toBe('thread-a');
    expect(stateAfterStaleFailure.pendingActiveThreadId).toBe('');
    expect(stateAfterStaleFailure.threadStateLoadingByThread['thread-a']).toBe(true);
    expect(stateAfterStaleFailure.actionNotice).toBeNull();
    expect(stateAfterStaleFailure.warningEntries).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ event: 'thread.open.resolve.failed' }),
    ]));

    currentSync.resolve({
      activeThreadId: 'thread-a',
      threads: [{ id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' }],
    });
    await expect(openA2).resolves.toBe(true);
    expect(useClientStore.getState().threadStateLoadingByThread['thread-a']).toBe(false);
  });

  it('does not commit a stale resolved canonical id after a newer selection', async () => {
    const resolvedIdentity = deferred();
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'alias-a',
      threads: [
        { id: 'alias-a', cwd: '/repo/app', name: 'Alias A', provider: 'codex', status: 'idle' },
        { id: 'thread-c', cwd: '/repo/app', name: 'Thread C', provider: 'codex', status: 'idle' },
      ],
    });
    backend.resolveThreadIdentity.mockReturnValue(resolvedIdentity.promise);
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'canonical-a',
      threads: [{ id: 'canonical-a', agentId: 'alias-a', cwd: '/repo/app', provider: 'codex', status: 'idle' }],
    });
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    const intentA = useClientStore.getState().beginOpeningThread({ id: 'alias-a' });
    const openA = useClientStore.getState().openThreadById('alias-a', {
      source: 'sidebar',
      selectionIntent: intentA,
    });
    useClientStore.getState().beginOpeningThread({ id: 'thread-c' });
    resolvedIdentity.resolve({
      id: 'canonical-a',
      agentId: 'alias-a',
      cwd: '/repo/app',
      provider: 'codex',
      status: 'idle',
    });
    await openA;

    expect(useClientStore.getState().activeThreadId).toBe('thread-c');
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
    registerBridgeEventHandlersForTest();
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

  it('emits a sanitized slow patch trace after thresholded bridge patch application', () => {
    resetClientStoreForTests({
      threads: [{ id: 'thread-new', name: 'Trace me', provider: 'codex', status: 'running' }],
    });
    let clockCalls = 0;
    setClientStoreClockMillisForTests(() => {
      clockCalls += 1;
      return clockCalls === 1 ? 0 : 75;
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-new',
        sequence: '1',
        prompt: 'forbidden prompt text',
        timelineItems: [{ id: 'assistant-1', kind: 'assistant', text: 'AI reply' }],
        agentRuntime: { agentId: 'agent-1' },
        activeTurn: { id: 'turn-1' },
      },
    });

    expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'frontend.patch.apply.slow',
      method: 'ui/thread/patch',
      thread_id: 'thread-new',
      agent_id: 'agent-1',
      turn_id: 'turn-1',
      duration_ms: 75,
      status: 'ok',
    }));
    expect(JSON.stringify(backend.emitFrontendTraceEvent.mock.calls[0][0])).not.toContain('prompt');
    setClientStoreClockMillisForTests(null);
  });

  it('maps explicit activeTurn patch payload without inventing one when omitted', () => {
    resetClientStoreForTests({
      threads: [
        { id: 'thread-active', name: 'Active', provider: 'codex', status: 'running' },
        { id: 'thread-empty', name: 'Empty', provider: 'codex', status: 'running' },
      ],
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-active',
        sequence: '1',
        activeTurn: { id: 'turn-active', threadId: 'thread-active', status: 'thinking' },
      },
    });
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-empty',
        sequence: '1',
        interruptible: true,
      },
    });

    expect(useClientStore.getState().activeTurnByThread).toEqual({
      'thread-active': expect.objectContaining({
        id: 'turn-active',
        threadId: 'thread-active',
        status: 'thinking',
      }),
    });
  });

  it('preserves the selected Claude provider when runtime patches omit provider metadata', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      provider: 'claude',
      activeThreadId: 'thread-claude',
      threads: [{ id: 'thread-claude', name: 'Claude chat', provider: 'claude', status: 'running' }],
    });
    registerBridgeEventHandlersForTest();

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
    registerBridgeEventHandlersForTest();

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
    registerBridgeEventHandlersForTest();

    expect(useClientStore.getState().workflowRevision).toBe(0);
    bridgeCallback({
      type: 'task/node/statusChanged',
      payload: { dag_key: 'flow-a', run_key: 'run-a', node_key: 'step', new_status: 'running' },
    });
    expect(useClientStore.getState().workflowRevision).toBe(1);

    bridgeCallback({
      method: 'cron/job/runStateChanged',
      payload: { job_id: 'job-1', run_id: 'run-1', status: 'running' },
    });
    expect(useClientStore.getState().workflowRevision).toBe(2);
  });

  it('fails fast instead of refreshing workflow data for malformed task node status events', () => {
    registerBridgeEventHandlersForTest();

    expect(() => bridgeCallback({
      type: 'task/node/statusChanged',
      payload: { dag_key: 'flow-a', node_key: 'step', new_status: 'running' },
    })).toThrow('dag status event run identity is required');

    expect(useClientStore.getState().workflowRevision).toBe(0);
  });

  it('[regression] completes agent timeline streaming when the canonical terminal arrives without cwd', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent-douyin',
      threads: [{ id: 'agent-douyin', name: 'Douyin agent', provider: 'codex', status: 'running' }],
      timelinesByThread: {
        'agent-douyin': [
          { id: 'assistant-stream-turn1', role: 'assistant', text: '正在流式输出...', done: false, turnId: 'turn1' }
        ]
      }
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: 'terminal-turn1',
        threadId: 'agent-douyin',
        turnId: 'turn1',
        outcome: 'success',
        occurredAt: '2026-07-16T01:00:00Z',
      }
    });

    await vi.waitFor(() => {
      const timeline = useClientStore.getState().timelinesByThread['agent-douyin'] || optionalUiArray();
      const msg = timeline.find(m => m.id === 'assistant-stream-turn1');
      expect(msg).toBeDefined();
      expect(msg.done).toBe(true);
    });
  });

  it('subscribes bridge events with callback error escalation for malformed DAG payloads', () => {
    registerBridgeEventHandlersForTest();

    expect(backend.onBridgeEvent).toHaveBeenCalledWith(expect.any(Function), expect.objectContaining({
      escalateCallbackError: expect.any(Function),
    }));
    expect(bridgeOptions).toEqual(expect.objectContaining({
      escalateCallbackError: expect.any(Function),
    }));
    expect(bridgeOptions.escalateCallbackError(new Error('bad payload'), {
      type: 'task/node/statusChanged',
    })).toBe(true);
    expect(bridgeOptions.escalateCallbackError(new Error('non-critical'), {
      type: 'ui/sidebar/changed',
    })).toBe(false);
  });

  it('refreshes the chat list when the backend sidebar projection changes', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-main',
      threads: [{ id: 'thread-main', name: 'Main agent', provider: 'codex', status: 'running' }],
    });
    backend.getSidebarState.mockResolvedValueOnce({
      activeThreadId: 'thread-main',
      threads: [
        { id: 'thread-main', name: 'Main agent', provider: 'codex', status: 'running' },
        { id: 'thread-child', name: 'Child agent', provider: 'codex', status: 'running' },
      ],
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/sidebar/changed',
      payload: { projection: 'sidebar', revision: 2 },
    });

    await vi.waitFor(() => {
      expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/app' });
    });
    await vi.waitFor(() => {
      expect(useClientStore.getState().threads).toEqual(expect.arrayContaining([
        expect.objectContaining({ id: 'thread-child', name: 'Child agent' }),
      ]));
    });
    expect(useClientStore.getState().activeThreadId).toBe('thread-main');
  });

  it('keeps live running status when a sidebar refresh returns a stale idle projection', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      projectScopeCwd: '/repo/app',
      activeProject: '/repo/app',
      projects: ['/repo/app'],
      activeThreadId: 'thread-main',
      threads: [{ id: 'thread-main', name: 'Main agent', provider: 'codex', status: 'running', cwd: '/repo/app' }],
      sidebarThreadsByProject: {
        '/repo/app': [{ id: 'thread-main', name: 'Main agent', provider: 'codex', status: 'running', cwd: '/repo/app' }],
      },
      activeTurnByThread: {
        'thread-main': { id: 'turn-main', threadId: 'thread-main', status: 'running' },
      },
    });
    backend.getSidebarState.mockResolvedValueOnce({
      activeThreadId: 'thread-main',
      threads: [{ id: 'thread-main', name: 'Main agent', provider: 'codex', status: 'idle', cwd: '/repo/app' }],
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/sidebar/changed',
      payload: { projection: 'sidebar', revision: 2 },
    });

    await vi.waitFor(() => {
      expect(backend.getSidebarState).toHaveBeenCalledWith({ cwd: '/repo/app' });
    });
    await flushPromises();

    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({
      id: 'thread-main',
      status: 'running',
    }));
    expect(useClientStore.getState().sidebarThreadsByProject['/repo/app'][0]).toEqual(expect.objectContaining({
      id: 'thread-main',
      status: 'running',
    }));

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-main',
        sequence: 'done',
        status: 'completed',
        interruptible: false,
        thread: { name: 'Main agent' },
      },
    });
    await flushPromises();

    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({
      id: 'thread-main',
      status: 'completed',
    }));
    expect(useClientStore.getState().sidebarThreadsByProject['/repo/app'][0]).toEqual(expect.objectContaining({
      id: 'thread-main',
      status: 'completed',
    }));
  });

  it('coalesces burst sidebar projection events and runs one trailing refresh', async () => {
    const firstRefresh = deferred();
    const trailingRefresh = deferred();
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-main',
      threads: [{ id: 'thread-main', name: 'Main agent', provider: 'codex', status: 'running' }],
    });
    backend.getSidebarState
      .mockReturnValueOnce(firstRefresh.promise)
      .mockReturnValueOnce(trailingRefresh.promise);
    registerBridgeEventHandlersForTest();

    bridgeCallback({ type: 'ui/sidebar/changed', payload: { revision: 2 } });
    bridgeCallback({ type: 'ui/sidebar/changed', payload: { revision: 3 } });
    bridgeCallback({ type: 'ui/sidebar/changed', payload: { revision: 4 } });

    expect(backend.getSidebarState).toHaveBeenCalledTimes(1);
    firstRefresh.resolve({
      activeThreadId: 'thread-main',
      threads: [
        { id: 'thread-main', name: 'Main agent', provider: 'codex', status: 'running' },
        { id: 'thread-stale', name: 'Stale snapshot', provider: 'codex', status: 'running' },
      ],
    });

    await vi.waitFor(() => {
      expect(backend.getSidebarState).toHaveBeenCalledTimes(2);
    });
    expect(backend.getSidebarState).toHaveBeenNthCalledWith(2, { cwd: '/repo/app' });

    trailingRefresh.resolve({
      activeThreadId: 'thread-main',
      threads: [
        { id: 'thread-main', name: 'Main agent', provider: 'codex', status: 'running' },
        { id: 'thread-fresh', name: 'Fresh snapshot', provider: 'codex', status: 'running' },
      ],
    });

    await vi.waitFor(() => {
      expect(useClientStore.getState().threads).toEqual(expect.arrayContaining([
        expect.objectContaining({ id: 'thread-fresh', name: 'Fresh snapshot' }),
      ]));
    });
    expect(useClientStore.getState().threads).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ id: 'thread-stale' }),
    ]));
    expect(backend.getSidebarState).toHaveBeenCalledTimes(2);
  });

  it('runs a pending sidebar refresh after an in-flight refresh rejects', async () => {
    const failedRefresh = deferred();
    const retryRefresh = deferred();
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-main',
      threads: [{ id: 'thread-main', name: 'Main agent', provider: 'codex', status: 'running' }],
    });
    backend.getSidebarState
      .mockReturnValueOnce(failedRefresh.promise)
      .mockReturnValueOnce(retryRefresh.promise);
    registerBridgeEventHandlersForTest();

    bridgeCallback({ type: 'ui/sidebar/changed', payload: { revision: 2 } });
    bridgeCallback({ type: 'ui/sidebar/changed', payload: { revision: 3 } });

    expect(backend.getSidebarState).toHaveBeenCalledTimes(1);
    failedRefresh.reject(new Error('sidebar refresh failed'));

    await vi.waitFor(() => {
      expect(backend.getSidebarState).toHaveBeenCalledTimes(2);
    });
    retryRefresh.resolve({
      activeThreadId: 'thread-main',
      threads: [
        { id: 'thread-main', name: 'Main agent', provider: 'codex', status: 'running' },
        { id: 'thread-recovered', name: 'Recovered snapshot', provider: 'codex', status: 'running' },
      ],
    });

    await vi.waitFor(() => {
      expect(useClientStore.getState().threads).toEqual(expect.arrayContaining([
        expect.objectContaining({ id: 'thread-recovered', name: 'Recovered snapshot' }),
      ]));
    });
    expect(useClientStore.getState().warningEntries[0]).toEqual(expect.objectContaining({
      level: 'error',
      event: 'thread.sidebar.refresh.failed',
    }));
    expect(backend.getSidebarState).toHaveBeenCalledTimes(2);
  });

  it('increments prompt revision from prompt and active-prompt preference bridge events', () => {
    registerBridgeEventHandlersForTest();

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

  it('clears text, attachments, and capabilities after a successful send', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      draft: 'Review this change',
      attachments: [{ path: '/tmp/change.patch', name: 'change.patch' }],
      composerCapabilities: boundCapabilities,
    });
    backend.startTurn.mockResolvedValueOnce({ ok: true });

    await expect(useClientStore.getState().sendDraft()).resolves.toBe(true);

    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-1',
      input: [
        { type: 'text', text: 'Review this change' },
        { type: 'mention', name: 'change.patch', path: '/tmp/change.patch' },
      ],
      selectedSkills: ['review'],
      selectedSkillRefs: [{
        name: 'review',
        scope: 'project',
        path: '/repo/app/.agents/skills/review',
      }],
      manualSkillSelection: true,
      enabledTools: ['lsp_edit'],
    });
    expect(useClientStore.getState()).toEqual(expect.objectContaining({
      draft: '',
      attachments: [],
      composerCapabilities: [],
    }));
  });

  it('restores text, attachments, and capabilities after a failed send', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      draft: 'Review this change',
      attachments: [{ path: '/tmp/change.patch', name: 'change.patch' }],
      composerCapabilities: boundCapabilities,
    });
    backend.startTurn.mockRejectedValueOnce(new Error('turn/start failed'));

    await expect(useClientStore.getState().sendDraft()).rejects.toThrow('turn/start failed');

    expect(useClientStore.getState()).toEqual(expect.objectContaining({
      draft: 'Review this change',
      attachments: [expect.objectContaining({ path: '/tmp/change.patch' })],
      composerCapabilities: [
        expect.objectContaining({
          key: 'skill:project::review:/repo/app/.agents/skills/review',
        }),
        expect.objectContaining({ key: 'mcp_tool:lsp:lsp_edit' }),
      ],
    }));
    expect(backend.startTurn).toHaveBeenCalledWith(expect.objectContaining({
      selectedSkills: ['review'],
      manualSkillSelection: true,
      enabledTools: ['lsp_edit'],
    }));
  });

  it.each(['unverified', 'stale'])(
    'blocks %s capabilities before turn/start',
    async (availability) => {
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        draft: 'Review this change',
        attachments: [],
        composerCapabilities: [{
          kind: 'mcp_tool',
          key: 'mcp_tool:lsp:grep',
          name: 'grep',
          label: 'grep',
          serverName: 'lsp',
          availability,
        }],
      });
      backend.startTurn.mockReset();
      backend.startTurn.mockResolvedValue({ ok: true });

      await expect(useClientStore.getState().sendDraft()).rejects.toThrow(
        `composer capability mcp_tool:lsp:grep is ${availability}`,
      );
      expect(backend.startTurn).not.toHaveBeenCalled();
    },
  );

  it('does not send capability-only composer state', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      draft: '',
      attachments: [],
      composerCapabilities: [boundCapabilities[1]],
    });

    await expect(useClientStore.getState().sendDraft()).resolves.toBe(false);
    expect(backend.startTurn).not.toHaveBeenCalled();
  });

  it('exposes capability mutations and clears the whole composer', () => {
    resetClientStoreForTests({
      draft: 'Keep together',
      attachments: [{ path: '/tmp/change.patch', name: 'change.patch' }],
      composerCapabilities: [],
    });

    useClientStore.getState().addComposerCapability(boundCapabilities[0]);
    expect(useClientStore.getState().composerCapabilities).toEqual([
      expect.objectContaining({ key: boundCapabilities[0].key }),
    ]);

    useClientStore.getState().reconcileComposerCapabilities({
      kind: 'skill',
      status: 'success',
      items: [],
    });
    expect(useClientStore.getState().composerCapabilities[0]).toEqual(
      expect.objectContaining({ availability: 'stale' }),
    );

    useClientStore.getState().removeComposerCapability(boundCapabilities[0].key);
    useClientStore.getState().addComposerCapability(boundCapabilities[1]);
    useClientStore.getState().clearComposer();

    expect(useClientStore.getState()).toEqual(expect.objectContaining({
      draft: '',
      attachments: [],
      composerCapabilities: [],
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
    const state = useClientStore.getState();
    expect(state.draft).toBe('Clean up provisional thread');
    expect(state.activeThreadId).not.toBe('thread-provisional');
    expect(state.threads.some((thread) => thread.id === 'thread-provisional')).toBe(false);
    expect((state.sidebarThreadsByProject['/repo/app'] || optionalUiArray()).some((thread) => thread.id === 'thread-provisional')).toBe(false);
    expect(state.timelinesByThread['thread-provisional']).toBeUndefined();
    expect(state.threadTimelineReadyByThread['thread-provisional']).toBeUndefined();
    expect(state.activityThreadAtById['thread-provisional']).toBeUndefined();
  });


  it('does not delete an unrelated active thread when a provisional send fails', async () => {
    const turnResult = deferred();

    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Keep draft',
      attachments: [],
      threads: [{ id: 'thread-other', name: 'Other thread', provider: 'codex', status: 'running' }],
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
    backend.startTurn.mockImplementation(() => turnResult.promise);

    const sendPromise = useClientStore.getState().sendDraft();
    useClientStore.setState({ activeThreadId: 'thread-other' });
    turnResult.reject(new Error('turn/start failed'));

    await expect(sendPromise).rejects.toThrow('turn/start failed');

    expect(backend.deleteThread).toHaveBeenCalledWith({ threadId: 'thread-provisional' });
    expect(backend.deleteThread).not.toHaveBeenCalledWith({ threadId: 'thread-other' });
  });

  it('does not let a stale send failure overwrite the active composer after a thread switch', async () => {
    const turnResult = deferred();
    const nextAttachments = [{ path: '/tmp/next.txt', name: 'next.txt' }];

    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Original pending send',
      attachments: [{ path: '/tmp/original.txt', name: 'original.txt' }],
      threads: [{ id: 'thread-other', name: 'Other thread', provider: 'codex', status: 'running' }],
      sidebarThreadsByProject: {
        '/repo/app': [{ id: 'thread-other', name: 'Other thread', provider: 'codex', status: 'running' }],
      },
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
    backend.startTurn.mockImplementation(() => turnResult.promise);

    const sendPromise = useClientStore.getState().sendDraft();
    await flushPromises();

    useClientStore.setState({
      activeThreadId: 'thread-other',
      draft: 'New active draft',
      attachments: nextAttachments,
    });
    turnResult.reject(new Error('turn/start failed'));

    await expect(sendPromise).rejects.toThrow('turn/start failed');

    const state = useClientStore.getState();
    expect(state.activeThreadId).toBe('thread-other');
    expect(state.draft).toBe('New active draft');
    expect(state.attachments).toEqual(nextAttachments);
    expect(state.threads.some((thread) => thread.id === 'thread-provisional')).toBe(false);
    expect((state.sidebarThreadsByProject['/repo/app'] || optionalUiArray()).some((thread) => thread.id === 'thread-provisional')).toBe(false);
    expect(state.timelinesByThread['thread-provisional']).toBeUndefined();
    expect(backend.deleteThread).toHaveBeenCalledWith({ threadId: 'thread-provisional' });
  });

  it('restores a failed new-chat draft when returning after a thread switch', async () => {
    const turnResult = deferred();
    const originalAttachments = [{ path: '/tmp/original.txt', name: 'original.txt' }];
    const nextAttachments = [{ path: '/tmp/next.txt', name: 'next.txt' }];

    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      draft: 'Original pending send',
      attachments: originalAttachments,
      threads: [{ id: 'thread-other', name: 'Other thread', provider: 'codex', status: 'running' }],
      sidebarThreadsByProject: {
        '/repo/app': [{ id: 'thread-other', name: 'Other thread', provider: 'codex', status: 'running' }],
      },
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
    backend.startTurn.mockImplementation(() => turnResult.promise);

    const sendPromise = useClientStore.getState().sendDraft();
    await flushPromises();

    useClientStore.setState({
      activeThreadId: 'thread-other',
      draft: 'New active draft',
      attachments: nextAttachments,
    });
    turnResult.reject(new Error('turn/start failed'));

    await expect(sendPromise).rejects.toThrow('turn/start failed');

    expect(useClientStore.getState().draft).toBe('New active draft');
    expect(useClientStore.getState().attachments).toEqual(nextAttachments);

    useClientStore.getState().newThread();

    expect(useClientStore.getState().draft).toBe('Original pending send');
    expect(useClientStore.getState().attachments).toEqual([
      expect.objectContaining({ path: '/tmp/original.txt', name: 'original.txt' }),
    ]);
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

  it('opens an inherited fork draft from a shared file continuation action when a source thread exists', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      activePage: 'files',
      threads: [{ id: 'thread-1', name: 'Existing thread', provider: 'codex', status: 'idle' }],
      draft: 'old draft',
      attachments: [{ path: 'reports/final.md', name: 'final.md' }],
    });

    expect(useClientStore.getState().continueWithSharedFile('reports/final.md')).toBe(true);

    const state = useClientStore.getState();
    expect(state.activePage).toBe('chat');
    expect(state.activeThreadId).toBe('thread-1');
    expect(state.forkDraft.open).toBe(true);
    expect(state.forkDraft.sourceThreadId).toBe('thread-1');
    expect(state.forkDraft.sourceTitle).toBe('继承自会话：Existing thread');
    expect(state.forkDraft.sharedFilePaths).toEqual(['reports/final.md']);
    expect(state.draft).toBe('old draft');
  });

  it('falls back to a new composer draft from a shared file when no source thread exists', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '',
      activePage: 'files',
      draft: 'old draft',
      attachments: [],
    });

    expect(useClientStore.getState().continueWithSharedFile('reports/final.md')).toBe(true);

    const state = useClientStore.getState();
    expect(state.activePage).toBe('chat');
    expect(state.activeThreadId).toBe('');
    expect(state.draft).toContain('reports/final.md');
    expect(state.attachments).toEqual([{ path: 'reports/final.md', name: 'final.md' }]);
  });

  it('uses canonical thread/fork and sends exactly one created-only kickoff', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing thread', provider: 'codex', status: 'idle' }],
      timelinesByThread: {
        'thread-1': [
          { id: 'user-1', kind: 'user', text: 'first message' },
          { id: 'assistant-1', kind: 'assistant', text: 'reply with next steps' },
        ],
      },
    });
    backend.forkThread.mockResolvedValue({
      thread: { id: 'thread-fork', forkedFrom: 'thread-1' },
      kickoffState: 'created_only',
    });
    backend.startTurn.mockResolvedValue({ ok: true });

    await expect(useClientStore.getState().openForkDraft()).resolves.toBe(true);
    await expect(useClientStore.getState().submitForkThread()).resolves.toBe('thread-fork');

    expect(backend.forkThread).toHaveBeenCalledWith({ threadId: 'thread-1' });
    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).toHaveBeenCalledTimes(1);
    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-fork',
      input: [{ type: 'text', text: '请基于已继承的完整对话历史，简要总结当前进展并提出下一步建议。' }],
      manualSkillSelection: false,
    });
    expect(useClientStore.getState().activeThreadId).toBe('thread-fork');
    expect(useClientStore.getState().forkDraft.open).toBe(false);
  });

  it('marks inherited fork kickoff failure as partial instead of a full working success', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing thread', provider: 'codex', status: 'idle' }],
      timelinesByThread: {
        'thread-1': [
          { id: 'user-1', kind: 'user', text: 'fork this work' },
          { id: 'assistant-1', kind: 'assistant', text: 'forkable context' },
        ],
      },
    });
    backend.forkThread.mockResolvedValue({
      thread: { id: 'thread-fork', forkedFrom: 'thread-1' },
      kickoffState: 'created_only',
    });
    backend.startTurn.mockRejectedValue(new Error('turn/start failed'));

    await expect(useClientStore.getState().openForkDraft()).resolves.toBe(true);
    await expect(useClientStore.getState().submitForkThread()).resolves.toBe('thread-fork');

    const state = useClientStore.getState();
    expect(state.actionNotice).toEqual(expect.objectContaining({
      message: expect.stringContaining('开场消息发送失败'),
      tone: 'warning',
    }));
    expect(state.threads[0]).toEqual(expect.objectContaining({
      id: 'thread-fork',
      status: '需要操作',
      forkKickoffStatus: 'failed',
      forkKickoffError: 'turn/start failed',
    }));
    expect(state.timelinesByThread['thread-fork'] || optionalUiArray()).not.toEqual(expect.arrayContaining([
      expect.objectContaining({
        id: expect.stringMatching(/^fork-kickoff-/),
        optimistic: true,
      }),
    ]));
  });

  it('sends selected shared files as canonical filecontent kickoff input', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing thread', provider: 'codex', status: 'idle' }],
      timelinesByThread: {
        'thread-1': [{ id: 'user-1', kind: 'user', text: 'continue with shared files' }],
      },
    });
    backend.listSharedFiles.mockResolvedValue({
      files: [{ path: 'notes/a.md' }, { path: 'notes/b.md' }],
    });
    backend.forkThread.mockResolvedValue({
      thread: { id: 'thread-fork', forkedFrom: 'thread-1' },
      kickoffState: 'created_only',
    });
    backend.readSharedFile.mockResolvedValue({
      path: 'notes/a.md',
      content: '  indented\n',
    });
    backend.startTurn.mockResolvedValue({ ok: true });

    await useClientStore.getState().openForkDraft();
    expect(useClientStore.getState().forkDraft.availableSharedFiles).toEqual([
      { path: 'notes/a.md' },
      { path: 'notes/b.md' },
    ]);

    expect(useClientStore.getState().toggleForkDraftSharedFile('notes/a.md')).toBe(true);
    await useClientStore.getState().submitForkThread();

    expect(backend.readSharedFile).toHaveBeenCalledWith({ path: 'notes/a.md' });
    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-fork',
      input: [
        { type: 'text', text: '请基于已继承的完整对话历史，简要总结当前进展并提出下一步建议。' },
        {
          type: 'filecontent',
          path: 'notes/a.md',
          name: 'notes/a.md',
          content: '  indented\n',
        },
      ],
      manualSkillSelection: false,
    });
  });

  it('validates selected filecontent before creating a backend fork', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing thread', provider: 'codex', status: 'idle' }],
      timelinesByThread: { 'thread-1': [] },
    });
    backend.listSharedFiles.mockResolvedValue({ files: [{ path: 'notes/blank.md' }] });
    backend.readSharedFile.mockResolvedValue({ path: 'notes/blank.md', content: '   \n' });

    await useClientStore.getState().openForkDraft();
    expect(useClientStore.getState().toggleForkDraftSharedFile('notes/blank.md')).toBe(true);
    await expect(useClientStore.getState().submitForkThread()).rejects.toThrow(
      'fork shared file path and content are required',
    );

    expect(backend.forkThread).not.toHaveBeenCalled();
    expect(backend.startTurn).not.toHaveBeenCalled();
    expect(useClientStore.getState().activeThreadId).toBe('thread-1');
    expect(useClientStore.getState().forkDraft.open).toBe(true);
  });

  it('does not fall back to thread/start when canonical fork fails', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing thread', provider: 'codex', status: 'idle' }],
      timelinesByThread: { 'thread-1': [] },
    });
    backend.forkThread.mockRejectedValue(new Error('thread/fork unsupported'));

    await useClientStore.getState().openForkDraft();
    await expect(useClientStore.getState().submitForkThread()).rejects.toThrow('thread/fork unsupported');

    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).not.toHaveBeenCalled();
    expect(useClientStore.getState().activeThreadId).toBe('thread-1');
    expect(useClientStore.getState().forkDraft.open).toBe(true);
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

  it('keeps a newer active archived thread when an older sync response returns late', async () => {
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
        { id: 'thread-new-archived', name: 'New Archived', provider: 'codex', status: 'archived' },
      ],
      timelinesByThread: {
        'thread-new-archived': [{ id: 'user-new', role: 'user', text: 'new message', time: '2026-05-30T00:00:00Z' }],
      },
    });

    const sync = useClientStore.getState().syncThreadState('thread-old');
    await vi.waitFor(() => expect(backend.getThreadState).toHaveBeenCalled());
    useClientStore.setState({ activeThreadId: 'thread-new-archived' });
    resolveSnapshot({
      activeThreadId: 'thread-old',
      threads: [{ id: 'thread-old', name: 'Old', provider: 'codex', status: 'idle' }],
      timelinesByThread: {
        'thread-old': [{ id: 'old-assistant', kind: 'assistant', text: 'old reply' }],
      },
    });

    await sync;

    const state = useClientStore.getState();
    expect(state.activeThreadId).toBe('thread-new-archived');
  });

  it('applies thread snapshot before a concurrent message history load finishes', async () => {
    const snapshot = deferred();
    const messages = deferred();
    backend.getThreadState.mockReturnValue(snapshot.promise);
    backend.getThreadMessages.mockReturnValue(messages.promise);
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'running' }],
    });

    const sync = useClientStore.getState().syncThreadState('thread-1');
    await vi.waitFor(() => expect(backend.getThreadMessages).toHaveBeenCalledWith({ threadId: 'thread-1', limit: 300 }));
    snapshot.resolve({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Synced name', provider: 'codex', status: 'idle' }],
      timelinesByThread: {
        'thread-1': [{ id: 'snapshot-assistant', kind: 'assistant', text: 'snapshot reply' }],
      },
    });
    await vi.waitFor(() => expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({ name: 'Synced name' })));

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ text: 'snapshot reply' }),
    ]);

    messages.resolve({
      messages: [{ id: 'message-user', role: 'user', content: 'loaded prompt', createdAt: '2026-05-30T00:00:00Z' }],
    });
    await expect(sync).resolves.toBe(true);
    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ text: 'loaded prompt' }),
      expect.objectContaining({ text: 'snapshot reply' }),
    ]);
  });

  it('applies the first message page before a slower thread snapshot returns', async () => {
    const snapshot = deferred();
    backend.getThreadState.mockReturnValue(snapshot.promise);
    backend.getThreadMessages.mockResolvedValue({
      messages: [{ id: 'message-user', role: 'user', content: 'loaded prompt', createdAt: '2026-05-30T00:00:00Z' }],
      hasMore: false,
      nextBefore: '',
    });
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'running' }],
    });

    const sync = useClientStore.getState().syncThreadState('thread-1');
    await vi.waitFor(() => expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ text: 'loaded prompt' }),
    ]));

    snapshot.resolve({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Synced name', provider: 'codex', status: 'idle' }],
      timelinesByThread: {},
    });
    await expect(sync).resolves.toBe(true);
    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ text: 'loaded prompt' }),
    ]);
  });

  it('keeps trusted cached messages visible while a refresh message page is loading', async () => {
    const snapshot = deferred();
    const messages = deferred();
    backend.getThreadState.mockReturnValue(snapshot.promise);
    backend.getThreadMessages.mockReturnValue(messages.promise);
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'running' }],
      timelinesByThread: {
        'thread-1': [{ id: 'cached-user', role: 'user', text: 'cached prompt', time: '2026-05-30T00:00:00Z' }],
      },
      threadTimelineReadyByThread: { 'thread-1': true },
    });

    const sync = useClientStore.getState().syncThreadState('thread-1');
    await vi.waitFor(() => expect(backend.getThreadMessages).toHaveBeenCalled());
    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ text: 'cached prompt' }),
    ]);

    messages.resolve({ messages: [], hasMore: false, nextBefore: '' });
    snapshot.resolve({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
      timelinesByThread: {},
    });

    await expect(sync).resolves.toBe(true);
    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ text: 'cached prompt' }),
    ]);
  });

  it('drops stale empty userMessage command cards while preserving cached messages', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'idle' }],
      timelinesByThread: {},
    });
    backend.getThreadMessages.mockResolvedValue({ messages: [], hasMore: false, nextBefore: '' });
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'running' }],
      timelinesByThread: {
        'thread-1': [
          { id: 'cached-user', role: 'user', text: 'cached prompt', time: '2026-05-30T00:00:00Z' },
          { id: 'item:userMessage', kind: 'command', status: 'completed', itemType: 'userMessage', done: true, success: true },
        ],
      },
      threadTimelineReadyByThread: { 'thread-1': true },
    });

    await expect(useClientStore.getState().syncThreadState('thread-1')).resolves.toBe(true);

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ id: 'cached-user', text: 'cached prompt' }),
    ]);
  });

  it('ignores stale message pages and loading cleanup from older same-thread requests', async () => {
    const firstSnapshot = deferred();
    const secondSnapshot = deferred();
    const firstMessages = deferred();
    const secondMessages = deferred();
    backend.getThreadState
      .mockReturnValueOnce(firstSnapshot.promise)
      .mockReturnValueOnce(secondSnapshot.promise);
    backend.getThreadMessages
      .mockReturnValueOnce(firstMessages.promise)
      .mockReturnValueOnce(secondMessages.promise);
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'running' }],
    });

    const firstSync = useClientStore.getState().syncThreadState('thread-1');
    await vi.waitFor(() => expect(backend.getThreadMessages).toHaveBeenCalledTimes(1));
    const secondSync = useClientStore.getState().syncThreadState('thread-1');
    await vi.waitFor(() => expect(backend.getThreadMessages).toHaveBeenCalledTimes(2));

    secondMessages.resolve({
      messages: [{ id: 'fresh', role: 'user', content: 'fresh prompt', createdAt: '2026-05-30T00:02:00Z' }],
      hasMore: false,
      nextBefore: '',
    });
    await vi.waitFor(() => expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ text: 'fresh prompt' }),
    ]));
    firstMessages.resolve({
      messages: [{ id: 'stale', role: 'user', content: 'stale prompt', createdAt: '2026-05-30T00:01:00Z' }],
      hasMore: true,
      nextBefore: 'stale',
    });
    firstSnapshot.resolve({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Old snapshot', provider: 'codex', status: 'idle' }],
      timelinesByThread: {},
    });
    secondSnapshot.resolve({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Fresh snapshot', provider: 'codex', status: 'idle' }],
      timelinesByThread: {},
    });

    await expect(firstSync).resolves.toBe(true);
    await expect(secondSync).resolves.toBe(true);
    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ text: 'fresh prompt' }),
    ]);
    expect(useClientStore.getState().threadMessagePaginationByThread['thread-1']).toEqual(expect.objectContaining({
      hasMore: false,
      loading: false,
    }));
  });

  it('ignores stale same-thread snapshots that resolve after a newer sync applied', async () => {
    const firstSnapshot = deferred();
    const secondSnapshot = deferred();
    const firstMessages = deferred();
    const secondMessages = deferred();
    backend.getThreadState
      .mockReturnValueOnce(firstSnapshot.promise)
      .mockReturnValueOnce(secondSnapshot.promise);
    backend.getThreadMessages
      .mockReturnValueOnce(firstMessages.promise)
      .mockReturnValueOnce(secondMessages.promise);
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'running' }],
    });

    const firstSync = useClientStore.getState().syncThreadState('thread-1');
    await vi.waitFor(() => expect(backend.getThreadState).toHaveBeenCalledTimes(1));
    const secondSync = useClientStore.getState().syncThreadState('thread-1');
    await vi.waitFor(() => expect(backend.getThreadState).toHaveBeenCalledTimes(2));

    secondMessages.resolve({
      messages: [{ id: 'fresh-message', role: 'user', content: 'fresh prompt', createdAt: '2026-05-30T00:02:00Z' }],
      hasMore: false,
      nextBefore: '',
    });
    secondSnapshot.resolve({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Fresh snapshot', provider: 'codex', status: 'idle' }],
      timelinesByThread: {
        'thread-1': [{ id: 'fresh-snapshot', kind: 'assistant', text: 'fresh snapshot reply' }],
      },
      diffText: 'fresh diff',
    });
    await vi.waitFor(() => expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({
      name: 'Fresh snapshot',
      status: 'idle',
    })));

    firstMessages.resolve({
      messages: [{ id: 'stale-message', role: 'user', content: 'stale prompt', createdAt: '2026-05-30T00:01:00Z' }],
      hasMore: true,
      nextBefore: 'stale',
    });
    firstSnapshot.resolve({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Old snapshot', provider: 'codex', status: 'running' }],
      timelinesByThread: {
        'thread-1': [{ id: 'old-snapshot', kind: 'assistant', text: 'old snapshot reply' }],
      },
      diffText: 'old diff',
    });

    await expect(firstSync).resolves.toBe(true);
    await expect(secondSync).resolves.toBe(true);
    const state = useClientStore.getState();
    expect(state.threads[0]).toEqual(expect.objectContaining({
      name: 'Fresh snapshot',
      status: 'idle',
    }));
    expect(state.timelinesByThread['thread-1'].map((message) => message.text)).toEqual([
      'fresh prompt',
      'fresh snapshot reply',
    ]);
    expect(state.diffTextByThread['thread-1']).toBe('fresh diff');
    expect(state.threadStateLoadingByThread['thread-1']).toBe(false);
  });

  it('batches burst runtime assistant deltas before applying them to the timeline', async () => {
    vi.useFakeTimers();
    try {
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
        timelinesByThread: {
          'thread-1': [{ id: 'user-1', role: 'user', text: 'count', time: '2026-05-30T00:00:00Z' }],
        },
      });
      registerBridgeEventHandlersForTest();

      const chunks = Array.from({ length: 100 }, (_, index) => `${index},`);
      for (const delta of chunks) {
        bridgeCallback({
          method: 'item/agentMessage/delta',
          payload: {
            threadId: 'thread-1',
            turnId: 'turn-1',
            delta,
            stream: 'message',
          },
        });
      }

      expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
        expect.objectContaining({ id: 'user-1', role: 'user', text: 'count' }),
      ]);

      await flushAssistantDeltaBatch();

      expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
        expect.objectContaining({ id: 'user-1', role: 'user', text: 'count' }),
        expect.objectContaining({
          id: 'assistant-stream-turn-1',
          role: 'assistant',
          text: chunks.join(''),
          done: false,
        }),
      ]);
    }
    finally {
      vi.useRealTimers();
    }
  });

  it('flushes pending assistant deltas before applying completion events', async () => {
    vi.useFakeTimers();
    try {
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
        timelinesByThread: {
          'thread-1': [{ id: 'user-1', role: 'user', text: 'say ok', time: '2026-05-30T00:00:00Z' }],
        },
      });
      registerBridgeEventHandlersForTest();

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
      await flushAssistantDeltaBatch();
      expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
        expect.objectContaining({ role: 'user', text: 'say ok' }),
        expect.objectContaining({ id: 'msg-final', role: 'assistant', text: 'ok', done: true }),
      ]);
    }
    finally {
      vi.useRealTimers();
    }
  });

  it('preserves markdown block whitespace across assistant delta chunks', async () => {
    vi.useFakeTimers();
    try {
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
        timelinesByThread: {
          'thread-1': [{ id: 'user-1', role: 'user', text: 'inspect repo', time: '2026-05-30T00:00:00Z' }],
        },
      });
      registerBridgeEventHandlersForTest();

      for (const delta of [
        '已完成代码库速览。',
        '\n\n## 代码库画像\n',
        '- 这是一个多 agent 编排平台',
      ]) {
        bridgeCallback({
          method: 'item/agentMessage/delta',
          payload: {
            threadId: 'thread-1',
            turnId: 'turn-1',
            delta,
            stream: 'message',
          },
        });
      }

      await flushAssistantDeltaBatch();

      expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
        expect.objectContaining({ id: 'user-1', role: 'user', text: 'inspect repo' }),
        expect.objectContaining({
          id: 'assistant-stream-turn-1',
          role: 'assistant',
          text: '已完成代码库速览。\n\n## 代码库画像\n- 这是一个多 agent 编排平台',
          done: false,
        }),
      ]);
    }
    finally {
      vi.useRealTimers();
    }
  });

  it('merges completion into stream messages stored under provider thread aliases', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{
        id: 'thread-1',
        providerThreadId: 'provider-thread-1',
        agentId: 'agent_123',
        name: 'Thread 1',
        provider: 'codex',
        status: 'running',
      }],
      timelinesByThread: {
        'thread-1': [{ id: 'user-1', role: 'user', text: 'say ok', time: '2026-05-30T00:00:00Z' }],
        'provider-thread-1': [{
          id: 'assistant-stream-turn-1',
          role: 'assistant',
          kind: 'assistant',
          text: 'ok',
          done: false,
          runtime: true,
          turnId: 'turn-1',
          time: '2026-05-30T00:01:00Z',
        }],
      },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      method: 'item/completed',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        item: { id: 'assistant-final-turn-1', type: 'agentMessage', text: 'ok' },
      },
    });

    expect(useClientStore.getState().timelinesByThread['provider-thread-1']).toEqual([
      expect.objectContaining({
        id: 'assistant-final-turn-1',
        role: 'assistant',
        text: 'ok',
        done: true,
      }),
    ]);
  });

  it('clears pending assistant delta timers and buffers on store reset', async () => {
    vi.useFakeTimers();
    try {
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
        timelinesByThread: {
          'thread-1': [{ id: 'user-1', role: 'user', text: 'say ok', time: '2026-05-30T00:00:00Z' }],
        },
      });
      registerBridgeEventHandlersForTest();

      bridgeCallback({
        method: 'item/agentMessage/delta',
        payload: {
          threadId: 'thread-1',
          turnId: 'turn-1',
          delta: 'ok',
          stream: 'message',
        },
      });
      resetClientStoreForTests();
      await flushAssistantDeltaBatch();

      expect(useClientStore.getState().timelinesByThread).toEqual({});
    }
    finally {
      vi.useRealTimers();
    }
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
    registerBridgeEventHandlersForTest();

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
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '1',
        timelineItems: [{
          id: 'assistant-from-patch',
          kind: 'assistant',
          text: '你是指：\n\n1. 页面/应用里没有内容了？\n2. 某个文件被清空了？',
          turnId: 'turn-1',
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

  it('does not duplicate assistant messages split by tool calls during turn completion', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
      timelinesByThread: {
        'thread-1': [{ id: 'user-1', role: 'user', text: 'say hi', time: '2026-05-30T00:00:00Z' }],
      },
    });
    registerBridgeEventHandlersForTest();

    // 1. Assistant outputs part 1
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '1',
        timelineItems: [{
          id: 'assistant-part-1',
          kind: 'assistant',
          text: 'hello',
          turnId: 'turn-1',
          createdAt: '2026-05-30T00:01:00Z',
        }],
      },
    });

    // 2. A tool call is made
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '2',
        timelineItems: [{
          id: 'tool-call-1',
          kind: 'toolCall',
          toolName: 'my_tool',
          createdAt: '2026-05-30T00:01:01Z',
        }],
      },
    });

    // 3. Assistant outputs part 2
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '3',
        timelineItems: [{
          id: 'assistant-part-2',
          kind: 'assistant',
          text: 'world',
          turnId: 'turn-1',
          createdAt: '2026-05-30T00:01:02Z',
        }],
      },
    });

    // 4. item/completed is called with the concatenated turn result
    bridgeCallback({
      method: 'item/completed',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        item: {
          id: 'assistant-concatenated',
          type: 'agentMessage',
          text: 'helloworld',
        },
      },
    });

    const timeline = useClientStore.getState().timelinesByThread['thread-1'];
    const assistantMessages = timeline.filter((message) => message.role === 'assistant' && (message.kind === 'assistant' || !message.kind));
    expect(assistantMessages).toHaveLength(2);
    expect(assistantMessages[0]).toEqual(expect.objectContaining({
      id: 'assistant-part-1',
      text: 'hello',
      done: true,
    }));
    expect(assistantMessages[1]).toEqual(expect.objectContaining({
      id: 'assistant-part-2',
      text: 'world',
      done: true,
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
    registerBridgeEventHandlersForTest();

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

  it('removes a loosely matching runtime assistant duplicate when the formatted patch has small content differences', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
      timelinesByThread: {
        'thread-1': [{ id: 'user-1', role: 'user', text: '总结这个 Markdown 文件', time: '2026-06-03T13:15:36Z' }],
      },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      method: 'item/agentMessage/delta',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        stream: 'message',
        delta: '我会用“核心信息提取与总结”技能来提炼这个 Markdown 文件。摘要这个文件是一个 JSON 内容库，包含 5 条抖音爆款短视频脚本，主题覆盖省钱生活、选择困难、亲情愧疚、健身变化和职场面试。内容结构每条视频都包含 title：标题 hook：开场钩子 script：完整短视频脚本 thumbnail_idea：封面设计思路 cta：评论/转发引导语。爆款套路总结：开头都使用强钩子：哭了、懂了、活下去、笑死、实拍变化。',
      },
    });
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '1',
        timelineItems: [{
          id: 'assistant-from-patch',
          kind: 'assistant',
          text: '## 摘要\n\n这个文件是一个 JSON 内容库，包含 5 条抖音爆款短视频脚本，主题覆盖省钱生活、选择困难、亲情愧疚、健身变化和职场面试。\n\n## 内容结构\n\n每条视频都包含：\n\n- `title`：标题\n- `hook`：开场钩子\n- `script`：完整短视频脚本\n- `thumbnail_idea`：封面设计思路\n- `cta`：评论/转发引导语\n\n爆款套路总结：开头都使用强钩子：哭了、懂了、活下去、笑死、实拍变化。',
          createdAt: '2026-06-03T13:15:40Z',
        }],
      },
    });

    const assistantMessages = useClientStore.getState().timelinesByThread['thread-1']
      .filter((message) => message.role === 'assistant');
    expect(assistantMessages).toEqual([
      expect.objectContaining({
        id: 'assistant-from-patch',
        text: expect.stringContaining('## 摘要'),
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
    registerBridgeEventHandlersForTest();

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

  it('replaces a shorter runtime assistant completion when item completion carries the full answer', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
      timelinesByThread: {
        'thread-1': [{ id: 'user-1', role: 'user', text: '检查抖音脚本', time: '2026-06-03T13:15:36Z' }],
      },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      method: 'item/completed',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        timestamp: '2026-06-03T13:15:38Z',
        item: {
          id: 'msg-short-prefix',
          type: 'agentMessage',
          text: '我先读取共享资源里是否有 `reports/douyin_viral_scripts.md`。',
        },
      },
    });
    bridgeCallback({
      method: 'item/completed',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        timestamp: '2026-06-03T13:15:43Z',
        result: '我先读取共享资源里是否有 `reports/douyin_viral_scripts.md`。\n\n已找到脚本文件，接下来会根据模板整理今日任务。',
      },
    });

    const assistantMessages = useClientStore.getState().timelinesByThread['thread-1']
      .filter((message) => message.role === 'assistant');
    expect(assistantMessages).toEqual([
      expect.objectContaining({
        id: 'assistant-final-turn-1',
        text: '我先读取共享资源里是否有 `reports/douyin_viral_scripts.md`。\n\n已找到脚本文件，接下来会根据模板整理今日任务。',
      }),
    ]);
  });

  it('applies fallback turn output deltas with empty stream as assistant message text', async () => {
    vi.useFakeTimers();
    try {
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'claude', status: 'running' }],
        timelinesByThread: {
          'thread-1': [{ id: 'user-1', role: 'user', text: 'say ok', time: '2026-05-30T00:00:00Z' }],
        },
      });
      registerBridgeEventHandlersForTest();

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

      await flushAssistantDeltaBatch();

      expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
        expect.objectContaining({ role: 'user', text: 'say ok' }),
        expect.objectContaining({ id: 'assistant-stream-turn-1', role: 'assistant', text: 'ok', done: false }),
      ]);
    }
    finally {
      vi.useRealTimers();
    }
  });

  it('deduplicates overlapping assistant deltas before merging the formatted patch reply', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
      timelinesByThread: {
        'thread-1': [{ id: 'user-1', role: 'user', text: 'say math', time: '2026-05-30T00:00:00Z' }],
      },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      method: 'item/agentMessage/delta',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        delta: '正常',
        stream: 'message',
      },
    });
    bridgeCallback({
      method: 'item/agentMessage/delta',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        delta: '常数学',
        stream: 'message',
      },
    });
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '1',
        timelineItems: [{
          id: 'assistant-from-patch',
          kind: 'assistant',
          text: '正常数学',
          createdAt: '2026-05-30T00:01:00Z',
        }],
      },
    });

    const assistantMessages = useClientStore.getState().timelinesByThread['thread-1']
      .filter((message) => message.role === 'assistant');
    expect(assistantMessages).toEqual([
      expect.objectContaining({
        id: 'assistant-from-patch',
        text: '正常数学',
      }),
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
    registerBridgeEventHandlersForTest();

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

  it('retains runtime: true protection on completed assistant message when double-channel completed event arrives and later patch omits it', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'user-1', role: 'user', text: 'say ok', done: true }],
      },
    });
    registerBridgeEventHandlersForTest();

    // 1. Stream delta
    bridgeCallback({
      type: 'item/agentMessage/delta',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        delta: 'ok',
      },
    });

    // 2. Item completion and backend snapshot arrive on independent channels.
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '2',
        timelineItems: [
          { id: 'user-1', role: 'user', text: 'say ok', done: true },
          { id: 'msg-final', role: 'assistant', text: 'ok', done: true, turnId: 'turn-1' },
        ],
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

    // Verify it is merged and retains runtime: true
    const timelineAfterCompleted = useClientStore.getState().timelinesByThread['thread-1'];
    const assistantMsg = timelineAfterCompleted.find(m => m.id === 'msg-final');
    expect(assistantMsg).toBeDefined();
    expect(assistantMsg.runtime).toBe(true);

    // 3. Subsequent ui/thread/patch omitting the message
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '3',
        timelineItems: [{ id: 'turn-end:turn-1', kind: 'turn_end', status: 'completed' }],
      },
    });

    // Verify it is still preserved and not discarded
    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ role: 'user', text: 'say ok' }),
      expect.objectContaining({ id: 'msg-final', role: 'assistant', text: 'ok', done: true }),
    ]);
  });

  it('marks visible live timeline bridge patches ready so tool cards survive empty history hydration', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'idle' }],
      timelinesByThread: {},
    });
    backend.getThreadMessages.mockResolvedValue({ messages: [], hasMore: false, nextBefore: '' });
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-1': [] },
      threadTimelineReadyByThread: {},
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '1',
        timelineItems: [{
          id: 'tool-file-read',
          kind: 'tool',
          title: 'file',
          status: 'completed',
          text: '{"success":true}',
          callId: 'call-file',
        }],
      },
    });

    expect(useClientStore.getState().threadTimelineReadyByThread['thread-1']).toBe(true);

    await expect(useClientStore.getState().syncThreadState('thread-1')).resolves.toBe(true);

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ id: 'tool-file-read', kind: 'tool', title: 'file' }),
    ]);
  });

  it('does not mark structural-only bridge patches ready', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-1': [] },
      threadTimelineReadyByThread: {},
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '1',
        timelineItems: [{ id: 'turn-end:turn-1', kind: 'turn_end', status: 'completed' }],
      },
    });

    expect(useClientStore.getState().threadTimelineReadyByThread['thread-1']).toBeUndefined();
    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([]);
  });

  it('drops live ghost command bridge patches emitted immediately after send', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-1': [] },
      threadTimelineReadyByThread: {},
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '1',
        timelineItems: [{
          id: 'ghost-command',
          kind: 'command',
          title: '执行命令',
          status: 'completed',
          done: true,
        }, {
          id: 'tool-shadow-command',
          kind: 'command',
          title: 'file',
          toolName: 'file',
          status: 'completed',
          done: true,
        }],
      },
    });

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([]);
    expect(useClientStore.getState().threadTimelineReadyByThread['thread-1']).toBeUndefined();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '2',
        timelineItems: [{
          id: 'real-command',
          kind: 'command',
          command: 'npm test',
          status: 'running',
        }],
      },
    });

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ id: 'real-command', kind: 'command', command: 'npm test' }),
    ]);
    expect(useClientStore.getState().threadTimelineReadyByThread['thread-1']).toBe(true);
  });

  it('applies bridge patches for timeline, token usage, diff and warnings', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();

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

  it('never publishes success before a failed terminal after item completion', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-open', role: 'assistant', text: 'partial', status: 'running', turnId: 'turn-1' }],
      },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'item/completed',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        item: { id: 'assistant-open', role: 'assistant', text: 'partial' },
      },
    });
    expect(useClientStore.getState().actionNotice).toBeNull();

    bridgeCallback({
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: 'terminal-failed-1',
        threadId: 'thread-1',
        turnId: 'turn-1',
        outcome: 'failed',
        publicError: {
          code: 'PROVIDER_FAILED',
          title: '运行失败',
          message: '提供方未能完成本轮响应',
          diagnosticId: 'diag-failed-1',
          retryable: false,
          recoveryActions: [],
        },
        occurredAt: '2026-07-16T01:00:00Z',
      },
    });

    const state = useClientStore.getState();
    expect(state.actionNotice).toEqual(expect.objectContaining({
      tone: 'error',
      message: expect.stringContaining('提供方未能完成本轮响应'),
    }));
    expect(state.actionNotice.tone).not.toBe('success');
    expect(state.timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ id: 'assistant-open', text: 'partial', done: true }),
      expect.objectContaining({
        kind: 'turn_terminal',
        terminalOutcome: 'failed',
        publicError: expect.objectContaining({ diagnosticId: 'diag-failed-1' }),
      }),
    ]);
    expect(state.warningEntries).toEqual([
      expect.objectContaining({
        level: 'error',
        event: 'turn.terminal.failed',
        threadId: 'thread-1',
      }),
    ]);
  });

  it('replays a canonical terminal after its accepted partial delta arrives out of order', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: 'terminal-out-of-order',
        threadId: 'thread-1',
        turnId: 'turn-1',
        outcome: 'failed',
        publicError: {
          code: 'PROVIDER_FAILED',
          title: '运行失败',
          message: '提供方未能完成本轮响应',
          diagnosticId: 'diag-out-of-order',
          retryable: false,
          recoveryActions: ['copy_diagnostics'],
        },
        partialItemIds: ['partial-1'],
        occurredAt: '2026-07-16T01:00:00Z',
      },
    });
    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([]);

    bridgeCallback({
      type: 'turn/output/delta',
      payload: { threadId: 'thread-1', turnId: 'turn-1', itemId: 'partial-1', delta: 'partial answer' },
    });

    const state = useClientStore.getState();
    expect(state.timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ id: 'partial-1', text: 'partial answer', done: true }),
      expect.objectContaining({ kind: 'turn_terminal', terminalOutcome: 'failed' }),
    ]);
    expect(state.actionNotice).toEqual(expect.objectContaining({
      tone: 'error',
      message: expect.stringContaining('提供方未能完成本轮响应'),
    }));
    expect(state.warningEntries).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ event: 'turn.terminal.contract_invalid' }),
    ]));
  });

  it('keeps the first pending terminal truth when a conflicting terminal arrives before its delta', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: 'terminal-first-pending',
        threadId: 'thread-1',
        turnId: 'turn-1',
        outcome: 'failed',
        publicError: {
          code: 'PROVIDER_FAILED',
          title: '运行失败',
          message: '首个终态失败',
          diagnosticId: 'diag-first-pending',
          retryable: false,
          recoveryActions: [],
        },
        partialItemIds: ['partial-1'],
        occurredAt: '2026-07-19T01:00:00Z',
      },
    });
    bridgeCallback({
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: 'terminal-conflicting-pending',
        threadId: 'thread-1',
        turnId: 'turn-1',
        outcome: 'success',
        occurredAt: '2026-07-19T01:00:01Z',
      },
    });
    bridgeCallback({
      type: 'turn/output/delta',
      payload: { threadId: 'thread-1', turnId: 'turn-1', itemId: 'partial-1', delta: 'partial answer' },
    });

    const state = useClientStore.getState();
    expect(state.timelinesByThread['thread-1']).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'turn_terminal', terminalOutcome: 'failed' }),
    ]));
    expect(state.actionNotice).toEqual(expect.objectContaining({ tone: 'error' }));
    expect(state.warningEntries).toEqual(expect.arrayContaining([
      expect.objectContaining({ event: 'turn.terminal.conflict' }),
    ]));
  });

  it('rejects the oldest late event after sequential turns exceed tombstone capacity', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();

    for (let index = 0; index <= 65; index++) {
      bridgeCallback({
        type: 'turn/output/delta',
        payload: {
          threadId: 'thread-1',
          turnId: `turn-${index}`,
          itemId: `item-${index}`,
          delta: `answer ${index}`,
        },
      });
      bridgeCallback({
        type: 'turn/terminal',
        payload: {
          schemaVersion: 2,
          eventId: `terminal-${index}`,
          threadId: 'thread-1',
          turnId: `turn-${index}`,
          outcome: 'success',
          occurredAt: '2026-07-20T01:00:00Z',
        },
      });
    }

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'turn_terminal', turnId: 'turn-65', terminalOutcome: 'success' }),
    ]));
    expect(useClientStore.getState().getTurnTerminalCacheStats()).toEqual({
      capacity: 64,
      terminalStates: 1,
      observedTurns: 1,
      retiredTurns: 64,
    });

    bridgeCallback({
      type: 'turn/output/delta',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-0',
        itemId: 'late-item',
        delta: 'late mutation',
      },
    });

    const state = useClientStore.getState();
    expect(state.timelinesByThread['thread-1']).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ id: 'late-item' }),
    ]));
    expect(state.warningEntries).toEqual(expect.arrayContaining([
      expect.objectContaining({
        event: 'turn.event.late',
        fields: expect.objectContaining({ turn_id: 'turn-0' }),
      }),
    ]));
  });

  it('keeps pending terminals replayable and fails closed when they fill capacity', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'pending-thread-0',
      threads: Array.from({ length: 65 }, (_, index) => ({
        id: `pending-thread-${index}`,
        name: `Pending ${index}`,
        provider: 'codex',
        status: 'running',
      })),
      timelinesByThread: { 'pending-thread-0': [] },
    });
    registerBridgeEventHandlersForTest();
    const pendingTerminal = (index) => ({
      schemaVersion: 2,
      eventId: `pending-terminal-${index}`,
      threadId: `pending-thread-${index}`,
      turnId: `pending-turn-${index}`,
      outcome: 'failed',
      publicError: {
        code: 'PROVIDER_FAILED',
        title: '运行失败',
        message: `第 ${index} 个缺失部分响应`,
        diagnosticId: `diag-pending-${index}`,
        retryable: false,
        recoveryActions: [],
      },
      partialItemIds: [`partial-${index}`],
      occurredAt: '2026-07-20T01:00:00Z',
    });

    for (let index = 0; index < 64; index++) {
      bridgeCallback({ type: 'turn/terminal', payload: pendingTerminal(index) });
    }

    expect(useClientStore.getState().getTurnTerminalCacheStats()).toEqual({
      capacity: 64,
      terminalStates: 64,
      observedTurns: 64,
      retiredTurns: 0,
    });
    bridgeCallback({ type: 'turn/terminal', payload: pendingTerminal(64) });
    expect(useClientStore.getState().getTurnTerminalCacheStats()).toEqual({
      capacity: 64,
      terminalStates: 64,
      observedTurns: 64,
      retiredTurns: 0,
    });
    expect(useClientStore.getState().warningEntries).toEqual(expect.arrayContaining([
      expect.objectContaining({
        event: 'turn.terminal.cache_exhausted',
        fields: expect.objectContaining({ turn_id: 'pending-turn-64', reason: 'capacity' }),
      }),
    ]));

    bridgeCallback({
      type: 'turn/output/delta',
      payload: {
        threadId: 'pending-thread-0',
        turnId: 'pending-turn-0',
        itemId: 'partial-0',
        delta: 'replayed partial',
      },
    });

    const state = useClientStore.getState();
    expect(state.timelinesByThread['pending-thread-0']).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: 'partial-0', text: 'replayed partial', done: true }),
      expect.objectContaining({ kind: 'turn_terminal', turnId: 'pending-turn-0', terminalOutcome: 'failed' }),
    ]));
    expect(state.getTurnTerminalCacheStats()).toEqual({
      capacity: 64,
      terminalStates: 64,
      observedTurns: 64,
      retiredTurns: 0,
    });
  });

  it('does not evict active turn references when terminal capacity is full', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'active-thread-0',
      activeTurnByThread: Object.fromEntries(Array.from({ length: 65 }, (_, index) => [
        `active-thread-${index}`,
        { id: `active-turn-${index}`, status: 'running' },
      ])),
      threads: Array.from({ length: 65 }, (_, index) => ({
        id: `active-thread-${index}`,
        name: `Active ${index}`,
        provider: 'codex',
        status: 'running',
      })),
      timelinesByThread: { 'active-thread-0': [] },
    });
    registerBridgeEventHandlersForTest();

    for (let index = 0; index < 64; index++) {
      bridgeCallback({
        type: 'turn/terminal',
        payload: {
          schemaVersion: 2,
          eventId: `active-terminal-${index}`,
          threadId: `active-thread-${index}`,
          turnId: `active-turn-${index}`,
          outcome: 'success',
          occurredAt: '2026-07-20T01:00:00Z',
        },
      });
    }

    expect(useClientStore.getState().getTurnTerminalCacheStats()).toEqual({
      capacity: 64,
      terminalStates: 64,
      observedTurns: 64,
      retiredTurns: 0,
    });
    bridgeCallback({
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: 'active-terminal-64',
        threadId: 'active-thread-64',
        turnId: 'active-turn-64',
        outcome: 'success',
        occurredAt: '2026-07-20T01:00:00Z',
      },
    });

    expect(useClientStore.getState().timelinesByThread['active-thread-64']).toBeUndefined();
    expect(useClientStore.getState().getTurnTerminalCacheStats()).toEqual({
      capacity: 64,
      terminalStates: 64,
      observedTurns: 64,
      retiredTurns: 0,
    });
    bridgeCallback({
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: 'active-terminal-conflict-0',
        threadId: 'active-thread-0',
        turnId: 'active-turn-0',
        outcome: 'success',
        occurredAt: '2026-07-20T01:00:01Z',
      },
    });
    expect(useClientStore.getState().warningEntries).toEqual(expect.arrayContaining([
      expect.objectContaining({
        event: 'turn.terminal.cache_exhausted',
        fields: expect.objectContaining({ turn_id: 'active-turn-64', reason: 'capacity' }),
      }),
      expect.objectContaining({
        event: 'turn.terminal.conflict',
        fields: expect.objectContaining({ turn_id: 'active-turn-0' }),
      }),
    ]));
  });

  it('seals the first terminal and routes conflicting or late turn events to diagnostics', async () => {
    vi.useFakeTimers();
    try {
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
        timelinesByThread: { 'thread-1': [] },
      });
      registerBridgeEventHandlersForTest();

      bridgeCallback({
        type: 'turn/output/delta',
        payload: { threadId: 'thread-1', turnId: 'turn-1', itemId: 'partial-1', delta: 'partial answer' },
      });
      const firstTerminal = {
        type: 'turn/terminal',
        payload: {
          schemaVersion: 2,
          eventId: 'terminal-first',
          threadId: 'thread-1',
          turnId: 'turn-1',
          outcome: 'failed',
          publicError: {
            code: 'FAILED',
            title: '运行失败',
            message: '本轮执行失败',
            diagnosticId: 'diag-first',
            retryable: false,
            recoveryActions: ['copy_diagnostics'],
          },
          partialItemIds: ['partial-1'],
          occurredAt: '2026-07-16T01:00:00Z',
        },
      };
      bridgeCallback(firstTerminal);
      backend.emitFrontendTraceEvent.mockClear();
      bridgeCallback(firstTerminal);
      expect(backend.emitFrontendTraceEvent).not.toHaveBeenCalled();
      bridgeCallback({
        type: 'turn/terminal',
        payload: {
          schemaVersion: 2,
          eventId: 'terminal-first',
          threadId: 'thread-1',
          turnId: 'turn-1',
          outcome: 'success',
          occurredAt: '2026-07-16T01:00:01Z',
        },
      });
      bridgeCallback({
        ...firstTerminal,
        payload: {
          ...firstTerminal.payload,
          eventId: 'terminal-replayed-content',
        },
      });
      bridgeCallback({
        type: 'turn/terminal',
        payload: {
          schemaVersion: 2,
          eventId: 'terminal-conflict',
          threadId: 'thread-1',
          turnId: 'turn-1',
          outcome: 'success',
          occurredAt: '2026-07-16T01:00:01Z',
        },
      });
      bridgeCallback({
        type: 'turn/output/delta',
        payload: { threadId: 'thread-1', turnId: 'turn-2', itemId: 'partial-2', delta: 'next turn' },
      });
      bridgeCallback({
        type: 'turn/output/delta',
        payload: {
          threadId: 'thread-1',
          turnId: 'turn-1',
          itemId: 'partial-late',
          delta: 'late mutation token=super-secret-value',
        },
      });
      await flushAssistantDeltaBatch();

      const state = useClientStore.getState();
      expect(state.actionNotice.tone).toBe('error');
      expect(state.timelinesByThread['thread-1']).toEqual([
        expect.objectContaining({ id: 'partial-1', text: 'partial answer', done: true }),
        expect.objectContaining({ terminalOutcome: 'failed' }),
        expect.objectContaining({ id: 'partial-2', text: 'next turn', done: false }),
      ]);
      expect(state.timelinesByThread['thread-1']).not.toEqual(expect.arrayContaining([
        expect.objectContaining({ text: expect.stringContaining('late mutation') }),
      ]));
      expect(state.warningEntries).toEqual([
        expect.objectContaining({
          event: 'turn.event.late',
          threadId: 'thread-1',
          fields: expect.objectContaining({ eventName: 'turn/output/delta', turn_id: 'turn-1' }),
          occurrenceCount: 1,
        }),
        expect.objectContaining({
          event: 'turn.terminal.conflict',
          threadId: 'thread-1',
          fields: expect.objectContaining({ eventName: 'turn/terminal', turn_id: 'turn-1' }),
          occurrenceCount: 3,
        }),
        expect.objectContaining({ event: 'turn.terminal.failed' }),
      ]);
      expect(JSON.stringify(state.warningEntries)).not.toContain('super-secret-value');
      expect(JSON.stringify(backend.emitFrontendTraceEvent.mock.calls)).not.toContain('super-secret-value');
      expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
        phase: 'frontend.turn_event.rejected',
        method: 'turn.terminal.conflict',
        thread_id: 'thread-1',
        turn_id: 'turn-1',
      }));
      expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
        phase: 'frontend.turn_event.rejected',
        method: 'turn.event.late',
        thread_id: 'thread-1',
        turn_id: 'turn-1',
      }));
    } finally {
      vi.useRealTimers();
    }
  });

  it('rejects a stale terminal when a newer turn is active without changing UI truth', () => {
    const actionNotice = { message: '新一轮仍在运行', tone: 'info' };
    const timeline = [
      { id: 'turn-1-answer', role: 'assistant', text: 'older answer', done: true, turnId: 'turn-1' },
      { id: 'turn-2-answer', role: 'assistant', text: 'new answer', done: false, turnId: 'turn-2' },
    ];
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      activeTurnByThread: { 'thread-1': { id: 'turn-2', status: 'running' } },
      actionNotice,
      timelinesByThread: { 'thread-1': timeline },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: 'terminal-stale-turn-1',
        threadId: 'thread-1',
        turnId: 'turn-1',
        outcome: 'success',
        occurredAt: '2026-07-16T01:00:00Z',
      },
    });

    const state = useClientStore.getState();
    expect(state.timelinesByThread['thread-1']).toBe(timeline);
    expect(state.actionNotice).toBe(actionNotice);
    expect(state.timelinesByThread['thread-1'][1]).toEqual(expect.objectContaining({ done: false, turnId: 'turn-2' }));
    expect(state.warningEntries).toEqual([
      expect.objectContaining({
        event: 'turn.terminal.stale',
        fields: expect.objectContaining({ eventName: 'turn/terminal', turn_id: 'turn-1' }),
        occurrenceCount: 1,
      }),
    ]);
    expect(state.activityEntries).toEqual([]);
    expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'frontend.turn_event.rejected',
      method: 'turn.terminal.stale',
      thread_id: 'thread-1',
      turn_id: 'turn-1',
    }));
  });

  it('rejects item completion after the same turn is sealed', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-turn-1', role: 'assistant', text: 'sealed answer', done: false, turnId: 'turn-1' }],
      },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: 'terminal-turn-1',
        threadId: 'thread-1',
        turnId: 'turn-1',
        outcome: 'success',
        occurredAt: '2026-07-16T01:00:00Z',
      },
    });
    const sealedTimeline = useClientStore.getState().timelinesByThread['thread-1'];
    const sealedNotice = useClientStore.getState().actionNotice;

    bridgeCallback({
      type: 'item/completed',
      payload: {
        threadId: 'thread-1',
        turnId: 'turn-1',
        item: { id: 'assistant-turn-1', type: 'assistant', text: 'late replacement' },
      },
    });

    const state = useClientStore.getState();
    expect(state.timelinesByThread['thread-1']).toBe(sealedTimeline);
    expect(state.actionNotice).toBe(sealedNotice);
    expect(state.warningEntries).toEqual([
      expect.objectContaining({
        event: 'turn.event.late',
        fields: expect.objectContaining({ eventName: 'item/completed', turn_id: 'turn-1' }),
        occurrenceCount: 1,
      }),
    ]);
    expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'frontend.turn_event.rejected',
      method: 'turn.event.late',
      thread_id: 'thread-1',
      turn_id: 'turn-1',
    }));
  });

  it.each([
    ['assistant delta', { type: 'turn/output/delta', payload: { threadId: 'thread-1', itemId: 'assistant-open', delta: 'late text' } }],
    ['reasoning delta', { type: 'item/reasoning/textDelta', payload: { threadId: 'thread-1', delta: 'late thought' } }],
    ['command output', { type: 'item/commandExecution/outputDelta', payload: { threadId: 'thread-1', delta: 'late output' } }],
    ['item completion', { type: 'item/completed', payload: { threadId: 'thread-1', item: { id: 'assistant-open', type: 'assistant', text: 'late final' } } }],
  ])('rejects %s without a canonical TurnRef before mutating UI state', async (_label, event) => {
    vi.useFakeTimers();
    try {
      const actionNotice = { message: '保持原状态', tone: 'info' };
      const timeline = [{ id: 'assistant-open', role: 'assistant', kind: 'command', text: 'existing', done: false, turnId: 'turn-1' }];
      const activityEntries = [{ id: 'existing-activity', method: 'existing', threadId: 'thread-1' }];
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        actionNotice,
        activityEntries,
        timelinesByThread: { 'thread-1': timeline },
      });
      registerBridgeEventHandlersForTest();

      bridgeCallback(event);
      await flushAssistantDeltaBatch();

      const state = useClientStore.getState();
      expect(state.timelinesByThread['thread-1']).toBe(timeline);
      expect(state.actionNotice).toBe(actionNotice);
      expect(state.activityEntries).toBe(activityEntries);
      expect(state.warningEntries).toEqual([
        expect.objectContaining({ level: 'error', event: 'turn.event.contract_invalid' }),
      ]);
    } finally {
      vi.useRealTimers();
    }
  });

  it('rejects every sealed turn event through telemetry without mutating UI state', async () => {
    vi.useFakeTimers();
    try {
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        timelinesByThread: {
          'thread-1': [
            { id: 'assistant-turn-1', role: 'assistant', kind: 'assistant', text: 'answer', done: false, turnId: 'turn-1' },
            { id: 'command-turn-1', role: 'assistant', kind: 'command', text: 'command', done: false, turnId: 'turn-1' },
          ],
        },
      });
      registerBridgeEventHandlersForTest();
      bridgeCallback({
        type: 'turn/terminal',
        payload: {
          schemaVersion: 2,
          eventId: 'terminal-sealed-turn-1',
          threadId: 'thread-1',
          turnId: 'turn-1',
          outcome: 'success',
          occurredAt: '2026-07-16T01:00:00Z',
        },
      });
      const sealedState = useClientStore.getState();
      const timeline = sealedState.timelinesByThread['thread-1'];
      const actionNotice = sealedState.actionNotice;
      const activityEntries = sealedState.activityEntries;
      backend.emitFrontendTraceEvent.mockClear();

      bridgeCallback({ type: 'turn/output/delta', payload: { threadId: 'thread-1', turnId: 'turn-1', itemId: 'assistant-turn-1', delta: 'late assistant' } });
      bridgeCallback({ type: 'item/reasoning/textDelta', payload: { threadId: 'thread-1', turnId: 'turn-1', delta: 'late thought' } });
      bridgeCallback({ type: 'item/commandExecution/outputDelta', payload: { threadId: 'thread-1', turnId: 'turn-1', delta: 'late output' } });
      bridgeCallback({ type: 'item/completed', payload: { threadId: 'thread-1', turnId: 'turn-1', item: { id: 'assistant-turn-1', type: 'assistant', text: 'late final' } } });
      await flushAssistantDeltaBatch();

      const state = useClientStore.getState();
      expect(state.timelinesByThread['thread-1']).toBe(timeline);
      expect(state.actionNotice).toBe(actionNotice);
      expect(state.activityEntries).toBe(activityEntries);
      expect(state.warningEntries).toEqual([
        expect.objectContaining({
          event: 'turn.event.late',
          fields: expect.objectContaining({ eventName: 'item/completed', turn_id: 'turn-1' }),
          occurrenceCount: 4,
        }),
      ]);
      expect(backend.emitFrontendTraceEvent.mock.calls.filter(([payload]) => (
        payload.phase === 'frontend.turn_event.rejected'
      ))).toHaveLength(4);
      expect(backend.emitFrontendTraceEvent.mock.calls.filter(([payload]) => (
        payload.phase === 'frontend.warning' && payload.method === 'turn.event.late'
      ))).toHaveLength(4);
      expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
        phase: 'frontend.turn_event.rejected',
        method: 'turn.event.late',
        thread_id: 'thread-1',
        turn_id: 'turn-1',
      }));
    } finally {
      vi.useRealTimers();
    }
  });

  it.each([
    ['assistant delta', { type: 'turn/output/delta', payload: { threadId: 'thread-1', turnId: 'turn-1', itemId: 'late-turn-1', delta: 'late answer' } }],
    ['item completion', { type: 'item/completed', payload: { threadId: 'thread-1', turnId: 'turn-1', item: { id: 'late-turn-1', type: 'assistant', text: 'late final' } } }],
  ])('rejects stale %s when the active turn is authoritative', async (_label, event) => {
    vi.useFakeTimers();
    try {
      const activeTurn = { id: 'turn-2', status: 'running' };
      const actionNotice = { message: 'T2 正在运行', tone: 'info' };
      const activityEntries = [{ id: 'existing-activity', method: 'turn/started', threadId: 'thread-1' }];
      const warningEntries = [];
      const timeline = [{ id: 'turn-2-open', role: 'assistant', kind: 'assistant', text: 'current', done: false, turnId: 'turn-2' }];
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        activeTurnByThread: { 'thread-1': activeTurn },
        actionNotice,
        activityEntries,
        warningEntries,
        timelinesByThread: { 'thread-1': timeline },
      });
      registerBridgeEventHandlersForTest();

      bridgeCallback(event);
      await flushAssistantDeltaBatch();

      const state = useClientStore.getState();
      expect(state.timelinesByThread['thread-1']).toBe(timeline);
      expect(state.actionNotice).toBe(actionNotice);
      expect(state.activityEntries).toBe(activityEntries);
      expect(state.warningEntries).toEqual([
        expect.objectContaining({
          event: 'turn.event.stale',
          fields: expect.objectContaining({ eventName: event.type, turn_id: 'turn-1' }),
          occurrenceCount: 1,
        }),
      ]);
      expect(state.activeTurnByThread['thread-1']).toBe(activeTurn);
      expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
        phase: 'frontend.turn_event.rejected',
        method: 'turn.event.stale',
        thread_id: 'thread-1',
        turn_id: 'turn-1',
      }));

      bridgeCallback({
        type: 'turn/terminal',
        payload: {
          schemaVersion: 2,
          eventId: `terminal-turn-2-after-${event.type}`,
          threadId: 'thread-1',
          turnId: 'turn-2',
          outcome: 'success',
          occurredAt: '2026-07-16T01:00:00Z',
        },
      });
      expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual(expect.arrayContaining([
        expect.objectContaining({ kind: 'turn_terminal', turnId: 'turn-2', terminalOutcome: 'success' }),
      ]));
    } finally {
      vi.useRealTimers();
    }
  });

  it.each(['success', 'failed'])('retires an observed turn when a patch selects T2 and rejects stale T1 %s terminals without mutation', async (outcome) => {
    vi.useFakeTimers();
    try {
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        actionNotice: { message: 'T1 is streaming', tone: 'info' },
        timelinesByThread: { 'thread-1': [] },
      });
      registerBridgeEventHandlersForTest();

      bridgeCallback({
        type: 'turn/output/delta',
        payload: { threadId: 'thread-1', turnId: 'turn-1', itemId: 'turn-1-open', delta: 'T1 partial' },
      });
      bridgeCallback({
        type: 'ui/thread/patch',
        payload: {
          threadId: 'thread-1',
          sequence: '1',
          status: 'running',
          activeTurn: { id: 'turn-2', status: 'running' },
        },
      });

      const beforeTerminal = useClientStore.getState();
      const timeline = beforeTerminal.timelinesByThread['thread-1'];
      const actionNotice = beforeTerminal.actionNotice;
      const activityEntries = beforeTerminal.activityEntries;

      bridgeCallback({
        type: 'turn/terminal',
        payload: {
          schemaVersion: 2,
          eventId: `terminal-stale-turn-1-${outcome}`,
          threadId: 'thread-1',
          turnId: 'turn-1',
          outcome,
          ...(outcome === 'failed' ? {
            publicError: {
              code: 'PROVIDER_FAILED',
              title: 'Provider failed',
              message: 'T1 failed',
              diagnosticId: 'diag-stale-turn-1',
              retryable: false,
              recoveryActions: [],
            },
          } : {}),
          occurredAt: '2026-07-18T01:00:00Z',
        },
      });
      await flushAssistantDeltaBatch();

      const state = useClientStore.getState();
      expect(state.timelinesByThread['thread-1']).toBe(timeline);
      expect(state.actionNotice).toBe(actionNotice);
      expect(state.activityEntries).toBe(activityEntries);
      expect(state.timelinesByThread['thread-1']).toEqual(expect.arrayContaining([
        expect.objectContaining({ id: 'turn-1-open', text: 'T1 partial', done: false, turnId: 'turn-1' }),
      ]));
      expect(state.timelinesByThread['thread-1']).not.toEqual(expect.arrayContaining([
        expect.objectContaining({ kind: 'turn_terminal', turnId: 'turn-1' }),
      ]));
      expect(state.warningEntries).toEqual([
        expect.objectContaining({
          event: 'turn.terminal.stale',
          fields: expect.objectContaining({ eventName: 'turn/terminal', turn_id: 'turn-1' }),
          occurrenceCount: 1,
        }),
      ]);
      expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
        phase: 'frontend.turn_event.rejected',
        method: 'turn.terminal.stale',
        thread_id: 'thread-1',
        turn_id: 'turn-1',
      }));
    } finally {
      vi.useRealTimers();
    }
  });

  it('accepts T2 first terminal after a patch retires the observed T1 turn', async () => {
    vi.useFakeTimers();
    try {
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        timelinesByThread: { 'thread-1': [] },
      });
      registerBridgeEventHandlersForTest();

      bridgeCallback({
        type: 'turn/output/delta',
        payload: { threadId: 'thread-1', turnId: 'turn-1', itemId: 'turn-1-open', delta: 'T1 partial' },
      });
      bridgeCallback({
        type: 'ui/thread/patch',
        payload: {
          threadId: 'thread-1',
          sequence: '1',
          status: 'running',
          activeTurn: { id: 'turn-2', status: 'running' },
        },
      });
      bridgeCallback({
        type: 'turn/terminal',
        payload: {
          schemaVersion: 2,
          eventId: 'terminal-active-turn-2',
          threadId: 'thread-1',
          turnId: 'turn-2',
          outcome: 'success',
          occurredAt: '2026-07-18T01:00:01Z',
        },
      });
      await flushAssistantDeltaBatch();

      const state = useClientStore.getState();
      expect(state.timelinesByThread['thread-1']).toEqual(expect.arrayContaining([
        expect.objectContaining({ id: 'turn-1-open', text: 'T1 partial', done: false, turnId: 'turn-1' }),
        expect.objectContaining({ kind: 'turn_terminal', turnId: 'turn-2', terminalOutcome: 'success' }),
      ]));
      expect(state.actionNotice).toEqual(expect.objectContaining({ message: '已收到回复', tone: 'success' }));
      expect(state.warningEntries).toEqual([]);
    } finally {
      vi.useRealTimers();
    }
  });

  it('does not flush or finalize a newer buffered turn when an older terminal arrives before the active-turn patch', async () => {
    vi.useFakeTimers();
    try {
      const actionNotice = { message: '旧轮仍显示运行中', tone: 'info' };
      const timeline = [{ id: 'turn-1-open', role: 'assistant', kind: 'assistant', text: 'old', done: false, turnId: 'turn-1' }];
      const activityEntries = [];
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        actionNotice,
        activityEntries,
        timelinesByThread: { 'thread-1': timeline },
      });
      registerBridgeEventHandlersForTest();

      bridgeCallback({
        type: 'turn/output/delta',
        payload: { threadId: 'thread-1', turnId: 'turn-2', itemId: 'turn-2-open', delta: 'new turn' },
      });
      bridgeCallback({
        type: 'turn/terminal',
        payload: {
          schemaVersion: 2,
          eventId: 'terminal-late-turn-1',
          threadId: 'thread-1',
          turnId: 'turn-1',
          outcome: 'success',
          occurredAt: '2026-07-16T01:00:00Z',
        },
      });

      const beforeFlush = useClientStore.getState();
      expect(beforeFlush.timelinesByThread['thread-1']).toBe(timeline);
      expect(beforeFlush.actionNotice).toBe(actionNotice);
      expect(beforeFlush.activityEntries).toBe(activityEntries);
      expect(beforeFlush.warningEntries).toEqual([
        expect.objectContaining({
          event: 'turn.terminal.stale',
          fields: expect.objectContaining({ eventName: 'turn/terminal', turn_id: 'turn-1' }),
          occurrenceCount: 1,
        }),
      ]);
      expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
        phase: 'frontend.turn_event.rejected',
        method: 'turn.terminal.stale',
        turn_id: 'turn-1',
      }));

      await flushAssistantDeltaBatch();
      expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
        expect.objectContaining({ id: 'turn-1-open', done: false, turnId: 'turn-1' }),
        expect.objectContaining({ id: 'turn-2-open', done: false, turnId: 'turn-2' }),
      ]);
    } finally {
      vi.useRealTimers();
    }
  });

  it('cleans a pending terminal when a thread lifecycle ends', async () => {
    vi.useFakeTimers();
    try {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();
    bridgeCallback({
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: 'terminal-before-delete-without-delta',
        threadId: 'thread-1',
        turnId: 'turn-1',
        outcome: 'failed',
        publicError: {
          code: 'PROVIDER_FAILED',
          title: '运行失败',
          message: '删除前缺失部分响应',
          diagnosticId: 'diag-before-delete',
          retryable: false,
          recoveryActions: [],
        },
        partialItemIds: ['partial-1'],
        occurredAt: '2026-07-19T01:00:00Z',
      },
    });

    await useClientStore.getState().deleteStaleThreads(['thread-1']);
    useClientStore.setState({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Recreated', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-1': [] },
      actionNotice: null,
      activityEntries: [],
      warningEntries: [],
    });
    bridgeCallback({
      type: 'turn/output/delta',
      payload: { threadId: 'thread-1', turnId: 'turn-1', itemId: 'partial-1', delta: 'new lifecycle partial' },
    });
    await flushAssistantDeltaBatch();

    const state = useClientStore.getState();
    expect(state.timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ id: 'partial-1', text: 'new lifecycle partial', done: false }),
    ]);
    expect(state.timelinesByThread['thread-1']).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'turn_terminal' }),
    ]));
    expect(state.actionNotice).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it('evicts sealed terminal state when a thread lifecycle ends', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();
    const terminal = {
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: 'terminal-before-delete',
        threadId: 'thread-1',
        turnId: 'turn-1',
        outcome: 'success',
        occurredAt: '2026-07-16T01:00:00Z',
      },
    };
    bridgeCallback(terminal);

    await useClientStore.getState().deleteStaleThreads(['thread-1']);
    useClientStore.setState({
      activeThreadId: 'thread-1',
      activeTurnByThread: { 'thread-1': { id: 'turn-2', status: 'running' } },
      threads: [{ id: 'thread-1', name: 'Recreated', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-1': [] },
      actionNotice: null,
      activityEntries: [],
      warningEntries: [],
    });
    backend.emitFrontendTraceEvent.mockClear();

    bridgeCallback(terminal);

    expect(useClientStore.getState()).toMatchObject({
      actionNotice: null,
      activityEntries: [],
      warningEntries: [
        expect.objectContaining({
          event: 'turn.terminal.stale',
          fields: expect.objectContaining({ eventName: 'turn/terminal', turn_id: 'turn-1' }),
          occurrenceCount: 1,
        }),
      ],
      timelinesByThread: { 'thread-1': [] },
    });
    expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'frontend.turn_event.rejected',
      method: 'turn.terminal.stale',
      thread_id: 'thread-1',
      turn_id: 'turn-1',
    }));
  });

  it('rejects legacy or malformed terminal payloads into a visible contract error sink', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'turn/completed',
      payload: { threadId: 'thread-1', turnId: 'turn-1', success: true },
    });

    const state = useClientStore.getState();
    expect(state.actionNotice).toEqual(expect.objectContaining({
      tone: 'error',
      message: '响应契约错误',
    }));
    expect(state.warningEntries).toEqual([
      expect.objectContaining({ event: 'turn.terminal.contract_invalid' }),
    ]);
    expect(state.timelinesByThread['thread-1']).toEqual([]);
  });

  it.each([
    ['cancelled', '本轮已取消'],
    ['interrupted', '本轮已中断'],
  ])('keeps a user-requested %s terminal visibly non-successful', (outcome, message) => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: `terminal-${outcome}-1`,
        threadId: 'thread-1',
        turnId: 'turn-1',
        outcome,
        terminationCause: 'user_request',
        terminationRequestId: 'stop-1',
        occurredAt: '2026-07-16T01:00:00Z',
      },
    });

    const state = useClientStore.getState();
    expect(state.actionNotice).toEqual(expect.objectContaining({ tone: 'info', message }));
    expect(state.actionNotice.tone).not.toBe('success');
    expect(state.timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ kind: 'turn_terminal', terminalOutcome: outcome }),
    ]);
    expect(state.warningEntries).toEqual([]);
  });

  it('routes malformed bridge event parse failures into visible warnings', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'bridge.event.parse_failed',
      payload: {
        eventName: 'bridge-event',
        error: 'Unexpected end of JSON input',
        rawLen: 10,
        rawPreview: '{"method":',
      },
    });

    expect(useClientStore.getState().warningEntries).toEqual([
      expect.objectContaining({
        level: 'error',
        event: 'bridge.event.parse_failed',
        fields: expect.objectContaining({
          eventName: 'bridge-event',
          error: '[redacted]',
          rawLen: 10,
        }),
      }),
    ]);
    expect(useClientStore.getState().warningEntries[0].fields).not.toHaveProperty('rawPreview');
  });

  it('routes bridge events without a method into visible warnings', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({ payload: { source: 'runtime', rawPreview: '{}' } });

    expect(useClientStore.getState().warningEntries).toEqual([
      expect.objectContaining({
        level: 'error',
        event: 'bridge.event.method_missing',
        fields: expect.objectContaining({
          payloadKeys: ['source', 'rawPreview'],
        }),
      }),
    ]);
    expect(useClientStore.getState().warningEntries[0].fields).not.toHaveProperty('payload');
  });

  it('normalizes legacy token usage pushes like the Vue frontend', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Demo', provider: 'codex' }],
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'thread/tokenUsage/updated',
      payload: {
        threadId: 'thread-1',
        input_tokens: 40000,
        output_tokens: 2000,
        context_window: 258400,
      },
    });

    const usage = useClientStore.getState().tokenUsageByThread['thread-1'];
    expect(usage.usedTokens).toBe(42000);
    expect(usage.contextWindowTokens).toBe(258400);
    expect(usage.usedPercent).toBeCloseTo((42000 / 258400) * 100, 6);
  });

  it('normalizes Codex raw tokenUsage.last ahead of cumulative tokenUsage.total', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Demo', provider: 'codex' }],
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'thread/tokenUsage/updated',
      payload: {
        threadId: 'thread-1',
        tokenUsage: {
          total: { inputTokens: 4000000, outputTokens: 465418, totalTokens: 4465418 },
          last: { inputTokens: 88502, outputTokens: 557 },
        },
        modelContextWindow: 258400,
      },
    });

    const usage = useClientStore.getState().tokenUsageByThread['thread-1'];
    expect(usage.usedTokens).toBe(89059);
    expect(usage.contextWindowTokens).toBe(258400);
    expect(usage.usedPercent).toBeCloseTo((89059 / 258400) * 100, 6);
  });

  it('normalizes Codex info.last_token_usage ahead of info.total_token_usage', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Demo', provider: 'codex' }],
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'thread/tokenUsage/updated',
      payload: {
        threadId: 'thread-1',
        info: {
          total_token_usage: { input_tokens: 4000000, output_tokens: 465418, total_tokens: 4465418 },
          last_token_usage: { input_tokens: 88502, output_tokens: 557, total_tokens: 89059 },
          model_context_window: 258400,
        },
      },
    });

    const usage = useClientStore.getState().tokenUsageByThread['thread-1'];
    expect(usage.usedTokens).toBe(89059);
    expect(usage.contextWindowTokens).toBe(258400);
    expect(usage.usedPercent).toBeCloseTo((89059 / 258400) * 100, 6);
  });

  it('caps legacy token usage percentages without replacing current totals with cumulative totals', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Demo', provider: 'codex' }],
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'thread/tokenUsage/updated',
      payload: {
        threadId: 'thread-1',
        input: 900000,
        output: 50000,
        total_tokens: 950000,
        context_window: 872000,
      },
    });

    expect(useClientStore.getState().tokenUsageByThread['thread-1']).toEqual({
      usedTokens: 950000,
      contextWindowTokens: 872000,
      usedPercent: 100,
    });
  });

  it('deduplicates repeated terminal tool ids from one bridge patch', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '9007199254740993124',
        timelineItems: [{
          id: 'tool:21:file',
          kind: 'tool',
          tool: 'file',
          status: 'completed',
          preview: '{"success":true}',
          output: 'stale duplicate result',
          ts: '2026-06-02T08:00:01Z',
        }, {
          id: 'tool:21:file',
          kind: 'tool',
          tool: 'file',
          status: 'completed',
          output: 'package codexapp',
          ts: '2026-06-02T08:00:01Z',
        }],
      },
    });

    const timeline = useClientStore.getState().timelinesByThread['thread-1'];
    expect(timeline.filter((item) => item.id === 'tool:21:file')).toHaveLength(1);
    expect(timeline[0]).toEqual(expect.objectContaining({
      id: 'tool:21:file',
      status: 'completed',
      text: expect.stringContaining('package codexapp'),
    }));
  });

  it('keys bridge diff patches by nested thread id before agent id', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Existing', provider: 'codex', status: 'running' }],
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();

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

  it('allows matching activeThreadId even when the payload threadId has agent runtime id format', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_1780669491412230000',
      threads: [],
      timelinesByThread: {},
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'agent_1780669491412230000',
        diffText: 'some diff text',
      },
    });

    const state = useClientStore.getState();
    expect(state.diffTextByThread['agent_1780669491412230000']).toBe('some diff text');
    expect(state.warningEntries).not.toEqual([
      expect.objectContaining({ event: 'thread.patch.unknown_thread' }),
    ]);
  });

  it('records tool result timeline items for the runtime log while preserving warnings', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();

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
        detail: '[redacted]',
      }),
    ]);
    expect(state.runtimeResultEntries[0].message).not.toContain('src/App.jsx');
    expect(JSON.stringify(state.runtimeResultEntries[0].fields)).not.toContain('src/App.jsx');
  });

  it('preserves backend thinking start and duration fields for elapsed-time display', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        sequence: '9007199254740993125',
        timelineItems: [{
          id: 'thinking-started-at',
          kind: 'thinking',
          text: 'grep',
          started_at: '2026-05-30T08:00:00Z',
          duration_ms: 2300,
          done: true,
        }],
      },
    });

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({
        id: 'thinking-started-at',
        kind: 'thinking',
        time: '2026-05-30T08:00:00Z',
        elapsedMs: 2300,
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
    registerBridgeEventHandlersForTest();

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

  it('applies restarted thread patch sequences when generation advances', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        generation: 1,
        sequence: '1',
        timelineItems: [{ id: 'assistant-1', kind: 'assistant', text: 'first generation' }],
      },
    });
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        generation: 1,
        sequence: '2',
        timelineItems: [{ id: 'assistant-2', kind: 'assistant', text: 'second patch' }],
      },
    });
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        generation: 2,
        sequence: '1',
        timelineItems: [{ id: 'assistant-restarted', kind: 'assistant', text: 'restarted generation' }],
      },
    });

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ id: 'assistant-1', text: 'first generation' }),
      expect.objectContaining({ id: 'assistant-2', text: 'second patch' }),
      expect.objectContaining({ id: 'assistant-restarted', text: 'restarted generation' }),
    ]);
  });

  it('rejects stale thread patch generation after restart advances', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: { 'thread-1': [] },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        generation: '2',
        sequence: '1',
        timelineItems: [{ id: 'assistant-current', kind: 'assistant', text: 'current generation' }],
      },
    });
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        generation: '1',
        sequence: '99',
        timelineItems: [{ id: 'assistant-stale-generation', kind: 'assistant', text: 'stale generation' }],
      },
    });

    expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
      expect.objectContaining({ id: 'assistant-current', text: 'current generation' }),
    ]);
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

  it('emits failed warning entries to frontend observability traces', () => {
    useClientStore.getState().addWarning('warn', 'memory.badge.refresh.failed', {
      error: '记忆中心加载超时，请检查记忆数据或后端状态。',
      traceId: 'trace-memory-1',
      spanId: 'span-memory-1',
      threadId: 'thread-1',
      req_id: 17,
    });

    expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'frontend.warning',
      method: 'memory.badge.refresh.failed',
      trace_id: 'trace-memory-1',
      span_id: 'span-memory-1',
      thread_id: 'thread-1',
      status: 'error',
      error: '[redacted]',
      metadata: { component: 'memory', req_id: 17 },
    }));
  });

  it('coalesces repeated backend RPC return entries while preserving occurrence count', () => {
    const resultPreview = JSON.stringify({
      messages: [{
        id: 1,
        content: 'private prompt body',
        path: '/home/l4place/private-project/secret.txt',
        api_key: 'sk-live-secret',
        count: 2,
      }],
      total: 1,
    });
    useClientStore.getState().addLog('debug', 'api.rpc.done', {
      method: 'thread/messages',
      threadId: 'thread-1',
      req_id: 1,
      result: resultPreview,
    });
    useClientStore.getState().addLog('debug', 'api.rpc.done', {
      method: 'thread/messages',
      threadId: 'thread-1',
      req_id: 2,
      result: resultPreview,
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
    const serializedFields = JSON.stringify(results[0].fields);
    expect(serializedFields).not.toContain('private prompt body');
    expect(serializedFields).not.toContain('/home/l4place');
    expect(serializedFields).not.toContain('sk-live-secret');
    expect(serializedFields).not.toContain('secret.txt');
  });

  it('integrates large result from bridge producer to client store without crashing and without leaking sensitive values', async () => {
    const hadTraceDebugFlag = Object.prototype.hasOwnProperty.call(window, '__AO_FRONTEND_TRACE_DEBUG__');
    const previousTraceDebugFlag = window.__AO_FRONTEND_TRACE_DEBUG__;

    try {
      window.__AO_FRONTEND_TRACE_DEBUG__ = true;
      const largeResult = {
        api_key: 'super-secret-password-123',
        values: Array.from({ length: 900 }, (_, index) => index),
      };

      vi.doMock('/wails/runtime.js', () => ({
        Call: {
          ByID: vi.fn().mockResolvedValue({
            ok: true,
            tool: 'mcp__large__tool',
            result: largeResult,
          }),
        },
        Events: { On: vi.fn() },
      }));

      const { callAPI } = await import('../../../shared/api/wailsBridge.js');
      await callAPI('tools/call', { name: 'mcp__large__tool' });

      const entries = useClientStore.getState().runtimeResultEntries;
      const entry = entries.find(e => e.fields?.method === 'tools/call');
      expect(entry).toBeDefined();
      expect(entry.detail).toHaveLength(500);
      expect(entry.detail.endsWith('...')).toBe(true);
      expect(entry.detail).not.toContain('super-secret-password-123');
      expect(JSON.stringify(entry.fields)).not.toContain('super-secret-password-123');
    } finally {
      vi.doUnmock('/wails/runtime.js');
      if (hadTraceDebugFlag) {
        window.__AO_FRONTEND_TRACE_DEBUG__ = previousTraceDebugFlag;
      } else {
        delete window.__AO_FRONTEND_TRACE_DEBUG__;
      }
    }
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
    await useClientStore.getState().forceCompleteActiveThread();
    await useClientStore.getState().compactActiveThread();
    await useClientStore.getState().recoverActiveThread();
    await useClientStore.getState().renameThread('thread-1', 'Renamed');
    await useClientStore.getState().archiveThread('thread-1', true);

    expect(backend.interruptTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-1',
      expectedTurnId: 'turn-1',
      requestId: expect.any(String),
      source: 'ui_stop',
    });
    expect(backend.forceCompleteTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
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

  it('shows a warning when force complete returns a diagnosed no-target envelope', async () => {
    backend.forceCompleteTurn.mockResolvedValueOnce({
      ok: false,
      forceCompleted: false,
      errorCode: 'force_complete_target_not_found',
    });
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      activeTurnByThread: {
        'thread-1': { id: 'turn-1', threadId: 'thread-1', status: 'running' },
      },
    });

    await expect(useClientStore.getState().forceCompleteActiveThread()).resolves.toBe(false);

    expect(backend.forceCompleteTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '强制完成当前执行失败，请重试。',
      tone: 'warning',
    }));
    expect(useClientStore.getState().warningEntries).toContainEqual(expect.objectContaining({
      level: 'warn',
      event: 'thread.force_complete.failed',
      fields: expect.objectContaining({
        error: '[redacted]',
      }),
    }));
  });

  const approvalItem = (requestId, overrides = {}) => ({
    sessionScope: 'session-scope-a',
    callId: `call-${requestId}`,
    requestId,
    command: 'deploy',
    ...overrides,
  });

  it('responds to timeline approval requests through the approval RPC', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '运行线程', provider: 'codex', status: 'waiting' }],
    });

    await expect(useClientStore.getState().respondApproval(approvalItem(11), true)).resolves.toBe(true);

    expect(backend.respondApproval).toHaveBeenCalledWith({
      sessionScope: 'session-scope-a',
      callId: 'call-11',
      requestId: 11,
      approved: true,
    });
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '审批结果已提交',
      tone: 'success',
    }));
    expect(diagnosticBreadcrumbs()).toEqual([
      { actionCode: 'approval.submit', routeId: 'chat', phase: 'start' },
      { actionCode: 'approval.submit', routeId: 'chat', phase: 'success' },
    ]);
  });

  it('rejects malformed approval responses without publishing success', async () => {
    for (const response of [{ ok: false }, { ok: true }, undefined]) {
      backend.respondApproval.mockResolvedValueOnce(response);
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', name: '运行线程', provider: 'codex', status: 'waiting' }],
      });

      await expect(useClientStore.getState().respondApproval(approvalItem(11), true))
        .rejects.toThrow('approval/respond response must be null');

      expect(useClientStore.getState().actionNotice).not.toEqual(expect.objectContaining({
        message: '审批结果已提交',
        tone: 'success',
      }));
      expect(useClientStore.getState().warningEntries).toContainEqual(expect.objectContaining({
        level: 'error',
        event: 'timeline.approval.respond.failed',
      }));
      expect(useClientStore.getState().approvalSubmitByIdentity).toEqual({});
    }
  });

  it('rejects malformed timeline approval request ids before calling the approval RPC', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '运行线程', provider: 'codex', status: 'waiting' }],
    });

    for (const item of [
      approvalItem('11.9'),
      { sessionScope: 'session-scope-a', callId: 'call-11', request_id: '11', command: 'deploy' },
      { sessionScope: 'session-scope-a', requestId: 11, command: 'deploy' },
      { callId: 'call-11', requestId: 11, command: 'deploy' },
    ]) {
      await expect(useClientStore.getState().respondApproval(item, true)).resolves.toBe(false);
    }

    expect(backend.respondApproval).not.toHaveBeenCalled();
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '当前审批缺少完整身份，无法提交',
      tone: 'error',
    }));
    expect(diagnosticBreadcrumbs()).toEqual([]);
  });

  it.each([
    { label: 'false string', approved: 'false' },
    { label: 'true string', approved: 'true' },
    { label: 'number', approved: 1 },
    { label: 'null', approved: null },
    { label: 'undefined', approved: undefined },
    { label: 'object', approved: { value: true } },
  ])('rejects a non-boolean approval decision: $label', async ({ approved }) => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '运行线程', provider: 'codex', status: 'waiting' }],
    });

    await expect(useClientStore.getState().respondApproval(approvalItem(11), approved))
      .resolves.toBe(false);

    expect(backend.respondApproval).not.toHaveBeenCalled();
    expect(diagnosticBreadcrumbs()).toEqual([]);
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '审批提交失败，请重试。',
      tone: 'error',
    }));
    expect(useClientStore.getState().warningEntries).toContainEqual(expect.objectContaining({
      level: 'error',
      event: 'timeline.approval.respond.failed',
      fields: expect.objectContaining({
        requestId: 11,
        error: '[redacted]',
      }),
    }));
    expect(useClientStore.getState().approvalSubmitByIdentity).toEqual({});
  });

  it('keeps approval RPC submission idempotent per exact identity while in flight', async () => {
    const pendingApproval = deferred();
    backend.respondApproval.mockReturnValueOnce(pendingApproval.promise);
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '运行线程', provider: 'codex', status: 'waiting' }],
    });

    const identity = approvalItem(11);
    const first = useClientStore.getState().respondApproval(identity, true);
    await flushPromises();
    await expect(useClientStore.getState().respondApproval(identity, false)).resolves.toBe(false);

    expect(backend.respondApproval).toHaveBeenCalledTimes(1);
    expect(Object.values(useClientStore.getState().approvalSubmitByIdentity)).toEqual([
      expect.objectContaining({
        sessionScope: 'session-scope-a',
        callId: 'call-11',
        requestId: 11,
        approved: true,
        inFlight: true,
      }),
    ]);
    expect(diagnosticBreadcrumbs()).toEqual([
      { actionCode: 'approval.submit', routeId: 'chat', phase: 'start' },
    ]);

    pendingApproval.resolve(null);
    await expect(first).resolves.toBe(true);
    expect(useClientStore.getState().approvalSubmitByIdentity).toEqual({});
    expect(diagnosticBreadcrumbs()).toEqual([
      { actionCode: 'approval.submit', routeId: 'chat', phase: 'start' },
      { actionCode: 'approval.submit', routeId: 'chat', phase: 'success' },
    ]);
  });

  it('dedupes only the exact approval identity while allowing the same request id in another session', async () => {
    const firstPending = deferred();
    const secondPending = deferred();
    backend.respondApproval
      .mockReturnValueOnce(firstPending.promise)
      .mockReturnValueOnce(secondPending.promise);
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '运行线程', provider: 'codex', status: 'waiting' }],
    });

    const firstIdentity = {
      sessionScope: 'session-scope-a',
      callId: 'call-a',
      requestId: 11,
      command: 'deploy',
    };
    const secondIdentity = {
      sessionScope: 'session-scope-b',
      callId: 'call-b',
      requestId: 11,
      command: 'deploy',
    };
    const first = useClientStore.getState().respondApproval(firstIdentity, true);
    await flushPromises();
    await expect(useClientStore.getState().respondApproval(firstIdentity, false)).resolves.toBe(false);
    const second = useClientStore.getState().respondApproval(secondIdentity, false);
    await flushPromises();

    expect(backend.respondApproval).toHaveBeenCalledTimes(2);
    expect(backend.respondApproval).toHaveBeenNthCalledWith(1, {
      sessionScope: 'session-scope-a',
      callId: 'call-a',
      requestId: 11,
      approved: true,
    });
    expect(backend.respondApproval).toHaveBeenNthCalledWith(2, {
      sessionScope: 'session-scope-b',
      callId: 'call-b',
      requestId: 11,
      approved: false,
    });

    firstPending.resolve(null);
    secondPending.resolve(null);
    await expect(first).resolves.toBe(true);
    await expect(second).resolves.toBe(true);
  });

  it('records malformed and ordinary approval failures as one failed terminal without private fields', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '运行线程', provider: 'codex', status: 'waiting' }],
    });
    backend.respondApproval.mockResolvedValueOnce({ ok: false });

    await expect(useClientStore.getState().respondApproval(approvalItem(11), true)).rejects.toThrow('approval/respond response must be null');
    expect(diagnosticBreadcrumbs()).toEqual([
      { actionCode: 'approval.submit', routeId: 'chat', phase: 'start' },
      { actionCode: 'approval.submit', routeId: 'chat', phase: 'failure' },
    ]);

    frontendBreadcrumbs.resetFrontendBreadcrumbsForTests();
    backend.respondApproval.mockRejectedValueOnce(new Error('private failure /Users/alice'));
    await expect(useClientStore.getState().respondApproval(approvalItem(12), false)).rejects.toThrow('private failure');
    expect(diagnosticBreadcrumbs()).toEqual([
      { actionCode: 'approval.submit', routeId: 'chat', phase: 'start' },
      { actionCode: 'approval.submit', routeId: 'chat', phase: 'failure' },
    ]);
  });

  it('times out the owned approval attempt and keeps a retried request isolated from the late transport', async () => {
    vi.useFakeTimers();
    try {
      const firstApproval = deferred();
      const secondApproval = deferred();
      backend.respondApproval
        .mockReturnValueOnce(firstApproval.promise)
        .mockReturnValueOnce(secondApproval.promise);
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', name: '运行线程', provider: 'codex', status: 'waiting' }],
      });

      let firstOutcome;
      const identity = approvalItem(11);
      const first = useClientStore.getState().respondApproval(identity, true);
      const firstHandled = first.then(
        (value) => { firstOutcome = { status: 'fulfilled', value }; },
        (error) => { firstOutcome = { status: 'rejected', error }; },
      );
      await flushPromises();

      await vi.advanceTimersByTimeAsync(15_000);
      await flushPromises();

      expect(firstOutcome).toMatchObject({
        status: 'rejected',
        error: { code: 'APPROVAL_SUBMIT_TIMEOUT', message: '审批提交超时' },
      });
      expect(diagnosticBreadcrumbs()).toEqual([
        { actionCode: 'approval.submit', routeId: 'chat', phase: 'start' },
        { actionCode: 'approval.submit', routeId: 'chat', phase: 'timeout' },
      ]);
      expect(useClientStore.getState().approvalSubmitByIdentity).toEqual({});

      const second = useClientStore.getState().respondApproval(identity, true);
      await flushPromises();
      expect(backend.respondApproval).toHaveBeenCalledTimes(2);
      expect(Object.values(useClientStore.getState().approvalSubmitByIdentity)).toEqual([
        expect.objectContaining({ approved: true, inFlight: true }),
      ]);

      firstApproval.resolve(null);
      await flushPromises();
      expect(Object.values(useClientStore.getState().approvalSubmitByIdentity)).toEqual([
        expect.objectContaining({ approved: true, inFlight: true }),
      ]);

      secondApproval.resolve(null);
      await expect(second).resolves.toBe(true);
      await firstHandled;
      expect(useClientStore.getState().approvalSubmitByIdentity).toEqual({});
      expect(diagnosticBreadcrumbs()).toEqual([
        { actionCode: 'approval.submit', routeId: 'chat', phase: 'start' },
        { actionCode: 'approval.submit', routeId: 'chat', phase: 'timeout' },
        { actionCode: 'approval.submit', routeId: 'chat', phase: 'start' },
        { actionCode: 'approval.submit', routeId: 'chat', phase: 'success' },
      ]);
    }
    finally {
      vi.useRealTimers();
    }
  });

  it('does not call interrupt when the selected running thread has no active turn id', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '运行线程', provider: 'codex', status: 'running' }],
    });

    await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(false);

    expect(backend.interruptTurn).not.toHaveBeenCalled();
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '当前没有可中断任务',
      tone: 'warning',
    }));
  });

  it('does not interrupt a runtime agent when backend status marks it interruptible without an active turn id', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_123',
      threads: [{ id: 'agent_123', name: 'Runtime Agent', provider: 'codex', status: 'running' }],
      statuses: {
        agent_123: { status: 'running', interruptible: true },
      },
    });

    expect(useClientStore.getState().hasInterruptibleThreadAction()).toBe(false);

    await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(false);

    expect(backend.interruptTurn).not.toHaveBeenCalled();
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '当前没有可中断任务',
      tone: 'warning',
    }));
  });

  it('does not treat a stale active turn as interruptible after the thread becomes idle', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_123',
      threads: [{ id: 'agent_123', name: 'Runtime Agent', provider: 'codex', status: 'idle' }],
      activeTurnByThread: {
        agent_123: { id: 'turn-123', threadId: 'agent_123', status: 'running' },
      },
      statuses: {
        agent_123: { status: 'idle', interruptible: false },
      },
    });

    expect(useClientStore.getState().hasInterruptibleThreadAction()).toBe(false);

    await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(false);

    expect(backend.interruptTurn).not.toHaveBeenCalled();
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '当前没有可中断任务',
      tone: 'warning',
    }));
  });

  it.each(['completed', 'failed', 'interrupted', 'stalled', 'done', 'ended', 'closed'])(
    'does not treat a terminal active turn status as interruptible: %s',
    async (status) => {
      resetClientStoreForTests({
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'agent_123',
        threads: [{ id: 'agent_123', name: 'Runtime Agent', provider: 'codex', status: 'idle' }],
        activeTurnByThread: {
          agent_123: { id: 'turn-123', threadId: 'agent_123', status },
        },
        statuses: {
          agent_123: { status: 'idle', interruptible: false },
        },
      });

      expect(useClientStore.getState().hasInterruptibleThreadAction()).toBe(false);

      await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(false);

      expect(backend.interruptTurn).not.toHaveBeenCalled();
    },
  );

  it('clears active turn state when a bridge patch reports a completed active turn', () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_123',
      threads: [{ id: 'agent_123', name: 'Runtime Agent', provider: 'codex', status: 'running' }],
      activeTurnByThread: {
        agent_123: { id: 'turn-123', threadId: 'agent_123', status: 'running' },
      },
    });
    registerBridgeEventHandlersForTest();

    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'agent_123',
        status: 'idle',
        activeTurn: { id: 'turn-123', threadId: 'agent_123', status: 'completed' },
        thread: { id: 'agent_123', name: 'Runtime Agent', status: 'idle' },
      },
    });

    expect(useClientStore.getState().activeTurnByThread.agent_123).toBeUndefined();
    expect(useClientStore.getState().hasInterruptibleThreadAction()).toBe(false);
  });

  it('interrupts a runtime agent when an active turn id is present', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_123',
      threads: [{ id: 'agent_123', name: 'Runtime Agent', provider: 'codex', status: 'running' }],
      activeTurnByThread: {
        agent_123: { id: 'turn-123', threadId: 'agent_123', status: 'running' },
      },
      statuses: {
        agent_123: { status: 'running', interruptible: true },
      },
    });

    expect(useClientStore.getState().hasInterruptibleThreadAction()).toBe(true);

    await expect(useClientStore.getState().interruptActiveThread()).resolves.toBe(true);

    expect(backend.interruptTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'agent_123',
      expectedTurnId: 'turn-123',
      requestId: expect.any(String),
      source: 'ui_stop',
    });
  });

  it('surfaces recover RPC failures to the unified action boundary', async () => {
    backend.recoverThread.mockRejectedValueOnce(new Error('orchestration: service not configured'));
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '运行线程', provider: 'codex', status: 'running' }],
    });

    await expect(useClientStore.getState().recoverActiveThread()).rejects.toThrow('orchestration: service not configured');

    expect(backend.recoverThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '恢复连接失败，请重试。',
      tone: 'error',
    }));
    expect(useClientStore.getState().warningEntries.at(-1)).toEqual(expect.objectContaining({
      event: 'thread.recover.failed',
      level: 'error',
    }));
  });

  it('submits one recover RPC while the same thread request is pending', async () => {
    const recovery = deferred();
    backend.recoverThread.mockReturnValueOnce(recovery.promise);
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '运行线程', provider: 'codex', status: 'running' }],
    });

    const first = useClientStore.getState().recoverActiveThread();
    const repeated = useClientStore.getState().recoverActiveThread();

    await expect(repeated).resolves.toBe(false);
    expect(backend.recoverThread).toHaveBeenCalledTimes(1);
    expect(useClientStore.getState().threadRecoveryPendingByThread).toEqual({ 'thread-1': true });

    recovery.resolve({
      thread: { id: 'thread-1', status: 'recovering' },
      recovered: true,
      mode: 'relaunch_resume',
    });
    await expect(first).resolves.toBe(true);

    expect(useClientStore.getState().threadRecoveryPendingByThread).toEqual({});
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '恢复请求已接受，正在恢复',
      tone: 'success',
    }));
    expect(useClientStore.getState().actionNotice.message).not.toContain('已恢复完成');
  });

  it('treats recovered false as a failed request and never as accepted', async () => {
    backend.recoverThread.mockResolvedValueOnce({
      thread: { id: 'thread-1', status: 'recovering' },
      recovered: false,
      mode: 'relaunch_resume',
    });
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '运行线程', provider: 'codex', status: 'running' }],
    });

    await expect(useClientStore.getState().recoverActiveThread()).resolves.toBe(false);

    expect(useClientStore.getState().threadRecoveryPendingByThread).toEqual({});
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '恢复请求失败',
      tone: 'warning',
    }));
    expect(useClientStore.getState().actionNotice.message).not.toContain('已接受');
    expect(useClientStore.getState().warningEntries.at(-1)).toEqual(expect.objectContaining({
      event: 'thread.recover.failed',
      level: 'warn',
    }));
  });

  it('clears stale recover pending without polluting the newly active thread', async () => {
    const recovery = deferred();
    backend.recoverThread.mockReturnValueOnce(recovery.promise);
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: '旧线程', provider: 'codex', status: 'running' },
        { id: 'thread-2', name: '新线程', provider: 'codex', status: 'idle' },
      ],
    });

    const pending = useClientStore.getState().recoverActiveThread();
    expect(useClientStore.getState().threadRecoveryPendingByThread).toEqual({ 'thread-1': true });
    useClientStore.setState({ activeThreadId: 'thread-2', actionNotice: null });

    recovery.resolve({
      thread: { id: 'thread-1', status: 'recovering' },
      recovered: true,
      mode: 'relaunch_resume',
    });
    await expect(pending).resolves.toBe(true);

    expect(useClientStore.getState().activeThreadId).toBe('thread-2');
    expect(useClientStore.getState().threadRecoveryPendingByThread).toEqual({});
    expect(useClientStore.getState().actionNotice).toBeNull();
    expect(useClientStore.getState().warningEntries.filter((entry) => entry.event === 'thread.recover.failed')).toEqual([]);
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

  it('surfaces archive RPC failures without mutating local archive state', async () => {
    backend.archiveThread.mockRejectedValueOnce(new Error('orchestration: service not configured'));
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'idle', archived: false }],
    });

    await expect(useClientStore.getState().archiveThread('thread-1', true)).rejects.toThrow('orchestration: service not configured');

    expect(backend.archiveThread).toHaveBeenCalledWith({ threadId: 'thread-1' });
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'archivedThreadAtById.thread-1',
    }));
    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({ archived: false, status: 'idle' }));
    expect(useClientStore.getState().threadArchiveLoadingByThread['thread-1']).toBe(false);
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '归档会话失败，请重试。',
      tone: 'error',
    }));
    expect(useClientStore.getState().warningEntries.at(-1)).toEqual(expect.objectContaining({
      event: 'thread.archive.failed',
      level: 'error',
    }));
  });

  it('surfaces archive preference failures after backend archive succeeds', async () => {
    backend.setPreference.mockRejectedValueOnce(new Error('preference backend offline'));
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'idle', archived: false }],
    });

    await expect(useClientStore.getState().archiveThread('thread-1', true)).rejects.toThrow('preference backend offline');

    expect(backend.archiveThread).toHaveBeenCalledWith({ threadId: 'thread-1' });
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'archivedThreadAtById.thread-1',
      value: expect.any(Number),
    });
    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({
      archived: true,
      status: 'archived',
    }));
    expect(useClientStore.getState().activeThreadId).toBe('');
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '归档偏好保存失败，请重试。',
      tone: 'error',
    }));
    expect(useClientStore.getState().warningEntries.at(-1)).toEqual(expect.objectContaining({
      event: 'thread.archive.preference.failed',
      level: 'error',
    }));
  });

  it('surfaces rename RPC failures without closing over a rejected action', async () => {
    backend.renameThread.mockRejectedValueOnce(new Error('name backend offline'));
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '旧名称', provider: 'codex', status: 'idle' }],
    });

    await expect(useClientStore.getState().renameThread('thread-1', '新名称')).rejects.toThrow('name backend offline');

    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({ name: '旧名称' }));
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '重命名会话失败，请重试。',
      tone: 'error',
    }));
    expect(useClientStore.getState().warningEntries.at(-1)).toEqual(expect.objectContaining({
      event: 'thread.rename.failed',
      level: 'error',
    }));
  });

  it('surfaces pin preference failures without mutating local pin state', async () => {
    backend.setPreference.mockRejectedValueOnce(new Error('preference backend offline'));
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'idle', pinned: false, pinnedAt: 0 }],
    });

    await expect(useClientStore.getState().toggleThreadPin('thread-1')).rejects.toThrow('preference backend offline');

    expect(useClientStore.getState().threads[0]).toEqual(expect.objectContaining({ pinned: false, pinnedAt: 0 }));
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '置顶会话失败，请重试。',
      tone: 'error',
    }));
    expect(useClientStore.getState().warningEntries.at(-1)).toEqual(expect.objectContaining({
      event: 'thread.pin.failed',
      level: 'error',
    }));
  });

  it('deletes stale archived threads through backend and clears archive preferences', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-stale',
      threads: [
        { id: 'thread-stale', name: '旧归档线程', provider: 'codex', status: 'archived', archived: true, archivedAt: systemClockMillis() - 8 * 24 * 60 * 60 * 1000 },
        { id: 'thread-fresh', name: '近期归档线程', provider: 'codex', status: 'archived', archived: true, archivedAt: systemClockMillis() },
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

  it('commits successful thread deletions but rejects a partial failure for the action boundary', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-ok',
      threads: [
        { id: 'thread-ok', name: '可删除', provider: 'codex', status: 'archived', archived: true },
        { id: 'thread-failed', name: '删除失败', provider: 'codex', status: 'archived', archived: true },
      ],
    });
    const rawFailure = new Error('raw delete provider failure');
    backend.deleteThread
      .mockResolvedValueOnce({ ok: true })
      .mockRejectedValueOnce(rawFailure);

    await expect(useClientStore.getState().deleteStaleThreads(['thread-ok', 'thread-failed']))
      .rejects.toThrow('1 thread delete action(s) failed');

    expect(useClientStore.getState().threads.map((thread) => thread.id)).toEqual(['thread-failed']);
    expect(useClientStore.getState().actionNotice).toEqual(expect.objectContaining({
      message: '已删除 1 个无用会话，1 个失败',
      tone: 'warning',
    }));
    expect(JSON.stringify(useClientStore.getState().actionNotice)).not.toContain('raw delete');
  });

  it('preserves the reference of equivalent timeline items during bridge patch merges', async () => {
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'idle' }],
      timelinesByThread: {
        'thread-1': [{
          id: 'msg-1',
          role: 'assistant',
          kind: 'assistant',
          text: 'hello world',
          done: true,
          time: '2026-06-05T00:00:00Z',
        }],
      },
    });
    registerBridgeEventHandlersForTest();

    const existingMessage = useClientStore.getState().timelinesByThread['thread-1'][0];
    // 模拟推送一个具有相同 ID 并且内容完全一致的 replacement timelineItem，但引用不同
    const patchItem = { ...existingMessage };
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        timelineItems: [patchItem],
      },
    });

    const timeline = useClientStore.getState().timelinesByThread['thread-1'];
    expect(timeline).toHaveLength(1);
    // 判定其引用必须保持为原来的 existingMessage，而不是被 patchItem 替换
    expect(timeline[0]).toBe(existingMessage);

    // 另外测试如果内容不一致（比如 done 变为了 false），则必须被 replacement 覆盖，且引用改变
    const changedPatchItem = { ...existingMessage, done: false };
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        timelineItems: [changedPatchItem],
      },
    });
    const updatedTimeline = useClientStore.getState().timelinesByThread['thread-1'];
    expect(updatedTimeline[0]).not.toBe(existingMessage);
    expect(updatedTimeline[0].done).toBe(false);
  });

  it('keeps the backend archive result but rejects when its preference write fails', async () => {
    backend.setPreference.mockRejectedValueOnce(new Error('preference write error'));
    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'idle', archived: false }],
    });

    await expect(useClientStore.getState().archiveThread('thread-1', true)).rejects.toThrow('preference write error');
    expect(useClientStore.getState().threads[0].archived).toBe(true);
  });

  it('preserves the optimistic archive state when a snapshot or patch is applied while loading or recently mutated', async () => {
    backend.getThreadState
      .mockResolvedValueOnce({
        threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'idle', archived: false }],
      })
      .mockResolvedValueOnce({
        threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'idle', archived: false }],
      });
    backend.getThreadMessages
      .mockResolvedValueOnce({ messages: [] })
      .mockResolvedValueOnce({ messages: [] });

    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'idle', archived: false }],
    });
    registerBridgeEventHandlersForTest();

    // Start archiving (simulates in-flight archive)
    const archivePromise = useClientStore.getState().archiveThread('thread-1', true);
    expect(useClientStore.getState().threads[0].archived).toBe(true);
    expect(useClientStore.getState().threadArchiveLoadingByThread['thread-1']).toBe(true);

    // 1. Simulate a bridge patch containing stale state
    bridgeCallback({
      type: 'ui/thread/patch',
      payload: {
        threadId: 'thread-1',
        patchedThread: {
          id: 'thread-1',
          archived: false,
        },
      },
    });
    expect(useClientStore.getState().threads[0].archived).toBe(true);

    // 2. Simulate a syncThreadState database reload containing stale state
    await useClientStore.getState().syncThreadState('thread-1');
    expect(useClientStore.getState().threads[0].archived).toBe(true);

    // Resolve the archive RPC
    await archivePromise;
    expect(useClientStore.getState().threads[0].archived).toBe(true);
    expect(useClientStore.getState().threadArchiveLoadingByThread['thread-1']).toBe(false);

    // 3. Simulate another syncThreadState database reload containing stale state within the 8s window
    await useClientStore.getState().syncThreadState('thread-1');
    expect(useClientStore.getState().threads[0].archived).toBe(true);
  });

  it('matches optimistic archive overrides by both agent runtime ID and database UUID', async () => {
    backend.getThreadState.mockResolvedValueOnce({
      threads: [{ id: '019e98df-2cd9-76b0-ad5b-9f1f252fa764', agent_id: 'agent_123', name: 'Draft', provider: 'codex', status: 'idle', archived: false }],
    });
    backend.getThreadMessages.mockResolvedValueOnce({ messages: [] });

    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_123',
      threads: [{ id: 'agent_123', agentId: 'agent_123', name: 'Draft', provider: 'codex', status: 'idle', archived: false }],
    });

    // Start archiving. Because store.threads has id = 'agent_123', the id in archiveThread resolves to 'agent_123'
    const archivePromise = useClientStore.getState().archiveThread('agent_123', true);
    expect(useClientStore.getState().threads[0].archived).toBe(true);
    expect(useClientStore.getState().threadArchiveLoadingByThread['agent_123']).toBe(true);

    // Now, run syncThreadState. The server responds with database UUID '019e98df-2cd9-76b0-ad5b-9f1f252fa764'
    // B's override (saved under agent_123) should be matched via identity.agentId and preserve its archived status!
    await useClientStore.getState().syncThreadState('agent_123');
    expect(useClientStore.getState().threads[0].archived).toBe(true);

    await archivePromise;
  });

  it('preserves other threads optimistic archive states when a concurrent archive action fails and rolls back', async () => {
    // A fails, B succeeds.
    backend.archiveThread
      .mockRejectedValueOnce(new Error('Archiving A failed')) // A fails
      .mockResolvedValueOnce({ ok: true }); // B succeeds

    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-A',
      threads: [
        { id: 'thread-A', name: 'Thread A', provider: 'codex', status: 'idle', archived: false },
        { id: 'thread-B', name: 'Thread B', provider: 'codex', status: 'idle', archived: false },
      ],
    });

    const promiseA = useClientStore.getState().archiveThread('thread-A', true);
    const promiseB = useClientStore.getState().archiveThread('thread-B', true);

    expect(useClientStore.getState().threads.find(t => t.id === 'thread-A').archived).toBe(true);
    expect(useClientStore.getState().threads.find(t => t.id === 'thread-B').archived).toBe(true);

    // Resolve A (which fails)
    await expect(promiseA).rejects.toThrow('Archiving A failed');

    // A should be rolled back to active (archived = false)
    expect(useClientStore.getState().threads.find(t => t.id === 'thread-A').archived).toBe(false);
    // B's optimistic archive state should NOT be affected (remains true)!
    expect(useClientStore.getState().threads.find(t => t.id === 'thread-B').archived).toBe(true);

    // Resolve B (succeeds)
    await promiseB;
    expect(useClientStore.getState().threads.find(t => t.id === 'thread-B').archived).toBe(true);
  });

  it('resolves archivedAt and pinnedAt states using both agent runtime ID and database UUID from configuration maps', async () => {
    backend.getThreadState.mockReset();
    backend.getThreadMessages.mockReset();
    backend.getThreadMessages.mockResolvedValue({ messages: [] });

    // Case A: Map has agent_123, Thread has DB UUID
    backend.getThreadState.mockResolvedValueOnce({
      threads: [{ id: '019e98df-2cd9-76b0-ad5b-9f1f252fa764', agent_id: 'agent_123', name: 'Draft', provider: 'codex', status: 'idle' }],
      archivedThreadAtById: { 'agent_123': 1500000000000 },
      pinnedThreadAtById: { 'agent_123': 1600000000000 },
    });
    backend.getThreadMessages.mockResolvedValueOnce({ messages: [] });

    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_123',
      threads: [{ id: 'agent_123', agentId: 'agent_123', name: 'Draft', provider: 'codex', status: 'idle', archived: false }],
    });

    await useClientStore.getState().syncThreadState('agent_123');
    let syncedThread = useClientStore.getState().threads[0];
    expect(syncedThread.archived).toBe(true);
    expect(syncedThread.archivedAt).toBe(1500000000000);
    expect(syncedThread.pinned).toBe(true);
    expect(syncedThread.pinnedAt).toBe(1600000000000);

    // Case B: Map has DB UUID, Thread has agent_123
    backend.getThreadState.mockResolvedValueOnce({
      threads: [{ id: 'agent_123', agent_id: 'agent_123', name: 'Draft', provider: 'codex', status: 'idle' }],
      archivedThreadAtById: { '019e98df-2cd9-76b0-ad5b-9f1f252fa764': 1500000000000 },
      pinnedThreadAtById: { '019e98df-2cd9-76b0-ad5b-9f1f252fa764': 1600000000000 },
    });
    backend.getThreadMessages.mockResolvedValueOnce({ messages: [] });

    resetClientStoreForTests({
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: '019e98df-2cd9-76b0-ad5b-9f1f252fa764',
      threads: [{ id: '019e98df-2cd9-76b0-ad5b-9f1f252fa764', agentId: 'agent_123', name: 'Draft', provider: 'codex', status: 'idle', archived: false }],
    });

    await useClientStore.getState().syncThreadState('019e98df-2cd9-76b0-ad5b-9f1f252fa764');
    syncedThread = useClientStore.getState().threads[0];
    expect(syncedThread.archived).toBe(true);
    expect(syncedThread.archivedAt).toBe(1500000000000);
    expect(syncedThread.pinned).toBe(true);
    expect(syncedThread.pinnedAt).toBe(1600000000000);
  });
