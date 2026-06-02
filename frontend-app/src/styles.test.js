import { readFileSync } from 'node:fs';
import path from 'node:path';
import { cwd } from 'node:process';
import postcss from 'postcss';
import { describe, expect, it } from 'vitest';

const css = readFileSync(path.join(cwd(), 'src/styles.css'), 'utf8');
const root = postcss.parse(css);

function splitSelectors(selector) {
  const selectors = [];
  let current = '';
  let depth = 0;

  for (const char of selector) {
    if (char === '(') depth += 1;
    if (char === ')') depth = Math.max(0, depth - 1);
    if (char === ',' && depth === 0) {
      selectors.push(current.trim());
      current = '';
      continue;
    }
    current += char;
  }

  if (current.trim()) selectors.push(current.trim());
  return selectors;
}

function declarationsFor(selector) {
  const declarations = {};
  root.walkRules((rule) => {
    const selectors = splitSelectors(rule.selector);
    if (!selectors.includes(selector)) return;
    rule.walkDecls((decl) => {
      declarations[decl.prop] = decl.value;
    });
  });
  return declarations;
}

function firstDeclarationsFor(selector) {
  const declarations = {};
  let found = false;
  root.walkRules(selector, (rule) => {
    if (found) return;
    found = true;
    rule.walkDecls((decl) => {
      declarations[decl.prop] = decl.value;
    });
  });
  return declarations;
}

function mediaDeclarationsFor(mediaParams, selector) {
  const matches = [];
  root.walkAtRules('media', (atRule) => {
    if (atRule.params !== mediaParams) return;
    atRule.walkRules((rule) => {
      const selectors = splitSelectors(rule.selector);
      if (!selectors.includes(selector)) return;
      const declarations = {};
      rule.walkDecls((decl) => {
        declarations[decl.prop] = decl.value;
      });
      matches.push(declarations);
    });
  });
  return matches;
}

