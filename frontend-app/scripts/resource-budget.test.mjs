import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, expect, it } from 'vitest';
import { measureFrontendResources, verifyResourceEvidence } from './resource-budget.mjs';

const temporaryRoots = [];

function resourceFixture(sizes) {
  const root = mkdtempSync(join(tmpdir(), 'frontend-resource-budget-'));
  temporaryRoots.push(root);
  mkdirSync(join(root, 'assets'));
  writeFileSync(join(root, 'index.html'), 'x'.repeat(sizes.index));
  writeFileSync(join(root, 'assets', 'app.js'), 'x'.repeat(sizes.app));
  return root;
}

afterEach(() => {
  temporaryRoots.splice(0).forEach((root) => rmSync(root, { recursive: true, force: true }));
});

it('collects non-zero bundle and maximum chunk evidence', () => {
  const evidence = measureFrontendResources({
    distDir: resourceFixture({ index: 10, app: 90 }),
    subjectSha: 'a'.repeat(40),
  });
  expect(evidence).toEqual(expect.objectContaining({
    fileCount: 2,
    maxChunkBytes: 90,
    totalBundleBytes: 100,
  }));
});

it('fails a deliberately enlarged bundle', () => {
  const evidence = measureFrontendResources({
    distDir: resourceFixture({ index: 10, app: 96 }),
    subjectSha: 'a'.repeat(40),
  });
  const verdict = verifyResourceEvidence(evidence, {
    metrics: {
      'P04-resource-budget': {
        status: 'PASS',
        subjectSha: 'b'.repeat(40),
        sampleCount: 5,
        warmupCount: 1,
        maxRegressionRatio: 1.05,
        totalBundleBytes: 100,
        maxChunkBytes: 90,
      },
    },
  });
  expect(verdict.status).toBe('FAIL');
});

it('rejects zero-byte resource evidence', () => {
  const root = resourceFixture({ index: 1, app: 1 });
  writeFileSync(join(root, 'assets', 'app.js'), '');
  expect(() => measureFrontendResources({ distDir: root, subjectSha: 'a'.repeat(40) }))
    .toThrow(/invalid size 0/);
});
