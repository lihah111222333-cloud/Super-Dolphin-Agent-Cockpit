import { readFileSync } from 'node:fs';
import path from 'node:path';
import { cwd } from 'node:process';
import postcss from 'postcss';
import { describe, expect, it } from 'vitest';

const css = readFileSync(path.join(cwd(), 'src/styles.css'), 'utf8');
const root = postcss.parse(css);

function declarationsFor(selector) {
  const declarations = {};
  root.walkRules(selector, (rule) => {
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