describe('composer layout styles', () => {
  it('does not draw a separator line between the composer textarea and controls', () => {
    const textarea = declarationsFor('.composer textarea');

    expect(textarea['border-bottom']).toBe('0');
  });

  it('keeps the composer textarea within a three-to-eight-row range', () => {
    const textarea = declarationsFor('.composer textarea');

    expect(textarea['line-height']).toBe('1.5');
    expect(textarea['min-height']).toBe('calc(1.5em * 3 + 34px)');
    expect(textarea['max-height']).toBe('calc(1.5em * 8 + 34px)');
    expect(textarea['overflow-y']).toBe('auto');
  });

  it('keeps composer permission and send controls aligned with the shell theme', () => {
    const permission = declarationsFor('.permission-chip select');
    const sendIcon = declarationsFor('.composer .send svg');

    expect(permission['border-color']).toBe('var(--border-strong)');
    expect(permission.background).toBe('color-mix(in srgb, var(--surface-2) 88%, var(--bg))');
    expect(permission.color).toBe('var(--text-sec)');
    expect(sendIcon.transform).toBe('rotate(-90deg)');
    expect(sendIcon['transform-origin']).toBe('50% 50%');
  });

  it('keeps app rail and agent list icons on consistent fixed sizes', () => {
    const navIcon = declarationsFor('.nav-rail button svg');
    const threadToolIcon = declarationsFor('.thread-tools svg');
    const threadCardIcon = declarationsFor('.thread-card svg');
    const providerBadge = declarationsFor('.thread-card b');
    const statusLine = declarationsFor('.thread-card em');

    expect(navIcon.width).toBe('20px');
    expect(navIcon.height).toBe('20px');
    expect(navIcon['flex-shrink']).toBe('0');
    expect(threadToolIcon.width).toBe('16px');
    expect(threadToolIcon.height).toBe('16px');
    expect(threadCardIcon.width).toBe('16px');
    expect(threadCardIcon.height).toBe('16px');
    expect(providerBadge.display).toBe('inline-flex');
    expect(providerBadge['min-height']).toBe('22px');
    expect(providerBadge['min-width']).toBe('52px');
    expect(providerBadge['font-size']).toBe('12px');
    expect(providerBadge['line-height']).toBe('1');
    expect(statusLine.display).toBe('inline-flex');
    expect(statusLine['font-size']).toBe('12px');
  });

  it('keeps runtime panel details shrink-safe inside the right rail', () => {
    const panel = declarationsFor('.runtime-panel');
    const icons = declarationsFor('.runtime-icons');
    const tooltipRow = declarationsFor('.runtime-stat-tooltip-row');
    const tooltipName = declarationsFor('.runtime-stat-tooltip-name');
    const logLine = declarationsFor('.warning-log-line');

    expect(panel['border-left']).toBe('1px solid var(--line)');
    expect(panel['overflow-x']).toBe('hidden');
    expect(icons['min-width']).toBe('0');
    expect(tooltipRow['min-width']).toBe('0');
    expect(tooltipName['min-width']).toBe('0');
    expect(tooltipName.overflow).toBe('hidden');
    expect(tooltipName['text-overflow']).toBe('ellipsis');
    expect(logLine['min-width']).toBe('0');
    expect(logLine.overflow).toBe('hidden');
    expect(logLine['text-overflow']).toBe('ellipsis');
  });

  it('uses theme-aware colors for skill filter active buttons', () => {
    const active = declarationsFor('.skill-filter .active');

    expect(active.background).toBe('color-mix(in srgb, var(--accent-2) 16%, var(--surface-3))');
    expect(active.color).toBe('var(--text-pri)');
    expect(active['border-color']).toBe('var(--border-strong)');
    expect(active.background).not.toBe('#4d4f55');
  });

  it('shows a readable generated-image fallback instead of a bare broken image icon', () => {
    const fallback = declarationsFor('.message-image-fallback');
    const fallbackCode = declarationsFor('.message-image-fallback code');

    expect(fallback.display).toBe('inline-grid');
    expect(fallback.background).toBe('var(--surface-2)');
    expect(fallback.color).toBe('var(--text-sec)');
    expect(fallbackCode.overflow).toBe('hidden');
    expect(fallbackCode['text-overflow']).toBe('ellipsis');
  });

  it('styles generated image previews with an enlarge affordance', () => {
    const preview = declarationsFor('.message-image-preview');
    const hint = declarationsFor('.message-image-preview span');
    const lightbox = declarationsFor('.image-lightbox');
    const panel = declarationsFor('.image-lightbox-panel');

    expect(preview.cursor).toBe('zoom-in');
    expect(preview.background).toBe('transparent');
    expect(hint.opacity).toBe('0');
    expect(lightbox.position).toBe('fixed');
    expect(lightbox['z-index']).toBe('80');
    expect(panel.width).toBe('min(1180px, 94vw)');
    expect(panel['max-height']).toBe('92vh');
  });

  it('styles mermaid diagrams as bounded readable timeline content', () => {
    const diagram = declarationsFor('.mermaid-diagram');
    const preview = declarationsFor('.mermaid-diagram-preview');
    const hint = declarationsFor('.mermaid-diagram-preview span');
    const lightboxSvg = declarationsFor('.mermaid-lightbox-svg');
    const svg = declarationsFor('.mermaid-diagram svg');

    expect(diagram['max-width']).toBe('100%');
    expect(diagram.overflow).toBe('auto');
    expect(diagram.background).toBe('var(--surface)');
    expect(preview.cursor).toBe('zoom-in');
    expect(preview.background).toBe('transparent');
    expect(hint.opacity).toBe('0');
    expect(lightboxSvg.overflow).toBe('auto');
    expect(svg.display).toBe('block');
    expect(svg['max-width']).toBe('100%');
  });

  it('styles AI reasoning traces with theme tokens and shrink-safe text', () => {
    const message = declarationsFor('.reasoning-message');
    const adjacentMessage = declarationsFor('.reasoning-message + .reasoning-message');
    const openMessage = declarationsFor('.reasoning-message:has(.reasoning-trace[open])');
    const trace = declarationsFor('.reasoning-trace');
    const summary = declarationsFor('.reasoning-trace summary');
    const summaryMeta = declarationsFor('.reasoning-trace summary em');
    const step = declarationsFor('.reasoning-step');
    const stepList = declarationsFor('.reasoning-step-list');
    const openStepList = declarationsFor('.reasoning-trace[open] .reasoning-step-list');
    const stepOutput = declarationsFor('.reasoning-step-body .message-output');
    const stepOutputPre = declarationsFor('.reasoning-step-body .message-output pre');
    const stepOutputCode = declarationsFor('.reasoning-step-body .message-output code');
    const stepMarkdown = declarationsFor('.reasoning-step-body .message-markdown');
    const stepMarkdownPre = declarationsFor('.reasoning-step-body .message-markdown pre');
    const stepMarkdownCode = declarationsFor('.reasoning-step-body .message-markdown pre code');
    const stepTitle = declarationsFor('.reasoning-step-body header strong');

    expect(message['box-sizing']).toBe('border-box');
    expect(message.display).toBe('flow-root');
    expect(message.flex).toBe('0 0 auto');
    expect(message.width).toBe('min(720px, var(--conversation-content-width))');
    expect(message['max-width']).toBe('min(720px, var(--conversation-content-width))');
    expect(message.margin).toBe('4px var(--conversation-content-right-gutter) 4px var(--conversation-content-left-gutter)');
    expect(message['min-height']).toBe('30px');
    expect(message.padding).toBe('0 0 0 66px');
    expect(openMessage['margin-bottom']).toBe('8px');
    expect(adjacentMessage['margin-top']).toBe('4px');
    expect(trace.display).toBe('block');
    expect(trace.margin).toBe('0');
    expect(trace.width).toBe('100%');
    expect(trace.border).toBe('0');
    expect(trace.background).toBe('transparent');
    expect(trace.padding).toBe('0');
    expect(trace['box-shadow']).toBe('none');
    expect(trace.color).toBe('var(--text-sec)');
    expect(summary.display).toBe('flex');
    expect(summary.width).toBe('fit-content');
    expect(summary['max-width']).toBe('100%');
    expect(summary['min-height']).toBe('30px');
    expect(summary['font-size']).toBe('14px');
    expect(summary['line-height']).toBe('1.25');
    expect(summary.gap).toBe('6px');
    expect(summary.padding).toBe('0');
    expect(summaryMeta['min-width']).toBe('0');
    expect(summaryMeta.overflow).toBe('hidden');
    expect(summaryMeta['text-overflow']).toBe('ellipsis');
    expect(step.display).toBe('grid');
    expect(step['grid-template-columns']).toBe('18px minmax(0, 1fr)');
    expect(step.gap).toBe('6px');
    expect(stepList.resize).toBe('vertical');
    expect(stepList.overflow).toBe('auto');
    expect(stepList.padding).toBe('8px 10px');
    expect(stepList['max-height']).toBe('min(240px, 34vh)');
    expect(openStepList.border).toBe('1px solid var(--border)');
    expect(openStepList['box-sizing']).toBe('border-box');
    expect(openStepList.width).toBe('100%');
    expect(stepMarkdown['font-size']).toBe('14px');
    expect(stepMarkdown['line-height']).toBe('1.42');
    expect(stepMarkdownPre.margin).toBe('4px 0 0');
    expect(stepMarkdownPre.padding).toBe('7px 9px');
    expect(stepMarkdownCode.font).toBe('14px/1.42 ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace');
    expect(stepOutput['font-size']).toBe('14px');
    expect(stepOutput['line-height']).toBe('1.42');
    expect(stepOutputPre.margin).toBe('4px 0 0');
    expect(stepOutputPre.padding).toBe('7px 9px');
    expect(stepOutputCode.font).toBe('14px/1.42 ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace');
    expect(stepTitle['text-overflow']).toBe('ellipsis');
    expect(stepTitle['font-size']).toBe('14px');
  });

  it('keeps assistant message content compact instead of spanning the full timeline', () => {
    const message = declarationsFor('.message');
    const assistantBubble = declarationsFor('.message.assistant .bubble');
    const markdown = declarationsFor('.message-markdown');

    expect(message.margin).toBe('18px var(--conversation-content-right-gutter) 18px var(--conversation-content-left-gutter)');
    expect(assistantBubble['max-width']).toBe('min(760px, 100%)');
    expect(assistantBubble.background).toBe('transparent');
    expect(markdown['font-size']).toBe('14px');
    expect(markdown['line-height']).toBe('1.62');
  });

  it('styles assistant message copy actions as low-noise controls', () => {
    const actions = declarationsFor('.message-actions');
    const button = declarationsFor('.message-copy');
    const copied = declarationsFor('.message-copy.is-copied');

    expect(actions.display).toBe('flex');
    expect(button['min-height']).toBe('34px');
    expect(button['border-radius']).toBe('999px');
    expect(button.background).toBe('transparent');
    expect(copied.color).toBe('var(--success)');
  });

  it('does not override the computed chat grid at medium widths', () => {
    const mediumChatLayouts = mediaDeclarationsFor('(max-width: 1280px)', '.chat-layout');
    const mediumRuntimePanels = mediaDeclarationsFor('(max-width: 1280px)', '.runtime-panel');
    const mediumRightSplitters = mediaDeclarationsFor('(max-width: 1280px)', '.splitter--right');

    for (const declarations of mediumChatLayouts) {
      expect(declarations['grid-template-columns']).toBeUndefined();
    }
    for (const declarations of [...mediumRuntimePanels, ...mediumRightSplitters]) {
      expect(declarations.display).not.toBe('none');
    }
    expect(css).not.toContain('280px minmax(0, 1fr) !important');
    expect(css).not.toContain('.chat-layout {\n    grid-template-columns');
  });

  it('prevents long chat content from widening the conversation grid', () => {
    const conversation = declarationsFor('.conversation');
    const timeline = declarationsFor('.timeline');
    const message = declarationsFor('.message');
    const bubble = declarationsFor('.bubble');
    const status = declarationsFor('.work-status');
    const composer = declarationsFor('.composer');

    expect(conversation['min-width']).toBe('0');
    expect(conversation.overflow).toBe('hidden');
    expect(timeline['min-width']).toBe('0');
    expect(timeline['max-width']).toBe('100%');
    expect(message['min-width']).toBe('0');
    expect(bubble['min-width']).toBe('0');
    expect(bubble['max-width']).toBe('100%');
    expect(status['min-width']).toBe('0');
    expect(status['max-width']).toBe('var(--conversation-content-width)');
    expect(composer['min-width']).toBe('0');
    expect(composer['max-width']).toBe('100%');
  });

  it('keeps the work-status token chip from being squeezed by long status text', () => {
    const status = declarationsFor('.work-status');
    const detail = declarationsFor('.work-status em');
    const token = declarationsFor('.work-status code');

    expect(status['grid-template-columns']).toBe('auto auto minmax(0, 1fr) auto');
    expect(detail['min-width']).toBe('0');
    expect(detail.overflow).toBe('hidden');
    expect(token['justify-self']).toBe('end');
    expect(token['white-space']).toBe('nowrap');
    expect(token.overflow).toBe('visible');
  });

  it('keeps timeline messages, status, and docked composer in one left-biased content column', () => {
    const conversation = declarationsFor('.conversation');
    const timeline = declarationsFor('.timeline');
    const message = declarationsFor('.message');
    const userMessage = declarationsFor('.message.user');
    const userBubble = declarationsFor('.message.user .bubble');
    const status = declarationsFor('.work-status');
    const composerCard = declarationsFor('.composer-card');

    expect(conversation['--conversation-content-width']).toBe('min(1040px, calc(100% - 56px))');
    expect(conversation['--conversation-content-left-nudge']).toBe('clamp(48px, 6vw, 112px)');
    expect(conversation['--conversation-content-left-gutter']).toContain(
      'calc((100% - var(--conversation-content-width)) / 2 - var(--conversation-content-left-nudge))',
    );
    expect(conversation['--conversation-content-right-gutter']).toContain(
      '100% - var(--conversation-content-width) - var(--conversation-content-left-gutter)',
    );
    expect(timeline.display).toBe('flex');
    expect(timeline['flex-direction']).toBe('column');
    expect(timeline['align-items']).toBe('flex-start');
    expect(message.width).toBe('var(--conversation-content-width)');
    expect(message['max-width']).toBe('var(--conversation-content-width)');
    expect(message.margin).toBe(
      '18px var(--conversation-content-right-gutter) 18px var(--conversation-content-left-gutter)',
    );
    expect(userMessage['margin-left']).toBeUndefined();
    expect(userMessage.width).toBe('var(--conversation-content-width)');
    expect(userBubble['margin-left']).toBe('auto');
    expect(status.width).toBe('var(--conversation-content-width)');
    expect(status['max-width']).toBe('var(--conversation-content-width)');
    expect(status['justify-self']).toBe('start');
    expect(status['margin-left']).toBe('var(--conversation-content-left-gutter)');
    expect(composerCard.width).toBe('var(--conversation-content-width)');
    expect(composerCard.margin).toBe(
      '0 var(--conversation-content-right-gutter) 0 var(--conversation-content-left-gutter)',
    );
  });

  it('keeps the new-chat intro stage centered and full width', () => {
    const introConversation = declarationsFor('.conversation--intro');
    const introTimeline = declarationsFor('.conversation--intro .timeline');
    const introStage = declarationsFor('.intro-chat-stage');
    const floatingComposer = declarationsFor('.conversation--intro .composer--floating');
    const floatingCard = declarationsFor('.composer--floating .composer-card');

    expect(introConversation['--conversation-intro-width']).toBe('min(880px, calc(100% - 96px))');
    expect(introConversation['--conversation-content-width']).toBe('var(--conversation-intro-width)');
    expect(introConversation['--conversation-content-left-nudge']).toBe('0px');
    expect(introTimeline['align-items']).toBe('center');
    expect(introStage.width).toBe('var(--conversation-intro-width)');
    expect(floatingComposer.width).toBe('100%');
    expect(floatingComposer['max-width']).toBe('100%');
    expect(floatingCard.width).toBe('100%');
    expect(floatingCard.margin).toBe('0 auto');
  });

  it('keeps the light-mode new-chat composer on matching theme surfaces', () => {
    const floatingCard = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer--floating .composer-card');
    const attach = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer--floating .composer-attach');
    const permission = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer--floating .permission-chip select');
    const track = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer--floating .provider-track');

    expect(floatingCard.background).toContain('var(--surface)');
    expect(floatingCard['border-color']).toContain('var(--border-strong)');
    expect(floatingCard['box-shadow']).toContain('rgba(42, 54, 88, 0.14)');
    expect(attach.background).toBe('color-mix(in srgb, var(--surface-2) 86%, var(--surface))');
    expect(permission.background).toBe('color-mix(in srgb, var(--surface-2) 86%, var(--surface))');
    expect(track.background).toBe('color-mix(in srgb, var(--surface-3) 72%, var(--border))');
  });

  it('keeps the collapsed project selector short while allowing a wider project menu', () => {
    const select = declarationsFor('.top-command .project-select');
    const dropdown = declarationsFor('.top-command .project-dropdown');

    expect(select.width).toBe('fit-content');
    expect(select['max-width']).toBe('min(220px, 34vw)');
    expect(dropdown.width).toBe('max-content');
    expect(dropdown['min-width']).toBe('360px');
    expect(dropdown['max-width']).toBe('min(520px, 86vw)');
  });

  it('keeps the right sidebar toggle docked to the page edge', () => {
    const toggle = declarationsFor('.top-command .sidebar-toggle');

    expect(toggle['margin-left']).toBe('auto');
    expect(toggle.display).toBe('inline-flex');
  });

  it('lets the model selector popover escape the adaptive composer card', () => {
    const card = declarationsFor('.composer-card');
    const wrap = declarationsFor('.composer-model-wrap');
    const dropdown = declarationsFor('.model-dropdown');

    expect(card.overflow).toBe('visible');
    expect(wrap.position).toBe('relative');
    expect(wrap.width).toBe('158px');
    expect(dropdown.position).toBe('absolute');
    expect(dropdown.bottom).toBe('calc(100% + 8px)');
  });

  it('keeps attachment and permission left while model controls sit on the right', () => {
    const meta = firstDeclarationsFor('.composer-meta');
    const actions = firstDeclarationsFor('.composer-actions');
    const provider = firstDeclarationsFor('.composer .provider');

    expect(meta['align-items']).toBe('center');
    expect(meta.gap).toBe('12px');
    expect(actions['margin-left']).toBe('auto');
    expect(actions['justify-content']).toBe('flex-end');
    expect(actions['padding-left']).toBe('18px');
    expect(provider['margin-left']).toBe('0');
  });

  it('renders the provider toggle as a sliding pill control', () => {
    const provider = declarationsFor('.provider');
    const composerProvider = declarationsFor('.composer .provider');
    const track = declarationsFor('.provider-track');
    const thumb = declarationsFor('.provider-thumb');
    const activeThumb = declarationsFor('.provider.active .provider-thumb');
    const label = declarationsFor('.provider-label');

    expect(provider.width).toBe('112px');
    expect(provider.background).toBe('transparent');
    expect(provider['border-color']).toBe('transparent');
    expect(provider['border-radius']).toBe('999px');
    expect(composerProvider['border-color']).toBe('transparent');
    expect(composerProvider.background).toBe('transparent');
    expect(track.width).toBe('43px');
    expect(track.height).toBe('22px');
    expect(track.background).toBe('#2a2a2b');
    expect(thumb.width).toBe('16px');
    expect(thumb.height).toBe('16px');
    expect(label.width).toBe('52px');
    expect(activeThumb.transform).toBe('translateX(19px)');
  });
});

