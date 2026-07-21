import fs from 'node:fs';
import path from 'node:path';
import { contractStoreGuardViolationsFromSources } from './frontend-contract-store-guard-ast.mjs';

export const appRoot = path.resolve(new URL('..', import.meta.url).pathname);
export const guardedSourceRoots = Object.freeze(['src']);
const sourceExtensionPattern = /\.[cm]?[jt]sx?$/;

function walkSourceFiles(dir) {
  if (!fs.existsSync(dir)) return [];
  const files = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === 'node_modules' || entry.name === 'dist' || entry.name === 'coverage') continue;
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) files.push(...walkSourceFiles(fullPath));
    else if (sourceExtensionPattern.test(entry.name) && !/\.test\.[cm]?[jt]sx?$/.test(entry.name)) files.push(fullPath);
  }
  return files;
}

export function collectContractStoreGuardViolations({ root = appRoot, roots = guardedSourceRoots } = {}) {
  const sources = new Map();
  for (const sourceRootName of roots) {
    for (const file of walkSourceFiles(path.join(root, sourceRootName))) {
      sources.set(path.relative(root, file).split(path.sep).join('/'), fs.readFileSync(file, 'utf8'));
    }
  }
  return contractStoreGuardViolationsFromSources(sources);
}
