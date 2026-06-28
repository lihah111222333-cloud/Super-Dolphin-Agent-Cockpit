// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const composer = vi.hoisted(() => ({
  state: { text: '', attachments: [] },
  clearComposer: vi.fn(),
  attachByPaths: vi.fn(() => 0),
}));
const provider = vi.hoisted(() => ({ load: vi.fn(async () => {}), toggle: vi.fn(async () => {}) }));

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    ...actual,
    onMounted: () => {},
    onBeforeUnmount: () => {},
  };
});

vi.mock('./stores/composer.js', () => ({
  useComposerStore: () => composer,
}));

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(async () => ({})),
  copyTextToClipboard: vi.fn(async () => true),
  onFilesDropped: vi.fn(() => () => {}),
  resolveThreadIdentity: vi.fn(async () => ({})),
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./composables/useProviderMode.js', async () => {
  const { ref } = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    useProviderMode: () => ({
      useClaudeProvider: ref(false),
      loadProviderPreference: provider.load,
      toggleProviderMode: provider.toggle,
    }),
  };
});

vi.mock('./composables/useAutoScroll.js', async () => {
  const { ref } = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    useAutoScroll: () => ({
      scheduleScrollToBottom: vi.fn(),
      scrollToTop: vi.fn(),
      resetScrollState: vi.fn(),
      isAtBottom: ref(true),
    }),
  };
});

vi.mock('./composables/useResizePanels.js', async () => {
  const { ref, computed } = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    useResizePanels: () => ({
      dragging: ref(false),
      threadRailDragging: ref(false),
      activityPanelDragging: ref(false),
      splitRatio: ref(60),
      threadRailStyle: computed(() => ({})),
      chatComposerShellStyle: computed(() => ({})),
      activityPanelRowStyle: computed(() => ({})),
      onResizeStart: vi.fn(),
      onThreadRailResizeStart: vi.fn(),
      onActivityResizeStart: vi.fn(),
    }),
  };
});

vi.mock('./composables/useDiffPreview.js', async () => {
  const { ref, computed } = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    useDiffPreview: (opts) => {
      const focusedDiffPath = ref('');
      const focusedDiffLine = ref(0);
      const pendingFileRefFocus = ref(null);
      const fallbackDiffText = ref('');
      const fallbackMediaPreview = ref(null);
      const fallbackMarkdownPreview = ref(null);
      return {
        focusedDiffPath,
        focusedDiffLine,
        pendingFileRefFocus,
        fallbackDiffText,
        fallbackMediaPreview,
        fallbackMarkdownPreview,
        activeMediaPreview: computed(() => fallbackMediaPreview.value),
        activeMarkdownPreview: computed(() => fallbackMarkdownPreview.value),
        activeDiffText: computed(() => fallbackDiffText.value || (opts.activeThreadDiffText.value || '')),
        activeDiffFocusFile: computed(() => focusedDiffPath.value),
        activeDiffFocusLine: computed(() => focusedDiffLine.value),
        timelinePreview: vi.fn(() => []),
        diffPreview: vi.fn(() => ''),
      };
    },
  };
});

import { nextTick, reactive, ref } from '../lib/vue.esm-browser.prod.js';
import { UnifiedChatPage } from './pages/UnifiedChatPage.js';

const flush = async () => {
  await Promise.resolve();
  await Promise.resolve();
  await nextTick();
};

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function buildConfig(threadId, overrides = {}) {
  return {
    threadId,
    provider: overrides.provider ?? 'codex',
    supportsThreadOverride: overrides.supportsThreadOverride ?? (overrides.provider ?? 'codex') === 'codex',
    override: overrides.override ?? { model: '', effort: '' },
    effective: overrides.effective ?? { model: 'gpt-5.5', effort: 'xhigh' },
  };
}

