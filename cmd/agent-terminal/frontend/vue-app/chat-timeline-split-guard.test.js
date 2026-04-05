// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { reactive } from '../lib/vue.esm-browser.prod.js';

const markdownMock = vi.hoisted(() => ({
  render: vi.fn((text) => `<p>${text}</p>`),
}));

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(async () => ({ ok: true })),
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./utils/assistant-markdown.js', () => ({
  renderAssistantMarkdown: markdownMock.render,
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

beforeEach(() => {
  apiMock.callAPI.mockReset();
  apiMock.callAPI.mockResolvedValue({ ok: true });
  markdownMock.render.mockClear();
  markdownMock.render.mockImplementation((text) => `<p>${text}</p>`);
  vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('ChatTimeline split guard coverage', () => {
  it('locks the setup return contract before composable extraction', () => {
    const { vm } = setupTimeline();
    const expected = 'approvalActionDisabled,approvalHint,attachmentCanZoomOut,attachmentHoverPreview,attachmentHoverStyle,attachmentLightbox,attachmentPreviewApi,avatarText,bubbleRole,closeAttachmentLightbox,closePresencePopover,collapsedToolCount,collapsedToolTickerText,commandExitText,commandHasOutput,commandStatusIcon,commandStatusIconClass,commandStatusText,commandTitle,copyFilePath,copyPlanText,displayFilePath,formatTime,getItemKey,hasAvatar,hasMore,hasPresenceTarget,injectSentenceBreaks,internalRouteLabel,isCitationTarget,isDialog,itemHasSpec,jsonRenderMarkdownActionHandlers,onAssistantBodyClick,onAttachmentHoverLeave,onAttachmentPreviewEnter,onAttachmentPreviewLeave,onAttachmentPreviewResetZoom,onAttachmentPreviewZoomIn,onAttachmentPreviewZoomOut,openPresencePopover,planCardSpec,presenceLabel,presencePopoverTitle,renderAssistantBody,resolvedPresenceTarget,respondApproval,roleLabel,schedulePresencePopoverClose,sharedStatusMeta,sharedStatusText,showAgentPresence,showMore,showPresencePopover,showThinkingPopover,showToolTicker,splitBySpec,stateLabel,streamingAssistantState,streamingFrameVersion,thinkingPopoverText,thinkingToolSummaries,timelineItems,translateText,visibleItems,visibleOffset'.split(',').sort();
    expect(Object.keys(vm).sort()).toEqual(expected);
  });

  it('keeps timeline filtering, short-reasoning merge and fallback keys stable', () => {
    const planItem = { kind: 'plan', ts: '2026-03-14T10:00:00Z', text: '拆分计划', done: false };
    const { vm } = setupTimeline({
      items: [
        { id: 'user-1', kind: 'user', text: '继续' },
        { id: 'assistant-1', kind: 'assistant', text: 'checking files', done: true },
        { id: 'assistant-2', kind: 'assistant', text: 'running tests', done: true },
        { id: 'thinking-1', kind: 'thinking', text: '处理中', done: false },
        { id: 'tool-1', kind: 'tool', tool: 'open_file', preview: 'ChatTimeline.js' },
        planItem,
      ],
    });

    expect(vm.timelineItems.value.map((item) => item.kind)).toEqual(['user', 'assistant', 'assistant', 'plan']);
    expect(vm.visibleItems.value.map((item) => item.id || item.kind)).toEqual(['user-1', 'assistant-2', 'plan']);
    expect(vm.visibleItems.value[1].text).toBe('checking files\n\nrunning tests');
    expect(vm.getItemKey(planItem, 3)).toBe('ts:2026-03-14T10:00:00Z');
    expect(vm.getItemKey({ kind: 'plan', text: '无时间戳计划' }, 4)).toBe('无时间戳计划');
  });

  it('keeps command helper mappings stable', () => {
    const { vm } = setupTimeline();
    const running = { kind: 'command', status: 'running', command: 'npm test' };
    const failed = { kind: 'command', status: 'failed', command: 'go test ./...', output: 'boom', exitCode: 2 };
    const cancelled = { kind: 'command', status: 'cancelled' };

    expect(vm.commandStatusText(running)).toBe('命令执行中');
    expect(vm.commandStatusIcon(running)).toBe('◌');
    expect(vm.commandStatusIconClass(running)).toContain('running');
    expect(vm.commandTitle(running)).toBe('$ npm test');

    expect(vm.commandStatusText(failed)).toBe('命令执行失败');
    expect(vm.commandStatusIcon(failed)).toBe('✕');
    expect(vm.commandHasOutput(failed)).toBe(true);
    expect(vm.commandExitText(failed)).toBe('退出码 2');

    expect(vm.commandStatusText(cancelled)).toBe('命令已取消');
    expect(vm.commandTitle(cancelled)).toBe('终端命令');
    expect(vm.commandHasOutput(cancelled)).toBe(false);
    expect(vm.commandExitText(cancelled)).toBe('');
  });

  it('keeps approval action state stable across success, failure and invalid ids', async () => {
    const { vm } = setupTimeline();
    const successItem = { kind: 'approval', requestId: 7, command: 'rm -rf /tmp/demo' };

    expect(vm.approvalActionDisabled(successItem)).toBe(false);
    expect(vm.approvalHint(successItem)).toBe('请选择同意或拒绝');
    expect(vm.stateLabel(successItem)).toBe('待确认');

    await vm.respondApproval(successItem, true);
    expect(apiMock.callAPI).toHaveBeenCalledWith('approval/respond', { requestId: 7, approved: true });
    expect(vm.approvalActionDisabled(successItem)).toBe(true);
    expect(vm.approvalHint(successItem)).toBe('审批结果已提交');
    expect(vm.stateLabel(successItem)).toBe('已提交');

    apiMock.callAPI.mockReset();
    apiMock.callAPI.mockRejectedValueOnce(new Error('network down'));
    const failedItem = { kind: 'approval', requestId: 9, command: 'deploy' };
    await vm.respondApproval(failedItem, false);
    expect(apiMock.callAPI).toHaveBeenCalledWith('approval/respond', { requestId: 9, approved: false });
    expect(vm.approvalActionDisabled(failedItem)).toBe(false);
    expect(vm.approvalHint(failedItem)).toBe('请选择同意或拒绝');
    expect(vm.stateLabel(failedItem)).toBe('待确认');

    const invalidItem = { kind: 'approval', requestId: 'oops' };
    expect(vm.approvalActionDisabled(invalidItem)).toBe(true);
    expect(vm.approvalHint(invalidItem)).toBe('当前审批不可交互，请重试');
    expect(vm.stateLabel(invalidItem)).toBe('不可交互');
  });

  it('keeps timeline helper contracts stable for plan spec splitting and clipboard actions', async () => {
    const { vm } = setupTimeline();
    const rawPlan = ['前文', '```json-render', '{"type":"Text","text":"Spec body"}', '```', '尾注'].join('\n');
    const spec = vm.planCardSpec({ kind: 'plan', done: false, ts: '2026-03-14T10:00:00Z', text: rawPlan });

    expect(vm.itemHasSpec(rawPlan)).toBe(true);
    expect(vm.splitBySpec(rawPlan).map((part) => part.type)).toEqual(['text', 'spec', 'text']);
    expect(spec.type).toBe('Card');
    expect(spec.description).toBe('进行中');
    expect(spec.children[0].children[0]).toEqual({ type: 'Badge', text: '进行中', variant: 'primary' });
    expect(spec.children.some((child) => child.type === 'Text' && child.text === 'Spec body')).toBe(true);
    expect(spec.children.some((child) => child.type === 'Markdown' && child.text.includes('尾注'))).toBe(true);
    expect(vm.displayFilePath('/Users/alice/project/src/main.go')).toBe('~/project/src/main.go');

    await vm.copyFilePath(' /tmp/demo.go ');
    await vm.copyPlanText(' 1. add tests ');
    expect(globalThis.navigator.clipboard.writeText).toHaveBeenNthCalledWith(1, '/tmp/demo.go');
    expect(globalThis.navigator.clipboard.writeText).toHaveBeenNthCalledWith(2, '1. add tests');
  });

  it('keeps presence popover summaries, ticker and timers stable', async () => {
    vi.useFakeTimers();
    const { vm } = setupTimeline({
      activeStatus: 'thinking',
      activeStatusText: '分析中',
      activeStatusMeta: 'gpt-5',
      items: [
        { id: 'assistant-1', kind: 'assistant', text: '收到', done: true },
        { id: 'thinking-1', kind: 'thinking', text: '检查错误', done: false, ts: '2026-03-14T10:00:00Z' },
        { id: 'tool-1', kind: 'tool', tool: 'open_file', preview: 'ChatTimeline.js', elapsedMs: 18, ts: '2026-03-14T10:00:01Z' },
        { id: 'cmd-1', kind: 'command', status: 'running', command: 'npm test', output: 'running...', ts: '2026-03-14T10:00:02Z' },
      ],
      presenceTarget: '#presence-anchor',
    });

    expect(vm.showAgentPresence.value).toBe(true);
    expect(vm.presenceLabel.value).toBe('分析中');
    expect(vm.sharedStatusMeta.value).toBe('gpt-5');
    expect(vm.thinkingPopoverText.value).toBe('检查错误');
    expect(vm.thinkingToolSummaries.value.some((entry) => entry.kindLabel === '工具' && entry.text.includes('open_file'))).toBe(true);
    expect(vm.thinkingToolSummaries.value.some((entry) => entry.kindLabel === '命令' && entry.text.includes('$ npm test'))).toBe(true);
    expect(vm.collapsedToolCount.value).toBe(1);
    expect(vm.collapsedToolTickerText.value).toContain('open_file');
    expect(vm.showToolTicker.value).toBe(true);
    expect(vm.resolvedPresenceTarget.value).toBe('#presence-anchor');
    expect(vm.hasPresenceTarget.value).toBe(true);
    expect(vm.presencePopoverTitle.value).toContain('已收起 1 个工具调用');

    expect(vm.showPresencePopover.value).toBe(false);
    vm.openPresencePopover();
    expect(vm.showPresencePopover.value).toBe(true);
    vm.schedulePresencePopoverClose();
    await vi.advanceTimersByTimeAsync(119);
    expect(vm.showPresencePopover.value).toBe(true);
    await vi.advanceTimersByTimeAsync(1);
    expect(vm.showPresencePopover.value).toBe(false);
  });

  it('keeps timeline window expansion and non-merge boundaries stable', () => {
    const manyItems = Array.from({ length: 101 }, (_, index) => ({
      id: `user-${index + 1}`,
      kind: 'user',
      text: `message ${index + 1}`,
    }));
    const { vm } = setupTimeline({ items: manyItems });

    expect(vm.hasMore.value).toBe(true);
    expect(vm.visibleItems.value).toHaveLength(100);
    vm.showMore();
    expect(vm.visibleItems.value).toHaveLength(101);
    expect(vm.hasMore.value).toBe(false);

    const { vm: boundaryVm } = setupTimeline({
      items: [
        { id: 'assistant-md', kind: 'assistant', text: '# Title', done: true },
        { id: 'assistant-machine', kind: 'assistant', text: 'chunk-1', done: true },
        { id: 'assistant-long', kind: 'assistant', text: 'word '.repeat(170), done: true },
      ],
    });

    expect(boundaryVm.visibleItems.value.map((item) => item.id)).toEqual([
      'assistant-md',
      'assistant-machine',
      'assistant-long',
    ]);
  });

  it('keeps approval busy and non-pending branches stable', async () => {
    let resolvePending;
    apiMock.callAPI.mockReset();
    apiMock.callAPI.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolvePending = resolve;
        }),
    );
    const { vm } = setupTimeline();
    const pendingItem = { kind: 'approval', requestId: 11, command: 'deploy' };

    const submitPromise = vm.respondApproval(pendingItem, true);
    expect(vm.approvalActionDisabled(pendingItem)).toBe(true);
    expect(vm.approvalHint(pendingItem)).toBe('正在提交审批结果...');
    await vm.respondApproval(pendingItem, false);
    expect(apiMock.callAPI).toHaveBeenCalledTimes(1);

    resolvePending({ ok: false });
    await submitPromise;
    expect(vm.approvalActionDisabled(pendingItem)).toBe(false);
    expect(vm.approvalHint(pendingItem)).toBe('请选择同意或拒绝');
    expect(vm.stateLabel(pendingItem)).toBe('待确认');
  });

  it('keeps helper empty and fallback branches stable', async () => {
    const { vm } = setupTimeline();

    expect(vm.formatTime('bad-ts')).toBe('');
    expect(vm.displayFilePath('/home/alice/repo/main.go')).toBe('~/repo/main.go');
    expect(vm.displayFilePath('C:\\Users\\Alice\\repo\\main.go')).toBe('~\\repo\\main.go');
    expect(vm.itemHasSpec('plain text')).toBe(false);
    expect(vm.splitBySpec('plain text')).toEqual([{ type: 'text', content: 'plain text' }]);

    const emptyPlan = vm.planCardSpec({ kind: 'plan', done: true, text: '' });
    expect(emptyPlan.description).toBe('已完成');
    expect(emptyPlan.children[emptyPlan.children.length - 1]).toEqual({ type: 'Text', text: '(空计划)' });

    vi.stubGlobal('navigator', {});
    await expect(vm.copyFilePath('/tmp/demo.go')).resolves.toBeUndefined();
    await expect(vm.copyPlanText('copy me')).resolves.toBeUndefined();
  });

  it('keeps assistant body auxiliary interaction branches stable', async () => {
    vi.useFakeTimers();
    const emit = vi.fn();
    const { vm } = setupTimeline(
      {
        items: [{ id: 'cmd-1', kind: 'command', terminal_chunk_id: 'chunk-1', output: 'ok' }],
      },
      emit,
    );

    const copyBtn = {
      hasAttribute: vi.fn((name) => name === 'data-copy-code'),
      getAttribute: vi.fn((name) => (name === 'data-copy-code' ? 'console.log(1)' : '')),
      classList: { add: vi.fn(), remove: vi.fn() },
    };
    vm.onAssistantBodyClick({
      target: {
        closest: vi.fn((selector) => (selector === '.chat-md-code-copy-btn' ? copyBtn : null)),
      },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });
    await Promise.resolve();
    await Promise.resolve();
    expect(globalThis.navigator.clipboard.writeText).toHaveBeenCalledWith('console.log(1)');
    expect(copyBtn.classList.add).toHaveBeenCalledWith('is-copied');
    await vi.advanceTimersByTimeAsync(1800);
    expect(copyBtn.classList.remove).toHaveBeenCalledWith('is-copied');

    const block = { classList: { toggle: vi.fn() } };
    const expandBtn = {
      hasAttribute: vi.fn((name) => name === 'data-expand-code'),
      closest: vi.fn((selector) => (selector === '.chat-md-code-block[data-collapsible]' ? block : null)),
    };
    const expandEvent = {
      target: {
        closest: vi.fn((selector) => {
          if (selector === '.chat-md-code-copy-btn') return null;
          if (selector === '.chat-md-code-expand-btn') return expandBtn;
          return null;
        }),
      },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    };
    vm.onAssistantBodyClick(expandEvent);
    expect(block.classList.toggle).toHaveBeenCalledWith('is-expanded');
    expect(expandEvent.preventDefault).toHaveBeenCalled();
    expect(expandEvent.stopPropagation).toHaveBeenCalled();

    const brokenFileRefTarget = {
      closest: vi.fn((selector) => {
        if (selector === '.chat-md-code-copy-btn') return null;
        if (selector === '.chat-md-code-expand-btn') return null;
        if (selector.includes('chat-md-file-ref')) {
          return {
            getAttribute: vi.fn((name) => ({
              'data-file-path': '   ',
              'data-file-line': '7',
              'data-file-column': '3',
            }[name] || '')),
            textContent: 'broken-ref',
          };
        }
        return null;
      }),
    };
    const beforeBroken = emit.mock.calls.length;
    vm.onAssistantBodyClick({
      target: brokenFileRefTarget,
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });
    expect(emit).toHaveBeenCalledTimes(beforeBroken);

    const citationNode = {
      classList: { contains: vi.fn((name) => name === 'chat-md-citation') },
      getAttribute: vi.fn((name) => ({
        'data-citation-kind': 'terminal',
        'data-terminal-chunk-id': 'chunk-1',
        'data-line-start': '2',
        'data-line-end': '4',
      }[name] || '')),
      textContent: 'Terminal output',
    };
    vm.onAssistantBodyClick({
      target: {},
      composedPath: () => [citationNode],
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });
    expect(vm.isCitationTarget({ id: 'cmd-1' })).toBe(true);
    expect(emit).toHaveBeenLastCalledWith(
      'citation-click',
      expect.objectContaining({ kind: 'terminal', chunkId: 'chunk-1', lineStart: 2, lineEnd: 4 }),
    );
    await vi.advanceTimersByTimeAsync(2200);
    expect(vm.isCitationTarget({ id: 'cmd-1' })).toBe(false);
  });

  it('keeps presence no-op and immediate-close fallbacks stable', () => {
    const { vm } = setupTimeline({
      activeStatusText: '未选择会话',
      items: [],
      presenceTarget: { value: '' },
    });

    expect(vm.showAgentPresence.value).toBe(false);
    expect(vm.resolvedPresenceTarget.value).toBe('body');
    expect(vm.hasPresenceTarget.value).toBe(false);
    vm.openPresencePopover();
    expect(vm.showPresencePopover.value).toBe(false);

    const { vm: activeVm } = setupTimeline({
      activeStatus: 'thinking',
      activeStatusText: '分析中',
      items: [{ id: 'thinking-1', kind: 'thinking', text: '检查错误', done: false }],
      presenceTarget: { value: '#anchor' },
    });
    activeVm.openPresencePopover();
    expect(activeVm.showPresencePopover.value).toBe(true);
    vi.stubGlobal('setTimeout', undefined);
    activeVm.schedulePresencePopoverClose();
    expect(activeVm.showPresencePopover.value).toBe(false);
    expect(activeVm.resolvedPresenceTarget.value).toBe('#anchor');
    expect(activeVm.hasPresenceTarget.value).toBe(true);
  });

  it('keeps role, avatar, dialog and state mappings stable', () => {
    const { vm } = setupTimeline();
    const internalUser = {
      kind: 'user',
      internal: true,
      fromDisplay: 'worker-fallback',
      toDisplay: 'main-fallback',
    };

    expect(vm.roleLabel(internalUser)).toBe('worker-fallback');
    expect(vm.internalRouteLabel(internalUser)).toBe('→ main-fallback');
    expect(vm.bubbleRole(internalUser)).toBe('role-internal');
    expect(vm.avatarText(internalUser)).toBe('↔');
    expect(vm.hasAvatar(internalUser)).toBe(true);
    expect(vm.isDialog(internalUser)).toBe(true);

    expect(vm.roleLabel({ kind: 'user' })).toBe('你');
    expect(vm.roleLabel({ kind: 'assistant' })).toBe('助手');
    expect(vm.roleLabel({ kind: 'thinking' })).toBe('思考');
    expect(vm.roleLabel({ kind: 'error' })).toBe('错误');
    expect(vm.roleLabel({ kind: 'unknown' })).toBe('事件');
    expect(vm.bubbleRole({ kind: 'assistant' })).toBe('role-assistant');
    expect(vm.bubbleRole({ kind: 'command' })).toBe('role-system');
    expect(vm.avatarText({ kind: 'assistant' })).toBe('AI');
    expect(vm.avatarText({ kind: 'user' })).toBe('U');
    expect(vm.hasAvatar({ kind: 'command' })).toBe(false);
    expect(vm.isDialog({ kind: 'command' })).toBe(false);

    expect(vm.stateLabel({ kind: 'thinking', done: false })).toBe('处理中');
    expect(vm.stateLabel({ kind: 'thinking', done: true })).toBe('完成');
    expect(vm.stateLabel({ kind: 'command', status: 'running' })).toBe('执行中');
    expect(vm.stateLabel({ kind: 'tool', status: 'failed' })).toBe('失败');
    expect(vm.stateLabel({ kind: 'tool', status: 'ok' })).toBe('调用');
    expect(vm.stateLabel({ kind: 'file', status: 'saved' })).toBe('已保存');
    expect(vm.stateLabel({ kind: 'file', status: 'editing' })).toBe('修改中');
    expect(vm.stateLabel({ kind: 'plan', done: true })).toBe('完成');
  });

  it('keeps render empty/cache branches and file-ref composedPath fallback stable', () => {
    const emit = vi.fn();
    const { vm } = setupTimeline({}, emit);

    expect(vm.renderAssistantBody('')).toBe('');
    expect(vm.renderAssistantBody('same body')).toBe('<p>same body</p>');
    expect(vm.renderAssistantBody('same body')).toBe('<p>same body</p>');
    expect(markdownMock.render).toHaveBeenCalledWith('same body');

    const fileRefNode = {
      classList: {
        contains: vi.fn((name) => name === 'chat-md-file-ref'),
      },
      getAttribute: vi.fn((name) => ({
        'data-file-path': 'src/main.go',
        'data-file-line': '8',
        'data-file-column': '2',
      }[name] || '')),
      textContent: 'src/main.go:8',
    };
    vm.onAssistantBodyClick({
      target: {},
      composedPath: () => [fileRefNode],
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });
    expect(emit).toHaveBeenCalledWith('file-ref-click', {
      path: 'src/main.go',
      line: 8,
      column: 2,
      raw: 'src/main.go:8',
    });

    const callCount = emit.mock.calls.length;
    vm.onAssistantBodyClick({
      target: {
        closest: vi.fn(() => null),
      },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });
    expect(emit).toHaveBeenCalledTimes(callCount);
  });

  it('keeps command helper fallback statuses stable', () => {
    const { vm } = setupTimeline();

    expect(vm.commandStatusText({ kind: 'command', status: 'canceled' })).toBe('命令已取消');
    expect(vm.commandStatusIcon({ kind: 'command', status: 'canceled' })).toBe('⚠');
    expect(vm.commandStatusIconClass({ kind: 'command', status: 'canceled' })).toContain('waiting');
    expect(vm.commandStatusText({ kind: 'command', status: 'done' })).toBe('已执行命令');
    expect(vm.commandStatusIcon({ kind: 'command', status: 'done' })).toBe('✓');
  });

  it('keeps item key fallbacks and file-ref numeric coercion stable', () => {
    const emit = vi.fn();
    const { vm } = setupTimeline({}, emit);

    expect(vm.getItemKey(null, 2)).toBe('idx-2');
    expect(vm.getItemKey({ kind: 'plan', id: '', ts: '', text: '' }, 3)).toBe('idx-3-');

    const refNode = {
      getAttribute: vi.fn((name) => ({
        'data-file-path': 'src/worker.go',
        'data-file-line': 'oops',
        'data-file-column': 'nan',
      }[name] || '')),
      textContent: 'src/worker.go',
    };
    vm.onAssistantBodyClick({
      target: {
        nodeType: 3,
        parentElement: {
          closest: vi.fn((selector) => (selector.includes('chat-md-file-ref') ? refNode : null)),
        },
      },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(emit).toHaveBeenCalledWith('file-ref-click', {
      path: 'src/worker.go',
      line: 1,
      column: 0,
      raw: 'src/worker.go',
    });
  });

  it('keeps presence summary branches stable for file-only and failed-tool cases', () => {
    const { vm: fileVm } = setupTimeline({
      activeStatus: 'responding',
      activeStatusText: '同步中',
      items: [
        { id: 'assistant-1', kind: 'assistant', text: '收到', done: true },
        { id: 'file-1', kind: 'file', status: 'saved', file: '', ts: '2026-03-14T10:00:03Z' },
      ],
    });

    expect(fileVm.thinkingPopoverText.value).toBe('');
    expect(fileVm.showThinkingPopover.value).toBe(true);
    expect(fileVm.showToolTicker.value).toBe(false);
    expect(fileVm.presencePopoverTitle.value).toBe('悬浮查看思考过程与工具摘要');
    expect(fileVm.thinkingToolSummaries.value).toEqual([
      expect.objectContaining({ kindLabel: '文件', text: '已保存 · 未知文件' }),
    ]);

    const { vm: failedToolVm } = setupTimeline({
      activeStatus: 'thinking',
      activeStatusText: '分析中',
      items: [
        { id: 'assistant-1', kind: 'assistant', text: '收到', done: true },
        { id: 'tool-1', kind: 'tool', status: 'failed', tool: '', file: '/Users/alice/repo/src/main.go', ts: '2026-03-14T10:00:04Z' },
      ],
    });

    expect(failedToolVm.collapsedToolCount.value).toBe(1);
    expect(failedToolVm.collapsedToolTickerText.value).toContain('失败 · 未知工具');
    expect(failedToolVm.collapsedToolTickerText.value).toContain('~/repo/src/main.go');
  });

  it('keeps copy helper no-op branches stable for empty values', async () => {
    const { vm } = setupTimeline();

    await vm.copyFilePath('');
    await vm.copyPlanText('   ');
    expect(globalThis.navigator.clipboard.writeText).not.toHaveBeenCalled();
  });

  it('keeps presence dedupe, truncation and no-summary branches stable', () => {
    const longPreview = 'preview '.repeat(20);
    const { vm } = setupTimeline({
      activeStatus: 'thinking',
      activeStatusText: '分析中',
      items: [
        { id: 'assistant-1', kind: 'assistant', text: '收到', done: true },
        { id: 'tool-1', kind: 'tool', tool: 'open_file', preview: longPreview, elapsedMs: 18, ts: '2026-03-14T10:00:05Z' },
        { id: 'tool-2', kind: 'tool', tool: 'open_file', preview: longPreview, elapsedMs: 18, ts: '2026-03-14T10:00:05Z' },
        { id: 'cmd-1', kind: 'command', status: 'running', command: 'npm test', output: 'log '.repeat(40), ts: '2026-03-14T10:00:06Z' },
        { id: 'cmd-2', kind: 'command', status: 'running', command: 'npm test', output: 'log '.repeat(40), ts: '2026-03-14T10:00:06Z' },
      ],
    });

    expect(vm.thinkingToolSummaries.value).toHaveLength(2);
    expect(vm.thinkingToolSummaries.value.some((entry) => entry.text.includes('…'))).toBe(true);
    expect(vm.collapsedToolTickerText.value.split('   •   ')).toHaveLength(1);

    const { vm: idleVm } = setupTimeline({
      activeStatus: 'starting',
      activeStatusText: '准备中',
      items: [{ id: 'assistant-1', kind: 'assistant', text: '收到', done: true }],
    });
    expect(idleVm.showAgentPresence.value).toBe(true);
    expect(idleVm.showThinkingPopover.value).toBe(false);
    idleVm.openPresencePopover();
    expect(idleVm.showPresencePopover.value).toBe(false);
  });

  it('keeps full-text pretext streaming and deferred flush branches stable', async () => {
    vi.useFakeTimers();
    const { props, vm } = setupTimeline({
      items: [{ id: 'assistant-1', kind: 'assistant', text: 'Intro\n```js\nconst x = 1', done: false }],
    });

    const initial = vm.streamingAssistantState(props.items[0]);
    expect(initial.text).toBe('Intro\n```js\nconst x = 1');
    expect(initial.heightPx).toBeGreaterThanOrEqual(0);

    props.items = [{ ...props.items[0], text: 'Intro\n```js\nconst x = 1\n```', done: false }];
    const beforeFlush = vm.streamingAssistantState(props.items[0]);
    expect(beforeFlush.text).toBe('Intro\n```js\nconst x = 1');

    await vi.advanceTimersByTimeAsync(32);
    const afterFlush = vm.streamingAssistantState(props.items[0]);
    expect(afterFlush.text).toBe('Intro\n```js\nconst x = 1\n```');
    expect(afterFlush.heightPx).toBeGreaterThanOrEqual(0);
  });

  it('locks template split contracts for presence, plan card and approval actions', () => {
    expect(ChatTimeline.template).toContain('<teleport :to="resolvedPresenceTarget" :disabled="!hasPresenceTarget">');
    expect(ChatTimeline.template).toContain('<JsonRenderer :spec="planCardSpec(item)" :markdown-action-handlers="jsonRenderMarkdownActionHandlers" />');
    expect(ChatTimeline.template).toContain('approval-action-btn approval-action-btn--approve');
    expect(ChatTimeline.template).toContain("chat-status-presence--popoverable");
    expect(ChatTimeline.template).toContain(':text="collapsedToolTickerText"');
  });
});