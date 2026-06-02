// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const composerStoreMock = vi.hoisted(() => ({
  state: {
    text: '',
    attachments: [],
  },
  attachByPaths: vi.fn(() => 0),
  clearComposer: vi.fn(),
}));

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    ...actual,
    onMounted: () => {},
    onBeforeUnmount: () => {},
  };
});

import { reactive, ref } from '../lib/vue.esm-browser.prod.js';
import { displayToolName, summarizeToolActivity, toolActivityDetail } from './utils/format-utils.js';

vi.mock('./stores/composer.js', () => ({
  useComposerStore: () => composerStoreMock,
}));

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(async () => ({})),
  copyTextToClipboard: vi.fn(async () => true),
  onFilesDropped: vi.fn(() => () => {}),
  resolveThreadIdentity: vi.fn(async () => ({})),
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./composables/useAutoScroll.js', () => ({
  useAutoScroll: () => ({
    scheduleScrollToBottom: vi.fn(),
  }),
}));

import { UnifiedChatPage } from './pages/UnifiedChatPage.js';

beforeEach(() => {
  composerStoreMock.state.text = '';
  composerStoreMock.state.attachments = [];
  composerStoreMock.attachByPaths.mockReset();
  composerStoreMock.attachByPaths.mockImplementation(() => 0);
  composerStoreMock.clearComposer.mockReset();
  composerStoreMock.clearComposer.mockImplementation(() => {
    composerStoreMock.state.text = '';
    composerStoreMock.state.attachments = [];
  });

  globalThis.window = {
    ...(globalThis.window || {}),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    setTimeout: vi.fn(() => 1),
    clearTimeout: vi.fn(),
    setInterval: vi.fn(() => 1),
    clearInterval: vi.fn(),
  };
  globalThis.document = {
    ...(globalThis.document || {}),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    querySelector: vi.fn(() => null),
    activeElement: null,
  };
});

function makeProjectStore() {
  return {
    state: reactive({ active: '.', showModal: false, projects: ['.'] }),
    projectOptions: { value: [] },
    setActive: () => {},
  };
}

function makeThreadStore(options = {}) {
  const currentThreadId = ref('thread-active');
  const timelinesByThread = reactive({ 'thread-active': options.timeline || [] });
  const statusDetail = (options.statusDetail || '').toString();
  return {
    state: reactive({

      pinnedThreadAtById: {},
      archivedThreadAtById: {},
      agentRuntimeById: {},
      skillRevision: 0,
    }),
    getLayout: () => 'focus',
    setLayout: () => {},
    getCmdCardCols: () => 3,
    setCmdCardCols: () => {},
    getSplitRatio: () => 60,
    setSplitRatio: () => {},
    getThreadRailWidth: () => 232,
    setThreadRailWidth: () => {},
    getCurrentThreadId: () => currentThreadId.value,
    saveActiveThread: (value) => { currentThreadId.value = value || ''; },
    saveActiveCmdThread: (value) => { currentThreadId.value = value || ''; },
    getThreadsByMode: () => [{ id: 'thread-active', name: 'Active' }],
    displayName: (thread) => thread.name,
    getThreadStatus: () => options.status || 'running',
    getThreadStatusHeader: () => options.statusHeader || '处理中',
    getThreadInterruptible: () => options.interruptible !== false,
    getThreadPinnedAt: () => 0,
    getThreadArchivedAt: () => 0,
    getThreadTimeline: (threadId) => timelinesByThread[threadId] || [],
    loadMessages: async () => ({}),
    stopThread: vi.fn(async () => ({ confirmed: true, settled: true, mode: 'interrupt_confirmed' })),
    getThreadDiff: () => '',
    getThreadStatusDetails: () => statusDetail,
    getThreadTokenUsage: () => options.tokenUsage || null,
    getThreadCompacting: () => false,
    getThreadCompactResult: () => null,
    getThreadCompactSuccessCount: () => 0,
    getThreadActivityStats: () => ({}),
    getThreadAlerts: () => [],
    startThread: vi.fn(async () => 'thread-active'),
    sendMessage: vi.fn(async () => ({})),
  };
}

