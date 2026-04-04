// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const hooks = vi.hoisted(() => ({ mounted: [], unmounted: [] }));
const lifecycle = vi.hoisted(() => ({
  currentCitationNode: null,
  terminalTargetId: '',
  onAttachmentLightboxKeydown: vi.fn(),
  streamingDispose: vi.fn(),
  renderMarkdown: vi.fn((text) => '<p>' + text + '</p>'),
}));

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    ...actual,
    onMounted: (fn) => hooks.mounted.push(fn),
    onBeforeUnmount: (fn) => hooks.unmounted.push(fn),
  };
});

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logError: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(async () => ({ ok: true })),
}));

vi.mock('./utils/assistant-markdown.js', () => ({
  renderAssistantMarkdown: lifecycle.renderMarkdown,
  injectSentenceBreaks: vi.fn((text) => text),
}));

vi.mock('./utils/assistant-markdown-streaming.js', () => ({
  createStreamingMarkdownStateResolver: vi.fn(() => {
    const resolver = () => ({ text: '', heightPx: 0 });
    resolver.dispose = lifecycle.streamingDispose;
    return resolver;
  }),
}));

vi.mock('./composables/useMermaidRenderer.js', () => ({
  useMermaidRenderer: vi.fn(),
}));

vi.mock('./components/timeline/useAttachmentPreviewState.ts', async () => {
  const { ref } = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    useAttachmentPreviewState: () => ({
      attachmentType: () => '',
      attachmentPreview: () => '',
      attachmentLabel: () => '',
      imageAttachments: () => [],
      fileAttachments: () => [],
      onAttachmentHoverMove: vi.fn(),
      onAttachmentHoverLeave: vi.fn(),
      onAttachmentPreviewEnter: vi.fn(),
      onAttachmentPreviewLeave: vi.fn(),
      onAttachmentPreviewZoomIn: vi.fn(),
      onAttachmentPreviewZoomOut: vi.fn(),
      onAttachmentPreviewResetZoom: vi.fn(),
      attachmentCanZoomOut: () => false,
      attachmentHoverStyle: ref({}),
      attachmentHoverPreview: ref(null),
      attachmentLightbox: ref(null),
      openAttachmentLightbox: vi.fn(),
      closeAttachmentLightbox: vi.fn(),
      onAttachmentLightboxKeydown: lifecycle.onAttachmentLightboxKeydown,
    }),
  };
});

vi.mock('./components/timeline/timeline-markdown-helpers.js', () => ({
  describeClickNode: vi.fn(() => 'node'),
  logRenderedFileRefPaths: vi.fn(),
  resolveCitationNode: vi.fn(() => lifecycle.currentCitationNode),
  resolveFileRefNode: vi.fn(() => null),
  resolveTerminalCitationTargetId: vi.fn(() => lifecycle.terminalTargetId),
  scrollToTimelineItem: vi.fn(),
  whitespaceMeta: vi.fn((value) => ({ raw: value })),
}));

import { reactive } from '../lib/vue.esm-browser.prod.js';
import { ChatTimeline } from './components/ChatTimeline.js';

function setupTimeline(overrides = {}, emit = vi.fn()) {
  const props = reactive({
    items: overrides.items ?? [],
    activeStatus: overrides.activeStatus ?? 'idle',
    activeStatusText: overrides.activeStatusText ?? '',
    activeStatusMeta: overrides.activeStatusMeta ?? '',
    pinnedPlanVisible: overrides.pinnedPlanVisible ?? false,
    pinnedPlanItemId: overrides.pinnedPlanItemId ?? null,
    resolveThreadDisplayName: overrides.resolveThreadDisplayName ?? null,
    presenceTarget: overrides.presenceTarget ?? null,
  });
  const vm = ChatTimeline.setup(props, { emit });
  return { props, vm, emit };
}

