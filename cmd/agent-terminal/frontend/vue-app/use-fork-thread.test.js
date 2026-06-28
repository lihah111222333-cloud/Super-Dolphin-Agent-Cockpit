// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ref, reactive } from '../lib/vue.esm-browser.prod.js';

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(),
}));
vi.mock('./services/log.js', () => ({
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

const { callAPI } = await import('./services/api.js');
const { useForkThread } = await import('./composables/useForkThread.js');

function createDeferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function makeCtx({
  timeline = [],
  sharedFilePaths = [],
  threadName = 'src-thread',
  sourceRuntime = null,
  withSendMessage = true,
  sendMessageImpl = null,
  activeProject = '/repo',
  windowCwd = '',
} = {}) {
  const composer = {
    forkDraft: reactive({ active: true, sharedFilePaths: [...sharedFilePaths], origin: '' }),
    closeForkDraft: vi.fn(() => {
      composer.forkDraft.active = false;
      composer.forkDraft.sharedFilePaths = [];
    }),
  };
  const agentRuntimeById = {};
  if (sourceRuntime) agentRuntimeById['src-thread'] = sourceRuntime;
  const threadStore = {
    getThreadTimeline: vi.fn(() => timeline),
    startThread: vi.fn(async () => 'new-thread-id'),
    state: { agentRuntimeById },
  };
  if (withSendMessage) {
    threadStore.sendMessage = vi.fn(sendMessageImpl || (async () => {}));
  }
  const projectStore = { state: reactive({ active: activeProject }) };
  return {
    composer,
    threadStore,
    projectStore,
    selectedThreadId: ref('src-thread'),
    activeThread: ref({ id: 'src-thread', name: threadName }),
    isCmd: ref(false),
    windowCwd,
  };
}

beforeEach(() => {
  vi.mocked(callAPI).mockReset();
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe('useForkThread.submit', () => {
  it('returns empty string and sets error when no summary and no shared files', async () => {
    const ctx = makeCtx({ timeline: [], sharedFilePaths: [] });
    const { submit, error } = useForkThread(ctx);
    const id = await submit();
    expect(id).toBe('');
    expect(error.value).toContain('没有可用上下文');
    expect(ctx.threadStore.startThread).not.toHaveBeenCalled();
  });

  it('builds baseInstructions from timeline-only summary and starts thread', async () => {
    const ctx = makeCtx({
      timeline: [
        { role: 'user', text: 'first message' },
        { role: 'assistant', text: 'reply' },
      ],
    });
    const { submit } = useForkThread(ctx);
    const id = await submit();
    expect(id).toBe('new-thread-id');
    expect(ctx.threadStore.startThread).toHaveBeenCalledOnce();
    const [cwd, opts] = ctx.threadStore.startThread.mock.calls[0];
    expect(cwd).toBe('/repo');
    expect(opts.focusMode).toBe('chat');
    expect(opts.name).toContain('src-thread');
    expect(opts.baseInstructions).toContain('摘要：');
    expect(opts.baseInstructions).toContain('first message');
    expect(opts.baseInstructions).toContain('reply');
    // shared files section should be absent
    expect(opts.baseInstructions).not.toContain('挂载的共享文件');
    // close should be called after success
    expect(ctx.composer.closeForkDraft).toHaveBeenCalledOnce();
  });

  it('uses the window cwd when the active project is dot', async () => {
    const ctx = makeCtx({
      activeProject: '.',
      windowCwd: '/worktrees/fork',
      timeline: [{ role: 'user', text: 'first message' }],
    });

    const { submit } = useForkThread(ctx);
    await submit();

    expect(ctx.threadStore.startThread.mock.calls[0][0]).toBe('/worktrees/fork');
  });

  it('loads shared files via callAPI and includes them in baseInstructions', async () => {
    vi.mocked(callAPI).mockImplementation(async (method, params) => {
      if (method === 'ui/memory/shared-file/get') {
        return { path: params.path, content: `content for ${params.path}` };
      }
      return null;
    });
    const ctx = makeCtx({
      timeline: [{ role: 'user', text: 'hi' }],
      sharedFilePaths: ['notes/a.md', 'notes/b.md'],
    });
    const { submit } = useForkThread(ctx);
    const id = await submit();
    expect(id).toBe('new-thread-id');
    expect(callAPI).toHaveBeenCalledWith('ui/memory/shared-file/get', { path: 'notes/a.md' });
    expect(callAPI).toHaveBeenCalledWith('ui/memory/shared-file/get', { path: 'notes/b.md' });
    const [, opts] = ctx.threadStore.startThread.mock.calls[0];
    expect(opts.baseInstructions).toContain('挂载的共享文件');
    expect(opts.baseInstructions).toContain('共享文件：notes/a.md');
    expect(opts.baseInstructions).toContain('共享文件：notes/b.md');
    expect(opts.baseInstructions).toContain('content for notes/a.md');
  });

  it('fails fast when a selected shared file fails to load', async () => {
    vi.mocked(callAPI).mockImplementation(async (method, params) => {
      if (params.path === 'good.md') return { path: 'good.md', content: 'good content' };
      throw new Error('not found');
    });
    const ctx = makeCtx({
      timeline: [{ role: 'user', text: 'hi' }],
      sharedFilePaths: ['good.md', 'bad.md'],
    });
    const { submit, error } = useForkThread(ctx);
    await expect(submit()).rejects.toThrow('not found');
    expect(error.value).toContain('not found');
    expect(ctx.threadStore.startThread).not.toHaveBeenCalled();
  });

  it('does not surface late submit failures after selected thread changes', async () => {
    const pendingSharedFile = createDeferred();
    vi.mocked(callAPI).mockImplementation(async (method, params) => {
      if (method === 'ui/memory/shared-file/get' && params.path === 'notes/a.md') {
        return pendingSharedFile.promise;
      }
      return {};
    });
    const ctx = makeCtx({
      timeline: [{ role: 'user', text: 'hi from A' }],
      sharedFilePaths: ['notes/a.md'],
    });
    const { submit, error } = useForkThread(ctx);

    const submitPromise = submit().catch(() => {});
    ctx.selectedThreadId.value = 'thread-b';
    ctx.activeThread.value = { id: 'thread-b', name: 'Thread B' };

    const handledPendingSharedFile = pendingSharedFile.promise.catch(() => {});
    pendingSharedFile.reject(new Error('shared file failed'));
    await Promise.all([submitPromise, handledPendingSharedFile]);

    expect(error.value).toBe('');
    expect(ctx.threadStore.startThread).not.toHaveBeenCalled();
  });

  it('keeps the source title captured when submit continues after a thread switch', async () => {
    const pendingSharedFile = createDeferred();
    vi.mocked(callAPI).mockImplementation(async (method, params) => {
      if (method === 'ui/memory/shared-file/get' && params.path === 'notes/a.md') {
        return pendingSharedFile.promise;
      }
      return {};
    });
    const ctx = makeCtx({
      threadName: 'Thread A',
      timeline: [{ role: 'user', text: 'hi from A' }],
      sharedFilePaths: ['notes/a.md'],
    });
    const { submit } = useForkThread(ctx);

    const submitPromise = submit();
    ctx.selectedThreadId.value = 'thread-b';
    ctx.activeThread.value = { id: 'thread-b', name: 'Thread B' };
    pendingSharedFile.resolve({ path: 'notes/a.md', content: 'content from A' });
    await submitPromise;

    const [, opts] = ctx.threadStore.startThread.mock.calls[0];
    expect(opts.name).toBe('继承自会话：Thread A');
    expect(opts.baseInstructions).toContain('来源：继承自会话：Thread A');
    expect(opts.baseInstructions).not.toContain('Thread B');
  });

  it('does not call closeForkDraft when startThread returns empty id', async () => {
    const ctx = makeCtx({ timeline: [{ role: 'user', text: 'hi' }] });
    ctx.threadStore.startThread.mockResolvedValueOnce('');
    const { submit } = useForkThread(ctx);
    const id = await submit();
    expect(id).toBe('');
    expect(ctx.composer.closeForkDraft).not.toHaveBeenCalled();
  });

  it('rejects concurrent submit calls (lock via submitting flag)', async () => {
    const ctx = makeCtx({ timeline: [{ role: 'user', text: 'hi' }] });
    let resolveStart;
    ctx.threadStore.startThread.mockImplementation(() => new Promise((r) => { resolveStart = r; }));
    const { submit, submitting } = useForkThread(ctx);
    const first = submit();
    expect(submitting.value).toBe(true);
    const second = await submit();
    expect(second).toBe(''); // second call short-circuits
    resolveStart('thread-late');
    await first;
    expect(ctx.threadStore.startThread).toHaveBeenCalledOnce();
  });

  // ── Phase 4-fork-kickoff：agent 主动开场 ──

  it('源是普通对话时 startThread 后调 sendMessage 发 kickoff（options.kickoff=true）', async () => {
    const ctx = makeCtx({ timeline: [{ role: 'user', text: 'hi' }] });
    const { submit } = useForkThread(ctx);
    const id = await submit();
    expect(id).toBe('new-thread-id');
    expect(ctx.threadStore.sendMessage).toHaveBeenCalledOnce();
    const [tid, prompt, attachments, options] = ctx.threadStore.sendMessage.mock.calls[0];
    expect(tid).toBe('new-thread-id');
    expect(prompt).toContain('上文摘要');
    expect(prompt).toContain('下一步建议');
    expect(attachments).toEqual([]);
    expect(options.kickoff).toBe(true);
  });

  it('source runtime metadata does not suppress kickoff', async () => {
    const ctx = makeCtx({
      timeline: [{ role: 'user', text: 'hi' }],
      sourceRuntime: { sessionFlags: { persistentSubagentDefault: true } },
    });
    const { submit } = useForkThread(ctx);
    await submit();
    expect(ctx.threadStore.sendMessage).toHaveBeenCalledOnce();
  });

  it('kickoff sendMessage 抛错时 fork 主流程仍返回新 thread id（review M1：kickoffError 被设）', async () => {
    const ctx = makeCtx({
      timeline: [{ role: 'user', text: 'hi' }],
      sendMessageImpl: async () => { throw new Error('rpc fail'); },
    });
    const { submit, error, kickoffError } = useForkThread(ctx);
    const id = await submit();
    expect(id).toBe('new-thread-id'); // 不破 fork
    expect(ctx.composer.closeForkDraft).toHaveBeenCalledOnce();
    // M1：fork 主 error 不被污染，kickoff 错误单独 surface 给 UI
    expect(error.value).toBe('');
    expect(kickoffError.value).toContain('rpc fail');
  });

  it('kickoff 失败时清 kickoffByThread 让 timeline 不过滤 user prompt（review P2 部分修）', async () => {
    const ctx = makeCtx({
      timeline: [{ role: 'user', text: 'hi' }],
      sendMessageImpl: async () => { throw new Error('rpc fail'); },
    });
    // 模拟 sendMessage 在抛错前已经写入 kickoffByThread（生产路径就是这个时序）
    ctx.threadStore.state.kickoffByThread = { 'new-thread-id': '请基于上文摘要…' };
    const { submit } = useForkThread(ctx);
    await submit();
    // catch 路径应当 delete entry，避免 timeline selector 过滤这条 user message——
    // 用户至少能看到 kickoff prompt 原文，比「完全空白」更可定位
    expect(ctx.threadStore.state.kickoffByThread['new-thread-id']).toBeUndefined();
  });

  it('kickoff 成功时不动其它 thread 的 kickoffByThread entry', async () => {
    const ctx = makeCtx({ timeline: [{ role: 'user', text: 'hi' }] });
    // 别的 thread 已有 entry，本次 fork 成功不应误删
    ctx.threadStore.state.kickoffByThread = { 'other-thread': 'other prompt' };
    const { submit } = useForkThread(ctx);
    await submit();
    // 别 thread 不被动
    expect(ctx.threadStore.state.kickoffByThread['other-thread']).toBe('other prompt');
  });

  it('多次 submit 之间 kickoffError 会被重置（避免上次失败错误被下次看到）', async () => {
    let shouldFail = true;
    const ctx = makeCtx({
      timeline: [{ role: 'user', text: 'hi' }],
      sendMessageImpl: async () => {
        if (shouldFail) throw new Error('first attempt fail');
      },
    });
    const { submit, kickoffError } = useForkThread(ctx);
    await submit();
    expect(kickoffError.value).toContain('first attempt fail');
    // 第二次 fork 时 sendMessage 不抛——kickoffError 应被清零
    shouldFail = false;
    ctx.composer.forkDraft.active = true; // 重新开草稿模拟用户再点 fork
    await submit();
    expect(kickoffError.value).toBe('');
  });

  it('threadStore 缺 sendMessage 时静默跳过 kickoff（向后兼容）', async () => {
    const ctx = makeCtx({
      timeline: [{ role: 'user', text: 'hi' }],
      withSendMessage: false,
    });
    const { submit } = useForkThread(ctx);
    const id = await submit();
    expect(id).toBe('new-thread-id');
  });
});
