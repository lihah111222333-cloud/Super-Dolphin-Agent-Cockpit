import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { cwd } from 'node:process';
import { describe, expect, it } from 'vitest';
import {
  controlStatus,
  probeResult,
  scoreCurrentTree,
  sourceHasPromptHistoryConsoleOnly,
  sourceHasTerminalFalseSuccess,
  validateConfiguration,
} from './frontend-maintainability-score.mjs';

describe('frontend maintainability scorer', () => {
  function documents() {
    return {
      controls: JSON.parse(readFileSync(join(cwd(), 'scripts/frontend-maintainability-controls.json'), 'utf8')),
      fixtures: JSON.parse(readFileSync(join(cwd(), 'scripts/frontend-maintainability-red-fixtures.json'), 'utf8')),
    };
  }

  it('rejects hand-authored PASS, weak commands, missing, stale, and zero-test fixture evidence', () => {
    expect(validateConfiguration()).toBe(true);
    const handAuthored = documents();
    handAuthored.controls.controls[0].status = 'PASS';
    expect(() => validateConfiguration(handAuthored.controls, handAuthored.fixtures)).toThrow('hand-authored result is forbidden');

    const weakCommand = documents();
    weakCommand.controls.controls[19].allOf[0].argv = ['echo', 'PASS'];
    expect(() => validateConfiguration(weakCommand.controls, weakCommand.fixtures)).toThrow('weak runner command');

    const zeroTest = documents();
    zeroTest.controls.controls[0].allOf[0].testCount = 0;
    expect(() => validateConfiguration(zeroTest.controls, zeroTest.fixtures)).toThrow('zero-test runner evidence');

    const missingFixture = documents();
    missingFixture.controls.controls[0].allOf[0].caseIds = ['does-not-exist'];
    expect(() => validateConfiguration(missingFixture.controls, missingFixture.fixtures)).toThrow('missing fixture case');

    const staleFixture = documents();
    staleFixture.fixtures.fixtures.push({ id: 'stale-red', area: 'test', expected: 'reject' });
    expect(() => validateConfiguration(staleFixture.controls, staleFixture.fixtures)).toThrow('fixture coverage exact set mismatch');
  });

  it('reproduces the two confirmed blockers without turning either into a passing result', () => {
    expect(sourceHasTerminalFalseSuccess()).toBe(true);
    expect(sourceHasPromptHistoryConsoleOnly()).toBe(true);
    expect(probeResult('terminalTruth')).toBe('FAIL');
    expect(probeResult('promptHistoryVisibleError')).toBe('FAIL');
    expect(probeResult('redMatrix')).toBe('FAIL');
    expect(probeResult('actionRegistry')).toBe('FAIL');
  });

  it('uses three-state allOf semantics and derives a current score instead of reading a historical score', () => {
    expect(controlStatus([{ status: 'PASS' }, { status: 'PASS' }])).toBe('PASS');
    expect(controlStatus([{ status: 'PASS' }, { status: 'NOT_VERIFIED' }])).toBe('NOT_VERIFIED');
    expect(controlStatus([{ status: 'PASS' }, { status: 'FAIL' }])).toBe('FAIL');
    const result = scoreCurrentTree();
    expect(result.controls).toHaveLength(25);
    expect(result.controls.find(({ id }) => id === 'E01-terminal-truth')).toMatchObject({ status: 'FAIL' });
    expect(result.controls.find(({ id }) => id === 'E02-visible-action-error')).toMatchObject({ status: 'FAIL' });
    expect(result.displayScore).not.toBe(61.8);
  });
});
