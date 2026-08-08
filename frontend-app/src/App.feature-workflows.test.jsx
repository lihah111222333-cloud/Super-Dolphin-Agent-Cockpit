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
  openPluginsAndSkillsPage,
  mockWorkflowDagLifecycle,
  openWorkflowDashboard,
  runAndStopWorkflowDag,
  createWorkflowSchedule,
  editWorkflowStep,
  deleteWorkflowDag,
  designWorkflowWithAi,
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

  it('runs, stops, deletes, schedules, edits and designs DAGs through the old RPC surface', async () => {
    mockWorkflowDagLifecycle();

    await openWorkflowDashboard();
    await runAndStopWorkflowDag();
    await createWorkflowSchedule();

    await editWorkflowStep();
    await deleteWorkflowDag();
    await designWorkflowWithAi();
  });

  it('uses the active-run query, not stale selected run detail, to unlock DAG controls after stop', async () => {
    const dag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    const agentNode = {
      node_key: 'draft',
      title: '起草',
      node_type: 'agent',
      assigned_to: 'agent-a',
      depends_on: [],
      config: {},
    };
    let hasActiveRun = true;
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [dag] } : { skills: [] },
    ));
    backend.getDagDetail.mockResolvedValue({ dag, nodes: [agentNode] });
    backend.getDagRuns.mockImplementation(({ status }) => {
      if (status === 'running') {
        return Promise.resolve({ runs: hasActiveRun ? [{ run_key: 'run-live', status: 'running' }] : [] });
      }
      return Promise.resolve({ runs: [{ run_key: 'run-live', status: hasActiveRun ? 'running' : 'cancelled' }] });
    });
    backend.getDagRun.mockResolvedValue({ run: { run_key: 'run-live', status: 'running' }, nodes: [agentNode] });
    backend.terminateDagRun.mockImplementation(() => {
      hasActiveRun = false;
      return Promise.resolve({ ok: true });
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect(await screen.findByRole('button', { name: '停止运行' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '停止运行' }));
    await waitFor(() => {
      expect(backend.terminateDagRun).toHaveBeenCalledWith({
        dagKey: 'daily-brief',
        runKey: 'run-live',
        reason: 'user_requested',
      });
    });

    await waitFor(() => {
      expect(screen.queryByRole('button', { name: '停止运行' })).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: '运行' })).not.toBeDisabled();
    });
  });

  it('blocks scheduling when root DAG steps have no assignee', async () => {
    const dag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    const unassignedRoot = {
      node_key: 'draft',
      title: '起草',
      node_type: 'agent',
      assigned_to: '',
      depends_on: [],
      config: { provider: 'codex', model: 'gpt-5', first_turn: '请起草简报' },
    };
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [dag] } : { skills: [] },
    ));
    backend.getDagDetail.mockResolvedValue({ dag, nodes: [unassignedRoot] });
    backend.getDagRuns.mockResolvedValue({ runs: [] });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(1);

    expect(await screen.findByText('首个步骤「起草」缺少执行者，请先在高级设置中填写执行者。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '运行' })).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: '创建定时任务' }));
    const scheduleDialog = await screen.findByRole('dialog', { name: '创建定时任务' });
    fireEvent.click(within(scheduleDialog).getByRole('button', { name: '创建定时任务' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('保存定时任务失败：首个步骤「起草」缺少执行者');
    expect(backend.applyDagOps).not.toHaveBeenCalled();
  });

  it('keeps workflow action notices scoped to the selected task', async () => {
    const firstDag = {
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    const secondDag = {
      dag_key: 'flow-b',
      title: '流程 B',
      status: 'ready',
      trigger: 'manual',
      version: 8,
    };
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [firstDag, secondDag] } : { skills: [] },
    ));
    backend.getDagDetail.mockImplementation(({ dagKey }) => Promise.resolve({
      dag: dagKey === 'flow-a' ? firstDag : secondDag,
      nodes: [{
        node_key: 'draft',
        title: dagKey === 'flow-a' ? '步骤 A' : '步骤 B',
        node_type: 'agent',
        status: 'pending',
        depends_on: [],
        config: {},
      }],
    }));
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.applyDagOps.mockResolvedValue({ newVersion: 8 });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(2);
    expect((await screen.findAllByText('步骤 A')).length).toBeGreaterThanOrEqual(1);

    fireEvent.click(screen.getByText('高级设置'));
    fireEvent.click(screen.getByRole('button', { name: '保存步骤' }));
    await waitFor(() => {
      expect(screen.getByText('已保存步骤 步骤 A')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole('button', { name: /流程 B/ }));
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '流程 B' })).toBeInTheDocument();
    });
    expect((await screen.findAllByText('步骤 B')).length).toBeGreaterThanOrEqual(1);
    await waitFor(() => {
      expect(screen.queryByText('已保存步骤 步骤 A')).not.toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole('button', { name: /流程 A/ }));
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '流程 A' })).toBeInTheDocument();
    });
    expect((await screen.findAllByText('步骤 A')).length).toBeGreaterThanOrEqual(1);
    await waitFor(() => {
      expect(screen.queryByText('已保存步骤 步骤 A')).not.toBeInTheDocument();
    });
  });

  it('does not expose database Skill tools from the Skills navigation', async () => {
    backend.listSkillTools.mockResolvedValueOnce({
      tools: [{
        id: 7,
        name: 'Format Go',
        description: 'Run formatter',
        command: 'gofmt',
        args: ['-w', './internal/module/skill'],
        enabled: true,
      }],
    });
    render(<App />);
    await screen.findByLabelText('插件与技能');
    openPluginsAndSkillsPage();

    expect(await screen.findByRole('heading', { name: 'MCP工具' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Skill工具' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Skill工具' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '新增工具' })).not.toBeInTheDocument();
    expect(screen.queryByText('Format Go')).not.toBeInTheDocument();
    expect(screen.queryByText('本地技能库')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '后端' })).not.toBeInTheDocument();
    expect(backend.listSkillTools).not.toHaveBeenCalled();
    expect(backend.getDashboardPage).not.toHaveBeenCalledWith({ cwd: '/repo/app', page: 'skills' });
    expect(backend.listSkillResolutions).not.toHaveBeenCalled();
  });

  it('keeps composer dock pinned inside the viewport', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': Array.from({ length: 70 }, (_, index) => ({
          id: `m-${index}`,
          role: index % 2 ? 'user' : 'assistant',
          text: `message ${index}`,
          time: '2026-05-30T00:00:00Z',
        })),
      },
    });

    render(<App skipBootstrap />);

    expect(await screen.findByTestId('composer-dock')).toHaveClass('composer', 'composer--docked');
    expect(screen.getByRole('region', { name: '聊天记录' })).toHaveClass('timeline');
  });

  it('connects settings page build info and provider preferences to backend', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activePage: 'settings',
    });
    const preferenceValues = {
      stallThresholdSec: 60,
      'contextUsageAlerts.thresholds': [65, 80, 95],
      'settings.provider.active': 'codex',
      'settings.provider.codex.codexHome': '/home/test/.codex',
      'settings.provider.codex.codexInstanceKey': 'main',
      'settings.provider.codex.codexModelProvider': 'openai',
      'settings.provider.codex.model': 'gpt-5.5',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.sandbox': { type: 'workspaceWrite', writableRoots: ['/repo/app'], networkAccess: false },
    };
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferenceValues[key] ?? null));

    render(<App skipBootstrap />);

    expect(await screen.findByText('Agent Orchestrator v1.2.3')).toBeInTheDocument();
    expect(screen.getByText('linux/amd64')).toBeInTheDocument();
    expect(screen.getByText('2026-05-30T07:00:00Z')).toBeInTheDocument();
    expect(screen.getByText('abc123def456')).toBeInTheDocument();
    expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'stallThresholdSec' });
    expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.active' });

    fireEvent.change(screen.getByLabelText('统一超时阈值'), { target: { value: '120' } });
    fireEvent.change(screen.getByLabelText('Warn 阈值'), { target: { value: '70' } });
    fireEvent.change(screen.getByLabelText('Danger 阈值'), { target: { value: '85' } });
    fireEvent.change(screen.getByLabelText('Critical 阈值'), { target: { value: '96' } });
    fireEvent.click(screen.getByRole('button', { name: '保存运行阈值' }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'stallThresholdSec', value: 120 });
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'contextUsageAlerts.thresholds', value: [70, 85, 96] });
    });

    expect(screen.queryByLabelText('Model Provider')).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Codex Home'), { target: { value: '/tmp/codex-home' } });
    fireEvent.change(screen.getByLabelText('Instance Key'), { target: { value: 'desktop-main' } });
    fireEvent.change(screen.getByLabelText('Sandbox Policy'), { target: { value: 'readOnly' } });
    fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));

    await waitFor(() => {
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.codexHome', value: '/tmp/codex-home' });
      expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.codexInstanceKey', value: 'desktop-main' });
      expect(backend.setPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.provider.codex.sandbox',
        value: { type: 'readOnly' },
      });
    });
    expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
      key: 'settings.provider.codex.codexModelProvider',
    }));

    backend.getBuildInfo.mockResolvedValueOnce({
      version: 'v1.2.4',
      runtime: 'linux/amd64',
      buildTime: '2026-05-30T08:00:00Z',
      commit: 'feedface9876',
    });
    fireEvent.click(screen.getByRole('button', { name: '刷新构建信息' }));
    expect(await screen.findByText('Agent Orchestrator v1.2.4')).toBeInTheDocument();
    expect(screen.getByText('feedface9876')).toBeInTheDocument();
  });
