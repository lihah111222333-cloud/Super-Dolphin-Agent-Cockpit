// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { reactive, ref } from '../lib/vue.esm-browser.prod.js';

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn().mockResolvedValue({}),
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { useThreadActions } from './composables/useThreadActions.js';

function createThreadActions() {
  const composerState = reactive({
    text: 'launch claude',
    attachments: [{ path: '/file.txt' }],
  });
  let threadStore;
  const startThread = vi.fn()
    .mockImplementationOnce(async () => {
      threadStore.state.agentRuntimeById = { 'thread-started': { provider: 'claude' } };
      return 'thread-started';
    })
    .mockImplementationOnce(async () => {
      threadStore.state.agentRuntimeById = { ...threadStore.state.agentRuntimeById, 'thread-retry': { provider: 'claude' } };
      return 'thread-retry';
    });
  const sendMessage = vi.fn().mockResolvedValue(undefined);
  threadStore = {
    state: reactive({
      agentRuntimeById: {},
      sendBlockedNoticesByThread: {},
      sendHoldNoticesByThread: {},
      threads: [],
    }),
    startThread,
    sendMessage,
    getThreadStatus: vi.fn(() => 'idle'),
    getThreadConfig: vi.fn().mockResolvedValue(null),
    setThreadConfig: vi.fn().mockResolvedValue(null),
    stopThread: vi.fn().mockResolvedValue({ confirmed: true, settled: true, mode: 'confirmed' }),
    compactThread: vi.fn().mockResolvedValue(undefined),
    forceCompleteThread: vi.fn().mockResolvedValue(undefined),
    recoverThread: vi.fn().mockResolvedValue(undefined),
    displayName: vi.fn((thread) => thread?.name || thread?.id || ''),
    getThreadSendBlockedNotice: vi.fn((threadId) => threadStore.state.sendBlockedNoticesByThread[threadId] || ''),
    isThreadSendBlocked: vi.fn((threadId) => Boolean(threadStore.state.sendBlockedNoticesByThread[threadId])),
    clearThreadSendBlockedNotice: vi.fn((threadId) => {
      const next = { ...threadStore.state.sendBlockedNoticesByThread };
      delete next[threadId];
      threadStore.state.sendBlockedNoticesByThread = next;
    }),
  };
  sendMessage.mockImplementationOnce(async () => {
    const error = new Error('[-32098] exit status 1');
    threadStore.state.sendBlockedNoticesByThread = {
      ...threadStore.state.sendBlockedNoticesByThread,
      'thread-started': `发送失败：${error.message}`,
    };
    throw error;
  });
  const deps = {
    selectedThreadId: ref(''),
    modeKey: ref('chat'),
    isCmd: ref(false),
    composer: {
      state: composerState,
      clearComposer: vi.fn(() => {
        composerState.text = '';
        composerState.attachments = [];
      }),
      clearDraft: vi.fn(),
      restoreDraft: vi.fn(),
    },
    layoutMode: ref('mix'),
    cmdCardCols: ref(3),
    isThreadInterruptible: vi.fn(() => true),
    beginInlineRename: vi.fn(),
    scheduleScrollToBottom: vi.fn(),
    showArchivedThreadList: ref(false),
    compacting: ref(false),
  };
  const vm = useThreadActions({
    threadStore,
    projectStore: { state: { active: '/repo' } },
  }, deps);
  return { ...vm, deps, threadStore };
}

beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(globalThis.crypto, 'randomUUID')
    .mockReturnValueOnce('018f00e0-39fc-72ac-a47a-2a858c75d111')
    .mockReturnValueOnce('018f00e0-39fc-72ac-a47a-2a858c75d222')
    .mockReturnValue('018f00e0-39fc-72ac-a47a-2a858c75d333');
});

describe('useThreadActions auto-start launch failures', () => {
  it('releases the blank composer after first-turn provider startup failure', async () => {
    const vm = createThreadActions();

    await expect(vm.send()).rejects.toThrow('exit status 1');

    expect(vm.deps.selectedThreadId.value).toBe('');
    expect(vm.deps.composer.state.text).toBe('launch claude');
    expect(vm.deps.composer.state.attachments).toEqual([{ path: '/file.txt' }]);
    expect(vm.deps.composer.restoreDraft).toHaveBeenCalledWith('', 'chat', {
      text: 'launch claude',
      attachments: [{ path: '/file.txt' }],
    });
    expect(vm.sendFailureNotice.value).toBe('发送失败：Claude 启动失败：[-32098] exit status 1');

    vm.threadStore.sendMessage.mockClear();
    await vm.send();

    expect(vm.threadStore.startThread).toHaveBeenNthCalledWith(2, '/repo', {
      focusMode: 'chat',
      deferSpawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d222',
      optimisticUserMessage: { text: 'launch claude', attachments: [{ path: '/file.txt' }] },
      skipInitialRuntimeSync: true,
    });
    expect(vm.threadStore.sendMessage).toHaveBeenCalledWith(
      'thread-retry',
      'launch claude',
      [{ path: '/file.txt' }],
      { manualSkillSelection: false, cwd: '/repo' },
    );
  });
});
