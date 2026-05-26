// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ref, reactive } from './lib/vue.esm-browser.prod.js';

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(),
}));
vi.mock('./services/log.js', () => ({
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

const { callAPI } = await import('./services/api.js');
const { logInfo, logWarn } = await import('./services/log.js');
const { useTaskHandoff, buildTaskFromRuntime } = await import('./composables/useTaskHandoff.js');

function makeCtx({
  selectedThreadId: selectedThreadIdValue = 'src-thread',
  agentRuntimeById = {},
  threadName = 'src-thread',
  isCmd: isCmdValue = false,
  runtime = null,
  withSendMessage = true,
  sendMessageImpl = null,
  activeProject = '/repo',
  windowCwd = '',
} = {}) {
  const threadStore = {
    state: reactive({ agentRuntimeById, kickoffByThread: {} }),
    startThread: vi.fn(async () => 'new-thread-id'),
  };
  if (withSendMessage) {
    threadStore.sendMessage = vi.fn(sendMessageImpl || (async () => {}));
  }
  const projectStore = { state: reactive({ active: activeProject }) };
  return {
    threadStore,
    projectStore,
    selectedThreadId: ref(selectedThreadIdValue),
    activeThread: ref({ id: selectedThreadIdValue, name: threadName }),
    activeRuntime: ref(runtime),
    isCmd: ref(isCmdValue),
    windowCwd,
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

beforeEach(() => {
  vi.mocked(callAPI).mockReset();
  vi.mocked(callAPI).mockResolvedValue({});
  vi.mocked(logInfo).mockReset();
  vi.mocked(logWarn).mockReset();
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe('buildTaskFromRuntime', () => {
  it('returns null for null / undefined / non-object', () => {
    expect(buildTaskFromRuntime(null)).toBeNull();
    expect(buildTaskFromRuntime(undefined)).toBeNull();
    expect(buildTaskFromRuntime('string')).toBeNull();
    expect(buildTaskFromRuntime(42)).toBeNull();
  });

  it('returns null when runtime has no taskId', () => {
    expect(buildTaskFromRuntime({})).toBeNull();
    expect(buildTaskFromRuntime({ foo: 'bar' })).toBeNull();
    expect(buildTaskFromRuntime({ taskId: '   ' })).toBeNull();
  });

  it('extracts task with camelCase fields', () => {
    const task = buildTaskFromRuntime({
      taskId: 't-1',
      taskTitle: '迁移工作',
      handoffFile: 'handoff/tasks/t-1.md',
      ownerThreadId: 'owner-1',
    });
    expect(task).toEqual({
      taskId: 't-1',
      title: '迁移工作',
      handoffFile: 'handoff/tasks/t-1.md',
      ownerThreadId: 'owner-1',
    });
  });

  it('accepts snake_case aliases', () => {
    const task = buildTaskFromRuntime({
      task_id: 't-2',
      task_title: '回归任务',
      handoff_file: 'handoff/tasks/t-2.md',
      owner_thread_id: 'owner-2',
    });
    expect(task).toEqual({
      taskId: 't-2',
      title: '回归任务',
      handoffFile: 'handoff/tasks/t-2.md',
      ownerThreadId: 'owner-2',
    });
  });

  it('uses fallbackTitle when runtime.taskTitle empty', () => {
    const task = buildTaskFromRuntime({ taskId: 't-3' }, '我的对话');
    expect(task.title).toBe('我的对话');
  });

  it('falls back to "当前任务" when both runtime title and fallback empty', () => {
    expect(buildTaskFromRuntime({ taskId: 't-4' }).title).toBe('当前任务');
    expect(buildTaskFromRuntime({ taskId: 't-5' }, '   ').title).toBe('当前任务');
  });

  it('camelCase takes priority over snake_case when both set', () => {
    const task = buildTaskFromRuntime({
      taskId: 'camel',
      task_id: 'snake',
      taskTitle: '驼峰',
      task_title: '蛇形',
    });
    expect(task.taskId).toBe('camel');
    expect(task.title).toBe('驼峰');
  });
});

describe('useTaskHandoff.continueTaskById', () => {
  it('returns "" for empty threadId without logging skipped', async () => {
    const ctx = makeCtx();
    const { continueTaskById } = useTaskHandoff(ctx);
    const id = await continueTaskById('');
    expect(id).toBe('');
    expect(ctx.threadStore.startThread).not.toHaveBeenCalled();
    expect(logWarn).not.toHaveBeenCalled();
  });

  it('logs skipped + returns "" when threadId not in runtime map', async () => {
    const ctx = makeCtx({ agentRuntimeById: {} });
    const { continueTaskById } = useTaskHandoff(ctx);
    const id = await continueTaskById('unknown-thread');
    expect(id).toBe('');
    expect(ctx.threadStore.startThread).not.toHaveBeenCalled();
    expect(logWarn).toHaveBeenCalledWith('ui', 'taskHandoff.continue.skipped', expect.objectContaining({
      source_thread_id: 'unknown-thread',
      reason: 'no_task_id',
    }));
  });

  it('logs skipped + returns "" when runtime exists but has no taskId', async () => {
    const ctx = makeCtx({
      agentRuntimeById: { 'plain-thread': { provider: 'codex' } },
    });
    const { continueTaskById } = useTaskHandoff(ctx);
    const id = await continueTaskById('plain-thread');
    expect(id).toBe('');
    expect(ctx.threadStore.startThread).not.toHaveBeenCalled();
    expect(logWarn).toHaveBeenCalledWith('ui', 'taskHandoff.continue.skipped', expect.objectContaining({
      source_thread_id: 'plain-thread',
      reason: 'no_task_id',
    }));
  });

  it('happy path: calls startThread with continueTask config and returns new id', async () => {
    const ctx = makeCtx({
      agentRuntimeById: {
        'task-thread': {
          taskId: 't-100',
          taskTitle: '修 Bug',
          handoffFile: 'handoff/tasks/t-100.md',
          ownerThreadId: 'owner-x',
        },
      },
    });
    const { continueTaskById } = useTaskHandoff(ctx);
    const id = await continueTaskById('task-thread');
    expect(id).toBe('new-thread-id');
    expect(ctx.threadStore.startThread).toHaveBeenCalledOnce();
    const [cwd, opts] = ctx.threadStore.startThread.mock.calls[0];
    expect(cwd).toBe('/repo');
    expect(opts.focusMode).toBe('chat');
    expect(opts.name).toBe('修 Bug');
    expect(opts.config).toEqual({
      taskId: 't-100',
      taskTitle: '修 Bug',
      handoffFile: 'handoff/tasks/t-100.md',
      continueTask: true,
      autoTaskHandoff: true,
    });
    expect(logInfo).toHaveBeenCalledWith('ui', 'taskHandoff.continue.start', expect.objectContaining({
      source_thread_id: 'task-thread',
      task_id: 't-100',
    }));
    expect(logInfo).toHaveBeenCalledWith('ui', 'taskHandoff.continue.done', expect.objectContaining({
      source_thread_id: 'task-thread',
      next_thread_id: 'new-thread-id',
      task_id: 't-100',
    }));
  });

  it('passes focusMode "cmd" when isCmd is true', async () => {
    const ctx = makeCtx({
      isCmd: true,
      agentRuntimeById: { 'task-thread': { taskId: 't-200', taskTitle: '命令任务' } },
    });
    const { continueTaskById } = useTaskHandoff(ctx);
    await continueTaskById('task-thread');
    const [, opts] = ctx.threadStore.startThread.mock.calls[0];
    expect(opts.focusMode).toBe('cmd');
  });

  it('uses the window cwd when continuing from dot project scope', async () => {
    const ctx = makeCtx({
      activeProject: '.',
      windowCwd: '/worktrees/task-continue',
      agentRuntimeById: { 'task-thread': { taskId: 't-window', taskTitle: '窗口任务' } },
    });
    const { continueTaskById } = useTaskHandoff(ctx);

    await continueTaskById('task-thread');

    expect(ctx.threadStore.startThread.mock.calls[0][0]).toBe('/worktrees/task-continue');
  });

  it('does nothing when continueBusy already true (concurrent guard)', async () => {
    // 让 startThread 挂起，第一调用还在 in-flight 时再来一次
    let releaseFirst;
    const ctx = makeCtx({
      agentRuntimeById: { 'task-thread': { taskId: 't-300', taskTitle: 'hang' } },
    });
    ctx.threadStore.startThread = vi.fn(() => new Promise((resolve) => { releaseFirst = resolve; }));
    const { continueTaskById } = useTaskHandoff(ctx);
    const firstP = continueTaskById('task-thread');
    // 立即并发触发第二次
    const secondId = await continueTaskById('task-thread');
    expect(secondId).toBe('');
    expect(ctx.threadStore.startThread).toHaveBeenCalledOnce();
    releaseFirst('first-thread-id');
    const firstId = await firstP;
    expect(firstId).toBe('first-thread-id');
  });

  it('logs failure and rethrows when startThread throws', async () => {
    const ctx = makeCtx({
      agentRuntimeById: { 'task-thread': { taskId: 't-400', taskTitle: '失败任务' } },
    });
    ctx.threadStore.startThread = vi.fn(async () => { throw new Error('boom'); });
    const { continueTaskById } = useTaskHandoff(ctx);
    await expect(continueTaskById('task-thread')).rejects.toThrow('boom');
    expect(logWarn).toHaveBeenCalledWith('ui', 'taskHandoff.continue.failed', expect.objectContaining({
      source_thread_id: 'task-thread',
      task_id: 't-400',
    }));
  });

  it('clears continueBusy after failure so next call can proceed', async () => {
    const ctx = makeCtx({
      agentRuntimeById: { 'task-thread': { taskId: 't-500', taskTitle: '恢复测试' } },
    });
    let calls = 0;
    ctx.threadStore.startThread = vi.fn(async () => {
      calls += 1;
      if (calls === 1) throw new Error('first failed');
      return 'second-id';
    });
    const { continueTaskById } = useTaskHandoff(ctx);
    await expect(continueTaskById('task-thread')).rejects.toThrow('first failed');
    const id = await continueTaskById('task-thread');
    expect(id).toBe('second-id');
    expect(calls).toBe(2);
  });

  // Phase 1.4b：焦点判断
  it('passes skipSaveActive=true when source differs from selected (后台不抢焦点)', async () => {
    const ctx = makeCtx({
      selectedThreadId: 'other-selected',
      agentRuntimeById: { 'task-thread': { taskId: 't-bg', taskTitle: '后台任务' } },
    });
    const { continueTaskById } = useTaskHandoff(ctx);
    await continueTaskById('task-thread');
    const [, opts] = ctx.threadStore.startThread.mock.calls[0];
    expect(opts.skipSaveActive).toBe(true);
    expect(logInfo).toHaveBeenCalledWith('ui', 'taskHandoff.continue.start', expect.objectContaining({
      focus_followed: false,
    }));
    expect(logInfo).toHaveBeenCalledWith('ui', 'taskHandoff.continue.done', expect.objectContaining({
      focus_followed: false,
    }));
  });

  it('passes skipSaveActive=false when source matches selected (焦点跟随)', async () => {
    const ctx = makeCtx({
      selectedThreadId: 'task-thread',
      agentRuntimeById: { 'task-thread': { taskId: 't-fg', taskTitle: '前台任务' } },
    });
    const { continueTaskById } = useTaskHandoff(ctx);
    await continueTaskById('task-thread');
    const [, opts] = ctx.threadStore.startThread.mock.calls[0];
    expect(opts.skipSaveActive).toBe(false);
    expect(logInfo).toHaveBeenCalledWith('ui', 'taskHandoff.continue.start', expect.objectContaining({
      focus_followed: true,
    }));
  });

  it('preserves config / focusMode / name when wrapping with skipSaveActive', async () => {
    const ctx = makeCtx({
      selectedThreadId: 'other',
      agentRuntimeById: { 'task-thread': { taskId: 't-cfg', taskTitle: '配置保留', handoffFile: 'h.md' } },
    });
    const { continueTaskById } = useTaskHandoff(ctx);
    await continueTaskById('task-thread');
    const [, opts] = ctx.threadStore.startThread.mock.calls[0];
    expect(opts.focusMode).toBe('chat');
    expect(opts.name).toBe('配置保留');
    expect(opts.config.taskId).toBe('t-cfg');
    expect(opts.config.continueTask).toBe(true);
    expect(opts.config.autoTaskHandoff).toBe(true);
  });
});

describe('useTaskHandoff.continueCurrentTask (wrapper)', () => {
  it('delegates to continueTaskById with selectedThreadId', async () => {
    const ctx = makeCtx({
      selectedThreadId: 'task-thread',
      agentRuntimeById: { 'task-thread': { taskId: 't-700', taskTitle: '当前线程' } },
    });
    const { continueCurrentTask } = useTaskHandoff(ctx);
    const id = await continueCurrentTask();
    expect(id).toBe('new-thread-id');
    expect(ctx.threadStore.startThread).toHaveBeenCalledOnce();
    const [, opts] = ctx.threadStore.startThread.mock.calls[0];
    expect(opts.config.taskId).toBe('t-700');
  });

  it('returns "" when no thread selected', async () => {
    const ctx = makeCtx({ selectedThreadId: '' });
    const { continueCurrentTask } = useTaskHandoff(ctx);
    const id = await continueCurrentTask();
    expect(id).toBe('');
    expect(ctx.threadStore.startThread).not.toHaveBeenCalled();
  });

  it('returns "" when selected thread has no taskId (普通对话不可续)', async () => {
    const ctx = makeCtx({
      selectedThreadId: 'plain-thread',
      agentRuntimeById: { 'plain-thread': { provider: 'codex' } },
    });
    const { continueCurrentTask } = useTaskHandoff(ctx);
    const id = await continueCurrentTask();
    expect(id).toBe('');
    expect(logWarn).toHaveBeenCalledWith('ui', 'taskHandoff.continue.skipped', expect.objectContaining({
      reason: 'no_task_id',
    }));
  });
});

describe('useTaskHandoff.continueCurrentTaskInNewWindow', () => {
  it('sets visible error state and rethrows openNewWindow failures', async () => {
    vi.mocked(callAPI).mockRejectedValueOnce(new Error('window launch failed'));
    const ctx = makeCtx({
      runtime: { taskId: 't-window', taskTitle: '窗口续接' },
    });
    const vm = useTaskHandoff(ctx);

    await expect(vm.continueCurrentTaskInNewWindow()).rejects.toThrow('window launch failed');

    expect(vm.taskHandoffError.value).toBe('window launch failed');
    expect(logWarn).toHaveBeenCalledWith('ui', 'taskHandoff.continue.new_window.failed', expect.objectContaining({
      source_thread_id: 'src-thread',
      task_id: 't-window',
      error: 'window launch failed',
    }));
  });

  it('does not surface late openNewWindow failures after selected task changes', async () => {
    const pendingOpen = createDeferred();
    vi.mocked(callAPI).mockImplementation(async (method) => {
      if (method === 'ui/openNewWindow') return pendingOpen.promise;
      return {};
    });
    const ctx = makeCtx({
      selectedThreadId: 'thread-a',
      threadName: 'Thread A',
      runtime: { taskId: 'task-a', taskTitle: 'Task A', handoffFile: 'handoff/a.md' },
    });
    const vm = useTaskHandoff(ctx);

    const openPromise = vm.continueCurrentTaskInNewWindow().catch(() => {});
    ctx.selectedThreadId.value = 'thread-b';
    ctx.activeThread.value = { id: 'thread-b', name: 'Thread B' };
    ctx.activeRuntime.value = { taskId: 'task-b', taskTitle: 'Task B', handoffFile: 'handoff/b.md' };

    const handledPendingOpen = pendingOpen.promise.catch(() => {});
    pendingOpen.reject(new Error('window launch failed'));
    await Promise.all([openPromise, handledPendingOpen]);

    expect(vm.taskHandoffError.value).toBe('');
    expect(logWarn).not.toHaveBeenCalledWith('ui', 'taskHandoff.continue.new_window.failed', expect.anything());
  });
});

describe('taskStripExpanded 默认折叠 + 事件展开', () => {
  it('initial state 默认为折叠（false）', () => {
    const ctx = makeCtx({ runtime: { taskId: 't-collapse-init', handoffFile: 'h.md' } });
    const { taskStripExpanded } = useTaskHandoff(ctx);
    expect(taskStripExpanded.value).toBe(false);
  });

  it('toggleTaskStrip 在 true / false 之间切换', () => {
    const ctx = makeCtx({ runtime: { taskId: 't-toggle', handoffFile: 'h.md' } });
    const { taskStripExpanded, toggleTaskStrip } = useTaskHandoff(ctx);
    expect(taskStripExpanded.value).toBe(false);
    toggleTaskStrip();
    expect(taskStripExpanded.value).toBe(true);
    toggleTaskStrip();
    expect(taskStripExpanded.value).toBe(false);
  });

  it('expandTaskStrip 强制展开，collapseTaskStrip 强制折叠', () => {
    const ctx = makeCtx({ runtime: { taskId: 't-cmd', handoffFile: 'h.md' } });
    const { taskStripExpanded, expandTaskStrip, collapseTaskStrip } = useTaskHandoff(ctx);
    expandTaskStrip('test_reason');
    expect(taskStripExpanded.value).toBe(true);
    collapseTaskStrip();
    expect(taskStripExpanded.value).toBe(false);
  });

  it('load 错误出现时自动展开', async () => {
    vi.mocked(callAPI).mockRejectedValueOnce(new Error('boom'));
    const ctx = makeCtx({ runtime: { taskId: 't-err', handoffFile: 'handoff/err.md' } });
    const { taskStripExpanded, taskHandoffError } = useTaskHandoff(ctx);
    // 初始 watch immediate 改变 activeTask trigger 了 loadTaskHandoff。错误被集成后，
    // expanded 应该被 error watcher 推为 true。
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
    expect(taskHandoffError.value).toBe('boom');
    expect(taskStripExpanded.value).toBe(true);
  });

  it('ignores late handoff content after selected task changes', async () => {
    const pendingLoad = createDeferred();
    vi.mocked(callAPI).mockReturnValueOnce(pendingLoad.promise);
    const ctx = makeCtx({ runtime: null });
    const vm = useTaskHandoff(ctx);

    ctx.selectedThreadId.value = 'thread-a';
    ctx.activeThread.value = { id: 'thread-a', name: 'Thread A' };
    ctx.activeRuntime.value = { taskId: 'task-a', handoffFile: 'handoff/a.md' };
    const loadPromise = vm.refreshTaskHandoff();

    ctx.selectedThreadId.value = 'thread-b';
    ctx.activeThread.value = { id: 'thread-b', name: 'Thread B' };
    ctx.activeRuntime.value = { taskId: 'task-b', handoffFile: 'handoff/b.md' };

    pendingLoad.resolve({ content: 'handoff from task A', updatedAt: 'old', updatedBy: 'agent-a' });
    await loadPromise;

    expect(vm.taskHandoffPreview.value).toBe('');
    expect(vm.taskHandoffError.value).toBe('');
    expect(vm.taskHandoffUpdatedAt.value).toBe('');
    expect(vm.taskHandoffUpdatedBy.value).toBe('');
  });

  it('ignores late handoff load errors after selected task changes', async () => {
    const pendingLoad = createDeferred();
    vi.mocked(callAPI).mockReturnValueOnce(pendingLoad.promise);
    const ctx = makeCtx({ runtime: null });
    const vm = useTaskHandoff(ctx);

    ctx.selectedThreadId.value = 'thread-a';
    ctx.activeThread.value = { id: 'thread-a', name: 'Thread A' };
    ctx.activeRuntime.value = { taskId: 'task-a', handoffFile: 'handoff/a.md' };
    const loadPromise = vm.refreshTaskHandoff().catch(() => {});

    ctx.selectedThreadId.value = 'thread-b';
    ctx.activeThread.value = { id: 'thread-b', name: 'Thread B' };
    ctx.activeRuntime.value = { taskId: 'task-b', handoffFile: 'handoff/b.md' };

    const handledPendingLoad = pendingLoad.promise.catch(() => {});
    pendingLoad.reject(new Error('task A handoff failed'));
    await Promise.all([loadPromise, handledPendingLoad]);

    expect(vm.taskHandoffError.value).toBe('');
    expect(vm.taskStripExpanded.value).toBe(false);
  });

  // 注：「切 thread/task 重置为折叠」这个行为由 useTaskHandoff 里 immediate+sync
  // 的 watch 保证，在生产环境里随 activeRuntime 赋值同步生效。测试环境里
  // test 文件和 composable 分别从 './lib/...' 与 '../../lib/...' 加载 Vue，
  // 极端条件下两边拿不到同一个 reactivity 实例，跨实例 watch 不会 fire。
  // 改体：不直接断言 watch fire，而是靠上面几个 case 锁住 manual API（toggle/expand/collapse）
  // + load 错误自动展开这个同 watch 同机制的 case 间接证明 watcher 在测试
  // 环境里是工作的。
});

describe('useTaskHandoff.startNewTaskFromHandoff kickoff', () => {
  // 用户报告：「自动化任务」点「以此新建任务」按钮没生成对话——侧栏 / 焦点其实切到
  // 了新 thread（startThread 默认 saveActive），但新 thread 是空白的因为没有任何
  // user message 触发 agent 开场。参照 fork-kickoff 模式：startThread 成功后主动发
  // 一条 prompt 让 agent 基于 baseInstructions 总结接力摘要 + 提建议。

  it('成功 startThread 后发 kickoff prompt（与 fork-kickoff 同款体验）', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce({ content: '上次完成了什么…' });
    const ctx = makeCtx({
      runtime: { taskId: 't-new-1', taskTitle: '迁移任务', handoffFile: 'handoff/t-new-1.md' },
    });
    const { startNewTaskFromHandoff } = useTaskHandoff(ctx);
    // 等 immediate watch 触发的 loadTaskHandoff 完成（state.content 才会被填）
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
    const id = await startNewTaskFromHandoff();
    expect(id).toBe('new-thread-id');
    expect(ctx.threadStore.sendMessage).toHaveBeenCalledOnce();
    const [threadId, prompt, attachments, opts] = ctx.threadStore.sendMessage.mock.calls[0];
    expect(threadId).toBe('new-thread-id');
    expect(prompt).toContain('请基于上文任务接力摘要');
    expect(Array.isArray(attachments)).toBe(true);
    expect(opts).toEqual(expect.objectContaining({ kickoff: true }));
    expect(logInfo).toHaveBeenCalledWith('ui', 'taskHandoff.kickoff_sent', expect.objectContaining({
      next_thread_id: 'new-thread-id',
      task_id: 't-new-1',
    }));
  });

  it('uses the window cwd when starting a new task from dot project scope', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce({ content: '上次完成了什么…' });
    const ctx = makeCtx({
      activeProject: '.',
      windowCwd: '/worktrees/task-new',
      runtime: { taskId: 't-new-window', taskTitle: '窗口新任务', handoffFile: 'handoff/t-new-window.md' },
    });
    const { startNewTaskFromHandoff } = useTaskHandoff(ctx);
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();

    await startNewTaskFromHandoff();

    expect(ctx.threadStore.startThread.mock.calls[0][0]).toBe('/worktrees/task-new');
  });

  it('threadStore 没 sendMessage 时仍返回 thread id 但 logWarn 跳过 kickoff', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce({ content: '上次完成了什么…' });
    const ctx = makeCtx({
      runtime: { taskId: 't-new-2', taskTitle: 'noSend', handoffFile: 'handoff/t-new-2.md' },
      withSendMessage: false,
    });
    const { startNewTaskFromHandoff } = useTaskHandoff(ctx);
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
    const id = await startNewTaskFromHandoff();
    expect(id).toBe('new-thread-id');
    expect(logWarn).toHaveBeenCalledWith('ui', 'taskHandoff.kickoff_no_send_message', expect.objectContaining({
      next_thread_id: 'new-thread-id',
    }));
  });

  it('kickoff 失败时清 kickoffByThread 让 timeline 不过滤 user prompt（review P2 教训）', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce({ content: '上次完成了什么…' });
    const ctx = makeCtx({
      runtime: { taskId: 't-new-3', taskTitle: 'kickFail', handoffFile: 'handoff/t-new-3.md' },
      sendMessageImpl: async () => { throw new Error('rpc fail'); },
    });
    // 模拟 sendMessage 内部抛错前已写入 kickoffByThread（生产路径就是这个时序）
    ctx.threadStore.state.kickoffByThread = { 'new-thread-id': 'pending-prompt' };
    const { startNewTaskFromHandoff } = useTaskHandoff(ctx);
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
    const id = await startNewTaskFromHandoff();
    expect(id).toBe('new-thread-id'); // startThread 主流程仍成功，kickoff 是次要错误
    expect(ctx.threadStore.state.kickoffByThread['new-thread-id']).toBeUndefined();
    expect(logWarn).toHaveBeenCalledWith('ui', 'taskHandoff.kickoff_failed', expect.objectContaining({
      next_thread_id: 'new-thread-id',
      error: expect.stringContaining('rpc fail'),
    }));
  });

  it('kickoff 失败时不动其它 thread 的 kickoffByThread entry', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce({ content: '上次完成了什么…' });
    const ctx = makeCtx({
      runtime: { taskId: 't-new-4', taskTitle: 'isolation', handoffFile: 'handoff/t-new-4.md' },
      sendMessageImpl: async () => { throw new Error('rpc fail'); },
    });
    ctx.threadStore.state.kickoffByThread = { 'other-thread': 'other prompt' };
    const { startNewTaskFromHandoff } = useTaskHandoff(ctx);
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
    await startNewTaskFromHandoff();
    expect(ctx.threadStore.state.kickoffByThread['other-thread']).toBe('other prompt');
  });

  // F1：完善 fork 路径踩过的 review M1 同款坑——kickoff 失败要单独暴露 kickoffError ref
  // 让 UI 能设 banner/toast，不能只靠「看到 prompt 原文」这种间接反馈。
  // 与现有 taskHandoffError（load 错误）区分，单独叫 taskHandoffKickoffError。

  it('kickoff 成功时 taskHandoffKickoffError 保持为空', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce({ content: '上次完成了什么…' });
    const ctx = makeCtx({
      runtime: { taskId: 't-new-5', taskTitle: 'success', handoffFile: 'handoff/t-new-5.md' },
    });
    const { startNewTaskFromHandoff, taskHandoffKickoffError } = useTaskHandoff(ctx);
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
    await startNewTaskFromHandoff();
    expect(taskHandoffKickoffError.value).toBe('');
  });

  it('kickoff 失败时 taskHandoffKickoffError 含错误原文（UI 可接警示）', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce({ content: '上次完成了什么…' });
    const ctx = makeCtx({
      runtime: { taskId: 't-new-6', taskTitle: 'errorSurface', handoffFile: 'handoff/t-new-6.md' },
      sendMessageImpl: async () => { throw new Error('rpc detonated'); },
    });
    const { startNewTaskFromHandoff, taskHandoffKickoffError } = useTaskHandoff(ctx);
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
    await startNewTaskFromHandoff();
    expect(taskHandoffKickoffError.value).toContain('rpc detonated');
  });

  it('多次 startNewTaskFromHandoff 之间 taskHandoffKickoffError 会被重置（避免上次错误脱饱）', async () => {
    let calls = 0;
    vi.mocked(callAPI).mockResolvedValue({ content: '上次完成了什么…' });
    const ctx = makeCtx({
      runtime: { taskId: 't-new-7', taskTitle: 'reset', handoffFile: 'handoff/t-new-7.md' },
      sendMessageImpl: async () => {
        calls += 1;
        if (calls === 1) throw new Error('first kickoff fail');
      },
    });
    const { startNewTaskFromHandoff, taskHandoffKickoffError } = useTaskHandoff(ctx);
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
    await startNewTaskFromHandoff();
    expect(taskHandoffKickoffError.value).toContain('first kickoff fail');
    // 第二次调用成功：error 应该为空（不脱饱上次的 'first kickoff fail'）
    await startNewTaskFromHandoff();
    expect(taskHandoffKickoffError.value).toBe('');
  });
});
