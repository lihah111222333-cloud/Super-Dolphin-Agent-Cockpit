import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { screen } from '@testing-library/react';
import { beforeEach, vi } from 'vitest';
import mermaid from 'mermaid';
import { createShellLayoutStore, useShellLayoutStore } from '../../../app/shell/model/useShellLayoutStore.js';
import { solveWorkbenchGeometry } from '../../../shared/layout/workbenchGeometry.js';
import { ChatPage as ChatPageComponent } from '../ChatPage.jsx';

const chatCodeServiceMocks = vi.hoisted(() => ({
  copyTextToClipboard: vi.fn(),
  locateCodeFile: vi.fn(),
  openCodeFile: vi.fn(),
  openPath: vi.fn(),
  onFilesDropped: vi.fn(() => () => {}),
  saveCodeFile: vi.fn(),
}));
const { copyTextToClipboard, locateCodeFile, onFilesDropped, openCodeFile, openPath, saveCodeFile } = chatCodeServiceMocks;

vi.mock('../services/chatCodeService.js', () => chatCodeServiceMocks);

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn((_id, source) => Promise.resolve({
      svg: `<svg role="img" aria-label="mock mermaid"><text>${source}</text></svg>`,
    })),
  },
}));

function createFakeStore(overrides = {}) {
  const store = {
    actionNotice: null,
    activeProject: '/repo/app',
    activeThreadId: '',
    activeTurnByThread: {},
    activityStatsByThread: {},
    activityThreadAtById: {},
    agents: [],
    mainAgentId: '',
    addComposerCapability: vi.fn(),
    archiveThread: vi.fn(),
    attachDroppedFilesForComposer: vi.fn(),
    attachPastedImagesForComposer: vi.fn(),
    attachPathsForComposer: vi.fn(),
    attachments: [],
    bootstrap: vi.fn().mockResolvedValue(undefined),
    bootstrapStatus: 'ready',
    chatSurfaceLoadingCwd: '',
    clearComposer: vi.fn(),
    composerCapabilities: [],
    copyActiveThreadInfo: vi.fn(),
    cwd: '/repo/app',
    deleteStaleThreads: vi.fn(),
    diffTextByThread: {},
    draft: '',
    error: '',
    forceCompleteActiveThread: vi.fn(),
    hasActiveThreadActions: vi.fn(() => Boolean(store.activeThreadId)),
    hasInterruptibleThreadAction: vi.fn(() => false),
    interruptActiveThread: vi.fn(),
    loadOlderThreadMessages: vi.fn(),
    loadThreadConfig: vi.fn(),
    newThread: vi.fn(),
    openNewWindow: vi.fn(),
    openForkDraft: vi.fn(),
    pendingActiveThreadId: '',
    pinnedThreadAtById: {},
    provider: 'codex',
    providerConfig: { provider: 'codex', model: 'gpt-5.5', effort: 'xhigh' },
    recoverActiveThread: vi.fn(),
    reconcileComposerCapabilities: vi.fn(),
    removeAttachment: vi.fn(),
    removeComposerCapability: vi.fn(),
    renameThread: vi.fn(),
    runtimeResultEntries: [],
    saveComposerModelConfig: vi.fn(),
    selectFilesForComposer: vi.fn(),
    selectThread: vi.fn(),
    sendDraft: vi.fn(),
    sending: false,
    smoothStreaming: true,
    setDraft: vi.fn((value) => {
      store.draft = value;
    }),
    statuses: {},
    syncThreadState: vi.fn(),
    threadArchiveLoadingByThread: {},
    threadConfigByThread: {},
    threadConfigLoadingByThread: {},
    threadConfigSaving: false,
    threadDiffReadyByThread: {},
    threadMessagePaginationByThread: {},
    threadStateLoadingByThread: {},
    threadTimelineReadyByThread: {},
    threads: [],
    timelinesByThread: {},
    toggleProviderMode: vi.fn(),
    tokenUsageByThread: {},
    warningEntries: [],
    ...overrides,
  };
  return store;
}

function createMemoryShellLayoutStorage(initialValue = '0') {
  let storedValue = initialValue;
  return {
    get: vi.fn(() => storedValue),
    set: vi.fn((_key, value) => { storedValue = value; }),
    remove: vi.fn(() => { storedValue = null; }),
    value: () => storedValue,
  };
}

