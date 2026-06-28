// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick, reactive, ref } from '../lib/vue.esm-browser.prod.js';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { useThreadActions } from './composables/useThreadActions.js';

function createThreadActions(overrides = {}) {
  const threadStore = {
    state: reactive({
      agentRuntimeById: overrides.runtimeById ?? {},
      sendBlockedNoticesByThread: {},
      sendHoldNoticesByThread: {},
      threads: overrides.threads ?? [],
    }),
    startThread: vi.fn().mockResolvedValue('thread-started'),
    getThreadConfig: vi.fn().mockResolvedValue(null),
    setThreadConfig: vi.fn().mockResolvedValue(null),
    sendMessage: vi.fn().mockResolvedValue(undefined),
    stopThread: vi.fn().mockResolvedValue({ confirmed: true, settled: true, mode: 'confirmed' }),
    compactThread: vi.fn().mockResolvedValue(undefined),
    setThreadCompactResult: vi.fn(),
    forceCompleteThread: vi.fn().mockResolvedValue(undefined),
    recoverThread: vi.fn().mockResolvedValue(undefined),

    loadMessages: vi.fn(),
    promptRenameThread: vi.fn(),
    toggleThreadPin: vi.fn(),
    toggleThreadArchive: vi.fn().mockResolvedValue(undefined),
    displayName: vi.fn((thread) => thread?.name || thread?.id || ''),
    ...overrides.threadStore,
  };
  const props = {
    threadStore,
    projectStore: { state: { active: '/repo' } },
    ...overrides.props,
  };
  const composerState = reactive({
    text: overrides.text ?? '',
    attachments: overrides.attachments ?? [],
  });
  const deps = {
    selectedThreadId: ref(overrides.selectedThreadId ?? ''),
    modeKey: ref(overrides.modeKey ?? 'chat'),
    isCmd: ref(overrides.isCmd ?? false),
    composer: {
      state: composerState,
      clearComposer: vi.fn(() => { composerState.text = ''; composerState.attachments = []; }),
      clearDraft: vi.fn(),
      restoreDraft: vi.fn(),
    },
    layoutMode: ref(overrides.layoutMode ?? 'mix'),
    cmdCardCols: ref(overrides.cmdCardCols ?? 3),
    isThreadInterruptible: overrides.isThreadInterruptible ?? vi.fn(() => true),
    beginInlineRename: vi.fn(),
    scheduleScrollToBottom: vi.fn(),
    showArchivedThreadList: ref(false),
    compacting: ref(false),
    ...overrides.deps,
  };

  const vm = useThreadActions(props, deps);
  return {
    props,
    threadStore,
    deps,
    ...vm,
  };
}

function createDeferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function flush() {
  await Promise.resolve();
  await nextTick();
}

function mockNextSendBackendFailure(vm, threadId, error) {
  vm.threadStore.sendMessage.mockImplementationOnce(async () => {
    vm.threadStore.state.sendBlockedNoticesByThread = {
      ...(vm.threadStore.state.sendBlockedNoticesByThread || {}),
      [threadId]: `发送失败：${error?.message || String(error)}`,
    };
    throw error;
  });
}

beforeEach(() => {
  vi.restoreAllMocks();
  apiMock.callAPI.mockReset().mockResolvedValue({});
  globalThis.window = { ...(globalThis.window || {}), alert: vi.fn() };
  vi.spyOn(globalThis.crypto, 'randomUUID')
    .mockReturnValueOnce('018f00e0-39fc-72ac-a47a-2a858c75d111')
    .mockReturnValueOnce('018f00e0-39fc-72ac-a47a-2a858c75d222')
    .mockReturnValue('018f00e0-39fc-72ac-a47a-2a858c75d333');
});

