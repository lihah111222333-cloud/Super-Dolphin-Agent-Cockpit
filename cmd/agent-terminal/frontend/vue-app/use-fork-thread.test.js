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
const { useForkThread } = await import('./composables/useForkThread.js');

function makeCtx({ timeline = [], sharedFilePaths = [], threadName = 'src-thread' } = {}) {
  const composer = {
    forkDraft: reactive({ active: true, sharedFilePaths: [...sharedFilePaths], origin: '' }),
    closeForkDraft: vi.fn(() => {
      composer.forkDraft.active = false;
      composer.forkDraft.sharedFilePaths = [];
    }),
  };
  const threadStore = {
    getThreadTimeline: vi.fn(() => timeline),
    startThread: vi.fn(async () => 'new-thread-id'),
  };
  const projectStore = { state: reactive({ active: '/repo' }) };
  return {
    composer,
    threadStore,
    projectStore,
    selectedThreadId: ref('src-thread'),
    activeThread: ref({ id: 'src-thread', name: threadName }),
    isCmd: ref(false),
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

  it('proceeds with summary when some shared files fail to load', async () => {
    vi.mocked(callAPI).mockImplementation(async (method, params) => {
      if (params.path === 'good.md') return { path: 'good.md', content: 'good content' };
      throw new Error('not found');
    });
    const ctx = makeCtx({
      timeline: [{ role: 'user', text: 'hi' }],
      sharedFilePaths: ['good.md', 'bad.md'],
    });
    const { submit } = useForkThread(ctx);
    const id = await submit();
    expect(id).toBe('new-thread-id');
    const [, opts] = ctx.threadStore.startThread.mock.calls[0];
    expect(opts.baseInstructions).toContain('共享文件：good.md');
    expect(opts.baseInstructions).not.toContain('共享文件：bad.md');
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
});