function makeStores(opts = {}) {
  const current = ref(opts.selectedId ?? 'thread-a');
  const threads = reactive(opts.threads ?? [
    { id: 'thread-a', name: 'Thread A' },
    { id: 'thread-b', name: 'Thread B' },
    { id: 'thread-claude', name: 'Claude Thread' },
  ]);
  const runtime = reactive(opts.runtime ?? {
    'thread-a': { provider: 'codex' },
    'thread-b': { provider: 'codex' },
    'thread-claude': { provider: 'claude' },
  });
  const getThreadConfig = opts.getThreadConfig ?? vi.fn(async (threadId) => buildConfig(threadId, { provider: runtime[threadId]?.provider || 'codex' }));
  const setThreadConfig = opts.setThreadConfig ?? vi.fn(async (threadId, config) => buildConfig(threadId, {
    provider: runtime[threadId]?.provider || 'codex',
    override: { model: config?.model || '', effort: config?.effort || '' },
    effective: { model: config?.model || 'gpt-5.5', effort: config?.effort || 'xhigh' },
  }));
  const store = {
    state: reactive({
      pinnedThreadAtById: {},
      archivedThreadAtById: {},
      agentRuntimeById: runtime,
      diffTextByThread: {},
      skillRevision: 0,
      agentMetaById: {},
    }),
    getLayout: vi.fn(() => 'focus'),
    setLayout: vi.fn(),
    getCmdCardCols: vi.fn(() => 3),
    setCmdCardCols: vi.fn(),
    getSplitRatio: vi.fn(() => 60),
    setSplitRatio: vi.fn(),
    getThreadRailWidth: vi.fn(() => 232),
    setThreadRailWidth: vi.fn(),
    getCurrentThreadId: vi.fn(() => current.value),
    saveActiveThread: vi.fn((value) => { current.value = value || ''; }),
    saveActiveCmdThread: vi.fn((value) => { current.value = value || ''; }),
    getThreadsByMode: vi.fn(() => threads),
    displayName: vi.fn((thread) => thread?.name || thread?.id || ''),
    getThreadStatus: vi.fn(() => 'idle'),
    getThreadStatusHeader: vi.fn(() => ''),
    getThreadInterruptible: vi.fn(() => false),
    getThreadPinnedAt: vi.fn(() => 0),
    getThreadArchivedAt: vi.fn(() => 0),
    getThreadTimeline: vi.fn(() => []),
    getThreadDiff: vi.fn(() => ''),
    getThreadStatusDetails: vi.fn(() => ''),
    getThreadTokenUsage: vi.fn(() => null),
    getThreadCompacting: vi.fn(() => false),
    getThreadCompactResult: vi.fn(() => null),
    getThreadCompactSuccessCount: vi.fn(() => 0),
    getThreadActivityStats: vi.fn(() => ({})),
    getThreadAlerts: vi.fn(() => []),
    startThread: vi.fn(async () => 'thread-started'),
    sendMessage: vi.fn(async () => ({})),
    stopThread: vi.fn(async () => ({ confirmed: true, settled: true, mode: 'interrupt_confirmed' })),
    compactThread: vi.fn(async () => ({})),
    forceCompleteThread: vi.fn(async () => ({})),
    recoverThread: vi.fn(async () => ({})),
    loadMessages: vi.fn(async () => ({})),
    renameThread: vi.fn(async () => ({})),
    promptRenameThread: vi.fn(),
    toggleThreadPin: vi.fn(),
    toggleThreadArchive: vi.fn(async () => ({})),
    getThreadConfig,
    setThreadConfig,
  };
  const projectStore = {
    state: reactive({ active: '/repo', showModal: false, projects: ['/repo'] }),
    projectOptions: { value: [] },
    setActive: vi.fn(),
    quickAdd: vi.fn(),
    removeProject: vi.fn(),
  };
  return { store, projectStore, current, getThreadConfig, setThreadConfig };
}

async function createVm(opts = {}) {
  const parts = makeStores(opts);
  const vm = UnifiedChatPage.setup({ threadStore: parts.store, projectStore: parts.projectStore, mode: 'chat' });
  await flush();
  return { vm, ...parts };
}

