import { beforeEach, expect, it, vi } from 'vitest';

const backend = vi.hoisted(() => ({
  getThreadMessages: vi.fn(),
  getThreadState: vi.fn(),
  resolveThreadIdentity: vi.fn(),
}));

vi.mock('../../../shared/api/backendApi.js', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    ...backend,
    registerBridgeLogStore: actual.registerBridgeLogStore,
    sendFrontendLogBatch: vi.fn(),
  };
});

import { resetClientStoreForTests, useClientStore } from './useClientStore.js';

const oldTimeline = [{ id: 'old-message', role: 'assistant', kind: 'assistant', text: 'old content', done: true }];

function deferred() {
  let reject;
  let resolve;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, reject, resolve };
}

beforeEach(() => {
  vi.clearAllMocks();
  resetClientStoreForTests({
    cwd: '/repo/app',
    activeProject: '/repo/app',
    activeThreadId: 'thread-a',
    threads: [
      { id: 'thread-a', name: 'Thread A', provider: 'codex', status: 'idle' },
      { id: 'thread-b', name: 'Thread B', provider: 'codex', status: 'idle' },
    ],
    timelinesByThread: { 'thread-a': oldTimeline },
    threadTimelineReadyByThread: { 'thread-a': true },
    threadMessagePaginationByThread: { 'thread-a': { loading: false, hasMore: false } },
    threadStateLoadingByThread: { 'thread-a': false },
    draft: 'old draft',
    attachments: [{ path: '/repo/app/old.txt', name: 'old.txt' }],
  });
  backend.resolveThreadIdentity.mockResolvedValue({
    id: 'thread-b',
    name: 'Thread B',
    provider: 'codex',
    cwd: '/repo/app',
  });
  backend.getThreadMessages.mockResolvedValue({ messages: [] });
});

it('atomically restores active content, draft, and loading when canonical open sync returns false', async () => {
  const snapshot = deferred();
  backend.getThreadState.mockReturnValue(snapshot.promise);

  const opening = useClientStore.getState().openThreadById('thread-b', {
    source: 'sidebar-project-tree',
  });
  await Promise.resolve();
  expect(useClientStore.getState().activeThreadId).toBe('thread-a');
  useClientStore.setState((state) => ({
    timelinesByThread: {
      ...state.timelinesByThread,
      'thread-c': [{ id: 'concurrent-event', role: 'assistant', text: 'must survive' }],
    },
  }));
  snapshot.reject(new Error('thread snapshot unavailable'));
  await expect(opening).resolves.toBe(false);

  const state = useClientStore.getState();
  expect(state.activeThreadId).toBe('thread-a');
  expect(state.threads).toEqual(expect.arrayContaining([expect.objectContaining({ id: 'thread-a' })]));
  expect(state.pendingActiveThreadId).toBe('');
  expect(state.timelinesByThread['thread-a']).toBe(oldTimeline);
  expect(state.timelinesByThread['thread-c']).toEqual([
    expect.objectContaining({ id: 'concurrent-event', text: 'must survive' }),
  ]);
  expect(state.threadTimelineReadyByThread['thread-a']).toBe(true);
  expect(state.threadStateLoadingByThread['thread-b']).toBe(false);
  expect(state.draft).toBe('old draft');
  expect(state.attachments).toEqual([{ path: '/repo/app/old.txt', name: 'old.txt' }]);
});

it('does not silently reject a different thread immediately after a list mutation', async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: 'thread-b',
    threads: [{ id: 'thread-b', name: 'Thread B', provider: 'codex', status: 'idle' }],
    timelinesByThread: { 'thread-b': [] },
  });
  useClientStore.setState({ lastListMutationTime: Date.now() });

  await expect(useClientStore.getState().setActiveThread('thread-b')).resolves.toBe(true);
  expect(useClientStore.getState().activeThreadId).toBe('thread-b');
});