describe('tool activity formatting', () => {
  it('shortens known and unknown tool names without dropping new tools', () => {
    expect(displayToolName('mcp__lsp__grep')).toBe('grep');
    expect(displayToolName('mcp__lsp__edit')).toBe('edit');
    expect(displayToolName('mcp__lsp__lsp_edit')).toBe('edit');
    expect(displayToolName('mcp__lsp__lsp_format_preview')).toBe('format_preview');
    expect(displayToolName('mcp__orch__workspace_merge_run')).toBe('workspace_merge_run');
    expect(displayToolName('mcp__lsp-tools__lsp_grep')).toBe('grep');
    expect(displayToolName('MCP__LSP__LSP_GREP')).toBe('grep');
    expect(displayToolName('functions.exec_command')).toBe('exec_command');
    expect(displayToolName('future.vendor/scan')).toBe('future_vendor_scan');
  });

  it('summarizes known tools and keeps a generic fallback for unknown tools', () => {
    expect(summarizeToolActivity('mcp__lsp__grep', { preview: '{"total":3}', success: true })).toEqual({ name: 'grep', summary: '搜索到 3 处', status: 'done' });
    expect(summarizeToolActivity('mcp__lsp__lsp_grep', { preview: '{"total":3}', success: true })).toEqual({ name: 'grep', summary: '搜索到 3 处', status: 'done' });
    expect(summarizeToolActivity('functions.exec_command', { preview: '{"output":"cat: missing file"}', success: false })).toEqual({ name: 'exec_command', summary: '命令执行失败：cat: missing file', status: 'failed' });
    expect(summarizeToolActivity('future.vendor/scan', { status: 'completed', success: true })).toEqual({ name: 'future_vendor_scan', summary: '已完成', status: 'done' });
  });

  it('uses unified detail fallback fields in precedence order', () => {
    expect(summarizeToolActivity('mcp__lsp__grep', {
      status: 'completed',
      success: true,
      output: '{"total":5}',
      result: '{"total":9}',
      argumentsPreview: '{"total":11}',
    })).toEqual({ name: 'grep', summary: '搜索到 5 处', status: 'done' });

    expect(summarizeToolActivity('mcp__lsp__format_preview', {
      status: 'completed',
      success: true,
      result: {
        structuredContent: {
          success: true,
          action: 'format_preview',
          text_edit_count: 3,
        },
      },
    })).toEqual({ name: 'format_preview', summary: '预览到 3 处格式化改动', status: 'done' });

    expect(summarizeToolActivity('mcp__lsp__grep', {
      status: 'completed',
      success: true,
      arguments_preview: '{"total":4}',
    })).toEqual({ name: 'grep', summary: '搜索到 4 处', status: 'done' });

    expect(summarizeToolActivity('mcp__lsp__completion', {
      status: 'completed',
      success: true,
      result: [{ label: 'alpha' }],
    })).toEqual({ name: 'completion', summary: '1 条补全建议', status: 'done' });

    expect(summarizeToolActivity('mcp__lsp__structure', {
      status: 'completed',
      success: true,
      result: { structuredContent: [{ name: 'main' }, { name: 'helper' }] },
    })).toEqual({ name: 'structure', summary: '获取到 2 个符号', status: 'done' });
  });

  it('marks parsed tool payload failures as failed even when transport completed', () => {
    expect(summarizeToolActivity('mcp__lsp__grep', {
      status: 'completed',
      success: true,
      preview: '{"success":false,"error":"ripgrep exited 2","total":0}',
    })).toEqual({ name: 'grep', summary: '搜索代码失败：ripgrep exited 2', status: 'failed' });

    expect(summarizeToolActivity('mcp__lsp__xref', {
      status: 'completed',
      success: true,
      preview: '{"isError":true,"message":"lsp peer unavailable","count":0}',
    })).toEqual({ name: 'xref', summary: '查找引用失败：lsp peer unavailable', status: 'failed' });

    expect(summarizeToolActivity('mcp__lsp__format_preview', {
      status: 'completed',
      success: true,
      preview: '{"success":true,"action":"format_preview","text_edit_count":2,"applied":false,"persisted":false}',
    })).toEqual({ name: 'format_preview', summary: '预览到 2 处格式化改动', status: 'done' });

    expect(summarizeToolActivity('edit', {
      status: 'completed',
      success: true,
      preview: '{"success":true,"action":"format","text_edit_count":2,"applied":true,"persisted":true}',
    })).toEqual({ name: 'edit', summary: '已应用格式化（2 处改动）', status: 'done' });

    expect(summarizeToolActivity('mcp__lsp__format_preview', {
      status: 'completed',
      success: true,
      preview: '{"success":true,"structuredContent":{"success":true,"action":"format_preview","text_edit_count":2,"applied":false,"persisted":false}}',
    })).toEqual({ name: 'format_preview', summary: '预览到 2 处格式化改动', status: 'done' });

    expect(summarizeToolActivity('mcp__lsp__edit', {
      status: 'completed',
      success: true,
      preview: '{"success":true,"structuredContent":{"success":true,"action":"format","text_edit_count":2,"applied":true,"persisted":true}}',
    })).toEqual({ name: 'edit', summary: '已应用格式化（2 处改动）', status: 'done' });

    expect(summarizeToolActivity('future.vendor/scan', {
      status: 'completed',
      success: true,
      preview: '{"error_code":"workspace_missing","error":"workspace root is required"}',
    })).toEqual({ name: 'future_vendor_scan', summary: '执行失败：workspace root is required', status: 'failed' });
  });

  it('treats item.error as failure and shows it when preview is also present', () => {
    const item = {
      status: 'completed',
      success: true,
      preview: '{"total":3}',
      error: 'ripgrep exited 2',
    };

    expect(toolActivityDetail(item)).toBe('ripgrep exited 2');
    expect(summarizeToolActivity('mcp__lsp__grep', item)).toEqual({
      name: 'grep',
      summary: '搜索代码失败：ripgrep exited 2',
      status: 'failed',
    });
  });

  it('treats item.error as failure and shows it when result or argumentsPreview are present', () => {
    expect(summarizeToolActivity('mcp__lsp__format_preview', {
      status: 'completed',
      success: true,
      result: {
        structuredContent: {
          success: true,
          action: 'format_preview',
          text_edit_count: 2,
        },
      },
      error: 'format crashed',
    })).toEqual({
      name: 'format_preview',
      summary: '预览格式化失败：format crashed',
      status: 'failed',
    });

    expect(summarizeToolActivity('future.vendor/scan', {
      status: 'completed',
      success: true,
      argumentsPreview: '{"path":"src"}',
      error: 'workspace missing',
    })).toEqual({
      name: 'future_vendor_scan',
      summary: '执行失败：workspace missing',
      status: 'failed',
    });
  });

  it('does not treat running tool arguments as failed result payloads', () => {
    expect(summarizeToolActivity('mcp__lsp__grep', {
      status: 'running',
      success: true,
      argumentsPreview: '{"pattern":"success:false","error":"literal search text"}',
    })).toEqual({ name: 'grep', summary: '执行中', status: 'active' });

    expect(summarizeToolActivity('mcp__lsp__grep', {
      status: 'running',
      success: true,
      preview: '{"success":false,"isError":true,"error_code":"literal"}',
    })).toEqual({ name: 'grep', summary: '执行中', status: 'active' });
  });
});

