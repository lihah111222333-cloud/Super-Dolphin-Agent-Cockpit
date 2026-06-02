// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';
import { ref } from '../lib/vue.esm-browser.prod.js';

import { useThreadCards } from './composables/useThreadCards.js';

function createThreadCards(overrides = {}) {
  const baseThreads = overrides.threads ?? [
    { id: 'thread-1', name: 'Thread One', state: 'running' },
    { id: 'thread-2', name: 'Thread Two', state: 'error' },
  ];
  const statusMap = overrides.statusMap ?? Object.fromEntries(baseThreads.map((thread) => [thread.id, thread.state || 'idle']));
  const headerMap = overrides.headerMap ?? {};
  const threadStore = {
    state: {
      pinnedThreadAtById: overrides.pinnedMap ?? {},
      archivedThreadAtById: overrides.archivedMap ?? {},
      agentRuntimeById: overrides.runtimeById ?? {},
      agentMetaById: overrides.metaById ?? {},
    },
    displayName: vi.fn((thread) => thread?.name || thread?.id || ''),
    getThreadStatus: vi.fn((threadId) => statusMap[threadId] || 'idle'),
  };
  const props = {
    threadStore,
  };
  const deps = {
    threads: ref(baseThreads),
    chatThreadOptions: ref(overrides.chatThreadOptions ?? baseThreads),
    selectedThreadId: ref(overrides.selectedThreadId ?? 'thread-1'),

    showArchivedThreadList: ref(overrides.showArchived ?? false),
    activeTimeline: ref(overrides.activeTimeline ?? []),
    isCmd: ref(overrides.isCmd ?? false),
    layoutMode: ref(overrides.layoutMode ?? 'mix'),
    timelinePreview: overrides.timelinePreview ?? vi.fn(() => []),
    diffPreview: overrides.diffPreview ?? vi.fn(() => ''),
    getThreadStatusHeader: overrides.getThreadStatusHeader ?? vi.fn((threadId) => headerMap[threadId] || ''),
    isThreadInterruptible: overrides.isThreadInterruptible ?? vi.fn(() => false),
    buildVisibleChatThreadCards: overrides.buildVisibleChatThreadCards ?? vi.fn(() => ({
      cards: baseThreads.map((thread) => ({ id: thread.id, name: thread.name })),
      activeCount: baseThreads.length,
      archivedCount: 0,
    })),
  };
  const vm = useThreadCards(props, deps);
  return {
    props,
    deps,
    threadStore,
    ...vm,
  };
}

