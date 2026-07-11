import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { validateZIndexContract } from './frontend-z-index-token-guard.mjs';

const packageJson = JSON.parse(readFileSync(path.join(process.cwd(), 'package.json'), 'utf8'));
const Z_INDEX_GUARD_COMMAND = 'node scripts/frontend-z-index-token-guard.mjs';

const VALID_TOKEN_SOURCE = `
  :root {
    --z-local-behind: -1;
    --z-local-raised: 1;
    --z-local-handle: 2;
    --z-local-sticky: 3;
    --z-shell-control: 4;
    --z-overlay-popover: 100;
    --z-overlay-dialog: 200;
    --z-overlay-lightbox: 300;
    --z-overlay-critical: 400;
  }
`;

const VALID_CSS_SOURCE = `
  .decoration::before { z-index: var(--z-local-behind); }
  .raised-content { z-index: var(--z-local-raised); }
  .resize-handle { z-index: var(--z-local-handle); }
  .sticky-header { z-index: var(--z-local-sticky); }
  .shell-control { z-index: var(--z-shell-control); }
  .project-selector-popover { z-index: var(--z-local-raised); }
  #overlay-root .global-popover { z-index: var(--z-overlay-popover); }
  #overlay-root .dialog { z-index: var(--z-overlay-dialog); }
  #overlay-root .lightbox { z-index: var(--z-overlay-lightbox); }
  #overlay-root .critical-overlay { z-index: var(--z-overlay-critical); }
`;

function validate({ tokenSource = VALID_TOKEN_SOURCE, cssSource = VALID_CSS_SOURCE, ...policy } = {}) {
  return validateZIndexContract({
    tokenSource,
    cssSources: new Map([['src/fixture.css', cssSource]]),
    ...policy,
  });
}

function codes(violations) {
  return violations.map((violation) => violation.code);
}

describe('frontend z-index token guard', () => {
  it('runs the strict CLI exactly once through the existing critical guard hook', () => {
    const criticalCommands = packageJson.scripts['guard:critical-skip'].split('&&').map((command) => command.trim());

    expect(criticalCommands.filter((command) => command === Z_INDEX_GUARD_COMMAND)).toEqual([Z_INDEX_GUARD_COMMAND]);
    expect(criticalCommands.filter((command) => command.startsWith(`${Z_INDEX_GUARD_COMMAND} `))).toEqual([]);
    expect(packageJson.scripts['guard:critical-skip']).not.toMatch(/baseline|allowlist|threshold/);
    expect(packageJson.scripts['test:hook']).toMatch(/^npm run guard:critical-skip\s*&&/);
  });

  it('accepts the exact nine-token contract with global and local selectors in one file', () => {
    expect(validate()).toEqual([]);
  });

  it('requires the exact token names without missing or additional definitions', () => {
    const tokenSource = VALID_TOKEN_SOURCE
      .replace('    --z-local-handle: 2;\n', '')
      .replace('  }', '    --z-local-floating: 5;\n  }');

    expect(validate({ tokenSource })).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: 'token-set-mismatch', token: '--z-local-handle' }),
      expect.objectContaining({ code: 'token-set-mismatch', token: '--z-local-floating' }),
    ]));
  });

  it.each([-1, 0, 1, 9999])('rejects the bare numeric z-index %s without threshold semantics', (value) => {
    const violations = validate({
      cssSource: `${VALID_CSS_SOURCE}\n.bare-number { z-index: ${value}; }`,
    });

    expect(violations).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: 'z-index-bare-number', file: 'src/fixture.css' }),
    ]));
  });

  it('accepts only exact known var references and rejects unknown or fallback forms', () => {
    expect(validate()).toEqual([]);

    const unknown = validate({
      cssSource: `${VALID_CSS_SOURCE}\n.unknown { z-index: var(--z-not-declared); }`,
    });
    expect(codes(unknown)).toContain('z-index-unknown-token');

    const fallback = validate({
      cssSource: `${VALID_CSS_SOURCE}\n.fallback { z-index: var(--z-local-raised, 8); }`,
    });
    expect(codes(fallback)).toContain('z-index-invalid-value');
  });

  it('rejects duplicate token definitions', () => {
    const tokenSource = VALID_TOKEN_SOURCE.replace(
      '    --z-local-raised: 1;',
      '    --z-local-raised: 1;\n    --z-local-raised: 2;',
    );

    expect(validate({ tokenSource })).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: 'token-duplicate-definition', token: '--z-local-raised' }),
    ]));
  });

  it('rejects token definitions outside the layer token source', () => {
    const violations = validate({
      cssSource: `${VALID_CSS_SOURCE}\n.page { --z-local-raised: 2; }`,
    });

    expect(violations).toEqual(expect.arrayContaining([
      expect.objectContaining({
        code: 'token-definition-outside-source',
        file: 'src/fixture.css',
        token: '--z-local-raised',
      }),
    ]));
  });

  it('rejects every declared token that has no production consumer', () => {
    const cssSource = VALID_CSS_SOURCE.replace(
      '  .resize-handle { z-index: var(--z-local-handle); }\n',
      '',
    );

    expect(validate({ cssSource })).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: 'token-unused', token: '--z-local-handle' }),
    ]));
  });

  it.each([
    ['duplicate', '    --z-overlay-dialog: 200;', '    --z-overlay-dialog: 100;'],
    ['reverse', '    --z-overlay-dialog: 200;', '    --z-overlay-dialog: 90;'],
    ['non-numeric', '    --z-overlay-dialog: 200;', '    --z-overlay-dialog: calc(100 + 100);'],
  ])('requires strictly numeric popover < dialog < lightbox < critical order for %s values', (_name, before, after) => {
    const tokenSource = VALID_TOKEN_SOURCE.replace(before, after);

    expect(validate({ tokenSource })).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: 'overlay-order-invalid' }),
    ]));
  });

  it('rejects global overlay tokens outside an explicit overlay-root selector', () => {
    const violations = validate({
      cssSource: `${VALID_CSS_SOURCE}\n.page-dialog { z-index: var(--z-overlay-dialog); }`,
    });

    expect(violations).toEqual(expect.arrayContaining([
      expect.objectContaining({
        code: 'global-token-outside-overlay-root',
        file: 'src/fixture.css',
        token: '--z-overlay-dialog',
      }),
    ]));
  });

  it('allows a ProjectSelector-style local popover to use a local token', () => {
    expect(validate({
      cssSource: VALID_CSS_SOURCE.replace(
        '.project-selector-popover { z-index: var(--z-local-raised); }',
        '.project-selector-popover { z-index: var(--z-local-sticky); }',
      ),
    })).toEqual([]);
  });

  it('parses comments and whitespace without hiding active numeric declarations', () => {
    const cssSource = `${VALID_CSS_SOURCE}
      /* .comment-only { z-index: 9999; } */
      .active {
        z-index /* cannot bypass */ :
          -4
        ;
      }
    `;
    const violations = validate({ cssSource });

    expect(violations.filter((violation) => violation.code === 'z-index-bare-number')).toHaveLength(1);
  });

  it.each(['baseline', 'allowlist', 'threshold'])('rejects the policy bypass option %s', (option) => {
    const violations = validate({ [option]: option === 'threshold' ? 8 : [] });

    expect(violations).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: 'policy-bypass-option', option }),
    ]));
  });
});
