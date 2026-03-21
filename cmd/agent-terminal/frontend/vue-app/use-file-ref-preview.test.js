// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';
import { ref } from '../lib/vue.esm-browser.prod.js';

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

import { useFileRefPreview } from './composables/useFileRefPreview.js';

const DIFF_TEXT = [
  'diff --git a/src/a.js b/src/a.js',
  '--- a/src/a.js',
  '+++ b/src/a.js',
  '@@ -1,1 +1,2 @@',
  ' line1',
  '+line2',
].join('\n');

const STALE_DIFF_TEXT = [
  'diff --git a/src/old.js b/src/old.js',
  '--- a/src/old.js',
  '+++ b/src/old.js',
  '@@ -1,1 +1,1 @@',
  '-old',
  '+stale',
].join('\n');

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function makePreviewEnv(overrides = {}) {
  const selectedThreadId = overrides.selectedThreadId || ref('thread-live');
  const activeThreadDiffText = ref(overrides.activeThreadDiffText ?? '');
  const focusedDiffPath = ref(overrides.focusedDiffPath ?? '');
  const focusedDiffLine = ref(overrides.focusedDiffLine ?? 0);
  const fallbackDiffText = ref(overrides.fallbackDiffText ?? '');
  const fallbackMediaPreview = ref(overrides.fallbackMediaPreview ?? null);
  const fallbackMarkdownPreview = ref(overrides.fallbackMarkdownPreview ?? null);
  const requestPathChoice = overrides.requestPathChoice || vi.fn(async () => '');
  const threadStore = overrides.threadStore || {
    state: { diffTextByThread: { 'thread-live': activeThreadDiffText.value } },
    syncThreadState: vi.fn(async () => ({})),
    syncThreadDiffState: vi.fn(async () => ({})),
    getThreadDiff: vi.fn((threadId) => threadStore.state.diffTextByThread[threadId] || ''),
  };
  const projectStore = overrides.projectStore || { state: { active: '.', projects: ['.'] } };
  const preview = useFileRefPreview(
    { threadStore, projectStore },
    {
      selectedThreadId,
      activeTimeline: ref([]),
      activeThreadDiffText,
      focusedDiffPath,
      focusedDiffLine,
      fallbackDiffText,
      fallbackMediaPreview,
      fallbackMarkdownPreview,
      requestPathChoice,
    },
  );
  return {
    ...preview,
    threadStore,
    requestPathChoice,
    selectedThreadId,
    activeThreadDiffText,
    focusedDiffPath,
    focusedDiffLine,
    fallbackDiffText,
    fallbackMediaPreview,
    fallbackMarkdownPreview,
  };
}