describe('runtime activity panel styles', () => {
  it('lets activity popovers render above the code diff panel', () => {
    const panel = declarationsFor('.runtime-panel');
    const activity = declarationsFor('.runtime-activity-panel');
    const diff = declarationsFor('.diff-empty');
    const tooltip = declarationsFor('.runtime-stat-tooltip');

    expect(panel['overflow-x']).toBe('hidden');
    expect(panel['overflow-y']).toBe('visible');
    expect(panel['grid-template-rows']).toContain('var(--activity-panel-height)');
    expect(activity.overflow).toBe('visible');
    expect(activity.height).toBe('var(--activity-panel-height)');
    expect(Number(activity['z-index'])).toBeGreaterThan(Number(diff['z-index']));
    expect(tooltip.position).toBe('fixed');
    expect(tooltip.left).toBe('var(--runtime-stat-tooltip-left, 12px)');
    expect(tooltip['max-height']).toBe('var(--runtime-stat-tooltip-max-height, min(280px, 42vh))');
    expect(Number(tooltip['z-index'])).toBeGreaterThan(Number(activity['z-index']));
  });

  it('keeps resized runtime sidebar content visible instead of requiring horizontal scrolling', () => {
    const toolbar = declarationsFor('.runtime-toolbar');
    const toolbarButton = declarationsFor('.runtime-toolbar button');
    const score = declarationsFor('.score');
    const goodScore = declarationsFor('.score.good');
    const diffView = declarationsFor('.diff-view');
    const diffFileGroup = declarationsFor('.diff-file-group');
    const diffFileToggle = declarationsFor('.diff-file-toggle');
    const diffFileCaret = declarationsFor('.diff-file-caret');
    const diffFileStats = declarationsFor('.diff-file-stats');
    const diffFileLines = declarationsFor('.diff-file-lines');
    const diffLine = declarationsFor('.diff-line');
    const diffContent = declarationsFor('.diff-line-content');
    const icons = declarationsFor('.runtime-icons');
    const stat = declarationsFor('.runtime-stat');

    expect(toolbar['min-width']).toBe('0');
    expect(toolbar.display).toBe('grid');
    expect(toolbar['grid-template-columns']).toBe('repeat(2, minmax(0, 1fr))');
    expect(toolbar['align-content']).toBe('center');
    expect(toolbar['overflow']).toBe('hidden');
    expect(toolbarButton['min-width']).toBe('0');
    expect(toolbarButton['justify-content']).toBe('center');
    expect(score['min-width']).toBe('0');
    expect(score['justify-content']).toBe('center');
    expect(goodScore['margin-left']).toBe('0');
    expect(diffView.display).toBe('grid');
    expect(diffFileGroup.overflow).toBe('hidden');
    expect(diffFileToggle['min-width']).toBe('0');
    expect(diffFileToggle['grid-template-columns']).toBe('minmax(0, 1fr)');
    expect(diffFileCaret.width).toBe('14px');
    expect(diffFileCaret.height).toBe('14px');
    expect(diffFileCaret['flex-shrink']).toBe('0');
    expect(diffFileStats['justify-content']).toBe('flex-end');
    expect(diffFileLines.overflow).toBe('hidden');
    expect(diffLine['grid-template-columns']).toBe('42px 42px 14px minmax(0, 1fr)');
    expect(diffContent['white-space']).toBe('pre-wrap');
    expect(diffContent['overflow-wrap']).toBe('anywhere');
    expect(icons['min-width']).toBe('0');
    expect(icons['display']).toBe('grid');
    expect(icons['grid-template-columns']).toBe('repeat(4, minmax(0, 1fr))');
    expect(icons['overflow']).toBe('hidden');
    expect(stat['min-width']).toBe('0');
    expect(stat['justify-content']).toBe('center');
  });

  it('truncates long runtime tool names without widening the tooltip', () => {
    const toolName = declarationsFor('.runtime-stat-tooltip-name');

    expect(toolName['min-width']).toBe('0');
    expect(toolName.overflow).toBe('hidden');
    expect(toolName['overflow-wrap']).toBe('anywhere');
    expect(toolName['text-overflow']).toBe('ellipsis');
    expect(toolName['white-space']).toBe('nowrap');
  });

  it('keeps warning log details inside hover popovers', () => {
    const line = declarationsFor('.warning-log-line');
    const popover = declarationsFor('.warning-log-popover');
    const code = declarationsFor('.warning-log-popover code');

    expect(line['white-space']).toBe('nowrap');
    expect(popover.position).toBe('fixed');
    expect(popover['box-sizing']).toBe('border-box');
    expect(popover['min-width']).toBe('0');
    expect(popover.left).toBe('var(--warning-log-popover-left, 12px)');
    expect(popover.right).toBe('var(--warning-log-popover-right, 12px)');
    expect(popover['pointer-events']).toBe('none');
    expect(Number(popover['z-index'])).toBeGreaterThan(80);
    expect(code.display).toBe('block');
    expect(code['max-width']).toBe('100%');
    expect(code['overflow-wrap']).toBe('anywhere');
    expect(code['word-break']).toBe('break-word');
  });
});

