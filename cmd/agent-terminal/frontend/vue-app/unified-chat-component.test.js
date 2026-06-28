// @ts-nocheck
import { describe, it, expect, vi, beforeEach } from 'vitest';
const lifecycleMock = vi.hoisted(() => ({
  mounted: [],
  unmounted: [],
}));

const autoScrollMock = vi.hoisted(() => ({
  scheduleScrollToBottom: vi.fn(),
}));

const composerStoreMock = vi.hoisted(() => ({
  state: {
    text: '',
    attachments: [],
  },
  attachByPaths: vi.fn(() => 0),
  clearComposer: vi.fn(),
  activateDraft: vi.fn(),
}));

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    ...actual,
    onMounted: (cb) => lifecycleMock.mounted.push(cb),
    onBeforeUnmount: (cb) => lifecycleMock.unmounted.push(cb),
  };
});

import { reactive, ref } from '../lib/vue.esm-browser.prod.js';
import { PathChoiceModal } from './components/PathChoiceModal.js';

vi.mock('./stores/composer.js', () => ({
  useComposerStore: () => composerStoreMock,
}));

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(async () => ({})),
  copyTextToClipboard: vi.fn(async () => true),
  onFilesDropped: vi.fn(() => () => { }),
  resolveThreadIdentity: vi.fn(async () => ({})),
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./composables/useAutoScroll.js', () => ({
  useAutoScroll: () => ({
    scheduleScrollToBottom: autoScrollMock.scheduleScrollToBottom,
  }),
}));

import { callAPI, copyTextToClipboard, resolveThreadIdentity } from './services/api.js';
import { UnifiedChatPage } from './pages/UnifiedChatPage.js';

beforeEach(() => {
  lifecycleMock.mounted.length = 0;
  lifecycleMock.unmounted.length = 0;
  composerStoreMock.state.text = '';
  composerStoreMock.state.attachments = [];
  composerStoreMock.attachByPaths.mockReset();
  composerStoreMock.attachByPaths.mockImplementation(() => 0);
  composerStoreMock.clearComposer.mockReset();
  composerStoreMock.clearComposer.mockImplementation(() => {
    composerStoreMock.state.text = '';
    composerStoreMock.state.attachments = [];
  });
  composerStoreMock.activateDraft.mockReset();

  globalThis.window = {
    ...(globalThis.window || {}),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    setTimeout: vi.fn(() => 1),
    clearTimeout: vi.fn(),
    setInterval: vi.fn(() => 1),
    clearInterval: vi.fn(),
    alert: vi.fn(),
  };
  globalThis.document = {
    ...(globalThis.document || {}),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    querySelector: vi.fn(() => null),
    activeElement: null,
    body: { classList: { add: vi.fn(), remove: vi.fn() } },
  };
  vi.mocked(callAPI).mockReset();
  vi.mocked(callAPI).mockImplementation(async () => ({}));
  vi.mocked(copyTextToClipboard).mockReset();
  vi.mocked(copyTextToClipboard).mockImplementation(async () => true);
  vi.mocked(resolveThreadIdentity).mockReset();
  vi.mocked(resolveThreadIdentity).mockImplementation(async () => ({}));
  autoScrollMock.scheduleScrollToBottom.mockClear();
});

async function runMountedHooks() {
  for (const hook of lifecycleMock.mounted.splice(0)) {
    await hook();
  }
}

async function runUnmountedHooks() {
  for (const hook of lifecycleMock.unmounted.splice(0)) {
    await hook();
  }
}

async function flushTicks(count = 2) {
  for (let index = 0; index < count; index += 1) {
    await Promise.resolve();
  }
}

function makeProjectStore() {
  return {
    state: { active: '.' },
    projectOptions: { value: [] },
    setActive: () => { },
  };
}

