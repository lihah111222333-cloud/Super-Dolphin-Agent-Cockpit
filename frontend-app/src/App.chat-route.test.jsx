import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import App from './App.jsx';
import { resetClientStoreForTests, useClientStore } from './entities/client/model/useClientStore.js';
import './test-utils/preloadAppRouteModules.js';
import { createAppTestSupport } from './test/appTestSupport.test-helper.jsx';

let bridgeCallback;
let appOverlayHost;

window.matchMedia = vi.fn(() => ({
  matches: false,
  addEventListener: vi.fn(),
  removeEventListener: vi.fn(),
}));

const backend = vi.hoisted(() => {
  const mockNames = `
	    readConfig getWindowBootstrap openNewWindow getProjects setActiveProject addProject removeProject
	    callBackend checkAppUpdate installLatestAppUpdate
    getSidebarState getThreadState getThreadMessages getBuildInfo getVideoApiKey getDashboardPage getObservabilityStatus
    getObservabilityTrace getObservabilityThreadRecent listObservabilityRecent listObservabilitySlow
    listObservabilityErrors listSharedFiles listPromptAssets getDashboardPrompts getPrompt writePrompt
    readLspPromptHint writeLspPromptHint readBuiltinTools writeBuiltinTool listDashboardLogs
    getPersonalizationProfile savePersonalizationProfile listPromptSections writePromptSection deletePromptSection
    deletePrompt draftPromptIntent commitPromptIntent discardPromptIntent dryRunPromptIntent getMemorySnapshot
    getMemoryEntry upsertMemoryEntry deleteMemoryEntry setMemoryAutoDreamIntent mergeMemoryEntries
    ignoreMemorySimilarity consolidateMemorySimilarities startConsolidateMemorySimilarities getMemoryConsolidationStatus
    listDags getDagDetail getDagRuns getDagRun startDag terminateDagRun deleteDag applyDagOps listWorkflowTemplates getWorkflowTemplate renderWorkflowTemplateDraft deleteSkill
    listCronJobs getCronJob createCronJob updateCronJob deleteCronJob runCronJobOnce setCronJobEnabled listCronJobRuns
    readSkill listSkillFiles createSkill writeSkill importSkillDirectories suggestSkillSummary selectProjectDir selectProjectDirs
    createSkillTool listSkillTools getSkillTool updateSkillTool deleteSkillTool
    listMCPServers listToolbridgeTools startSQLiteMCPServer stopSQLiteMCPServer startPlaywrightMCPServer stopPlaywrightMCPServer
    listSkillResolutions previewSkillResolution applySkillResolution readSharedFile deleteSharedFile getPreference
    forkThread startThread startTurn interruptTurn forceCompleteTurn compactThread recoverThread respondApproval resolveThreadIdentity archiveThread unarchiveThread
    deleteThread getThreadConfig setThreadConfig renameThread setPreference setVideoApiKey selectFiles saveClipboardImage saveTextFile
    locateCodeFile openCodeFile openPath saveCodeFile beginTextClipboardWrite copyTextToClipboard emitFrontendTraceEvent
  `.trim().split(/\s+/);
  return {
    ...Object.fromEntries(mockNames.map((name) => [name, vi.fn()])),
    onFilesDropped: vi.fn(() => () => {}),
    onRuntimeReconnect: vi.fn(() => () => {}),
    onBridgeEvent: vi.fn((callback) => {
      bridgeCallback = callback;
      return () => {
        if (bridgeCallback === callback) bridgeCallback = null;
      };
    }),
  };
});

vi.mock('./shared/api/backendApi.js', () => ({
  ...backend,
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
}));

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn((_id, source) => Promise.resolve({
      svg: `<svg role="img" aria-label="mock mermaid"><text>${source}</text></svg>`,
    })),
  },
}));

