import React from 'react';
import { screen } from '@testing-library/react';
import { beforeEach, vi } from 'vitest';
import mermaid from 'mermaid';
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
    archiveThread: vi.fn(),
    attachDroppedFilesForComposer: vi.fn(),
    attachPastedImagesForComposer: vi.fn(),
    attachPathsForComposer: vi.fn(),
    attachments: [],
    bootstrapStatus: 'ready',
    chatSurfaceLoadingCwd: '',
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
    removeAttachment: vi.fn(),
    renameThread: vi.fn(),
    rightPanelWidth: 0,
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
    setRightPanelWidth: vi.fn((value) => {
      store.rightPanelWidth = value;
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

function TestChatPageWrapper({ copy, store, projectPath, rightPanelOpen: initialOpen = false }) {
  const [open, setOpen] = React.useState(initialOpen);

  return React.createElement(
    'div',
    null,
    React.createElement(
      'button',
      { type: 'button', onClick: () => setOpen((prev) => !prev) },
      '测试切换侧边栏',
    ),
    React.createElement(ChatPageComponent, {
      copy,
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
  ChatPageComponent as ChatPage,
  TestChatPageWrapper,
  copyTextToClipboard,
  createActiveThreadStore,
  createFakeStore,
  deferred,
  getThreadCardByName,
  locateCodeFile,
  mermaid,
  onFilesDropped,
  openCodeFile,
  openPath,
  saveCodeFile,
};
