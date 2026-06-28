// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const lifecycleMock = vi.hoisted(() => ({ mounted: [], unmounted: [] }));
const composerStoreMock = vi.hoisted(() => ({ state: { text: '', attachments: [] }, attachByPaths: vi.fn(() => 0), clearComposer: vi.fn() }));

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return { ...actual, onMounted: (cb) => lifecycleMock.mounted.push(cb), onBeforeUnmount: (cb) => lifecycleMock.unmounted.push(cb) };
});

import { reactive, ref } from '../lib/vue.esm-browser.prod.js';

vi.mock('./stores/composer.js', () => ({ useComposerStore: () => composerStoreMock }));
vi.mock('./services/api.js', () => ({ callAPI: vi.fn(async () => ({})), copyTextToClipboard: vi.fn(async () => true), onFilesDropped: vi.fn(() => () => {}), resolveThreadIdentity: vi.fn(async () => ({})) }));
vi.mock('./services/log.js', () => ({ logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn() }));
vi.mock('./composables/useAutoScroll.js', () => ({ useAutoScroll: () => ({ scheduleScrollToBottom: vi.fn() }) }));

import { UnifiedChatPage } from './pages/UnifiedChatPage.js';

beforeEach(() => {
  lifecycleMock.mounted.length = 0;
  lifecycleMock.unmounted.length = 0;
  composerStoreMock.state.text = '';
  composerStoreMock.state.attachments = [];
  composerStoreMock.attachByPaths.mockReset();
  composerStoreMock.attachByPaths.mockImplementation(() => 0);
  composerStoreMock.clearComposer.mockReset();
  globalThis.window = { ...(globalThis.window || {}), addEventListener: vi.fn(), removeEventListener: vi.fn(), setTimeout: vi.fn(() => 1), clearTimeout: vi.fn(), setInterval: vi.fn(() => 1), clearInterval: vi.fn(), alert: vi.fn() };
  globalThis.document = { ...(globalThis.document || {}), addEventListener: vi.fn(), removeEventListener: vi.fn(), querySelector: vi.fn(() => null), activeElement: null };
});

function makeProjectStore() {
  return { state: reactive({ active: '/workspace/chat', showModal: false, projects: ['/workspace/chat'] }), projectOptions: { value: [] }, setActive: () => {} };
}

function makeThreadStore() {
  const currentThreadId = ref('thread-active');
  const timelinesByThread = reactive({ 'thread-active': [], 'thread-archived': [] });
  const diffTextByThread = reactive({ 'thread-active': '', 'thread-archived': '' });
  return {
    currentThreadId,
    threadStore: {
      state: reactive({ pinnedThreadAtById: {}, archivedThreadAtById: { 'thread-archived': 99 }, agentRuntimeById: {}, skillRevision: 0, diffTextByThread }),
      getLayout: () => 'focus',
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
      getThreadsByMode: () => [{ id: 'thread-active', name: 'Active' }, { id: 'thread-archived', name: 'Archived' }],
      displayName: (thread) => thread.name,
      getThreadStatus: () => 'idle',
      getThreadStatusHeader: () => '等待指示',
      getThreadInterruptible: () => true,
      getThreadPinnedAt: () => 0,
      getThreadArchivedAt: (threadId) => (threadId === 'thread-archived' ? 99 : 0),
      getThreadTimeline: (threadId) => timelinesByThread[threadId] || [],
      loadMessages: async () => ({}),
      stopThread: vi.fn(async () => ({ confirmed: true, settled: true, mode: 'interrupt_confirmed' })),
      getThreadDiff: (threadId) => diffTextByThread[threadId] || '',
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

describe('UnifiedChatPage citation actions', () => {
  it('switches selected thread when clicking a conversation citation', () => {
    const { threadStore, currentThreadId } = makeThreadStore();
    const vm = UnifiedChatPage.setup({ threadStore, projectStore: makeProjectStore(), mode: 'chat' });

    vm.onTimelineCitationClick({ kind: 'conversation', conversationId: 'thread-archived', raw: '@thread-archived' });

    expect(currentThreadId.value).toBe('thread-archived');
    expect(vm.selectedThreadId.value).toBe('thread-archived');
  });

  it('does not revive composer skill selection from skill citations', () => {
    const { threadStore } = makeThreadStore();
    const vm = UnifiedChatPage.setup({ threadStore, projectStore: makeProjectStore(), mode: 'chat' });

    expect(vm).not.toHaveProperty('isComposerSkillSelected');
    vm.onTimelineCitationClick({ kind: 'skill', skillName: 'DeploySkill', path: 'docs/skills/deploy/SKILL.md', raw: 'DeploySkill' });
    vm.onTimelineCitationClick({ kind: 'skill', skillName: 'DeploySkill', path: '', raw: 'DeploySkill' });
    expect(composerStoreMock.state.text).toBe('');
  });
});
