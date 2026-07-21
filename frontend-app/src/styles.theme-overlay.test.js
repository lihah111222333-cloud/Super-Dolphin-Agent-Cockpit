import { describe, expect, it } from 'vitest';
import {
  css,
  root,
  splitSelectors,
  declarationsFor,
  topLevelDeclarationsFor,
  mediaDeclarationFor,
} from './styles.test.fixture.js';
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
  ['.runtime-toolbar .runtime-stat', ['background', 'color']],
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

describe('light theme baseline usability', () => {
  it('keeps assistant text and code readable in light mode', () => {
    const lightTheme = declarationsFor('.sa-window[data-theme="light"]');
    const markdown = declarationsFor('.sa-window[data-theme="light"] .message-markdown');
    const assistantMarkdown = declarationsFor('.sa-window[data-theme="light"] .message.assistant .message-markdown');
    const inlineCode = declarationsFor('.sa-window[data-theme="light"] .message-markdown code');
    const codeBlock = declarationsFor('.sa-window[data-theme="light"] .message-markdown pre');

    expect(lightTheme['--bg']).toBe('#fbf9f3');
    expect(lightTheme['--text-sec']).toBe('#584238');
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
    const tooltip = declarationsFor('#overlay-root[data-theme="light"] .runtime-stat-tooltip');
    const popoverCode = declarationsFor('#overlay-root[data-theme="light"] .warning-log-popover code');
    const diffLines = declarationsFor('.sa-window[data-theme="light"] .diff-file-lines');

    expect(activity['border-top-color']).toBe('var(--line)');
    expect(activity['border-color']).toBeUndefined();
    expect(activity.background).toBe('var(--surface-2)');
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
    const stat = declarationsFor('.sa-window[data-theme="light"] .runtime-toolbar .runtime-stat');
    const score = declarationsFor('.sa-window[data-theme="light"] .score');
    const goodScore = declarationsFor('.sa-window[data-theme="light"] .score.good');
    const badScore = declarationsFor('.sa-window[data-theme="light"] .score.bad');

    expect(toolbar.background).toBe('var(--surface-2)');
    expect(toolbar['border-bottom-color']).toBe('var(--line)');
    expect(stat.background).toBe('var(--surface)');
    expect(stat.color).toBe('var(--text-sec)');
    expect(stat['border-color']).toBe('var(--line)');
    expect(declarationsFor('.sa-window[data-theme="light"] .runtime-toolbar .runtime-stat:hover')).toEqual({});
    expect(declarationsFor('.sa-window[data-theme="light"] .runtime-toolbar .runtime-stat:focus-visible')).toEqual({});
    expect(score.background).toBe('var(--surface)');
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
    const modalButton = declarationsFor('#overlay-root[data-theme="light"] .skills-editor-modal button');
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
  it('keeps MCP tool controls aligned when feedback appears', () => {
    const card = topLevelDeclarationsFor('.mcp-tool-card');
    const compactCard = mediaDeclarationFor('(max-width: 640px)', '.mcp-tool-card', 'grid-template-columns');
    const notice = declarationsFor('.mcp-tool-notice');
    const status = declarationsFor('.mcp-tool-status');

    expect(card['align-items']).toBe('center');
    expect(card['grid-template-columns']).toBe('36px minmax(0, 1fr) max-content auto');
    expect(compactCard['grid-template-columns']).toBe('32px minmax(0, 1fr) max-content auto');
    expect(status['align-self']).toBe('center');
    expect(status['justify-self']).toBe('end');
    expect(notice.margin).toBe('0');
    expect(notice['line-height']).toBe('1.35');

    // contract: MCP 卡片统一使用暗色主题变量底座（浅色/深色主题各自取值），
    // 保证白色标题、说明与状态文字的对比度；颜色必须走 --mcp-card-* 变量而非硬编码。
    expect(card.background).toBe('var(--mcp-card-bg)');
    expect(card.border).toBe('1px solid var(--mcp-card-border)');
    expect(card.color).toBe('var(--mcp-card-text)');

    const lightTokens = declarationsFor(':root[data-theme="light"]');
    const darkTokens = declarationsFor(':root[data-theme="dark"]');
    for (const token of ['--mcp-card-bg', '--mcp-card-text', '--mcp-card-text-muted', '--mcp-card-border']) {
      expect(lightTokens[token], `light ${token}`).toBeTruthy();
      expect(darkTokens[token], `dark ${token}`).toBeTruthy();
    }
    expect(lightTokens['--mcp-card-text']).toBe('#ffffff');
    // 两个主题的卡片底色都必须是暗色（亮度低），确保白色文字可读。
    expect(lightTokens['--mcp-card-bg']).toMatch(/^#([0-5][0-9a-f]){3}$/i);
    expect(darkTokens['--mcp-card-bg']).toMatch(/^#([0-5][0-9a-f]){3}$/i);
  });

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

  it('keeps MCP status badges vertically centered in their own grid column', () => {
    const card = topLevelDeclarationsFor('.mcp-tool-card');
    const compactCard = mediaDeclarationFor('(max-width: 640px)', '.mcp-tool-card', 'grid-template-columns');
    const status = declarationsFor('.mcp-tool-status');

    expect(card['grid-template-columns']).toBe('36px minmax(0, 1fr) max-content auto');
    expect(card['align-items']).toBe('center');
    expect(compactCard['grid-template-columns']).toBe('32px minmax(0, 1fr) max-content auto');
    expect(status['align-self']).toBe('center');
    expect(status['justify-self']).toBe('end');
  });
});

describe('fusion surface redesign contracts', () => {
  it('keeps personalization overview split between hero and content', () => {
    const overview = topLevelDeclarationsFor('.personalization-overview');
    const hero = topLevelDeclarationsFor('.personalization-overview-hero');

    // contract: overview must not have fusion bg; it sits transparently on the page
    // canvas so the header copy and the white stat cards read as separate objects.
    expect(overview.background).not.toContain('color-mix(in srgb, var(--orange)');
    expect(overview.background).toBe('transparent');

    // contract: hero has specific grid
    expect(hero.display).toBe('grid');
    expect(hero['grid-template-columns']).toBe('minmax(220px, 1fr) auto');
  });

  it('keeps skills resolution panel neutral with a distinct header', () => {
    const panel = declarationsFor('.skills-resolution-panel');
    const header = declarationsFor('.skills-resolution-header');

    // contract: panel must be neutral
    expect(panel.background).toBe('var(--surface)');
    expect(panel.background).not.toContain('color-mix(in srgb, var(--orange)');

    // contract: header is distinct (receives fusion-surface via JSX)
    expect(header['border-radius']).toBe('0');
  });

  it('keeps datasource empty state neutral', () => {
    const emptyCard = declarationsFor('.datasource-empty-card');

    // contract: empty state must not use fusion background
    expect(emptyCard.background).toBe('var(--surface)');

    // contract: empty state explicitly overrides 42vh
    expect(emptyCard['min-height']).toBe('auto');
  });

  it('restricts workflow overview to a stable vertical column layout', () => {
    const overview = declarationsFor('.workflow-overview');
    const dl = declarationsFor('.workflow-overview dl');
    const dd = declarationsFor('.workflow-overview dd');

    // contract: workflow-overview must be column-based to avoid overflow
    expect(overview.display).toBe('flex');
    expect(overview['flex-direction']).toBe('column');
    expect(overview['grid-template-columns']).toBeUndefined();

    // contract: workflow-overview dl stats row uses flex-wrap and justify-content: center
    expect(dl.display).toBe('flex');
    expect(dl['flex-wrap']).toBe('wrap');
    expect(dl['justify-content']).toBe('center');

    // contract: no text clipping or ellipsis in stats; must wrap naturally
    expect(dd['overflow']).toBeUndefined();
    expect(dd['text-overflow']).toBeUndefined();
    expect(dd['white-space']).toBeUndefined();
    expect(dd['overflow-wrap']).toBe('anywhere');
    expect(dd['word-break']).toBe('break-word');
  });
});

describe('suiyuan theme contract', () => {
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

  it('defines one root light-default token contract plus one dark override contract', () => {
    const themeRootRules = [];
    root.walkRules((rule) => {
      if (splitSelectors(rule.selector).includes(':root') && rule.nodes.some((node) => node.type === 'decl' && node.prop === '--bg')) {
        themeRootRules.push(rule);
      }
    });

    const rootLight = declarationsFor(':root');
    const dark = declarationsFor(':root[data-theme="dark"]');
    const light = declarationsFor('.sa-window[data-theme="light"]');

    expect(themeRootRules).toHaveLength(1);
    expect(rootLight['--bg']).toBe('#fbf9f3');
    expect(rootLight['--surface']).toBe('#ffffff');
    expect(dark['--bg']).toBe('#131411');
    expect(dark['--surface']).toBe('#1b1c18');
    expect(dark['--surface-2']).toBe('#1e1f1b');
    expect(dark['--surface-3']).toBe('#292a25');
    expect(dark['--text-pri']).toBe('#e5e2da');
    expect(dark['--primary']).toBe('#ffb597');
    expect(dark['--primary-2']).toBe('#ff8a50');
    expect(dark['--app-bg']).toBe('#131411');
    expect(dark['--accent']).toBe('var(--primary)');
    expect(dark['--accent-2']).toBe('var(--primary-2)');
    expect(dark['--green']).toBe('var(--success)');
    expect(dark['--blue']).toBe('var(--info)');
    expect(light['--bg']).toBe('#fbf9f3');
    expect(light['--primary']).toBe('#a03b00');
    expect(light['--primary-2']).toBe('#792b00');
    expect(light['--accent']).toBe('var(--primary)');
    expect(light['--accent-2']).toBe('var(--primary-2)');
  });

  it('uses the Suiyuan primary action treatment in light mode', () => {
    const tokens = declarationsFor(':root[data-theme="dark"]');
    const light = declarationsFor('.sa-window[data-theme="light"]');
    const primary = declarationsFor('.btn-primary');
    const primaryHover = declarationsFor('.btn-primary:hover');
    const primaryDisabled = declarationsFor('.btn-primary:disabled');
    const secondary = declarationsFor('.btn-secondary');

    expect(tokens['--primary-action-bg']).toBe('#ffb597');
    expect(light['--primary-action-bg']).toBe('#a03b00');
    expect(light['--primary-action-bg-hover']).toBe('#c84d05');
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
