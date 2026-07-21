import { describe, expect, it } from 'vitest';
import {
  declarationsFor,
  firstDeclarationsFor,
  mediaDeclarationsFor,
} from './styles.test.fixture.js';
describe('timeline content styles', () => {
  it('keeps observability filters aligned to the page and theme surface', () => {
    const page = declarationsFor('.settings-page.observability-page');
    const search = declarationsFor('.observability-search');
    const grid = firstDeclarationsFor('.observability-filter-grid');
    const label = declarationsFor('.observability-filter-grid label');
    const input = declarationsFor('.observability-filter-grid input');
    const placeholder = declarationsFor('.observability-filter-grid input::placeholder');
    const focus = declarationsFor('.observability-filter-grid input:focus-visible');
    const tabletGrid = mediaDeclarationsFor('(max-width: 980px)', '.observability-filter-grid');
    const mobileGrid = mediaDeclarationsFor('(max-width: 640px)', '.observability-filter-grid');

    expect(page.padding).toBe('18px 24px 36px');
    expect(search.width).toBe('100%');
    expect(search['min-width']).toBe('0');
    expect(search.border).toBeUndefined();
    expect(search.background).toBeUndefined();
    expect(grid.width).toBe('100%');
    expect(grid['min-width']).toBe('0');
    expect(grid['grid-template-columns']).toBe('repeat(4, minmax(0, 1fr))');
    expect(label['min-width']).toBe('0');
    expect(input['box-sizing']).toBe('border-box');
    expect(input['min-width']).toBe('0');
    expect(input.height).toBe('40px');
    expect(input.border).toContain('var(--line)');
    expect(input.background).toContain('var(--panel-2)');
    expect(placeholder.color).toBe('var(--text-subtle)');
    expect(focus['border-color']).toContain('var(--blue)');
    expect(focus['box-shadow']).toContain('var(--blue)');
    expect(tabletGrid).toContainEqual(expect.objectContaining({ 'grid-template-columns': 'repeat(2, minmax(0, 1fr))' }));
    expect(mobileGrid).toContainEqual(expect.objectContaining({ 'grid-template-columns': 'minmax(0, 1fr)' }));
  });

  it('keeps observability system log cards from being flex-shrunk and clipped', () => {
    const systemLog = declarationsFor('.observability-system-log');
    const logTable = declarationsFor('.observability-log-table');
    const logHeadRow = declarationsFor('.observability-log-table-head-row');
    const detailRow = declarationsFor('.observability-log-table-detail-row');
    const detailCell = declarationsFor('.observability-log-table-detail-cell');

    expect(systemLog['flex-shrink']).toBe('0');
    expect(systemLog.overflow).toBe('visible');
    expect(logTable['max-height']).toBe('min(560px, calc(100vh - 320px))');
    expect(logTable.overflow).toBe('auto');
    expect(logHeadRow['grid-template-columns']).toContain('minmax(168px');
    expect(detailRow.display).toBe('block');
    expect(detailRow.width).toBe('100%');
    expect(detailCell.display).toBe('block');
    expect(detailCell['min-width']).toBe('0');
  });
});

describe('reasoning trace styles', () => {
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
    expect(openStepList.border).toBe('1px solid var(--line)');
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
});

describe('assistant message styles', () => {
  it('keeps assistant message content compact instead of spanning the full timeline', () => {
    const message = declarationsFor('.message');
    const assistantBubble = declarationsFor('.message.assistant:not(.approval-message) .bubble');
    const markdown = declarationsFor('.message-markdown');

    expect(message.margin).toBe('12px auto');
    expect(assistantBubble['max-width']).toBe('min(840px, 100%)');
    expect(assistantBubble.padding).toBe('0');
    expect(assistantBubble.border).toBe('0');
    expect(assistantBubble.background).toBe('transparent');
    expect(markdown['font-size']).toBe('14px');
    expect(markdown['line-height']).toBe('1.62');
  });

  it('does not flatten approval message cards through the no-avatar assistant rule', () => {
    const genericNoAvatarBubble = declarationsFor('.message.assistant.no-avatar .bubble');
    const noAvatarBubble = declarationsFor('.message.assistant.no-avatar:not(.approval-message) .bubble');
    const approvalCard = declarationsFor('.message.assistant .approval-card');

    expect(genericNoAvatarBubble.background).toBeUndefined();
    expect(noAvatarBubble.background).toBe('transparent');
    expect(approvalCard.background).toBe('color-mix(in srgb, var(--surface-2) 82%, var(--accent) 4%)');
    expect(approvalCard['box-shadow']).toBe('var(--shadow)');
  });

  it('keeps streaming markdown and long code lines from forcing single-line layout', () => {
    const markdown = declarationsFor('.message-markdown');
    const markdownPre = declarationsFor('.message-markdown pre');
    const markdownPreCode = declarationsFor('.message-markdown pre code');

    expect(markdown['min-width']).toBe('0');
    expect(markdown['max-width']).toBe('100%');
    expect(markdown['white-space']).toBe('normal');
    expect(markdown['overflow-wrap']).toBe('anywhere');
    expect(markdownPre['max-width']).toBe('100%');
    expect(markdownPre['white-space']).toBe('pre-wrap');
    expect(markdownPre['overflow-wrap']).toBe('anywhere');
    expect(markdownPreCode['white-space']).toBe('pre-wrap');
    expect(markdownPreCode['overflow-wrap']).toBe('anywhere');
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
});
