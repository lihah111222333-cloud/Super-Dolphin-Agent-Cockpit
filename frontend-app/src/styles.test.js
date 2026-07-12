import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { cwd } from 'node:process';
import postcss from 'postcss';
import { describe, expect, it } from 'vitest';

const LAYER_TOKENS_FILE = 'src/shared/styles/LayerTokens.css';
const cssFiles = [
  LAYER_TOKENS_FILE,
  'src/styles.css',
  'src/AppChrome.css',
  'src/AppShell.css',
  'src/pages/chat/ChatPage.css',
  'src/pages/chat/ChatMessages.css',
  'src/pages/chat/ChatReasoning.css',
  'src/pages/chat/composer/ComposerDock.css',
  'src/pages/chat/runtime/RuntimePanel.css',
  'src/shared/styles/PagePrimitives.css',
  'src/pages/workflows/WorkflowPage.css',
  'src/pages/skills/SkillsPage.css',
  'src/pages/files/FilesPage.css',
  'src/pages/memory/MemoryPage.css',
  'src/pages/settings/SettingsPage.css',
  'src/pages/observability/ObservabilityPage.css',
  'src/shared/styles/PagePrimitivesLate.css',
  'src/features/prompts/PromptPageView.css',
  'src/pages/settings/components/SettingsPageComponents.css',
  'src/shared/styles/ThemePolish.css',
  'src/shared/styles/PagePrimitivesPolish.css',
  'src/AppShellWorkbench.css',
  'src/pages/chat/ChatPageWorkbench.css',
  'src/pages/chat/components/ProjectSelector.css',
  'src/pages/workflows/WorkflowEmptyState.css',
  'src/pages/files/FilesPageWorkbench.css',
  'src/features/prompts/PromptPagePolish.css',
  'src/pages/skills/SkillsPageHub.css',
  'src/features/prompts/Personalization.css',
  'src/AppShellSidebarPolish.css',
  'src/pages/workflows/WorkflowPolish.css',
  'src/pages/chat/components/RuntimePanelPolish.css',
  'src/pages/skills/DatasourcePage.css',
  'src/AppShellSidebarThreadActions.css',
  'src/shared/styles/MarkdownReferences.css',
];

const mainSource = readFileSync(path.join(cwd(), 'src/main.jsx'), 'utf8');
const appSource = readFileSync(path.join(cwd(), 'src/App.jsx'), 'utf8');
const indexSource = readFileSync(path.join(cwd(), 'index.html'), 'utf8');
const mainCssImports = [...mainSource.matchAll(/^import '\.\/([^']+\.css)';$/gm)].map((match) => `src/${match[1]}`);
const cssSources = new Map(cssFiles.map((file) => {
  const filePath = path.join(cwd(), file);
  if (file === LAYER_TOKENS_FILE && !existsSync(filePath)) return [file, ''];
  return [file, readFileSync(filePath, 'utf8')];
}));
const css = [...cssSources.values()].join('\n');
const root = postcss.parse(css);

const EXPECTED_Z_INDEX_TOKENS = new Set([
  '--z-local-behind',
  '--z-local-raised',
  '--z-local-handle',
  '--z-local-sticky',
  '--z-shell-control',
  '--z-overlay-popover',
  '--z-overlay-dialog',
  '--z-overlay-lightbox',
  '--z-overlay-critical',
]);
const EXPECTED_Z_INDEX_FILES = [
  'src/AppChrome.css',
  'src/AppShell.css',
  'src/AppShellSidebarThreadActions.css',
  'src/AppShellWorkbench.css',
  'src/pages/chat/ChatMessages.css',
  'src/pages/chat/ChatPage.css',
  'src/pages/chat/ChatPageWorkbench.css',
  'src/pages/chat/components/ProjectSelector.css',
  'src/pages/chat/composer/ComposerDock.css',
  'src/pages/chat/runtime/RuntimePanel.css',
  'src/pages/memory/MemoryPage.css',
  'src/pages/skills/SkillsPage.css',
];
const FORBIDDEN_HOST_STACKING_PROPERTIES = new Set([
  'transform',
  'opacity',
  'filter',
  'perspective',
  'contain',
  'isolation',
]);
const OVERLAY_THEME_SELECTOR_MIGRATIONS = [
  [
    '.sa-window[data-theme="light"] .runtime-stat-tooltip',
    '#overlay-root[data-theme="light"] .runtime-stat-tooltip',
  ],
  [
    '.sa-window[data-theme="light"] .warning-log-popover',
    '#overlay-root[data-theme="light"] .warning-log-popover',
  ],
  [
    '.sa-window[data-theme="light"] .warning-log-popover code',
    '#overlay-root[data-theme="light"] .warning-log-popover code',
  ],
  [
    '.sa-window[data-theme="light"] .skills-editor-modal button',
    '#overlay-root[data-theme="light"] .skills-editor-modal button',
  ],
  [
    '.sa-window[data-theme="light"] .skills-editor-modal button:hover:not(:disabled)',
    '#overlay-root[data-theme="light"] .skills-editor-modal button:hover:not(:disabled)',
  ],
  [
    '.sa-window[data-theme="light"] .skills-editor-modal button.ghost',
    '#overlay-root[data-theme="light"] .skills-editor-modal button.ghost',
  ],
];

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

function activeZIndexDeclarations() {
  const declarations = [];

  for (const [file, source] of cssSources) {
    const fileRoot = postcss.parse(source, { from: file });
    fileRoot.walkDecls('z-index', (declaration) => {
      declarations.push({
        file,
        selector: declaration.parent?.selector || '',
        value: declaration.value,
      });
    });
  }

  return declarations;
}

function selectorOccurrences(selector) {
  let count = 0;
  root.walkRules((rule) => {
    if (splitSelectors(rule.selector).includes(selector)) count += 1;
  });
  return count;
}

function indexHostViolations(source) {
  const parsed = new DOMParser().parseFromString(source, 'text/html');
  const roots = [...parsed.querySelectorAll('#root')];
  const overlayRoots = [...parsed.querySelectorAll('#overlay-root')];
  const violations = [];

  if (roots.length !== 1) violations.push('root-count');
  if (overlayRoots.length !== 1) violations.push('overlay-root-count');
  if (roots.length !== 1 || overlayRoots.length !== 1) return violations;

  const [appRoot] = roots;
  const [overlayRoot] = overlayRoots;
  if (appRoot.parentElement !== parsed.body || overlayRoot.parentElement !== parsed.body) {
    violations.push('host-sibling');
  }

  const bodyChildren = [...parsed.body.children];
  const appRootIndex = bodyChildren.indexOf(appRoot);
  const overlayRootIndex = bodyChildren.indexOf(overlayRoot);
  const scriptIndex = bodyChildren.findIndex((node) => node.tagName === 'SCRIPT');
  if (!(appRootIndex < overlayRootIndex && overlayRootIndex < scriptIndex)) {
    violations.push('host-order');
  }

  return violations;
}

