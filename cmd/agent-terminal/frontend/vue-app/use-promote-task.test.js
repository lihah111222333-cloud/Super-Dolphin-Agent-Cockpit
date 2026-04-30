// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(),
}));
vi.mock('./services/log.js', () => ({
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

const { callAPI } = await import('./services/api.js');
const { logInfo, logWarn } = await import('./services/log.js');
const { usePromoteTask } = await import('./composables/usePromoteTask.js');

beforeEach(() => {
  vi.mocked(callAPI).mockReset();
  vi.mocked(logInfo).mockReset();
  vi.mocked(logWarn).mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('usePromoteTask', () => {
  it('promoteTaskFromThread happy path: fires RPC + emits start/done logs', async () => {
    vi.mocked(callAPI).mockResolvedValue({
      threadId: 'thread-A',
      taskId: 'task_minted',
      handoffFile: 'handoff/tasks/task_minted.md',
      alreadyTask: false,
    });
    const { promoteTaskFromThread, promoting } = usePromoteTask();
    expect(promoting.value).toBe(false);

    const promise = promoteTaskFromThread('thread-A');
    expect(promoting.value).toBe(true);

    const result = await promise;
    expect(result?.taskId).toBe('task_minted');
    expect(promoting.value).toBe(false);

    expect(callAPI).toHaveBeenCalledTimes(1);
    expect(callAPI).toHaveBeenCalledWith('ui/thread/promote-task', { threadId: 'thread-A' });
    expect(logInfo).toHaveBeenCalledWith('ui', 'taskHandoff.promote.start', { source_thread_id: 'thread-A' });
    expect(logInfo).toHaveBeenCalledWith('ui', 'taskHandoff.promote.done', expect.objectContaining({
      source_thread_id: 'thread-A',
      task_id: 'task_minted',
      already_task: false,
    }));
    expect(logWarn).not.toHaveBeenCalled();
  });

  it('snake_case backend response is normalized in done log', async () => {
    vi.mocked(callAPI).mockResolvedValue({
      thread_id: 'thread-B',
      task_id: 'task_existing',
      already_task: true,
      handoff_shell_warning: '',
    });
    const { promoteTaskFromThread } = usePromoteTask();
    await promoteTaskFromThread('thread-B');
    expect(logInfo).toHaveBeenCalledWith('ui', 'taskHandoff.promote.done', expect.objectContaining({
      task_id: 'task_existing',
      already_task: true,
    }));
  });

  it('busy lock returns null on double-click without firing second RPC', async () => {
    let resolveCall;
    vi.mocked(callAPI).mockImplementation(() => new Promise((r) => { resolveCall = r; }));
    const { promoteTaskFromThread, promoting } = usePromoteTask();
    const first = promoteTaskFromThread('thread-busy');
    expect(promoting.value).toBe(true);
    const second = await promoteTaskFromThread('thread-busy');
    expect(second).toBeNull();
    expect(callAPI).toHaveBeenCalledTimes(1);
    resolveCall({ taskId: 'task_x' });
    await first;
    expect(promoting.value).toBe(false);
  });

  it('blank threadId throws synchronous-looking error and never calls RPC', async () => {
    const { promoteTaskFromThread } = usePromoteTask();
    await expect(promoteTaskFromThread('   ')).rejects.toThrow(/threadId required/);
    expect(callAPI).not.toHaveBeenCalled();
  });

  it('RPC failure surfaces error + sets lastError + logs warn + resets promoting', async () => {
    vi.mocked(callAPI).mockRejectedValue(new Error('thread "missing" not found'));
    const { promoteTaskFromThread, promoting, lastError } = usePromoteTask();
    await expect(promoteTaskFromThread('missing')).rejects.toThrow(/not found/);
    expect(promoting.value).toBe(false);
    expect(lastError.value).toMatch(/not found/);
    expect(logWarn).toHaveBeenCalledWith('ui', 'taskHandoff.promote.failed', expect.objectContaining({
      source_thread_id: 'missing',
      error: expect.stringMatching(/not found/),
    }));
  });

  it('handoffShellWarning surface from backend reaches done log', async () => {
    vi.mocked(callAPI).mockResolvedValue({
      threadId: 'thread-S',
      taskId: 'task_softfail',
      alreadyTask: false,
      handoffShellWarning: 'disk full',
    });
    const { promoteTaskFromThread } = usePromoteTask();
    await promoteTaskFromThread('thread-S');
    expect(logInfo).toHaveBeenCalledWith('ui', 'taskHandoff.promote.done', expect.objectContaining({
      handoff_shell_warning: 'disk full',
    }));
  });

  it('callAPIFn override lets tests inject custom client', async () => {
    const fakeClient = vi.fn().mockResolvedValue({ taskId: 'fake' });
    const { promoteTaskFromThread } = usePromoteTask({ callAPIFn: fakeClient });
    const result = await promoteTaskFromThread('t');
    expect(fakeClient).toHaveBeenCalledWith('ui/thread/promote-task', { threadId: 't' });
    expect(result.taskId).toBe('fake');
    expect(callAPI).not.toHaveBeenCalled();
  });
});