describe('format split barrier via UnifiedChatPage public outputs', () => {
  it('formats activity timeline items and truncates long command output', () => {
    const threadStore = makeThreadStore({
      timeline: [
        { id: 'thinking-1', kind: 'thinking', ts: '2026-03-09T10:00:00Z', done: false },
        { id: 'cmd-1', kind: 'command', ts: '2026-03-09T10:01:00Z', status: 'failed', command: 'npm test', output: 'x'.repeat(500), exitCode: 3 },
      ],
    });

    const vm = UnifiedChatPage.setup({ threadStore, projectStore: makeProjectStore(), mode: 'chat' });

    expect(vm.activeProcessActivity.value[0].message).toBe('$ npm test');
    expect(vm.activeProcessActivity.value[0].output).toContain('...[truncated]');
    expect(vm.activeProcessActivity.value[0].time).toMatch(/^\d{2}:\d{2}/);
    expect(vm.activeProcessActivity.value[1].message).toBe('思考中');
  });

  it('formats token inline text and tooltip from token usage', () => {
    const threadStore = makeThreadStore({ tokenUsage: { usedTokens: 1530, contextWindowTokens: 8192, usedPercent: 18.7 } });
    const vm = UnifiedChatPage.setup({ threadStore, projectStore: makeProjectStore(), mode: 'chat' });

    expect(vm.activeTokenInline.value).toBe('19% · 1.5k / 8.2k');
    expect(vm.activeTokenTooltip.value).toBe('Context window:\n19% used (81% left)\n1.5k / 8.2k tokens used');
  });

  it('formats long elapsed status text through activeStatusMeta', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-03-09T00:00:00Z'));
    let tick = () => {};
    globalThis.window.setInterval = vi.fn((cb) => { tick = cb; return 1; });
    try {
      const threadStore = makeThreadStore({ status: 'running', statusHeader: '处理中' });
      const vm = UnifiedChatPage.setup({ threadStore, projectStore: makeProjectStore(), mode: 'chat' });
      vi.setSystemTime(new Date('2026-03-09T01:01:01Z'));
      tick();
      expect(vm.activeStatusMeta.value).toContain('1h 01m 01s');
    } finally {
      vi.useRealTimers();
    }
  });
});
