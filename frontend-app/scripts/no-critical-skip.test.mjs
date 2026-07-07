import { describe, expect, it } from 'vitest';
import {
  CRITICAL_SKIP_ROOTS,
  collectCriticalSkipViolations,
  criticalSkipViolationsFromSources,
  skippedTestsInSource,
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
});
