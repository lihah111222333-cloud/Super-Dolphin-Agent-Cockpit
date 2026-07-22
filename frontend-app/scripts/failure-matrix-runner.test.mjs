import { mkdtempSync, mkdirSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

import {
  parseGoEvidence,
  parseVitestEvidence,
  validateFailureMatrixEvidence,
  validateFailureMatrixFixtures,
  validateFailureMatrixManifest,
  validateFailureMatrixMutations,
} from './failure-matrix-runner.mjs';

function validManifest() {
  const layersByCase = new Map([
    ['FM-01', ['frontend', 'go-wails']],
    ['FM-07', ['go-codex']],
    ['FM-08', ['go-claude']],
    ...Array.from({ length: 6 }, (_, index) => [`FM-${String(index + 9).padStart(2, '0')}`, ['go-codex']]),
    ['FM-15', ['go-turn']],
    ['FM-16', ['go-turn']],
    ['FM-17', ['go-turn']],
    ['FM-18', ['frontend']],
    ['FM-19', ['frontend', 'go-codex']],
    ['FM-20', ['frontend', 'go-codex']],
    ...Array.from({ length: 4 }, (_, index) => [`FM-${String(index + 21).padStart(2, '0')}`, ['frontend']]),
  ]);
  return {
    schemaVersion: 1,
    cases: Array.from({ length: 24 }, (_, index) => {
      const caseId = `FM-${String(index + 1).padStart(2, '0')}`;
      return {
        caseId,
        subject: `case ${index + 1}`,
        status: 'covered',
        requiredLayers: layersByCase.get(caseId) || ['frontend'],
      };
    }),
  };
}

function validFixtures(cases = validManifest().cases) {
  return {
    schemaVersion: 1,
    fixtures: cases.map(({ caseId, blockedBy }) => ({
      caseId,
      expected: `${caseId} expectation`,
      ...(blockedBy ? { blockedBy } : {}),
    })),
  };
}

describe('failure matrix runner', () => {
  it('rejects duplicate, malformed, invalid-status, and empty manifests', () => {
    const duplicate = validManifest();
    duplicate.cases[23].caseId = 'FM-23';
    expect(() => validateFailureMatrixManifest(duplicate)).toThrow(/duplicate/);

    const malformed = validManifest();
    malformed.cases[23].caseId = 'FM-25';
    expect(() => validateFailureMatrixManifest(malformed)).toThrow(/missing=.*FM-24.*stale=.*FM-25/);

    const invalidStatus = validManifest();
    invalidStatus.cases[23].status = 'unknown';
    expect(() => validateFailureMatrixManifest(invalidStatus)).toThrow(/covered or blocked/);

    const deleted = validManifest();
    deleted.cases.pop();
    expect(() => validateFailureMatrixManifest(deleted)).toThrow(/missing=.*FM-24/);
    expect(() => validateFailureMatrixManifest({ schemaVersion: 1, cases: [] })).toThrow(/missing=/);
  });

  it('rejects missing, stale, duplicate, and blocker-drift fixtures', () => {
    const cases = validateFailureMatrixManifest(validManifest());
    const valid = validFixtures(cases);
    expect(validateFailureMatrixFixtures(cases, valid)).toEqual(valid.fixtures);
    expect(() => validateFailureMatrixFixtures(cases, {
      ...valid,
      fixtures: valid.fixtures.slice(1),
    })).toThrow(/missing/);
    expect(() => validateFailureMatrixFixtures(cases, {
      ...valid,
      fixtures: [...valid.fixtures, { caseId: 'FM-99', expected: 'stale' }],
    })).toThrow(/stale/);
    expect(() => validateFailureMatrixFixtures(cases, {
      ...valid,
      fixtures: [...valid.fixtures.slice(0, -1), valid.fixtures[0]],
    })).toThrow(/duplicate/);
    expect(() => validateFailureMatrixFixtures(cases, {
      ...valid,
      fixtures: valid.fixtures.map((entry) => (
        entry.caseId === 'FM-15' ? { ...entry, blockedBy: 'stale-task' } : entry
      )),
    })).toThrow(/blockedBy drift/);
  });

  it('rejects zero, missing-layer, duplicate, and stale evidence', () => {
    const cases = validateFailureMatrixManifest(validManifest());
    const fixtures = validFixtures(cases).fixtures;
    const valid = cases.flatMap((entry) => entry.requiredLayers.map((layer) => ({ caseId: entry.caseId, layer, test: entry.caseId })));
    expect(() => validateFailureMatrixEvidence(cases, fixtures, [])).toThrow(/greater than zero/);
    expect(() => validateFailureMatrixEvidence(cases, fixtures, valid.slice(1))).toThrow(/missing/);
    expect(() => validateFailureMatrixEvidence(cases, fixtures, [...valid, valid[0]])).toThrow(/duplicate/);
    expect(() => validateFailureMatrixEvidence(cases, fixtures, [...valid.slice(0, -1), { caseId: 'FM-99', layer: 'frontend' }])).toThrow(/stale/);
  });

  it('requires one production mutation RED binding for every case', () => {
    const valid = {
      schemaVersion: 1,
      mutations: [{
        id: 'production-mutation',
        layer: 'frontend',
        sourcePath: 'frontend-app/src/production.js',
        search: 'before',
        replacement: 'after',
        caseIds: Array.from({ length: 24 }, (_, index) => `FM-${String(index + 1).padStart(2, '0')}`),
      }],
    };
    expect(validateFailureMatrixMutations(valid)).toEqual(valid.mutations);
    expect(() => validateFailureMatrixMutations({
      ...valid,
      mutations: [{ ...valid.mutations[0], caseIds: valid.mutations[0].caseIds.slice(1) }],
    })).toThrow(/missing=.*FM-01/);
    expect(() => validateFailureMatrixMutations({
      ...valid,
      mutations: [{ ...valid.mutations[0], sourcePath: '../production.js' }],
    })).toThrow(/repository-relative/);
    expect(() => validateFailureMatrixMutations({
      ...valid,
      mutations: [{ ...valid.mutations[0], replacement: 'before' }],
    })).toThrow(/distinct search and replacement/);
  });

  it('parses only passed case evidence from Vitest and Go JSON', () => {
    expect(parseVitestEvidence({
      testResults: [{
        assertionResults: [
          { fullName: 'matrix:FM-01 layer:frontend visible failure', status: 'passed' },
          { fullName: 'matrix:FM-02 layer:frontend failed assertion', status: 'failed' },
        ],
      }],
    })).toEqual([{ caseId: 'FM-01', layer: 'frontend', test: 'matrix:FM-01 layer:frontend visible failure' }]);

    const go = [
      JSON.stringify({ Action: 'pass', Test: 'TestFailureMatrix/FM-06' }),
      JSON.stringify({ Action: 'fail', Test: 'TestFailureMatrix/FM-07' }),
    ].join('\n');
    expect(parseGoEvidence(go, 'go-codex')).toEqual([
      { caseId: 'FM-06', layer: 'go-codex', test: 'TestFailureMatrix/FM-06' },
    ]);
  });

  it('canonicalizes symlinked worktree roots before recording focused Vitest files', () => {
    const root = mkdtempSync(join(tmpdir(), 'failure-matrix-realpath-'));
    try {
      const realFrontend = join(root, 'real', 'frontend-app');
      const testFile = join(realFrontend, 'src', 'failure.test.js');
      mkdirSync(join(realFrontend, 'src'), { recursive: true });
      writeFileSync(testFile, '// fixture\n');
      symlinkSync(join(root, 'real'), join(root, 'alias'), 'dir');

      expect(parseVitestEvidence({
        testResults: [{
          name: realpathSync(testFile),
          assertionResults: [{
            fullName: 'matrix:FM-01 layer:frontend visible failure',
            status: 'passed',
          }],
        }],
      }, join(root, 'alias', 'frontend-app'))).toEqual([{
        caseId: 'FM-01',
        layer: 'frontend',
        test: 'matrix:FM-01 layer:frontend visible failure',
        file: 'src/failure.test.js',
      }]);
    }
    finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
