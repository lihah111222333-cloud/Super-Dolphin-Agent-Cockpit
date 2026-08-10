import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import App from './App.jsx';
import { rightPanelWidthSchema } from './app/shell/model/shellLayoutSchema.js';
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
  dispatchPointer,
  deferred,
  waitForBackendThreadHeading,
  createShellLayoutStorage,
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

  it('coalesces running and completed lifecycle events for the same tool call', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'tool-file-running',
          kind: 'tool',
          title: 'file',
          status: 'running',
          call_id: 'call-file-1',
          done: false,
          ts: '2026-05-30T00:00:00Z',
        }, {
          id: 'tool-file-completed',
          kind: 'tool',
          title: 'file',
          status: 'completed',
          call_id: 'call-file-1',
          text: '{\n  "success": true\n}',
          done: true,
          ts: '2026-05-30T00:00:00Z',
          completedAt: '2026-05-30T00:00:01Z',
        }],
      },
    });

    render(<App />);

    const traces = await screen.findAllByLabelText('AI 思考记录');
    const fileTraces = traces.filter((node) => node.textContent.includes('success'));
    expect(fileTraces).toHaveLength(1);
    expect(fileTraces[0]).toHaveTextContent('已处理 file 1s');
    expect(fileTraces[0]).toHaveTextContent('"success": true');
    expect(within(fileTraces[0]).getByLabelText('工具步骤')).toHaveTextContent('"success": true');
    expect(fileTraces[0]).not.toHaveTextContent('正在调用工具并等待返回结果。');
  });

  it('does not append a pending thinking placeholder after completed processing activity', async () => {
    resetClientStoreForTests({
      bootstrapStatus: 'ready',
      cwd: '/repo/app',
      activeProject: '/repo/app',
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'running' }],
      activeTurnByThread: {
        'thread-1': { id: 'turn-running', threadId: 'thread-1', status: 'running', startedAt: '2026-05-30T00:00:00Z' },
      },
      timelinesByThread: {
        'thread-1': [{
          id: 'user-waiting',
          role: 'user',
          kind: 'user',
          text: '请生成架构图',
          time: '2026-05-30T00:00:00Z',
        }, {
          id: 'tool-file-completed',
          role: 'assistant',
          kind: 'tool',
          title: 'file',
          status: 'completed',
          text: '读取文件完成。',
          done: true,
          time: '2026-05-30T00:00:01Z',
          completedAt: '2026-05-30T00:00:02Z',
        }],
      },
      threadTimelineReadyByThread: { 'thread-1': true },
      threadStateLoadingByThread: {},
    });

    render(<App skipBootstrap />);

    await act(async () => {
      await Promise.resolve();
    });
    const traces = screen.getAllByLabelText('AI 思考记录');
    expect(traces).toHaveLength(1);
    expect(traces[0]).toHaveTextContent('读取文件完成。');
    expect(traces[0]).not.toHaveTextContent('正在处理请求');
  });

  it('renders AI execution plans as checklist details in the processing frame', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'plan-1',
          kind: 'plan',
          title: '执行计划',
          status: 'running',
          done: false,
          text: [
            '并行审查前端和后端代码',
            '✅ 1. 读取当前前端代码',
            '🔄 2. 修复项目选择器重复展示',
            '⏳ 3. 隐藏注入提示词',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    const plan = await screen.findByLabelText('AI 执行计划');
    expect(plan).toHaveTextContent('执行计划');
    expect(plan).toHaveTextContent('已完成 1/3 项任务');
    expect(within(plan).getByText('读取当前前端代码')).toBeInTheDocument();
    expect(within(plan).getByText('修复项目选择器重复展示')).toBeInTheDocument();
    expect(within(plan).getByText('隐藏注入提示词')).toBeInTheDocument();
    const list = within(plan).getByRole('list');
    expect(list.tagName).toBe('OL');
    expect(list).toHaveClass('execution-plan-list');
    const items = within(list).getAllByRole('listitem');
    expect(items).toHaveLength(3);
    expect(items[0]).toHaveAttribute('data-plan-status', 'done');
    expect(items[1]).toHaveAttribute('data-plan-status', 'pending');
  });

  it('shows an active thinking placeholder while a turn is running before output arrives', async () => {
    backend.getSidebarState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'running' }],
      active_turn: { id: 'turn-running', thread_id: 'thread-1', status: 'running', started_at: '2026-05-30T00:00:00Z' },
    });
    backend.getThreadState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'user-waiting', kind: 'user', text: '请生成架构图', ts: '2026-05-30T00:00:00Z' }],
      },
    });

    render(<App />);

    expect(await screen.findByLabelText('AI 思考记录')).toHaveTextContent(/正在思考 \d+[sm]/);
    expect(screen.getByLabelText('AI 思考记录')).toHaveTextContent('AI 正在分析上下文、选择工具并整理回答。');
  });

  it('shows a non-timed preparation status before the first real turn starts', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });
    backend.startThread.mockResolvedValue({ thread: { id: 'thread-new' } });
    const startTurnDeferred = deferred();
    backend.startTurn.mockReturnValue(startTurnDeferred.promise);

    render(<App />);

    await screen.findByText('我们应该在 Super Dolphin Agent 中构建什么？');
    fireEvent.change(screen.getByTestId('composer-input'), {
      target: { value: '请真正调用后端聊天' },
    });
    fireEvent.click(screen.getByLabelText('发送消息'));

    await waitFor(() => expect(backend.startTurn).toHaveBeenCalled());
    const preparingTrace = screen.getByLabelText('AI 思考记录');
    expect(preparingTrace).toHaveTextContent('正在准备响应');
    expect(preparingTrace).not.toHaveTextContent('正在思考');
    expect(preparingTrace).not.toHaveTextContent('0s');

    act(() => {
      bridgeCallback({
        type: 'ui/thread/patch',
        payload: {
          threadId: 'thread-new',
          sequence: '1',
          activeTurn: {
            id: 'turn-live',
            threadId: 'thread-new',
            status: 'running',
            startedAt: '2026-05-30T00:00:00Z',
          },
        },
      });
    });

    await waitFor(() => expect(screen.getByLabelText('AI 思考记录')).toHaveTextContent(/正在思考 \d+[sm]/));

    await act(async () => {
      startTurnDeferred.resolve({ ok: true });
      await Promise.resolve();
    });
  });

  it('updates active thinking elapsed time in place every second', async () => {
    await import('./pages/chat/ChatPage.jsx').then(() => vi.useFakeTimers());
    try {
      vi.setSystemTime(new Date('2026-05-30T00:00:00Z'));
      resetClientStoreForTests({
        bootstrapStatus: 'ready',
        cwd: '/repo/app',
        activeProject: '/repo/app',
        activeThreadId: 'thread-1',
        timelinesByThread: {
          'thread-1': [{
            id: 'thinking-live',
            role: 'assistant',
            kind: 'thinking',
            title: 'grep',
            text: '正在搜索。',
            time: '2026-05-30T00:00:00Z',
            done: false,
          }],
        },
      });

      render(<App skipBootstrap />);

      await act(async () => {
        await Promise.resolve();
        await Promise.resolve();
      });

      const trace = screen.getByLabelText('AI 思考记录');
      expect(trace).toHaveTextContent('正在思考 0s');

      await act(async () => {
        await vi.advanceTimersByTimeAsync(2100);
      });

      expect(trace).toHaveTextContent('正在思考 2s');
    }
    finally {
      vi.useRealTimers();
    }
  });

  it('renders runtime tool details with long names in a shrink-safe structure', async () => {
    const longToolName = 'mcp__very_long_server_name_that_would_overflow__deeply_nested_tool_name_with_many_segments';
    backend.getSidebarState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'running' }],
      activityStatsByThread: {
        'thread-1': {
          toolCalls: { [longToolName]: 3 },
        },
      },
    });

    const { container } = render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    const toolStat = screen.getByRole('button', { name: '工具调用总数' });
    expect(toolStat).not.toHaveAttribute('title');
    fireEvent.mouseEnter(toolStat);
    expect(screen.queryByTestId('runtime-stat-tooltip')).not.toBeInTheDocument();
    fireEvent.click(toolStat);

    const tooltip = await screen.findByTestId('runtime-stat-tooltip');
    expect(tooltip).toHaveTextContent('deeply_nested_tool_name_with_many_segments');
    expect(tooltip.querySelector('.runtime-stat-tooltip-row')).toBeInTheDocument();
    expect(tooltip.querySelector('.runtime-stat-tooltip-name')).not.toHaveAttribute('title');
    expect(container.querySelector('.runtime-panel')).toHaveClass('runtime-panel');
  });

  it('sets the chat composer textarea to three visible rows', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    const composer = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    expect(composer).toHaveAttribute('rows', '3');
    expect(composer).toHaveAttribute('placeholder', '随心输入');
  });

  it('does not render a desktop titlebar inside the workbench shell', async () => {
    const { container } = render(<App />);

    expect(await waitForBackendThreadHeading()).toBeInTheDocument();
    expect(container.querySelector('.traffic-lights')).toBeNull();
    expect(container.querySelectorAll('.titlebar')).toHaveLength(0);
    expect(within(screen.getByTestId('app-sidebar')).getByText('Super Dolphin Agent')).toBeInTheDocument();
    expect(screen.getByTestId('super-dolphin-agent-brand-light-logo')).toBeInTheDocument();
    expect(screen.getByTestId('super-dolphin-agent-brand-dark-logo')).toBeInTheDocument();
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '新对话' }).querySelector('.lucide-plus')).toBeInTheDocument();
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '聊天页面' }).querySelector('.lucide-message-square-text')).toBeInTheDocument();
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '自动化' }).querySelector('.lucide-sliders-horizontal')).toBeInTheDocument();
    expect(within(screen.getByTestId('app-sidebar')).getByRole('button', { name: '链路追踪' }).querySelector('.lucide-database')).toBeInTheDocument();
  });

  it('keeps the user message visible and calls thread/start before turn/start for a new chat', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });
    backend.startThread.mockResolvedValue({ thread: { id: 'thread-new' } });
    backend.startTurn.mockResolvedValue({ ok: true });

    render(<App />);

    await screen.findByText('我们应该在 Super Dolphin Agent 中构建什么？');
    expect(screen.queryByTestId('composer-project')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('发送权限')).not.toBeInTheDocument();
    fireEvent.change(screen.getByTestId('composer-input'), {
      target: { value: '请真正调用后端聊天' },
    });
    fireEvent.click(screen.getByLabelText('发送消息'));

    await waitFor(() => {
      expect(backend.startThread).toHaveBeenCalledBefore(backend.startTurn);
      expect(backend.startTurn).toHaveBeenCalledWith({
        cwd: '/repo/app',
        threadId: 'thread-new',
        input: [{ type: 'text', text: '请真正调用后端聊天' }],
        manualSkillSelection: false,
      });
    });
    const startPayload = backend.startThread.mock.calls[0][0];
    expect(startPayload).not.toHaveProperty('prompt');
    expect(startPayload).not.toHaveProperty('optimisticUserMessage');
    expect(startPayload).not.toHaveProperty('skipInitialRuntimeSync');
    expect(startPayload.config).toEqual({
      codexHome: '~/.codex',
      codexInstanceKey: 'default',
      codexModelProvider: 'openai',
    });

    expect(screen.getAllByText('请真正调用后端聊天').length).toBeGreaterThanOrEqual(1);
  });

  it('renders the inherited timeline used by fork drafts', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [
          { id: 'user-1', kind: 'user', text: '原始需求：补齐工作台能力' },
          { id: 'assistant-1', kind: 'assistant', text: '阶段结论：先迁移 fork draft 链路' },
        ],
      },
    });

    render(<App />);

    expect(await screen.findByText('阶段结论：先迁移 fork draft 链路')).toBeInTheDocument();
  });

  it('opens a fork draft card from the chat composer and submits an inherited thread', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [
          { id: 'user-1', kind: 'user', text: '原始需求：补齐工作台能力' },
          { id: 'assistant-1', kind: 'assistant', text: '阶段结论：先迁移 fork draft 链路' },
        ],
      },
    });
    backend.listSharedFiles.mockResolvedValue({
      files: [{ path: 'reports/final.md' }],
      finalOutputRefs: [],
      sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
    });
    backend.forkThread.mockResolvedValue({
      thread: { id: 'thread-fork', forkedFrom: 'thread-1' },
      kickoffState: 'created_only',
    });
    backend.startTurn.mockResolvedValue({ ok: true });

    render(<App />);

    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByRole('button', { name: '聊天操作' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: '继承当前对话' }));

    const card = await screen.findByTestId('fork-draft-card');
    expect(card).toHaveTextContent('继承自会话：后端线程');
    fireEvent.click(within(card).getByLabelText('选择共享文件 reports/final.md'));
    fireEvent.click(within(card).getByRole('button', { name: '创建继承对话' }));

    await waitFor(() => {
      expect(backend.forkThread).toHaveBeenCalledWith({ threadId: 'thread-1' });
    });
    expect(backend.startThread).not.toHaveBeenCalled();
    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-fork',
      input: [
        { type: 'text', text: '请基于已继承的完整对话历史，简要总结当前进展并提出下一步建议。' },
        {
          type: 'filecontent',
          path: 'reports/final.md',
          name: 'reports/final.md',
          content: 'content for reports/final.md',
        },
      ],
      manualSkillSelection: false,
    });
  });

  it('opens a fork draft from the context usage warning banner', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中' }],
      active_turn: { id: 'turn-1', thread_id: 'thread-1', status: 'running' },
      tokenUsageByThread: {
        'thread-1': { usedTokens: 920, contextWindowTokens: 1000, usedPercent: 92 },
      },
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [
          { id: 'user-1', kind: 'user', text: '上下文快满了' },
          { id: 'assistant-1', kind: 'assistant', text: '建议新建继承会话' },
        ],
      },
    });
    backend.listSharedFiles.mockResolvedValue({
      files: [{ path: 'reports/final.md' }],
      finalOutputRefs: [],
      sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
    });

    render(<App />);

    await screen.findByText('建议新建继承会话');
    const banner = await screen.findByTestId('context-usage-banner');
    expect(banner.tagName).toBe('OUTPUT');
    expect(banner).toHaveTextContent('上下文使用率');
    expect(banner).toHaveTextContent('92%');
    fireEvent.click(within(banner).getByRole('button', { name: '新建继承会话' }));

    const card = await screen.findByTestId('fork-draft-card');
    expect(card).toHaveTextContent('继承自会话：后端线程');
  });

  it('sends the composer draft when plain Enter is pressed inside the textarea', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: 'idle', cwd: '/repo/app' }],
    });
    backend.startTurn.mockResolvedValue({ ok: true });
    render(<App />);

    await waitForBackendThreadHeading();
    const input = screen.getByTestId('composer-input');
    fireEvent.change(input, {
      target: { value: '普通 Enter 发送' },
    });

    expect(fireEvent.keyDown(input, { key: 'Enter', code: 'Enter', isComposing: false })).toBe(false);

    expect(backend.startThread).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(backend.startTurn).toHaveBeenCalledWith({
        cwd: '/repo/app',
        threadId: 'thread-1',
        input: [{ type: 'text', text: '普通 Enter 发送' }],
        manualSkillSelection: false,
      });
    });
  });

  it('does not send the composer draft when Enter confirms IME composition', async () => {
    render(<App />);

    await waitForBackendThreadHeading();
    const input = screen.getByTestId('composer-input');
    fireEvent.change(input, {
      target: { value: '拼音候选' },
    });

    expect(fireEvent.keyDown(input, {
      key: 'Process',
      code: 'Enter',
      keyCode: 229,
      which: 229,
      isComposing: true,
    })).toBe(true);

    expect(backend.startTurn).not.toHaveBeenCalled();
    expect(input).toHaveValue('拼音候选');
  });

  it('floats the composer in the intro state and docks it after the first message', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });
    backend.startThread.mockResolvedValue({ thread: { id: 'thread-new' } });
    backend.startTurn.mockResolvedValue({ ok: true });

    const { container } = render(<App />);

    await screen.findByText('我们应该在 Super Dolphin Agent 中构建什么？');
    expect(screen.getByTestId('composer-dock')).toHaveClass('composer', 'composer--floating');
    expect(screen.getByTestId('chat-timeline')).toContainElement(screen.getByTestId('composer-dock'));
    expect(container.querySelector('.work-status')).toBeNull();

    fireEvent.change(screen.getByTestId('composer-input'), {
      target: { value: '让输入框下沉到底部' },
    });
    fireEvent.click(screen.getByLabelText('发送消息'));

    await waitFor(() => {
      expect(screen.getByTestId('composer-dock')).toHaveClass('composer', 'composer--docked');
    });
    expect(screen.getByTestId('composer-dock')).not.toHaveClass('composer--floating');
    expect(screen.getByTestId('chat-timeline')).not.toContainElement(screen.getByTestId('composer-dock'));
    expect(container.querySelector('.work-status')).toBeNull();
  });

  it('starts with only the chat rail and conversation, then toggles the right sidebar from the toolbar', async () => {
    const { container } = render(<App />);
    await waitForBackendThreadHeading();
    const layout = screen.getByTestId('chat-layout');

    expect(screen.queryByTestId('runtime-panel')).not.toBeInTheDocument();
    expect(screen.queryByTestId('right-panel-resizer')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '显示侧边栏' })).toBeInTheDocument();
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 0px 0px' });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    expect(screen.getByTestId('runtime-panel')).toBeInTheDocument();
    expect(screen.getByTestId('right-panel-resizer')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '隐藏侧边栏' })).toBeInTheDocument();
    expect(within(container.querySelector('.runtime-panel')).getByRole('button', { name: '折叠 file' })).toBeInTheDocument();
    expect(container.querySelector('.runtime-panel')).not.toHaveTextContent('diff --git a/file b/file');
    expect(screen.getByRole('list', { name: '工具调用统计' })).toBeInTheDocument();
    expect(screen.queryByTestId('warning-log-panel')).not.toBeInTheDocument();
    const restoredRightPanelWidth = Number(screen.getByTestId('right-panel-resizer').getAttribute('aria-valuenow'));
    expect(layout).toHaveStyle({
      gridTemplateColumns: `minmax(0, 1fr) 6px ${restoredRightPanelWidth}px`,
    });
  });

  it('supports keyboard resizing for chat and activity resizer controls', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1400 });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 640 });
    const storage = createShellLayoutStorage(String(rightPanelWidthSchema.initialValue));

    render(<App shellLayoutStorage={storage} />);

    await waitForBackendThreadHeading();
    const layout = screen.getByTestId('chat-layout');
    fireEvent.click(screen.getByRole('button', { name: '打开工作台' }));
    const leftResizer = screen.getByRole('separator', { name: '调整工作台侧栏宽度' });
    expect(leftResizer.tagName).toBe('BUTTON');

    expect(leftResizer).toHaveAttribute('aria-valuenow', '340');

    fireEvent.keyDown(leftResizer, { key: 'ArrowLeft' });

    expect(leftResizer).toHaveAttribute('aria-valuenow', '324');
    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 0px 0px' });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    let rightResizer = screen.getByRole('separator', { name: '调整侧边栏宽度' });
    expect(rightResizer.tagName).toBe('BUTTON');

    const rightPanelMaximum = Number(rightResizer.getAttribute('aria-valuemax'));
    const restoredWidth = Number(rightResizer.getAttribute('aria-valuenow'));
    expect(rightResizer).toHaveAttribute('aria-valuenow', String(restoredWidth));
    expect(storage.value()).toBe(String(rightPanelWidthSchema.initialValue));

    fireEvent.keyDown(rightResizer, { key: 'ArrowLeft' });

    const arrowWidth = Number(rightResizer.getAttribute('aria-valuenow'));
    expect(arrowWidth).toBeGreaterThan(restoredWidth);
    expect(storage.value()).toBe(String(arrowWidth));
    expect(layout).toHaveStyle({ gridTemplateColumns: `minmax(0, 1fr) 6px ${arrowWidth}px` });

    fireEvent.keyDown(rightResizer, { key: 'Home' });

    expect(storage.value()).toBe('0');
    await waitFor(() => expect(screen.queryByTestId('runtime-panel')).not.toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    rightResizer = screen.getByRole('separator', { name: '调整侧边栏宽度' });
    const defaultWidth = Number(rightResizer.getAttribute('aria-valuenow'));
    expect(rightResizer).toHaveAttribute('aria-valuenow', String(defaultWidth));
    expect(storage.value()).toBe(String(defaultWidth));

    fireEvent.keyDown(rightResizer, { key: 'End' });

    expect(rightResizer).toHaveAttribute('aria-valuenow', String(rightPanelMaximum));
    expect(storage.value()).toBe(String(rightPanelMaximum));

    fireEvent.click(screen.getByRole('button', { name: '隐藏侧边栏' }));
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    rightResizer = screen.getByRole('separator', { name: '调整侧边栏宽度' });
    expect(rightResizer).toHaveAttribute('aria-valuenow', String(rightPanelMaximum));
    expect(storage.value()).toBe(String(rightPanelMaximum));
    expect(storage.set).toHaveBeenCalledTimes(4);

    const activityResizer = screen.getByRole('separator', { name: '调整工具使用面板高度' });
    expect(activityResizer.tagName).toBe('BUTTON');

    expect(activityResizer).toHaveAttribute('aria-valuenow', '112');

    fireEvent.keyDown(activityResizer, { key: 'ArrowUp' });

    expect(activityResizer).toHaveAttribute('aria-valuenow', '128');
    expect(screen.getByTestId('runtime-panel')).toHaveStyle({ '--activity-panel-height': '128px' });
  });

  it('clamps only displayed panel width on shrink and restores the durable preference when it grows', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1400 });
    const persistedWidth = 480.5;
    const storage = createShellLayoutStorage(String(persistedWidth));
    render(<App shellLayoutStorage={storage} />);
    await waitForBackendThreadHeading();
    const layout = screen.getByTestId('chat-layout');
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    expect(layout).toHaveStyle({ gridTemplateColumns: `minmax(0, 1fr) 6px ${persistedWidth}px` });
    expect(storage.set).not.toHaveBeenCalled();

    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 });
    act(() => {
      window.dispatchEvent(new Event('resize'));
    });

    await waitFor(() => {
      const displayedWidth = Number(screen.getByTestId('right-panel-resizer').getAttribute('aria-valuenow'));
      expect(displayedWidth).toBeLessThan(persistedWidth);
      expect(layout).toHaveStyle({ gridTemplateColumns: `minmax(0, 1fr) 6px ${displayedWidth}px` });
      expect(storage.value()).toBe(String(persistedWidth));
    });
    expect(storage.set).not.toHaveBeenCalled();

    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
    act(() => {
      window.dispatchEvent(new Event('resize'));
    });

    await waitFor(() => {
      expect(layout).toHaveStyle({ gridTemplateColumns: `minmax(0, 1fr) 6px ${persistedWidth}px` });
    });
    expect(storage.value()).toBe(String(persistedWidth));
    expect(storage.set).not.toHaveBeenCalled();
  });

  it('exposes the visible Agent rail geometry through one separator snapshot', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1400 });
    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByRole('button', { name: '打开工作台' }));
    const rail = screen.getByTestId('workbench-sidebar-resizer');
    expect(rail).toHaveAttribute('aria-valuenow', '340');
    expect(screen.getByTestId('frontend-app')).toHaveStyle({ '--workbench-sidebar-width': '340px' });
  });

  it('keeps right sidebar drag updates local until the pointer is released', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
    const storage = createShellLayoutStorage('380');
    render(<App shellLayoutStorage={storage} />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    expect(storage.value()).toBe('380');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 700);

    const previewWidth = rightResizer.getAttribute('aria-valuenow');
    expect(layout).toHaveStyle({ gridTemplateColumns: `minmax(0, 1fr) 6px ${previewWidth}px` });
    expect(storage.value()).toBe('380');

    dispatchPointer(window, 'pointerup', 700);

    expect(storage.value()).toBe(previewWidth);
  });

  it('does not persist a no-move pointer up and rolls back pointer cancellation', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
    const storage = createShellLayoutStorage('380');

    render(<App shellLayoutStorage={storage} />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const layout = screen.getByTestId('chat-layout');
    const rightResizer = screen.getByTestId('right-panel-resizer');
    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointerup', 1100);
    expect(storage.set).not.toHaveBeenCalled();
    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 900);
    expect(layout).not.toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 380px' });
    dispatchPointer(window, 'pointercancel', 900);

    expect(layout).toHaveStyle({ gridTemplateColumns: 'minmax(0, 1fr) 6px 380px' });
    expect(storage.value()).toBe('380');
    expect(storage.set).not.toHaveBeenCalled();
  });