describe('useThreadActions', () => {
  it('surfaces an existing blocked notice for the initially selected thread', () => {
    const sendBlockedNoticesByThread = ref(new Map([['thread-a', '发送失败：backend boom']]));

    const vm = createThreadActions({
      selectedThreadId: 'thread-a',
      deps: { sendBlockedNoticesByThread },
    });

    expect(vm.sendFailureNotice.value).toBe('发送失败：backend boom');
  });

  it('surfaces an existing error-status block for the initially selected thread', () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-error',
      threadStore: {
        getThreadStatus: vi.fn(() => 'error'),
      },
    });

    expect(vm.sendFailureNotice.value).toContain('当前会话已报错');
  });

  it('launchOne opens a local draft without creating a backend thread when composer is empty', async () => {
    const vm = createThreadActions({ selectedThreadId: 'thread-live' });

    await vm.launchOne();

    expect(vm.deps.selectedThreadId.value).toBe('');
    expect(vm.threadStore.startThread).not.toHaveBeenCalled();
  });

  it('launchOne draft creates a launch intent only when the first message is sent', async () => {
    const vm = createThreadActions({ selectedThreadId: 'thread-live' });

    await vm.launchOne();
    expect(vm.threadStore.startThread).not.toHaveBeenCalled();

    vm.deps.composer.state.text = 'bind provider now';
    await vm.send();
    await vm.launchOne();
    vm.deps.composer.state.text = 'new provider-safe launch';
    await vm.send();

    expect(vm.threadStore.startThread).toHaveBeenNthCalledWith(1, '/repo', {
      focusMode: 'chat',
      deferSpawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      optimisticUserMessage: { text: 'bind provider now', attachments: [] },
      skipInitialRuntimeSync: true,
    });
    expect(vm.threadStore.startThread).toHaveBeenNthCalledWith(2, '/repo', {
      focusMode: 'chat',
      deferSpawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d222',
      optimisticUserMessage: { text: 'new provider-safe launch', attachments: [] },
      skipInitialRuntimeSync: true,
    });
  });

  it('launchOne opens a local draft even before provider preference is ready', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      deps: {
        providerPreferenceReady: ref(false),
      },
    });

    await vm.launchOne();

    expect(vm.deps.selectedThreadId.value).toBe('');
    expect(vm.threadStore.startThread).not.toHaveBeenCalled();
  });

  it('launchOne local draft does not resolve or start project-scoped backend work', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      props: {
        windowCwd: '/repo-root',
        projectStore: { state: { active: '.' } },
      },
    });

    await vm.launchOne();

    expect(vm.deps.selectedThreadId.value).toBe('');
    expect(vm.threadStore.startThread).not.toHaveBeenCalled();
  });

  it('launchOne does not surface backend startup errors because it only opens a local draft', async () => {
    const backendError = new Error('{"message":"[-32098] thread: prompt assembly: ClaudeMd candidate containment failed: safe read denied"}');
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      threadStore: {
        startThread: vi.fn().mockRejectedValueOnce(backendError),
      },
    });

    await vm.launchOne();

    expect(globalThis.window.alert).not.toHaveBeenCalled();
    expect(vm.threadStore.startThread).not.toHaveBeenCalled();
    expect(vm.sendFailureNotice.value).toBe('');
  });

  it('pipes thread config get/set calls through the thread store', async () => {
    const vm = createThreadActions();
    vm.threadStore.getThreadConfig.mockResolvedValueOnce({ threadId: 'thread-live' });
    vm.threadStore.setThreadConfig.mockResolvedValueOnce({ threadId: 'thread-live', override: { model: 'gpt-5.2', effort: 'high' } });

    const got = await vm.getThreadConfig('thread-live');
    const saved = await vm.setThreadConfig('thread-live', { model: 'gpt-5.2', effort: 'high' });

    expect(vm.threadStore.getThreadConfig).toHaveBeenCalledWith('thread-live');
    expect(vm.threadStore.setThreadConfig).toHaveBeenCalledWith('thread-live', { model: 'gpt-5.2', effort: 'high' });
    expect(got.threadId).toBe('thread-live');
    expect(saved.override.effort).toBe('high');
  });

  it('stops early when the selected thread is not interruptible', () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      isThreadInterruptible: vi.fn(() => false),
    });

    vm.stopSelected();

    expect(vm.threadStore.stopThread).not.toHaveBeenCalled();
  });

  it('delegates pin toggles to the thread store', () => {
    const vm = createThreadActions();

    vm.toggleThreadPin('thread-live');

    expect(vm.threadStore.toggleThreadPin).toHaveBeenCalledWith('thread-live');
  });

  it('rethrows archive toggle failures after calling the thread store', async () => {
    const vm = createThreadActions();
    vm.threadStore.toggleThreadArchive.mockRejectedValueOnce(new Error('boom'));

    await expect(vm.toggleThreadArchive('thread-live')).rejects.toThrow('boom');

    expect(vm.threadStore.toggleThreadArchive).toHaveBeenCalledWith('thread-live');
  });

  it('returns early when there is no text and no attachments to send', async () => {
    const vm = createThreadActions({
      text: '   ',
      attachments: [],
    });

    await vm.send();

    expect(vm.threadStore.startThread).not.toHaveBeenCalled();
    expect(vm.threadStore.sendMessage).not.toHaveBeenCalled();
  });

  it('updates layout and card column refs through setters', () => {
    const vm = createThreadActions({ layoutMode: 'overview', cmdCardCols: 2 });

    vm.setCmdLayout('mix');
    vm.setCmdCardCols(4);

    expect(vm.deps.layoutMode.value).toBe('mix');
    expect(vm.deps.cmdCardCols.value).toBe(4);
  });

  // ── regression guards for send / interrupt / recover ──

  it('send: auto-starts a thread when none is selected, then sends message', async () => {
    const vm = createThreadActions({
      selectedThreadId: '',
      text: 'hello world',
    });

    await vm.send();

    expect(vm.threadStore.startThread).toHaveBeenCalledWith('/repo', {
      focusMode: 'chat',
      deferSpawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      optimisticUserMessage: { text: 'hello world', attachments: [] },
      skipInitialRuntimeSync: true,
    });
    expect(vm.deps.selectedThreadId.value).toBe('thread-started');
    expect(vm.threadStore.sendMessage).toHaveBeenCalledWith(
      'thread-started',
      'hello world',
      [],
      expect.objectContaining({ cwd: '/repo' }),
    );
    expect(vm.deps.scheduleScrollToBottom).toHaveBeenCalledWith(true);
  });

  it('send: uses the latest window cwd when app bootstrap updates props after setup', async () => {
    const vm = createThreadActions({
      selectedThreadId: '',
      text: 'hello world',
      props: {
        projectStore: { state: { active: '.' } },
        windowCwd: '',
      },
    });

    vm.props.windowCwd = '/async-window-cwd';

    await vm.send();

    expect(vm.threadStore.startThread).toHaveBeenCalledWith('/async-window-cwd', {
      focusMode: 'chat',
      deferSpawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      optimisticUserMessage: { text: 'hello world', attachments: [] },
      skipInitialRuntimeSync: true,
    });
    expect(vm.threadStore.sendMessage).toHaveBeenCalledWith(
      'thread-started',
      'hello world',
      [],
      expect.objectContaining({ cwd: '/async-window-cwd' }),
    );
  });

  it('send: ignores duplicate invocations while auto-start is still in flight', async () => {
    const startThread = createDeferred();
    const vm = createThreadActions({
      selectedThreadId: '',
      text: 'duplicate text',
      threadStore: {
        startThread: vi.fn(() => startThread.promise),
      },
    });

    const firstSend = vm.send();
    const secondSend = vm.send();
    await Promise.resolve();

    expect(vm.threadStore.startThread).toHaveBeenCalledTimes(1);
    expect(vm.threadStore.sendMessage).not.toHaveBeenCalled();

    startThread.resolve('thread-started');
    await Promise.all([firstSend, secondSend]);

    expect(vm.threadStore.sendMessage).toHaveBeenCalledTimes(1);
    expect(vm.deps.selectedThreadId.value).toBe('thread-started');
  });

  it('send: does not auto-start a thread before provider preference is ready', async () => {
    const vm = createThreadActions({
      selectedThreadId: '',
      text: 'hello world',
      deps: {
        providerPreferenceReady: ref(false),
      },
    });

    await vm.send();

    expect(vm.threadStore.startThread).not.toHaveBeenCalled();
    expect(vm.threadStore.sendMessage).not.toHaveBeenCalled();
    expect(vm.deps.composer.state.text).toBe('hello world');
  });

  it('send: shows backend startup errors, keeps composer, and does not send when auto-start fails', async () => {
    const backendError = new Error('{"message":"[-32098] thread: prompt assembly: ClaudeMd candidate containment failed: safe read denied"}');
    const vm = createThreadActions({
      selectedThreadId: '',
      text: 'hello world',
      attachments: [{ path: '/tmp/a.txt' }],
      threadStore: {
        startThread: vi.fn().mockRejectedValueOnce(backendError),
      },
    });

    await expect(vm.send()).rejects.toBe(backendError);

    expect(globalThis.window.alert).not.toHaveBeenCalled();
    expect(vm.deps.composer.state.text).toBe('hello world');
    expect(vm.deps.composer.state.attachments).toEqual([{ path: '/tmp/a.txt' }]);
    expect(vm.threadStore.sendMessage).not.toHaveBeenCalled();
    expect(vm.sendFailureNotice.value).toContain('发送失败');
    expect(vm.sendFailureNotice.value).toContain('ClaudeMd');
    expect(vm.sendFailureNotice.value).toContain('safe read');
  });

  it('send: can retry after missing cwd failure once an explicit project is selected', async () => {
    const projectStore = { state: { active: '.' } };
    const vm = createThreadActions({
      selectedThreadId: '',
      text: 'retry after project selection',
      props: {
        windowCwd: '',
        projectStore,
      },
    });

    await expect(vm.send()).rejects.toThrow('project action cwd is required');
    expect(vm.deps.composer.state.text).toBe('retry after project selection');
    expect(vm.threadStore.startThread).not.toHaveBeenCalled();

    projectStore.state.active = '/Users/ai/Desktop/sd';
    await vm.send();

    expect(vm.threadStore.startThread).toHaveBeenCalledWith('/Users/ai/Desktop/sd', {
      focusMode: 'chat',
      deferSpawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      optimisticUserMessage: { text: 'retry after project selection', attachments: [] },
      skipInitialRuntimeSync: true,
    });
    expect(vm.threadStore.sendMessage).toHaveBeenCalledWith(
      'thread-started',
      'retry after project selection',
      [],
      { manualSkillSelection: false, cwd: '/Users/ai/Desktop/sd' },
    );
  });

  it('send: preserves launch intent after start failure so retained backend failures stay keyed', async () => {
    const backendError = new Error('provider failed');
    const vm = createThreadActions({
      selectedThreadId: '',
      text: 'hello world',
      threadStore: {
        startThread: vi.fn()
          .mockRejectedValueOnce(backendError)
          .mockResolvedValueOnce('thread-started'),
      },
    });

    await expect(vm.send()).rejects.toBe(backendError);
    await vm.send();

    expect(vm.threadStore.startThread).toHaveBeenNthCalledWith(1, '/repo', {
      focusMode: 'chat',
      deferSpawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      optimisticUserMessage: { text: 'hello world', attachments: [] },
      skipInitialRuntimeSync: true,
    });
    expect(vm.threadStore.startThread).toHaveBeenNthCalledWith(2, '/repo', {
      focusMode: 'chat',
      deferSpawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      optimisticUserMessage: { text: 'hello world', attachments: [] },
      skipInitialRuntimeSync: true,
    });
  });

  it('send resolves dot project scope to the window cwd for start and message payloads', async () => {
    const vm = createThreadActions({
      selectedThreadId: '',
      text: 'hello from root',
      props: {
        windowCwd: '/repo-root',
        projectStore: { state: { active: '.' } },
      },
    });

    await vm.send();

    expect(vm.threadStore.startThread).toHaveBeenCalledWith('/repo-root', {
      focusMode: 'chat',
      deferSpawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      optimisticUserMessage: { text: 'hello from root', attachments: [] },
      skipInitialRuntimeSync: true,
    });
    expect(vm.threadStore.sendMessage).toHaveBeenCalledWith(
      'thread-started',
      'hello from root',
      [],
      expect.objectContaining({ cwd: '/repo-root' }),
    );
  });

  it('send: reuses the thread start cwd for the first message even if project scope changes during start', async () => {
    const projectStore = { state: { active: '/repo' } };
    const vm = createThreadActions({
      selectedThreadId: '',
      text: 'hello while project changes',
      props: { projectStore },
      threadStore: {
        startThread: vi.fn().mockImplementation(async () => {
          projectStore.state.active = '/other-repo';
          return 'thread-started';
        }),
      },
    });

    await vm.send();

    expect(vm.threadStore.startThread).toHaveBeenCalledWith('/repo', {
      focusMode: 'chat',
      deferSpawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      optimisticUserMessage: { text: 'hello while project changes', attachments: [] },
      skipInitialRuntimeSync: true,
    });
    expect(vm.threadStore.sendMessage).toHaveBeenCalledWith(
      'thread-started',
      'hello while project changes',
      [],
      { manualSkillSelection: false, cwd: '/repo' },
    );
  });

  it('send: starts a new thread before sending and does not forward skill selections', async () => {
    const vm = createThreadActions({
      selectedThreadId: '',
      text: 'boot launch',
      attachments: [{ path: '/tmp/a.txt' }],
    });

    await vm.send();

    expect(vm.threadStore.startThread).toHaveBeenCalledWith('/repo', {
      focusMode: 'chat',
      deferSpawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      optimisticUserMessage: { text: 'boot launch', attachments: [{ path: '/tmp/a.txt' }] },
      skipInitialRuntimeSync: true,
    });
    expect(vm.threadStore.sendMessage).toHaveBeenCalledWith(
      'thread-started',
      'boot launch',
      [{ path: '/tmp/a.txt' }],
      {
        manualSkillSelection: false,
        cwd: '/repo',
      },
    );
    expect(vm.threadStore.startThread.mock.invocationCallOrder[0]).toBeLessThan(vm.threadStore.sendMessage.mock.invocationCallOrder[0]);
  });

  it('send: uses the selected thread runtime cwd for active threads', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      text: 'ping worktree',
      props: {
        windowCwd: '/repo',
        projectStore: { state: { active: '/repo' } },
      },
      runtimeById: {
        'thread-live': { cwd: '/repo/.worktrees/provider-connecting-overlay' },
      },
    });

    await vm.send();

    expect(vm.threadStore.startThread).not.toHaveBeenCalled();
    expect(vm.threadStore.sendMessage).toHaveBeenCalledWith(
      'thread-live',
      'ping worktree',
      [],
      {
        manualSkillSelection: false,
        cwd: '/repo/.worktrees/provider-connecting-overlay',
      },
    );
  });

  it('send: falls back to the selected thread model cwd when runtime cwd is not visible', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      text: 'ping model cwd',
      props: {
        windowCwd: '/repo',
        projectStore: { state: { active: '/repo' } },
      },
      threads: [
        { id: 'thread-live', name: 'Thread Live', state: 'idle', cwd: '/repo/.worktrees/model-cwd' },
      ],
    });

    await vm.send();

    expect(vm.threadStore.startThread).not.toHaveBeenCalled();
    expect(vm.threadStore.sendMessage).toHaveBeenCalledWith(
      'thread-live',
      'ping model cwd',
      [],
      {
        manualSkillSelection: false,
        cwd: '/repo/.worktrees/model-cwd',
      },
    );
  });

  it('send: omits cwd for active threads when no thread cwd is locally visible', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      text: 'ping with skills',
    });

    await vm.send();

    expect(vm.threadStore.sendMessage).toHaveBeenCalledWith(
      'thread-live',
      'ping with skills',
      [],
      { manualSkillSelection: false },
    );
  });

  it('send: stops when startThread returns no thread id', async () => {
    const vm = createThreadActions({
      selectedThreadId: '',
      text: 'boot launch',
      threadStore: {
        startThread: vi.fn().mockResolvedValue(''),
      },
    });

    await vm.send();

    expect(vm.threadStore.sendMessage).not.toHaveBeenCalled();
  });

  it('send: sends on existing thread without starting a new one', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      text: 'ping',
      attachments: [{ path: '/tmp/a.txt' }],
    });

    await vm.send();

    expect(vm.threadStore.startThread).not.toHaveBeenCalled();
    expect(vm.threadStore.sendMessage).toHaveBeenCalledWith(
      'thread-live',
      'ping',
      [{ path: '/tmp/a.txt' }],
      { manualSkillSelection: false },
    );
  });

  it('send: blocks manual sends on an errored selected thread before calling the backend', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-error',
      text: '继续',
      attachments: [{ path: '/tmp/a.txt' }],
      threadStore: {
        getThreadStatus: vi.fn(() => 'error'),
      },
    });

    await vm.send();

    expect(vm.threadStore.startThread).not.toHaveBeenCalled();
    expect(vm.threadStore.sendMessage).not.toHaveBeenCalled();
    expect(vm.deps.composer.state.text).toBe('继续');
    expect(vm.deps.composer.state.attachments).toEqual([{ path: '/tmp/a.txt' }]);
    expect(vm.sendFailureNotice.value).toContain('当前会话已报错');
  });

  it('send: restores composer content on sendMessage failure', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      text: 'will fail',
      attachments: [{ path: '/file.txt' }],
    });
    mockNextSendBackendFailure(vm, 'thread-live', new Error('network'));

    await expect(vm.send()).rejects.toThrow('network');

    expect(vm.deps.composer.state.text).toBe('will fail');
    expect(vm.deps.composer.state.attachments).toEqual([{ path: '/file.txt' }]);
  });

  it('send: clears stale selected thread when resolver cannot use the persisted session', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'agent-stale',
      text: 'retry later',
    });
    vm.threadStore.sendMessage.mockRejectedValueOnce(new Error('resolve session: thread "agent-stale": get_by_thread_id thread: timeout: context deadline exceeded'));

    await expect(vm.send()).rejects.toThrow('context deadline exceeded');

    expect(vm.deps.selectedThreadId.value).toBe('');
    expect(vm.deps.composer.state.text).toBe('retry later');
    await flush();
    expect(vm.sendFailureNotice.value).toContain('context deadline exceeded');
  });

  it('send: shows generic backend details and blocks repeated sends after non-workdir failures', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      text: 'retry',
    });
    vm.sendFailureNotice.value = '旧的工作目录提示';
    mockNextSendBackendFailure(vm, 'thread-live', new Error('network'));

    await expect(vm.send()).rejects.toThrow('network');

    expect(vm.sendFailureNotice.value).toBe('发送失败：network');
    expect(vm.deps.composer.state.text).toBe('retry');

    vm.threadStore.sendMessage.mockClear();
    vm.deps.composer.state.text = 'retry again';
    await vm.send();

    expect(vm.threadStore.sendMessage).not.toHaveBeenCalled();
    expect(vm.deps.composer.state.text).toBe('retry again');
    expect(vm.sendFailureNotice.value).toBe('发送失败：network');
  });

  it('send: holds repeated sends when the send action rejects without a store marker', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      text: 'retry',
    });
    vm.threadStore.sendMessage.mockRejectedValueOnce(new Error('local sync failed'));

    await expect(vm.send()).rejects.toThrow('local sync failed');

    expect(vm.sendFailureNotice.value).toBe('发送失败：local sync failed');

    vm.threadStore.sendMessage.mockClear();
    vm.deps.composer.state.text = 'retry again';
    await vm.send();

    expect(vm.threadStore.sendMessage).not.toHaveBeenCalled();
    expect(vm.sendFailureNotice.value).toBe('发送失败：local sync failed');
  });

  it('send: clears the failure notice only when the selected thread changes', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-a',
      text: 'retry',
    });
    vm.threadStore.sendMessage.mockRejectedValueOnce(new Error('network'));

    await expect(vm.send()).rejects.toThrow('network');
    expect(vm.sendFailureNotice.value).toBe('发送失败：network');

    vm.deps.selectedThreadId.value = 'thread-a';
    await flush();
    expect(vm.sendFailureNotice.value).toBe('发送失败：network');

    vm.deps.selectedThreadId.value = 'thread-b';
    await flush();
    expect(vm.sendFailureNotice.value).toBe('');
  });

  it('send: does not surface a late failure after the selected thread changed', async () => {
    const pendingSend = createDeferred();
    const sendBlockedNoticesByThread = ref(new Map());
    const vm = createThreadActions({
      selectedThreadId: 'thread-a',
      text: 'retry after switch',
      deps: { sendBlockedNoticesByThread },
    });
    vm.threadStore.sendMessage.mockImplementationOnce(() => pendingSend.promise);

    const sendPromise = vm.send();
    vm.deps.selectedThreadId.value = 'thread-b';
    vm.deps.composer.state.text = 'thread-b draft';
    vm.deps.composer.state.attachments = [{ path: '/thread-b.txt' }];
    await flush();

    vm.threadStore.state.sendBlockedNoticesByThread = { 'thread-a': '发送失败：late network' };
    pendingSend.reject(new Error('late network'));
    await expect(sendPromise).rejects.toThrow('late network');

    expect(vm.sendFailureNotice.value).toBe('');
    expect(vm.deps.composer.state.text).toBe('thread-b draft');
    expect(vm.deps.composer.state.attachments).toEqual([{ path: '/thread-b.txt' }]);
    expect(vm.deps.composer.restoreDraft).toHaveBeenCalledWith('thread-a', 'chat', {
      text: 'retry after switch',
      attachments: [],
    });
    expect(sendBlockedNoticesByThread.value.get('thread-a')).toBe('发送失败：late network');
    expect(sendBlockedNoticesByThread.value.has('thread-b')).toBe(false);

    vm.deps.selectedThreadId.value = 'thread-a';
    await flush();
    expect(vm.sendFailureNotice.value).toBe('发送失败：late network');

    vm.threadStore.sendMessage.mockClear();
    vm.deps.composer.state.text = 'retry thread-a';
    await vm.send();

    expect(vm.threadStore.sendMessage).not.toHaveBeenCalled();
    expect(vm.deps.composer.state.text).toBe('retry thread-a');
  });

  it('send: shows a Chinese notice when the thread worktree directory is missing', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      text: '继续',
      attachments: [{ path: '/file.txt' }],
    });
    vm.threadStore.sendMessage.mockRejectedValueOnce(new Error('resolve session: auto-resume failed: codexapp: spawn "/codex-home": codexapp: pool work dir stat "/repo/.worktrees/missing": stat /repo/.worktrees/missing: no such file or directory'));

    await expect(vm.send()).rejects.toThrow('pool work dir stat');

    expect(globalThis.window.alert).not.toHaveBeenCalled();
    expect(vm.deps.composer.state.text).toBe('继续');
    expect(vm.deps.composer.state.attachments).toEqual([{ path: '/file.txt' }]);
    expect(vm.sendFailureNotice.value).toContain('该会话的工作目录已不存在');
    expect(vm.sendFailureNotice.value).toContain('/repo/.worktrees/missing');
    expect(vm.sendFailureNotice.value).toContain('请恢复该目录，或新建/重新绑定会话后继续');
  });

  it('send: blocks repeated sends after provider cwd realpath reports a missing directory', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'missing-cwd-repro-20260525',
      text: '111',
    });
    const missingCwd = '/Users/ai/.config/superpowers/worktrees/Super-Dolphin/fix-session-error-leak/.tmp-missing-cwd-agent.8vlje0';
    mockNextSendBackendFailure(vm, 'missing-cwd-repro-20260525', new Error(`{"message":"[-32098] resolve session: thread \\"missing-cwd-repro-20260525\\": resolve session: auto-resume failed: resolve provider project cwd realpath: lstat ${missingCwd}: no such file or directory"}`));

    await expect(vm.send()).rejects.toThrow('resolve provider project cwd realpath');

    expect(vm.threadStore.sendMessage).toHaveBeenCalledTimes(1);
    expect(vm.sendFailureNotice.value).toContain('该会话的工作目录已不存在');
    expect(vm.sendFailureNotice.value).toContain(missingCwd);

    vm.threadStore.sendMessage.mockClear();
    vm.deps.composer.state.text = '222';
    await vm.send();

    expect(vm.threadStore.sendMessage).not.toHaveBeenCalled();
    expect(vm.deps.composer.state.text).toBe('222');
    expect(vm.sendFailureNotice.value).toContain(missingCwd);
  });

  it('send: shows a Chinese notice when skill mirror safety conflicts block provider startup', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      text: '继续',
    });
    vm.threadStore.sendMessage.mockRejectedValueOnce(new Error('thread: establish session: skill mirror conflicts: 1 unresolved (mirror_root_symlink)'));

    await expect(vm.send()).rejects.toThrow('skill mirror conflicts');

    expect(globalThis.window.alert).not.toHaveBeenCalled();
    expect(vm.deps.composer.state.text).toBe('继续');
    expect(vm.sendFailureNotice.value).toContain('当前项目有技能冲突');
    expect(vm.sendFailureNotice.value).toContain('技能页面');
  });

  it('send: shows a Chinese notice when the Claude provider reports a missing cwd', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      text: '继续 Claude',
    });
    vm.threadStore.sendMessage.mockRejectedValueOnce(new Error('auto-resume failed: claudecli: cwd stat "/repo/.worktrees/missing": stat /repo/.worktrees/missing: no such file or directory'));

    await expect(vm.send()).rejects.toThrow('cwd stat');

    expect(vm.sendFailureNotice.value).toContain('该会话的工作目录已不存在');
    expect(vm.sendFailureNotice.value).toContain('/repo/.worktrees/missing');
  });

  it('interruptCurrent: calls stopThread and invokes confirm callback on success', async () => {
    const vm = createThreadActions({ selectedThreadId: 'thread-live' });
    const control = { threadId: 'thread-live', confirm: vi.fn(), reject: vi.fn() };

    await vm.interruptCurrent(control);

    expect(vm.threadStore.stopThread).toHaveBeenCalledWith('thread-live', { source: 'ui_stop' });
    expect(control.confirm).toHaveBeenCalledWith(expect.objectContaining({ threadId: 'thread-live' }));
    expect(control.reject).not.toHaveBeenCalled();
  });

  it('interruptCurrent: invokes reject callback and rethrows on stopThread failure', async () => {
    const vm = createThreadActions({ selectedThreadId: 'thread-live' });
    vm.threadStore.stopThread.mockRejectedValueOnce(new Error('fail'));
    const control = { threadId: 'thread-live', confirm: vi.fn(), reject: vi.fn() };

    await expect(vm.interruptCurrent(control)).rejects.toThrow('fail');

    expect(control.reject).toHaveBeenCalledWith(expect.objectContaining({ reason: 'error' }));
    expect(control.confirm).not.toHaveBeenCalled();
  });

  it('compactCurrent: blocks unsupported providers and stores an inline failure message', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-claude',
      runtimeById: {
        'thread-claude': { provider: 'claude', capabilities: ['message_send'] },
      },
    });

    const result = await vm.compactCurrent();

    expect(vm.threadStore.compactThread).not.toHaveBeenCalled();
    expect(vm.threadStore.setThreadCompactResult).toHaveBeenCalledWith(
      'thread-claude',
      'failed',
      expect.stringContaining('不支持上下文压缩'),
      expect.objectContaining({ code: 'compact_unsupported' }),
    );
    expect(result).toEqual(expect.objectContaining({ ok: false, code: 'compact_unsupported' }));
  });

  it('compactCurrent: stores a user-visible error message when compact fails', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      runtimeById: {
        'thread-live': { provider: 'codex', capabilities: ['context_compact'] },
      },
    });
    vm.threadStore.compactThread.mockRejectedValueOnce(new Error('boom'));

    const result = await vm.compactCurrent();

    expect(vm.threadStore.compactThread).toHaveBeenCalledWith('thread-live');
    expect(vm.threadStore.setThreadCompactResult).toHaveBeenCalledWith(
      'thread-live',
      'failed',
      '压缩失败: boom',
      expect.objectContaining({ code: 'compact_failed' }),
    );
    expect(result).toEqual(expect.objectContaining({ ok: false, code: 'compact_failed', message: '压缩失败: boom' }));
  });

  it('recoverSelected: calls recoverThread, returns a notice and resets the recovering flag', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      threadStore: { clearThreadSendBlockedNotice: vi.fn() },
    });

    const result = await vm.recoverSelected();

    expect(vm.threadStore.recoverThread).toHaveBeenCalledWith('thread-live');
    expect(vm.threadStore.clearThreadSendBlockedNotice).toHaveBeenCalledWith('thread-live');
    expect(vm.recoveringSelected.value).toBe(false);
    expect(result).toEqual(expect.objectContaining({ ok: true, threadId: 'thread-live' }));
    expect(result.message).toContain('恢复');
    expect(globalThis.window.alert).toHaveBeenCalledWith('已触发进程恢复，请等待连接重建。');

  });
});
