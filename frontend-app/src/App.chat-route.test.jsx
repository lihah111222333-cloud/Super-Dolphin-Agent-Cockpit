import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import App from './App.jsx';
import { resetClientStoreForTests } from './entities/client/model/useClientStore.js';
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
