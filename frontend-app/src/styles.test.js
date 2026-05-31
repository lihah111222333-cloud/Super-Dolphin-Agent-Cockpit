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

describe('composer layout styles', () => {
  it('lets the model selector popover escape the adaptive composer card', () => {
    const card = declarationsFor('.composer-card');
    const wrap = declarationsFor('.composer-model-wrap');
    const dropdown = declarationsFor('.model-dropdown');

    expect(card.overflow).toBe('visible');
    expect(wrap.position).toBe('relative');
    expect(dropdown.position).toBe('absolute');
    expect(dropdown.bottom).toBe('calc(100% + 8px)');
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
    expect(light['--bg']).toBe('#f3f6ff');
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
