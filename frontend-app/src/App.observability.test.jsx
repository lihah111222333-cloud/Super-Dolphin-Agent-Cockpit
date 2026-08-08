import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import App from './App.jsx';
import { resetClientStoreForTests } from "./entities/client/model/useClientStore.js";
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
  mockTraceDashboardQueryResult,
  openTraceDashboardForTraceId,
  expectTraceDashboardRpcCalls,
  expectTraceDashboardRows,
  expectTraceDashboardDetails,
  showAllTraceDashboardEvents,
  mockRecentSystemLogsResult,
  openRecentSystemLogs,
  expectRecentSystemLogsTable,
  expectRecentSystemLogsRpcCall,
  copyTraceFromRecentLogs,
  toggleInlineTraceFromRecentLogs,
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

  it('opens observability tracing dashboard and queries by trace id', async () => {
    mockTraceDashboardQueryResult();

    const table = await openTraceDashboardForTraceId();

    await expectTraceDashboardRows(table);
    expectTraceDashboardDetails();
    expectTraceDashboardRpcCalls();
    await showAllTraceDashboardEvents();
  });

  it('renders recent system logs and opens a trace from the table', async () => {
    mockRecentSystemLogsResult();

    const table = await openRecentSystemLogs();

    expectRecentSystemLogsTable(table);
    expectRecentSystemLogsRpcCall();
    await copyTraceFromRecentLogs(table);
    await toggleInlineTraceFromRecentLogs(table);
  });

  it('keeps the observability page focused on filtered logs and trace drilldown', async () => {
    render(<App />);

    fireEvent.click(await screen.findByRole('button', { name: '链路追踪' }));

    expect(screen.queryByTestId('observability-backend-logs')).not.toBeInTheDocument();
    expect(screen.queryByTestId('observability-status')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '刷新慢点/错误' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '查询 Trace' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '查询 Thread Recent' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '查询最新日志' })).toBeInTheDocument();
  });

  it('bootstraps project, sidebar, and timeline from backend without the removed work status bar', async () => {
    const { container } = render(<App />);

    expect(await waitForBackendThreadHeading()).toBeInTheDocument();
    expect(within(screen.getByLabelText('Suiyuan app bar')).queryByRole('button', { name: '选择项目' })).not.toBeInTheDocument();
    expect(container.querySelector('.work-status')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    expect(await within(screen.getByTestId('runtime-panel')).findByRole('button', { name: '折叠 file' })).toBeInTheDocument();
    expect(screen.queryByText(/diff --git a\/file b\/file/)).not.toBeInTheDocument();
    expect(backend.getProjects).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-1',
      includeDiff: true,
    });
  });

  it('keeps project selection out of the Suiyuan shell toolbar', async () => {
    render(<App />);

    expect(await waitForBackendThreadHeading()).toBeInTheDocument();
    const topAppBar = within(screen.getByLabelText('Suiyuan app bar'));
    expect(topAppBar.queryByRole('button', { name: '选择项目' })).not.toBeInTheDocument();
    expect(topAppBar.queryByLabelText('当前工作目录')).not.toBeInTheDocument();
    const sidebarToggle = screen.getByRole('button', { name: '显示侧边栏' });
    expect(sidebarToggle).toHaveAttribute('title', '显示侧边栏');
    expect(sidebarToggle).not.toHaveTextContent('侧边栏');
  });

  it('exposes an explicit collapse control outside the Suiyuan sidebar', () => {
    render(<App skipBootstrap />);

    const shell = screen.getByTestId('frontend-app');
    fireEvent.click(screen.getByRole('button', { name: '展开主侧栏' }));
    expect(shell).toHaveClass('sidebar-open');

    const collapseButton = screen.getByRole('button', { name: '折叠侧栏' });
    expect({ container: collapseButton.closest('#app-sidebar'), title: collapseButton.title }).toEqual({ container: null, title: '折叠侧栏' });
    expect(collapseButton.textContent).toBe('');
    fireEvent.click(collapseButton);

    expect(shell).toHaveClass('sidebar-collapsed');
    expect(screen.getAllByRole('button', { name: '展开主侧栏' })).toHaveLength(1);
  });

  it('renders the Stitch Suiyuan sidebar primary navigation order', () => {
    render(<App skipBootstrap />);

    const navButtons = Array.from(screen.getByTestId('sidebar-nav').querySelectorAll('.suiyuan-nav-item'));

    expect(navButtons.map((button) => button.textContent)).toEqual([
      '聊天页面',
      '插件与技能',
      '自动化',
      '提示词',
      '共享文件',
      '记忆中心',
      '链路追踪',
    ]);
    expect(navButtons.map((button) => button.querySelector('svg')?.classList.value)).toEqual([
      expect.stringContaining('lucide-message-square-text'),
      expect.stringContaining('lucide-puzzle'),
      expect.stringContaining('lucide-sliders-horizontal'),
      expect.stringContaining('lucide-circle-user-round'),
      expect.stringContaining('lucide-folder-open'),
      expect.stringContaining('lucide-brain'),
      expect.stringContaining('lucide-database'),
    ]);
    expect(screen.getByRole('button', { name: '新对话' }).querySelector('svg')).toHaveClass('lucide-plus');
  });

  it('keeps only reachable Suiyuan footer actions outside the primary rail', () => {
    render(<App skipBootstrap />);

    expect(within(screen.getByTestId('app-sidebar')).getAllByRole('button').slice(-1).map((button) => button.getAttribute('aria-label'))).toEqual([
      '设置',
    ]);
    expect(within(screen.getByTestId('app-sidebar')).queryByRole('button', { name: 'Support' })).not.toBeInTheDocument();
  });

  it('renders the mobile bottom navigation with core destinations and active state', async () => {
    render(<App skipBootstrap />);

    const mobileNav = screen.getByTestId('mobile-nav');
    expect(mobileNav).toHaveAttribute('aria-label', '主要导航');
    const items = within(mobileNav).getAllByRole('button');
    expect(items.map((button) => button.textContent)).toEqual(['聊天', '插件', '定制角色', '记忆', '设置']);
    expect(items.map((button) => button.querySelector('svg')?.classList.value)).toEqual([
      expect.stringContaining('lucide-message-square-text'),
      expect.stringContaining('lucide-puzzle'),
      expect.stringContaining('lucide-circle-user-round'),
      expect.stringContaining('lucide-brain'),
      expect.stringContaining('lucide-settings'),
    ]);
    expect(within(mobileNav).getByRole('button', { name: '聊天' })).toHaveAttribute('aria-current', 'page');

    fireEvent.click(within(mobileNav).getByRole('button', { name: '记忆' }));

    await waitFor(() => expect(window.location.pathname).toBe('/memory'));
    expect(within(mobileNav).getByRole('button', { name: '记忆' })).toHaveAttribute('aria-current', 'page');
    expect(within(mobileNav).getByRole('button', { name: '聊天' })).not.toHaveAttribute('aria-current');
  });

  it('uses the current URL path as the active page on boot', async () => {
    window.history.pushState({}, '', '/dags');
    backend.getWindowBootstrap.mockResolvedValueOnce({ snapshot: { page: 'chat' } });

    render(<App />);

    const workflowButton = await screen.findByRole('button', { name: '自动化' });
    await waitFor(() => expect(workflowButton).toHaveClass('active'));
    expect(window.location.pathname).toBe('/dags');
  });