function forbiddenHostStackingDeclarations() {
  const violations = [];
  const hostSelectors = new Set(['html', 'body', '#overlay-root']);

  root.walkRules((rule) => {
    const matchedSelectors = splitSelectors(rule.selector).filter((selector) => hostSelectors.has(selector));
    if (matchedSelectors.length === 0) return;
    rule.walkDecls((declaration) => {
      if (!FORBIDDEN_HOST_STACKING_PROPERTIES.has(declaration.prop)) return;
      for (const selector of matchedSelectors) {
        violations.push({ selector, property: declaration.prop, value: declaration.value });
      }
    });
  });

  return violations;
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

describe('css import order', () => {
  it('keeps the test stylesheet list aligned with the app cascade order', () => {
    expect(mainCssImports).toEqual(cssFiles);
  });

  it('documents the cascade groups in the main entrypoint', () => {
    expect(mainSource).toContain('Base layers load first');
    expect(mainSource).toContain('Route and feature styles stay in navigation order');
    expect(mainSource).toContain('Late polish layers intentionally override');
  });
});

describe('layer token and overlay host contract', () => {
  it('requires the dedicated layer token source without masking the rest of this suite', () => {
    expect(cssSources.get(LAYER_TOKENS_FILE)).not.toBe('');
  });

  it('imports the layer token source exactly once before every other production stylesheet', () => {
    expect(mainCssImports.filter((file) => file === LAYER_TOKENS_FILE)).toEqual([LAYER_TOKENS_FILE]);
    expect(mainCssImports[0]).toBe(LAYER_TOKENS_FILE);
  });

  it('keeps all 40 active z-index declarations in 12 files on exact known token references', () => {
    const declarations = activeZIndexDeclarations();
    const files = [...new Set(declarations.map((declaration) => declaration.file))].sort();
    const invalid = declarations.filter((declaration) => {
      const match = /^var\((--z-[a-z-]+)\)$/.exec(declaration.value);
      return !match || !EXPECTED_Z_INDEX_TOKENS.has(match[1]);
    });

    expect(declarations).toHaveLength(40);
    expect(files).toEqual(EXPECTED_Z_INDEX_FILES);
    expect(invalid).toEqual([]);
  });

  it('shares one light palette rule between the app shell and overlay host', () => {
    const expectedSelectors = new Set([
      '.sa-window[data-theme="light"]',
      '#overlay-root[data-theme="light"]',
    ]);
    const stylesRoot = postcss.parse(cssSources.get('src/styles.css'), { from: 'src/styles.css' });
    const paletteRules = stylesRoot.nodes.filter((node) => (
      node.type === 'rule'
      && splitSelectors(node.selector).some((selector) => expectedSelectors.has(selector))
    ));

    expect(paletteRules).toHaveLength(1);
    expect(new Set(splitSelectors(paletteRules[0].selector))).toEqual(expectedSelectors);

    const declarations = {};
    paletteRules[0].walkDecls((declaration) => {
      declarations[declaration.prop] = declaration.value;
    });
    expect(declarations['--surface']).toBe('#ffffff');
    expect(declarations['--text-pri']).toBe('#1b1c18');
  });

  it('classifies missing, duplicate, nested, and misordered overlay hosts', () => {
    expect(indexHostViolations('<body><div id="root"></div><div id="overlay-root"></div><script></script></body>')).toEqual([]);
    expect(indexHostViolations('<body><div id="root"></div><script></script></body>')).toContain('overlay-root-count');
    expect(indexHostViolations('<body><div id="root"></div><div id="overlay-root"></div><div id="overlay-root"></div><script></script></body>')).toContain('overlay-root-count');
    expect(indexHostViolations('<body><div id="root"><div id="overlay-root"></div></div><script></script></body>')).toContain('host-sibling');
    expect(indexHostViolations('<body><div id="root"></div><script></script><div id="overlay-root"></div></body>')).toContain('host-order');
  });

  it('requires one root and one overlay-root as body siblings before the module script', () => {
    expect(indexHostViolations(indexSource)).toEqual([]);
  });

  it('keeps html, body, and overlay-root free of accidental stacking contexts', () => {
    expect(forbiddenHostStackingDeclarations()).toEqual([]);
  });

  it('moves every light overlay selector from the app shell to the overlay host', () => {
    const remainingOldSelectors = [];
    const missingHostSelectors = [];

    for (const [oldSelector, hostSelector] of OVERLAY_THEME_SELECTOR_MIGRATIONS) {
      if (selectorOccurrences(oldSelector) !== 0) remainingOldSelectors.push(oldSelector);
      if (selectorOccurrences(hostSelector) !== 1) missingHostSelectors.push(hostSelector);
    }

    expect({ remainingOldSelectors, missingHostSelectors }).toEqual({
      remainingOldSelectors: [],
      missingHostSelectors: [],
    });
  });
});

function mediaDeclarationFor(mediaParams, selector, property) {
  return mediaDeclarationsFor(mediaParams, selector).find((declarations) => (
    Object.prototype.hasOwnProperty.call(declarations, property)
  )) || {};
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

  it('keeps the composer textarea in the screenshot-height input area', () => {
    const textarea = declarationsFor('.composer textarea');
    const floatingTextarea = declarationsFor('.composer--floating textarea');
    const activeTimelineShell = declarationsFor('.conversation:not(.conversation--intro) .timeline-shell');
    const activeTimeline = declarationsFor('.conversation:not(.conversation--intro) .timeline');
    const activeComposer = declarationsFor('.conversation:not(.conversation--intro) .composer--docked');

    expect(textarea['line-height']).toBe('1.5');
    expect(textarea.height).toBe('76px');
    expect(textarea['min-height']).toBe('76px');
    expect(floatingTextarea.height).toBe('76px');
    expect(floatingTextarea['min-height']).toBe('76px');
    expect(textarea['max-height']).toBe('calc(1.5em * 8 + 34px)');
    expect(textarea['overflow-y']).toBe('auto');
    expect(activeTimelineShell['grid-row']).toBe('2');
    expect(activeTimeline.padding).toBe('24px 0 clamp(112px, 16vh, 172px)');
    expect(activeComposer['grid-row']).toBe('3');
  });

  it('renders the Suiyuan floating composer as a raised white input object', () => {
    const floatingCard = declarationsFor('.composer--floating .composer-card');
    const textarea = declarationsFor('.composer--floating textarea');
    const meta = declarationsFor('.composer--floating .composer-meta');
    const send = declarationsFor('.composer .send');
    const disabledSend = declarationsFor('.composer .send:disabled');

    expect(floatingCard.background).toBe('var(--surface)');
    expect(floatingCard['border-radius']).toBe('20px');
    expect(floatingCard['box-shadow']).toContain('var(--suiyuan-input-shadow)');
    expect(textarea.padding).toBe('18px 20px 12px');
    expect(meta['min-height']).toBe('48px');
    expect(send.background).toBe('var(--primary-action-bg)');
    expect(disabledSend.background).toBe('var(--surface-2)');
  });

  it('keeps the fixed floating composer aligned when the product nav collapses', () => {
    const expandedFloatingComposer = topLevelDeclarationsFor('.sa-window .composer.composer--floating[data-file-drop-target]');
    const collapsedFloatingComposer = topLevelDeclarationsFor('.sa-window.sidebar-collapsed .composer.composer--floating[data-file-drop-target]');

    expect(expandedFloatingComposer['--composer-fixed-left']).toBe('var(--suiyuan-sidebar-width, 280px)');
    expect(collapsedFloatingComposer['--composer-fixed-left']).toBe('0px');
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
    const newChat = declarationsFor('.sidebar-new-chat');
    const navIcon = declarationsFor('.app-sidebar-nav button svg');
    const navActive = declarationsFor('.app-sidebar-nav button.active');
    const threadToolIcon = declarationsFor('.thread-tools svg');
    const threadCardIcon = declarationsFor('.thread-card svg');
    const threadPin = declarationsFor('.thread-pin');
    const threadPinHover = declarationsFor('.thread-pin:hover');
    const providerBadge = declarationsFor('.thread-card b');
    const statusLine = declarationsFor('.thread-status-row');
    const statusDot = declarationsFor('.thread-status-dot');

    expect(nav.gap).toBe('10px');
    expect(nav['padding-top']).toBe('0');
    expect(navButton.width).toBe('100%');
    expect(navButton['border-left']).toBeUndefined();
    expect(navActive.background).toBe('var(--sidebar-active)');
    expect(newChat.height).toBe('auto');
    expect(newChat['min-height']).toBe('38px');
    expect(newChat.padding).toBe('8px 10px');
    expect(navIcon.width).toBe('20px');
    expect(navIcon.height).toBe('20px');
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
    const threadDelete = declarationsFor('.thread-delete-trigger');
    const threadRunning = declarationsFor('.thread-running-spinner');
    const threadPinActive = declarationsFor('.thread-pin.active');
    const threadPinFocus = declarationsFor('.thread-pin:focus-visible');

    expect(threadArchive.border).toBe('0');
    expect(threadArchive.background).toBe('transparent');
    expect(threadArchive['box-shadow']).toBe('none');
    expect(threadDelete.border).toBe('0');
    expect(threadDelete.background).toBe('transparent');
    expect(threadDelete.outline).toBe('0');
    expect(threadDelete['box-shadow']).toBe('none');
    expect(threadRunning.display).toBe('inline-grid');
    expect(threadArchiveActive.color).toBe(threadPinActive.color);
    expect(threadArchiveActive.border).toBe('none');
    expect(threadArchiveActive.background).toBe(threadPinActive.background);
    expect(threadArchiveActive['border-color']).toBe(threadPinActive['border-color']);
    expect(threadArchiveActive['border-style']).toBe(threadPinActive['border-style']);
    expect(threadArchiveActive.outline).toBe(threadPinFocus.outline);
    expect(threadArchiveActive['box-shadow']).toBe(threadPinActive['box-shadow']);
    expect(threadArchiveFocus.outline).toBe(threadPinFocus.outline);
    expect(threadArchiveFocus['box-shadow']).toBe('none');
  });

  it('lets thread card actions adapt inside the agent list width', () => {
    const card = declarationsFor('.thread-card');
    const main = declarationsFor('.thread-main');
    const editingMain = declarationsFor('.thread-main--editing');
    const actions = firstDeclarationsFor('.thread-card-actions');
    const archive = declarationsFor('.thread-archive');
    const pin = declarationsFor('.thread-pin');
    const deleteAction = declarationsFor('.thread-delete-trigger');
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
    expect(deleteAction.position).toBe('relative');
    expect(archive.transform).toBe('none');
    expect(pin.transform).toBe('none');
    expect(deleteAction.transform).toBe('none');
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
    const toolbar = declarationsFor('.runtime-toolbar');
    const activityPanel = declarationsFor('.runtime-activity-panel');
    const diffGroup = declarationsFor('.diff-file-group');
    const icons = declarationsFor('.runtime-icons');
    const logs = declarationsFor('.log-lines');
    const tooltipRow = declarationsFor('.runtime-stat-tooltip-row');
    const tooltipName = declarationsFor('.runtime-stat-tooltip-name');
    const logLine = declarationsFor('.warning-log-line');

    expect(panel.position).toBe('relative');
    expect(panel['z-index']).toBe('var(--z-local-sticky)');
    expect(panel.overflow).toBe('hidden');
    expect(panel.background).toBe('var(--surface-2)');
    expect(toolbar.background).toBe('var(--surface-2)');
    expect(diffGroup.background).toBe('var(--surface)');
    expect(activityPanel['border-top']).toBe('1px solid var(--line)');
    expect(activityPanel.background).toBe('var(--surface-2)');
    expect(activityPanel['min-width']).toBe('0');
    expect(activityPanel['max-width']).toBe('100%');
    expect(activityPanel.overflow).toBe('hidden');
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
  describe('suiyuan design tokens', () => {
    it('maps the light theme to exported DESIGN.md tokens', () => {
      const light = declarationsFor('.sa-window[data-theme="light"]');
      const lightSpecific = declarationsFor('.sa-window[data-theme="light"].sa-window');

      expect(light['--bg']).toBe('#fbf9f3');
      expect(light['--bg-elevated']).toBe('#fbf9f3');
      expect(light['--surface']).toBe('#ffffff');
      expect(light['--surface-2']).toBe('#f5f4ed');
      expect(light['--surface-3']).toBe('#f0eee7');
      expect(light['--primary']).toBe('#a03b00');
      expect(light['--primary-2']).toBe('#792b00');
      expect(light['--text-pri']).toBe('#1b1c18');
      expect(light['--text-sec']).toBe('#584238');
      expect(light['--text-muted']).toBe('#8b7268');
      expect(lightSpecific['--bg']).toBe('#fbf9f3');
      expect(lightSpecific['--bg-elevated']).toBe('#fbf9f3');
      expect(lightSpecific['--surface-2']).toBe('#f5f4ed');
      expect(lightSpecific['--surface-3']).toBe('#f0eee7');
      expect(lightSpecific['--text-sec']).toBe('#584238');
    });

    it('keeps the Suiyuan workbench aliases available for shell and chat surfaces', () => {
      const rootTokens = declarationsFor(':root');

      expect(rootTokens['--suiyuan-sidebar-width']).toBe('280px');
      expect(rootTokens['--suiyuan-content-max-width']).toBe('1100px');
      expect(rootTokens['--suiyuan-gutter']).toBe('24px');
      expect(rootTokens['--suiyuan-card-shadow']).toBe('0 20px 40px -10px rgba(0, 0, 0, 0.05)');
      expect(rootTokens['--suiyuan-input-shadow']).toBe('0 8px 30px rgba(0, 0, 0, 0.04)');
      expect(rootTokens['--suiyuan-input-highlight']).toBe('inset 0 1px 0 rgba(255, 255, 255, 0.82)');
    });
  });

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
    const hostLightbox = declarationsFor('#overlay-root .image-lightbox');
    const panel = declarationsFor('.image-lightbox-panel');

    expect(preview.cursor).toBe('zoom-in');
    expect(preview.background).toBe('transparent');
    expect(hint.opacity).toBe('0');
    expect(lightbox.position).toBe('fixed');
    expect(lightbox['z-index']).toBeUndefined();
    expect(hostLightbox['z-index']).toBe('var(--z-overlay-lightbox)');
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

describe('suiyuan chat canvas', () => {
  it('centers the chat canvas with unframed assistant responses and compact user bubbles', () => {
    const conversation = declarationsFor('.conversation');
    const activeConversation = declarationsFor('.conversation:not(.conversation--intro)');
    const timeline = declarationsFor('.timeline');
    const message = declarationsFor('.message');
    const assistantMessage = declarationsFor('.message.assistant');
    const userMessage = declarationsFor('.message.user');
    const assistantBubble = declarationsFor('.message.assistant .bubble');
    const userBubble = declarationsFor('.message.user .bubble');
    const markdownPre = declarationsFor('.message-markdown pre');

    expect(conversation.background).toBe('var(--bg)');
    expect(activeConversation['grid-template-rows']).toBe('auto minmax(0, 1fr) auto');
    expect(timeline['align-items']).toBe('center');
    expect(message.border).toBe('0');
    expect(message.background).toBe('transparent');
    expect(message['box-shadow']).toBe('none');
    expect(assistantMessage.background).toBe('transparent');
    expect(assistantMessage['box-shadow']).toBe('none');
    expect(assistantBubble.width).toBe('100%');
    expect(assistantBubble['max-width']).toBe('min(840px, 100%)');
    expect(userMessage.background).toBe('transparent');
    expect(userBubble.width).toBe('fit-content');
    expect(userBubble.background).toBe('var(--surface-3)');
    expect(markdownPre.background).toBe('var(--surface-code)');
  });
});

describe('suiyuan responsive chat workbench', () => {
  it('collapses side surfaces before the message canvas becomes unreadable', () => {
    const narrowConversation = mediaDeclarationFor('(max-width: 760px)', '.conversation', 'border-right');
    const narrowFloatingComposer = mediaDeclarationFor('(max-width: 760px)', '.composer--floating', 'width');
    const narrowTimeline = mediaDeclarationFor('(max-width: 760px)', '.timeline', 'padding-bottom');

    expect(narrowConversation['border-right']).toBe('0');
    expect(narrowFloatingComposer.width).toBe('calc(100% - 24px)');
    expect(narrowTimeline['padding-bottom']).toBe('clamp(104px, 18vh, 156px)');
  });
});

describe('conversation content column styles', () => {
  it('keeps timeline messages centered while the docked composer fills the footer frame', () => {
    const conversation = declarationsFor('.conversation');
    const activeConversation = declarationsFor('.conversation:not(.conversation--intro)');
    const activeTimelineShell = declarationsFor('.conversation:not(.conversation--intro) .timeline-shell');
    const activeTimeline = declarationsFor('.conversation:not(.conversation--intro) .timeline');
    const timeline = declarationsFor('.timeline');
    const message = declarationsFor('.message');
    const userMessage = declarationsFor('.message.user');
    const userBubble = declarationsFor('.message.user .bubble');
    const composer = declarationsFor('.composer');
    const dockedComposer = declarationsFor('.composer.composer--docked');
    const activeDockedComposer = declarationsFor('.conversation:not(.conversation--intro) .composer.composer--docked');
    const dockedComposerCard = declarationsFor('.composer--docked .composer-card');
    const composerTextarea = declarationsFor('.composer textarea');
    const composerMeta = declarationsFor('.composer-meta');
    const composerAttach = declarationsFor('.composer-attach');
    const composerSend = declarationsFor('.composer .send');
    const scrollButton = declarationsFor('.chat-scroll-bottom-btn');
    const headerTools = declarationsFor('.chat-header-tools');
    const headerTool = declarationsFor('.chat-header-tool');
    const disabledHeaderTool = declarationsFor('.chat-header-tool:disabled');

    expect(conversation['--conversation-content-width']).toBe('min(var(--suiyuan-content-max-width), max(0px, calc(100% - clamp(32px, 7vw, 112px))))');
    expect(activeConversation.display).toBe('grid');
    expect(activeConversation['grid-template-rows']).toBe('auto minmax(0, 1fr) auto');
    expect(activeConversation.overflow).toBe('hidden');
    expect(activeTimelineShell['min-height']).toBe('0');
    expect(activeTimelineShell.overflow).toBe('hidden');
    expect(activeTimeline.height).toBe('100%');
    expect(activeTimeline['overflow-y']).toBe('auto');
    expect(headerTools.display).toBe('inline-flex');
    expect(headerTools['align-items']).toBe('center');
    expect(headerTools.gap).toBe('22px');
    expect(headerTools['margin-top']).toBe('-6px');
    expect(headerTool.width).toBe('32px');
    expect(headerTool.height).toBe('32px');
    expect(headerTool.background).toBe('transparent');
    expect(headerTool.color).toBe('var(--text-sec)');
    expect(disabledHeaderTool.opacity).toBe('1');
    expect(timeline.display).toBe('flex');
    expect(timeline['flex-direction']).toBe('column');
    expect(timeline['align-items']).toBe('center');
    expect(message.width).toBe('var(--conversation-content-width)');
    expect(message.margin).toBe('12px auto');
    expect(userMessage['margin-left']).toBeUndefined();
    expect(userMessage.width).toBe('var(--conversation-content-width)');
    expect(userBubble['margin-left']).toBe('auto');
    expect(userBubble['max-width']).toBe('min(720px, 78%)');
    expect(userBubble.background).toBe('var(--surface-3)');
    expect(userBubble.color).toBe('var(--text-pri)');
    expect(composer.width).toBe('min(900px, max(0px, calc(100% - clamp(24px, 6vw, 96px))))');
    expect(dockedComposer.padding).toBe('0');
    expect(dockedComposer['border-top']).toBe('0');
    expect(dockedComposer.background).toBe('transparent');
    expect(activeDockedComposer.padding).toBe('14px 0 18px');
    expect(activeDockedComposer['border-top']).toBe('0');
    expect(activeDockedComposer.background).toBe('transparent');
    expect(dockedComposerCard.width).toBe('100%');
    expect(dockedComposerCard.margin).toBe('0 auto');
    expect(dockedComposerCard.border).toBe('1px solid color-mix(in srgb, var(--border) 88%, var(--surface))');
    expect(dockedComposerCard['border-radius']).toBe('20px');
    expect(dockedComposerCard['box-shadow']).toBe('var(--composer-shadow)');
    expect(composerTextarea.height).toBe('76px');
    expect(composerTextarea['min-height']).toBe('76px');
    expect(composerMeta['min-height']).toBe('48px');
    expect(composerMeta['flex-wrap']).toBe('nowrap');
    expect(composerAttach.flex).toBe('0 0 36px');
    expect(composerAttach.width).toBe('36px');
    expect(composerAttach['min-width']).toBe('36px');
    expect(composerAttach.height).toBe('36px');
    expect(composerAttach.padding).toBe('0');
    expect(composerSend.flex).toBe('0 0 40px');
    expect(composerSend.width).toBe('40px');
    expect(composerSend['min-width']).toBe('40px');
    expect(composerSend.height).toBe('40px');
    expect(scrollButton.position).toBe('absolute');
    expect(scrollButton.right).toBe('max(18px, var(--conversation-content-right-gutter))');
    expect(scrollButton.bottom).toBe('18px');
    expect(scrollButton.width).toBe('32px');
    expect(scrollButton.height).toBe('32px');
  });

  it('keeps the new-chat intro stage positioned and full width', () => {
    const introConversation = declarationsFor('.conversation--intro');
    const introTimelineShell = declarationsFor('.conversation--intro .timeline-shell');
    const introTimeline = declarationsFor('.conversation--intro .timeline');
    const stitchIntroTimeline = declarationsFor('.sa-window .chat-page.chat-page--intro .conversation--intro .timeline');
    const introStage = declarationsFor('.conversation--intro .intro-chat-stage');
    const introTitle = topLevelDeclarationsFor('.empty-chat h2');
    const scopedIntroTitle = declarationsFor('.conversation--intro .empty-chat h2');
    const introCopy = topLevelDeclarationsFor('.empty-chat p');
    const floatingComposer = declarationsFor('.conversation--intro .composer--floating');
    const stitchScopedFloatingComposer = declarationsFor('.sa-window .chat-page.chat-page--intro .conversation--intro .composer--floating');
    const floatingCard = declarationsFor('.composer--floating .composer-card');

    expect(introConversation['--conversation-intro-width']).toBe('min(920px, max(0px, calc(100% - clamp(24px, 6vw, 88px))))');
    expect(introConversation['--conversation-composer-width']).toBe('min(820px, 100%)');
    expect(introConversation['--conversation-content-width']).toBe('var(--conversation-intro-width)');
    expect(introConversation['--conversation-content-left-nudge']).toBe('0px');
    expect(introTimelineShell.display).toBe('block');
    expect(introTimelineShell.width).toBe('100%');
    expect(introTimelineShell.height).toBe('100%');
    expect(introTimeline.display).toBe('flex');
    expect(introTimeline.width).toBe('100%');
    expect(introTimeline.height).toBe('100%');
    expect(introTimeline['align-items']).toBe('center');
    expect(introTimeline['justify-content']).toBe('flex-start');
    expect(introTimeline.padding).toBe('0');
    expect(stitchIntroTimeline.transform).toBe('none');
    expect(stitchIntroTimeline['-webkit-transform']).toBe('none');
    expect(introStage.width).toBe('min(100%, var(--conversation-intro-width))');
    expect(introStage['max-width']).toBe('100%');
    expect(introStage.display).toBe('flex');
    expect(introStage['justify-content']).toBe('flex-start');
    expect(introStage['min-height']).toBe('0');
    expect(introStage.gap).toBe('clamp(22px, 3.8vh, 38px)');
    expect(introStage['padding-top']).toBe('clamp(116px, 30vh, 340px)');
    expect(introTitle['font-size']).toBe('clamp(30px, 2.75rem, 48px)');
    expect(introTitle['font-weight']).toBe('800');
    expect(introTitle['white-space']).toBe('normal');
    expect(introTitle['overflow-wrap']).toBe('anywhere');
    expect(scopedIntroTitle.margin).toBe('0 auto');
    expect(scopedIntroTitle.width).toBe('min(100%, max-content)');
    expect(scopedIntroTitle['max-width']).toBe('100%');
    expect(scopedIntroTitle.transform).toBe('translateX(clamp(-28px, -2.2vw, 0px))');
    expect(introCopy.display).toBe('none');
    expect(floatingComposer.width).toBe('var(--conversation-composer-width)');
    expect(floatingComposer['max-width']).toBe('100%');
    expect(floatingComposer.padding).toBe('0');
    expect(floatingComposer.background).toBe('transparent');
    expect(stitchScopedFloatingComposer.position).toBeUndefined();
    expect(stitchScopedFloatingComposer.width).toBeUndefined();
    expect(stitchScopedFloatingComposer['max-width']).toBeUndefined();
    expect(stitchScopedFloatingComposer['pointer-events']).toBeUndefined();
    expect(stitchScopedFloatingComposer['z-index']).toBe('var(--z-local-sticky)');
    expect(floatingCard.width).toBe('100%');
    expect(floatingCard.margin).toBe('0 auto');
  });

  it('keeps the light new-chat composer on the dark-mode geometry', () => {
    const floating = topLevelDeclarationsFor('.composer--floating');
    const sharedCard = topLevelDeclarationsFor('.composer.composer--floating[data-file-drop-target] .composer-card');
    const sharedTextarea = topLevelDeclarationsFor('.composer.composer--floating[data-file-drop-target] textarea');
    const sharedMeta = topLevelDeclarationsFor('.composer.composer--floating[data-file-drop-target] .composer-meta');
    const sharedAttach = topLevelDeclarationsFor('.composer.composer--floating[data-file-drop-target] .composer-attach');
    const sharedContext = topLevelDeclarationsFor('.composer.composer--floating[data-file-drop-target] .composer-context');
    const sharedModel = topLevelDeclarationsFor('.composer.composer--floating[data-file-drop-target] .composer-model');
    const lightCard = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer--floating .composer-card');
    const lightAttach = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer--floating .composer-attach');
    const lightTextarea = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer.composer--floating[data-file-drop-target] textarea');
    const lightMeta = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer.composer--floating[data-file-drop-target] .composer-meta');
    const lightContext = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer.composer--floating[data-file-drop-target] .composer-context');
    const lightModel = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer.composer--floating[data-file-drop-target] .composer-model');
    const disabledSend = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer.composer--floating[data-file-drop-target] .send:disabled');
    const disclaimer = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer-disclaimer');
    const track = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer--floating .provider-track');
    const darkFloating = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .composer.composer--floating[data-file-drop-target]');
    const darkCard = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .composer.composer--floating[data-file-drop-target] .composer-card');
    const darkTextarea = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .composer.composer--floating[data-file-drop-target] textarea');

    expect(floating['--composer-floating-max-width']).toBe('800px');
    expect(floating['--composer-floating-bottom-gap']).toBe('22px');
    expect(sharedCard.padding).toBe('0');
    expect(sharedCard['border-radius']).toBe('20px');
    expect(sharedTextarea.height).toBe('76px');
    expect(sharedTextarea['min-height']).toBe('76px');
    expect(sharedMeta['min-height']).toBe('48px');
    expect(sharedMeta['flex-wrap']).toBe('nowrap');
    expect(sharedAttach.flex).toBe('0 0 36px');
    expect(sharedAttach.width).toBe('36px');
    expect(sharedAttach['min-width']).toBe('36px');
    expect(sharedAttach.height).toBe('36px');
    expect(sharedAttach['min-height']).toBe('36px');
    expect(sharedAttach.padding).toBe('0');
    expect(sharedContext.display).toBeUndefined();
    expect(sharedModel.height).toBe('34px');
    expect(sharedModel['min-height']).toBe('34px');
    for (const property of ['height', 'min-height', 'padding', 'border-radius']) {
      expect(lightTextarea[property]).toBeUndefined();
    }
    for (const property of ['font-size', 'line-height']) {
      expect(lightTextarea[property]).toBeUndefined();
    }
    for (const property of ['height', 'min-height', 'margin-top', 'padding']) {
      expect(lightMeta[property]).toBeUndefined();
    }
    expect(lightContext.display).toBeUndefined();
    for (const property of ['width', 'height', 'min-height', 'padding']) {
      expect(lightAttach[property]).toBeUndefined();
    }
    for (const property of ['min-width', 'height', 'min-height']) {
      expect(lightModel[property]).toBeUndefined();
    }
    expect(darkFloating['--composer-floating-max-width']).toBeUndefined();
    expect(darkFloating['--composer-floating-bottom-gap']).toBeUndefined();
    expect(darkCard.padding).toBeUndefined();
    expect(darkCard['border-radius']).toBeUndefined();
    expect(darkTextarea.height).toBeUndefined();
    expect(darkTextarea['min-height']).toBeUndefined();
    expect(lightCard.background).toBe('var(--surface)');
    expect(lightCard['border-color']).toBe('var(--suiyuan-surface-variant)');
    expect(lightCard['box-shadow']).toBe('var(--suiyuan-input-shadow)');
    expect(lightAttach.background).toBe('var(--suiyuan-surface-low)');
    expect(lightAttach['border-color']).toBe('var(--suiyuan-surface-variant)');
    expect(lightModel.background).toBe('color-mix(in srgb, var(--suiyuan-primary-fixed) 10%, transparent)');
    expect(disabledSend.background).toBe('var(--suiyuan-primary)');
    expect(disabledSend.color).toBe('var(--on-accent)');
    expect(disclaimer.color).toBe('var(--suiyuan-secondary-fixed-dim)');
    expect(disclaimer.margin).toBeUndefined();
    expect(disclaimer['font-weight']).toBeUndefined();
    expect(disclaimer['line-height']).toBeUndefined();
    expect(track.background).toBe('color-mix(in srgb, var(--surface-3) 72%, var(--border))');
  });

  it('keeps the Suiyuan intro and floating composer dark when the shell theme is dark', () => {
    const intro = declarationsFor('.sa-window[data-theme="dark"] .chat-page.chat-page--intro');
    const darkCard = declarationsFor('.sa-window[data-theme="dark"] .chat-intro-card');
    const composerBackdrop = declarationsFor('.sa-window[data-theme="dark"] .composer.composer--floating[data-file-drop-target]');
    const composerCard = declarationsFor('.sa-window[data-theme="dark"] .composer.composer--floating[data-file-drop-target] .composer-card');

    expect(intro.background).toContain('var(--suiyuan-surface-bright)');
    expect(darkCard.background).toContain('var(--suiyuan-surface-lowest)');
    expect(composerBackdrop.background).toContain('var(--bg)');
    expect(composerCard.background).toContain('var(--suiyuan-surface-low)');
    expect(composerCard['border-color']).toContain('var(--suiyuan-outline-variant)');
  });

  it('keeps light and dark intro geometry structurally isomorphic', () => {
    const spotlight = topLevelDeclarationsFor('.chat-intro-spotlight');
    const spotlightInner = topLevelDeclarationsFor('.chat-intro-spotlight__inner');
    const logoTile = topLevelDeclarationsFor('.chat-intro-logo-tile');
    const introTitle = topLevelDeclarationsFor('.chat-intro-title');
    const introSubtitle = topLevelDeclarationsFor('.chat-intro-subtitle');
    const suggestions = topLevelDeclarationsFor('.chat-intro-suggestions');
    const card = topLevelDeclarationsFor('.chat-intro-card');
    const cardIcon = topLevelDeclarationsFor('.chat-intro-card__icon');
    const cardIconSvg = topLevelDeclarationsFor('.chat-intro-card__icon svg');
    const cardTitle = topLevelDeclarationsFor('.chat-intro-card__title');
    const cardDescription = topLevelDeclarationsFor('.chat-intro-card__description');
    const darkSpotlight = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-spotlight');
    const darkCard = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-card');
    const darkTitle = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-title');
    const darkSuggestions = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-suggestions');
    const darkIcon = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-card__icon');
    const darkCardTitle = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-card__title');
    const darkCardDescription = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-card__description');
    const mobileSpotlight = mediaDeclarationFor('(max-width: 640px)', '.chat-intro-spotlight', 'overflow-y');
    const mobileInner = mediaDeclarationFor('(max-width: 640px)', '.chat-intro-spotlight__inner', 'justify-content');
    const mobileCard = mediaDeclarationFor('(max-width: 640px)', '.chat-intro-card', 'min-height');
    const mobileIcon = mediaDeclarationFor('(max-width: 640px)', '.chat-intro-card__icon', 'width');
    const mobileTitle = mediaDeclarationFor('(max-width: 640px)', '.chat-intro-card__title', 'margin');
    const mobileDescription = mediaDeclarationFor('(max-width: 640px)', '.chat-intro-card__description', 'line-height');

    expect(spotlight.inset).toBe('64px 0 0');
    expect(spotlight.padding).toBe('0 32px');
    expect(spotlightInner.height).toBe('auto');
    expect(logoTile.display).toBe('none');
    expect(introTitle.margin).toBe('0');
    expect(introTitle['font-weight']).toBe('650');
    expect(introSubtitle.display).toBe('none');
    expect(suggestions.width).toBe('min(900px, 100%)');
    expect(suggestions['margin-top']).toBe('48px');
    expect(suggestions['margin-bottom']).toBe('128px');
    expect(card['min-height']).toBe('174px');
    expect(card.gap).toBe('8px');
    expect(card['border-color']).toBe('var(--suiyuan-surface-variant)');
    expect(card.padding).toBe('24px');
    expect(cardIcon.width).toBe('36px');
    expect(cardIcon.height).toBe('36px');
    expect(cardIcon['margin-bottom']).toBe('0');
    expect(cardIconSvg.width).toBe('17px');
    expect(cardIconSvg.height).toBe('17px');
    expect(cardTitle.margin).toBe('8px 0 0');
    expect(cardTitle['font-weight']).toBe('700');
    expect(cardDescription['min-height']).toBe('0');
    expect(cardDescription.color).toBe('var(--suiyuan-secondary)');
    expect(cardDescription['font-weight']).toBe('500');
    expect(cardDescription['line-height']).toBe('16px');
    expect(darkSpotlight.inset).toBeUndefined();
    expect(darkSpotlight.padding).toBeUndefined();
    expect(darkTitle['font-weight']).toBeUndefined();
    expect(darkSuggestions.width).toBeUndefined();
    expect(darkSuggestions['margin-top']).toBeUndefined();
    expect(darkSuggestions['margin-bottom']).toBeUndefined();
    expect(darkCard['min-height']).toBeUndefined();
    expect(darkCard.gap).toBeUndefined();
    expect(darkIcon.width).toBeUndefined();
    expect(darkIcon.height).toBeUndefined();
    expect(darkIcon['margin-bottom']).toBeUndefined();
    expect(darkCardTitle.margin).toBeUndefined();
    expect(darkCardTitle['font-weight']).toBeUndefined();
    expect(darkCardDescription['min-height']).toBeUndefined();
    expect(darkCardDescription['font-weight']).toBeUndefined();
    expect(darkCardDescription['line-height']).toBeUndefined();
    expect(css).not.toContain('.composer-attach-label');
    expect(css).not.toContain('content: "附件"');
    expect(mobileSpotlight['overflow-y']).toBe('auto');
    expect(mobileSpotlight.padding).toBe('24px 16px 270px');
    expect(mobileInner['justify-content']).toBe('flex-start');
    expect(mobileCard['min-height']).toBe('92px');
    expect(mobileCard.gap).toBe('5px');
    expect(mobileIcon.width).toBe('34px');
    expect(mobileIcon.height).toBe('34px');
    expect(mobileTitle.margin).toBe('4px 0 0');
    expect(mobileDescription['line-height']).toBe('15px');
  });

  it('keeps the composer toolbar on one row at tablet and mobile widths', () => {
    const tabletMeta = mediaDeclarationFor('(max-width: 920px)', '.sa-window .composer-meta', 'flex-wrap');
    const tabletActions = mediaDeclarationFor('(max-width: 920px)', '.sa-window .composer-actions', 'width');
    const mobileMeta = mediaDeclarationFor('(max-width: 640px)', '.sa-window .composer-meta', 'display');
    const mobileAttach = mediaDeclarationFor('(max-width: 640px)', '.sa-window .composer-attach', 'width');
    const mobileActions = mediaDeclarationFor('(max-width: 640px)', '.sa-window .composer-actions', 'display');

    expect(tabletMeta['flex-wrap']).toBe('nowrap');
    expect(tabletActions.width).toBe('auto');
    expect(mobileMeta.display).toBe('flex');
    expect(mobileMeta['flex-wrap']).toBe('nowrap');
    expect(mobileAttach.width).toBe('36px');
    expect(mobileActions.display).toBe('inline-flex');
  });
});

describe('workbench shell styles', () => {
  describe('suiyuan shell layout', () => {
    it('uses warm Suiyuan surfaces for the app shell and primary navigation', () => {
      const navRail = topLevelDeclarationsFor('.app-sidebar.suiyuan-sidebar');
      const activeNav = declarationsFor('.suiyuan-nav-item.active');
      const activeIndicator = declarationsFor('.suiyuan-nav-item.active::before');
      const topCommand = topLevelDeclarationsFor('.suiyuan-top-appbar');
      const mobileTopCommand = mediaDeclarationFor('(max-width: 920px)', '.suiyuan-top-appbar', 'padding');
      const mainCanvas = declarationsFor('.suiyuan-main-canvas');
      const nonChatPage = declarationsFor('.sa-window.suiyuan-shell .suiyuan-main-canvas > .memory-page');
      const skillsPage = declarationsFor('.sa-window.suiyuan-shell .suiyuan-main-canvas > .skills-tabbed-container');
      const main = declarationsFor('.sa-main');

      expect(navRail.background).toBe('var(--sidebar-bg)');
      expect(activeNav.background).toBe('var(--primary-soft)');
      expect(activeNav.color).toBe('var(--text-pri)');
      expect(activeIndicator.width).toBe('4px');
      expect(topCommand.position).toBe('absolute');
      expect(topCommand.height).toBe('64px');
      expect(topCommand.padding).toBe('0 24px');
      expect(mobileTopCommand.padding).toBe('0 14px 0 64px');
      expect(topCommand.background).toBe('var(--bg)');
      expect(topCommand['border-bottom']).toBe('0');
      expect(mainCanvas.height).toBe('100%');
      expect(nonChatPage['padding-top']).toBe('64px');
      expect(skillsPage['padding-top']).toBe('64px');
      expect(main.background).toBe('var(--bg)');
    });

    it('keeps the light sidebar on the dark-mode geometry', () => {
      const sharedSidebar = topLevelDeclarationsFor('.app-sidebar.suiyuan-sidebar');
      const sharedBrand = topLevelDeclarationsFor('.suiyuan-brand-block');
      const sharedBrandTitle = topLevelDeclarationsFor('.suiyuan-brand-meta strong');
      const sharedNewChat = topLevelDeclarationsFor('.suiyuan-new-chat');
      const sharedNav = topLevelDeclarationsFor('.suiyuan-nav');
      const sharedNavItem = topLevelDeclarationsFor('.suiyuan-nav-item');
      const lightSidebar = declarationsFor('.sa-window[data-theme="light"] .app-sidebar.suiyuan-sidebar');
      const lightBrand = declarationsFor('.sa-window[data-theme="light"] .suiyuan-brand-block');
      const lightMark = declarationsFor('.sa-window[data-theme="light"] .suiyuan-brand-light-mark');
      const lightDarkMark = declarationsFor('.sa-window[data-theme="light"] .suiyuan-brand-dark-mark');
      const lightBrandTitle = declarationsFor('.sa-window[data-theme="light"] .suiyuan-brand-meta strong');
      const lightNewChat = declarationsFor('.sa-window[data-theme="light"] .suiyuan-new-chat');
      const lightNav = declarationsFor('.sa-window[data-theme="light"] .suiyuan-nav');
      const lightNavItem = declarationsFor('.sa-window[data-theme="light"] .suiyuan-nav-item');
      const activeNav = declarationsFor('.sa-window[data-theme="light"] .suiyuan-nav-item.active');
      const activeIndicator = declarationsFor('.sa-window[data-theme="light"] .suiyuan-nav-item.active::before');
      const appbarTitle = topLevelDeclarationsFor('.suiyuan-appbar-title h1');

      expect(sharedSidebar.gap).toBe('20px');
      expect(sharedSidebar.padding).toBe('24px 18px 18px');
      expect(sharedBrand['min-height']).toBe('42px');
      expect(sharedBrand.gap).toBe('12px');
      expect(sharedBrandTitle['font-size']).toBe('17px');
      expect(sharedNewChat.width).toBe('100%');
      expect(sharedNewChat['min-height']).toBe('40px');
      expect(sharedNav.gap).toBe('6px');
      expect(sharedNavItem['min-height']).toBe('34px');
      expect(sharedNavItem.gap).toBe('10px');
      expect(sharedNavItem.padding).toBe('0 12px 0 14px');
      expect(sharedNavItem['font-size']).toBe('13px');
      expect(sharedNavItem['font-weight']).toBe('620');
      expect(sharedNavItem['line-height']).toBe('18px');
      expect(appbarTitle['line-height']).toBe('1.25');
      for (const property of ['gap', 'padding']) expect(lightSidebar[property]).toBeUndefined();
      for (const property of ['width', 'min-height', 'margin', 'gap']) expect(lightBrand[property]).toBeUndefined();
      for (const property of ['width', 'height', 'display']) expect(lightMark[property]).toBeUndefined();
      expect(lightDarkMark.display).toBeUndefined();
      expect(lightBrandTitle['font-size']).toBeUndefined();
      expect(lightBrandTitle['font-weight']).toBeUndefined();
      for (const property of ['width', 'min-height', 'margin']) {
        expect(lightNewChat[property]).toBeUndefined();
      }
      for (const property of ['font-size', 'font-weight', 'line-height']) {
        expect(lightNewChat[property]).toBeUndefined();
      }
      for (const property of ['width', 'margin', 'gap']) expect(lightNav[property]).toBeUndefined();
      for (const property of ['min-height', 'gap', 'padding']) {
        expect(lightNavItem[property]).toBeUndefined();
      }
      for (const property of ['font-size', 'font-weight', 'line-height']) {
        expect(lightNavItem[property]).toBeUndefined();
      }
      expect(lightNewChat.background).toBe('var(--suiyuan-primary)');
      expect(lightNewChat['box-shadow']).toBe('none');
      expect(lightNewChat.opacity).toBe('0.8');
      expect(activeNav.color).toBe('var(--suiyuan-primary)');
      expect(activeNav['font-weight']).toBeUndefined();
      expect(activeIndicator.inset).toBeUndefined();
    });

    it('keeps marketing tabs and upgrade CTAs out of the Suiyuan app shell', () => {
      expect(appSource).not.toContain('SUIYUAN_APP_TABS');
      expect(appSource).not.toContain("label: 'Overview'");
      expect(appSource).not.toContain("label: 'Usage'");
      expect(appSource).not.toContain("label: 'Limits'");
      expect(appSource).not.toContain('Upgrade Plan');
      expect(appSource).not.toContain('Support');
      expect(css).not.toContain('suiyuan-upgrade-action');
    });

    it('maps Suiyuan design tokens to dark surfaces in dark mode', () => {
      const darkShell = declarationsFor('.sa-window.suiyuan-shell[data-theme="dark"]');

      expect(darkShell['--suiyuan-background']).toBe('#131411');
      expect(darkShell['--suiyuan-surface-bright']).toBe('#131411');
      expect(darkShell['--suiyuan-surface-lowest']).toBe('#1b1c18');
      expect(darkShell['--suiyuan-surface-low']).toBe('#1e1f1b');
      expect(darkShell['--suiyuan-on-surface']).toBe('#e5e2da');
      expect(darkShell['--suiyuan-primary']).toBe('#ffb597');
      expect(darkShell['--suiyuan-card-shadow']).toBe('0 20px 40px -10px rgba(0, 0, 0, 0.3)');
      expect(darkShell['--suiyuan-input-shadow']).toBe('0 8px 30px rgba(0, 0, 0, 0.2)');
    });

    it('renders memory controls as compact Suiyuan components', () => {
      const stats = topLevelDeclarationsFor('.memory-page .memory-stats');
      const panel = declarationsFor('.memory-page .memory-stats .panel');
      const overviewChip = declarationsFor('.memory-page .memory-overview-breakdown > span');
      const autoToggle = topLevelDeclarationsFor('.memory-page .memory-auto-dream-toggle');
      const createButton = declarationsFor('.memory-page .memory-create-button');
      const createMenu = declarationsFor('.memory-page .memory-create-menu');

      expect(stats['grid-template-columns']).toBe('repeat(2, minmax(280px, 1fr))');
      expect(panel['border-radius']).toBe('8px');
      expect(overviewChip['border-radius']).toBe('999px');
      expect(autoToggle['grid-column']).toBe('2');
      expect(createButton.background).toBe('var(--primary-action-bg)');
      expect(createButton['border-radius']).toBe('8px');
      expect(createMenu.left).toBe('0');
      expect(createMenu.right).toBe('auto');
      expect(createMenu.width).toBe('max-content');
    });
  });

  it('keeps the screenshot-style sidebar fixed and branded', () => {
    const sidebar = topLevelDeclarationsFor('.app-sidebar.suiyuan-sidebar');
    const body = topLevelDeclarationsFor('.sa-body.suiyuan-shell-body');
    const brand = declarationsFor('.suiyuan-brand-block');
    const brandMeta = declarationsFor('.suiyuan-brand-meta');
    const newChat = declarationsFor('.suiyuan-new-chat');
    const nav = declarationsFor('.suiyuan-nav');
    const chatNavGroup = declarationsFor('.suiyuan-chat-nav-group');
    const projectTree = declarationsFor('.suiyuan-chat-project-tree');
    const collapseButton = declarationsFor('.suiyuan-sidebar-collapse');
    const footer = declarationsFor('.suiyuan-sidebar-footer');

    expect(sidebar.width).toBe('280px');
    expect(sidebar.position).toBe('relative');
    expect(sidebar.background).toBe('var(--sidebar-bg)');
    expect(sidebar['border-right']).toBe('1px solid var(--sidebar-border)');
    expect(sidebar.overflow).toBe('hidden');
    expect(body.height).toBe('100vh');
    expect(body['grid-template-columns']).toBe('280px minmax(0, 1fr)');
    expect(brand.display).toBe('flex');
    expect(brandMeta.display).toBe('grid');
    expect(newChat['min-height']).toBe('40px');
    expect(newChat.background).toBe('var(--primary-action-bg)');
    expect(nav.display).toBe('grid');
    expect(chatNavGroup.display).toBe('grid');
    expect(chatNavGroup['min-height']).toBe('0');
    expect(projectTree['max-height']).toBe('min(300px, 32vh)');
    expect(projectTree['overflow-y']).toBe('auto');
    expect(projectTree['overscroll-behavior']).toBe('contain');
    expect(projectTree['scrollbar-gutter']).toBe('stable');
    expect(collapseButton.width).toBe('32px');
    expect(collapseButton.height).toBe('32px');
    expect(collapseButton['margin-left']).toBe('auto');
    expect(footer['margin-top']).toBe('auto');
  });

  it('keeps the primary product nav while nesting projects under Chat', () => {
    expect(appSource).toContain('<ChatSidebarProjectTree');
    expect(appSource).not.toContain('<SidebarTaskSummary');
    expect(appSource).toContain("label: 'Chat'");
    expect(appSource).toContain("label: 'Plugins'");
    expect(appSource).toContain("label: 'Automation'");
    expect(appSource).toContain("label: 'Roles'");
    expect(appSource).toContain("label: 'Files'");
    expect(appSource).toContain("label: 'Memory'");
    expect(appSource).toContain("label: 'Logs'");
  });

  it('exposes a mobile workbench drawer so settings remains reachable', () => {
    const desktopToggle = topLevelDeclarationsFor('.workbench-toggle');
    const mobileToggle = mediaDeclarationsFor('(max-width: 920px)', '.workbench-toggle')[0];
    const mobileSidebar = mediaDeclarationsFor('(max-width: 920px)', '.app-sidebar')[0];
    const openSidebar = mediaDeclarationsFor('(max-width: 920px)', '.app-sidebar.is-open')[0];
    const mobileResizer = mediaDeclarationsFor('(max-width: 920px)', '.workbench-sidebar-resizer')[0];
    const scrim = mediaDeclarationsFor('(max-width: 920px)', '.sidebar-scrim')[0];
    const mobileSettings = mediaDeclarationsFor('(max-width: 920px)', '.sa-window .settings-page')[0];

    expect(desktopToggle.display).toBe('none');
    expect(mobileToggle.display).toBe('inline-flex');
    expect(mobileToggle.position).toBe('fixed');
    expect(mobileToggle['z-index']).toBe('var(--z-shell-control)');
    expect(mobileSidebar.position).toBe('fixed');
    expect(mobileSidebar['--workbench-sidebar-width']).toBe('min(320px, calc(100vw - 52px))');
    expect(mobileSidebar.width).toBe('var(--workbench-sidebar-width)');
    expect(mobileSidebar['margin-left']).toBe('calc(-1 * var(--workbench-sidebar-width))');
    expect(mobileSidebar.transform).toBe('none');
    expect(mobileSidebar.transition).toBe('margin-left 180ms ease');
    expect(mobileSidebar['max-width']).toBe('var(--workbench-sidebar-width)');
    expect(mobileSidebar['box-shadow']).toBe('none');
    expect(openSidebar['margin-left']).toBe('0');
    expect(openSidebar.transform).toBe('none');
    expect(openSidebar['box-shadow']).toBe('var(--shadow)');
    expect(mobileResizer.display).toBe('none');
    expect(scrim.display).toBe('block');
    expect(mobileSettings['padding-top']).toBe('78px');
  });

  it('keeps the chat composer adaptive across desktop client widths and phones', () => {
    const mediumConversation = mediaDeclarationsFor('(max-width: 1180px)', '.sa-window .conversation')[0];
    const mediumComposer = mediaDeclarationsFor('(max-width: 1180px)', '.sa-window .composer')[0];
    const mediumActions = mediaDeclarationsFor('(max-width: 1180px)', '.sa-window .composer-actions')[0];
    const mediumModel = mediaDeclarationsFor('(max-width: 1180px)', '.sa-window .composer-model')[0];
    const tabletTitle = mediaDeclarationsFor('(max-width: 920px)', '.empty-chat h2')[0];
    const mobileMeta = mediaDeclarationsFor('(max-width: 640px)', '.sa-window .composer-meta')[0];
    const mobileProject = mediaDeclarationFor('(max-width: 640px)', '.sa-window .composer-meta .project-select-wrap', 'width');
    const mobileActions = mediaDeclarationFor('(max-width: 640px)', '.sa-window .composer-actions', 'display');
    const mobileModel = mediaDeclarationFor('(max-width: 640px)', '.sa-window .composer-model', 'max-width');
    const mobileSend = mediaDeclarationsFor('(max-width: 640px)', '.sa-window .composer .send')[0];

    expect(mediumConversation['--conversation-content-width']).toBe('min(100%, calc(100% - 44px))');
    expect(mediumComposer.width).toBe('min(100%, calc(100% - 44px))');
    expect(mediumActions['min-width']).toBe('0');
    expect(mediumModel['min-width']).toBe('0');
    expect(tabletTitle['white-space']).toBe('normal');
    expect(tabletTitle['overflow-wrap']).toBe('anywhere');
    expect(mobileMeta.display).toBe('flex');
    expect(mobileMeta['flex-wrap']).toBe('nowrap');
    expect(mobileProject.width).toBe('auto');
    expect(mobileActions.display).toBe('inline-flex');
    expect(mobileModel['max-width']).toBe('100%');
    expect(mobileSend.width).toBe('40px');
    expect(mobileSend['min-width']).toBe('40px');
  });
});

describe('composer control styles', () => {
  it('gives the portalled project menu an opaque component-owned surface', () => {
    const trigger = topLevelDeclarationsFor('.sa-window .composer-meta .project-select');
    const popover = topLevelDeclarationsFor('.project-selector-popover');
    const hostPopover = topLevelDeclarationsFor('#overlay-root .project-selector-popover');
    const menu = topLevelDeclarationsFor('.project-dropdown');
    const row = topLevelDeclarationsFor('.project-dropdown-row');

    expect(trigger.background).toBe('var(--surface)');
    expect(trigger['box-shadow']).toBe('var(--suiyuan-input-highlight)');
    expect(popover.background).toBe('var(--surface)');
    expect(popover.border).toBe('1px solid var(--border)');
    expect(popover['border-radius']).toBe('8px');
    expect(popover['box-shadow']).toBe('var(--shadow)');
    expect(hostPopover['z-index']).toBe('var(--z-overlay-popover)');
    expect(menu.display).toBe('grid');
    expect(menu['overflow-y']).toBe('auto');
    expect(row.display).toBe('grid');
    expect(row['grid-template-columns']).toBe('minmax(0, 1fr) 32px');
  });

  it('keeps the composer project selector clickable without displacing send controls', () => {
    const wrap = topLevelDeclarationsFor('.composer-meta .project-select-wrap');
    const button = topLevelDeclarationsFor('.composer-meta .project-select');
    const label = topLevelDeclarationsFor('.composer-meta .project-select span');

    expect(wrap.flex).toBe('0 1 210px');
    expect(wrap['min-width']).toBe('0');
    expect(wrap['max-width']).toBe('210px');
    expect(button.width).toBe('100%');
    expect(button['max-width']).toBe('100%');
    expect(label['min-width']).toBe('0');
    expect(label.overflow).toBe('hidden');
    expect(label['text-overflow']).toBe('ellipsis');
    expect(label['white-space']).toBe('nowrap');
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
    const wrap = topLevelDeclarationsFor('.composer-model-wrap');
    const button = topLevelDeclarationsFor('.composer-model');
    const dropdown = topLevelDeclarationsFor('.model-dropdown');

    expect(card.overflow).toBe('visible');
    expect(wrap.position).toBe('relative');
    expect(wrap.width).toBe('auto');
    expect(wrap['max-width']).toBe('min(210px, 100%)');
    expect(button.width).toBe('100%');
    expect(button.padding).toBe('0 12px');
    expect(dropdown.position).toBe('absolute');
    expect(dropdown.inset).toBe('auto 0 calc(100% + 8px) auto');
    expect(dropdown.bottom).toBe('calc(100% + 8px)');
    expect(dropdown.height).toBe('max-content');
    expect(dropdown['max-height']).toBe('min(320px, calc(100vh - 48px))');
    expect(dropdown['grid-auto-rows']).toBe('max-content');
    expect(dropdown['align-content']).toBe('start');
    expect(dropdown.overflow).toBe('visible');
  });

  it('keeps the workbench composer send button visible when model text is long', () => {
    const actions = topLevelDeclarationsFor('.composer-actions');
    const wrap = topLevelDeclarationsFor('.composer-model-wrap');
    const button = topLevelDeclarationsFor('.composer-model');
    const label = topLevelDeclarationsFor('.composer-model span');
    const send = topLevelDeclarationsFor('.composer .send');

    expect(actions['min-width']).toBe('0');
    expect(actions.flex).toBe('1 1 0');
    expect(actions['justify-content']).toBe('flex-end');
    expect(wrap.flex).toBe('1 1 auto');
    expect(wrap['min-width']).toBe('0');
    expect(wrap['max-width']).toBe('min(210px, 100%)');
    expect(button.width).toBe('100%');
    expect(button['min-width']).toBe('0');
    expect(button['max-width']).toBe('100%');
    expect(label['min-width']).toBe('0');
    expect(label.overflow).toBe('hidden');
    expect(label['text-overflow']).toBe('ellipsis');
    expect(send.flex).toBe('0 0 40px');
    expect(send['min-width']).toBe('40px');
  });

  it('keeps attachment controls left while model controls sit on the right', () => {
    const meta = topLevelDeclarationsFor('.composer-meta');
    const actions = topLevelDeclarationsFor('.composer-actions');
    const provider = firstDeclarationsFor('.composer .provider');

    expect(meta['align-items']).toBe('center');
    expect(meta.gap).toBe('8px');
    expect(actions['margin-left']).toBe('auto');
    expect(actions['justify-content']).toBe('flex-end');
    expect(actions['padding-left']).toBe('0');
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
    const hostTooltip = declarationsFor('#overlay-root .runtime-stat-tooltip');
    const hostWarningPopover = declarationsFor('#overlay-root .warning-log-popover');

    expect(panel['--activity-panel-height']).toBe('64px');
    expect(panel['--activity-panel-min-height']).toBe('64px');
    expect(panel.overflow).toBe('hidden');
    expect(panel['grid-template-rows']).toContain('var(--activity-panel-height)');
    expect(activity.overflow).toBe('hidden');
    expect(activity.height).toBe('var(--activity-panel-height)');
    expect(collapsedActivity['grid-template-rows']).toBe('minmax(0, 1fr)');
    expect(collapsedIcons.height).toBe('100%');
    expect(collapsedIcons['border-bottom']).toBe('0');
    expect(diff['z-index']).toBe('var(--z-local-raised)');
    expect(activity['z-index']).toBe('var(--z-local-sticky)');
    expect(tooltip.position).toBe('fixed');
    expect(tooltip.left).toBe('var(--runtime-stat-tooltip-left, 12px)');
    expect(tooltip['max-height']).toBe('var(--runtime-stat-tooltip-max-height, min(280px, 42vh))');
    expect(tooltip['z-index']).toBeUndefined();
    expect(warningPopover['z-index']).toBeUndefined();
    expect(hostTooltip['z-index']).toBe('var(--z-overlay-dialog)');
    expect(hostWarningPopover['z-index']).toBe('var(--z-overlay-popover)');
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
    const diffFileLinesVirtual = declarationsFor('.diff-file-lines-virtual');
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
    expect(diffFileLines['max-height']).toBe('420px');
    expect(diffFileLines.overflow).toBe('auto');
    expect(diffFileLines.position).toBe('relative');
    expect(diffFileLinesVirtual.position).toBe('relative');
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
    const hostPopover = declarationsFor('#overlay-root .warning-log-popover');
    const code = declarationsFor('.warning-log-popover code');

    expect(line['white-space']).toBe('nowrap');
    expect(popover.position).toBe('fixed');
    expect(popover['box-sizing']).toBe('border-box');
    expect(popover['min-width']).toBe('0');
    expect(popover.left).toBe('var(--warning-log-popover-left, 12px)');
    expect(popover.right).toBe('var(--warning-log-popover-right, 12px)');
    expect(popover['pointer-events']).toBe('auto');
    expect(popover['z-index']).toBeUndefined();
    expect(hostPopover['z-index']).toBe('var(--z-overlay-popover)');
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
    const button = declarationsFor('.sa-window[data-theme="light"] .runtime-toolbar button');
    const score = declarationsFor('.sa-window[data-theme="light"] .score');
    const goodScore = declarationsFor('.sa-window[data-theme="light"] .score.good');
    const badScore = declarationsFor('.sa-window[data-theme="light"] .score.bad');

    expect(toolbar.background).toBe('var(--surface-2)');
    expect(toolbar['border-bottom-color']).toBe('var(--line)');
    expect(button.background).toBe('var(--surface)');
    expect(button.color).toBe('var(--text-sec)');
    expect(button['border-color']).toBe('var(--line)');
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

  it('defines one root dark token contract plus one light override contract', () => {
    const themeRootSelectors = [];
    root.walkRules((rule) => {
      if (rule.selector === ':root' && rule.nodes.some((node) => node.type === 'decl' && node.prop === '--bg')) {
        themeRootSelectors.push(rule);
      }
    });

    const dark = declarationsFor(':root');
    const light = declarationsFor('.sa-window[data-theme="light"]');

    expect(themeRootSelectors).toHaveLength(1);
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
    const tokens = declarationsFor(':root');
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