function makeThreadStore(counters) {
  const displayName = (thread) => {
    counters.display.push(thread.id);
    return thread.name;
  };
  const getThreadStatus = (threadId) => {
    counters.status.push(threadId);
    return threadId === 'thread-active' ? 'running' : 'idle';
  };
  const getThreadStatusHeader = (threadId) => {
    counters.header.push(threadId);
    return '等待指示';
  };
  const getThreadInterruptible = (threadId) => {
    counters.interrupt.push(threadId);
    return true;
  };
  const getThreadPinnedAt = (threadId) => (threadId === 'thread-active' ? 11 : 0);
  const getThreadArchivedAt = (threadId) => (threadId === 'thread-archived' ? 99 : 0);

  return {
    state: {

      pinnedThreadAtById: { 'thread-active': 11 },
      archivedThreadAtById: { 'thread-archived': 99 },
      agentRuntimeById: {},
      skillRevision: 0,
    },
    getLayout: () => 'focus',
    setLayout: () => { },
    getCmdCardCols: () => 3,
    setCmdCardCols: () => { },
    getSplitRatio: () => 60,
    setSplitRatio: () => { },
    getThreadRailWidth: () => 232,
    setThreadRailWidth: () => { },
    getCurrentThreadId: () => '',
    saveActiveThread: () => { },
    saveActiveCmdThread: () => { },
    getThreadsByMode: () => [
      { id: 'thread-active', name: 'Active' },
      { id: 'thread-archived', name: 'Archived' },
    ],
    displayName,
    getThreadStatus,
    getThreadStatusHeader,
    getThreadInterruptible,
    getThreadPinnedAt,
    getThreadArchivedAt,
    getThreadTimeline: () => [],
    loadMessages: async () => ({}),
    stopThread: vi.fn(async () => ({ confirmed: true, settled: true, mode: 'interrupt_confirmed' })),
    getThreadDiff: () => '',
    getThreadStatusDetails: () => '',
    getThreadTokenUsage: () => null,
    getThreadCompacting: () => false,
    getThreadCompactResult: () => null,
    getThreadCompactSuccessCount: () => 0,
    getThreadActivityStats: () => ({}),
    getThreadAlerts: () => [],
  };
}

function makeAutoScrollThreadStore() {
  const currentThreadId = ref('thread-active');
  const timelinesByThread = reactive({
    'thread-active': [
      { id: 'assistant-1', kind: 'assistant', text: '已有消息', ts: '2026-03-07T10:00:00Z' },
    ],
  });
  const statuses = reactive({ 'thread-active': 'running' });
  const statusHeaders = reactive({ 'thread-active': '处理中' });

  return {
    currentThreadId,
    timelinesByThread,
    statuses,
    statusHeaders,
    threadStore: {
      state: reactive({
  
        pinnedThreadAtById: {},
        archivedThreadAtById: {},
        agentRuntimeById: {},
        skillRevision: 0,
      }),
      getLayout: () => 'focus',
      setLayout: () => { },
      getCmdCardCols: () => 3,
      setCmdCardCols: () => { },
      getSplitRatio: () => 60,
      setSplitRatio: () => { },
      getThreadRailWidth: () => 232,
      setThreadRailWidth: () => { },
      getCurrentThreadId: () => currentThreadId.value,
      saveActiveThread: (value) => { currentThreadId.value = value || ''; },
      saveActiveCmdThread: (value) => { currentThreadId.value = value || ''; },
      getThreadsByMode: () => [{ id: 'thread-active', name: 'Active' }],
      displayName: (thread) => thread.name,
      getThreadStatus: (threadId) => statuses[threadId] || 'idle',
      getThreadStatusHeader: (threadId) => statusHeaders[threadId] || '等待指示',
      getThreadInterruptible: () => true,
      getThreadPinnedAt: () => 0,
      getThreadArchivedAt: () => 0,
      getThreadTimeline: (threadId) => timelinesByThread[threadId] || [],
      loadMessages: async () => ({}),
      stopThread: vi.fn(async () => ({ confirmed: true, settled: true, mode: 'interrupt_confirmed' })),
      getThreadDiff: () => '',
      getThreadStatusDetails: () => '',
      getThreadTokenUsage: () => null,
      getThreadCompacting: () => false,
      getThreadCompactResult: () => null,
      getThreadCompactSuccessCount: () => 0,
      getThreadActivityStats: () => ({}),
      getThreadAlerts: () => [],
      startThread: vi.fn(async () => 'thread-active'),
      sendMessage: vi.fn(async () => ({})),
    },
  };
}