beforeEach(() => {
  hooks.mounted.length = 0;
  hooks.unmounted.length = 0;
  lifecycle.currentCitationNode = null;
  lifecycle.terminalTargetId = '';
  lifecycle.onAttachmentLightboxKeydown.mockReset();
  lifecycle.streamingDispose.mockReset();
  lifecycle.renderMarkdown.mockReset().mockImplementation((text) => '<p>' + text + '</p>');
  vi.stubGlobal('window', {
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    isSecureContext: true,
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('ChatTimeline host lifecycle guards', () => {
  it('registers and unregisters the host keydown listener symmetrically', () => {
    setupTimeline();

    expect(hooks.mounted).toHaveLength(1);
    expect(hooks.unmounted.length).toBeGreaterThan(0);

    hooks.mounted.forEach((fn) => fn());
    expect(globalThis.window.addEventListener).toHaveBeenCalledWith('keydown', lifecycle.onAttachmentLightboxKeydown);

    hooks.unmounted.forEach((fn) => fn());
    expect(globalThis.window.removeEventListener).toHaveBeenCalledWith('keydown', lifecycle.onAttachmentLightboxKeydown);
    expect(lifecycle.streamingDispose).toHaveBeenCalledTimes(1);
  });

  it('does not attach a timeline-level mutation observer during mount', () => {
    const observe = vi.fn();
    const disconnect = vi.fn();
    const MutationObserverMock = vi.fn(function MutationObserverMock() {
      this.observe = observe;
      this.disconnect = disconnect;
    });
    vi.stubGlobal('MutationObserver', MutationObserverMock);

    setupTimeline();
    hooks.mounted.forEach((fn) => fn());

    expect(MutationObserverMock).not.toHaveBeenCalled();
    expect(observe).not.toHaveBeenCalled();
    expect(disconnect).not.toHaveBeenCalled();
  });

  it('cleans pending popover and citation timers during unmount', () => {
    vi.useFakeTimers();
    vi.stubGlobal('requestAnimationFrame', (cb) => {
      cb();
      return 1;
    });
    lifecycle.terminalTargetId = 'cmd-1';
    lifecycle.currentCitationNode = {
      getAttribute: vi.fn((name) => ({
        'data-citation-kind': 'terminal',
        'data-terminal-chunk-id': 'chunk-1',
        'data-line-start': '2',
        'data-line-end': '4',
      }[name] || '')),
      textContent: 'Terminal output',
    };

    const { vm } = setupTimeline({
      activeStatus: 'thinking',
      activeStatusText: '分析中',
      items: [
        { id: 'assistant-1', kind: 'assistant', text: '收到', done: true },
        { id: 'thinking-1', kind: 'thinking', text: '检查错误', done: false },
        { id: 'cmd-1', kind: 'command', terminal_chunk_id: 'chunk-1', output: 'ok' },
      ],
    }, vi.fn());

    hooks.mounted.forEach((fn) => fn());
    vm.openPresencePopover();
    vm.schedulePresencePopoverClose();
    expect(vi.getTimerCount()).toBe(1);

    vm.onAssistantBodyClick({
      target: {},
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });
    expect(vm.isCitationTarget({ id: 'cmd-1' })).toBe(true);
    expect(vi.getTimerCount()).toBeGreaterThanOrEqual(2);

    hooks.unmounted.forEach((fn) => fn());
    expect(vi.getTimerCount()).toBe(0);
    expect(lifecycle.streamingDispose).toHaveBeenCalledTimes(1);
    expect(globalThis.window.removeEventListener).toHaveBeenCalledWith('keydown', lifecycle.onAttachmentLightboxKeydown);
  });

  it('keeps terminal citation misses free of stale highlight state', () => {
    lifecycle.terminalTargetId = '';
    lifecycle.currentCitationNode = {
      getAttribute: vi.fn((name) => ({
        'data-citation-kind': 'terminal',
        'data-terminal-chunk-id': 'chunk-missing',
        'data-line-start': '1',
        'data-line-end': '2',
      }[name] || '')),
      textContent: 'Missing terminal output',
    };
    const emit = vi.fn();
    const { vm } = setupTimeline({
      items: [{ id: 'cmd-1', kind: 'command', terminal_chunk_id: 'chunk-1', output: 'ok' }],
    }, emit);

    vm.onAssistantBodyClick({
      target: {},
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(vm.isCitationTarget({ id: 'cmd-1' })).toBe(false);
    expect(emit).toHaveBeenCalledWith('citation-click', {
      kind: 'terminal',
      chunkId: 'chunk-missing',
      lineStart: 1,
      lineEnd: 2,
      raw: 'Missing terminal output',
    });
  });
});
