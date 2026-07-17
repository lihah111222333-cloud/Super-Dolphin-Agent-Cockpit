import { readFileSync } from 'node:fs';
import path from 'node:path';

import { expect, it } from 'vitest';

const scriptsRoot = path.resolve(import.meta.dirname);
const matrix = JSON.parse(readFileSync(path.join(scriptsRoot, 'failure-matrix-cases.json'), 'utf8'));
const fixtures = JSON.parse(readFileSync(path.join(scriptsRoot, 'failure-matrix-fixtures.json'), 'utf8'));
const blockedCases = matrix.cases.filter((entry) => entry.status === 'blocked');
it('keeps dependency-only fixture replay absent after all matrix cases gain executable coverage', () => {
  expect(blockedCases).toEqual([]);
  expect(fixtures.fixtures).toHaveLength(matrix.cases.length);
  expect(fixtures.fixtures.some((entry) => entry.blockedBy)).toBe(false);
});
