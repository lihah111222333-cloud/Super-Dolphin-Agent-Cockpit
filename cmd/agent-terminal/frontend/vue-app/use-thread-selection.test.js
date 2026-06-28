// @ts-nocheck
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('./utils/thread-page-utils.js', async () => {
  const actual = await vi.importActual('./utils/thread-page-utils.js');
  return {
    ...actual,
    ensureThreadSelectionFresh: vi.fn(async () => ({
      requestedHistory: false,
      syncedThreadState: false,
      forcedHistoryReload: false,
    })),
  };
});

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logError: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
  registerLogBridgeSink: vi.fn(),
}));

import { nextTick, ref } from '../lib/vue.esm-browser.prod.js';
import { useThreadSelection } from './composables/useThreadSelection.js';
import { ensureThreadSelectionFresh } from './utils/thread-page-utils.js';

const activeHandles = [];

function stopWatcher(handle) {
  if (!handle) return;
  if (typeof handle === 'function') {
    handle();
    return;
  }
  if (typeof handle.stop === 'function') {
    handle.stop();
  }
}

async function flush() {
  await Promise.resolve();
  await Promise.resolve();
  await nextTick();
}

function mountSelection(opts = {}) {
  const selectedThreadId = ref(opts.selectedId ?? '');
  const focusedDiffPath = ref(opts.focusedDiffPath ?? 'stale.diff');
  const focusedDiffLine = ref(opts.focusedDiffLine ?? 99);
  const fallbackDiffText = ref(opts.fallbackDiffText ?? 'stale diff');
  const fallbackMediaPreview = ref(opts.fallbackMediaPreview ?? { kind: 'image' });
  const fallbackMarkdownPreview = ref(opts.fallbackMarkdownPreview ?? { kind: 'markdown' });
  const scheduleScrollToBottom = vi.fn();
  const resetScrollState = opts.resetScrollState ?? vi.fn();
  const threadStore = opts.threadStore ?? { id: 'store' };

  const handle = useThreadSelection({
    selectedThreadId,
    threadStore,
    focusedDiffPath,
    focusedDiffLine,
    fallbackDiffText,
    fallbackMediaPreview,
    fallbackMarkdownPreview,
    scheduleScrollToBottom,
    resetScrollState,
  });
  activeHandles.push(handle);

  return {
    selectedThreadId,
    threadStore,
    focusedDiffPath,
    focusedDiffLine,
    fallbackDiffText,
    fallbackMediaPreview,
    fallbackMarkdownPreview,
    scheduleScrollToBottom,
    resetScrollState,
  };
}

afterEach(() => {
  while (activeHandles.length > 0) {
    stopWatcher(activeHandles.pop());
  }
  vi.clearAllMocks();
  vi.mocked(ensureThreadSelectionFresh).mockResolvedValue({
    requestedHistory: false,
    syncedThreadState: false,
    forcedHistoryReload: false,
  });
});

