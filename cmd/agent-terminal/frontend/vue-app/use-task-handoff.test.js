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
} = {}) {
  const threadStore = {
    state: reactive({ agentRuntimeById }),
    startThread: vi.fn(async () => 'new-thread-id'),
  };
  const projectStore = { state: reactive({ active: '/repo' }) };
  return {
    threadStore,
    projectStore,
    selectedThreadId: ref(selectedThreadIdValue),
    activeThread: ref({ id: selectedThreadIdValue, name: threadName }),
    activeRuntime: ref(runtime),
    isCmd: ref(isCmdValue),
  };
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
