// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

const markdownMock = vi.hoisted(() => ({
  render: vi.fn((text) => `<p>${text}</p>`),
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(),
}));

vi.mock('./utils/assistant-markdown.js', () => ({
  renderAssistantMarkdown: markdownMock.render,
  injectSentenceBreaks: vi.fn((text) => text),
}));

import { ChatTimeline } from './components/ChatTimeline.js';

function setupTimeline(overrides = {}, emit = vi.fn()) {
  return ChatTimeline.setup(
    {
      items: overrides.items ?? [],
      activeStatus: overrides.activeStatus ?? 'idle',
      activeStatusText: overrides.activeStatusText ?? '',
      activeStatusMeta: overrides.activeStatusMeta ?? '',
      pinnedPlanVisible: overrides.pinnedPlanVisible ?? false,
      pinnedPlanItemId: overrides.pinnedPlanItemId ?? null,
      resolveThreadDisplayName: overrides.resolveThreadDisplayName ?? null,
      presenceTarget: overrides.presenceTarget ?? null,
    },
    { emit },
  );
}

describe('ChatTimeline behavior', () => {
  it('keeps both timeline lists empty when there are no items', () => {
    const vm = setupTimeline();

    expect(vm.timelineItems.value).toEqual([]);
    expect(vm.visibleItems.value).toEqual([]);
  });

  it('renders assistant markdown through the shared cache', () => {
    const vm = setupTimeline({
      items: [{ id: 'assistant-1', kind: 'assistant', text: '**hello**' }],
    });

    expect(vm.renderAssistantBody('**hello**')).toBe('<p>**hello**</p>');
    expect(vm.renderAssistantBody('**hello**')).toBe('<p>**hello**</p>');
    expect(markdownMock.render).toHaveBeenCalledTimes(1);
  });

  it('emits file-ref-click with normalized payload when clicking a rendered file ref', () => {
    const emit = vi.fn();
    const vm = setupTimeline({}, emit);
    const refNode = {
      getAttribute: vi.fn((name) => ({
        'data-file-path': 'src/main.js',
        'data-file-line': '7',
        'data-file-column': '3',
      }[name] || '')),
      textContent: 'src/main.js:7',
    };

    vm.onAssistantBodyClick({
      target: {
        closest: vi.fn(() => refNode),
      },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(emit).toHaveBeenCalledWith('file-ref-click', {
      path: 'src/main.js',
      line: 7,
      column: 3,
      raw: 'src/main.js:7',
    });
  });

  it('emits citation-click for image citations and highlights terminal citation targets', () => {
    const emit = vi.fn();
    const vm = setupTimeline({
      items: [{ id: 'cmd-1', kind: 'command', command: 'npm test', output: 'ok', terminal_chunk_id: 'chunk-1' }],
    }, emit);

    const terminalNode = {
      getAttribute: vi.fn((name) => ({
        'data-citation-kind': 'terminal',
        'data-terminal-chunk-id': 'chunk-1',
        'data-line-start': '3',
        'data-line-end': '5',
      }[name] || '')),
      textContent: 'Terminal output',
    };

    vm.onAssistantBodyClick({
      target: { closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? terminalNode : null)) },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(vm.isCitationTarget({ id: 'cmd-1' })).toBe(true);
    expect(emit).toHaveBeenCalledWith('citation-click', {
      kind: 'terminal',
      chunkId: 'chunk-1',
      lineStart: 3,
      lineEnd: 5,
      raw: 'Terminal output',
    });

    const imageNode = {
      getAttribute: vi.fn((name) => ({
        'data-citation-kind': 'image',
        'data-asset-pointer': 'data:image/png;base64,abc',
        'data-image-src': 'https://example.com/preview.png',
        'data-file-path': '',
      }[name] || '')),
      textContent: 'Screenshot',
    };

    vm.onAssistantBodyClick({
      target: { closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? imageNode : null)) },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(emit).toHaveBeenCalledWith('citation-click', {
      kind: 'image',
      assetPointer: 'data:image/png;base64,abc',
      imageSrc: 'https://example.com/preview.png',
      path: '',
      raw: 'Screenshot',
    });
  });

  it('emits actionable payloads for task, automation, and code-comment citations', () => {
    const emit = vi.fn();
    const vm = setupTimeline({}, emit);

    const taskNode = {
      getAttribute: vi.fn((name) => ({
        'data-citation-kind': 'task',
        'data-task-title': 'Review task',
        'data-task-prompt': 'Review the patch',
      }[name] || '')),
      textContent: 'Review task',
    };
    vm.onAssistantBodyClick({ target: { closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? taskNode : null)) }, preventDefault: vi.fn(), stopPropagation: vi.fn() });
    expect(emit).toHaveBeenCalledWith('citation-click', {
      kind: 'task',
      title: 'Review task',
      prompt: 'Review the patch',
      raw: 'Review task',
    });

    const commentNode = {
      getAttribute: vi.fn((name) => ({
        'data-citation-kind': 'code-comment',
        'data-message': 'Please rename this',
        'data-file-path': 'src/main.go',
        'data-line-start': '9',
        'data-line-end': '11',
      }[name] || '')),
      textContent: 'Code comment',
    };
    vm.onAssistantBodyClick({ target: { closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? commentNode : null)) }, preventDefault: vi.fn(), stopPropagation: vi.fn() });
    expect(emit).toHaveBeenCalledWith('citation-click', {
      kind: 'code-comment',
      title: '',
      message: 'Please rename this',
      prompt: '',
      path: 'src/main.go',
      lineStart: 9,
      lineEnd: 11,
      raw: 'Code comment',
    });

    const automationNode = {
      getAttribute: vi.fn((name) => ({
        'data-citation-kind': 'automation-update',
        'data-message': 'Workflow rerun completed',
      }[name] || '')),
      textContent: 'Automation update',
    };
    vm.onAssistantBodyClick({ target: { closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? automationNode : null)) }, preventDefault: vi.fn(), stopPropagation: vi.fn() });
    expect(emit).toHaveBeenCalledWith('citation-click', {
      kind: 'automation-update',
      title: '',
      message: 'Workflow rerun completed',
      prompt: '',
      path: '',
      lineStart: 0,
      lineEnd: 0,
      raw: 'Automation update',
    });

  });

  it('retains the pinned plan item in the main visible timeline', () => {
    const vm = setupTimeline({
      pinnedPlanVisible: true,
      pinnedPlanItemId: 'plan-1',
      items: [
        { id: 'plan-1', kind: 'plan', text: '先执行计划', done: false },
        { id: 'assistant-1', kind: 'assistant', text: '已继续执行' },
      ],
    });

    expect(vm.timelineItems.value.map((item) => item.id)).toEqual(['plan-1', 'assistant-1']);
    expect(vm.visibleItems.value.map((item) => item.id)).toEqual(['plan-1', 'assistant-1']);
  });

  it('hides completed plans once a newer plan supersedes them', () => {
    const vm = setupTimeline({
      items: [
        { id: 'plan-old', kind: 'plan', text: '旧计划', done: true },
        { id: 'assistant-1', kind: 'assistant', text: '旧计划已完成', done: true },
        { id: 'plan-new', kind: 'plan', text: '新计划', done: false },
      ],
    });

    expect(vm.timelineItems.value.map((item) => item.id)).toEqual(['assistant-1', 'plan-new']);
    expect(vm.visibleItems.value.map((item) => item.id)).toEqual(['assistant-1', 'plan-new']);
  });

  it('hides completed plans once a newer user task starts before the next plan exists', () => {
    const vm = setupTimeline({
      items: [
        { id: 'plan-old', kind: 'plan', text: '旧任务计划', done: true },
        { id: 'assistant-1', kind: 'assistant', text: '旧任务已完成', done: true },
        { id: 'user-2', kind: 'user', text: '开始一个新任务' },
      ],
    });

    expect(vm.timelineItems.value.map((item) => item.id)).toEqual(['assistant-1', 'user-2']);
    expect(vm.visibleItems.value.map((item) => item.id)).toEqual(['assistant-1', 'user-2']);
  });

  it('hides stale plans after a newer user instruction even when the old plan never flips done', () => {
    const vm = setupTimeline({
      items: [
        { id: 'plan-old', kind: 'plan', text: '旧任务计划', done: false },
        { id: 'assistant-1', kind: 'assistant', text: '旧任务已完成', done: true },
        { id: 'user-2', kind: 'user', text: '回收子agent' },
      ],
    });

    expect(vm.timelineItems.value.map((item) => item.id)).toEqual(['assistant-1', 'user-2']);
    expect(vm.visibleItems.value.map((item) => item.id)).toEqual(['assistant-1', 'user-2']);
  });

  it('locks the active-status loading contract in the template', () => {
    expect(ChatTimeline.template).toContain("activeStatus === 'thinking' || activeStatus === 'starting' || activeStatus === 'running' || activeStatus === 'responding'");
    expect(ChatTimeline.template).toContain('loading-shimmer');
  });
});
