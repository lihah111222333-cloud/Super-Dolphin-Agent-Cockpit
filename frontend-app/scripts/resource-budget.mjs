import { readdirSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';
import {
  evaluateResourceBudget,
} from './performance-budget-model.mjs';

function collectFiles(root, directory = root) {
  return readdirSync(directory, { withFileTypes: true })
    .flatMap((entry) => {
      const path = join(directory, entry.name);
      return entry.isDirectory() ? collectFiles(root, path) : [{ path, relativePath: relative(root, path) }];
    });
}

function measureFrontendResources({
  distDir = resolve('dist'),
  subjectSha,
} = {}) {
  const indexPath = join(distDir, 'index.html');
  if (!statSync(indexPath).isFile()) throw new Error(`frontend dist index is missing: ${indexPath}`);
  const files = collectFiles(distDir);
  if (files.length === 0) throw new Error('frontend dist contains zero files');
  const measured = files.map(({ path, relativePath }) => ({
    path: relativePath,
    bytes: statSync(path).size,
  }));
  measured.forEach(({ bytes, path }) => {
    if (!Number.isSafeInteger(bytes) || bytes <= 0) throw new Error(`resource ${path} has invalid size ${bytes}`);
  });
  return Object.freeze({
    metricId: 'P04-resource-budget',
    subjectSha,
    fileCount: measured.length,
    totalBundleBytes: measured.reduce((total, { bytes }) => total + bytes, 0),
    maxChunkBytes: Math.max(...measured.map(({ bytes }) => bytes)),
    files: Object.freeze(measured.sort((left, right) => left.path.localeCompare(right.path))),
  });
}

function verifyResourceEvidence(evidence, baseline) {
  return evaluateResourceBudget(evidence, baseline?.metrics?.['P04-resource-budget']);
}

export {
  measureFrontendResources,
  verifyResourceEvidence,
};
