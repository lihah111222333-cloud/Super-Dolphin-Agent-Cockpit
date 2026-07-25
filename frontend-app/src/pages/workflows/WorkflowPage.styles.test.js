import { readFileSync } from 'node:fs';
import path from 'node:path';
import { cwd } from 'node:process';
import postcss from 'postcss';
import { describe, expect, it } from 'vitest';

const stylesheet = readFileSync(path.join(cwd(), 'src/pages/workflows/WorkflowPage.css'), 'utf8');
const root = postcss.parse(stylesheet);

describe('WorkflowPage styles', () => {
  it('keeps disclosure styles scoped away from chat details', () => {
    const selectors = [];
    root.walkRules((rule) => selectors.push(rule.selector));

    expect(selectors).toContain('.workflow-page details');
    expect(selectors).not.toContain('details');
  });
});