const appTestSupport = createAppTestSupport({
  App,
  backend,
  resetClientStoreForTests,
  state: {
    get bridgeCallback() { return bridgeCallback; },
    set bridgeCallback(value) { bridgeCallback = value; },
    get appOverlayHost() { return appOverlayHost; },
    set appOverlayHost(value) { appOverlayHost = value; },
  },
});
const {
  waitForBackendThreadHeading,
  getBackendThreadText,
  getThreadCardByName,
  queryThreadCardByName,
  findThreadCardByName,
  installAppOverlayHost,
  resetConnectedShellTestState,
  mockBootstrapBackendDefaults,
  mockDashboardPageDefaults,
  mockObservabilityDefaults,
  mockPromptDefaults,
  mockMemoryDefaults,
  mockWorkflowDefaults,
  mockCronDefaults,
  mockSkillDefaults,
  mockSharedFileDefaults,
  mockSettingsAndThreadDefaults,
  resetFrontendHealthForTest,
  cleanupAppTest
} = appTestSupport;

beforeEach(installAppOverlayHost);
beforeEach(resetConnectedShellTestState);
beforeEach(mockBootstrapBackendDefaults);
beforeEach(mockDashboardPageDefaults);
beforeEach(mockObservabilityDefaults);
beforeEach(mockPromptDefaults);
beforeEach(mockMemoryDefaults);
beforeEach(mockWorkflowDefaults);
beforeEach(mockCronDefaults);
beforeEach(mockSkillDefaults);
beforeEach(mockSharedFileDefaults);
beforeEach(mockSettingsAndThreadDefaults);
beforeEach(resetFrontendHealthForTest);
afterEach(cleanupAppTest);

  it('shows visible feedback for chat toolbar actions', async () => {
    backend.resolveThreadIdentity.mockResolvedValue({
      id: 'thread-1',
      providerThreadId: 'provider-thread-1',
      sessionId: 'session-uuid-1',
      agent_id: 'agent-1',
      provider: 'codex',
      port: 4512,
      cwd: '/repo/app',
      logPath: '/repo/app/.multi-agent/log/app/agent.log',
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByLabelText('复制当前线程'));

    await waitFor(() => {
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('线程信息已复制');
      const payload = JSON.parse(backend.copyTextToClipboard.mock.calls[0][0]);
      expect(payload).toEqual(expect.objectContaining({
        agentId: 'agent-1',
        providerThreadId: 'provider-thread-1',
        uuid: 'session-uuid-1',
        name: '后端线程',
        status: '工作中',
        provider: 'codex',
        model: 'gpt-5.4',
        effort: 'medium',
        port: 4512,
        cwd: '/repo/app',
        'log-path': '/repo/app/.multi-agent/log/app/agent.log',
      }));
      expect(payload.copiedAt).toContain('UTC+8');
    });
  });

  it('shows visible feedback when copying thread info is blocked', async () => {
    backend.resolveThreadIdentity.mockResolvedValue({ id: 'thread-1', providerThreadId: 'provider-thread-1', agent_id: 'agent-1' });
    backend.copyTextToClipboard.mockRejectedValue(new Error('clipboard copy failed: native ui/copyText returned ok=false: clipboard not available in headless mode'));

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByLabelText('复制当前线程'));

    await waitFor(() => {
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('复制失败，请重试。');
      expect(screen.getByTestId('chat-action-feedback')).not.toHaveTextContent('headless mode');
      expect(JSON.parse(backend.copyTextToClipboard.mock.calls[0][0])).toEqual(expect.objectContaining({
        agentId: 'agent-1',
        providerThreadId: 'provider-thread-1',
      }));
    });
  });

  it('hides the provider toggle after an opened chat already has an assistant reply', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    expect(screen.queryByLabelText('线程状态')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('压缩当前线程')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('选择附件')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('权限')).not.toBeInTheDocument();
    expect(screen.getByLabelText('添加文件')).toBeInTheDocument();
    expect(screen.queryByLabelText('发送权限')).not.toBeInTheDocument();

    expect(screen.queryByLabelText('切换 Claude / Codex provider')).not.toBeInTheDocument();
    expect(screen.queryByText('Codex')).not.toBeInTheDocument();
  });

  it('keeps Codex model selection available before a backend chat exists', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });

    render(<App />);
    await screen.findByText('我们应该在 Super Dolphin Agent 中构建什么？');

    expect(screen.queryByLabelText('切换 Claude / Codex provider')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '选择模型' })).toBeEnabled();
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({ key: 'settings.provider.active' }));
  });

  it('uses the opened thread provider model selector without showing the global provider toggle', async () => {
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
    }[key] ?? null));
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-failed',
      threads: [{ id: 'thread-failed', name: 'Broken Codex', provider: 'codex', status: 'failed' }],
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-failed',
      timelinesByThread: { 'thread-failed': [] },
    });
    backend.getThreadConfig.mockResolvedValue({
      threadId: 'thread-failed',
      provider: 'codex',
      supportsThreadOverride: true,
      availableModels: ['gpt-5.4'],
      override: {},
      effective: { model: 'gpt-5.4', effort: 'medium' },
    });

    render(<App />);

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '选择模型' })).toHaveTextContent('5.4 中');
    });
    expect(screen.queryByLabelText('切换 Claude / Codex provider')).not.toBeInTheDocument();
  });

  it('renders hydrated provider metadata for provider-less thread cards', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-unknown',
      threads: [{ id: 'thread-unknown', name: 'Provider missing', status: 'error', provider: 'codex' }],
      timelinesByThread: { 'thread-unknown': [] },
      threadTimelineReadyByThread: { 'thread-unknown': true },
    });

    render(<App skipBootstrap />);

    const card = await findThreadCardByName('Provider missing');
    expect(card).toHaveTextContent('codex');
    expect(screen.queryByText('unknown')).not.toBeInTheDocument();
  });

  it('keeps project switching controls out of the Super Dolphin Agent top app bar while loading the active thread', async () => {
    render(<App />);

    expect(await waitForBackendThreadHeading()).toBeInTheDocument();
    const topAppBar = within(screen.getByLabelText('Super Dolphin Agent app bar'));
    expect(topAppBar.queryByRole('button', { name: '选择项目' })).not.toBeInTheDocument();
    expect(topAppBar.queryByText('Overview')).not.toBeInTheDocument();
    expect(topAppBar.queryByText('Usage')).not.toBeInTheDocument();
    expect(topAppBar.queryByText('Limits')).not.toBeInTheDocument();
    expect(topAppBar.queryByRole('button', { name: 'Upgrade Plan' })).not.toBeInTheDocument();
    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-1',
      includeDiff: true,
    });
    expect(backend.setActiveProject).not.toHaveBeenCalled();
  });

  it('turns the composer model chip into a thread model selector', async () => {
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.4',
      'settings.provider.codex.effort': 'medium',
      'settings.provider.codex.codexHome': '~/.codex',
      'settings.provider.codex.codexInstanceKey': 'default',
      'settings.provider.codex.codexModelProvider': 'openai',
    }[key] ?? null));

    render(<App />);
    await waitForBackendThreadHeading();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '选择模型' })).toHaveTextContent('5.4 中');
    });

    const modelButton = screen.getByRole('button', { name: '选择模型' });
    fireEvent.click(modelButton);
    expect(screen.getByRole('dialog', { name: '模型配置' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '默认（当前：GPT-5.4）' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '默认（当前：中）' })).toBeInTheDocument();
    expect(screen.queryByText('渠道')).not.toBeInTheDocument();
    expect(screen.queryByRole('group', { name: '模型渠道' })).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('模型'), { target: { value: 'gpt-5.5' } });

    await waitFor(() => {
      expect(backend.setThreadConfig).toHaveBeenCalledWith({
        threadId: 'thread-1',
        model: 'gpt-5.5',
        effort: '',
      });
      expect(modelButton).toHaveTextContent('5.5 中');
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('线程配置已保存');
    });
  });

  it('shows delete and running indicators on each visible thread card without active archive', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    expect(screen.getAllByLabelText('会话运行中').length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: '删除会话' })).toBeInTheDocument();
    expect(screen.queryByLabelText('归档会话')).not.toBeInTheDocument();
    expect(getBackendThreadText()).toBeInTheDocument();
  });

  it('shows the pin action tooltip when hovering the thread pin icon', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    const pinButton = screen.getByLabelText('置顶对话');
    expect(pinButton).not.toHaveAttribute('title');
    fireEvent.mouseEnter(pinButton);

    expect(screen.getByTestId('thread-pin-tooltip')).toHaveTextContent('置顶对话');

    fireEvent.mouseLeave(pinButton);

    expect(screen.queryByTestId('thread-pin-tooltip')).not.toBeInTheDocument();
  });

  it('renames a thread inline through the legacy backend name RPC', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.doubleClick(within(getThreadCardByName('后端线程')).getByRole('button', { name: /后端线程/ }));
    const input = screen.getByLabelText('会话别名');
    fireEvent.change(input, { target: { value: '重命名会话' } });
    fireEvent.click(screen.getByRole('button', { name: '保存别名' }));

    await waitFor(() => {
      expect(backend.renameThread).toHaveBeenCalledWith({ threadId: 'thread-1', name: '重命名会话' });
      expect(getThreadCardByName('重命名会话')).toBeInTheDocument();
    });
  });

  it('persists thread pins through the backend threadPins chat preference', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByLabelText('置顶对话'));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'threadPins.chat',
        value: { 'thread-1': expect.any(Number) },
      });
      expect(screen.getByLabelText('取消置顶对话')).toBeInTheDocument();
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('会话已置顶');
    });
  });

  it('moves a sent ordinary chat below pinned chats but above other ordinary chats', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-old',
      threads: [
        { id: 'thread-pin', name: 'Pinned chat', provider: 'codex', status: 'idle' },
        { id: 'thread-new', name: 'Newer chat', provider: 'codex', status: 'idle' },
        { id: 'thread-old', name: 'Older chat', provider: 'codex', status: 'idle', cwd: '/repo/app' },
      ],
      'threadPins.chat': { 'thread-pin': 1735689600000 },
    });
    backend.getThreadState.mockResolvedValue({ activeThreadId: 'thread-old', timelinesByThread: {} });
    backend.startTurn.mockResolvedValue({ ok: true });
    const { container } = render(<App />);
    await findThreadCardByName('Older chat');

    fireEvent.change(screen.getByTestId('composer-input'), { target: { value: 'bring old chat forward' } });
    fireEvent.click(screen.getByLabelText('发送消息'));

    await waitFor(() => expect(backend.startTurn).toHaveBeenCalledWith(expect.objectContaining({ threadId: 'thread-old' })));
    expect([...container.querySelectorAll('.thread-card .thread-name')].map((node) => node.textContent)).toEqual([
      'Pinned chat',
      'Older chat',
      'Newer chat',
    ]);
  });

  it('only floats an ordinary chat on reply completion, not unrelated runtime patches', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-old',
      threads: [
        { id: 'thread-pin', name: 'Pinned chat', provider: 'codex', status: 'idle' },
        { id: 'thread-old', name: 'Older chat', provider: 'codex', status: 'idle' },
        { id: 'thread-new', name: 'Newer chat', provider: 'codex', status: 'idle' },
      ],
      'threadPins.chat': { 'thread-pin': 1735689600000 },
    });
    backend.getThreadState.mockResolvedValue({ activeThreadId: 'thread-old', timelinesByThread: {} });
    const { container } = render(<App />);
    await waitFor(() => expect(getThreadCardByName('Newer chat')).toBeInTheDocument());

    act(() => {
      bridgeCallback?.({
        type: 'ui/thread/patch',
        payload: {
          threadId: 'thread-new',
          source: 'tool/diffUpdated',
          status: 'running',
          thread: { id: 'thread-new', name: 'Newer chat', status: 'running' },
        },
      });
    });
    expect([...container.querySelectorAll('.thread-card .thread-name')].map((node) => node.textContent)).toEqual([
      'Pinned chat',
      'Older chat',
      'Newer chat',
    ]);

    act(() => {
      bridgeCallback?.({
        type: 'ui/thread/patch',
        payload: {
          threadId: 'thread-new',
          source: 'turn/completed',
          status: 'idle',
          thread: { id: 'thread-new', name: 'Newer chat', status: 'idle' },
        },
      });
    });
    expect([...container.querySelectorAll('.thread-card .thread-name')].map((node) => node.textContent)).toEqual([
      'Pinned chat',
      'Newer chat',
      'Older chat',
    ]);
  });

  it('matches the legacy thread rail archive-list toggle', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '归档线程', provider: 'codex', status: 'archived' },
      ],
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {},
    });

    render(<App />);
    await findThreadCardByName('活跃线程');

    expect(screen.getByLabelText('会话列表')).toBeInTheDocument();
    expect(screen.getByLabelText('1 个 Agent')).toBeInTheDocument();
    expect(screen.queryByText('归档线程')).not.toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('打开归档列表'));

    expect(await screen.findByText('归档线程')).toBeInTheDocument();
    expect(screen.getByLabelText('归档列表')).toBeInTheDocument();
    expect(screen.getByLabelText('返回会话列表')).toBeInTheDocument();
    expect(queryThreadCardByName('活跃线程')).not.toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('恢复会话'));

    await waitFor(() => {
      expect(backend.unarchiveThread).toHaveBeenCalledWith({ threadId: 'thread-archived' });
      expect(backend.setPreference).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        key: 'archivedThreadAtById.thread-archived',
        value: null,
      }));
      expect(screen.getByText('暂无归档会话')).toBeInTheDocument();
    });
  });

  it('opens archived thread content from the archive list without showing the new-chat draft', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '归档线程', provider: 'codex', status: 'archived' },
      ],
    });
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '归档线程', provider: 'codex', status: 'idle' },
      ],
      timelinesByThread: {
        [threadId]: [{
          id: `${threadId}-assistant`,
          kind: 'assistant',
          text: threadId === 'thread-archived' ? '归档线程历史内容' : '活跃线程内容',
        }],
      },
    }));

    render(<App />);
    await screen.findByText('活跃线程内容');

    fireEvent.click(screen.getByLabelText('打开归档列表'));
    fireEvent.click(await screen.findByRole('button', { name: /归档线程/ }));

    await waitFor(() => expect(useClientStore.getState().activeThreadId).toBe('thread-archived'));
    expect(await screen.findByText('归档线程历史内容')).toBeInTheDocument();
    expect(screen.queryByText(/让我们从/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText('复制当前线程')).not.toBeInTheDocument();
  });

  it('keeps an empty archived thread selection out of the new-chat intro state', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '空归档线程', provider: 'codex', status: 'archived' },
      ],
    });
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '空归档线程', provider: 'codex', status: 'idle' },
      ],
      timelinesByThread: threadId === 'thread-1'
        ? { 'thread-1': [{ id: 'active-msg', kind: 'assistant', text: '活跃线程内容' }] }
        : { 'thread-archived': [] },
    }));

    render(<App />);
    await screen.findByText('活跃线程内容');

    fireEvent.click(screen.getByLabelText('打开归档列表'));
    fireEvent.click(await screen.findByRole('button', { name: /空归档线程/ }));

    await waitFor(() => expect(useClientStore.getState().activeThreadId).toBe('thread-archived'));
    expect(screen.queryByText(/让我们从/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /空归档线程/ }).closest('.thread-card')).toHaveClass('active');
    expect(screen.queryByLabelText('复制当前线程')).not.toBeInTheDocument();
  });

  it('loads archived thread messages from the legacy messages RPC', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '消息归档线程', provider: 'codex', status: 'archived' },
      ],
    });
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-archived', name: '消息归档线程', provider: 'codex', status: 'idle' },
      ],
      timelinesByThread: threadId === 'thread-1'
        ? { 'thread-1': [{ id: 'active-msg', kind: 'assistant', text: '活跃线程内容' }] }
        : { 'thread-archived': [] },
    }));
    backend.getThreadMessages.mockImplementation(({ threadId }) => Promise.resolve({
      messages: threadId === 'thread-archived'
        ? [{ id: 'archived-message', role: 'assistant', content: '来自 thread/messages 的归档内容', createdAt: '2026-05-30T00:00:00Z' }]
        : [],
    }));

    render(<App />);
    await screen.findByText('活跃线程内容');

    fireEvent.click(screen.getByLabelText('打开归档列表'));
    fireEvent.click(await screen.findByRole('button', { name: /消息归档线程/ }));

    expect(await screen.findByText('来自 thread/messages 的归档内容')).toBeInTheDocument();
    expect(backend.getThreadMessages).toHaveBeenCalledWith({ threadId: 'thread-archived', limit: 300 });
    expect(screen.queryByText(/让我们从/)).not.toBeInTheDocument();
  });

  it('cleans stale archived threads through the legacy delete RPC', async () => {
    const staleArchiveAt = Date.now() - (8 * 24 * 60 * 60 * 1000);
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [
        { id: 'thread-1', name: '活跃线程', provider: 'codex', status: '工作中' },
        { id: 'thread-stale', name: '旧归档线程', provider: 'codex', status: 'archived', archivedAt: staleArchiveAt },
        { id: 'thread-fresh', name: '近期归档线程', provider: 'codex', status: 'archived', archivedAt: Date.now() },
      ],
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {},
    });

    render(<App />);
    await findThreadCardByName('活跃线程');

    fireEvent.click(screen.getByLabelText('打开归档列表'));
    expect(await screen.findByText('旧归档线程')).toBeInTheDocument();
    expect(screen.getByText('近期归档线程')).toBeInTheDocument();
    expect(screen.getByText('超7天')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('清理无用对话'));
    fireEvent.click(screen.getByText('确认'));

    await waitFor(() => {
      expect(backend.deleteThread).toHaveBeenCalledWith({ threadId: 'thread-stale' });
      expect(backend.deleteThread).not.toHaveBeenCalledWith({ threadId: 'thread-fresh' });
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'archivedThreadAtById.thread-stale',
        value: null,
      });
      expect(screen.queryByText('旧归档线程')).not.toBeInTheDocument();
      expect(screen.getByText('近期归档线程')).toBeInTheDocument();
      expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('已删除 1 个无用会话');
    });
  });

  it('renders warning log entries from bridge events', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    fireEvent.keyDown(screen.getByTestId('activity-panel-resizer'), { key: 'ArrowUp' });

    act(() => {
      bridgeCallback({
        type: 'rpc.failed',
        payload: { method: 'turn/start', threadId: 'thread-1', traceId: 'trace-123' },
      });
    });

    const warningLine = await screen.findByRole('button', { name: /rpc.failed/ });
    expect(screen.queryByText(/turn\/start/)).not.toBeInTheDocument();

    fireEvent.mouseEnter(warningLine);
    expect(screen.queryByTestId('warning-log-popover')).not.toBeInTheDocument();
    fireEvent.click(warningLine);

    expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('rpc.failed');
    expect(screen.getByTestId('warning-log-popover')).toHaveTextContent('turn/start');

    fireEvent.keyDown(screen.getByRole('dialog', { name: 'rpc.failed' }), { key: 'Escape' });

    await waitFor(() => {
      expect(screen.queryByTestId('warning-log-popover')).not.toBeInTheDocument();
    });
  });
