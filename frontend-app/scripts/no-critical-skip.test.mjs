import { describe, expect, it } from 'vitest';
import {
  CRITICAL_SKIP_ROOTS,
  collectCriticalSkipViolations,
  criticalSkipViolationsFromSources,
  discoverableTestImportsInSource,
  skippedTestsInSource,
  testSourceViolationsFromSources,
} from './no-critical-skip.mjs';

describe('critical skip guard', () => {
  it('scans frontend source and script tests', () => {
    expect(CRITICAL_SKIP_ROOTS).toEqual(['src', 'scripts']);
  });

  it('fails fast when a configured root is missing', () => {
    expect(() => collectCriticalSkipViolations({ roots: ['missing-critical-root'] }))
      .toThrow(/critical skip root does not exist/);
  });

  it('flags critical skipped tests by name or file path', () => {
    const sources = new Map([
      ['scripts/rpc-contract-audit.test.mjs', "it.skip('allows harmless fixture drift', () => {})"],
      ['src/shared/api/harmless.test.js', "test.skip('provider flow is flaky', () => {})"],
      ['src/shared/api/ui.test.js', "it.skip('visual polish backlog', () => {})"],
    ]);

    expect(criticalSkipViolationsFromSources(sources)).toEqual([
      {
        file: 'scripts/rpc-contract-audit.test.mjs',
        line: 1,
        name: 'allows harmless fixture drift',
        parseError: false,
      },
      {
        file: 'src/shared/api/harmless.test.js',
        line: 1,
        name: 'provider flow is flaky',
        parseError: false,
      },
    ]);
  });

  it('detects multiline and computed skipped tests without matching comments or strings', () => {
    const source = [
      'const fixture = "it.skip(\'provider in a string\', () => {})";',
      "// describe.skip('thread in a comment', () => {})",
      'describe',
      "  .skip('workflow multiline describe', () => {});",
      "test['skip']('rpc computed test', () => {});",
    ].join('\n');

    expect(skippedTestsInSource('src/shared/api/skip-shapes.test.js', source)).toEqual([
      {
        file: 'src/shared/api/skip-shapes.test.js',
        line: 3,
        name: 'workflow multiline describe',
        parseError: false,
      },
      {
        file: 'src/shared/api/skip-shapes.test.js',
        line: 5,
        name: 'rpc computed test',
        parseError: false,
      },
    ]);
  });

  it('detects chained skip.each calls on test APIs only', () => {
    const source = [
      "test.skip.each([[1]])('provider %s flow', () => {});",
      'it.skip.each([[1]])(`rpc each flow`, () => {});',
      "describe.skip.each([['thread']])('thread %s flow', () => {});",
      "foo.skip.each([[1]])('provider %s flow', () => {});",
      'test.skip.each([[caseName]])(`provider ${caseName} flow`, () => {});',
    ].join('\n');

    expect(skippedTestsInSource('src/shared/api/thread.test.js', source)).toEqual([
      {
        file: 'src/shared/api/thread.test.js',
        line: 1,
        name: 'provider %s flow',
        parseError: false,
      },
      {
        file: 'src/shared/api/thread.test.js',
        line: 2,
        name: 'rpc each flow',
        parseError: false,
      },
      {
        file: 'src/shared/api/thread.test.js',
        line: 3,
        name: 'thread %s flow',
        parseError: false,
      },
      {
        file: 'src/shared/api/thread.test.js',
        line: 5,
        name: '<unparseable>',
        parseError: true,
      },
    ]);
  });

  it('fails fast when a test source cannot be parsed as JavaScript', () => {
    expect(() => skippedTestsInSource(
      'src/shared/api/broken.test.js',
      "it.skip('provider flow', () => {",
    )).toThrow(/critical skip source parse failed: src\/shared\/api\/broken\.test\.js:1/);
  });

  it('treats unparseable skipped test names as violations', () => {
    expect(skippedTestsInSource(
      'src/shared/api/thread.test.js',
      "it.skip(`thread ${caseName}`, () => {})",
    )).toEqual([{
      file: 'src/shared/api/thread.test.js',
      line: 1,
      name: '<unparseable>',
      parseError: true,
    }]);
  });

  it('rejects runtime imports between default-discoverable test files', () => {
    const sources = new Map([
      [
        'scripts/rpc-contract-audit.test.mjs',
        [
          'import "./rpc-contract-audit-payload-sidebar.test.mjs";',
          'export * from "./rpc-contract-audit-registry-validator.test.mjs";',
        ].join('\n'),
      ],
      [
        'scripts/type-consumer.test.ts',
        [
          'import type { Fixture } from "./fixture.test.ts";',
          'import { type NamedFixture } from "./named-fixture.test.ts";',
          'export type { Fixture } from "./fixture.test.ts";',
          'export { type NamedFixture } from "./named-fixture.test.ts";',
        ].join('\n'),
      ],
      ['scripts/raw-consumer.test.js', 'import fixture from "./fixture.test.js?raw";'],
    ]);

    expect(testSourceViolationsFromSources(sources).discoverableTestImports).toEqual([
      {
        file: 'scripts/rpc-contract-audit.test.mjs',
        line: 1,
        modulePath: './rpc-contract-audit-payload-sidebar.test.mjs',
      },
      {
        file: 'scripts/rpc-contract-audit.test.mjs',
        line: 2,
        modulePath: './rpc-contract-audit-registry-validator.test.mjs',
      },
    ]);
  });

  it('does not infer discoverable test imports from comments or strings', () => {
    const source = [
      'const fixture = `import "./other.test.js"`;',
      '// import "./comment.test.js";',
      'import helper from "./helper.js";',
    ].join('\n');

    expect(discoverableTestImportsInSource('scripts/clean.test.js', source)).toEqual([]);
  });
});
