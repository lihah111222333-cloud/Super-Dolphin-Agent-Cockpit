import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import App from './App.jsx';
import { resetClientStoreForTests } from "./entities/client/model/useClientStore.js";
import { frontendHealthSnapshot } from "./shared/diagnostics/frontendHealthStore.js";
import { normalizeMemorySnapshot as normalizeMemorySnapshotForFacade } from './adapters/memoryAdapter.js';
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
  mockPromptWizardEntryPrompt,
  mockPromptAssetWorkflow,
  openPromptAssetsPage,
  openPromptWizardFromPendingCard,
  editAndDeleteReviewerPrompt,
  handlePendingPromptDraft,
  createGeneratedPromptIntent,
  createSimilaritySnapshots,
  openMemoryCenterWithSimilarity,
  runConsolidationUntilSimilaritiesClear,
  expectSimilarityWarningCleared,
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

  it('wires prompt edit, delete, pending draft, and intent wizard actions without card copy action', async () => {
    mockPromptAssetWorkflow();

    await openPromptAssetsPage();
    await editAndDeleteReviewerPrompt();
    await handlePendingPromptDraft();
    await createGeneratedPromptIntent();
  });

  it('uses the first generated prompt draft option when the backend infers multiple choices', async () => {
    backend.draftPromptIntent.mockResolvedValueOnce({
      requested_kind: 'expert',
      inferred_kind: 'recall',
      drafts: [{
        draft_key: 'intent/recall/generated',
        kind: 'recall',
        scope: 'project',
        status: 'review',
        card: {
          kind: 'recall',
          title: '酒后提醒',
          summary: '阻止酒后继续操作',
          recall_body: '在用户喝酒时提醒停止继续操作。',
          hit_examples: ['我喝酒了还想继续工作'],
          miss_examples: ['普通工作安排'],
        },
        issues: [],
      }],
    });
    backend.commitPromptIntent.mockResolvedValueOnce({ prompt: { id: 'recall/alcohol-guard' } });
    mockPromptWizardEntryPrompt();

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('提示词'));
    const { wizard } = await openPromptWizardFromPendingCard('待确认入口');
    fireEvent.click(within(wizard).getByRole('tab', { name: '专家能力' }));
    fireEvent.change(await screen.findByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: '在我喝酒的时候阻止我' },
    });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));

    expect(await screen.findByText('酒后提醒')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认保存' }));
    await waitFor(() => {
      expect(backend.commitPromptIntent).toHaveBeenCalledWith({ cwd: '/repo/app', draftKey: 'intent/recall/generated', scope: 'project' });
    });
  });

  it('does not submit prompt drafts that still need revision', async () => {
    backend.draftPromptIntent.mockResolvedValueOnce({
      draft_key: 'intent/expert/alcohol-support',
      kind: 'expert',
      scope: 'project',
      status: 'draft',
      card: {
        kind: 'expert',
        title: '想喝酒时给予支持性鼓励',
        summary: '在用户想喝酒时给予支持。',
        output: '温和提醒用户先停下来。',
        hit_examples: ['我想喝酒'],
        miss_examples: ['帮我写代码'],
      },
      issues: [{ code: 'missing_when_not_to_use', severity: 'block', message: '需要补充不用它的场景' }],
    });
    mockPromptWizardEntryPrompt();

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('提示词'));
    await openPromptWizardFromPendingCard('待确认入口');
    fireEvent.change(await screen.findByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: '在我想喝酒的时候鼓励我' },
    });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));

    expect(await screen.findByText('想喝酒时给予支持性鼓励')).toBeInTheDocument();
    expect(screen.getByText('当前草稿含有必须修正的问题，因此暂不能保存；请按下方“需修正”提示补充描述后重新生成。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '确认保存' })).toBeDisabled();
    expect(backend.commitPromptIntent).not.toHaveBeenCalled();
  });

  it('shows user-facing prompt save guidance when the backend rejects an unready draft', async () => {
    backend.draftPromptIntent.mockResolvedValueOnce({
      draft_key: 'intent/expert/alcohol-support',
      kind: 'expert',
      scope: 'project',
      status: 'ready_to_save',
      card: {
        kind: 'expert',
        title: '想喝酒时给予支持性鼓励',
        summary: '在用户想喝酒时给予支持。',
        output: '温和提醒用户先停下来。',
        hit_examples: ['我想喝酒'],
        miss_examples: ['帮我写代码'],
      },
      issues: [],
    });
    backend.commitPromptIntent.mockRejectedValueOnce(new Error('with_tx prompt_template: [-31007] prompt intent draft is not ready to save'));
    mockPromptWizardEntryPrompt();

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('提示词'));
    await openPromptWizardFromPendingCard('待确认入口');
    fireEvent.change(await screen.findByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: '在我想喝酒的时候鼓励我' },
    });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));
    expect(await screen.findByText('想喝酒时给予支持性鼓励')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认保存' }));

    await waitFor(() => {
      expect(screen.getByText('当前草稿含有必须修正的问题，因此暂不能保存；请按下方“需修正”提示补充描述后重新生成。')).toBeInTheDocument();
    });
    expect(screen.getByText('当前草稿含有必须修正的问题，因此暂不能保存；请按下方“需修正”提示补充描述后重新生成。')).not.toHaveClass('error');
    expect(screen.queryByText(/with_tx|31007|not ready to save/i)).not.toBeInTheDocument();
  });

  it('shows generated prompt draft details like the legacy confirmation card', async () => {
    backend.draftPromptIntent.mockResolvedValueOnce({
      draft_key: 'intent/expert/alcohol-support',
      kind: 'expert',
      scope: 'project',
      status: 'draft',
      card: {
        kind: 'expert',
        title: '想喝酒时暂停提醒',
        summary: '在用户表达想喝酒时给予支持。',
        when_to_use: '当用户表达想喝酒、想买酒或可能冲动饮酒时使用。',
        when_not_to_use: '不要用于普通饮食建议或医疗诊断。',
        workflow: ['先接住情绪', '提醒用户暂停饮酒', '建议做一个安全替代行动'],
        save_boundary: '只给出建议，不声称已经保存到记忆。',
        output: '输出一段温和、坚定的提醒，并给出一个可马上执行的替代行动。',
        hit_examples: ['我现在想喝酒'],
        miss_examples: ['推荐一杯咖啡'],
      },
      issues: [{ code: 'missing_when_not_to_use', severity: 'block', message: 'internal field copy' }],
    });
    mockPromptWizardEntryPrompt();

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('提示词'));
    await openPromptWizardFromPendingCard('待确认入口');
    fireEvent.change(await screen.findByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: '在我想喝酒的时候阻止我' },
    });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));

    expect(await screen.findByText('想喝酒时暂停提醒')).toBeInTheDocument();
    expect(screen.getByText('当用户表达想喝酒、想买酒或可能冲动饮酒时使用。')).toBeInTheDocument();
    expect(screen.getByText('不要用于普通饮食建议或医疗诊断。')).toBeInTheDocument();
    expect(screen.getByText('先接住情绪')).toBeInTheDocument();
    expect(screen.getByText('只给出建议，不声称已经保存到记忆。')).toBeInTheDocument();
    expect(screen.getByText('我现在想喝酒')).toBeInTheDocument();
    expect(screen.getByText('推荐一杯咖啡')).toBeInTheDocument();
    expect(screen.getByText('需要说明哪些问题不适合使用它。')).toBeInTheDocument();
    expect(screen.queryByText('internal field copy')).not.toBeInTheDocument();
  });

  it('renders memory create button inside search toolbar', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByRole('button', { name: '记忆中心' }));
    const toolbar = await screen.findByTestId('memory-toolbar');
    expect(toolbar).toBeInTheDocument();
    expect(within(toolbar).getByRole('textbox', { name: '搜索记忆' })).toBeInTheDocument();
    expect(within(toolbar).getByRole('button', { name: /\+ 新建/ })).toBeInTheDocument();
  });

  it('loads memory center through ui/memory/get and groups entries by type', async () => {
    backend.getMemorySnapshot.mockResolvedValue(normalizeMemorySnapshotForFacade({
      overview: {
        enabled: true,
        autoDreamEnabled: false,
        autoDreamIntent: null,
        projectRoot: '/repo/app', writeAvailable: true,
        health: {
          preferenceCount: 1,
          projectCount: 1,
          maxPerCategory: 15,
          similarGroups: [{
            nameA: '遵守 TDD', targetA: 'private', pathA: 'feedback/tdd.md',
            nameB: 'TDD 流程', targetB: 'team', pathB: 'feedback/team-tdd.md',
            score: 0.91,
          }],
        },
      },
      private: {
        entries: [{
          name: 'tdd-rule',
          title: '遵守 TDD',
          description: '先写红测并运行确认。',
          type: 'feedback',
          path: 'feedback/tdd.md',
          updatedAt: '2026-05-30T08:00:00Z',
          preview: '规则\n先写红测',
        }],
      },
      team: {
        entries: [{
          name: 'dag-policy',
          title: 'DAG 规范',
          description: '任务流程要使用 DAG 生命周期。',
          type: 'project',
          path: 'project/dag.md',
          updatedAt: '2026-05-29T08:00:00Z',
          preview: 'DAG 内容',
        }],
      },
    }));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('记忆中心'));

    expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();
    const memoryCard = screen.getByText('遵守 TDD').closest('article');
    expect(within(memoryCard).getByText('偏好')).toBeInTheDocument();
    expect(within(memoryCard).queryByText('私有')).not.toBeInTheDocument();
    expect(within(memoryCard).queryByText('团队')).not.toBeInTheDocument();
    expect(within(memoryCard).queryByText('feedback/tdd.md')).not.toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '偏好 1' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '项目 1' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '全部 2' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();
    expect(screen.getByText('1 组条目内容相似')).toBeInTheDocument();
    expect(backend.getMemorySnapshot).toHaveBeenCalledWith({ cwd: '/repo/app' });

    fireEvent.click(screen.getByRole('tab', { name: '项目 1' }));
    expect(screen.queryByText('遵守 TDD')).not.toBeInTheDocument();
    expect(screen.getByText('DAG 规范')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('搜索记忆'), { target: { value: 'tdd' } });
    expect(screen.queryByText('DAG 规范')).not.toBeInTheDocument();
    expect(screen.getByText('没有匹配的条目')).toBeInTheDocument();
  });

  it('auto-updates memory center without a manual refresh button', async () => {
    let entries = [{
      name: 'tdd-rule',
      title: '遵守 TDD',
      description: '先写红测',
      type: 'feedback',
      path: 'feedback/tdd.md',
      updatedAt: '2026-05-30T08:00:00Z',
      preview: '规则\n先写红测',
    }];
    backend.getMemorySnapshot.mockImplementation(() => Promise.resolve(normalizeMemorySnapshotForFacade({
      overview: {
        enabled: true,
        autoDreamEnabled: true,
        autoDreamIntent: null,
        projectRoot: '/repo/app', writeAvailable: true,
        health: { preferenceCount: entries.length, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
      },
      private: { entries },
      team: { entries: [] },
    })));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('记忆中心'));

    expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();

    entries = [
      ...entries,
      {
        name: 'reply-language',
        title: '默认中文',
        description: '回答时使用中文',
        type: 'feedback',
        path: 'feedback/reply-language.md',
        updatedAt: '2026-05-30T09:00:00Z',
        preview: '默认中文回复',
      },
    ];
    await act(async () => {
      bridgeCallback?.({ type: 'ui/memory/changed', payload: { action: 'upsert' } });
    });
    expect(await screen.findByText('默认中文')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '偏好 2' })).toBeInTheDocument();

    entries = [
      ...entries,
      {
        name: 'review-style',
        title: '审查风格',
        description: '先列风险',
        type: 'feedback',
        path: 'feedback/review-style.md',
        updatedAt: '2026-05-30T09:01:00Z',
        preview: '先列风险',
      },
    ];
    await act(async () => {
      window.dispatchEvent(new Event('focus'));
    });
    expect(await screen.findByText('审查风格')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '偏好 3' })).toBeInTheDocument();
  });

  it('does not poll memory center with a page interval', async () => {
    const intervalSpy = vi.spyOn(window, 'setInterval');
    try {
      backend.getMemorySnapshot.mockResolvedValue(normalizeMemorySnapshotForFacade({
        overview: {
          enabled: true,
          autoDreamEnabled: true,
          autoDreamIntent: null,
          projectRoot: '/repo/app', writeAvailable: true,
          health: { preferenceCount: 1, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
        },
        private: {
          entries: [{
            name: 'tdd-rule',
            title: '遵守 TDD',
            description: '先写红测',
            type: 'feedback',
            path: 'feedback/tdd.md',
            updatedAt: '2026-05-30T08:00:00Z',
            preview: '规则\n先写红测',
          }],
        },
        team: { entries: [] },
      }));

      render(<App />);
      await waitForBackendThreadHeading();
      fireEvent.click(screen.getByLabelText('记忆中心'));

      expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();
      expect(intervalSpy.mock.calls.filter((call) => call[1] === 4000)).toHaveLength(0);
    }
    finally {
      intervalSpy.mockRestore();
    }
  });

  it('keeps cached memory entries visible and exposes retry when a background sync fails', async () => {
    let entries = [{
      name: 'tdd-rule',
      title: '遵守 TDD',
      description: '先写红测',
      type: 'feedback',
      path: 'feedback/tdd.md',
      updatedAt: '2026-05-30T08:00:00Z',
      preview: '规则\n先写红测',
    }];
    backend.getMemorySnapshot.mockImplementation(() => Promise.resolve(normalizeMemorySnapshotForFacade({
      overview: {
        enabled: true,
        autoDreamEnabled: true,
        autoDreamIntent: null,
        projectRoot: '/repo/app', writeAvailable: true,
        health: { preferenceCount: entries.length, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
      },
      private: { entries },
      team: { entries: [] },
    })));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('记忆中心'));
    expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();

    backend.getMemorySnapshot.mockRejectedValueOnce(new Error('memory backend offline'));
    await act(async () => {
      bridgeCallback?.({ type: 'ui/memory/changed', payload: { action: 'upsert' } });
      await Promise.resolve();
    });

    expect(screen.getByText('遵守 TDD')).toBeInTheDocument();
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('同步记忆失败，当前显示上次成功数据。');
    expect(alert).not.toHaveTextContent('memory backend offline');
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'memory.dashboard.load', diagnosticId: expect.any(String) }),
    ]));

    entries = [{
      name: 'review-style',
      title: '审查风格',
      description: '先列风险',
      type: 'feedback',
      path: 'feedback/review-style.md',
      updatedAt: '2026-05-30T09:01:00Z',
      preview: '先列风险',
    }];
    fireEvent.click(screen.getByRole('button', { name: '重试同步' }));

    expect(await screen.findByText('审查风格')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows a retryable blocking error instead of an empty memory state on initial load failure', async () => {
    let failMemory = true;
    backend.getMemorySnapshot.mockImplementation(() => {
      if (failMemory) return Promise.reject(new Error('memory backend offline'));
      return Promise.resolve(normalizeMemorySnapshotForFacade({
        overview: {
          enabled: true,
          autoDreamEnabled: true,
          autoDreamIntent: null,
          projectRoot: '/repo/app', writeAvailable: true,
          health: { preferenceCount: 1, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
        },
        private: {
          entries: [{
            name: 'review-style',
            title: '审查风格',
            description: '先列风险',
            type: 'feedback',
            path: 'feedback/review-style.md',
            updatedAt: '2026-05-30T09:01:00Z',
            preview: '先列风险',
          }],
        },
        team: { entries: [] },
      }));
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('记忆中心'));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('读取记忆失败，请重试。');
    expect(alert).not.toHaveTextContent('memory backend offline');
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'memory.dashboard.load', diagnosticId: expect.any(String) }),
    ]));
    expect(screen.queryByText('暂无记忆')).not.toBeInTheDocument();

    failMemory = false;
    fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));

    expect(await screen.findByText('审查风格')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('keeps cached memory entries visible when navigating back and refreshes silently', async () => {
    let entries = [{
      name: 'tdd-rule',
      title: '遵守 TDD',
      description: '先写红测',
      type: 'feedback',
      path: 'feedback/tdd.md',
      updatedAt: '2026-05-30T08:00:00Z',
      preview: '规则\n先写红测',
    }];
    backend.getMemorySnapshot.mockImplementation(() => Promise.resolve(normalizeMemorySnapshotForFacade({
      overview: {
        enabled: true,
        autoDreamEnabled: true,
        autoDreamIntent: null,
        projectRoot: '/repo/app', writeAvailable: true,
        health: { preferenceCount: entries.length, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
      },
      private: { entries },
      team: { entries: [] },
    })));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('记忆中心'));
    expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('新对话'));
    entries = [{
      name: 'reply-language',
      title: '默认中文',
      description: '回答时使用中文',
      type: 'feedback',
      path: 'feedback/reply-language.md',
      updatedAt: '2026-05-30T09:00:00Z',
      preview: '默认中文回复',
    }];
    fireEvent.click(screen.getByLabelText('记忆中心'));

    expect(screen.queryByText('正在加载记忆中心...')).not.toBeInTheDocument();
    expect(screen.getByText('遵守 TDD')).toBeInTheDocument();
    expect(await screen.findByText('默认中文')).toBeInTheDocument();
    expect(screen.queryByText('遵守 TDD')).not.toBeInTheDocument();
  });

  it('wires memory center mutation actions to backend RPCs', async () => {
    backend.getMemorySnapshot.mockResolvedValue(normalizeMemorySnapshotForFacade({
      overview: {
        enabled: true,
        autoDreamEnabled: false,
        autoDreamIntent: null,
        projectRoot: '/repo/app', writeAvailable: true,
        health: { preferenceCount: 1, projectCount: 0, maxPerCategory: 15, similarGroups: [] },
      },
      private: {
        entries: [{
          name: 'tdd-rule',
          title: '遵守 TDD',
          description: '先写红测',
          type: 'feedback',
          path: 'feedback/tdd.md',
          updatedAt: '2026-05-30T08:00:00Z',
          preview: '规则\n先写红测',
        }],
      },
      team: { entries: [] },
    }));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('记忆中心'));
    expect(await screen.findByText('遵守 TDD')).toBeInTheDocument();

	fireEvent.click(screen.getByRole('button', { name: '开启' }));
	await waitFor(() => {
		expect(backend.setMemoryAutoDreamIntent).toHaveBeenCalledWith({ cwd: '/repo/app', enabled: true });
	});

    fireEvent.click(screen.getByRole('button', { name: '+ 新建 ▾' }));
    fireEvent.click(screen.getByRole('menuitem', { name: '新建偏好' }));
    const createEditor = await screen.findByRole('dialog', { name: '新建记忆' });
    expect(within(createEditor).getByLabelText('分类')).toHaveValue('feedback');
    expect(within(createEditor).queryByLabelText('目标')).not.toBeInTheDocument();
    expect(within(createEditor).queryByLabelText('标识名')).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('描述'), { target: { value: '回复时使用中文' } });
    fireEvent.change(screen.getByLabelText('内容'), { target: { value: '规则\n默认中文回复' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    await waitFor(() => {
      expect(backend.upsertMemoryEntry).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        target: 'private',
        name: expect.stringMatching(/^feedback-/),
        description: '回复时使用中文',
        type: 'feedback',
        content: '规则\n默认中文回复',
      }));
    });

    const card = screen.getByText('遵守 TDD').closest('article');
    fireEvent.click(within(card).getByRole('button', { name: '编辑' }));
    await waitFor(() => {
      expect(backend.getMemoryEntry).toHaveBeenCalledWith({ cwd: '/repo/app', target: 'private', path: 'feedback/tdd.md' });
    });
    const editor = await screen.findByRole('dialog', { name: '编辑记忆' });
    expect(within(editor).queryByRole('button', { name: '关闭' })).not.toBeInTheDocument();
    expect(within(editor).getByLabelText('分类')).toHaveValue('feedback');
    expect(within(editor).queryByLabelText('目标')).not.toBeInTheDocument();
    expect(within(editor).queryByLabelText('标识名')).not.toBeInTheDocument();
    expect(await screen.findByDisplayValue('先写红测')).toBeInTheDocument();
    fireEvent.click(within(editor).getByRole('button', { name: '取消' }));

    fireEvent.click(within(card).getByRole('button', { name: '删除' }));
    const deleteDialog = await screen.findByRole('dialog', { name: '删除记忆' });
    expect(deleteDialog).toBeInTheDocument();
    expect(within(deleteDialog).queryByText('private')).not.toBeInTheDocument();
    expect(within(deleteDialog).queryByText('feedback/tdd.md')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认删除' }));
    await waitFor(() => {
      expect(backend.deleteMemoryEntry).toHaveBeenCalledWith({ cwd: '/repo/app', target: 'private', path: 'feedback/tdd.md' });
    });
  });

  it('wires memory similarity actions to backend RPCs', async () => {
    backend.getMemorySnapshot.mockResolvedValue(normalizeMemorySnapshotForFacade({
      overview: {
        enabled: true,
        autoDreamEnabled: true,
        projectRoot: '/repo/app', writeAvailable: true,
        health: {
          preferenceCount: 2,
          projectCount: 0,
          maxPerCategory: 15,
          similarGroups: [{
            nameA: 'A', targetA: 'private', pathA: 'feedback/a.md',
            nameB: 'B', targetB: 'team', pathB: 'feedback/b.md',
            score: 0.88,
          }],
        },
      },
      private: { entries: [] },
      team: { entries: [] },
    }));

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('记忆中心'));
    expect(await screen.findByText('1 组条目内容相似')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '展开' }));
    fireEvent.click(screen.getByRole('button', { name: '整合' }));
    const mergeDialog = await screen.findByRole('dialog', { name: '整合相似记忆' });
    expect(mergeDialog).toBeInTheDocument();
    expect(within(mergeDialog).queryByText('private')).not.toBeInTheDocument();
    expect(within(mergeDialog).queryByText('team')).not.toBeInTheDocument();
    expect(within(mergeDialog).queryByText('feedback/a.md')).not.toBeInTheDocument();
    expect(within(mergeDialog).queryByText('feedback/b.md')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认整合' }));
    await waitFor(() => {
      expect(backend.mergeMemoryEntries).toHaveBeenCalledWith({
        cwd: '/repo/app', targetA: 'private', pathA: 'feedback/a.md', targetB: 'team', pathB: 'feedback/b.md',
      });
    });
    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: '整合相似记忆' })).not.toBeInTheDocument();
    });

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '一键整合全部' })).not.toBeDisabled();
    });
    backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
      'settings.provider.active': 'codex',
      'settings.provider.codex.model': 'gpt-5.4',
      'settings.provider.codex.effort': 'xhigh',
      'settings.provider.codex.codexHome': '~/.codex',
      'settings.provider.codex.codexInstanceKey': 'default',
      'settings.provider.codex.codexModelProvider': 'openai',
    }[key] ?? null));
    fireEvent.click(screen.getByRole('button', { name: '一键整合全部' }));
    await waitFor(() => {
      expect(backend.startConsolidateMemorySimilarities).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        provider: 'codex',
        model: 'gpt-5.4',
        codexModelProvider: 'openai',
      }));
    });
    await waitFor(() => {
      expect(backend.getMemoryConsolidationStatus).toHaveBeenCalledWith({ cwd: '/repo/app', jobId: 'memory-job-1' });
    });
    await waitFor(() => {
      expect(screen.getByRole('button', { name: '忽略' })).not.toBeDisabled();
    });

    fireEvent.click(screen.getByRole('button', { name: '忽略' }));
    await waitFor(() => {
      expect(backend.ignoreMemorySimilarity).toHaveBeenCalledWith({
        cwd: '/repo/app', targetA: 'private', pathA: 'feedback/a.md', targetB: 'team', pathB: 'feedback/b.md',
      });
    });
  });

  it('simulates one-click memory consolidation and clears similarity warnings after refresh', async () => {
    const { snapshotWithSimilar, snapshotWithoutSimilar } = createSimilaritySnapshots();
    let hasSimilar = true;
    backend.getMemorySnapshot.mockImplementation(() => Promise.resolve(hasSimilar ? snapshotWithSimilar : snapshotWithoutSimilar));
    backend.startConsolidateMemorySimilarities.mockResolvedValue({ jobId: 'memory-job-live', status: 'running' });
    backend.getMemoryConsolidationStatus
      .mockResolvedValueOnce({ jobId: 'memory-job-live', status: 'running' })
      .mockResolvedValueOnce({
        jobId: 'memory-job-live',
        status: 'succeeded',
        result: { merged: 1, ignored: 0, failed: 0, skipped: 0 },
      });

    await openMemoryCenterWithSimilarity();
    await runConsolidationUntilSimilaritiesClear(() => {
      hasSimilar = false;
    });

    await waitFor(() => {
      expectSimilarityWarningCleared();
    });
    expect(backend.getMemorySnapshot).toHaveBeenLastCalledWith({ cwd: '/repo/app' });
  });
