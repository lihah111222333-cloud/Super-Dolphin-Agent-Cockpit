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

  it('renders a grouped line-by-line diff instead of raw patch text', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
      },
      diffTextByThread: {
        'thread-1': [
          'diff --git a/src/a.js b/src/a.js',
          '--- a/src/a.js',
          '+++ b/src/a.js',
          '@@ -1 +1,2 @@',
          '-old',
          '+new',
          '+extra',
          'diff --git a/src/b.js b/src/b.js',
          '--- a/src/b.js',
          '+++ b/src/b.js',
          '@@ -4 +4 @@',
          '-removed',
          '+added',
          'diff --git a/docs/notes.md b/docs/notes.md',
          '--- a/docs/notes.md',
          '+++ b/docs/notes.md',
          '@@ -1,0 +1 @@',
          '+note',
        ].join('\n'),
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    const diffView = screen.getByTestId('diff-view');
    const fileGroups = diffView.querySelectorAll('.diff-file-group');
    expect(fileGroups).toHaveLength(3);
    expect(diffView).not.toHaveTextContent('diff --git');

    const firstFile = fileGroups[0];
    expect(within(firstFile).getByRole('button', { name: '折叠 src/a.js' })).toHaveTextContent('+2');
    expect(within(firstFile).getByRole('button', { name: '折叠 src/a.js' })).toHaveTextContent('-1');
    expect(firstFile.querySelector('.diff-line.hunk')).toHaveTextContent('@@ -1 +1,2 @@');
    expect(firstFile.querySelector('.diff-line.del')).toHaveTextContent('old');
    expect(firstFile.querySelector('.diff-line.add')).toHaveTextContent('new');
    expect(firstFile.querySelector('.diff-line.add .diff-line-new')).toHaveTextContent('1');
    expect(firstFile.querySelector('.diff-line.del .diff-line-old')).toHaveTextContent('1');
    expect(firstFile).not.toHaveTextContent('diff --git');
    expect(firstFile).not.toHaveTextContent('--- a/src/a.js');
    expect(firstFile).not.toHaveTextContent('+++ b/src/a.js');

    expect(diffView).toHaveTextContent('src/b.js');
    expect(diffView).toHaveTextContent('docs/notes.md');
    expect(screen.queryByTestId('diff-raw')).not.toBeInTheDocument();
  });

  it('locates, previews and saves runtime diff files through code RPCs', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
      },
      diffTextByThread: {
        'thread-1': [
          'diff --git a/src/a.js b/src/a.js',
          '--- a/src/a.js',
          '+++ b/src/a.js',
          '@@ -1 +1,2 @@',
          '-old',
          '+new',
          '+extra',
        ].join('\n'),
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    fireEvent.click(screen.getByRole('button', { name: '定位 src/a.js' }));

    await waitFor(() => {
      expect(backend.locateCodeFile).toHaveBeenCalledWith({
        filePath: 'src/a.js',
        project: '/repo/app',
        projects: ['/repo/app'],
      });
      expect(screen.getByTestId('runtime-panel')).toHaveTextContent('定位到 1 个路径');
    });

    fireEvent.click(screen.getByRole('button', { name: '打开 src/a.js' }));

    const previewDialog = await screen.findByRole('dialog', { name: '文件预览' });
    expect(backend.openCodeFile).toHaveBeenCalledWith({
      filePath: 'src/a.js',
      project: '/repo/app',
      projects: ['/repo/app'],
    });
    expect(within(previewDialog).getByText('src/a.js')).toBeInTheDocument();

    const previewEditor = within(previewDialog).getByLabelText('文件预览内容');
    expect(previewEditor).toHaveValue('old\nkeep');

    fireEvent.change(previewEditor, { target: { value: 'new\nkeep' } });
    fireEvent.click(within(previewDialog).getByRole('button', { name: '保存预览更改' }));

    await waitFor(() => {
      expect(backend.saveCodeFile).toHaveBeenCalledWith({
        filePath: '/repo/app/src/a.js',
        content: 'new\nkeep',
        previewMode: 'full',
        contentVersion: 'version-src-a',
        project: '/repo/app',
        projects: ['/repo/app'],
      });
      expect(within(previewDialog).getByText('已保存 src/a.js')).toBeInTheDocument();
    });
  });

  it('opens a path choice dialog when runtime diff locate returns multiple matches', async () => {
    backend.locateCodeFile.mockResolvedValueOnce({
      ok: true,
      paths: ['/repo/app/src/a.js', '/repo/app/packages/demo/src/a.js'],
      matches: [
        { path: '/repo/app/src/a.js', relative: 'src/a.js' },
        { path: '/repo/app/packages/demo/src/a.js', relative: 'packages/demo/src/a.js' },
      ],
      truncated: true,
    });
    backend.openCodeFile.mockResolvedValueOnce({
      ok: true,
      filePath: '/repo/app/packages/demo/src/a.js',
      relative: 'packages/demo/src/a.js',
      snippet: [{ line: 1, text: 'chosen file' }],
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
      },
      diffTextByThread: {
        'thread-1': [
          'diff --git a/src/a.js b/src/a.js',
          '--- a/src/a.js',
          '+++ b/src/a.js',
          '@@ -1 +1 @@',
          '-old',
          '+new',
        ].join('\n'),
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    fireEvent.click(screen.getByRole('button', { name: '定位 src/a.js' }));

    const chooser = await screen.findByRole('dialog', { name: '选择文件路径' });
    expect(within(chooser).getByText('/repo/app/src/a.js')).toBeInTheDocument();
    expect(within(chooser).getByText('/repo/app/packages/demo/src/a.js')).toBeInTheDocument();
    expect(within(chooser).getByText('结果已截断，仅显示部分结果')).toBeInTheDocument();

    fireEvent.click(within(chooser).getByRole('button', { name: '/repo/app/packages/demo/src/a.js' }));

    const previewDialog = await screen.findByRole('dialog', { name: '文件预览' });
    expect(backend.openCodeFile).toHaveBeenCalledWith({
      filePath: '/repo/app/packages/demo/src/a.js',
      project: '/repo/app',
      projects: ['/repo/app'],
    });
    expect(within(previewDialog).getByText('packages/demo/src/a.js')).toBeInTheDocument();
    expect(within(previewDialog).getByText('chosen file')).toBeInTheDocument();
    expect(within(previewDialog).queryByLabelText('文件预览内容')).not.toBeInTheDocument();
    expect(within(previewDialog).queryByRole('button', { name: '保存预览更改' })).not.toBeInTheDocument();
  });

  it('renders markdown runtime diff previews and blocks closing dirty edits', async () => {
    backend.openCodeFile.mockResolvedValueOnce({
      ok: true,
      filePath: '/repo/app/docs/readme.md',
      relative: 'docs/readme.md',
      language: 'markdown',
      startLine: 1,
      endLine: 3,
      totalLines: 3,
      previewMode: 'full',
      contentVersion: 'version-docs-readme',
      snippet: '# Guide\n\n- first step',
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
      },
      diffTextByThread: {
        'thread-1': [
          'diff --git a/docs/readme.md b/docs/readme.md',
          '--- a/docs/readme.md',
          '+++ b/docs/readme.md',
          '@@ -1 +1 @@',
          '-old',
          '+new',
        ].join('\n'),
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    fireEvent.click(screen.getByRole('button', { name: '打开 docs/readme.md' }));

    const previewDialog = await screen.findByRole('dialog', { name: '文件预览' });
    expect(within(previewDialog).getByRole('heading', { name: 'Guide' })).toBeInTheDocument();
    expect(within(previewDialog).getByText('first step')).toBeInTheDocument();
    expect(within(previewDialog).queryByLabelText('文件预览内容')).not.toBeInTheDocument();

    fireEvent.click(within(previewDialog).getByRole('button', { name: '编辑预览' }));
    const previewEditor = within(previewDialog).getByLabelText('文件预览内容');
    fireEvent.change(previewEditor, { target: { value: '# Guide\n\nchanged' } });
    fireEvent.click(within(previewDialog).getByRole('button', { name: '关闭文件预览' }));

    expect(screen.getByRole('dialog', { name: '文件预览' })).toBeInTheDocument();
    expect(within(previewDialog).getByRole('alert')).toHaveTextContent('请先保存或放弃预览更改');
  });

  it('renders image runtime diff previews without the text editor', async () => {
    backend.openCodeFile.mockResolvedValueOnce({
      ok: true,
      image: true,
      filePath: '/repo/app/assets/logo.png',
      relative: 'assets/logo.png',
      mediaType: 'image/png',
      previewURL: '/local-image?id=logo_full',
      thumbnailURL: '/local-image?id=logo_thumb',
      sizeBytes: 2048,
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
      },
      diffTextByThread: {
        'thread-1': [
          'diff --git a/assets/logo.png b/assets/logo.png',
          '--- a/assets/logo.png',
          '+++ b/assets/logo.png',
          '@@ -1 +1 @@',
          '-old',
          '+new',
        ].join('\n'),
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    fireEvent.click(screen.getByRole('button', { name: '打开 assets/logo.png' }));

    const previewDialog = await screen.findByRole('dialog', { name: '文件预览' });
    const image = within(previewDialog).getByRole('img', { name: 'assets/logo.png' });
    expect(image).toHaveAttribute('src', '/local-image?id=logo_thumb');
    expect(within(previewDialog).getByText('image/png · 2.0 KB')).toBeInTheDocument();
    expect(within(previewDialog).queryByLabelText('文件预览内容')).not.toBeInTheDocument();
    expect(within(previewDialog).queryByRole('button', { name: '保存预览更改' })).not.toBeInTheDocument();
  });

  it('does not render the removed work status from the backend turn state machine', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'preparing' }],
      tokenUsageByThread: {
        'thread-1': { usedTokens: 128, contextWindowTokens: 1024, usedPercent: 12.5 },
      },
    });

    const { container } = render(<App />);

    await waitForBackendThreadHeading();
    expect(container.querySelector('.work-status')).toBeNull();

    act(() => {
      bridgeCallback({
        type: 'ui/thread/patch',
        payload: {
          threadId: 'thread-1',
          sequence: '1',
          status: 'force_completing',
        },
      });
    });

    expect(container.querySelector('.work-status')).toBeNull();
  });

  it('keeps backend projected thread states out of the removed work status bar', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'idle' }],
    });

    const { container } = render(<App />);

    await waitForBackendThreadHeading();
    expect(container.querySelector('.work-status')).toBeNull();

    for (const [index, status] of [
      'starting',
      'thinking',
      'editing',
      'waiting',
      'syncing',
      'responding',
      'error',
      'archived',
    ].entries()) {
      act(() => {
        bridgeCallback({
          type: 'ui/thread/patch',
          payload: {
            threadId: 'thread-1',
            sequence: `${index + 1}`,
            status,
          },
        });
      });
      expect(container.querySelector('.work-status')).toBeNull();
    }
  });

  it('does not render removed work status details or token chip', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'idle' }],
      tokenUsageByThread: {
        'thread-1': { usedTokens: 21017, contextWindowTokens: 258400, usedPercent: 8.1 },
      },
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: { 'thread-1': [] },
    });

    const { container } = render(<App />);
    await waitForBackendThreadHeading();

    act(() => {
      useClientStore.setState((state) => ({
        statuses: {
          ...state.statuses,
          'thread-1': {
            status: 'idle',
            statusDetails: '��持被跳过，但写入成功|临时文件清理|输出 `scratch_removed`',
          },
        },
      }));
    });

    expect(container.querySelector('.work-status')).toBeNull();
    expect(container).not.toHaveTextContent('持被跳过，但写入成功');
    expect(container).not.toHaveTextContent('21017 / 258400 tokens');
  });

  it('does not expose internal thread identifiers when the work status bar is hidden', async () => {
    const internalId = 'agent_1780284988948557000';
    backend.getSidebarState.mockResolvedValueOnce({
      activeThreadId: internalId,
      threads: [{ id: internalId, name: internalId, provider: 'codex', status: 'idle' }],
      statuses: { [internalId]: 'idle' },
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: internalId,
      timelinesByThread: { [internalId]: [] },
    });

    const { container } = render(<App />);

    await screen.findByRole('button', { name: '新对话' });
    expect(container.querySelector('.work-status')).toBeNull();
    expect(container).not.toHaveTextContent(internalId);
    expect(screen.getAllByRole('button', { name: '新对话' }).length).toBeGreaterThan(0);
  });

  it('shows a lightweight history placeholder when the active thread has no trusted cache', async () => {
    const { container } = render(<App />);
    await waitForBackendThreadHeading();

    act(() => {
      useClientStore.setState((state) => ({
        statuses: { ...state.statuses, 'thread-1': 'idle' },
        threads: state.threads.map((thread) => (
          thread.id === 'thread-1' ? { ...thread, status: 'idle' } : thread
        )),
        timelinesByThread: {
          ...state.timelinesByThread,
          'thread-1': [],
        },
        threadTimelineReadyByThread: {
          ...state.threadTimelineReadyByThread,
          'thread-1': false,
        },
        threadStateLoadingByThread: {
          ...state.threadStateLoadingByThread,
          'thread-1': true,
        },
      }));
    });

    await waitFor(() => {
      expect(screen.getByTestId('timeline-loading-placeholder')).toHaveTextContent('正在同步会话历史');
      expect(container.querySelector('.work-status')).toBeNull();
    });
  });

  it('keeps the existing timeline visible while the active thread state is refreshing', () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'idle' }],
      statuses: { 'thread-1': 'idle' },
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-cached', kind: 'assistant', text: '刷新前已有的回答', ts: '2026-05-30T00:00:00Z' }],
      },
      threadTimelineReadyByThread: { 'thread-1': true },
      threadStateLoadingByThread: { 'thread-1': true },
    });

    const { container } = render(<App skipBootstrap />);

    expect(screen.getByText('刷新前已有的回答')).toBeInTheDocument();
    expect(screen.getByTestId('chat-timeline')).toHaveTextContent('刷新前已有的回答');
    expect(screen.queryByTestId('timeline-loading-placeholder')).not.toBeInTheDocument();
    expect(container.querySelector('.work-status')).toBeNull();
  });

  it('shows AI thinking records with elapsed time in the chat timeline', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'thinking-1',
          kind: 'thinking',
          text: '已探索 4 个文件并运行 2 条命令。',
          done: true,
          ts: '2026-05-30T00:00:00Z',
          completedAt: '2026-05-30T00:06:05Z',
        }, {
          id: 'assistant-after-thinking',
          kind: 'assistant',
          text: '这是整理后的回答。',
          ts: '2026-05-30T00:06:06Z',
        }],
      },
    });

    render(<App />);

    expect(await screen.findByLabelText('AI 思考记录')).toHaveTextContent('已处理 AI 思考 6m 5s');
    expect(screen.getByLabelText('AI 思考记录')).toHaveTextContent('已探索 4 个文件并运行 2 条命令。');
    expect(screen.getByText('这是整理后的回答。')).toBeInTheDocument();
  });

  it('does not invent elapsed time for completed thinking records without an end timestamp', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'thinking-without-end',
          kind: 'thinking',
          text: '完成态缺少结束时间。',
          done: true,
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    const traces = await screen.findAllByLabelText('AI 思考记录');
    const trace = traces.find((node) => node.textContent.includes('完成态缺少结束时间。'));
    expect(trace).toBeTruthy();
    expect(trace).toHaveTextContent('已处理');
    expect(trace).not.toHaveTextContent(/已处理 \d+[sm]/);
  });

  it('does not show noisy zero-second elapsed time for completed thinking records', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'thinking-zero-duration',
          kind: 'thinking',
          text: '完成态小于一秒。',
          done: true,
          ts: '2026-05-30T00:00:00Z',
          completedAt: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    const traces = await screen.findAllByLabelText('AI 思考记录');
    const trace = traces.find((node) => node.textContent.includes('完成态小于一秒。'));
    expect(trace).toBeTruthy();
    expect(trace).toHaveTextContent('已处理');
    expect(trace).not.toHaveTextContent('已处理 0s');
  });

  it('uses numeric unix timestamps for thinking elapsed time instead of dropping them', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'thinking-numeric-time',
          kind: 'thinking',
          text: '使用后端数值时间。',
          done: true,
          ts: 1000,
          completedAt: 1003,
        }],
      },
    });

    render(<App />);

    const traces = await screen.findAllByLabelText('AI 思考记录');
    const trace = traces.find((node) => node.textContent.includes('使用后端数值时间。'));
    expect(trace).toBeTruthy();
    expect(trace).toHaveTextContent('已处理 AI 思考 3s');
  });

  it('uses backend-provided thinking duration when timestamps are incomplete', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'thinking-duration-ms',
          kind: 'thinking',
          text: '使用后端耗时。',
          done: true,
          ts: '2026-05-30T00:00:00Z',
          elapsedMs: 2300,
        }],
      },
    });

    render(<App />);

    const traces = await screen.findAllByLabelText('AI 思考记录');
    const trace = traces.find((node) => node.textContent.includes('使用后端耗时。'));
    expect(trace).toBeTruthy();
    expect(trace).toHaveTextContent('已处理 AI 思考 2s');
  });

  it('shows tool execution details inside the AI processing frame', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'tool-file-read',
          kind: 'tool',
          title: 'file.open',
          status: 'completed',
          text: '读取 frontend-app/src/App.jsx，定位 ReasoningTrace。',
          done: true,
          ts: '2026-05-30T00:00:00Z',
          completedAt: '2026-05-30T00:00:03Z',
        }, {
          id: 'assistant-after-tool',
          kind: 'assistant',
          text: '工具结果已整理。',
          ts: '2026-05-30T00:00:04Z',
        }],
      },
    });

    render(<App />);

    const trace = await screen.findByLabelText('AI 思考记录');
    expect(trace).toHaveClass('reasoning-message');
    expect(trace).not.toHaveClass('message');
    expect(trace).not.toHaveClass('assistant');
    expect(trace).toHaveTextContent('已处理 file.open 3s');
    const step = within(trace).getByLabelText('工具步骤');
    expect(step).toHaveTextContent('读取 frontend-app/src/App.jsx');
    expect(screen.getByText('工具结果已整理。')).toBeInTheDocument();
  });

  it('shows active agent timeline tool cards when timeline state is keyed by agent id', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', agentId: 'agent-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
    });

    render(<App />);

    await screen.findByRole('heading', { name: 'Thread 1' });

    act(() => {
      useClientStore.setState((state) => ({
        activeThreadId: 'thread-1',
        threads: [{ id: 'thread-1', agentId: 'agent-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
        timelinesByThread: {
          ...state.timelinesByThread,
          'agent-1': [{
            id: 'tool-agent-keyed',
            kind: 'tool',
            title: 'file',
            status: 'completed',
            text: 'agent keyed tool result',
            done: true,
            ts: '2026-05-30T00:00:00Z',
          }],
        },
        threadTimelineReadyByThread: {
          ...state.threadTimelineReadyByThread,
          'agent-1': true,
        },
        threadStateLoadingByThread: {},
      }));
    });

    const trace = await screen.findByLabelText('AI 思考记录');
    expect(trace).toHaveTextContent('agent keyed tool result');
  });

  it('hides ghost command timeline cards from the conversation body', async () => {
    render(<App />);

    await waitForBackendThreadHeading();

    act(() => {
      useClientStore.setState((state) => ({
        timelinesByThread: {
          ...state.timelinesByThread,
          'thread-1': [{
            id: 'ghost-command',
            kind: 'command',
            title: '执行命令',
            status: 'completed',
            done: true,
          }, {
            id: 'assistant-after-ghost',
            role: 'assistant',
            kind: 'assistant',
            text: '正常回复',
            time: '2026-05-30T00:00:00Z',
          }],
        },
        threadTimelineReadyByThread: {
          ...state.threadTimelineReadyByThread,
          'thread-1': true,
        },
      }));
    });

    expect(await screen.findByText('正常回复')).toBeInTheDocument();
    expect(screen.queryByText('执行命令')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('AI 思考记录')).not.toBeInTheDocument();
  });
