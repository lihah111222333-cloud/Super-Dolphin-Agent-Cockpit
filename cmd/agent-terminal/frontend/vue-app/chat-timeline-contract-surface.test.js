// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

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

function setupTimeline(overrides = {}, emit = vi.fn()) {
  return ChatTimeline.setup(
    {
      items: overrides.items ?? [],
      activeStatus: overrides.activeStatus ?? 'idle',
      activeStatusText: overrides.activeStatusText ?? '',
      activeStatusMeta: overrides.activeStatusMeta ?? '',
      emptyText: overrides.emptyText ?? '暂无消息，先发送一句话试试。',
      pinnedPlanVisible: overrides.pinnedPlanVisible ?? false,
      pinnedPlanItemId: overrides.pinnedPlanItemId ?? null,
      resolveThreadDisplayName: overrides.resolveThreadDisplayName ?? null,
      presenceTarget: overrides.presenceTarget ?? null,
    },
    { emit },
  );
}

describe('ChatTimeline contract surface', () => {
  it('locks props and emits surface', () => {
    expect(Object.keys(ChatTimeline.props)).toEqual([
      'items',
      'activeStatus',
      'activeStatusText',
      'activeStatusMeta',
      'emptyText',
      'pinnedPlanVisible',
      'pinnedPlanItemId',
      'resolveThreadDisplayName',
      'presenceTarget',
    ]);
    expect(ChatTimeline.props.items.default()).toEqual([]);
    expect(ChatTimeline.props.activeStatus.default).toBe('idle');
    expect(ChatTimeline.props.activeStatusText.default).toBe('');
    expect(ChatTimeline.props.activeStatusMeta.default).toBe('');
    expect(ChatTimeline.props.emptyText.default).toBe('暂无消息，先发送一句话试试。');
    expect(ChatTimeline.props.pinnedPlanVisible.default).toBe(false);
    expect(ChatTimeline.props.pinnedPlanItemId.default).toBe(null);
    expect(ChatTimeline.props.resolveThreadDisplayName.default).toBe(null);
    expect(ChatTimeline.props.presenceTarget.default).toBe(null);
    expect(ChatTimeline.emits).toEqual(['file-ref-click', 'citation-click']);
  });

  it('locks delegated setup helper surfaces and forwarding handlers', () => {
    const emit = vi.fn();
    const vm = setupTimeline({}, emit);

    expect(Object.keys(vm.attachmentPreviewApi).sort()).toEqual([
      'attachmentLabel',
      'attachmentPreview',
      'attachmentType',
      'fileAttachments',
      'imageAttachments',
      'onAttachmentHoverLeave',
      'onAttachmentHoverMove',
      'openAttachmentLightbox',
    ]);
    expect(Object.keys(vm.jsonRenderMarkdownActionHandlers).sort()).toEqual(['onCitationClick', 'onFileRefClick']);
    expect(typeof vm.translateText).toBe('function');

    const filePayload = { path: 'src/main.go', line: 7, column: 3, raw: 'src/main.go:7' };
    const citationPayload = { kind: 'task', title: 'Review', prompt: 'Review patch', raw: 'Review' };
    vm.jsonRenderMarkdownActionHandlers.onFileRefClick(filePayload);
    vm.jsonRenderMarkdownActionHandlers.onCitationClick(citationPayload);

    expect(emit).toHaveBeenNthCalledWith(1, 'file-ref-click', filePayload);
    expect(emit).toHaveBeenNthCalledWith(2, 'citation-click', citationPayload);
  });

  it('locks template class and child-binding contracts', () => {
    const template = ChatTimeline.template;

    expect(template).toContain(":attachment-api=\"attachmentPreviewApi\"");
    expect(template).toContain(":markdown-action-handlers=\"jsonRenderMarkdownActionHandlers\"");
    expect(template).toContain('has-plan-pin');
    expect(template).toContain('{{ emptyText }}');
    expect(template).toContain('is-citation-target');
    expect(template).toContain('chat-presence-row--anchored');
    expect(template).toContain('chat-status-presence--popoverable');
    expect(template).toContain('chat-status-presence--popover-open');
    expect(template).toContain('internalRouteLabel(item)');
    expect(template).toContain("activeStatus === 'thinking' || activeStatus === 'starting' || activeStatus === 'running' || activeStatus === 'responding'");
    expect(template).toContain('loading-shimmer');
  });
});
