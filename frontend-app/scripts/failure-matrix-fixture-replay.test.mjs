import { readFileSync } from 'node:fs';
import path from 'node:path';

import { expect, it } from 'vitest';

const scriptsRoot = path.resolve(import.meta.dirname);
const matrix = JSON.parse(readFileSync(path.join(scriptsRoot, 'failure-matrix-cases.json'), 'utf8'));
const fixtures = JSON.parse(readFileSync(path.join(scriptsRoot, 'failure-matrix-fixtures.json'), 'utf8'));
const blockedCases = matrix.cases.filter((entry) => entry.status === 'blocked');
const fixtureByCaseID = new Map(fixtures.fixtures.map((entry) => [entry.caseId, entry]));

it.each(blockedCases.map((matrixCase) => [matrixCase.caseId, matrixCase]))(
  'matrix:%s layer:fixture-replay records dependency without claiming product coverage',
  (_caseId, matrixCase) => {
    const fixture = fixtureByCaseID.get(matrixCase.caseId);
    expect(fixture).toEqual(expect.objectContaining({
      caseId: matrixCase.caseId,
      blockedBy: matrixCase.blockedBy,
    }));
    expect(matrixCase.requiredLayers).toEqual(['fixture-replay']);
    expect(matrixCase.blocker).toEqual(expect.any(String));
    expect(fixture.expected).toEqual(expect.any(String));
  },
);