function createShellLayoutTestHarness(initialValue = '0') {
  const storage = createMemoryShellLayoutStorage(initialValue);
  return {
    storage,
    store: createShellLayoutStore({ storage }),
  };
}

function deferred() {
  const pending = {};
  pending.promise = new Promise((resolve, reject) => {
    pending.resolve = resolve;
    pending.reject = reject;
  });
  return pending;
}

function createActiveThreadStore(messages, overrides = {}) {
  return createFakeStore({
    activeThreadId: 'thread-1',
    threads: [{ id: 'thread-1', name: '渲染窗口会话', provider: 'codex', status: 'idle', updatedAt: '2026-06-02T08:00:00Z' }],
    threadTimelineReadyByThread: { 'thread-1': true },
    timelinesByThread: {
      'thread-1': messages,
    },
    ...overrides,
  });
}

function getThreadCardByName(name) {
  const card = screen.getAllByText(name)
    .map((node) => node.closest('.thread-card'))
    .find(Boolean);
  if (!card) throw new Error(`Thread card not found: ${name}`);
  return card;
}

function createChatPageQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

function TestChatPage({ shellLayoutStore, ...props }) {
  const [defaultShellLayout] = React.useState(createShellLayoutTestHarness);
  const [queryClient] = React.useState(createChatPageQueryClient);
  const resolvedShellLayoutStore = shellLayoutStore === undefined
    ? defaultShellLayout.store
    : shellLayoutStore;
  const rightPreference = useShellLayoutStore(resolvedShellLayoutStore, (state) => state.rightPanelWidth);
  const geometrySnapshot = solveWorkbenchGeometry({
    activityHeight: 64,
    railOpen: false,
    railWidth: 340,
    rightOpen: props.rightPanelOpen === true,
    rightPreference,
    viewportHeight: window.innerHeight,
    viewportWidth: window.innerWidth,
  });
  const layoutActions = {
    activity: { begin: vi.fn(), keyDown: vi.fn() },
    rail: { begin: vi.fn(), keyDown: vi.fn() },
    right: {
      begin: vi.fn(),
      keyDown: vi.fn(),
      setOpen: (next) => {
        const open = typeof next === 'function' ? next(props.rightPanelOpen === true) : next;
        if (open && rightPreference === 0) {
          resolvedShellLayoutStore.getState().setRightPanelWidth(geometrySnapshot.right.defaultWidth);
        }
        props.setRightPanelOpen?.(open);
      },
    },
  };
  return React.createElement(
    QueryClientProvider,
    { client: queryClient },
    React.createElement(ChatPageComponent, {
      ...props,
      geometrySnapshot,
      layoutActions,
    }),
  );
}

function TestChatPageWrapper({ copy, shellLayoutStore, store, projectPath, rightPanelOpen: initialOpen = false }) {
  const [open, setOpen] = React.useState(initialOpen);

  return React.createElement(
    'div',
    null,
    React.createElement(
      'button',
      { type: 'button', onClick: () => setOpen((prev) => !prev) },
      '测试切换侧边栏',
    ),
    React.createElement(TestChatPage, {
      copy,
      shellLayoutStore,
      store,
      projectPath,
      rightPanelOpen: open,
      setRightPanelOpen: setOpen,
    }),
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  delete window.matchMedia;
  locateCodeFile.mockResolvedValue({
    ok: true,
    paths: ['/repo/app/src/main.go'],
    matches: [{ path: '/repo/app/src/main.go', relative: 'src/main.go' }],
  });
  openCodeFile.mockResolvedValue({
    ok: true,
    filePath: '/repo/app/src/main.go',
    relative: 'src/main.go',
    startLine: 9,
    endLine: 11,
    totalLines: 20,
    snippet: [
      { line: 9, text: 'func main() {' },
      { line: 10, text: '  run()' },
      { line: 11, text: '}' },
    ],
  });
  openPath.mockResolvedValue({ ok: true, opened: true });
});


export {
  TestChatPage,
  TestChatPageWrapper,
  copyTextToClipboard,
  createActiveThreadStore,
  createFakeStore,
  createShellLayoutTestHarness,
  deferred,
  getThreadCardByName,
  locateCodeFile,
  mermaid,
  onFilesDropped,
  openCodeFile,
  openPath,
  saveCodeFile,
};