describe('light theme baseline usability', () => {
  it('keeps assistant text and code readable in light mode', () => {
    const lightTheme = declarationsFor('.sa-window[data-theme="light"]');
    const markdown = declarationsFor('.sa-window[data-theme="light"] .message-markdown');
    const assistantMarkdown = declarationsFor('.sa-window[data-theme="light"] .message.assistant .message-markdown');
    const inlineCode = declarationsFor('.sa-window[data-theme="light"] .message-markdown code');
    const codeBlock = declarationsFor('.sa-window[data-theme="light"] .message-markdown pre');

    expect(lightTheme['--bg']).toBe('#f6f8fc');
    expect(lightTheme['--text-sec']).toBe('#2f3a52');
    expect(markdown.color).toBe('var(--text-sec)');
    expect(assistantMarkdown.color).toBe('var(--text-sec)');
    expect(inlineCode.color).toBe('var(--text-pri)');
    expect(inlineCode.background).toBe('var(--surface-3)');
    expect(codeBlock.background).toBe('#f8fafc');
    expect(codeBlock.color).toBe('var(--text-pri)');
  });

  it('uses light surfaces for runtime details instead of the dark console treatment', () => {
    const activity = declarationsFor('.sa-window[data-theme="light"] .runtime-activity-panel');
    const logs = declarationsFor('.sa-window[data-theme="light"] .log-lines');
    const tooltip = declarationsFor('.sa-window[data-theme="light"] .runtime-stat-tooltip');
    const popoverCode = declarationsFor('.sa-window[data-theme="light"] .warning-log-popover code');
    const diffLines = declarationsFor('.sa-window[data-theme="light"] .diff-file-lines');

    expect(activity.background).toBe('var(--surface)');
    expect(activity.color).toBe('var(--text-sec)');
    expect(logs.background).toBe('#f8fafc');
    expect(logs.color).toBe('var(--text-sec)');
    expect(tooltip.background).toBe('var(--surface)');
    expect(tooltip.color).toBe('var(--text-pri)');
    expect(popoverCode.color).toBe('var(--text-sec)');
    expect(diffLines.background).toBe('#f8fafc');
  });

  it('keeps light-mode runtime summary pills on matching surfaces', () => {
    const toolbar = declarationsFor('.sa-window[data-theme="light"] .runtime-toolbar');
    const button = declarationsFor('.sa-window[data-theme="light"] .runtime-toolbar button');
    const score = declarationsFor('.sa-window[data-theme="light"] .score');
    const goodScore = declarationsFor('.sa-window[data-theme="light"] .score.good');
    const badScore = declarationsFor('.sa-window[data-theme="light"] .score.bad');

    expect(toolbar.background).toBe('var(--surface)');
    expect(toolbar['border-bottom-color']).toBe('var(--border)');
    expect(button.background).toBe('var(--surface-2)');
    expect(button.color).toBe('var(--text-sec)');
    expect(button['border-color']).toBe('var(--border)');
    expect(score.background).toBe('var(--surface-2)');
    expect(score.color).toBe('var(--text-sec)');
    expect(goodScore.background).toBe('color-mix(in srgb, var(--success) 9%, var(--surface))');
    expect(badScore.background).toBe('color-mix(in srgb, var(--error) 8%, var(--surface))');
  });

  it('keeps card actions consistent with light-mode controls', () => {
    const skillButton = declarationsFor('.sa-window[data-theme="light"] .skill-card button');
    const dangerButton = declarationsFor('.sa-window[data-theme="light"] .skill-card button.text-danger');

    expect(skillButton.background).toBe('var(--surface-2)');
    expect(skillButton.color).toBe('var(--text-pri)');
    expect(dangerButton.background).toBe('color-mix(in srgb, var(--error) 8%, var(--surface))');
    expect(dangerButton.color).toBe('var(--error)');
  });

  it('keeps chat action and composer controls on light-mode surfaces', () => {
    const feedback = declarationsFor('.sa-window[data-theme="light"] .top-command .action-feedback.success');
    const composerButton = declarationsFor('.sa-window[data-theme="light"] .composer button');
    const permission = declarationsFor('.sa-window[data-theme="light"] .permission-chip select');
    const model = declarationsFor('.sa-window[data-theme="light"] .composer-model');

    expect(feedback.background).toBe('color-mix(in srgb, var(--success) 11%, var(--surface))');
    expect(feedback.color).toBe('var(--success)');
    expect(composerButton.background).toBe('var(--surface-2)');
    expect(composerButton.color).toBe('var(--text-sec)');
    expect(permission.background).toBe('var(--surface-2)');
    expect(permission.color).toBe('var(--text-sec)');
    expect(model.background).toBe('var(--surface-2)');
    expect(model.color).toBe('var(--text-sec)');
  });
});

