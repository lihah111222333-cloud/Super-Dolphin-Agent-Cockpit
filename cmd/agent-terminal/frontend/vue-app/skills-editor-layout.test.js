// @ts-nocheck
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const FRONTEND_ROOT = resolve(import.meta.dirname, '.');

function readCSS(relativePath) {
  return readFileSync(resolve(FRONTEND_ROOT, relativePath), 'utf-8');
}

function cssBlock(css, selector) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = css.match(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`));
  return match?.[1] || '';
}

describe('Skills editor layout', () => {
  it('keeps save actions visible without covering the skill body', () => {
    const css = readCSS('styles/skills.css');
    const panel = cssBlock(css, '.skills-editor-panel');
    const actions = cssBlock(css, '.skills-actions-row');

    expect(panel).toMatch(/flex:\s*1\s+1\s+auto/);
    expect(panel).toMatch(/min-height:\s*0/);
    expect(panel).toMatch(/overflow-y:\s*auto/);
    expect(actions).toMatch(/position:\s*sticky/);
    expect(actions).toMatch(/bottom:\s*0/);
    expect(actions).toMatch(/z-index:\s*1/);
    expect(actions).toMatch(/background:/);
    expect(actions).toMatch(/border-top:/);
  });
});
