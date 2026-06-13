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

function topLevelDeclarationsFor(selector) {
  const declarations = {};
  root.walkRules((rule) => {
    if (rule.parent?.type !== 'root') return;
    const selectors = splitSelectors(rule.selector);
    if (!selectors.includes(selector)) return;
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

function containerDeclarationsFor(containerParams, selector) {
  const matches = [];
  root.walkAtRules('container', (atRule) => {
    if (atRule.params !== containerParams) return;
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

const RAW_COLOR_VALUE = /#[0-9a-fA-F]{3,8}\b|rgba?\(/;

function expectThemeTokenColors(selector, properties) {
  const declarations = declarationsFor(selector);

  for (const property of properties) {
    expect(declarations[property], `${selector} ${property}`).toBeDefined();
    expect(declarations[property], `${selector} ${property}`).not.toMatch(RAW_COLOR_VALUE);
  }
}

const TOKEN_COLOR_RULES = [
  ['.provider', ['color']],
  ['.provider:hover', ['color']],
  ['.provider.active', ['color']],
  ['.provider-track', ['background']],
  ['.provider.active .provider-track', ['background']],
  ['.provider-thumb', ['background']],
  ['.top-command .project-dropdown', ['background']],
  ['.top-command .project-dropdown-row.selected', ['background']],
  ['.top-command .project-dropdown-item:hover', ['background']],
  ['.top-command .sidebar-toggle.active', ['border-color', 'background', 'color']],
  ['.top-command .action-feedback', ['border', 'background', 'color']],
  ['.top-command .action-feedback.success', ['border-color', 'background', 'color']],
  ['.top-command .action-feedback.warning', ['border-color', 'background', 'color']],
  ['.top-command .action-feedback.error', ['border-color', 'background', 'color']],
  ['.app-sidebar-nav button.active', ['background', 'color']],
  ['.chat-scroll-bottom-btn', ['border', 'background', 'color']],
  ['.chat-scroll-bottom-btn:hover', ['border-color', 'background', 'color']],
  ['.thread-new-primary', ['border-color', 'color']],
  ['.thread-archive-toggle.active', ['border-color', 'background', 'color']],
  ['.thread-clean', ['border-color', 'color']],
  ['.thread-name-input', ['border', 'background', 'color']],
  ['.thread-name-input:focus', ['border-color', 'box-shadow']],
  ['.thread-rename-save', ['border', 'background', 'color']],
  ['.thread-archive', ['border', 'background', 'color']],
  ['.thread-archive.active', ['border-color', 'background', 'color']],
  ['.thread-pin', ['color']],
  ['.thread-pin.active', ['border-color', 'background', 'color']],
  ['.thread-pin-tooltip', ['border', 'background', 'color']],
  ['.thread-action-tooltip', ['border', 'background', 'color']],
  ['.image-lightbox-backdrop', ['background']],
  ['.message-markdown', ['color']],
  ['.message.assistant .message-markdown', ['color']],
  ['.message-markdown th', ['background']],
  ['.message-markdown code', ['background']],
  ['.message-markdown pre', ['background']],
  ['.message-output', ['color']],
  ['.message.assistant .message-output', ['color']],
  ['.message-output pre', ['background']],
  ['.message-output--json pre', ['border-left']],
  ['.message-output--config pre', ['border-left']],
  ['.message-output--log pre', ['border-left']],
  ['.message-output--diff pre', ['border-left']],
  ['.diff-line--meta', ['color', 'background']],
  ['.diff-line--hunk', ['color', 'background']],
  ['.diff-line--added', ['color', 'background']],
  ['.diff-line--deleted', ['color', 'background']],
  ['.composer-card', ['background']],
  ['.composer--floating .composer-card', ['border-color', 'background']],
  ['.composer textarea:focus', ['border-color']],
  ['.composer.drop-active', ['border-top-color']],
  ['.composer.drop-active .composer-card', ['border-color', 'box-shadow']],
  ['.composer-drop-hint', ['border', 'background', 'color']],
  ['.composer button', ['background', 'color']],
  ['.composer-model', ['background', 'color']],
  ['.model-dropdown', ['background']],
  ['.model-dropdown select', ['background', 'color']],
  ['.model-inherit', ['border', 'background', 'color']],
  ['.runtime-toolbar button', ['background', 'color']],
  ['.score', ['background', 'color']],
  ['.score.good', ['border-color', 'color']],
  ['.score.bad', ['border-color', 'color']],
  ['.diff-file-group', ['background']],
  ['.diff-file-header', ['background']],
  ['.diff-line.add', ['background']],
  ['.diff-line.del', ['background']],
  ['.diff-line.hunk', ['background']],
  ['.runtime-activity-panel', ['background']],
  ['.runtime-stat-tooltip', ['border', 'background', 'color']],
  ['.runtime-stat.stat-lsp', ['color']],
  ['.runtime-stat.stat-json-render', ['color']],
  ['.runtime-stat.stat-playwright', ['color']],
  ['.runtime-stat.stat-go-run', ['color']],
  ['.runtime-stat.stat-cmd', ['color']],
  ['.runtime-stat.stat-file', ['color']],
  ['.runtime-stat.stat-tool', ['color']],
  ['.warning-log-popover', ['border', 'background', 'color']],
  ['.warning-log-popover code', ['color']],
  ['.skill-card header span', ['border', 'color']],
  ['.skill-card .quote', ['background']],
  ['.skill-card button', ['background', 'color']],
  ['.modal-box button', ['background', 'color']],
  ['.skills-subfiles button.active', ['border-color', 'color']],
  ['.skills-body-field textarea', ['background', 'color']],
  ['.file-row header span', ['border', 'color']],
  ['.file-row.is-final-output', ['border-color']],
  ['.file-row.is-final-output header span', ['border-color', 'color']],
  ['.similar-alert', ['border', 'color']],
  ['.similar-alert button', ['border-color', 'color']],
  ['.memory-health-track', ['background']],
  ['.memory-health-track.danger span', ['background']],
  ['.memory-notice', ['color', 'background']],
  ['.memory-notice.is-error', ['border-color', 'color']],
  ['.memory-notice.is-warning', ['border-color', 'color']],
  ['.memory-similar-item', ['border', 'background', 'color']],
  ['.memory-similar-item button', ['background', 'color']],
  ['.memory-card.type-project', ['border-left-color']],
  ['.memory-form-grid label', ['color']],
  ['.memory-form-grid input', ['background', 'color']],
  ['.memory-editor-actions button', ['background', 'color']],
  ['.prompt-tabs button', ['color', 'background']],
  ['.prompt-tabs button.active', ['color', 'border-color', 'background']],
  ['.prompt-badges span', ['color', 'background']],
  ['.prompt-badges span.active', ['color', 'border-color']],
  ['.sa-window[data-theme="light"] .message-markdown pre', ['background']],
  ['.sa-window[data-theme="light"] .log-lines', ['background', 'color']],
  ['.sa-window[data-theme="light"] .diff-file-lines', ['background']],
  ['.modal-overlay', ['background']],
];

describe('composer layout styles', () => {
  it('draws the screenshot separator line between the composer textarea and controls', () => {
    const textarea = declarationsFor('.composer textarea');

    expect(textarea['border-bottom']).toBe('1px solid var(--border)');
  });

  it('keeps the composer textarea within a three-to-eight-row range', () => {
    const textarea = declarationsFor('.composer textarea');

    expect(textarea['line-height']).toBe('1.5');
    expect(textarea['min-height']).toBe('calc(1.5em * 3 + 34px)');
    expect(textarea['max-height']).toBe('calc(1.5em * 8 + 34px)');
    expect(textarea['overflow-y']).toBe('auto');
  });

  it('keeps composer send controls aligned with the shell theme', () => {
    const sendIcon = declarationsFor('.composer .send svg');

    expect(sendIcon.transform).toBe('none');
    expect(sendIcon['transform-origin']).toBe('50% 50%');
  });

  it('keeps composer interrupt controls visually distinct from sending', () => {
    const interruptButton = declarationsFor('.composer .send--interrupt');
    const interruptIcon = declarationsFor('.composer .send--interrupt svg');

    expect(interruptButton.background).toContain('var(--error)');
    expect(interruptIcon.transform).toBe('none');
  });

  it('keeps app workbench navigation and agent list icons on consistent fixed sizes', () => {
    const nav = declarationsFor('.app-sidebar-nav');
    const navButton = declarationsFor('.app-sidebar-nav button');
    const navIcon = declarationsFor('.app-sidebar-nav button svg');
    const navActive = declarationsFor('.app-sidebar-nav button.active');
    const threadToolIcon = declarationsFor('.thread-tools svg');
    const threadCardIcon = declarationsFor('.thread-card svg');
    const threadPin = declarationsFor('.thread-pin');
    const threadPinHover = declarationsFor('.thread-pin:hover');
    const providerBadge = declarationsFor('.thread-card b');
    const statusLine = declarationsFor('.thread-status-row');
    const statusDot = declarationsFor('.thread-status-dot');

    expect(nav.gap).toBe('14px');
    expect(nav['padding-top']).toBe('0');
    expect(navButton.width).toBe('100%');
    expect(navButton['border-left']).toBeUndefined();
    expect(navActive.background).toBe('var(--sidebar-active)');
    expect(navIcon.width).toBe('22px');
    expect(navIcon.height).toBe('22px');
    expect(navIcon['flex-shrink']).toBe('0');
    expect(threadToolIcon.width).toBe('16px');
    expect(threadToolIcon.height).toBe('16px');
    expect(threadCardIcon.width).toBe('16px');
    expect(threadCardIcon.height).toBe('16px');
    expect(threadPin.background).toBe('var(--surface-2)');
    expect(threadPin.color).toBe('var(--text-muted)');
    expect(threadPin['border-color']).toBe('var(--border)');
    expect(threadPinHover.color).toBe('var(--text-pri)');
    expect(providerBadge.display).toBe('inline-flex');
    expect(providerBadge['min-height']).toBe('22px');
    expect(providerBadge['min-width']).toBe('52px');
    expect(providerBadge['font-size']).toBe('12px');
    expect(providerBadge['line-height']).toBe('1');
    expect(statusLine.display).toBe('inline-flex');
    expect(statusLine['font-size']).toBe('12px');
    expect(statusDot.width).toBe('8px');
    expect(statusDot.height).toBe('8px');
    expect(statusDot['flex']).toBe('0 0 auto');
  });

  it('renders thread card icon actions without outer rings', () => {
    const threadArchive = declarationsFor('.thread-archive');
    const threadArchiveActive = declarationsFor('.thread-archive.active');
    const threadArchiveFocus = declarationsFor('.thread-archive:focus-visible');
    const threadRename = declarationsFor('.thread-rename-trigger');
    const threadRenameFocus = declarationsFor('.thread-rename-trigger:focus-visible');
    const threadPinActive = declarationsFor('.thread-pin.active');
    const threadPinFocus = declarationsFor('.thread-pin:focus-visible');

    expect(threadArchive.border).toBe('0');
    expect(threadArchive.background).toBe('transparent');
    expect(threadArchive['box-shadow']).toBe('none');
    expect(threadRename.border).toBe('0');
    expect(threadRename.background).toBe('transparent');
    expect(threadRename.outline).toBe(threadPinFocus.outline);
    expect(threadRename['box-shadow']).toBe('none');
    expect(threadArchiveActive.color).toBe(threadPinActive.color);
    expect(threadArchiveActive.border).toBe('none');
    expect(threadArchiveActive.background).toBe(threadPinActive.background);
    expect(threadArchiveActive['border-color']).toBe(threadPinActive['border-color']);
    expect(threadArchiveActive['border-style']).toBe(threadPinActive['border-style']);
    expect(threadArchiveActive.outline).toBe(threadPinFocus.outline);
    expect(threadArchiveActive['box-shadow']).toBe(threadPinActive['box-shadow']);
    expect(threadArchiveFocus.outline).toBe(threadPinFocus.outline);
    expect(threadArchiveFocus['box-shadow']).toBe('none');
    expect(threadRenameFocus.outline).toBe(threadPinFocus.outline);
    expect(threadRenameFocus['box-shadow']).toBe('none');
  });

  it('lets thread card actions adapt inside the agent list width', () => {
    const card = declarationsFor('.thread-card');
    const main = declarationsFor('.thread-main');
    const editingMain = declarationsFor('.thread-main--editing');
    const actions = firstDeclarationsFor('.thread-card-actions');
    const archive = declarationsFor('.thread-archive');
    const pin = declarationsFor('.thread-pin');
    const rename = declarationsFor('.thread-rename-trigger');
    const compactActions = containerDeclarationsFor('(max-width: 260px)', '.thread-card-actions');

    expect(card['grid-template-columns']).toBe('minmax(0, 1fr) minmax(0, max-content)');
    expect(card['container-type']).toBe('inline-size');
    expect(card.padding).toBe('8px');
    expect(main['grid-column']).toBe('1');
    expect(main.padding).toBe('0');
    expect(editingMain['padding-left']).toBe('0');
    expect(editingMain['padding-right']).toBe('0');
    expect(actions.display).toBe('flex');
    expect(actions['flex-wrap']).toBe('nowrap');
    expect(actions['justify-content']).toBe('flex-end');
    expect(actions['max-width']).toBe('max-content');
    expect(archive.position).toBe('relative');
    expect(pin.position).toBe('relative');
    expect(rename.position).toBe('relative');
    expect(archive.transform).toBe('none');
    expect(pin.transform).toBe('none');
    expect(rename.transform).toBe('none');
    expect(compactActions).toHaveLength(1);
    expect(compactActions[0]['grid-column']).toBe('1 / -1');
    expect(compactActions[0]['grid-row']).toBe('2');
    expect(compactActions[0].width).toBe('100%');
    expect(compactActions[0]['max-width']).toBe('100%');
    expect(compactActions[0].gap).toBe('3px');
    expect(compactActions[0]['flex-direction']).toBeUndefined();
  });

  it('keeps workflow run history rows aligned as scannable data columns', () => {
    const row = declarationsFor('.run-row');
    const label = declarationsFor('.run-row span');
    const status = declarationsFor('.run-row em');
    const time = declarationsFor('.run-row time');

    expect(row.display).toBe('grid');
    expect(row['grid-template-columns']).toBe('minmax(128px, 1fr) minmax(56px, max-content) max-content');
    expect(row['text-align']).toBe('left');
    expect(label['justify-self']).toBe('start');
    expect(status['justify-self']).toBe('end');
    expect(status['white-space']).toBe('nowrap');
    expect(time['justify-self']).toBe('end');
    expect(time['font-variant-numeric']).toBe('tabular-nums');
    expect(time['white-space']).toBe('nowrap');
  });

  it('keeps runtime panel details shrink-safe inside the right rail', () => {
    const panel = declarationsFor('.runtime-panel');
    const activityPanel = declarationsFor('.runtime-activity-panel');
    const icons = declarationsFor('.runtime-icons');
    const logs = declarationsFor('.log-lines');
    const tooltipRow = declarationsFor('.runtime-stat-tooltip-row');
    const tooltipName = declarationsFor('.runtime-stat-tooltip-name');
    const logLine = declarationsFor('.warning-log-line');

    expect(panel.border).toBeUndefined();
    expect(panel.position).toBe('relative');
    expect(Number(panel['z-index'])).toBeGreaterThan(20);
    expect(panel['overflow-x']).toBe('visible');
    expect(activityPanel.border).toBeUndefined();
    expect(activityPanel['border-top']).toBe('1px solid var(--line)');
    expect(activityPanel['border-radius']).toBeUndefined();
    expect(activityPanel['min-width']).toBe('0');
    expect(activityPanel['max-width']).toBe('100%');
    expect(activityPanel['overflow-x']).toBe('visible');
    expect(icons['min-width']).toBe('0');
    expect(icons.overflow).toBe('visible');
    expect(logs['min-width']).toBe('0');
    expect(logs['max-width']).toBe('100%');
    expect(logs['overflow-x']).toBe('hidden');
    expect(tooltipRow['min-width']).toBe('0');
    expect(tooltipName['min-width']).toBe('0');
    expect(tooltipName.overflow).toBe('visible');
    expect(tooltipName['white-space']).toBe('normal');
    expect(logLine['min-width']).toBe('0');
    expect(logLine.display).toBe('block');
    expect(logLine.width).toBe('100%');
    expect(logLine['max-width']).toBe('100%');
    expect(logLine.overflow).toBe('hidden');
    expect(logLine['text-overflow']).toBe('ellipsis');
  });
});

describe('theme-aware component styles', () => {
  it('uses theme-aware colors for skill filter active buttons', () => {
    const active = declarationsFor('.skill-filter .active');

    expect(active.background).toBe('color-mix(in srgb, var(--accent-2) 16%, var(--surface-3))');
    expect(active.color).toBe('var(--text-pri)');
    expect(active['border-color']).toBe('var(--border-strong)');
    expect(active.background).not.toBe('#4d4f55');
  });

  it('keeps skill editor controls on theme token surfaces', () => {
    const modalButton = declarationsFor('.skills-editor-modal button');
    const scopeButton = declarationsFor('.skills-scope-segmented button');
    const activeScopeButton = declarationsFor('.skills-scope-segmented button.active');
    const bodyPreview = declarationsFor('.skills-body-preview');
    const bodyPreviewHeading = declarationsFor('.skills-body-preview h3');

    expect(modalButton.background).toBe('var(--surface-2)');
    expect(modalButton.color).toBe('var(--text-pri)');
    expect(scopeButton.background).toBe('var(--surface-2)');
    expect(scopeButton.color).toBe('var(--text-sec)');
    expect(activeScopeButton.background).toBe('color-mix(in srgb, var(--success) 12%, var(--surface))');
    expect(activeScopeButton.color).toBe('var(--success)');
    expect(bodyPreview.background).toBe('var(--surface-2)');
    expect(bodyPreview.color).toBe('var(--text-sec)');
    expect(bodyPreviewHeading.color).toBe('var(--text-pri)');
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
    const image = declarationsFor('.mermaid-diagram img');

    expect(diagram['max-width']).toBe('100%');
    expect(diagram.overflow).toBe('auto');
    expect(diagram.background).toBe('var(--surface)');
    expect(preview.cursor).toBe('zoom-in');
    expect(preview.background).toBe('transparent');
    expect(hint.opacity).toBe('0');
    expect(lightboxSvg.overflow).toBe('auto');
    expect(image.display).toBe('block');
    expect(image['max-width']).toBe('100%');
  });

  it('keeps shared file rows compact while the preview modal can scroll content', () => {
    const rowTitle = declarationsFor('.file-row h3');
    const badge = declarationsFor('.file-row header span');
    const summary = declarationsFor('.file-row .shared-file-summary');
    const preview = declarationsFor('.shared-file-content-preview');

    expect(rowTitle['min-width']).toBe('0');
    expect(rowTitle.overflow).toBe('hidden');
    expect(rowTitle['text-overflow']).toBe('ellipsis');
    expect(badge.flex).toBe('0 0 auto');
    expect(summary['max-height']).toBe('calc(1.45em * 3)');
    expect(summary.overflow).toBe('hidden');
    expect(preview['max-height']).toBe('52vh');
    expect(preview.overflow).toBe('auto');
  });
});

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
    expect(search.border).toContain('var(--line)');
    expect(search.background).toContain('var(--panel)');
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
});

describe('assistant message styles', () => {
  it('keeps assistant message content compact instead of spanning the full timeline', () => {
    const message = declarationsFor('.message');
    const assistantBubble = declarationsFor('.message.assistant:not(.approval-message) .bubble');
    const markdown = declarationsFor('.message-markdown');

    expect(message.margin).toBe('18px auto');
    expect(assistantBubble['max-width']).toBe('min(1080px, 100%)');
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

describe('conversation grid styles', () => {
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
    const timelineShell = declarationsFor('.timeline-shell');
    const timeline = declarationsFor('.timeline');
    const message = declarationsFor('.message');
    const bubble = declarationsFor('.bubble');
    const composer = declarationsFor('.composer');

    expect(conversation['min-width']).toBe('0');
    expect(conversation.overflow).toBe('hidden');
    expect(timelineShell.position).toBe('relative');
    expect(timelineShell['min-width']).toBe('0');
    expect(timelineShell['min-height']).toBe('0');
    expect(timelineShell.overflow).toBe('hidden');
    expect(timeline['min-width']).toBe('0');
    expect(timeline.height).toBe('100%');
    expect(timeline['max-width']).toBe('100%');
    expect(message['min-width']).toBe('0');
    expect(bubble['min-width']).toBe('0');
    expect(bubble['max-width']).toBe('100%');
    expect(composer['min-width']).toBe('0');
    expect(composer['max-width']).toBe('100%');
  });
});

describe('conversation content column styles', () => {
  it('keeps timeline messages left-biased while the docked composer fills the footer frame', () => {
    const conversation = declarationsFor('.conversation');
    const timeline = declarationsFor('.timeline');
    const message = declarationsFor('.message');
    const userMessage = declarationsFor('.message.user');
    const userBubble = declarationsFor('.message.user .bubble');
    const composer = declarationsFor('.composer');
    const dockedComposer = declarationsFor('.composer.composer--docked');
    const dockedComposerCard = declarationsFor('.composer--docked .composer-card');
    const scrollButton = declarationsFor('.chat-scroll-bottom-btn');

    expect(conversation['--conversation-content-width']).toBe('min(1080px, calc(100% - 128px))');
    expect(timeline.display).toBe('flex');
    expect(timeline['flex-direction']).toBe('column');
    expect(timeline['align-items']).toBe('flex-start');
    expect(message.width).toBe('min(1080px, calc(100% - 128px))');
    expect(message.margin).toBe('18px auto');
    expect(userMessage['margin-left']).toBeUndefined();
    expect(userMessage.width).toBe('var(--conversation-content-width)');
    expect(userBubble['margin-left']).toBe('auto');
    expect(userBubble.background).toBe('var(--message-user-bg, var(--workbench-ink, var(--accent-2)))');
    expect(userBubble.color).toBe('var(--message-user-text, var(--on-accent))');
    expect(composer.width).toBe('min(940px, calc(100% - 112px))');
    expect(dockedComposer.padding).toBe('0');
    expect(dockedComposerCard.width).toBe('100%');
    expect(dockedComposerCard.margin).toBe('0 auto');
    expect(dockedComposerCard.border).toBe('1px solid var(--border-strong)');
    expect(dockedComposerCard['border-radius']).toBe('18px');
    expect(dockedComposerCard['box-shadow']).toBe('var(--composer-shadow)');
    expect(scrollButton.position).toBe('absolute');
    expect(scrollButton.right).toBe('max(18px, var(--conversation-content-right-gutter))');
    expect(scrollButton.bottom).toBe('18px');
    expect(scrollButton.width).toBe('32px');
    expect(scrollButton.height).toBe('32px');
  });

  it('keeps the new-chat intro stage centered and full width', () => {
    const introConversation = declarationsFor('.conversation--intro');
    const introTimeline = declarationsFor('.conversation--intro .timeline');
    const introStage = declarationsFor('.intro-chat-stage');
    const floatingComposer = declarationsFor('.conversation--intro .composer--floating');
    const floatingCard = declarationsFor('.composer--floating .composer-card');

    expect(introConversation['--conversation-intro-width']).toBe('min(884px, calc(100% - 120px))');
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
    const track = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer--floating .provider-track');

    expect(floatingCard.background).toContain('var(--surface)');
    expect(floatingCard['border-color']).toContain('var(--border-strong)');
    expect(floatingCard['box-shadow']).toContain('var(--shadow)');
    expect(attach.background).toBe('color-mix(in srgb, var(--surface-2) 86%, var(--surface))');
    expect(track.background).toBe('color-mix(in srgb, var(--surface-3) 72%, var(--border))');
  });
});

describe('workbench shell styles', () => {
  it('keeps the screenshot-style sidebar fixed and branded', () => {
    const sidebar = declarationsFor('.app-sidebar');
    const body = declarationsFor('.sa-body');
    const brand = declarationsFor('.sidebar-brand');
    const newChat = declarationsFor('.sidebar-new-chat');

    expect(sidebar.width).toBe('340px');
    expect(sidebar.background).toBe('var(--sidebar-bg)');
    expect(sidebar['border-right']).toBe('1px solid var(--sidebar-border)');
    expect(body.height).toBe('100vh');
    expect(body['grid-template-columns']).toBe('340px minmax(0, 1fr)');
    expect(brand.display).toBe('inline-flex');
    expect(newChat.height).toBe('56px');
    expect(newChat.background).toBe('var(--sidebar-active)');
  });

  it('exposes a mobile workbench drawer so settings remains reachable', () => {
    const desktopToggle = topLevelDeclarationsFor('.workbench-toggle');
    const mobileToggle = mediaDeclarationsFor('(max-width: 920px)', '.workbench-toggle')[0];
    const mobileSidebar = mediaDeclarationsFor('(max-width: 920px)', '.app-sidebar')[0];
    const openSidebar = mediaDeclarationsFor('(max-width: 920px)', '.app-sidebar.is-open')[0];
    const scrim = mediaDeclarationsFor('(max-width: 920px)', '.sidebar-scrim')[0];
    const mobileSettings = mediaDeclarationsFor('(max-width: 920px)', '.sa-window .settings-page')[0];

    expect(desktopToggle.display).toBe('none');
    expect(mobileToggle.display).toBe('inline-flex');
    expect(mobileToggle.position).toBe('fixed');
    expect(mobileToggle['z-index']).toBe('45');
    expect(mobileSidebar.position).toBe('fixed');
    expect(mobileSidebar['margin-left']).toBe('-324px');
    expect(mobileSidebar.transform).toBe('none');
    expect(mobileSidebar.transition).toBe('margin-left 180ms ease');
    expect(mobileSidebar['max-width']).toBe('min(324px, calc(100vw - 52px))');
    expect(openSidebar['margin-left']).toBe('0');
    expect(openSidebar.transform).toBe('none');
    expect(scrim.display).toBe('block');
    expect(mobileSettings['padding-top']).toBe('78px');
  });

  it('keeps the chat composer adaptive across desktop client widths and phones', () => {
    const mediumConversation = mediaDeclarationsFor('(max-width: 1180px)', '.sa-window .conversation')[0];
    const mediumComposer = mediaDeclarationsFor('(max-width: 1180px)', '.sa-window .composer')[0];
    const mediumActions = mediaDeclarationsFor('(max-width: 1180px)', '.sa-window .composer-actions')[0];
    const mobileMeta = mediaDeclarationsFor('(max-width: 640px)', '.sa-window .composer-meta')[0];
    const mobileProject = mediaDeclarationsFor('(max-width: 640px)', '.sa-window .composer-meta .project-select-wrap')[0];
    const mobileActions = mediaDeclarationsFor('(max-width: 640px)', '.sa-window .composer-actions')[0];
    const mobileModel = mediaDeclarationsFor('(max-width: 640px)', '.sa-window .composer-model')[0];
    const mobileSend = mediaDeclarationsFor('(max-width: 640px)', '.sa-window .composer .send')[0];

    expect(mediumConversation['--conversation-content-width']).toBe('min(100%, calc(100% - 56px))');
    expect(mediumComposer.width).toBe('min(100%, calc(100% - 56px))');
    expect(mediumActions['min-width']).toBe('0');
    expect(mobileMeta.display).toBe('grid');
    expect(mobileMeta['grid-template-columns']).toBe('minmax(0, 1fr) auto');
    expect(mobileProject['grid-column']).toBe('1 / -1');
    expect(mobileActions['grid-template-columns']).toBe('minmax(0, 1fr) auto');
    expect(mobileModel['max-width']).toBe('100%');
    expect(mobileSend['justify-self']).toBe('end');
  });
});

describe('composer control styles', () => {
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
    const wrap = topLevelDeclarationsFor('.composer-model-wrap');
    const button = topLevelDeclarationsFor('.composer-model');
    const dropdown = topLevelDeclarationsFor('.model-dropdown');

    expect(card.overflow).toBe('visible');
    expect(wrap.position).toBe('relative');
    expect(wrap.width).toBe('fit-content');
    expect(wrap['max-width']).toBe('min(150px, 42vw)');
    expect(button.width).toBe('fit-content');
    expect(button.padding).toBe('0 14px');
    expect(dropdown.position).toBe('absolute');
    expect(dropdown.inset).toBe('auto 0 calc(100% + 8px) auto');
    expect(dropdown.bottom).toBe('calc(100% + 8px)');
    expect(dropdown.height).toBe('max-content');
    expect(dropdown['max-height']).toBe('min(320px, calc(100vh - 48px))');
    expect(dropdown['grid-auto-rows']).toBe('max-content');
    expect(dropdown['align-content']).toBe('start');
    expect(dropdown.overflow).toBe('visible');
  });

  it('keeps attachment controls left while model controls sit on the right', () => {
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
    expect(track.background).toBe('var(--surface-3)');
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
    const collapsedActivity = declarationsFor('.runtime-activity-panel.is-log-collapsed');
    const collapsedIcons = declarationsFor('.runtime-activity-panel.is-log-collapsed .runtime-icons');
    const diff = declarationsFor('.diff-empty');
    const tooltip = declarationsFor('.runtime-stat-tooltip');
    const warningPopover = declarationsFor('.warning-log-popover');

    expect(panel['--activity-panel-height']).toBe('64px');
    expect(panel['--activity-panel-min-height']).toBe('64px');
    expect(panel['overflow-x']).toBe('visible');
    expect(panel['overflow-y']).toBe('visible');
    expect(panel['grid-template-rows']).toContain('var(--activity-panel-height)');
    expect(activity['overflow-x']).toBe('visible');
    expect(activity['overflow-y']).toBe('visible');
    expect(activity.height).toBe('var(--activity-panel-height)');
    expect(collapsedActivity['grid-template-rows']).toBe('minmax(0, 1fr)');
    expect(collapsedIcons.height).toBe('100%');
    expect(collapsedIcons['border-bottom']).toBe('0');
    expect(Number(activity['z-index'])).toBeGreaterThan(Number(diff['z-index']));
    expect(tooltip.position).toBe('fixed');
    expect(tooltip.left).toBe('var(--runtime-stat-tooltip-left, 12px)');
    expect(tooltip['max-height']).toBe('var(--runtime-stat-tooltip-max-height, min(280px, 42vh))');
    expect(Number(tooltip['z-index'])).toBeGreaterThan(Number(activity['z-index']));
    expect(Number(tooltip['z-index'])).toBeGreaterThan(Number(warningPopover['z-index']));
  });
});

describe('runtime resize styles', () => {
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
    expect(icons['overflow']).toBe('visible');
    expect(stat['min-width']).toBe('0');
    expect(stat['justify-content']).toBe('center');
  });

  it('wraps long runtime tool names inside the click tooltip instead of using native hover titles', () => {
    const toolName = declarationsFor('.runtime-stat-tooltip-name');

    expect(toolName['min-width']).toBe('0');
    expect(toolName.overflow).toBe('visible');
    expect(toolName['overflow-wrap']).toBe('anywhere');
    expect(toolName['text-overflow']).toBeUndefined();
    expect(toolName['white-space']).toBe('normal');
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
    expect(codeBlock.background).toBe('var(--surface-code)');
    expect(codeBlock.color).toBe('var(--text-pri)');
  });

  it('uses light surfaces for runtime details instead of the dark console treatment', () => {
    const activity = declarationsFor('.sa-window[data-theme="light"] .runtime-activity-panel');
    const logs = declarationsFor('.sa-window[data-theme="light"] .log-lines');
    const tooltip = declarationsFor('.sa-window[data-theme="light"] .runtime-stat-tooltip');
    const popoverCode = declarationsFor('.sa-window[data-theme="light"] .warning-log-popover code');
    const diffLines = declarationsFor('.sa-window[data-theme="light"] .diff-file-lines');

    expect(activity['border-top-color']).toBe('var(--border)');
    expect(activity['border-color']).toBeUndefined();
    expect(activity.background).toBe('var(--surface)');
    expect(activity.color).toBe('var(--text-sec)');
    expect(logs.background).toBe('var(--surface-code)');
    expect(logs.color).toBe('var(--text-sec)');
    expect(tooltip.background).toBe('var(--surface)');
    expect(tooltip.color).toBe('var(--text-pri)');
    expect(popoverCode.color).toBe('var(--text-sec)');
    expect(diffLines.background).toBe('var(--surface-code)');
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
});

describe('light theme control surfaces', () => {
  it('keeps card actions consistent with light-mode controls', () => {
    const skillButton = declarationsFor('.sa-window[data-theme="light"] .skill-card button');
    const dangerButton = declarationsFor('.sa-window[data-theme="light"] .skill-card button.text-danger');

    expect(skillButton.background).toBe('var(--surface-2)');
    expect(skillButton.color).toBe('var(--text-pri)');
    expect(dangerButton.background).toBe('color-mix(in srgb, var(--error) 8%, var(--surface))');
    expect(dangerButton.color).toBe('var(--error)');
  });

  it('keeps skill editor controls and preview readable in light mode', () => {
    const modalButton = declarationsFor('.sa-window[data-theme="light"] .skills-editor-modal button');
    const scopeButton = declarationsFor('.sa-window[data-theme="light"] .skills-scope-segmented button');
    const activeScopeButton = declarationsFor('.sa-window[data-theme="light"] .skills-scope-segmented button.active');
    const preview = declarationsFor('.sa-window[data-theme="light"] .skills-body-preview');
    const previewHeading = declarationsFor('.sa-window[data-theme="light"] .skills-body-preview h3');

    expect(modalButton.background).toBe('var(--surface-2)');
    expect(modalButton.color).toBe('var(--text-pri)');
    expect(scopeButton.background).toBe('var(--surface-2)');
    expect(scopeButton.color).toBe('var(--text-sec)');
    expect(activeScopeButton.background).toBe('color-mix(in srgb, var(--success) 12%, var(--surface))');
    expect(activeScopeButton.color).toBe('var(--success)');
    expect(preview.background).toBe('var(--surface-2)');
    expect(preview.color).toBe('var(--text-sec)');
    expect(previewHeading.color).toBe('var(--text-pri)');
  });

  it('keeps chat action and composer controls on light-mode surfaces', () => {
    const feedback = declarationsFor('.sa-window[data-theme="light"] .top-command .action-feedback.success');
    const composerButton = declarationsFor('.sa-window[data-theme="light"] .composer button');
    const model = declarationsFor('.sa-window[data-theme="light"] .composer-model');

    expect(feedback.background).toBe('color-mix(in srgb, var(--success) 11%, var(--surface))');
    expect(feedback.color).toBe('var(--success)');
    expect(composerButton.background).toBe('var(--surface-2)');
    expect(composerButton.color).toBe('var(--text-sec)');
    expect(model.background).toBe('var(--surface-2)');
    expect(model.color).toBe('var(--text-sec)');
  });
});

describe('card layout styles', () => {
  it('keeps card badges and actions horizontal beside long content', () => {
    const memoryTitle = declarationsFor('.memory-card h3');
    const skillTitle = declarationsFor('.skill-card h3');
    const promptTitle = declarationsFor('.prompt-card h3');
    const memoryBadge = declarationsFor('.memory-card header span');
    const skillBadge = declarationsFor('.skill-card header span');
    const promptBadge = declarationsFor('.prompt-badges span');
    const timestamp = declarationsFor('.memory-card footer time');
    const memoryAction = declarationsFor('.memory-card button');
    const skillAction = declarationsFor('.skill-card button');
    const promptAction = declarationsFor('.prompt-card-actions button');

    for (const title of [memoryTitle, skillTitle]) {
      expect(title.flex).toBe('1 1 auto');
      expect(title['min-width']).toBe('0');
      expect(title['overflow-wrap']).toBe('anywhere');
    }
    expect(promptTitle['min-width']).toBe('0');
    expect(promptTitle['overflow-wrap']).toBe('anywhere');

    for (const badge of [memoryBadge, skillBadge, promptBadge]) {
      expect(badge.display).toBe('inline-flex');
      expect(badge.flex).toBe('0 0 auto');
      expect(badge['align-items']).toBe('center');
      expect(badge['white-space']).toBe('nowrap');
    }

    expect(timestamp.flex).toBe('1 1 auto');
    expect(timestamp['min-width']).toBe('0');
    for (const action of [memoryAction, skillAction, promptAction]) {
      expect(action.flex).toBe('0 0 auto');
      expect(action['min-width']).toBe('64px');
      expect(action['white-space']).toBe('nowrap');
    }
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

  it('keeps workflow final-output media actions theme-token driven', () => {
    const group = declarationsFor('.workflow-output-actions');
    const action = declarationsFor('.workflow-page .workflow-output-action');
    const preview = declarationsFor('.workflow-page .workflow-output-action-preview');
    const previewHover = declarationsFor('.workflow-page .workflow-output-action-preview:hover');
    const system = declarationsFor('.workflow-page .workflow-output-action-system');
    const systemHover = declarationsFor('.workflow-page .workflow-output-action-system:hover');
    const disabled = declarationsFor('.workflow-page .workflow-output-action:disabled');

    expect(group.background).toContain('var(--surface-2)');
    expect(group.background).toContain('var(--bg)');
    expect(action.background).toBe('transparent');
    expect(action.color).toBe('var(--text-pri)');
    expect(preview.background).toBe('var(--primary-action-bg)');
    expect(preview.color).toBe('var(--primary-action-text)');
    expect(previewHover.background).toBe('var(--primary-action-bg-hover)');
    expect(system.background).toBe('transparent');
    expect(system.color).toBe('var(--text-pri)');
    expect(systemHover.background).toContain('var(--surface-3)');
    expect(disabled.background).toBe('var(--surface-3)');
    expect(disabled.color).toBe('var(--text-muted)');
  });
});

describe('blue-purple form and notice contract', () => {
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

  it('keeps prompt editor readonly previews on theme-aware form surfaces', () => {
    const preview = declarationsFor('.prompt-editor-grid .prompt-preview-readonly');

    expect(preview.background).toBe('color-mix(in srgb, var(--surface-2) 72%, var(--bg))');
    expect(preview.color).toBe('var(--text-sec)');
    expect(preview['border-color']).toBe('var(--border)');
    expect(preview.cursor).toBe('default');
  });
});

describe('modal overlay styles', () => {
  it('keeps the full-screen backdrop invisible inside button-themed pages', () => {
    const base = declarationsFor('.modal-overlay > button.modal-overlay-backdrop');
    const hover = declarationsFor('.modal-overlay > button.modal-overlay-backdrop:hover');
    const focus = declarationsFor('.modal-overlay > button.modal-overlay-backdrop:focus');
    const focusVisible = declarationsFor('.modal-overlay > button.modal-overlay-backdrop:focus-visible');

    for (const declarations of [base, hover, focus, focusVisible]) {
      expect(declarations.position).toBe('absolute');
      expect(declarations.inset).toBe('0');
      expect(declarations.border).toBe('0');
      expect(declarations.background).toBe('transparent');
      expect(declarations.color).toBe('transparent');
      expect(declarations.padding).toBe('0');
      expect(declarations['box-shadow']).toBe('none');
    }
  });
});

describe('theme token color coverage', () => {
  it('keeps themeable component colors on tokens instead of raw color literals', () => {
    for (const [selector, properties] of TOKEN_COLOR_RULES) {
      expectThemeTokenColors(selector, properties);
    }
  });

  it('does not hardcode raw colors outside theme token declarations', () => {
    const violations = [];

    root.walkDecls((declaration) => {
      if (declaration.prop.startsWith('--')) return;
      if (!RAW_COLOR_VALUE.test(declaration.value)) return;

      violations.push(
        `${declaration.source.start.line}: ${declaration.parent.selector} { ${declaration.prop}: ${declaration.value}; }`,
      );
    });

    expect(violations).toEqual([]);
  });
});