describe('blue-purple theme contract', () => {
  it('keeps the retired late visual layers out of the stylesheet', () => {
    const retiredFragments = [
      'Purple-blue luminous direction',
      'Monochrome product refinement',
      '--light-page',
      '--light-purple',
      '--light-cyan',
      '#c8b7ff',
      '#78dfff',
      'rgba(34, 211, 238',
    ];

    for (const fragment of retiredFragments) {
      expect(css).not.toContain(fragment);
    }
  });

  it('defines one root dark token contract plus one light override contract', () => {
    const rootSelectors = [];
    root.walkRules((rule) => {
      if (rule.selector === ':root') rootSelectors.push(rule);
    });

    const dark = declarationsFor(':root');
    const light = declarationsFor('.sa-window[data-theme="light"]');

    expect(rootSelectors).toHaveLength(1);
    expect(dark['--bg']).toBe('#070816');
    expect(dark['--surface']).toBe('#11162d');
    expect(dark['--surface-2']).toBe('#182039');
    expect(dark['--surface-3']).toBe('#243051');
    expect(dark['--text-pri']).toBe('#f7f9ff');
    expect(dark['--primary']).toBe('#9b6cff');
    expect(dark['--primary-2']).toBe('#22d3ee');
    expect(dark['--app-bg']).toContain('#12143a');
    expect(dark['--app-bg']).toContain('#0d213d');
    expect(dark['--accent']).toBe('var(--primary)');
    expect(dark['--accent-2']).toBe('var(--primary-2)');
    expect(dark['--green']).toBe('var(--success)');
    expect(dark['--blue']).toBe('var(--info)');
    expect(light['--bg']).toBe('#f6f8fc');
    expect(light['--primary']).toBe('#6d28d9');
    expect(light['--primary-2']).toBe('#0284c7');
    expect(light['--accent']).toBe('var(--primary)');
    expect(light['--accent-2']).toBe('var(--primary-2)');
  });

  it('uses the blue-purple primary action treatment in both themes', () => {
    const tokens = declarationsFor(':root');
    const light = declarationsFor('.sa-window[data-theme="light"]');
    const primary = declarationsFor('.btn-primary');
    const primaryHover = declarationsFor('.btn-primary:hover');
    const primaryDisabled = declarationsFor('.btn-primary:disabled');
    const secondary = declarationsFor('.btn-secondary');

    expect(tokens['--primary-action-bg']).toContain('#9b6cff');
    expect(tokens['--primary-action-bg']).toContain('#22d3ee');
    expect(light['--primary-action-bg']).toContain('#6d28d9');
    expect(light['--primary-action-bg']).toContain('#0284c7');
    expect(primary.background).toBe('var(--primary-action-bg)');
    expect(primary.color).toBe('var(--primary-action-text)');
    expect(primary['white-space']).toBe('nowrap');
    expect(primaryHover.background).toBe('var(--primary-action-bg-hover)');
    expect(primaryDisabled.background).toBe('var(--surface-3)');
    expect(primaryDisabled.color).toBe('var(--text-muted)');
    expect(secondary.background).toBe('transparent');
  });

  it('keeps focus and semantic notices visible without decorative glow', () => {
    const focus = declarationsFor(':where(button, input, textarea, select):focus-visible');
    const notice = declarationsFor('.settings-prompt-notice');
    const status = declarationsFor('.settings-status');
    const error = declarationsFor('.settings-prompt-notice.is-error');

    expect(focus.outline).toBe('2px solid var(--focus-ring)');
    expect(notice.border).toBe('1px solid var(--border)');
    expect(notice.background).toBe('var(--surface-2)');
    expect(declarationsFor('.settings-prompt-notice.is-info').color).toBe('var(--info)');
    expect(declarationsFor('.settings-prompt-notice.is-success').color).toBe('var(--success)');
    expect(declarationsFor('.settings-prompt-notice.is-warning').color).toBe('var(--warning)');
    expect(error.color).toBe('var(--error)');
    expect(status.color).toBe('var(--success)');
  });
});
