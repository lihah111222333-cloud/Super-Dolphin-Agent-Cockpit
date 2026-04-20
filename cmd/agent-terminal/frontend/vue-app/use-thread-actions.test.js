// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { reactive, ref } from '../lib/vue.esm-browser.prod.js';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./services/log.js', () => ({
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { useThreadActions } from './composables/useThreadActions.js';

function createThreadActions(overrides = {}) {
  const threadStore = {
    state: reactive({
      agentRuntimeById: overrides.runtimeById ?? {},
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
  const deps = {
    selectedThreadId: ref(overrides.selectedThreadId ?? ''),
    modeKey: ref(overrides.modeKey ?? 'chat'),
    isCmd: ref(overrides.isCmd ?? false),
    composer: {
      state: reactive({
        text: overrides.text ?? '',
        attachments: overrides.attachments ?? [],
      }),
      clearComposer: vi.fn(),
    },
    layoutMode: ref(overrides.layoutMode ?? 'mix'),
    cmdCardCols: ref(overrides.cmdCardCols ?? 3),
    isThreadInterruptible: overrides.isThreadInterruptible ?? vi.fn(() => true),
    beginInlineRename: vi.fn(),
    scheduleScrollToBottom: vi.fn(),
    resolveComposerSkillSelectionForSend: vi.fn().mockResolvedValue({
      selectedSkills: [],
      manualSkillSelection: false,
    }),
    resolveLaunchSkillSelectionForSend: vi.fn().mockResolvedValue({
      selectedSkills: ['LaunchSkill'],
      manualSkillSelection: true,
    }),
    launchSkillSelectionEnabled: ref(overrides.launchSkillSelectionEnabled ?? false),
    resetSelectedComposerSkills: vi.fn(),
    resetLaunchSkillSelection: vi.fn(),
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

beforeEach(() => {
  apiMock.callAPI.mockReset().mockResolvedValue({});
  globalThis.window = { ...(globalThis.window || {}), alert: vi.fn() };
});

describe('useThreadActions', () => {
  it('launches a thread and switches the selection to the new id', async () => {
    const vm = createThreadActions();

    await vm.launchOne();

    expect(vm.threadStore.startThread).toHaveBeenCalledWith('/repo', { focusMode: 'chat' });
    expect(vm.deps.selectedThreadId.value).toBe('thread-started');
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

  it('swallows archive toggle failures after calling the thread store', async () => {
    const vm = createThreadActions();
    vm.threadStore.toggleThreadArchive.mockRejectedValueOnce(new Error('boom'));

    await expect(vm.toggleThreadArchive('thread-live')).resolves.toBeUndefined();

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

  it('send: resolves launch skills before startThread on blank-thread send', async () => {
    const vm = createThreadActions({
      selectedThreadId: '',
      text: 'hello world',
      launchSkillSelectionEnabled: true,
    });

    await vm.send();

    expect(vm.deps.resolveLaunchSkillSelectionForSend).toHaveBeenCalledWith('hello world');
    expect(vm.deps.resolveComposerSkillSelectionForSend).not.toHaveBeenCalled();
    expect(vm.threadStore.startThread).toHaveBeenCalledWith('/repo', {
      focusMode: 'chat',
      selectedSkills: ['LaunchSkill'],
      manualSkillSelection: true,
    });
    expect(vm.deps.resetLaunchSkillSelection).toHaveBeenCalledTimes(1);
    expect(vm.deps.selectedThreadId.value).toBe('thread-started');
    expect(vm.threadStore.sendMessage).toHaveBeenCalledWith(
      'thread-started',
      'hello world',
      [],
      expect.objectContaining({
        cwd: '/repo',
        selectedSkills: ['LaunchSkill'],
        manualSkillSelection: true,
      }),
    );
    expect(vm.deps.scheduleScrollToBottom).toHaveBeenCalledWith(true);
  });

  it('send: keeps launch state when startThread fails', async () => {
    const vm = createThreadActions({
      selectedThreadId: '',
      text: 'hello world',
      launchSkillSelectionEnabled: true,
      threadStore: {
        startThread: vi.fn().mockResolvedValue(''),
      },
    });

    await vm.send();

    expect(vm.deps.resolveLaunchSkillSelectionForSend).toHaveBeenCalledWith('hello world');
    expect(vm.deps.resetLaunchSkillSelection).not.toHaveBeenCalled();
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
    expect(vm.deps.resolveLaunchSkillSelectionForSend).not.toHaveBeenCalled();
    expect(vm.deps.resolveComposerSkillSelectionForSend).toHaveBeenCalledWith('thread-live', 'ping');
    expect(vm.threadStore.sendMessage).toHaveBeenCalledWith(
      'thread-live',
      'ping',
      [{ path: '/tmp/a.txt' }],
      expect.objectContaining({ cwd: '/repo' }),
    );
  });

  it('send: restores composer content on sendMessage failure', async () => {
    const vm = createThreadActions({
      selectedThreadId: 'thread-live',
      text: 'will fail',
      attachments: [{ path: '/file.txt' }],
    });
    vm.deps.composer.addAttachment = vi.fn();
    vm.threadStore.sendMessage.mockRejectedValueOnce(new Error('network'));

    await vm.send();

    expect(vm.deps.composer.state.text).toBe('will fail');
    expect(vm.deps.composer.addAttachment).toHaveBeenCalledWith({ path: '/file.txt' });
  });

  it('interruptCurrent: calls stopThread and invokes confirm callback on success', async () => {
    const vm = createThreadActions({ selectedThreadId: 'thread-live' });
    const control = { threadId: 'thread-live', confirm: vi.fn(), reject: vi.fn() };

    await vm.interruptCurrent(control);

    expect(vm.threadStore.stopThread).toHaveBeenCalledWith('thread-live', { source: 'ui_stop' });
    expect(control.confirm).toHaveBeenCalledWith(expect.objectContaining({ threadId: 'thread-live' }));
    expect(control.reject).not.toHaveBeenCalled();
  });

  it('interruptCurrent: invokes reject callback on stopThread failure', async () => {
    const vm = createThreadActions({ selectedThreadId: 'thread-live' });
    vm.threadStore.stopThread.mockRejectedValueOnce(new Error('fail'));
    const control = { threadId: 'thread-live', confirm: vi.fn(), reject: vi.fn() };

    await vm.interruptCurrent(control);

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
    const vm = createThreadActions({ selectedThreadId: 'thread-live' });

    const result = await vm.recoverSelected();

    expect(vm.threadStore.recoverThread).toHaveBeenCalledWith('thread-live');
    expect(vm.recoveringSelected.value).toBe(false);
    expect(result).toEqual(expect.objectContaining({ ok: true, threadId: 'thread-live' }));
    expect(result.message).toContain('恢复');
    expect(globalThis.window.alert).toHaveBeenCalledWith('已触发进程恢复，请等待连接重建。');

  });
});
