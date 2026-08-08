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
  deferred,
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

  it('renders workflow topology, shared files, and readable final output file panels', async () => {
    const dag = {
      dag_key: 'report-flow',
      title: '报告流水线',
      status: 'succeeded',
      trigger: 'manual',
      version: 3,
      latest_run: { run_key: 'run-report', status: 'succeeded' },
    };
    const nodes = [{
      node_key: 'collect',
      title: '收集资料',
      status: 'succeeded',
      config: { outputs: { to_sharedfile: { path: 'brief/raw.md', lock_mode: 'exclusive' } } },
    }, {
      node_key: 'write',
      title: '撰写报告',
      status: 'succeeded',
      depends_on: ['collect', 'external-input'],
      config: {
        inputs: { from_sharedfiles: ['brief/raw.md'] },
        outputs: { to_sharedfile: { path: 'reports/final.md', lock_mode: 'append' } },
      },
    }];
    backend.getDashboardPage.mockImplementation(({ page }) => (
      page === 'dags' ? Promise.resolve({ dags: [dag] }) : Promise.resolve({ skills: [] })
    ));
    backend.getDagDetail.mockResolvedValue({ dag, nodes });
    backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
      runs: status === 'running' ? [] : [{ run_key: 'run-report', status: 'succeeded', metadata: { final_output: { kind: 'sharedfile', path: 'reports/final.md' } } }],
    }));
    backend.getDagRun.mockResolvedValue({
      run: { run_key: 'run-report', status: 'succeeded', metadata: { final_output: { kind: 'sharedfile', path: 'reports/final.md' } } },
      nodes,
    });
    backend.readSharedFile.mockResolvedValue({ path: 'reports/final.md', content: '最终报告正文' });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));

    expect((await screen.findAllByText('报告流水线')).length).toBeGreaterThanOrEqual(2);
    expect(await screen.findByText('流程图')).toBeInTheDocument();
    expect((await screen.findAllByText(/收集资料/)).length).toBeGreaterThanOrEqual(1);
    expect(await screen.findByText(/收集资料 --> 撰写报告/)).toBeInTheDocument();
    expect(await screen.findByText(/外部依赖 1 --> 撰写报告/)).toBeInTheDocument();

    expect(screen.getByText('工作文件')).toBeInTheDocument();
    expect(screen.getAllByText('brief/raw.md').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('reports/final.md').length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText('读取')).toBeInTheDocument();
    expect(screen.getByText('写入 · 追加写入')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '读取最终结果' }));
    await waitFor(() => {
      expect(backend.readSharedFile).toHaveBeenCalledWith({ path: 'reports/final.md' });
    });
    expect(await screen.findByText('最终报告正文')).toBeInTheDocument();
  });

  it('auto-updates workflow page without a manual refresh button', async () => {
    let dags = [{
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'running',
      trigger: 'manual',
      version: 1,
      latest_run: { run_key: 'run-a', status: 'running' },
    }];
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags } : { skills: [] },
    ));
    backend.getDagDetail.mockImplementation(({ dagKey }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      const suffix = (dag?.title || '').split(' ').pop() || '';
      return Promise.resolve({
        dag,
        nodes: [{ node_key: 'step', title: `步骤 ${suffix}`, node_type: 'agent', status: 'running', depends_on: [], config: {} }],
      });
    });
    backend.getDagRuns.mockImplementation(({ dagKey, status }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      if (status === 'running') return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
      return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
    });
    backend.getDagRun.mockImplementation(({ runKey }) => Promise.resolve({
      run: { run_key: runKey, status: 'running' },
      nodes: [],
    }));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));

    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();

    dags = [{
      dag_key: 'flow-b',
      title: '流程 B',
      status: 'running',
      trigger: 'manual',
      version: 2,
      latest_run: { run_key: 'run-b', status: 'running' },
    }];
    await act(async () => {
      bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-b', run_key: 'run-b', node_key: 'step', new_status: 'running' } });
    });

    expect((await screen.findAllByText('流程 B')).length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText('流程 A')).not.toBeInTheDocument();

    dags = [{
      dag_key: 'flow-c',
      title: '流程 C',
      status: 'running',
      trigger: 'manual',
      version: 3,
      latest_run: { run_key: 'run-c', status: 'running' },
    }];
    await act(async () => {
      window.dispatchEvent(new Event('focus'));
    });

    expect((await screen.findAllByText('流程 C')).length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText('流程 B')).not.toBeInTheDocument();
  });

  it('does not poll workflow data with a page interval', async () => {
    const intervalSpy = vi.spyOn(window, 'setInterval');
    try {
      const runningDag = {
        dag_key: 'flow-a',
        title: '流程 A',
        status: 'running',
        trigger: 'manual',
        version: 1,
        latest_run: { run_key: 'run-a', status: 'running' },
      };
      backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
        page === 'dags' ? { dags: [runningDag] } : { skills: [] },
      ));
      backend.getDagDetail.mockResolvedValue({
        dag: runningDag,
        nodes: [{ node_key: 'step', title: '步骤 A', node_type: 'agent', status: 'running', depends_on: [], config: {} }],
      });
      backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
        runs: status === 'running' ? [{ run_key: 'run-a', status: 'running' }] : [{ run_key: 'run-a', status: 'running' }],
      }));
      backend.getDagRun.mockResolvedValue({
        run: { run_key: 'run-a', status: 'running' },
        nodes: [],
      });

      render(<App />);
      await waitForBackendThreadHeading();
      fireEvent.click(screen.getByLabelText('自动化'));

      expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);
      expect(intervalSpy.mock.calls.filter((call) => call[1] === 4000)).toHaveLength(0);
    }
    finally {
      intervalSpy.mockRestore();
    }
  });

  it('keeps cached workflow data visible and exposes retry when a background sync fails', async () => {
    let dags = [{
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'running',
      trigger: 'manual',
      version: 1,
      latest_run: { run_key: 'run-a', status: 'running' },
    }];
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags } : { skills: [] },
    ));
    backend.getDagDetail.mockImplementation(({ dagKey }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      const suffix = (dag?.title || '').split(' ').pop() || '';
      return Promise.resolve({
        dag,
        nodes: [{ node_key: 'step', title: `步骤 ${suffix}`, node_type: 'agent', status: 'running', depends_on: [], config: {} }],
      });
    });
    backend.getDagRuns.mockImplementation(({ dagKey, status }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      if (status === 'running') return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
      return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
    });
    backend.getDagRun.mockImplementation(({ runKey }) => Promise.resolve({
      run: { run_key: runKey, status: 'running' },
      nodes: [],
    }));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);

    backend.getDashboardPage.mockRejectedValueOnce(new Error('workflow backend offline'));
    await act(async () => {
      bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-a', run_key: 'run-a', node_key: 'step', new_status: 'running' } });
      await Promise.resolve();
    });

    expect(screen.getAllByText('流程 A').length).toBeGreaterThanOrEqual(1);
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('同步自动化失败，当前显示上次成功数据。');
    expect(alert).not.toHaveTextContent('workflow backend offline');
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'workflow.dashboard.load', diagnosticId: expect.any(String) }),
    ]));

    dags = [{
      dag_key: 'flow-b',
      title: '流程 B',
      status: 'running',
      trigger: 'manual',
      version: 2,
      latest_run: { run_key: 'run-b', status: 'running' },
    }];
    fireEvent.click(screen.getByRole('button', { name: '重试同步' }));

    expect((await screen.findAllByText('流程 B')).length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('clears a stale workflow sync alert after focus refresh succeeds', async () => {
    let dags = [{
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'running',
      trigger: 'manual',
      version: 1,
      latest_run: { run_key: 'run-a', status: 'running' },
    }];
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags } : { skills: [] },
    ));
    backend.getDagDetail.mockImplementation(({ dagKey }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      const suffix = (dag?.title || '').split(' ').pop() || '';
      return Promise.resolve({
        dag,
        nodes: [{ node_key: 'step', title: `步骤 ${suffix}`, node_type: 'agent', status: 'running', depends_on: [], config: {} }],
      });
    });
    backend.getDagRuns.mockImplementation(({ dagKey, status }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      if (status === 'running') return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
      return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
    });
    backend.getDagRun.mockImplementation(({ runKey }) => Promise.resolve({
      run: { run_key: runKey, status: 'running' },
      nodes: [],
    }));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);

    backend.getDashboardPage.mockRejectedValueOnce(new Error('workflow backend offline'));
    await act(async () => {
      bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-a', run_key: 'run-a', node_key: 'step', new_status: 'running' } });
      await Promise.resolve();
    });
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('同步自动化失败，当前显示上次成功数据。');
    expect(alert).not.toHaveTextContent('workflow backend offline');

    dags = [{
      dag_key: 'flow-b',
      title: '流程 B',
      status: 'running',
      trigger: 'manual',
      version: 2,
      latest_run: { run_key: 'run-b', status: 'running' },
    }];
    await act(async () => {
      window.dispatchEvent(new Event('focus'));
    });

    expect((await screen.findAllByText('流程 B')).length).toBeGreaterThanOrEqual(1);
    await waitFor(() => {
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });
  });

  it('coalesces selected workflow detail and run refreshes when events and retry overlap', async () => {
    const runningDag = {
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'running',
      trigger: 'manual',
      version: 1,
      latest_run: { run_key: 'run-a', status: 'running' },
    };
    const node = { node_key: 'step', title: '步骤 A', node_type: 'agent', status: 'running', depends_on: [], config: {} };
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [runningDag] } : { skills: [] },
    ));
    backend.getDagDetail.mockResolvedValue({ dag: runningDag, nodes: [node] });
    backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
      runs: status === 'running' ? [runningDag.latest_run] : [runningDag.latest_run],
    }));
    backend.getDagRun.mockResolvedValue({ run: runningDag.latest_run, nodes: [node] });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);
    expect((await screen.findAllByText('步骤 A')).length).toBeGreaterThanOrEqual(1);

    vi.clearAllMocks();
    const detailRefresh = deferred();
    const recentRunsRefresh = deferred();
    const activeRunsRefresh = deferred();
    const runRefresh = deferred();
    backend.getDashboardPage
      .mockImplementationOnce(({ page }) => (
        page === 'dags' ? Promise.reject(new Error('workflow backend offline')) : Promise.resolve({ skills: [] })
      ))
      .mockImplementation(({ page }) => Promise.resolve(
        page === 'dags' ? { dags: [runningDag] } : { skills: [] },
      ));
    backend.getDagDetail.mockImplementation(() => detailRefresh.promise);
    backend.getDagRuns.mockImplementation(({ status }) => (
      status === 'running' ? activeRunsRefresh.promise : recentRunsRefresh.promise
    ));
    backend.getDagRun.mockImplementation(() => runRefresh.promise);

    await act(async () => {
      bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-a', run_key: 'run-a', node_key: 'step', new_status: 'running' } });
      await Promise.resolve();
    });
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('同步自动化失败，当前显示上次成功数据。');
    expect(alert).not.toHaveTextContent('workflow backend offline');
    await waitFor(() => expect(backend.getDagDetail).toHaveBeenCalledTimes(1));

    fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));
    await act(async () => {
      bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-a', run_key: 'run-a', node_key: 'step', new_status: 'running' } });
      await Promise.resolve();
      await Promise.resolve();
    });
    await waitFor(() => expect(backend.getDashboardPage.mock.calls.length).toBeGreaterThanOrEqual(2));

    expect(backend.getDagDetail).toHaveBeenCalledTimes(1);
    expect(backend.getDagRuns).toHaveBeenCalledTimes(2);
    expect(backend.getDagRun).toHaveBeenCalledTimes(1);

    await act(async () => {
      detailRefresh.reject(new Error('detail backend offline'));
      recentRunsRefresh.resolve({ runs: [runningDag.latest_run] });
      activeRunsRefresh.resolve({ runs: [runningDag.latest_run] });
      runRefresh.resolve({ run: runningDag.latest_run, nodes: [node] });
    });

    const detailAlert = await screen.findByRole('alert');
    expect(detailAlert).toHaveTextContent('同步自动化失败，当前显示上次成功数据。');
    expect(detailAlert).not.toHaveTextContent('detail backend offline');
  });

  it('shows a retryable blocking error instead of an empty workflow state on initial load failure', async () => {
    const flow = {
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'running',
      trigger: 'manual',
      version: 1,
      latest_run: { run_key: 'run-a', status: 'running' },
    };
    backend.getDashboardPage.mockImplementation(({ page }) => (
      page === 'dags'
        ? Promise.reject(new Error('workflow backend offline'))
        : Promise.resolve({ skills: [] })
    ));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('加载自动化失败，请重试。');
    expect(alert).not.toHaveTextContent('workflow backend offline');
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'workflow.dashboard.load', diagnosticId: expect.any(String) }),
    ]));
    expect(screen.queryByText('无任务')).not.toBeInTheDocument();

    backend.getDashboardPage.mockImplementation(({ page }) => (
      page === 'dags' ? Promise.resolve({ dags: [flow] }) : Promise.resolve({ skills: [] })
    ));
    backend.getDagDetail.mockResolvedValue({
      dag: flow,
      nodes: [{ node_key: 'step', title: '步骤 A', node_type: 'agent', status: 'running', depends_on: [], config: {} }],
    });
    backend.getDagRuns.mockResolvedValue({ runs: [{ run_key: 'run-a', status: 'running' }] });
    backend.getDagRun.mockResolvedValue({ run: { run_key: 'run-a', status: 'running' }, nodes: [] });
    fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));

    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('keeps cached workflow data visible when navigating back and refreshes silently', async () => {
    let dags = [{
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'running',
      trigger: 'manual',
      version: 1,
      latest_run: { run_key: 'run-a', status: 'running' },
    }];
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags } : { skills: [] },
    ));
    backend.getDagDetail.mockImplementation(({ dagKey }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      const suffix = (dag?.title || '').split(' ').pop() || '';
      return Promise.resolve({
        dag,
        nodes: [{ node_key: 'step', title: `步骤 ${suffix}`, node_type: 'agent', status: 'running', depends_on: [], config: {} }],
      });
    });
    backend.getDagRuns.mockImplementation(({ dagKey }) => {
      const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
      return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
    });
    backend.getDagRun.mockImplementation(({ runKey }) => Promise.resolve({
      run: { run_key: runKey, status: 'running' },
      nodes: [],
    }));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));
    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);

    fireEvent.click(screen.getByLabelText('新对话'));
    dags = [{
      dag_key: 'flow-b',
      title: '流程 B',
      status: 'running',
      trigger: 'manual',
      version: 2,
      latest_run: { run_key: 'run-b', status: 'running' },
    }];
    fireEvent.click(screen.getByLabelText('自动化'));

    expect(screen.queryByText('正在加载自动化...')).not.toBeInTheDocument();
    expect(screen.queryByText('正在加载详情...')).not.toBeInTheDocument();
    expect(screen.getAllByText('流程 A').length).toBeGreaterThanOrEqual(1);
    expect((await screen.findAllByText('流程 B')).length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText('流程 A')).not.toBeInTheDocument();
  });

  it('allows selecting an empty DAG category and shows an empty state', async () => {
    const scheduledDag = {
      dag_key: 'weekly-report',
      title: 'Weekly Report',
      description: '每周报告',
      status: 'ready',
      trigger: 'scheduled',
      cron_expr: '0 8 * * 1',
      version: 3,
    };
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [scheduledDag] } : { skills: [] },
    ));
    backend.getDagDetail.mockResolvedValue({ dag: scheduledDag, nodes: [] });
    backend.getDagRuns.mockResolvedValue({ runs: [] });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: '定时任务 1' })).toHaveAttribute('aria-selected', 'true');
    });
    fireEvent.click(screen.getByRole('tab', { name: '进行中 0' }));

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: '进行中 0' })).toHaveAttribute('aria-selected', 'true');
    });
    expect(screen.getByText('无任务')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Weekly Report/ })).not.toBeInTheDocument();
  });

  it('presents workflow schedules without raw cron or DAG internals', async () => {
    const scheduledDag = {
      dag_key: 'daily_remote_main_pr_review',
      title: '每日远程 main PR 审核',
      status: 'ready',
      trigger: 'scheduled',
      cron_expr: '0 1 * * *',
      next_run_at: '2026-06-01T01:00:00Z',
      version: 7,
    };
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [scheduledDag] } : { skills: [] },
    ));
    backend.getDagDetail.mockResolvedValue({ dag: scheduledDag, nodes: [] });
    backend.getDagRuns.mockResolvedValue({ runs: [] });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: '定时任务 1' })).toHaveAttribute('aria-selected', 'true');
    });
    expect(screen.getAllByText('每天 01:00').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('已启用')).toBeInTheDocument();
    expect(screen.queryByText('0 1 * * *')).not.toBeInTheDocument();
    expect(screen.queryByText('daily_remote_main_pr_review')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '修改计划' }));
    const dialog = await screen.findByRole('dialog', { name: '修改计划' });
    expect(within(dialog).queryByLabelText('Cron 表达式')).not.toBeInTheDocument();
    expect(within(dialog).getByLabelText('运行频率')).toHaveValue('daily');
    expect(within(dialog).getByLabelText('运行时间')).toHaveValue('01:00');
  });