it('keeps the previous active thread when the snapshot succeeds but message loading fails', async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: 'thread-b',
    threads: [{ id: 'thread-b', name: 'Thread B', provider: 'codex', status: 'idle' }],
    timelinesByThread: { 'thread-b': [{ id: 'snapshot-message', role: 'assistant', text: 'snapshot' }] },
  });
  backend.getThreadMessages.mockRejectedValue(new Error('message history unavailable'));

  await expect(useClientStore.getState().setActiveThread('thread-b')).resolves.toBe(false);

  const state = useClientStore.getState();
  expect(state.activeThreadId).toBe('thread-a');
  expect(state.threads).toEqual(expect.arrayContaining([expect.objectContaining({ id: 'thread-a' })]));
  expect(state.pendingActiveThreadId).toBe('');
  expect(state.timelinesByThread['thread-a']).toBe(oldTimeline);
  expect(state.threadStateLoadingByThread['thread-b']).toBe(false);
  expect(state.actionNotice).toEqual(expect.objectContaining({ message: '同步会话失败，请重试。', tone: 'error' }));
});

it('clears a previous thread from another project when opening the target thread fails', async () => {
  resetClientStoreForTests({
    cwd: '/repo/new',
    activeProject: '/repo/new',
    activeThreadId: 'thread-old',
    threads: [
      { id: 'thread-old', name: 'Old project thread', provider: 'codex', status: 'idle', cwd: '/repo/old' },
      { id: 'thread-b', name: 'Thread B', provider: 'codex', status: 'idle', cwd: '/repo/new' },
    ],
    draft: 'draft owned by the old project thread',
    attachments: [{ path: '/repo/old/input.txt', name: 'input.txt' }],
  });
  backend.resolveThreadIdentity.mockResolvedValue({
    id: 'thread-b',
    name: 'Thread B',
    provider: 'codex',
    cwd: '/repo/new',
  });
  backend.getThreadState.mockResolvedValue({
    activeThreadId: 'thread-b',
    threads: [{ id: 'thread-b', name: 'Thread B', provider: 'codex', status: 'idle', cwd: '/repo/new' }],
    timelinesByThread: { 'thread-b': [] },
  });
  backend.getThreadMessages.mockRejectedValue(new Error('message history unavailable'));

  await expect(useClientStore.getState().openThreadById('thread-b', {
    source: 'sidebar-project-tree',
  })).resolves.toBe(false);

  expect(useClientStore.getState()).toEqual(expect.objectContaining({
    activeThreadId: '',
    pendingActiveThreadId: '',
    draft: '',
    attachments: [],
    actionNotice: expect.objectContaining({
      message: '无法打开该会话；已取消旧项目会话选择，请重试后再发送。',
      tone: 'error',
    }),
  }));
});

it('blocks an existing-thread send when its authoritative workspace is absent', async () => {
  resetClientStoreForTests({
    cwd: '/repo/new',
    activeProject: '/repo/new',
    activeThreadId: 'thread-old',
    threads: [{ id: 'thread-old', name: 'Old thread', provider: 'codex', status: 'idle' }],
    draft: 'must remain unsent',
    attachments: [],
  });

  await expect(useClientStore.getState().sendDraft()).rejects.toThrow(
    'reopen the conversation before sending because its authoritative workspace is unavailable',
  );

  expect(useClientStore.getState()).toEqual(expect.objectContaining({
    activeThreadId: 'thread-old',
    draft: 'must remain unsent',
    sending: false,
    actionNotice: expect.objectContaining({
      message: '无法发送：请重新打开目标会话，确认项目后再重试。',
      tone: 'error',
      category: 'thread-scope',
    }),
  }));
});

it('dismisses only the action notice represented by the clicked close button', () => {
  const sentNotice = { message: '消息已发送，等待回复', tone: 'info' };
  const newerNotice = { message: '已收到回复', tone: 'success' };
  useClientStore.setState({ actionNotice: sentNotice });
  const dismiss = useClientStore.getState().dismissActionNotice;

  useClientStore.setState({ actionNotice: newerNotice });
  dismiss(sentNotice);
  expect(useClientStore.getState().actionNotice).toBe(newerNotice);

  dismiss(newerNotice);
  expect(useClientStore.getState().actionNotice).toBeNull();
});
