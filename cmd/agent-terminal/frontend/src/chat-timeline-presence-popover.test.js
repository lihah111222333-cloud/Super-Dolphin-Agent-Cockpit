// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick, reactive } from '../lib/vue.esm-browser.prod.js';

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(async () => ({ ok: true })),
}));

vi.mock('./utils/assistant-markdown.js', () => ({
  renderAssistantMarkdown: vi.fn((text) => '<p>' + text + '</p>'),
  injectSentenceBreaks: vi.fn((text) => text),
}));

import { ChatTimeline } from './components/ChatTimeline.js';

function createProps(overrides = {}) {
  return reactive({
    items: overrides.items ?? [],
    activeStatus: overrides.activeStatus ?? 'idle',
    activeStatusText: overrides.activeStatusText ?? '',
    activeStatusMeta: overrides.activeStatusMeta ?? '',
    pinnedPlanVisible: overrides.pinnedPlanVisible ?? false,
    pinnedPlanItemId: overrides.pinnedPlanItemId ?? null,
    resolveThreadDisplayName: overrides.resolveThreadDisplayName ?? null,
    presenceTarget: overrides.presenceTarget ?? null,
  });
}

function setupTimeline(overrides = {}, emit = vi.fn()) {
  const props = createProps(overrides);
  const vm = ChatTimeline.setup(props, { emit });
  return { props, vm, emit };
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

beforeEach(() => {
  vi.useRealTimers();
});

describe('ChatTimeline presence popover guards', () => {
  it('keeps presence target fallback semantics stable for string, object and null inputs', () => {
    const { vm: nullVm } = setupTimeline({ activeStatusText: '分析中', presenceTarget: null });
    expect(nullVm.resolvedPresenceTarget.value).toBe('body');
    expect(nullVm.hasPresenceTarget.value).toBe(false);

    const { vm: stringVm } = setupTimeline({
      activeStatusText: '分析中',
      presenceTarget: '#presence-anchor',
      items: [{ id: 'thinking-1', kind: 'thinking', text: '检查错误', done: false }],
    });
    expect(stringVm.resolvedPresenceTarget.value).toBe('#presence-anchor');
    expect(stringVm.hasPresenceTarget.value).toBe(true);

    const { vm: objectVm } = setupTimeline({
      activeStatusText: '分析中',
      presenceTarget: { value: '' },
      items: [{ id: 'thinking-1', kind: 'thinking', text: '检查错误', done: false }],
    });
    expect(objectVm.resolvedPresenceTarget.value).toBe('body');
    expect(objectVm.hasPresenceTarget.value).toBe(false);
  });

  it('auto closes the popover when summaries disappear', async () => {
    const { props, vm } = setupTimeline({
      activeStatus: 'thinking',
      activeStatusText: '分析中',
      items: [
        { id: 'assistant-1', kind: 'assistant', text: '收到', done: true },
        { id: 'thinking-1', kind: 'thinking', text: '检查错误', done: false },
      ],
      presenceTarget: '#anchor',
    });

    vm.openPresencePopover();
    expect(vm.showPresencePopover.value).toBe(true);

    props.items = [{ id: 'assistant-1', kind: 'assistant', text: '收到', done: true }];
    await nextTick();

    expect(vm.showThinkingPopover.value).toBe(false);
    expect(vm.showPresencePopover.value).toBe(false);
  });

  it('keeps ticker and title semantics stable for tool and file summary branches', () => {
    const { vm: toolVm } = setupTimeline({
      activeStatus: 'thinking',
      activeStatusText: '分析中',
      activeStatusMeta: 'gpt-5',
      items: [
        { id: 'assistant-1', kind: 'assistant', text: '收到', done: true },
        { id: 'thinking-1', kind: 'thinking', text: '检查错误', done: false, ts: '2026-03-14T10:00:00Z' },
        { id: 'tool-1', kind: 'tool', tool: 'open_file', preview: 'ChatTimeline.js', elapsedMs: 18, ts: '2026-03-14T10:00:01Z' },
      ],
    });

    expect(toolVm.showAgentPresence.value).toBe(true);
    expect(toolVm.showToolTicker.value).toBe(true);
    expect(toolVm.collapsedToolTickerText.value).toContain('open_file');
    expect(toolVm.presencePopoverTitle.value).toContain('已收起 1 个工具调用');
    expect(toolVm.sharedStatusMeta.value).toBe('gpt-5');

    const { vm: fileVm } = setupTimeline({
      activeStatus: 'responding',
      activeStatusText: '同步中',
      items: [
        { id: 'assistant-1', kind: 'assistant', text: '收到', done: true },
        { id: 'file-1', kind: 'file', status: 'saved', file: '', ts: '2026-03-14T10:00:03Z' },
      ],
    });

    expect(fileVm.showThinkingPopover.value).toBe(true);
    expect(fileVm.showToolTicker.value).toBe(false);
    expect(fileVm.presencePopoverTitle.value).toBe('悬浮查看思考过程与工具摘要');
  });

  it('falls back to an immediate close when setTimeout is unavailable and hides the row for no-thread status', () => {
    const { vm: hiddenVm } = setupTimeline({
      activeStatusText: '未选择会话',
      items: [],
      presenceTarget: { value: '#presence-anchor' },
    });
    expect(hiddenVm.showAgentPresence.value).toBe(false);

    const { vm } = setupTimeline({
      activeStatus: 'thinking',
      activeStatusText: '分析中',
      items: [{ id: 'thinking-1', kind: 'thinking', text: '检查错误', done: false }],
      presenceTarget: { value: '#presence-anchor' },
    });

    vm.openPresencePopover();
    expect(vm.showPresencePopover.value).toBe(true);
    vi.stubGlobal('setTimeout', undefined);
    vm.schedulePresencePopoverClose();
    expect(vm.showPresencePopover.value).toBe(false);
    expect(vm.resolvedPresenceTarget.value).toBe('#presence-anchor');
    expect(vm.hasPresenceTarget.value).toBe(true);
  });

  it('normalizes MCP tool names and uses argumentsPreview in tool summaries and ticker', () => {
    const { vm } = setupTimeline({
      activeStatus: 'thinking',
      activeStatusText: '分析中',
      items: [
        { id: 'assistant-1', kind: 'assistant', text: '收到', done: true },
        {
          id: 'tool-grep-running',
          kind: 'tool',
          tool: 'mcp__lsp__lsp_grep',
          status: 'running',
          success: true,
          argumentsPreview: '{"pattern":"TODO"}',
          elapsedMs: 12,
          ts: '2026-03-14T10:00:04Z',
        },
      ],
    });

    expect(vm.thinkingToolSummaries.value).toHaveLength(1);
    const summaryText = vm.thinkingToolSummaries.value[0].text;
    expect(summaryText).toContain('grep');
    expect(summaryText).toContain('执行中');
    expect(summaryText).toContain('"pattern":"TODO"');
    expect(summaryText).not.toContain('mcp__lsp__');

    expect(vm.collapsedToolTickerText.value).toContain('grep');
    expect(vm.collapsedToolTickerText.value).toContain('"pattern":"TODO"');
    expect(vm.collapsedToolTickerText.value).not.toContain('mcp__lsp__');
  });

  it('prefixes the collapsed tool ticker for structured payload failures', () => {
    const { vm } = setupTimeline({
      activeStatus: 'thinking',
      activeStatusText: '分析中',
      items: [
        { id: 'assistant-1', kind: 'assistant', text: '收到', done: true },
        {
          id: 'tool-grep-failed-payload',
          kind: 'tool',
          tool: 'mcp__lsp__grep',
          status: 'completed',
          success: true,
          preview: '{"success":false,"error":"search root is unavailable","total":0}',
          elapsedMs: 12,
          ts: '2026-03-14T10:00:05Z',
        },
      ],
    });

    expect(vm.collapsedToolTickerText.value).toContain('失败 · grep');
    expect(vm.collapsedToolTickerText.value).toContain('search root is unavailable');
  });
});