beforeEach(() => {
  composer.state.text = '';
  composer.state.attachments = [];
  composer.clearComposer.mockReset();
  composer.attachByPaths.mockReset().mockReturnValue(0);
  provider.load.mockClear();
  provider.toggle.mockClear();
  globalThis.window = {
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    setTimeout: (...args) => setTimeout(...args),
    clearTimeout: (id) => clearTimeout(id),
    setInterval: (...args) => setInterval(...args),
    clearInterval: (id) => clearInterval(id),
    alert: vi.fn(),
  };
  globalThis.document = {
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    querySelector: vi.fn(() => null),
    activeElement: null,
  };
});

describe('UnifiedChatPage thread config behavior', () => {
  it('hydrates on thread switch and ignores stale responses', async () => {
    const first = deferred();
    const second = deferred();
    const getThreadConfig = vi.fn((threadId) => (threadId === 'thread-a' ? first.promise : second.promise));
    const created = await createVm({ getThreadConfig });
    const vm = created.vm;

    expect(getThreadConfig).toHaveBeenCalledWith('thread-a');

    vm.selectThread('thread-b');
    await flush();
    expect(getThreadConfig).toHaveBeenCalledWith('thread-b');

    second.resolve(buildConfig('thread-b', {
      override: { model: 'gpt-5.2', effort: 'high' },
      effective: { model: 'gpt-5.2', effort: 'high' },
    }));
    await flush();

    expect(vm.threadConfigUi.meta.threadId).toBe('thread-b');
    expect(vm.threadConfigUi.draft.model).toBe('gpt-5.2');
    expect(vm.threadConfigUi.loading).toBe(false);

    first.resolve(buildConfig('thread-a', {
      override: { model: 'stale-model', effort: 'low' },
      effective: { model: 'stale-model', effort: 'low' },
    }));
    await flush();

    expect(vm.threadConfigUi.meta.threadId).toBe('thread-b');
    expect(vm.threadConfigUi.draft.model).toBe('gpt-5.2');
    expect(vm.threadConfigUi.draft.effort).toBe('high');
  });

  it('saves thread config, refreshes local state, and restores inherit with empty values', async () => {
	const getThreadConfig = vi.fn()
	  .mockResolvedValueOnce(buildConfig('thread-a'));
	const setThreadConfig = vi.fn()
	  .mockResolvedValueOnce(buildConfig('thread-a', {
	    override: { model: 'gpt-5.2', effort: 'high' },
	    effective: { model: 'gpt-5.2', effort: 'high' },
	  }))
	  .mockResolvedValueOnce(buildConfig('thread-a'));
	const { vm } = await createVm({ getThreadConfig, setThreadConfig });

	vm.updateThreadConfigModel('gpt-5.2');
	vm.updateThreadConfigEffort('high');
	await vm.saveThreadConfigDraft();

	expect(setThreadConfig).toHaveBeenNthCalledWith(1, 'thread-a', { model: 'gpt-5.2', effort: 'high' });
	expect(getThreadConfig).toHaveBeenCalledTimes(1);
	expect(vm.threadConfigUi.notice).toContain('下次发送生效');
	expect(vm.threadConfigUi.meta.override.model).toBe('gpt-5.2');
	expect(vm.threadConfigUi.meta.effective.effort).toBe('high');

	await vm.restoreThreadConfigInherit();

	expect(setThreadConfig).toHaveBeenNthCalledWith(2, 'thread-a', { model: '', effort: '' });
	expect(getThreadConfig).toHaveBeenCalledTimes(1);
	expect(vm.threadConfigUi.notice).toContain('已恢复继承');
	expect(vm.threadConfigUi.meta.override.model).toBe('');
	expect(vm.threadConfigUi.meta.effective.model).toBe('gpt-5.5');
  });

  it('surfaces a clear notice when the backend rejects busy-thread mutations', async () => {
    const setThreadConfig = vi.fn(async () => {
      throw new Error('thread thread-a is busy; stop current execution before changing launch config');
    });
    const { vm } = await createVm({ setThreadConfig });

    vm.updateThreadConfigModel('gpt-5.2');
    await vm.saveThreadConfigDraft();

    expect(vm.threadConfigUi.noticeLevel).toBe('warning');
    expect(vm.threadConfigUi.notice).toContain('当前线程正在执行');
  });
});
