import fs from 'node:fs';
import path from 'node:path';

import { dottedName, parseModule, stringValue, walkNode } from './frontend-state-ownership-ast.mjs';

const sourceExtensionPattern = /\.[cm]?[jt]sx?$/;
const testFilePattern = /\.(?:test|spec)\.[cm]?[jt]sx?$/;

export function productionSources(root, sourceRoot, overrides) {
  const normalizedRoot = normalizeRelativePath(sourceRoot);
  const files = walkSourceFiles(path.join(root, normalizedRoot))
    .map((absolutePath) => normalizeRelativePath(path.relative(root, absolutePath)));
  const candidatePaths = new Set(files);
  for (const relativePath of overrides.keys()) {
    const normalized = normalizeRelativePath(relativePath);
    if (normalized === normalizedRoot || normalized.startsWith(`${normalizedRoot}/`)) candidatePaths.add(normalized);
  }
  const sources = new Map();
  for (const relativePath of candidatePaths) {
    if (testFilePattern.test(relativePath) || relativePath.endsWith('.generated.js')) continue;
    sources.set(relativePath, readSource(root, relativePath, overrides));
  }
  if (sources.size === 0) throw new Error(`state ownership source root ${sourceRoot} has zero production files`);
  return sources;
}

export function discoverCaseIds(source, filePath, prefix) {
  const ids = [];
  walkNode(parseModule(source, filePath), [], (node) => {
    if (node.type !== 'CallExpression' || !['it', 'test'].includes(dottedName(node.callee))) return;
    const title = stringValue(node.arguments[0]);
    if (title?.startsWith(`[${prefix}`)) ids.push(title.slice(1, title.indexOf(']')));
  });
  return [...new Set(ids)].sort();
}

export function readSource(root, relativePath, overrides) {
  const normalized = normalizeRelativePath(relativePath);
  if (overrides.has(normalized)) return overrides.get(normalized);
  return fs.readFileSync(path.join(root, normalized), 'utf8');
}

export function readJSON(filePath) {
  try {
    return JSON.parse(fs.readFileSync(filePath, 'utf8'));
  } catch (error) {
    throw new Error(`read ${path.basename(filePath)}: ${error.message}`);
  }
}

export function normalizeRelativePath(value) {
  const normalized = path.posix.normalize(String(value).split(path.sep).join('/'));
  if (!normalized || path.posix.isAbsolute(normalized) || normalized.startsWith('../')) {
    throw new Error(`path must be app-relative: ${value}`);
  }
  return normalized;
}

function walkSourceFiles(directory) {
  if (!fs.existsSync(directory)) return [];
  const files = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const absolutePath = path.join(directory, entry.name);
    if (entry.isDirectory() && entry.name !== '__tests__') {
      files.push(...walkSourceFiles(absolutePath));
    } else if (sourceExtensionPattern.test(entry.name)) {
      files.push(absolutePath);
    }
  }
  return files;
}
