// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const composerStoreMock = vi.hoisted(() => ({
  state: {
    text: '',
    attachments: [],
  },
  attachByPaths: vi.fn(() => 0),
  clearComposer: vi.fn(),
}));

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    ...actual,
    onMounted: () => {},
    onBeforeUnmount: () => {},
  };
});

import { reactive, ref } from '../lib/vue.esm-browser.prod.js';

vi.mock('./stores/composer.js', () => ({
  useComposerStore: () => composerStoreMock,
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

vi.mock('./composables/useAutoScroll.js', () => ({
  useAutoScroll: () => ({
    scheduleScrollToBottom: vi.fn(),
  }),
}));

import { UnifiedChatPage } from './pages/UnifiedChatPage.js';

beforeEach(() => {
  composerStoreMock.state.text = '';
  composerStoreMock.state.attachments = [];
  composerStoreMock.attachByPaths.mockReset();
  composerStoreMock.attachByPaths.mockImplementation(() => 0);
  composerStoreMock.clearComposer.mockReset();
  composerStoreMock.clearComposer.mockImplementation(() => {});

  // Clear plan dismiss state to prevent bleed between tests
  try { sessionStorage.removeItem('__plan_dismissed_v2__'); } catch {}

  globalThis.window = {
    ...(globalThis.window || {}),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    setTimeout: vi.fn(() => 1),
    clearTimeout: vi.fn(),
    setInterval: vi.fn(() => 1),
    clearInterval: vi.fn(),
  };
  globalThis.document = {
    ...(globalThis.document || {}),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    querySelector: vi.fn(() => null),
    activeElement: null,
  };
});

function makeProjectStore() {
  return {
    state: reactive({ active: '.', showModal: false, projects: ['.'] }),
    projectOptions: { value: [] },
    setActive: () => {},
  };
}

function makeThreadStore(timeline) {
  const currentThreadId = ref('thread-active');
  const timelinesByThread = reactive({ 'thread-active': timeline || [] });
  return {
    state: reactive({

      pinnedThreadAtById: {},
      archivedThreadAtById: {},
      agentRuntimeById: {},
      skillRevision: 0,
    }),
    getLayout: () => 'mix',
    setLayout: () => {},
    getCmdCardCols: () => 3,
    setCmdCardCols: () => {},
    getSplitRatio: () => 60,
    setSplitRatio: () => {},
    getThreadRailWidth: () => 232,
    setThreadRailWidth: () => {},
    getCurrentThreadId: () => currentThreadId.value,
    saveActiveThread: (value) => { currentThreadId.value = value || ''; },
    saveActiveCmdThread: (value) => { currentThreadId.value = value || ''; },
    getThreadsByMode: () => [{ id: 'thread-active', name: 'Active' }],
    displayName: (thread) => thread.name,
    getThreadStatus: () => 'idle',
    getThreadStatusHeader: () => '等待指示',
    getThreadInterruptible: () => false,
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
  };
}

describe('plan split barrier via UnifiedChatPage public outputs', () => {
  it('builds and dismisses active pinned plans using stable keys', () => {
    const vm = UnifiedChatPage.setup({
      threadStore: makeThreadStore([{ kind: 'plan', id: 'plan-1', text: '1. add tests', done: false }]),
      projectStore: makeProjectStore(),
      mode: 'chat',
    });

    expect(vm.activePinnedPlan.value).toMatchObject({ id: 'plan-1', key: 'id:plan-1', statusText: '进行中', text: '1. add tests' });
    vm.dismissPinnedPlan();
    expect(vm.activePinnedPlan.value).toBeNull();
  });

  it('falls back to timestamp-based keys when a plan item has no id', () => {
    const vm = UnifiedChatPage.setup({
      threadStore: makeThreadStore([{ kind: 'plan', ts: '2026-03-09T10:00:00Z', text: 'review docs', done: true }]),
      projectStore: makeProjectStore(),
      mode: 'chat',
    });

    expect(vm.activePinnedPlan.value.key).toBe('ts:2026-03-09T10:00:00Z');
    expect(vm.activePinnedPlan.value.statusText).toBe('完成');
  });

  it('does not pin a completed plan after a newer user task starts', () => {
    const vm = UnifiedChatPage.setup({
      threadStore: makeThreadStore([
        { kind: 'plan', id: 'plan-old', text: '旧任务计划', done: true },
        { kind: 'assistant', id: 'assistant-1', text: '旧任务已完成', done: true },
        { kind: 'user', id: 'user-2', text: '开始一个新任务' },
      ]),
      projectStore: makeProjectStore(),
      mode: 'chat',
    });

    expect(vm.activePinnedPlan.value).toBeNull();
  });

  it('does not pin stale plans after a newer user instruction when the old plan stays in progress', () => {
    const vm = UnifiedChatPage.setup({
      threadStore: makeThreadStore([
        { kind: 'plan', id: 'plan-old', text: '旧任务计划', done: false },
        { kind: 'assistant', id: 'assistant-1', text: '旧任务已完成', done: true },
        { kind: 'user', id: 'user-2', text: '回收子agent' },
      ]),
      projectStore: makeProjectStore(),
      mode: 'chat',
    });

    expect(vm.activePinnedPlan.value).toBeNull();
  });

  it('splits json-render spec blocks inside pinnedPlanCardSpec', () => {
    const vm = UnifiedChatPage.setup({ threadStore: makeThreadStore([]), projectStore: makeProjectStore(), mode: 'chat' });
    const spec = vm.pinnedPlanCardSpec({
      done: false,
      text: ['前文', '```json-render', '{"type":"Text","text":"Spec body"}', '```', '尾注'].join('\n'),
    });

    expect(spec.type).toBe('Card');
    expect(spec.children.map((child) => child.type)).toEqual(['Stack', 'Separator', 'Markdown', 'Text', 'Markdown']);
    expect(spec.children[2].text).toContain('前文');
    expect(spec.children[3]).toEqual({ type: 'Text', text: 'Spec body' });
    expect(spec.children[4].text).toContain('尾注');
  });
});
