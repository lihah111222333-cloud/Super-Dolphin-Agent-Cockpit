import React from 'react';
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
  dispatchPointer,
  waitForBackendThreadHeading,
  getThreadCardByName,
  clickThreadCardByName,
  findThreadCardByName,
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

  it('ignores foreign pointer move and up without terminating the owner session', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
    const storage = createShellLayoutStorage('380');
    render(<App shellLayoutStorage={storage} />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const layout = screen.getByTestId('chat-layout');
    const rightResizer = screen.getByTestId('right-panel-resizer');
    dispatchPointer(rightResizer, 'pointerdown', 1100, { pointerId: 7 });
    dispatchPointer(window, 'pointermove', 700, { pointerId: 8 });
    dispatchPointer(window, 'pointerup', 700, { pointerId: 8 });
    expect(layout).toHaveStyle({ gridTemplateColumns: '240px minmax(0, 1fr) 6px 380px' });
    expect(storage.set).not.toHaveBeenCalled();

    dispatchPointer(window, 'pointermove', 900, { pointerId: 7 });
    dispatchPointer(window, 'pointerup', 900, { pointerId: 7 });
    expect(storage.value()).not.toBe('380');
  });

  it('closes the right sidebar when dragged flush to the right edge', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1980 });
    const storage = createShellLayoutStorage('380');

    render(<App shellLayoutStorage={storage} />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 1480);
    expect(screen.getByTestId('runtime-panel')).toBeInTheDocument();
    dispatchPointer(window, 'pointerup', 1480);

    expect(screen.queryByTestId('runtime-panel')).not.toBeInTheDocument(); expect(layout).toHaveStyle({ gridTemplateColumns: '240px minmax(0, 1fr)' });
    expect(screen.queryByTestId('right-panel-resizer')).not.toBeInTheDocument();
    expect(storage.value()).toBe('0');
  });

  it('isolates right sidebar diff, warnings, and tool stats to the selected agent', async () => {
    backend.resolveThreadIdentity.mockResolvedValue({
      id: 'thread-b', agent_id: 'agent-b', name: 'Agent B', provider: 'codex', status: 'running', cwd: '/repo/app',
    });
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-a',
      threads: [
        { id: 'thread-a', agentId: 'agent-a', name: 'Agent A', provider: 'codex', status: 'running' },
        { id: 'thread-b', agentId: 'agent-b', name: 'Agent B', provider: 'codex', status: 'running' },
      ],
      activityStatsByThread: {
        'agent-a': { lspCalls: 1, commands: 0, fileEdits: 1, toolCalls: { edit: 1 } },
        'agent-b': { lspCalls: 7, commands: 0, fileEdits: 0, toolCalls: { shell: 7 } },
      },
      diffTextByThread: {
        'agent-a': 'diff --git a/a b/a',
        'agent-b': 'diff --git a/b b/b',
      },
    });
    backend.getThreadState.mockImplementation(({ threadId }) => Promise.resolve({
      activeThreadId: threadId,
      timelinesByThread: { [threadId]: [{ id: `assistant-${threadId}`, kind: 'assistant', text: `${threadId} ready` }] },
    }));

    render(<App />);
    await findThreadCardByName('Agent A');

    act(() => {
      bridgeCallback({
        type: 'thread.send/failed',
        payload: { method: 'turn/start', agentId: 'agent-a', error: 'a failed' },
      });
      bridgeCallback({
        type: 'bridge.call/failed',
        payload: { method: 'turn/start', agentId: 'agent-b', error: 'b failed' },
      });
      bridgeCallback({
        type: 'api.rpc.failed',
        payload: { method: 'thread/config/get', error: 'global failed' },
      });
    });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    expect(screen.queryByTestId('warning-log-panel')).not.toBeInTheDocument();
    fireEvent.keyDown(screen.getByTestId('activity-panel-resizer'), { key: 'ArrowUp' });

    expect(within(screen.getByTestId('runtime-panel')).getByRole('button', { name: '折叠 a' })).toBeInTheDocument();
    expect(screen.getByTestId('runtime-panel')).not.toHaveTextContent('diff --git a/a b/a');
    expect(screen.getByTestId('runtime-panel')).not.toHaveTextContent('diff --git a/b b/b');
    expect(screen.getByLabelText('LSP (8 tools) 调用次数')).toHaveTextContent('1');
    expect(screen.getByTestId('warning-log-panel')).toHaveTextContent('thread.send/failed');
    expect(screen.getByTestId('warning-log-panel')).toHaveTextContent('api.rpc.failed');
    expect(screen.getByTestId('warning-log-panel')).not.toHaveTextContent('bridge.call/failed');

    clickThreadCardByName('Agent B');

    await waitFor(() => {
      expect(within(screen.getByTestId('runtime-panel')).getByRole('button', { name: '折叠 b' })).toBeInTheDocument();
      expect(screen.getByTestId('runtime-panel')).not.toHaveTextContent('diff --git a/a b/a');
      expect(screen.getByTestId('runtime-panel')).not.toHaveTextContent('diff --git a/b b/b');
      expect(screen.getByLabelText('LSP (8 tools) 调用次数')).toHaveTextContent('7');
      expect(screen.getByTestId('warning-log-panel')).toHaveTextContent('bridge.call/failed');
      expect(screen.getByTestId('warning-log-panel')).toHaveTextContent('api.rpc.failed');
      expect(screen.getByTestId('warning-log-panel')).not.toHaveTextContent('thread.send/failed');
    });
  });

  it('keeps the current identity and content until the target thread refresh commits', async () => {
    let resolveThreadBState;
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-a',
      threads: [
        { id: 'thread-a', name: 'Agent A', provider: 'codex', status: 'idle' },
        { id: 'thread-b', name: 'Agent B', provider: 'codex', status: 'idle' },
      ],
    });
    backend.getThreadState.mockImplementation(({ threadId }) => {
      if (threadId === 'thread-b') {
        return new Promise((resolve) => {
          resolveThreadBState = resolve;
        });
      }
      return Promise.resolve({
        activeThreadId: threadId,
        timelinesByThread: {
          [threadId]: [{ id: 'assistant-a', kind: 'assistant', text: 'Agent A ready' }],
        },
      });
    });

    render(<App />);
    await screen.findByText('Agent A ready');

    act(() => {
      useClientStore.setState((state) => ({
        timelinesByThread: {
          ...state.timelinesByThread,
          'thread-b': [{ id: 'stale-b', role: 'assistant', text: 'stale cached Agent B content' }],
        },
      }));
    });

    clickThreadCardByName('Agent B');

    await waitFor(() => expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-b',
      includeDiff: false,
    }));
    expect(useClientStore.getState().activeThreadId).toBe('thread-a');
    expect(useClientStore.getState().pendingActiveThreadId).toBe('thread-b');
    expect(useClientStore.getState().threadStateLoadingByThread['thread-b']).toBe(true);
    expect(getThreadCardByName('Agent A')).toHaveClass('active');
    expect(screen.getByText('Agent A ready')).toBeInTheDocument();
    expect(screen.queryByText('stale cached Agent B content')).not.toBeInTheDocument();
    expect(screen.queryByText(/让我们从/)).not.toBeInTheDocument();
    expect(screen.queryByTestId('timeline-loading-placeholder')).not.toBeInTheDocument();

    act(() => {
      resolveThreadBState({
        activeThreadId: 'thread-b',
        threads: [
          { id: 'thread-a', name: 'Agent A', provider: 'codex', status: 'idle' },
          { id: 'thread-b', name: 'Agent B', provider: 'codex', status: 'idle' },
        ],
        timelinesByThread: {
          'thread-b': [{ id: 'fresh-b', kind: 'assistant', text: 'fresh Agent B content' }],
        },
      });
    });

    await screen.findByText('fresh Agent B content');
    expect(useClientStore.getState().activeThreadId).toBe('thread-b');
    expect(useClientStore.getState().pendingActiveThreadId).toBe('');
    expect(screen.queryByText('Agent A ready')).not.toBeInTheDocument();
    expect(screen.queryByText('stale cached Agent B content')).not.toBeInTheDocument();
  });

  it('keeps current content while trusted target cache refreshes before commit', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-a',
      threads: [
        { id: 'thread-a', name: 'Agent A', provider: 'codex', status: 'idle' },
        { id: 'thread-b', name: 'Agent B', provider: 'codex', status: 'idle' },
      ],
    });
    backend.getThreadState.mockImplementation(({ threadId }) => {
      if (threadId === 'thread-b') return new Promise(() => {});
      return Promise.resolve({
        activeThreadId: threadId,
        timelinesByThread: {
          [threadId]: [{ id: 'assistant-a', kind: 'assistant', text: 'Agent A ready' }],
        },
      });
    });
    backend.getThreadMessages.mockImplementation(({ threadId }) => {
      if (threadId === 'thread-b') return new Promise(() => {});
      return Promise.resolve({ messages: [] });
    });

    render(<App />);
    await screen.findByText('Agent A ready');

    act(() => {
      useClientStore.setState((state) => ({
        timelinesByThread: {
          ...state.timelinesByThread,
          'thread-b': [{ id: 'cached-b', role: 'assistant', text: 'cached Agent B content' }],
        },
        threadTimelineReadyByThread: {
          ...state.threadTimelineReadyByThread,
          'thread-b': true,
        },
      }));
    });

    clickThreadCardByName('Agent B');

    await waitFor(() => expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-b',
      includeDiff: false,
    }));
    expect(screen.queryByText('cached Agent B content')).not.toBeInTheDocument();
    expect(screen.getByText('Agent A ready')).toBeInTheDocument(); expect(useClientStore.getState().pendingActiveThreadId).toBe('thread-b');
    expect(screen.queryByTestId('timeline-loading-placeholder')).not.toBeInTheDocument();
  });

  it('resizes the chat rail and right sidebar without crossing their minimum widths', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    const layout = screen.getByTestId('chat-layout');
    fireEvent.click(screen.getByRole('button', { name: '打开工作台' }));
    const leftResizer = screen.getByTestId('workbench-sidebar-resizer');

    dispatchPointer(leftResizer, 'pointerdown', 340);
    dispatchPointer(window, 'pointermove', 40);
    dispatchPointer(window, 'pointerup', 40);

    expect(leftResizer).toHaveAttribute('aria-valuenow', '280');
    expect(screen.getByTestId('frontend-app')).toHaveStyle({ '--workbench-sidebar-width': '280px' });
    expect(layout).toHaveStyle({ gridTemplateColumns: '240px minmax(0, 1fr)' });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    const rightResizer = screen.getByTestId('right-panel-resizer');

    dispatchPointer(rightResizer, 'pointerdown', 1100);
    dispatchPointer(window, 'pointermove', 1500);
    dispatchPointer(window, 'pointerup', 1500);

    await waitFor(() => {
      expect(screen.queryByTestId('runtime-panel')).not.toBeInTheDocument();
      expect(layout).toHaveStyle({ gridTemplateColumns: '240px minmax(0, 1fr)' });
    });
  });

  it('uses backend activity stats for the resizable tool usage panel', async () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 640 });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    expect(screen.getByTestId('runtime-panel')).toHaveStyle({
      '--activity-panel-height': '112px',
      '--activity-panel-min-height': '112px',
      '--activity-panel-max-height': '286px',
      '--diff-panel-min-height': '286px',
      '--diff-panel-max-height': '461px',
    });
    expect(screen.queryByTestId('warning-log-panel')).not.toBeInTheDocument();
    expect(screen.getByLabelText('LSP (8 tools) 调用次数')).toHaveTextContent('3');
    expect(screen.getByLabelText('LSP (8 tools) 调用次数')).not.toHaveAttribute('title');
    expect(screen.getByLabelText('工具调用总数')).toHaveTextContent('6');
    expect(screen.queryByText('edit:')).not.toBeInTheDocument();

    fireEvent.mouseEnter(screen.getByLabelText('LSP (8 tools) 调用次数'));
    expect(screen.queryByTestId('runtime-stat-tooltip')).not.toBeInTheDocument();
    fireEvent.click(screen.getByLabelText('LSP (8 tools) 调用次数'));
    expect(screen.getByTestId('runtime-stat-tooltip')).toHaveTextContent('LSP (8 tools)');
    expect(screen.getByTestId('runtime-stat-tooltip')).toHaveTextContent('edit');
    expect(screen.getByTestId('runtime-stat-tooltip')).toHaveTextContent('3');
    fireEvent.keyDown(screen.getByRole('dialog', { name: 'LSP (8 tools) 调用明细' }), { key: 'Escape' });
    await waitFor(() => {
      expect(screen.queryByTestId('runtime-stat-tooltip')).not.toBeInTheDocument();
    });

    dispatchPointer(screen.getByTestId('activity-panel-resizer'), 'pointerdown', 0, { clientY: 500 });
    dispatchPointer(window, 'pointermove', 0, { clientY: 0 });
    dispatchPointer(window, 'pointerup', 0, { clientY: 0 });

    await waitFor(() => {
      expect(screen.getByTestId('runtime-panel')).toHaveStyle({ '--activity-panel-height': '286px' });
    });
    expect(screen.getByTestId('warning-log-panel')).toBeInTheDocument();
  });

  it('shows tool return entries alongside warning lines in the runtime panel', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    act(() => {
      bridgeCallback({
        type: 'ui/thread/patch',
        payload: {
          threadId: 'thread-1',
          sequence: '9007199254740993124',
          timelineItems: [{
            id: 'tool-grep',
            kind: 'tool',
            tool: 'mcp__lsp__grep',
            status: 'completed',
            preview: '{"total":3}',
            output: 'src/App.jsx: runtime log result',
            ts: '2026-05-30T08:00:00Z',
          }],
        },
      });
      bridgeCallback({
        type: 'api.rpc.failed',
        payload: { method: 'thread/config/get', threadId: 'thread-1', error: 'backend unavailable' },
      });
    });

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));
    fireEvent.keyDown(screen.getByTestId('activity-panel-resizer'), { key: 'ArrowUp' });

    const logPanel = screen.getByTestId('warning-log-panel');
    expect(logPanel).toHaveTextContent('api.rpc.failed');
    expect(logPanel).toHaveTextContent('grep');
    expect(logPanel).toHaveTextContent('返回');
    expect(logPanel).not.toHaveTextContent('{"total":3}');

    const resultLine = within(logPanel).getByRole('button', { name: /grep/ });
    fireEvent.mouseEnter(resultLine);
    expect(screen.queryByTestId('warning-log-popover')).not.toBeInTheDocument();
    fireEvent.click(resultLine);

    const popover = screen.getByTestId('warning-log-popover');
    expect(popover).toHaveTextContent('[redacted]');
    expect(popover).not.toHaveTextContent('src/App.jsx: runtime log result');
    expect(popover).not.toHaveTextContent('"preview": "{\\"total\\":3}"');
  });

  it('clamps right-edge runtime click details into the viewport', async () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 640 });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    const toolStat = screen.getByLabelText('工具调用总数');
    toolStat.getBoundingClientRect = () => ({
      x: 980,
      y: 580,
      left: 980,
      right: 1008,
      top: 580,
      bottom: 596,
      width: 28,
      height: 16,
      toJSON() {
        return this;
      },
    });

    fireEvent.click(toolStat);

    const tooltip = screen.getByTestId('runtime-stat-tooltip');
    expect(tooltip).toHaveTextContent('工具');
    expect(tooltip.style.getPropertyValue('--runtime-stat-tooltip-left')).toBe('652px');
    expect(tooltip.style.getPropertyValue('--runtime-stat-tooltip-bottom')).toBe('70px');
  });

  it('lets bottom-right runtime click details use the available vertical space', async () => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 640 });
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中' }],
      active_turn: { id: 'turn-1', thread_id: 'thread-1', status: 'running' },
      tokenUsageByThread: {
        'thread-1': { usedTokens: 128, contextWindowTokens: 1024, usedPercent: 12.5 },
      },
      activityStatsByThread: {
        'thread-1': {
          toolCalls: Object.fromEntries(
            Array.from({ length: 18 }, (_, index) => [`very_long_tool_name_${index + 1}`, index + 1]),
          ),
        },
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    const toolStat = screen.getByLabelText('工具调用总数');
    toolStat.getBoundingClientRect = () => ({
      x: 980,
      y: 580,
      left: 980,
      right: 1008,
      top: 580,
      bottom: 596,
      width: 28,
      height: 16,
      toJSON() {
        return this;
      },
    });

    fireEvent.click(toolStat);

    const tooltip = screen.getByTestId('runtime-stat-tooltip');
    expect(tooltip).toHaveTextContent('very_long_tool_name_18');
    expect(tooltip.style.getPropertyValue('--runtime-stat-tooltip-left')).toBe('652px');
    expect(tooltip.style.getPropertyValue('--runtime-stat-tooltip-bottom')).toBe('70px');
    expect(tooltip.style.getPropertyValue('--runtime-stat-tooltip-max-height')).toBe('558px');
  });

  it('disables thread-scoped chat buttons before a backend thread exists', async () => {
    backend.getSidebarState.mockResolvedValue({ activeThreadId: '', threads: [] });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });

    render(<App />);

    await screen.findByText('我们应该在 Super Dolphin Agent 中构建什么？');
    expect(screen.queryByLabelText('复制当前线程')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('停止')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('线程状态')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('压缩当前线程')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('选择附件')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('权限')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('请先选择会话')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '自定义配置' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '语音输入' })).not.toBeInTheDocument();
    expect(screen.getByLabelText('添加文件')).toBeInTheDocument();
    expect(screen.queryByLabelText('发送权限')).not.toBeInTheDocument();
    expect(screen.getByLabelText('会话列表')).toBeInTheDocument();
    expect(screen.getByLabelText('0 个 Agent')).toBeInTheDocument();
    expect(screen.getByLabelText('打开归档列表')).toBeEnabled();
    expect(screen.getByText('暂无会话，点击「新建对话」开始草稿')).toBeInTheDocument();
  });

  it('disables thread-scoped chat buttons when the active backend thread is archived', async () => {
    backend.getSidebarState.mockResolvedValue({
      activeThreadId: 'essay_agent_15',
      threads: [{ id: 'essay_agent_15', name: '作文Agent-15', provider: 'codex', status: 'archived' }],
    });
    backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });

    render(<App />);

    await screen.findByText('我们应该在 Super Dolphin Agent 中构建什么？');
    expect(screen.queryByLabelText('复制当前线程')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('线程状态')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('停止')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('强制完成')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('请先选择会话')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '自定义配置' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '语音输入' })).not.toBeInTheDocument();
    expect(screen.queryByText('作文Agent-15')).not.toBeInTheDocument();
    expect(backend.getThreadState).not.toHaveBeenCalledWith(expect.objectContaining({ threadId: 'essay_agent_15' }));
  });

  it('connects ComposerMeta attachments as plain arrays and conversation operation buttons', async () => {
    backend.selectFiles.mockResolvedValue(['/tmp/a.txt']);
    backend.resolveThreadIdentity.mockResolvedValue({ id: 'thread-1', providerThreadId: 'provider-thread-1', agent_id: 'agent-1' });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByLabelText('添加文件'));
    expect(await screen.findByText('a.txt')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('复制当前线程'));
    fireEvent.click(screen.getByLabelText('停止'));
    fireEvent.click(screen.getByLabelText('强制完成'));
    fireEvent.click(screen.getByLabelText('进程恢复'));
    expect(screen.queryByLabelText('归档会话')).not.toBeInTheDocument();

    await waitFor(() => {
      expect(backend.selectFiles).toHaveBeenCalledWith();
      expect(JSON.parse(backend.copyTextToClipboard.mock.calls[0][0])).toEqual(expect.objectContaining({
        agentId: 'agent-1',
        providerThreadId: 'provider-thread-1',
        provider: 'codex',
      }));
      expect(backend.interruptTurn).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        threadId: 'thread-1',
        expectedTurnId: expect.any(String),
        requestId: expect.any(String),
        source: 'ui_stop',
      }));
      expect(backend.forceCompleteTurn).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
      expect(backend.recoverThread).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
      expect(backend.archiveThread).not.toHaveBeenCalled();
    });
  });

  it('submits timeline approval decisions from the React chat timeline', async () => {
    backend.respondApproval.mockResolvedValue(null);
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'approval-1',
          role: 'assistant',
          kind: 'approval',
          title: 'shell',
          text: '需要执行 deploy 命令',
          sessionScope: 'session-scope-a',
          callId: 'call-11',
          requestId: 11,
          status: 'pending',
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    expect(await screen.findByTestId('approval-request-11')).toHaveTextContent('需要执行 deploy 命令');
    fireEvent.click(screen.getByRole('button', { name: '同意' }));
    expect(backend.respondApproval).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: '确认选择' }));

    await waitFor(() => {
      expect(backend.respondApproval).toHaveBeenCalledWith({
        sessionScope: 'session-scope-a',
        callId: 'call-11',
        requestId: 11,
        approved: true,
      });
    });
    expect(screen.getByRole('button', { name: '同意' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '确认选择' })).toBeDisabled();
  });

  it('interrupts the selected conversation when Escape is pressed', async () => {
    const interruptActiveThread = vi.spyOn(useClientStore.getState(), 'interruptActiveThread');
    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.keyDown(window, { key: 'Escape', code: 'Escape' });

    await waitFor(() => {
      expect(interruptActiveThread).toHaveBeenCalledTimes(1);
      expect(backend.interruptTurn).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        threadId: 'thread-1',
        expectedTurnId: expect.any(String),
        requestId: expect.any(String),
        source: 'ui_stop',
      }));
    });
  });

  it('leaves Escape to an open local surface without interrupting or preventing it', async () => {
    render(<App />);
    await waitForBackendThreadHeading();
    const localSurface = document.createElement('div');
    localSurface.setAttribute('role', 'dialog');
    document.body.append(localSurface);

    const event = new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true, cancelable: true });
    act(() => window.dispatchEvent(event));

    expect(event.defaultPrevented).toBe(false);
    expect(backend.interruptTurn).not.toHaveBeenCalled();
    localSurface.remove();
  });

  it('does not interrupt the selected conversation when Escape is handled by the composer', async () => {
    render(<App />);
    await waitForBackendThreadHeading();

    const input = screen.getByTestId('composer-input');
    input.focus();
    fireEvent.keyDown(input, { key: 'Escape', code: 'Escape' });

    expect(backend.interruptTurn).not.toHaveBeenCalled();
  });

  it('does not send an invalid interrupt when a running conversation has no active turn id', async () => {
    backend.getSidebarState.mockResolvedValueOnce({
      activeThreadId: 'thread-1',
      threads: [{ id: 'thread-1', name: '后端线程', provider: 'codex', status: '工作中' }],
    });

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.keyDown(window, { key: 'Escape', code: 'Escape' });

    await waitFor(() => expect(screen.getByTestId('chat-action-feedback')).toHaveTextContent('当前没有可中断任务'));
    expect(backend.interruptTurn).not.toHaveBeenCalled();
  });

  it('previews attachments on click and removes them only with the remove control', async () => {
    backend.selectFiles.mockResolvedValue(['/tmp/a.txt']);

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByLabelText('添加文件'));
    const attachment = await screen.findByRole('button', { name: /预览附件 a\.txt/ });
    fireEvent.click(attachment);

    const dialog = screen.getByRole('dialog', { name: '附件预览' });
    expect(dialog).toBeInTheDocument();
    expect(dialog).toHaveTextContent('a.txt');
    expect(dialog).not.toHaveTextContent('/tmp/a.txt');
    expect(screen.getByRole('button', { name: /预览附件 a\.txt/ })).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('关闭附件预览'));
    fireEvent.click(screen.getByLabelText('移除附件 a.txt'));

    expect(screen.queryByRole('button', { name: /预览附件 a\.txt/ })).not.toBeInTheDocument();
  });

  it('traps focus in the attachment preview and restores focus after Escape', async () => {
    backend.selectFiles.mockResolvedValue(['/tmp/a.txt']);

    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.click(screen.getByLabelText('添加文件'));
    const attachment = await screen.findByRole('button', { name: /预览附件 a\.txt/ });
    attachment.focus();
    fireEvent.click(attachment);

    const dialog = screen.getByRole('dialog', { name: '附件预览' });
    const closeIcon = within(dialog).getByLabelText('关闭附件预览');
    const closeText = within(dialog).getByRole('button', { name: '关闭' });
    await waitFor(() => {
      expect(document.activeElement).toBe(closeIcon);
    });

    fireEvent.keyDown(dialog, { key: 'Tab', code: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(closeText);
    fireEvent.keyDown(dialog, { key: 'Tab', code: 'Tab' });
    expect(document.activeElement).toBe(closeIcon);
    fireEvent.keyDown(dialog, { key: 'Escape', code: 'Escape' });

    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: '附件预览' })).not.toBeInTheDocument();
    });
    expect(document.activeElement).toBe(attachment);
    expect(backend.interruptTurn).not.toHaveBeenCalled();
  });

  it('adds pasted images and dropped files to the composer attachments', async () => {
    backend.saveClipboardImage.mockResolvedValue('/tmp/pasted.png');

    render(<App />);
    await waitForBackendThreadHeading();

    const input = screen.getByTestId('composer-input');
    const image = new File(['png'], 'shot.png', { type: 'image/png' });
    fireEvent.paste(input, {
      clipboardData: {
        files: [image],
        items: [],
        getData: () => '',
      },
    });

    expect(await screen.findByRole('button', { name: /预览附件 shot\.png/ })).toBeInTheDocument();

    const dropped = new File(['txt'], 'notes.txt', { type: 'text/plain' });
    Object.defineProperty(dropped, 'path', { value: '/tmp/notes.txt' });
    fireEvent.drop(screen.getByTestId('composer-dock'), {
      dataTransfer: {
        files: [dropped],
        items: [],
        types: ['Files'],
      },
    });

    expect(await screen.findByRole('button', { name: /预览附件 notes\.txt/ })).toBeInTheDocument();

    fireEvent.paste(input, {
      clipboardData: {
        files: [],
        items: [],
        types: ['x-special/gnome-copied-files', 'text/uri-list', 'text/plain'],
        getData: (type) => {
          if (type === 'x-special/gnome-copied-files') return 'copy\nfile:///tmp/desktop-copy.txt';
          if (type === 'text/uri-list') return 'file:///tmp/desktop-copy.txt';
          if (type === 'text/plain') return '/tmp/desktop-copy.txt';
          return '';
        },
      },
    });

    expect(await screen.findByRole('button', { name: /预览附件 desktop-copy\.txt/ })).toBeInTheDocument();
    expect(backend.saveClipboardImage).toHaveBeenCalledWith(expect.any(String));
  });

  it('accepts native Wails file drops on the text editor target', async () => {
    let nativeDropHandler = null;
    backend.onFilesDropped.mockImplementation((handler) => {
      nativeDropHandler = handler;
      return () => {};
    });

    render(<App />);
    await waitForBackendThreadHeading();

    const composer = screen.getByTestId('composer-dock');
    const input = screen.getByTestId('composer-input');
    const conversation = screen.getByTestId('conversation-drop-zone');
    expect(composer).toHaveAttribute('data-file-drop-target');
    expect(input).toHaveAttribute('id', 'composer-input');
    expect(input).toHaveAttribute('data-file-drop-target');
    expect(conversation).toHaveAttribute('id', 'conversation-drop-zone');
    expect(conversation).toHaveAttribute('data-file-drop-target');

    act(() => {
      nativeDropHandler({
        files: ['/tmp/native-editor-drop.txt'],
        details: {
          id: 'composer-input',
          classList: [],
          attributes: { 'data-file-drop-target': '' },
        },
      });
    });

    expect(await screen.findByRole('button', { name: /预览附件 native-editor-drop\.txt/ })).toBeInTheDocument();

    act(() => {
      nativeDropHandler({
        name: 'files-dropped',
        data: {
          files: ['/tmp/native-wails-event-drop.txt'],
          details: {
            id: 'composer-input',
            classList: [],
            attributes: { 'data-file-drop-target': '' },
          },
        },
      });
    });

    expect(await screen.findByRole('button', { name: /预览附件 native-wails-event-drop\.txt/ })).toBeInTheDocument();

    act(() => {
      nativeDropHandler({
        payload: {
          files: ['/tmp/native-payload-drop.txt'],
          details: {
            id: 'composer-input',
            classList: [],
            attributes: { 'data-file-drop-target': '' },
          },
        },
      });
    });

    expect(await screen.findByRole('button', { name: /预览附件 native-payload-drop\.txt/ })).toBeInTheDocument();

    act(() => {
      nativeDropHandler({
        files: ['/tmp/native-composer-bar-drop.txt'],
        details: {
          id: 'chat-input-bar',
          classList: ['composer'],
          attributes: { 'data-file-drop-target': '' },
        },
      });
    });

    expect(await screen.findByRole('button', { name: /预览附件 native-composer-bar-drop\.txt/ })).toBeInTheDocument();

    act(() => {
      nativeDropHandler({
        files: ['/tmp/native-conversation-drop.txt'],
        details: {
          id: 'conversation-drop-zone',
          classList: ['conversation'],
          attributes: { 'data-file-drop-target': '' },
        },
      });
    });

    expect(await screen.findByRole('button', { name: /预览附件 native-conversation-drop\.txt/ })).toBeInTheDocument();

    act(() => {
      nativeDropHandler({
        files: ['/tmp/native-unknown-target-drop.txt'],
        details: {
          id: 'timeline-inner-node',
          classList: ['timeline-inner-node'],
          attributes: { 'data-testid': 'timeline-inner-node' },
        },
      });
    });

    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /预览附件 native-unknown-target-drop\.txt/ })).not.toBeInTheDocument();
    });
  });
