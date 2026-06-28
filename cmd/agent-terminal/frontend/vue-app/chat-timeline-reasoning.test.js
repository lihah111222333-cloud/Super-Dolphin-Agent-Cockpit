// @ts-nocheck
import { afterEach, describe, it, expect, vi } from 'vitest';
import { reactive, nextTick } from '../lib/vue.esm-browser.prod.js';
import { ChatTimeline } from './components/ChatTimeline.js';

const leakedProgressText = [
  'I’m resuming from the current filesystem state and verifying the two exact phase-3 surfaces now: the page helper/export block and the production chat-rail integration block, plus the cleaned integration test file.',
  'I found the helper is currently living at the wrong level in the file.',
  'I’m locating that stray definition around the setup prelude so I can move it back to the top-level utility section and leave only one canonical copy.',
  'I found the file has already evolved to a different phase-3 integration shape: a top-level `buildVisibleChatThreadCards(opts)` helper plus a `visibleChatThreadCardState` computed.',
  'I’m inspecting that live path directly now instead of forcing the earlier helper signature.',
  'The live path is now clearly helper-based and the module export resolves correctly in Node.',
  'I’m rerunning the two exact phase-3 test files from the current state.',
  'Phase-3 tests are green from the current state.',
  'I’m doing a split diagnostics pass so I can confirm the production page file separately from the test-file hints.',
  'I’ve stabilized the phase-3 integration.',
].join('');

function createTimelineProps(items) {
  return reactive({
    items,
    activeStatus: 'thinking',
    activeStatusText: '处理中',
    activeStatusMeta: '',
    pinnedPlanVisible: false,
    pinnedPlanItemId: null,
    resolveThreadDisplayName: null,
  });
}

function setupTimeline(items) {
  const props = createTimelineProps(items);
  const vm = ChatTimeline.setup(props, { emit: () => {} });
  return { props, vm };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('ChatTimeline reasoning leak handling', () => {
  it('keeps assistant replies in the main timeline and removes the hidden summary placeholder', () => {
    const { vm } = setupTimeline([
      { id: 'user-1', kind: 'user', text: '继续修复' },
      { id: 'assistant-1', kind: 'assistant', text: leakedProgressText, ts: '2026-03-07T10:00:00Z', done: true },
    ]);
    const visibleIds = vm.visibleItems.value.map((item) => item.id);

    expect(visibleIds).toEqual(['user-1', 'assistant-1']);
    expect(vm.thinkingPopoverText.value).toBe('');
    expect(vm.showThinkingPopover.value).toBe(false);
  });

  it('keeps the full assistant text in plain pretext mode during streaming and markdown after completion', async () => {
    const { props, vm } = setupTimeline([
      { id: 'assistant-1', kind: 'assistant', text: '# Title\n\n- first\n- sec', ts: '2026-03-07T10:00:00Z', done: false },
    ]);
    const streaming = vm.streamingAssistantState(props.items[0]);
    expect(streaming.text).toBe('# Title\n\n- first\n- sec');
    expect(streaming.heightPx).toBeGreaterThanOrEqual(0);

    props.items = [{ ...props.items[0], text: '# Title\n\n- first\n- second\n', done: true }];
    await nextTick();
    expect(vm.renderAssistantBody(props.items[0].text)).toContain('<li>second</li>');
  });

  it('keeps balanced inline markdown in plain pretext mode while the assistant is still streaming', () => {
    const { props, vm } = setupTimeline([
      { id: 'assistant-1', kind: 'assistant', text: '**hello**', ts: '2026-03-07T10:00:00Z', done: false },
    ]);
    const streaming = vm.streamingAssistantState(props.items[0]);
    expect(streaming.text).toBe('**hello**');
    expect(streaming.heightPx).toBeGreaterThanOrEqual(0);
  });

  it('treats assistant replies without an explicit done flag as markdown content', () => {
    const { props, vm } = setupTimeline([
      { id: 'assistant-1', kind: 'assistant', text: '**hello**', ts: '2026-03-07T10:00:00Z' },
    ]);
    expect(vm.renderAssistantBody(props.items[0].text)).toContain('<strong>');
  });

  it('does not emit diagnostic console warnings for completed long assistant content', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { props, vm } = setupTimeline([
      { id: 'assistant-1', kind: 'assistant', text: `# 标题\n\n${leakedProgressText}`, ts: '2026-03-07T10:00:00Z', done: true },
    ]);
    const streaming = vm.streamingAssistantState(props.items[0]);

    expect(streaming.text).toBe(`# 标题\n\n${leakedProgressText}`);
    expect(streaming.heightPx).toBeGreaterThanOrEqual(0);
    expect(warnSpy).not.toHaveBeenCalled();
  });


  it('does not reset the expanded visible window on streaming item updates', async () => {
    const items = Array.from({ length: 150 }, (_, index) => ({
      id: `assistant-${index + 1}`,
      kind: 'assistant',
      text: `chunk-${index + 1}`,
      ts: `2026-03-07T10:${String(index % 60).padStart(2, '0')}:00Z`,
      done: true,
    }));
    const { props, vm } = setupTimeline(items);

    expect(vm.visibleItems.value).toHaveLength(100);
    vm.showMore();
    expect(vm.visibleItems.value).toHaveLength(150);

    props.items = items.map((item, index) => (index === items.length - 1 ? { ...item, text: 'chunk-updated' } : item));
    await nextTick();

    expect(vm.visibleItems.value).toHaveLength(150);
    expect(vm.visibleItems.value.at(-1)?.text).toBe('chunk-updated');
  });
});