describe('UnifiedChatPage.setup chat rail integration', () => {
  it('shows a DAG designer intake prompt for an empty AI design thread', () => {
    const counters = { display: [], status: [], header: [], interrupt: [] };
    const threadStore = makeThreadStore(counters);
    threadStore.getCurrentThreadId = () => 'thread-design';
    threadStore.getThreadsByMode = () => [{ id: 'thread-design', name: 'AI 设计流程' }];
    threadStore.getThreadTimeline = () => [];
    const projectStore = makeProjectStore();

    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });

    expect(vm.chatEmptyText.value).toBe('我们应该设计点什么？');
  });

  it('does not eagerly build hidden archived cards when archived rail is closed', () => {
    const counters = { display: [], status: [], header: [], interrupt: [] };
    const threadStore = makeThreadStore(counters);
    const projectStore = makeProjectStore();

    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
    const visibleCards = vm.visibleChatThreadCards.value;

    expect(visibleCards.map((item) => item.id)).toEqual(['thread-active']);
    expect(vm.activeChatThreadCount.value).toBe(1);
    expect(vm.archivedChatThreadCount.value).toBe(1);
    expect(new Set(counters.display)).toEqual(new Set(['thread-active']));
    expect(new Set(counters.header.filter(Boolean))).toEqual(new Set(['thread-active']));
    expect(new Set(counters.interrupt.filter(Boolean))).toEqual(new Set(['thread-active']));
  });

  it('switches visible card construction side when archived rail toggles', () => {
    const counters = { display: [], status: [], header: [], interrupt: [] };
    const threadStore = makeThreadStore(counters);
    const projectStore = makeProjectStore();

    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
    const beforeToggle = vm.visibleChatThreadCards.value;
    expect(beforeToggle.map((item) => item.id)).toEqual(['thread-active']);

    vm.toggleArchivedThreadList();

    const afterToggle = vm.visibleChatThreadCards.value;
    expect(afterToggle.map((item) => item.id)).toEqual(['thread-archived']);
    expect(vm.activeChatThreadCount.value).toBe(1);
    expect(vm.archivedChatThreadCount.value).toBe(1);
    expect(new Set(counters.display)).toEqual(new Set(['thread-active', 'thread-archived']));
    expect(new Set(counters.header.filter(Boolean))).toEqual(new Set(['thread-active']));
    expect(new Set(counters.interrupt.filter(Boolean))).toEqual(new Set(['thread-active']));
  });

  it('does not force scroll to bottom when timeline receives new events', async () => {
    const { threadStore, timelinesByThread } = makeAutoScrollThreadStore();
    const projectStore = makeProjectStore();

    UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
    await Promise.resolve();
    await Promise.resolve();
    autoScrollMock.scheduleScrollToBottom.mockClear();

    timelinesByThread['thread-active'].push({
      id: 'assistant-2',
      kind: 'assistant',
      text: '新事件',
      ts: '2026-03-07T10:00:01Z',
    });
    await Promise.resolve();
    await Promise.resolve();

    expect(autoScrollMock.scheduleScrollToBottom).not.toHaveBeenCalled();
  });

  it('enters no-selection state when the selected chat thread is archived', async () => {
    const { threadStore, currentThreadId } = makeAutoScrollThreadStore();
    threadStore.getThreadsByMode = () => [{ id: 'thread-active', name: 'Active' }]
      .filter((thread) => !threadStore.getThreadArchivedAt(thread.id));
    threadStore.getThreadArchivedAt = (threadId) => Number(threadStore.state.archivedThreadAtById[threadId] || 0);
    const projectStore = makeProjectStore();

    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
    expect(vm.selectedThreadId.value).toBe('thread-active');
    expect(vm.noActiveThread.value).toBe(false);

    threadStore.state.archivedThreadAtById['thread-active'] = 123;
    currentThreadId.value = '';
    await flushTicks();

    expect(vm.selectedThreadId.value).toBe('');
    expect(vm.noActiveThread.value).toBe(true);
    expect(vm.activeTimeline.value).toEqual([]);
  });

  it('interrupts the clicked cmd card', async () => {
    const counters = { display: [], status: [], header: [], interrupt: [] };
    const threadStore = makeThreadStore(counters);
    const projectStore = makeProjectStore();

    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'cmd' });

    vm.stopCard('thread-active');
    await Promise.resolve();
    await Promise.resolve();

    expect(threadStore.stopThread).toHaveBeenCalledWith('thread-active', { source: 'ui_stop' });
  });
  it('copies current cwd log path in copied thread payload', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-03-08T06:41:26.626Z'));
    try {
      const counters = { display: [], status: [], header: [], interrupt: [] };
      const threadStore = makeThreadStore(counters);
      threadStore.getCurrentThreadId = () => 'thread-active';
      threadStore.state.agentRuntimeById = {
        'thread-active': {
          providerThreadId: 'provider-thread-1',
          port: 4501,
          provider: 'claude',
          effort: 'max',
          cwd: '/Users/mima0000/Desktop/wj/go-agent-v2',
          logPath: '/Users/mima0000/Desktop/wj/go-agent-v2/agent-terminal-2026-03-08-1.log',
        },

      };
      const projectStore = {
        ...makeProjectStore(),
        state: { active: '/Users/mima0000/Desktop/wj/go-agent-v2' },
      };
      vi.mocked(callAPI).mockImplementation(async (method) => {
        if (method === 'ui/preferences/get') return 'claude-3.7-sonnet';
        return {};
      });

      const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
      await vm.copySelectedThreadId();

      expect(copyTextToClipboard).toHaveBeenCalledTimes(1);
      const payload = JSON.parse(copyTextToClipboard.mock.calls[0][0]);
      expect(payload.cwd).toBe('/Users/mima0000/Desktop/wj/go-agent-v2');
      expect(payload.effort).toBe('max');
      expect(payload['log-path']).toBe('/Users/mima0000/Desktop/wj/go-agent-v2/agent-terminal-2026-03-08-1.log');
      expect(payload.copiedAt).toBe('2026-03-08 14:41:26 UTC+8');
    } finally {
      vi.useRealTimers();
    }
  });
  it('starts a thread and sends composer text through send()', async () => {
    const { threadStore, currentThreadId } = makeAutoScrollThreadStore();
    currentThreadId.value = '';
    threadStore.startThread.mockResolvedValue('thread-new');
    composerStoreMock.state.text = 'hello split';
    composerStoreMock.state.attachments = [{ path: '/tmp/a.txt' }];
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '/workspace/chat', showModal: false, projects: ['/workspace/chat'] }) };
    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
    await vm.send();
    expect(threadStore.startThread).toHaveBeenCalledWith('/workspace/chat', expect.objectContaining({ focusMode: 'chat', deferSpawn: true, launchIntentId: expect.stringMatching(/^launch_/) }));
    expect(threadStore.sendMessage).toHaveBeenCalledWith('thread-new', 'hello split', [{ path: '/tmp/a.txt' }], expect.objectContaining({ cwd: '/workspace/chat' }));
    expect(composerStoreMock.clearComposer).toHaveBeenCalled();
    expect(autoScrollMock.scheduleScrollToBottom).toHaveBeenCalledWith(true);
  });

  it('binds composer draft scope to the selected chat thread', async () => {
    const { threadStore, currentThreadId } = makeAutoScrollThreadStore();
    currentThreadId.value = 'thread-active';
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '/workspace/chat', showModal: false, projects: ['/workspace/chat'] }) };

    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });

    expect(composerStoreMock.activateDraft).toHaveBeenCalledWith('thread-active', 'chat');

    vm.selectedThreadId.value = '';
    expect(composerStoreMock.activateDraft).toHaveBeenLastCalledWith('', 'chat');
  });

  it('does not request skill catalog from chat composer setup', async () => {
    const counters = { display: [], status: [], header: [], interrupt: [] };
    const threadStore = makeThreadStore(counters);
    threadStore.getPreferenceScopeCwd = () => '/scoped/project';
    const projectStore = {
      ...makeProjectStore(),
      state: reactive({ active: '/project-fallback', showModal: false, projects: ['/project-fallback'], features: {} }),
    };
    vi.mocked(callAPI).mockImplementation(async (method) => {
      if (method === 'skills/list') return { skills: [] };
      return {};
    });

    UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
    await flushTicks(4);

    expect(callAPI).not.toHaveBeenCalledWith('skills/list', { cwd: '/scoped/project' });
  });

  it('confirms interruptCurrent when stopThread settles', async () => {
    const { threadStore } = makeAutoScrollThreadStore();
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '.', showModal: false, projects: ['.'] }) };
    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
    const confirm = vi.fn();
    const reject = vi.fn();
    await vm.interruptCurrent({ threadId: 'thread-active', confirm, reject });
    expect(confirm).toHaveBeenCalledWith({ mode: 'interrupt_confirmed', threadId: 'thread-active' });
    expect(reject).not.toHaveBeenCalled();
  });

  it('wires Escape handler after mount and stops selected thread', async () => {
    const { threadStore } = makeAutoScrollThreadStore();
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '.', showModal: false, projects: ['.'] }) };
    UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
    await runMountedHooks();
    const handler = globalThis.window.addEventListener.mock.calls[0][1];
    const event = { key: 'Escape', preventDefault: vi.fn(), target: null };
    handler(event);
    await flushTicks();
    expect(event.preventDefault).toHaveBeenCalled();
    expect(threadStore.stopThread).toHaveBeenCalledWith('thread-active', { source: 'ui_stop' });
  });

  it('pauses and resumes activeStatusMeta timer around modal visibility', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-03-09T00:00:00Z'));
    let intervalCallback = () => {};
    globalThis.window.setInterval = vi.fn((cb) => { intervalCallback = cb; return 7; });
    globalThis.window.clearInterval = vi.fn();
    try {
      const { threadStore } = makeAutoScrollThreadStore();
      const projectStore = { ...makeProjectStore(), state: reactive({ active: '.', showModal: false, projects: ['.'] }) };
      const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
      expect(globalThis.window.setInterval).toHaveBeenCalledWith(expect.any(Function), 1000);
      vi.setSystemTime(new Date('2026-03-09T00:00:05Z')); intervalCallback(); expect(vm.activeStatusMeta.value).toContain('5s');
      projectStore.state.showModal = true; await flushTicks(); expect(globalThis.window.clearInterval).toHaveBeenCalled();

      vi.setSystemTime(new Date('2026-03-09T00:00:15Z')); projectStore.state.showModal = false; await flushTicks();
      vi.setSystemTime(new Date('2026-03-09T00:00:17Z')); intervalCallback(); expect(vm.activeStatusMeta.value).toContain('7s');
      vm.showPathChoiceModal.value = true; await flushTicks(); expect(globalThis.window.clearInterval).toHaveBeenCalledTimes(2);
      vi.setSystemTime(new Date('2026-03-09T00:00:23Z')); vm.showPathChoiceModal.value = false; await flushTicks();
      expect(globalThis.window.setInterval.mock.calls.every(([, delay]) => delay === 1000)).toBe(true);
      vi.setSystemTime(new Date('2026-03-09T00:00:25Z')); intervalCallback(); expect(vm.activeStatusMeta.value).toContain('9s');
    } finally { vi.useRealTimers(); }

  });

  it('does not show elapsed status meta for archived selected thread', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-03-09T00:00:00Z'));
    try {
      const { threadStore, statuses, statusHeaders } = makeAutoScrollThreadStore();
      statuses['thread-active'] = 'archived';
      statusHeaders['thread-active'] = '已归档';
      const projectStore = { ...makeProjectStore(), state: reactive({ active: '.', showModal: false, projects: ['.'] }) };
      const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });

      expect(vm.displayStatusText.value).toBe('已归档');
      expect(vm.activeStatusMeta.value).toBe('');
      expect(globalThis.window.setInterval).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('submits inline rename on Enter and keeps edit mode for save-button blur', async () => {
    const { threadStore } = makeAutoScrollThreadStore();
    threadStore.renameThread = vi.fn(async () => ({}));
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '.', showModal: false, projects: ['.'] }) };
    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });

    vm.beginInlineRename('thread-active');
    vm.editingAlias.value = 'Renamed';
    const event = { preventDefault: vi.fn(), isComposing: false };
    vm.handleInlineRenameEnter(event, 'thread-active');
    vm.handleInlineRenameBlur({ relatedTarget: { dataset: { renameSaveButtonFor: 'thread-active' } } }, 'thread-active');
    expect(vm.editingThreadId.value).toBe('thread-active');

    await flushTicks();
    expect(threadStore.renameThread).toHaveBeenCalledWith('thread-active', 'Renamed');
  });


  it('focuses same-thread diff selection from timeline file ref click without hiding sibling files', async () => {
    const { threadStore } = makeAutoScrollThreadStore();
    threadStore.state.diffTextByThread = reactive({ 'thread-active': ['diff --git a/src/a.js b/src/a.js', '--- a/src/a.js', '+++ b/src/a.js', '@@ -1,1 +1,2 @@', ' line1', '+added', 'diff --git a/src/b.js b/src/b.js', '--- a/src/b.js', '+++ b/src/b.js', '@@ -1,1 +1,2 @@', ' line9', '+other'].join('\n') });
    threadStore.getThreadDiff = (threadId) => threadStore.state.diffTextByThread[threadId] || '';
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '/workspace/chat', showModal: false, projects: ['/workspace/chat'] }) };
    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
    await vm.onTimelineFileRefClick({ path: 'src/a.js', line: 2 });
    expect(vm.activeDiffFocusFile.value).toBe('src/a.js');
    expect(vm.activeDiffFocusLine.value).toBe(2);
    expect(vm.activeDiffText.value).toContain('+added');
    expect(vm.activeDiffText.value).toContain('diff --git a/src/b.js b/src/b.js');
  });

  it('keeps the current thread binding when file ref only exists in another diff cache', async () => {
    const { threadStore, currentThreadId } = makeAutoScrollThreadStore();
    threadStore.getThreadsByMode = () => [{ id: 'thread-active', name: 'Active' }, { id: 'thread-other', name: 'Other' }];
    threadStore.state.diffTextByThread = reactive({ 'thread-active': '', 'thread-other': ['diff --git a/src/b.js b/src/b.js', '--- a/src/b.js', '+++ b/src/b.js', '@@ -1,1 +1,2 @@', ' line1', '+other'].join('\n') });
    threadStore.getThreadDiff = (threadId) => threadStore.state.diffTextByThread[threadId] || '';
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '/workspace/chat', showModal: false, projects: ['/workspace/chat'] }) };
    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
    await vm.onTimelineFileRefClick({ path: 'src/b.js', line: 1 });
    await flushTicks(4);
    expect(currentThreadId.value).toBe('thread-active');
    expect(vm.activeDiffFocusFile.value).toBe('src/b.js');
  });

  it('uses backend fallbacks for recoverSelected and openNewWindow', async () => {
    const { threadStore } = makeAutoScrollThreadStore();
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '/workspace/chat', showModal: false, projects: ['/workspace/chat'] }) };
    vi.mocked(callAPI).mockImplementation(async (method) => {
      if (method === 'thread/recover') return {};
      if (method === 'ui/selectProjectDir') return { path: '/workspace/new-window' };
      if (method === 'ui/openNewWindow') return {};
      return {};
    });
    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
    await vm.recoverSelected();
    await vm.openNewWindow();
    expect(callAPI).toHaveBeenCalledWith('thread/recover', { threadId: 'thread-active' });
    expect(globalThis.window.alert).toHaveBeenCalledWith('已触发进程恢复，请等待连接重建。');
    expect(callAPI).toHaveBeenCalledWith('ui/openNewWindow', { cwd: '/workspace/new-window' });
  });

  it('shows a path choice modal when locate returns multiple matches', async () => {
    const { threadStore } = makeAutoScrollThreadStore();
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '/workspace/chat', showModal: false, projects: ['/workspace/chat'] }) };
    vi.mocked(callAPI).mockImplementation(async (method) => {
      if (method === 'ui/code/locate') {
        return {
          ok: true,
          paths: ['/workspace/chat/src/a.js', '/workspace/chat/lib/src/a.js'],
          truncated: true,
        };
      }
      return {};
    });
    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });

    const pending = vm.onTimelineFileRefClick({ path: 'src/a.js', line: 3, column: 1 });
    await flushTicks();

    expect(vm.showPathChoiceModal.value).toBe(true);
    expect(vm.pathChoiceOptions.value).toEqual(['/workspace/chat/src/a.js', '/workspace/chat/lib/src/a.js']);
    expect(vm.pathChoiceTitle.value).toBe('选择 src/a.js 的匹配路径');
    expect(vm.pathChoiceTruncated.value).toBe(true);

    vm.cancelPathChoice();
    await pending;
  });

  it('continues code open after confirming a path choice', async () => {
    const { threadStore } = makeAutoScrollThreadStore();
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '/workspace/chat', showModal: false, projects: ['/workspace/chat'] }) };
    vi.mocked(callAPI).mockImplementation(async (method, payload) => {
      if (method === 'ui/code/locate') {
        return {
          ok: true,
          paths: ['/workspace/chat/src/a.js', '/workspace/chat/lib/src/a.js'],
          truncated: false,
        };
      }
      if (method === 'ui/code/open') {
        return {
          ok: true,
          relative: 'lib/src/a.js',
          startLine: 3,
          endLine: 3,
          snippet: [{ line: 3, text: `picked:${payload.filePath}` }],
        };
      }
      return {};
    });
    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });

    const pending = vm.onTimelineFileRefClick({ path: 'src/a.js', line: 3, column: 1 });
    await flushTicks();
    vm.confirmPathChoice('/workspace/chat/lib/src/a.js');
    await pending;

    expect(callAPI).toHaveBeenCalledWith('ui/code/open', expect.objectContaining({
      filePath: '/workspace/chat/lib/src/a.js',
      line: 3,
      column: 1,
    }));
    expect(vm.activeDiffFocusFile.value).toBe('lib/src/a.js');
    expect(vm.activeDiffText.value).toContain('picked:/workspace/chat/lib/src/a.js');
  });

  it('does not pollute the current preview when a path choice is cancelled', async () => {
    const { threadStore } = makeAutoScrollThreadStore();
    threadStore.state.diffTextByThread = reactive({
      'thread-active': [
        'diff --git a/src/existing.js b/src/existing.js',
        '--- a/src/existing.js',
        '+++ b/src/existing.js',
        '@@ -1,1 +1,2 @@',
        ' line1',
        '+keep',
      ].join('\n'),
    });
    threadStore.getThreadDiff = (threadId) => threadStore.state.diffTextByThread[threadId] || '';
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '/workspace/chat', showModal: false, projects: ['/workspace/chat'] }) };
    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
    await vm.onTimelineFileRefClick({ path: 'src/existing.js', line: 2 });

    vi.mocked(callAPI).mockImplementation(async (method) => {
      if (method === 'ui/code/locate') {
        return {
          ok: true,
          paths: ['/workspace/chat/src/a.js', '/workspace/chat/lib/src/a.js'],
          truncated: false,
        };
      }
      return {};
    });

    const pending = vm.onTimelineFileRefClick({ path: 'src/a.js', line: 3, column: 1 });
    await flushTicks();
    vm.cancelPathChoice();
    await pending;

    expect(vm.activeDiffFocusFile.value).toBe('src/existing.js');
    expect(vm.activeDiffText.value).toContain('+keep');
    // path-choice 验证：仅 cancel 后不应再调 ui/code/locate；其它非 locate 调用允许。
    const locateCalls = vi.mocked(callAPI).mock.calls.filter(([m]) => m === 'ui/code/locate');
    expect(locateCalls).toHaveLength(1);
  });

  it('settles pending path choice promise on component unmount', async () => {
    const { threadStore } = makeAutoScrollThreadStore();
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '/workspace/chat', showModal: false, projects: ['/workspace/chat'] }) };
    vi.mocked(callAPI).mockImplementation(async (method) => {
      if (method === 'ui/code/locate') {
        return {
          ok: true,
          paths: ['/workspace/chat/src/a.js', '/workspace/chat/lib/src/a.js'],
          truncated: false,
        };
      }
      return {};
    });
    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });

    const pending = vm.onTimelineFileRefClick({ path: 'src/a.js', line: 3, column: 1 });
    await flushTicks();
    expect(vm.showPathChoiceModal.value).toBe(true);

    // Simulate component unmount while modal is open
    await runUnmountedHooks();

    // The pending promise should settle (not hang forever)
    await pending;
    expect(vm.showPathChoiceModal.value).toBe(false);
  });

  it('deduplicates normalized path choice options', async () => {
    const { threadStore } = makeAutoScrollThreadStore();
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '/workspace/chat', showModal: false, projects: ['/workspace/chat'] }) };
    vi.mocked(callAPI).mockImplementation(async (method) => {
      if (method === 'ui/code/locate') {
        return {
          ok: true,
          paths: ['/workspace/chat/src/a.js', ' /workspace/chat/src/a.js ', '/workspace/chat/lib/a.js'],
          truncated: false,
        };
      }
      return {};
    });
    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });

    const pending = vm.onTimelineFileRefClick({ path: 'src/a.js', line: 1, column: 1 });
    await flushTicks();

    // After dedup, the two '/workspace/chat/src/a.js' entries should collapse to one
    expect(vm.pathChoiceOptions.value).toEqual(['/workspace/chat/src/a.js', '/workspace/chat/lib/a.js']);

    vm.cancelPathChoice();
    await pending;
  });

  it('lets Escape close the path modal before any thread interrupt is triggered', async () => {
    const { threadStore } = makeAutoScrollThreadStore();
    const projectStore = { ...makeProjectStore(), state: reactive({ active: '/workspace/chat', showModal: false, projects: ['/workspace/chat'] }) };
    vi.mocked(callAPI).mockImplementation(async (method) => {
      if (method === 'ui/code/locate') {
        return {
          ok: true,
          paths: ['/workspace/chat/src/a.js', '/workspace/chat/lib/src/a.js'],
          truncated: false,
        };
      }
      return {};
    });
    const vm = UnifiedChatPage.setup({ threadStore, projectStore, mode: 'chat' });
    await runMountedHooks();

    const pending = vm.onTimelineFileRefClick({ path: 'src/a.js', line: 3, column: 1 });
    await flushTicks();
    const globalEscape = globalThis.window.addEventListener.mock.calls.find(([type]) => type === 'keydown')[1];
    globalEscape({ key: 'Escape', preventDefault: vi.fn(), target: { tagName: 'TEXTAREA', id: 'chatInput', closest: () => null } });
    await flushTicks();
    expect(threadStore.stopThread).not.toHaveBeenCalled();

    const modalVm = PathChoiceModal.setup({
      show: true,
      options: vm.pathChoiceOptions.value,
      title: vm.pathChoiceTitle.value,
      truncated: vm.pathChoiceTruncated.value,
      onConfirm: vm.confirmPathChoice,
      onCancel: vm.cancelPathChoice,
    });
    const event = { preventDefault: vi.fn() };
    modalVm.onEscapeKey(event);
    await pending;

    expect(event.preventDefault).toHaveBeenCalled();
    expect(vm.showPathChoiceModal.value).toBe(false);
    expect(threadStore.stopThread).not.toHaveBeenCalled();
  });

});