describe('useThreadSelection', () => {
  it('resets stale preview state and exits early when selected thread is empty', async () => {
    const ctx = mountSelection({ selectedId: '' });

    await flush();

    expect(ctx.focusedDiffPath.value).toBe('');
    expect(ctx.focusedDiffLine.value).toBe(0);
    expect(ctx.fallbackDiffText.value).toBe('');
    expect(ctx.fallbackMediaPreview.value).toBeNull();
    expect(ctx.fallbackMarkdownPreview.value).toBeNull();
    expect(ensureThreadSelectionFresh).not.toHaveBeenCalled();
    expect(ctx.scheduleScrollToBottom).not.toHaveBeenCalled();
  });

  it('reacts to selectedThreadId changes and forces scroll after refreshing the current thread', async () => {
    const ctx = mountSelection({ selectedId: '' });

    await flush();
    ctx.selectedThreadId.value = 'thread-2';
    await flush();

    expect(ensureThreadSelectionFresh).toHaveBeenLastCalledWith(ctx.threadStore, 'thread-2', {
      reason: 'selection',
      previousThreadId: '',
    });
    expect(ctx.focusedDiffPath.value).toBe('');
    expect(ctx.focusedDiffLine.value).toBe(0);
    expect(ctx.scheduleScrollToBottom).toHaveBeenLastCalledWith(true);
  });

  it('forces scroll when freshness indicates history reload', async () => {
    vi.mocked(ensureThreadSelectionFresh).mockResolvedValueOnce({
      requestedHistory: true,
      syncedThreadState: false,
      forcedHistoryReload: false,
    });

    const ctx = mountSelection({ selectedId: 'thread-1' });
    await flush();

    expect(ensureThreadSelectionFresh).toHaveBeenCalledWith(ctx.threadStore, 'thread-1', {
      reason: 'selection',
      previousThreadId: '',
    });
    expect(ctx.scheduleScrollToBottom).toHaveBeenCalledWith(true);
  });

  it('keeps running after freshness errors and still forces scroll', async () => {
    vi.mocked(ensureThreadSelectionFresh).mockRejectedValueOnce(new Error('boom'));

    const ctx = mountSelection({ selectedId: 'thread-3' });
    await flush();

    expect(ensureThreadSelectionFresh).toHaveBeenCalledWith(ctx.threadStore, 'thread-3', {
      reason: 'selection',
      previousThreadId: '',
    });
    expect(ctx.scheduleScrollToBottom).toHaveBeenCalledWith(true);
  });

  it('clears stale selected thread when freshness reports a missing persisted session', async () => {
    vi.mocked(ensureThreadSelectionFresh).mockRejectedValueOnce(new Error('thread "agent-stale" not found: store: not found'));

    const ctx = mountSelection({ selectedId: 'agent-stale' });
    await flush();

    expect(ctx.selectedThreadId.value).toBe('');
    expect(ctx.scheduleScrollToBottom).not.toHaveBeenCalledWith(true);
  });

  it('does not call resetScrollState on initial mount (prevId is empty)', async () => {
    const resetScrollState = vi.fn();
    const ctx = mountSelection({ selectedId: 'thread-1', resetScrollState });
    await flush();

    // immediate 首次触发时 prevId 为空，不应 reset scroll
    expect(resetScrollState).not.toHaveBeenCalled();
  });

  it('calls resetScrollState only when thread actually changes to a different id', async () => {
    const resetScrollState = vi.fn();
    const ctx = mountSelection({ selectedId: 'thread-A', resetScrollState });
    await flush();

    // immediate 首次：prevId 为空 → 不 reset
    expect(resetScrollState).toHaveBeenCalledTimes(0);

    // 切到不同线程 → 应该 reset
    ctx.selectedThreadId.value = 'thread-B';
    await flush();
    expect(resetScrollState).toHaveBeenCalledTimes(1);

    // 再切 → 应该 reset
    ctx.selectedThreadId.value = 'thread-C';
    await flush();
    expect(resetScrollState).toHaveBeenCalledTimes(2);
  });

  it('scrolls immediately on existing thread switches before freshness resolves', async () => {
    const ctx = mountSelection({ selectedId: 'thread-A' });
    await flush();
    ctx.resetScrollState.mockClear();
    ctx.scheduleScrollToBottom.mockClear();

    let resolveFreshness;
    vi.mocked(ensureThreadSelectionFresh).mockImplementationOnce(() => new Promise((resolve) => {
      resolveFreshness = resolve;
    }));

    ctx.selectedThreadId.value = 'thread-B';
    await nextTick();
    await Promise.resolve();

    expect(ctx.resetScrollState).toHaveBeenCalledTimes(1);
    expect(ctx.scheduleScrollToBottom).toHaveBeenCalledTimes(1);
    expect(ctx.scheduleScrollToBottom).toHaveBeenLastCalledWith(true);

    resolveFreshness({ requestedHistory: true, syncedThreadState: true, forcedHistoryReload: false });
    await flush();

    expect(ctx.scheduleScrollToBottom).toHaveBeenCalledTimes(1);
    expect(ctx.scheduleScrollToBottom).toHaveBeenLastCalledWith(true);
  });

  it('does not let a stale selection refresh scroll the newly selected thread', async () => {
    const ctx = mountSelection({ selectedId: 'thread-A' });
    await flush();
    ctx.scheduleScrollToBottom.mockClear();

    let resolveThreadB;
    vi.mocked(ensureThreadSelectionFresh).mockImplementationOnce(() => new Promise((resolve) => {
      resolveThreadB = resolve;
    }));

    ctx.selectedThreadId.value = 'thread-B';
    await nextTick();
    await Promise.resolve();
    expect(ctx.scheduleScrollToBottom).toHaveBeenCalledTimes(1);

    ctx.selectedThreadId.value = 'thread-C';
    await flush();
    const callsAfterThreadC = ctx.scheduleScrollToBottom.mock.calls.length;

    resolveThreadB({ requestedHistory: true, syncedThreadState: true, forcedHistoryReload: false });
    await flush();

    expect(ctx.scheduleScrollToBottom).toHaveBeenCalledTimes(callsAfterThreadC);
  });
});
