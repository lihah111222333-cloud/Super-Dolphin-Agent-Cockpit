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
  createSharedFileState,
  mockSharedFileWorkflow,
  openSharedFilesPage,
  refreshSharedFilesFromBridge,
  refreshSharedFilesFromFocus,
  previewFinalSharedFile,
  exportAndDeleteWorkSharedFile,
  continueChatFromFinalSharedFile,
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

  it('loads shared files from the shared-files RPC and wires open, export, delete, and continue actions', async () => {
    const sharedFiles = createSharedFileState();
    mockSharedFileWorkflow(sharedFiles);

    await openSharedFilesPage();
    await refreshSharedFilesFromBridge(sharedFiles);
    await refreshSharedFilesFromFocus(sharedFiles);
    await previewFinalSharedFile();
    await exportAndDeleteWorkSharedFile();
    await continueChatFromFinalSharedFile();
  });

  it('formats markdown-fenced JSON shared files for the row summary and preview modal', async () => {
    const content = [
      '```json',
      JSON.stringify({
        videos: [{
          title: '月薪5000我是怎么在上海活下去的',
          hook: '很多人问我，5000块在上海怎么活？',
          script: '开场：我来上海三年了，最低的时候月薪5000。',
        }],
        thumbnail_idea: '本人手写账单特写',
      }),
      '```',
    ].join('\n');
    backend.listSharedFiles.mockResolvedValue({
      files: [{
        path: 'reports/douyin_viral_scripts.md',
        content,
        updated_by: 'node-router',
        updated_at: '2026-06-03T12:59:59Z',
      }],
      finalOutputRefs: [{
        path: 'reports/douyin_viral_scripts.md',
        runKey: 'run-ui-1',
        dagKey: 'douyin-viral-script-daily-5pm',
        sourceNodeKey: 'generate_douyin_scripts',
      }],
      sharedFileRetention: {
        items: [{ path: 'reports/douyin_viral_scripts.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
        protectedCount: 1,
        cleanupCandidateCount: 0,
      },
    });
    backend.readSharedFile.mockResolvedValue({
      path: 'reports/douyin_viral_scripts.md',
      content,
      updatedBy: 'node-router',
      updatedAt: '2026-06-03T12:59:59Z',
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));

    const finalCard = (await screen.findByText('douyin_viral_scripts.md')).closest('article');
    expect(within(finalCard).getByText(/JSON 对象 · videos: 1 项/)).toBeInTheDocument();
    expect(within(finalCard).queryByText(/```json/)).not.toBeInTheDocument();

    fireEvent.click(within(finalCard).getByRole('button', { name: '打开' }));
    const dialog = await screen.findByRole('dialog', { name: '文件预览' });
    expect(within(dialog).getByText('JSON（Markdown 代码块）')).toBeInTheDocument();

    const preview = appOverlayHost.querySelector('.shared-file-content-preview');
    expect(preview?.textContent).toContain('"videos": [');
    expect(preview?.textContent).toContain('"title": "月薪5000我是怎么在上海活下去的"');
    expect(preview?.textContent).not.toContain('```json');
  });

  it('renders invalid markdown-fenced JSON-like shared files without showing parse errors', async () => {
    const content = [
      '```json',
      '{"videos":[{"title":"月薪5000我是怎么在上海活下去的","hook":"很多人问我，5000块在上海怎么活？","thumbnail_idea":"本人手写账单特写，标注"月薪5000存款5万"红色大字","cta":"评论区报一下"}]}',
      '```',
    ].join('\n');
    backend.listSharedFiles.mockResolvedValue({
      files: [{
        path: 'reports/douyin_viral_scripts.md',
        content,
        updated_by: 'node-router',
        updated_at: '2026-06-03T12:59:59Z',
      }],
      finalOutputRefs: [{
        path: 'reports/douyin_viral_scripts.md',
        runKey: 'run-ui-1',
        dagKey: 'douyin-viral-script-daily-5pm',
        sourceNodeKey: 'generate_douyin_scripts',
      }],
      sharedFileRetention: {
        items: [{ path: 'reports/douyin_viral_scripts.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
        protectedCount: 1,
        cleanupCandidateCount: 0,
      },
    });
    backend.readSharedFile.mockResolvedValue({
      path: 'reports/douyin_viral_scripts.md',
      content,
      updatedBy: 'node-router',
      updatedAt: '2026-06-03T12:59:59Z',
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));

    const finalCard = (await screen.findByText('douyin_viral_scripts.md')).closest('article');
    expect(within(finalCard).getByText(/类 JSON · videos: 1 项/)).toBeInTheDocument();
    expect(within(finalCard).queryByText(/JSON 格式化失败|JSON Parse error|Unrecognized token/)).not.toBeInTheDocument();

    fireEvent.click(within(finalCard).getByRole('button', { name: '打开' }));
    const dialog = await screen.findByRole('dialog', { name: '文件预览' });
    expect(within(dialog).getByText('类 JSON（Markdown 代码块）')).toBeInTheDocument();

    const preview = appOverlayHost.querySelector('.shared-file-content-preview');
    expect(preview?.textContent).toContain('\n    "hook":');
    expect(preview?.textContent).toContain('标注"月薪5000存款5万"红色大字');
    expect(preview?.textContent).not.toMatch(/JSON 格式化失败|JSON Parse error|Unrecognized token|```json/);
  });

  it('keeps the shared-file delete dialog open while deletion is pending', async () => {
    const deletePending = deferred();
    backend.listSharedFiles.mockResolvedValue({
      files: [{
        path: 'scratch/work.json',
        content: '{"step":1}',
        updated_by: 'agent',
        updated_at: '2026-05-30T07:00:00Z',
      }],
      memory: [{
        path: 'scratch/work.json',
        content: '{"step":1}',
        updated_by: 'agent',
        updated_at: '2026-05-30T07:00:00Z',
      }],
      finalOutputRefs: [],
      sharedFileRetention: {
        items: [{ path: 'scratch/work.json', protected: false, cleanupCandidate: true, reason: 'unreferenced' }],
        protectedCount: 0,
        cleanupCandidateCount: 1,
      },
    });
    backend.deleteSharedFile.mockReturnValue(deletePending.promise);

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));

    const workCard = (await screen.findByText('work.json')).closest('article');
    fireEvent.click(within(workCard).getByRole('button', { name: '删除' }));
    let dialog = await screen.findByRole('dialog', { name: '删除文件' });
    fireEvent.click(within(dialog).getByRole('button', { name: '确认删除' }));
    await waitFor(() => {
      expect(within(screen.getByRole('dialog', { name: '删除文件' })).getByRole('button', { name: '删除中...' })).toBeDisabled();
    });

    dialog = screen.getByRole('dialog', { name: '删除文件' });
    fireEvent.keyDown(dialog, { key: 'Escape', code: 'Escape' });
    expect(screen.getByRole('dialog', { name: '删除文件' })).toBeInTheDocument();

    await act(async () => {
      deletePending.resolve({ deleted: true });
    });
    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: '删除文件' })).not.toBeInTheDocument();
    });
  });

  it('accepts the legacy shared-files response without final-output metadata', async () => {
    backend.listSharedFiles.mockResolvedValue({
      memory: [{
        path: 'scratch/legacy.md',
        content: 'legacy shared file',
        updated_by: 'agent',
        updated_at: '2026-05-30T09:00:00Z',
      }],
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));

    expect(await screen.findByText('legacy.md')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '全部 1' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '工作文件 1' })).toBeInTheDocument();
  });

  it('keeps cached shared files visible when navigating back and refreshes silently', async () => {
    let memoryFiles = [{
      path: 'reports/final.md',
      content: 'final summary',
      updated_by: 'dag-runner',
      updated_at: '2026-05-30T08:00:00Z',
    }];
    const memoryPayload = () => ({
      files: memoryFiles,
      memory: memoryFiles,
      finalOutputRefs: [{ path: 'reports/final.md', runKey: 'run-1', dagKey: 'daily-brief', sourceNodeKey: 'report' }],
      sharedFileRetention: {
        items: [{ path: 'reports/final.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
        protectedCount: 1,
        cleanupCandidateCount: 0,
      },
    });
    backend.listSharedFiles.mockImplementation(() => Promise.resolve(memoryPayload()));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));
    expect(await screen.findByText('final.md')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('新对话'));
    memoryFiles = [{
      path: 'scratch/notes.md',
      content: 'fresh notes',
      updated_by: 'agent',
      updated_at: '2026-05-30T09:00:00Z',
    }];
    fireEvent.click(screen.getByLabelText('共享文件'));

    expect(screen.queryByText('正在加载共享文件...')).not.toBeInTheDocument();
    expect(screen.getByText('final.md')).toBeInTheDocument();
    expect(await screen.findByText('notes.md')).toBeInTheDocument();
    expect(screen.queryByText('final.md')).not.toBeInTheDocument();
  });

  it('does not poll shared files with a page interval', async () => {
    const intervalSpy = vi.spyOn(window, 'setInterval');
    try {
      backend.listSharedFiles.mockResolvedValue({
        files: [{
          path: 'reports/final.md',
          content: 'final summary',
          updated_by: 'dag-runner',
          updated_at: '2026-05-30T08:00:00Z',
        }],
        finalOutputRefs: [{ path: 'reports/final.md', runKey: 'run-1', dagKey: 'daily-brief', sourceNodeKey: 'report' }],
        sharedFileRetention: {
          items: [{ path: 'reports/final.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
          protectedCount: 1,
          cleanupCandidateCount: 0,
        },
      });

      render(<App />);
      await waitForBackendThreadHeading();
      fireEvent.click(screen.getByLabelText('共享文件'));

      expect(await screen.findByText('final.md')).toBeInTheDocument();
      expect(intervalSpy.mock.calls.filter((call) => call[1] === 4000)).toHaveLength(0);
    }
    finally {
      intervalSpy.mockRestore();
    }
  });

  it('keeps cached shared files visible and exposes retry when a background sync fails', async () => {
    let memoryFiles = [{
      path: 'reports/final.md',
      content: 'final summary',
      updated_by: 'dag-runner',
      updated_at: '2026-05-30T08:00:00Z',
    }];
    const memoryPayload = () => ({
      files: memoryFiles,
      memory: memoryFiles,
      finalOutputRefs: [{ path: 'reports/final.md', runKey: 'run-1', dagKey: 'daily-brief', sourceNodeKey: 'report' }],
      sharedFileRetention: {
        items: [{ path: 'reports/final.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
        protectedCount: 1,
        cleanupCandidateCount: 0,
      },
    });
    backend.listSharedFiles.mockImplementation(() => Promise.resolve(memoryPayload()));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));
    expect(await screen.findByText('final.md')).toBeInTheDocument();

    backend.listSharedFiles.mockRejectedValueOnce(new Error('shared files backend offline'));
    await act(async () => {
      bridgeCallback?.({ type: 'ui/shared-files/changed', payload: { path: 'reports/final.md', action: 'write' } });
      await Promise.resolve();
    });

    expect(screen.getByText('final.md')).toBeInTheDocument();
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('同步共享文件失败，当前显示上次成功数据。');
    expect(alert).not.toHaveTextContent('shared files backend offline');
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'file.dashboard.load', diagnosticId: expect.any(String) }),
    ]));

    memoryFiles = [{
      path: 'scratch/notes.md',
      content: 'fresh notes',
      updated_by: 'agent',
      updated_at: '2026-05-30T09:00:00Z',
    }];
    fireEvent.click(screen.getByRole('button', { name: '重试同步' }));

    expect(await screen.findByText('notes.md')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows a retryable blocking error instead of an empty shared-files state on initial load failure', async () => {
    backend.listSharedFiles.mockRejectedValueOnce(new Error('shared files backend offline'));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('加载共享文件失败，请重试。');
    expect(alert).not.toHaveTextContent('shared files backend offline');
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'file.dashboard.load', diagnosticId: expect.any(String) }),
    ]));
    expect(screen.queryByText('还没有文件产物')).not.toBeInTheDocument();

    backend.listSharedFiles.mockResolvedValueOnce({
      files: [{
        path: 'scratch/notes.md',
        content: 'fresh notes',
        updated_by: 'agent',
        updated_at: '2026-05-30T09:00:00Z',
      }],
      finalOutputRefs: [],
      sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
    });
    fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));

    expect(await screen.findByText('notes.md')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('loads DAG list, detail, runs and selected run through legacy dashboard RPCs', async () => {
    const runningDag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      description: '每日简报',
      status: 'ready',
      trigger: 'manual',
      version: 7,
      latest_run: { run_key: 'run-1', status: 'running', metadata: { final_output: '正在汇总' } },
    };
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags'
        ? {
          dags: [
            runningDag,
            { dag_key: 'weekly-report', title: 'Weekly Report', status: 'ready', trigger: 'scheduled', cron_expr: '0 8 * * 1', next_run_at: '2026-06-01T00:00:00Z' },
            { dag_key: 'done-flow', title: 'Done Flow', status: 'done', trigger: 'manual', latest_run: { run_key: 'run-done', status: 'done' } },
          ],
        }
        : { skills: [] },
    ));
    backend.getDagDetail.mockResolvedValue({
      dag: runningDag,
      nodes: [
        { node_key: 'draft', title: '起草', node_type: 'agent', status: 'running', depends_on: [], config: { provider: 'codex', model: 'gpt-5' } },
      ],
    });
    backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
      runs: status === 'running'
        ? [{ run_key: 'run-1', status: 'running', metadata: { final_output: '正在汇总' } }]
        : [
          { run_key: 'run-1', status: 'running', metadata: { final_output: '正在汇总' } },
          { run_key: 'run-0', status: 'done' },
        ],
    }));
    backend.getDagRun.mockResolvedValue({
      run: { run_key: 'run-1', status: 'running', metadata: { final_output: { text: '最终简报完成' } } },
      nodes: [{ node_key: 'draft', status: 'running' }],
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));

    expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(2);
    expect(screen.getByRole('tab', { name: '进行中 1' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '定时任务 1' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '历史记录 1' })).toBeInTheDocument();
    expect(await screen.findByText('最终简报完成')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();

    await waitFor(() => {
      expect(backend.getDashboardPage).toHaveBeenCalledWith({ cwd: '/repo/app', page: 'dags' });
      expect(backend.getDagDetail).toHaveBeenCalledWith({ dagKey: 'daily-brief' });
      expect(backend.getDagRuns).toHaveBeenCalledWith({ dagKey: 'daily-brief', limit: 30 });
      expect(backend.getDagRuns).toHaveBeenCalledWith({ dagKey: 'daily-brief', status: 'running', limit: 1 });
      expect(backend.getDagRun).toHaveBeenCalledWith({ runKey: 'run-1' });
    });
  });