describe('useFileRefPreview', () => {
  it('restores diff through the current thread binding when active diff is empty', async () => {
    apiMock.callAPI.mockReset();
    const selectedThreadId = ref('thread-live');
    const activeThreadDiffText = ref('');
    const focusedDiffPath = ref('');
    const focusedDiffLine = ref(0);
    const fallbackDiffText = ref('');
    const fallbackMediaPreview = ref(null);
    const fallbackMarkdownPreview = ref(null);
    const threadStore = {
      state: { diffTextByThread: { 'thread-live': '' } },
      syncThreadState: vi.fn(async () => ({})),
      syncThreadDiffState: vi.fn(async (threadId) => {
        threadStore.state.diffTextByThread[threadId] = DIFF_TEXT;
      }),
      getThreadDiff: vi.fn((threadId) => threadStore.state.diffTextByThread[threadId] || ''),
    };
    const { onTimelineFileRefClick } = useFileRefPreview(
      { threadStore, projectStore: { state: { active: '.', projects: [] } } },
      {
        selectedThreadId,
        activeThreadDiffText,
        focusedDiffPath,
        focusedDiffLine,
        fallbackDiffText,
        fallbackMediaPreview,
        fallbackMarkdownPreview,
      },
    );

    await onTimelineFileRefClick({ path: 'src/a.js', line: 2, column: 1 });

    expect(threadStore.syncThreadState).toHaveBeenCalledWith('thread-live');
    expect(threadStore.syncThreadDiffState).toHaveBeenCalledWith('thread-live', { force: true });
    expect(threadStore.getThreadDiff).toHaveBeenCalledWith('thread-live');
    expect(focusedDiffPath.value).toBe('src/a.js');
    expect(focusedDiffLine.value).toBe(2);
    expect(fallbackDiffText.value).toBe('');
    expect(apiMock.callAPI).not.toHaveBeenCalled();
  });

  it('restores markdown diff through the current thread binding before falling back to markdown preview', async () => {
    apiMock.callAPI.mockReset();
    const markdownDiffText = [
      'diff --git a/docs/readme.md b/docs/readme.md',
      '--- a/docs/readme.md',
      '+++ b/docs/readme.md',
      '@@ -1,1 +1,2 @@',
      ' # Title',
      '+updated',
    ].join('\n');
    const selectedThreadId = ref('thread-live');
    const activeThreadDiffText = ref('');
    const focusedDiffPath = ref('');
    const focusedDiffLine = ref(0);
    const fallbackDiffText = ref('');
    const fallbackMediaPreview = ref(null);
    const fallbackMarkdownPreview = ref(null);
    const threadStore = {
      state: { diffTextByThread: { 'thread-live': '' } },
      syncThreadState: vi.fn(async () => ({})),
      syncThreadDiffState: vi.fn(async (threadId) => {
        threadStore.state.diffTextByThread[threadId] = markdownDiffText;
      }),
      getThreadDiff: vi.fn((threadId) => threadStore.state.diffTextByThread[threadId] || ''),
    };
    const { onTimelineFileRefClick } = useFileRefPreview(
      { threadStore, projectStore: { state: { active: '.', projects: [] } } },
      {
        selectedThreadId,
        activeThreadDiffText,
        focusedDiffPath,
        focusedDiffLine,
        fallbackDiffText,
        fallbackMediaPreview,
        fallbackMarkdownPreview,
      },
    );

    await onTimelineFileRefClick({ path: 'docs/readme.md', line: 4, column: 1 });

    expect(threadStore.syncThreadState).toHaveBeenCalledWith('thread-live');
    expect(threadStore.syncThreadDiffState).toHaveBeenCalledWith('thread-live', { force: true });
    expect(threadStore.getThreadDiff).toHaveBeenCalledWith('thread-live');
    expect(focusedDiffPath.value).toBe('docs/readme.md');
    expect(focusedDiffLine.value).toBe(4);
    expect(fallbackDiffText.value).toBe('');
    expect(fallbackMarkdownPreview.value).toBeNull();
    expect(apiMock.callAPI).not.toHaveBeenCalled();
  });

  it('retries current-thread restore when the in-memory diff misses the requested file', async () => {
    apiMock.callAPI.mockReset();
    const selectedThreadId = ref('thread-live');
    const activeThreadDiffText = ref(STALE_DIFF_TEXT);
    const focusedDiffPath = ref('');
    const focusedDiffLine = ref(0);
    const fallbackDiffText = ref('');
    const fallbackMediaPreview = ref(null);
    const fallbackMarkdownPreview = ref(null);
    const threadStore = {
      state: { diffTextByThread: { 'thread-live': STALE_DIFF_TEXT } },
      syncThreadState: vi.fn(async () => ({})),
      syncThreadDiffState: vi.fn(async (threadId) => {
        threadStore.state.diffTextByThread[threadId] = DIFF_TEXT;
      }),
      getThreadDiff: vi.fn((threadId) => threadStore.state.diffTextByThread[threadId] || ''),
    };
    const { onTimelineFileRefClick } = useFileRefPreview(
      { threadStore, projectStore: { state: { active: '.', projects: [] } } },
      {
        selectedThreadId,
        activeThreadDiffText,
        focusedDiffPath,
        focusedDiffLine,
        fallbackDiffText,
        fallbackMediaPreview,
        fallbackMarkdownPreview,
      },
    );

    await onTimelineFileRefClick({ path: 'src/a.js', line: 2, column: 1 });

    expect(threadStore.syncThreadState).toHaveBeenCalledWith('thread-live');
    expect(threadStore.syncThreadDiffState).toHaveBeenCalledWith('thread-live', { force: true });
    expect(threadStore.getThreadDiff).toHaveBeenCalledWith('thread-live');
    expect(focusedDiffPath.value).toBe('src/a.js');
    expect(focusedDiffLine.value).toBe(2);
    expect(fallbackDiffText.value).toBe('');
    expect(apiMock.callAPI).not.toHaveBeenCalled();
  });

  it('ignores restored results after the selected thread changes during async restore', async () => {
    apiMock.callAPI.mockReset();
    const selectedThreadId = ref('thread-live');
    const activeThreadDiffText = ref('');
    const focusedDiffPath = ref('');
    const focusedDiffLine = ref(0);
    const fallbackDiffText = ref('');
    const fallbackMediaPreview = ref(null);
    const fallbackMarkdownPreview = ref(null);
    const threadStore = {
      state: { diffTextByThread: { 'thread-live': '' } },
      syncThreadState: vi.fn(async () => ({})),
      syncThreadDiffState: vi.fn(async (threadId) => {
        selectedThreadId.value = 'thread-other';
        threadStore.state.diffTextByThread[threadId] = DIFF_TEXT;
      }),
      getThreadDiff: vi.fn((threadId) => threadStore.state.diffTextByThread[threadId] || ''),
    };
    const { onTimelineFileRefClick } = useFileRefPreview(
      { threadStore, projectStore: { state: { active: '.', projects: [] } } },
      {
        selectedThreadId,
        activeThreadDiffText,
        focusedDiffPath,
        focusedDiffLine,
        fallbackDiffText,
        fallbackMediaPreview,
        fallbackMarkdownPreview,
      },
    );

    await onTimelineFileRefClick({ path: 'src/a.js', line: 2, column: 1 });

    expect(threadStore.syncThreadState).toHaveBeenCalledWith('thread-live');
    expect(threadStore.syncThreadDiffState).toHaveBeenCalledWith('thread-live', { force: true });
    expect(focusedDiffPath.value).toBe('');
    expect(focusedDiffLine.value).toBe(0);
    expect(fallbackDiffText.value).toBe('');
    expect(apiMock.callAPI).not.toHaveBeenCalled();
  });

  it('keeps the newest same-thread file-ref focus when earlier restore resolves later', async () => {
    apiMock.callAPI.mockReset();
    const selectedThreadId = ref('thread-live');
    const activeThreadDiffText = ref('');
    const focusedDiffPath = ref('');
    const focusedDiffLine = ref(0);
    const fallbackDiffText = ref('');
    const fallbackMediaPreview = ref(null);
    const fallbackMarkdownPreview = ref(null);
    const first = deferred();
    const second = deferred();
    let callCount = 0;
    const diffByCall = {
      1: ['diff --git a/src/first.js b/src/first.js', '--- a/src/first.js', '+++ b/src/first.js', '@@ -1,1 +1,1 @@', '-old', '+first'].join('\n'),
      2: ['diff --git a/src/second.js b/src/second.js', '--- a/src/second.js', '+++ b/src/second.js', '@@ -1,1 +1,1 @@', '-old', '+second'].join('\n'),
    };
    const threadStore = {
      state: { diffTextByThread: { 'thread-live': '' } },
      syncThreadState: vi.fn(async () => ({})),
      syncThreadDiffState: vi.fn(async (threadId) => {
        callCount += 1;
        const currentCall = callCount;
        await (currentCall === 1 ? first.promise : second.promise);
        threadStore.state.diffTextByThread[threadId] = diffByCall[currentCall];
      }),
      getThreadDiff: vi.fn((threadId) => threadStore.state.diffTextByThread[threadId] || ''),
    };
    const { onTimelineFileRefClick } = useFileRefPreview(
      { threadStore, projectStore: { state: { active: '.', projects: [] } } },
      {
        selectedThreadId,
        activeThreadDiffText,
        focusedDiffPath,
        focusedDiffLine,
        fallbackDiffText,
        fallbackMediaPreview,
        fallbackMarkdownPreview,
      },
    );

    const firstClick = onTimelineFileRefClick({ path: 'src/first.js', line: 1, column: 1 });
    await Promise.resolve();
    await Promise.resolve();
    const secondClick = onTimelineFileRefClick({ path: 'src/second.js', line: 1, column: 1 });

    second.resolve();
    await secondClick;
    expect(focusedDiffPath.value).toBe('src/second.js');

    first.resolve();
    await firstClick;
    expect(focusedDiffPath.value).toBe('src/second.js');
    expect(focusedDiffLine.value).toBe(1);
    expect(apiMock.callAPI).not.toHaveBeenCalled();
  });

  it('opens the unique locate match before falling back to raw code open candidates', async () => {
    apiMock.callAPI.mockReset();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/code/locate') return { ok: true, paths: ['/repo/src/a.js'], truncated: false };
      if (method === 'ui/code/open') {
        return {
          ok: true,
          relative: 'src/a.js',
          startLine: 2,
          endLine: 2,
          snippet: [{ line: 2, text: 'const answer = 42;' }],
        };
      }
      return {};
    });
    const preview = makePreviewEnv();

    await preview.onTimelineFileRefClick({ path: 'src/a.js', line: 2, column: 1 });

    expect(preview.requestPathChoice).not.toHaveBeenCalled();
    expect(apiMock.callAPI).toHaveBeenNthCalledWith(1, 'ui/code/locate', {
      filePath: 'src/a.js',
      project: '.',
      projects: ['.'],
    });
    expect(apiMock.callAPI).toHaveBeenNthCalledWith(2, 'ui/code/open', expect.objectContaining({
      filePath: '/repo/src/a.js',
      line: 2,
      column: 1,
    }));
    expect(preview.focusedDiffPath.value).toBe('src/a.js');
    expect(preview.focusedDiffLine.value).toBe(2);
    expect(preview.fallbackDiffText.value).toContain('diff --git a/src/a.js b/src/a.js');
  });

  it('requests a path choice when locate returns multiple matches', async () => {
    apiMock.callAPI.mockReset();
    const requestPathChoice = vi.fn(async () => '/repo/lib/src/a.js');
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/code/locate') {
        return {
          ok: true,
          paths: ['/repo/src/a.js', '/repo/lib/src/a.js'],
          truncated: true,
        };
      }
      if (method === 'ui/code/open') {
        return {
          ok: true,
          relative: 'lib/src/a.js',
          startLine: 3,
          endLine: 3,
          snippet: [{ line: 3, text: 'export const picked = true;' }],
        };
      }
      return {};
    });
    const preview = makePreviewEnv({ requestPathChoice });

    await preview.onTimelineFileRefClick({ path: 'src/a.js', line: 3, column: 2 });

    expect(requestPathChoice).toHaveBeenCalledWith(
      ['/repo/src/a.js', '/repo/lib/src/a.js'],
      { title: '选择 src/a.js 的匹配路径', truncated: true },
    );
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/code/open', expect.objectContaining({
      filePath: '/repo/lib/src/a.js',
      line: 3,
      column: 2,
    }));
    expect(preview.focusedDiffPath.value).toBe('lib/src/a.js');
  });

  it('does not mutate preview state when the user cancels a multi-path choice', async () => {
    apiMock.callAPI.mockReset();
    const requestPathChoice = vi.fn(async () => '');
    apiMock.callAPI.mockResolvedValueOnce({
      ok: true,
      paths: ['/repo/src/a.js', '/repo/lib/src/a.js'],
      truncated: false,
    });
    const preview = makePreviewEnv({
      requestPathChoice,
      focusedDiffPath: 'keep.js',
      focusedDiffLine: 9,
      fallbackDiffText: 'keep diff',
    });

    await preview.onTimelineFileRefClick({ path: 'src/a.js', line: 3, column: 2 });

    expect(requestPathChoice).toHaveBeenCalledTimes(1);
    expect(apiMock.callAPI).toHaveBeenCalledTimes(1);
    expect(preview.focusedDiffPath.value).toBe('keep.js');
    expect(preview.focusedDiffLine.value).toBe(9);
    expect(preview.fallbackDiffText.value).toBe('keep diff');
  });

  it('falls back to raw candidates when locate returns empty paths with truncated flag', async () => {
    apiMock.callAPI.mockReset();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/code/locate') return { ok: true, paths: [], truncated: true };
      if (method === 'ui/code/open') {
        return {
          ok: true,
          relative: 'src/a.js',
          startLine: 2,
          endLine: 2,
          snippet: [{ line: 2, text: 'fallback open' }],
        };
      }
      return {};
    });
    const preview = makePreviewEnv();

    await preview.onTimelineFileRefClick({ path: 'src/a.js', line: 2, column: 1 });

    // requestPathChoice should NOT be called since locate returned no paths
    expect(preview.requestPathChoice).not.toHaveBeenCalled();
    // code/open should still be attempted with fallback candidates
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/code/open', expect.objectContaining({
      filePath: 'src/a.js',
    }));
  });

  it('falls back to raw candidates when locate returns null paths', async () => {
    apiMock.callAPI.mockReset();
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/code/locate') return { ok: true, paths: null, truncated: false };
      if (method === 'ui/code/open') {
        return {
          ok: true,
          relative: 'src/a.js',
          startLine: 1,
          endLine: 1,
          snippet: [{ line: 1, text: 'null paths fallback' }],
        };
      }
      return {};
    });
    const preview = makePreviewEnv();

    await preview.onTimelineFileRefClick({ path: 'src/a.js', line: 1, column: 1 });

    expect(preview.requestPathChoice).not.toHaveBeenCalled();
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/code/open', expect.objectContaining({
      filePath: 'src/a.js',
    }));
  });

  it('falls back to raw candidates when locate throws a network error', async () => {
    apiMock.callAPI.mockReset();
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/code/locate') throw new Error('network timeout');
      if (method === 'ui/code/open') {
        return {
          ok: true,
          relative: 'src/a.js',
          startLine: 1,
          endLine: 1,
          snippet: [{ line: 1, text: 'error fallback' }],
        };
      }
      return {};
    });
    const preview = makePreviewEnv();

    await preview.onTimelineFileRefClick({ path: 'src/a.js', line: 1, column: 1 });

    expect(preview.requestPathChoice).not.toHaveBeenCalled();
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/code/open', expect.objectContaining({
      filePath: 'src/a.js',
    }));
  });

  it('ignores a slower locate result once a newer file-ref request wins', async () => {
    apiMock.callAPI.mockReset();
    const firstLocate = deferred();
    apiMock.callAPI.mockImplementation((method, payload) => {
      if (method === 'ui/code/locate' && payload?.filePath === 'src/first.js') return firstLocate.promise;
      if (method === 'ui/code/locate' && payload?.filePath === 'src/second.js') {
        return Promise.resolve({ ok: true, paths: ['/repo/src/second.js'], truncated: false });
      }
      if (method === 'ui/code/open' && payload?.filePath === '/repo/src/second.js') {
        return Promise.resolve({
          ok: true,
          relative: 'src/second.js',
          startLine: 1,
          endLine: 1,
          snippet: [{ line: 1, text: 'const second = true;' }],
        });
      }
      if (method === 'ui/code/open' && payload?.filePath === '/repo/src/first.js') {
        return Promise.resolve({
          ok: true,
          relative: 'src/first.js',
          startLine: 1,
          endLine: 1,
          snippet: [{ line: 1, text: 'const first = true;' }],
        });
      }
      return Promise.resolve({});
    });
    const preview = makePreviewEnv();

    const firstClick = preview.onTimelineFileRefClick({ path: 'src/first.js', line: 1, column: 1 });
    await Promise.resolve();
    const secondClick = preview.onTimelineFileRefClick({ path: 'src/second.js', line: 1, column: 1 });
    await secondClick;
    expect(preview.focusedDiffPath.value).toBe('src/second.js');

    firstLocate.resolve({ ok: true, paths: ['/repo/src/first.js'], truncated: false });
    await firstClick;

    expect(preview.focusedDiffPath.value).toBe('src/second.js');
    expect(
      apiMock.callAPI.mock.calls.filter(([method, payload]) => method === 'ui/code/open' && payload?.filePath === '/repo/src/first.js'),
    ).toHaveLength(0);
  });
});
