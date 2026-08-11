import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import App from './App.jsx';
import { resetClientStoreForTests } from "./entities/client/model/useClientStore.js";
import { frontendHealthSnapshot } from "./shared/diagnostics/frontendHealthStore.js";
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
  getSidebarNavButton,
  getThreadCardByName,
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

  it.each(['/tasks', '/commands'])('falls back to chat for the removed %s route', async (pathname) => {
    window.history.pushState({}, '', pathname);

    render(<App />);

    const chatButton = await screen.findByRole('button', { name: '聊天页面' });
    await waitFor(() => expect(chatButton).toHaveClass('active'));
    expect(screen.queryByRole('button', { name: '任务' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '命令' })).not.toBeInTheDocument();
    expect(chatButton).toHaveClass('active');
  });

  it('lets user navigation override the explicit boot URL after initial route sync', async () => {
    window.history.pushState({}, '', '/dags');

    render(<App skipBootstrap />);

    const workflowButton = await screen.findByRole('button', { name: '自动化' });
    await waitFor(() => expect(workflowButton).toHaveClass('active'));

    fireEvent.click(getSidebarNavButton('插件与技能'));

    await waitFor(() => expect(getSidebarNavButton('插件与技能')).toHaveClass('active'));
    expect(window.location.pathname).toBe('/skills');
  });

  it('writes page navigation to browser history and restores it on popstate', async () => {
    render(<App skipBootstrap />);

    fireEvent.click(getSidebarNavButton('插件与技能'));
    await waitFor(() => expect(window.location.pathname).toBe('/skills'));

    fireEvent.click(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '设置' }));
    await waitFor(() => expect(window.location.pathname).toBe('/settings'));

    await act(async () => {
      window.history.pushState({ activePage: 'skills' }, '', '/skills');
      window.dispatchEvent(new PopStateEvent('popstate', { state: { activePage: 'skills' } }));
    });

    await waitFor(() => expect(getSidebarNavButton('插件与技能')).toHaveClass('active'));
    expect(getSidebarNavButton('插件与技能')).toHaveClass('active');
  });

  it('hides idle status noise while keeping the provider badge in thread cards', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '静默会话', provider: 'codex', status: 'idle' }],
    });

    render(<App />);

    const card = await findThreadCardByName('静默会话');
    expect(within(card).queryByRole('button', { name: '重命名会话' })).not.toBeInTheDocument();
    expect(card).toHaveTextContent('codex');
    expect(card).not.toHaveTextContent('idle');
    expect(card.querySelector('em')).toBeNull();
    expect(card.querySelector('.thread-status-dot')).toHaveClass('thread-status-dot--idle');
  });

  it('maps backend projected thread statuses in thread cards', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-thinking',
      threads: [
        { id: 'thread-thinking', name: '思考会话', provider: 'codex', status: 'thinking' },
        { id: 'thread-editing', name: '编辑会话', provider: 'codex', status: 'editing' },
        { id: 'thread-waiting', name: '确认会话', provider: 'codex', status: 'waiting' },
        { id: 'thread-syncing', name: '同步会话', provider: 'codex', status: 'syncing' },
        { id: 'thread-error', name: '异常会话', provider: 'codex', status: 'error' },
      ],
    });

    render(<App />);

    expect(await findThreadCardByName('思考会话')).toHaveTextContent('思考中');
    expect(getThreadCardByName('编辑会话')).toHaveTextContent('编辑中');
    expect(getThreadCardByName('确认会话')).toHaveTextContent('等待确认');
    expect(getThreadCardByName('同步会话')).toHaveTextContent('同步中');
    expect(getThreadCardByName('异常会话')).toHaveTextContent('异常');
    expect(getThreadCardByName('思考会话').querySelector('.thread-status-dot')).toHaveClass('thread-status-dot--thinking');
    expect(getThreadCardByName('确认会话').querySelector('.thread-status-dot')).toHaveClass('thread-status-dot--waiting');
    expect(getThreadCardByName('异常会话').querySelector('.thread-status-dot')).toHaveClass('thread-status-dot--error');
  });

  it('shows a bootstrap failure notice when the backend bridge is unavailable', async () => {
    backend.readConfig.mockRejectedValue(new Error('runtime shim: failed to connect ws://127.0.0.1:5175/wails/ws'));

    render(<App />);

    expect(await screen.findByRole('button', { name: '重新连接后端' })).toBeInTheDocument(); expect(screen.queryByText('连接后端失败：连接后端失败，请重试。')).not.toBeInTheDocument();
    expect(screen.queryByText(/127\.0\.0\.1/)).not.toBeInTheDocument();
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'app.bootstrap.background' }),
    ]));
    expect(JSON.stringify(frontendHealthSnapshot())).not.toContain('127.0.0.1');
  });

  it('shows a recovery control when successful transport reaches a local bootstrap failure', async () => {
    const rawCause = 'invalid projects response /Users/private token=secret';
    backend.getProjects.mockRejectedValueOnce(new TypeError(rawCause));

    render(<App />);

    expect(await screen.findByRole('button', { name: '重新连接后端' })).toBeInTheDocument();
    expect(screen.queryByText(/连接后端失败/)).not.toBeInTheDocument();
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'app.bootstrap.background' }),
    ]));
    expect(JSON.stringify(frontendHealthSnapshot())).not.toContain('/Users/private');
    expect(JSON.stringify(frontendHealthSnapshot())).not.toContain('token=secret');
  });

  it('does not expose provider switching when no project cwd is available', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '',
      activeProject: '',
      provider: 'codex',
    });

    render(<App skipBootstrap />);

    await screen.findByTestId('composer-input');
    expect(screen.queryByLabelText('切换 Claude / Codex provider')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '请先连接后端并选择项目' })).not.toBeInTheDocument();
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({ key: 'settings.provider.active' }));
  });

  it('disables composer send by button and Enter when no project cwd is available', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '',
      activeProject: '',
      activeThreadId: '',
      draft: 'Write something',
      attachments: [],
    });

    render(<App skipBootstrap />);

    // 发送仍要求项目 cwd（业务契约保留）；附件/模型控件在后端就绪后即可交互。
    const sendButton = await screen.findByRole('button', { name: '发送消息' });
    expect(sendButton).toBeDisabled();
    expect(screen.getByRole('button', { name: '添加文件' })).toBeEnabled();
    expect(screen.queryByRole('combobox', { name: '发送权限' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '选择模型' })).toBeEnabled();

    fireEvent.click(sendButton);
    fireEvent.keyDown(screen.getByTestId('composer-input'), { key: 'Enter', code: 'Enter', charCode: 13 });

    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).not.toHaveBeenCalled();

    // 附件按钮在无项目时进入真实文件选择流程（不依赖项目 cwd）。
    fireEvent.click(screen.getByRole('button', { name: '添加文件' }));
    await waitFor(() => expect(backend.selectFiles).toHaveBeenCalled());
  });

  it('does not show composer interrupt controls for a running runtime agent without an active turn', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_123',
      draft: '',
      attachments: [],
      threads: [{ id: 'agent_123', name: 'Runtime Agent', provider: 'codex', status: 'running' }],
      statuses: {
        agent_123: { status: 'running', interruptible: true },
      },
      threadTimelineReadyByThread: { agent_123: true },
      timelinesByThread: {
        agent_123: [{ id: 'assistant-1', role: 'assistant', text: '正在执行。' }],
      },
    });

    render(<App skipBootstrap />);
    await act(async () => Promise.resolve());

    expect(screen.queryByRole('button', { name: '中断当前执行' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('停止')).not.toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape', code: 'Escape' });

    await waitFor(() => expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('当前没有可中断任务'));
    expect(backend.interruptTurn).not.toHaveBeenCalled();
  });

  it('shows an enabled composer interrupt button for a running runtime agent with an active turn and without a draft', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'agent_123',
      draft: '',
      attachments: [],
      threads: [{ id: 'agent_123', name: 'Runtime Agent', provider: 'codex', status: 'running' }],
      activeTurnByThread: {
        agent_123: { id: 'turn-123', threadId: 'agent_123', status: 'running' },
      },
      statuses: {
        agent_123: { status: 'running', interruptible: true },
      },
      threadTimelineReadyByThread: { agent_123: true },
      timelinesByThread: {
        agent_123: [{ id: 'assistant-1', role: 'assistant', text: '正在执行。' }],
      },
    });

    render(<App skipBootstrap />);

    const interruptButton = screen.getByRole('button', { name: '中断当前执行' });
    expect(interruptButton).toBeEnabled();
    expect(screen.queryByRole('button', { name: '发送消息' })).not.toBeInTheDocument();

    fireEvent.click(interruptButton);

    await waitFor(() => expect(backend.interruptTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'agent_123',
      expectedTurnId: 'turn-123',
      requestId: expect.any(String),
      source: 'ui_stop',
    }));
  });