describe('useThreadCards', () => {
  it('derives stats from thread ids and normalized statuses', () => {
    const vm = createThreadCards({
      statusMap: {
        'thread-1': 'running',
        'thread-2': 'error',
      },
    });

    expect(vm.stats.value).toEqual({ total: 2, running: 1, thinking: 0, editing: 0, error: 1 });
  });

  it('shows overview only in chat mode with mix layout', () => {
    const vm = createThreadCards({ isCmd: false, layoutMode: 'mix' });
    const cmdVm = createThreadCards({ isCmd: true, layoutMode: 'mix' });

    expect(vm.showOverview.value).toBe(true);
    expect(cmdVm.showOverview.value).toBe(false);
  });

  it('returns the latest active pinned plan and hides it after dismissal', () => {
    const vm = createThreadCards({
      activeTimeline: [
        { id: 'plan-1', kind: 'plan', text: '先分析', done: false },
        { id: 'plan-2', kind: 'plan', text: '再执行', done: true },
      ],
    });

    expect(vm.activePinnedPlan.value).toEqual({
      id: 'plan-2',
      key: 'id:plan-2',
      threadId: 'thread-1',
      done: true,
      statusText: '完成',
      text: '再执行',
    });

    vm.dismissPinnedPlan();
    expect(vm.activePinnedPlan.value).toBeNull();

    // Distinct new plans should be shown again
    vm.deps.activeTimeline.value = [
      { id: 'plan-rebuilt-1', kind: 'plan', text: '重建后的新计划', done: false },
    ];
    expect(vm.activePinnedPlan.value).not.toBeNull();
  });

  it('builds cmd cards with preview, diff, provider and cwd mismatch state', () => {
    const timelinePreview = vi.fn(() => ['preview-item']);
    const diffPreview = vi.fn(() => 'diff-preview');
    const vm = createThreadCards({
      isCmd: true,
      selectedThreadId: 'thread-1',
      layoutMode: 'mix',
      runtimeById: {
        'thread-1': {
          cwdMismatch: true,
          cwdMismatchReason: 'selected cwd differs',
          provider: 'claude',
        },
      },
      headerMap: {
        'thread-1': '处理中',
        'thread-2': '等待中',
      },
      isThreadInterruptible: vi.fn((threadId) => threadId === 'thread-1'),
      timelinePreview,
      diffPreview,
    });

    expect(vm.cmdCards.value[0]).toEqual(expect.objectContaining({
      id: 'thread-1',
      selected: true,
      preview: ['preview-item'],
      diff: 'diff-preview',
      cwdMismatch: true,
      cwdMismatchReason: 'selected cwd differs',
      provider: 'claude',
      interruptible: true,
    }));
    expect(vm.cmdCards.value[1]).toEqual(expect.objectContaining({
      id: 'thread-2',
      selected: false,
      preview: [],
      diff: '',
      interruptible: false,
    }));
  });

  it('reports there is no active thread when selection is empty', () => {
    const vm = createThreadCards({ selectedThreadId: '' });
    expect(vm.noActiveThread.value).toBe(true);
  });

  it('re-evaluates cmd card preview when activeTimeline changes (streaming regression)', () => {
    let previewContent = [{ key: 'i-0', text: '助手: 初始内容' }];
    const timelinePreview = vi.fn(() => previewContent);
    const vm = createThreadCards({
      isCmd: true,
      selectedThreadId: 'thread-1',
      layoutMode: 'mix',
      timelinePreview,
      diffPreview: vi.fn(() => ''),
    });

    expect(vm.cmdCards.value[0].preview).toEqual([{ key: 'i-0', text: '助手: 初始内容' }]);
    const initialCallCount = timelinePreview.mock.calls.length;

    previewContent = [{ key: 'i-0', text: '助手: 流式更新后的内容' }];
    vm.deps.activeTimeline.value = [
      { id: '1', kind: 'assistant', text: '流式更新后的内容' },
    ];

    expect(vm.cmdCards.value[0].preview).toEqual([{ key: 'i-0', text: '助手: 流式更新后的内容' }]);
    expect(timelinePreview.mock.calls.length).toBeGreaterThan(initialCallCount);
  });

  it('maps tool, file, and approval timeline items into process activity entries', () => {
    const vm = createThreadCards({
      activeTimeline: [
        { id: 'tool-1', kind: 'tool', tool: 'open_file', preview: 'src/main.js', status: 'completed', success: true, ts: '2026-03-09T10:00:00Z' },
        { id: 'file-1', kind: 'file', file: 'src/main.js', status: 'saved', success: true, ts: '2026-03-09T10:00:01Z' },
        { id: 'file-2', kind: 'file', file: 'src/app.js', status: 'running', success: true, ts: '2026-03-09T10:00:02Z' },
        { id: 'approval-1', kind: 'approval', tool: 'file_edit', requestId: 7, status: 'pending', ts: '2026-03-09T10:00:03Z' },
      ],
    });

    expect(vm.activeProcessActivity.value).toEqual([
      expect.objectContaining({ kind: 'approval', message: '审批确认 · file_edit', status: 'active' }),
      expect.objectContaining({ kind: 'file', message: '修改中 · src/app.js', status: 'active' }),
      expect.objectContaining({ kind: 'file', message: '已保存 · src/main.js', status: 'done' }),
      expect.objectContaining({ kind: 'tool', message: 'open_file · 已完成', status: 'done' }),
    ]);
  });

  it('shortens tool names and summarizes known and unknown tool results', () => {
    const vm = createThreadCards({
      activeTimeline: [
        { id: 'tool-edit', kind: 'tool', tool: 'mcp__lsp__edit', preview: '{"success":true,"action":"replace_range"}', status: 'completed', success: true, ts: '2026-03-09T10:00:00Z' },
        { id: 'tool-grep', kind: 'tool', tool: 'mcp__lsp__grep', preview: '{"files":{},"total":0,"showing":0}', status: 'completed', success: true, ts: '2026-03-09T10:00:01Z' },
        { id: 'tool-run', kind: 'tool', tool: 'functions.exec_command', preview: '{"success":false,"output":"cat: missing file"}', status: 'completed', success: false, ts: '2026-03-09T10:00:02Z' },
        { id: 'tool-workspace', kind: 'tool', tool: 'mcp__orch__workspace_merge_run', status: 'completed', success: true, ts: '2026-03-09T10:00:03Z' },
        { id: 'tool-unknown', kind: 'tool', tool: 'future.vendor/scan', status: 'completed', success: true, ts: '2026-03-09T10:00:04Z' },
      ],
    });

    expect(vm.activeProcessActivity.value).toEqual([
      expect.objectContaining({ kind: 'tool', message: 'future_vendor_scan · 已完成', status: 'done' }),
      expect.objectContaining({ kind: 'tool', message: 'workspace_merge_run · 已合并工作区', status: 'done' }),
      expect.objectContaining({ kind: 'tool', message: 'exec_command · 命令执行失败：cat: missing file', status: 'failed' }),
      expect.objectContaining({ kind: 'tool', message: 'grep · 搜索无结果', status: 'done' }),
      expect.objectContaining({ kind: 'tool', message: 'edit · 已替换文件内容', status: 'done' }),
    ]);
  });

  it('uses unified tool name and detail fallbacks for process activity summaries', () => {
    const vm = createThreadCards({
      activeTimeline: [
        {
          id: 'tool-format-result',
          kind: 'tool',
          toolName: 'mcp__lsp__format_preview',
          status: 'completed',
          success: true,
          result: {
            structuredContent: {
              success: true,
              action: 'format_preview',
              text_edit_count: 3,
            },
          },
          ts: '2026-03-09T10:00:00Z',
        },
        {
          id: 'tool-grep-args',
          kind: 'tool',
          tool: 'mcp__lsp__grep',
          status: 'completed',
          success: true,
          argumentsPreview: '{"total":4}',
          ts: '2026-03-09T10:00:01Z',
        },
        {
          id: 'tool-workspace-name',
          kind: 'tool',
          name: 'mcp__orch__workspace_create_run',
          status: 'completed',
          success: true,
          ts: '2026-03-09T10:00:02Z',
        },
        {
          id: 'tool-running-args',
          kind: 'tool',
          tool: 'mcp__lsp__grep',
          status: 'running',
          success: true,
          argumentsPreview: '{"pattern":"TODO"}',
          ts: '2026-03-09T10:00:03Z',
        },
      ],
    });

    expect(vm.activeProcessActivity.value).toEqual([
      expect.objectContaining({ kind: 'tool', message: 'grep · 执行中 · {"pattern":"TODO"}', status: 'active' }),
      expect.objectContaining({ kind: 'tool', message: 'workspace_create_run · 已创建工作区', status: 'done' }),
      expect.objectContaining({ kind: 'tool', message: 'grep · 搜索到 4 处', status: 'done' }),
      expect.objectContaining({ kind: 'tool', message: 'format_preview · 预览到 3 处格式化改动', status: 'done' }),
    ]);
  });

  it('marks failed command, tool, and file timeline items as failed from backend payloads', () => {
    const vm = createThreadCards({
      activeTimeline: [
        { id: 'tool-1', kind: 'tool', tool: 'bash', status: 'completed', success: false, error: 'boom', ts: '2026-03-09T10:00:00Z' },
        { id: 'file-1', kind: 'file', file: 'src/main.js', status: 'completed', success: false, error: 'write failed', ts: '2026-03-09T10:00:01Z' },
        { id: 'cmd-1', kind: 'command', command: 'go test ./...', status: 'completed', exitCode: 1, output: 'FAIL', ts: '2026-03-09T10:00:02Z' },
      ],
    });

    expect(vm.activeProcessActivity.value).toEqual([
      expect.objectContaining({ kind: 'command', status: 'failed', exitCode: 1 }),
      expect.objectContaining({ kind: 'file', status: 'failed', message: '修改失败 · src/main.js' }),
      expect.objectContaining({ kind: 'tool', status: 'failed', message: 'bash · 执行失败：boom' }),
    ]);
  });

  it('shows parsed failed tool preview payloads as failed process activity', () => {
    const vm = createThreadCards({
      activeTimeline: [
        {
          id: 'tool-grep',
          kind: 'tool',
          tool: 'mcp__lsp__grep',
          status: 'completed',
          success: true,
          preview: '{"success":false,"error":"search root is unavailable","total":0}',
          ts: '2026-03-09T10:00:00Z',
        },
      ],
    });

    expect(vm.activeProcessActivity.value).toEqual([
      expect.objectContaining({
        kind: 'tool',
        message: 'grep · 搜索代码失败：search root is unavailable',
        status: 'failed',
      }),
    ]);
  });
});
