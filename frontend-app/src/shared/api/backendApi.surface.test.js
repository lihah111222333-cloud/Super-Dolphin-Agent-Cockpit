import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { RPC_METHODS } from './backendApi.js';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const rawBridgeNames = new Set(['callAPI', 'callBackend']);
const rawBridgeModules = ['/backendApi.js', '/wailsBridge.js'];
const sessionBackendApiNames = new Set(['startThread', 'startTurn']);

function collectProductFiles() {
  const files = [];
  walk(sourceRoot, files);
  return files;
}

function walk(dir, files) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const fullPath = path.join(dir, entry.name);
    const relativePath = rel(fullPath);
    if (entry.isDirectory()) {
      if (relativePath === 'shared/api' || relativePath.startsWith('shared/api/')) continue;
      walk(fullPath, files);
      continue;
    }
    if (!/\.[jt]sx?$/.test(entry.name) || /\.test\.[jt]sx?$/.test(entry.name)) continue;
    if (relativePath.startsWith('shared/api/')) continue;
    files.push(fullPath);
  }
}

function rel(filePath) {
  return path.relative(sourceRoot, filePath).split(path.sep).join('/');
}

function isRawBridgeModule(specifier) {
  return rawBridgeModules.some((suffix) => specifier.endsWith(suffix));
}

function parseRawBridgeReferences(source) {
  const directImports = [];
  const namespaces = [];
  const importPattern = /import\s*(?:(?:\{([^}]+)\})|(?:\*\s+as\s+([A-Za-z_$][\w$]*)))\s*from\s*['"]([^'"]+)['"]/g;
  for (const match of source.matchAll(importPattern)) {
    const [, names, namespaceName, specifier] = match;
    if (!isRawBridgeModule(specifier)) continue;
    if (namespaceName) {
      namespaces.push({ local: namespaceName, specifier });
      continue;
    }
    for (const part of names.split(',')) {
      const [importedPart, aliasPart] = part.trim().split(/\s+as\s+/);
      const imported = importedPart?.trim();
      const local = aliasPart?.trim() || imported;
      if (rawBridgeNames.has(imported)) {
        directImports.push({ imported, local, specifier });
      }
    }
  }
  return { directImports, namespaces };
}

function parseBackendApiSessionImports(source) {
  const imports = [];
  const importPattern = /import\s*\{([^}]+)\}\s*from\s*['"]([^'"]*\/backendApi(?:\.js)?)['"]/g;
  for (const match of source.matchAll(importPattern)) {
    const [, names, specifier] = match;
    if (!isRawBridgeModule(specifier)) continue;
    for (const part of names.split(',')) {
      const imported = part.trim().split(/\s+as\s+/)[0]?.trim();
      if (sessionBackendApiNames.has(imported)) imports.push({ imported, specifier });
    }
  }
  return imports;
}

function directCallPattern(name) {
  return new RegExp(`\\b${escapeRegExp(name)}\\s*\\(`);
}

function namespaceCallPattern(namespaceName) {
  return new RegExp(`\\b${escapeRegExp(namespaceName)}\\.(callAPI|callBackend)\\s*\\(`);
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

describe('backend API surface gate', () => {
  it('keeps all product code behind named backend facade methods', () => {
    const violations = [];

    for (const filePath of collectProductFiles()) {
      const relativePath = rel(filePath);
      const source = fs.readFileSync(filePath, 'utf8');
      const { directImports, namespaces } = parseRawBridgeReferences(source);

      for (const { imported, local, specifier } of directImports) {
        violations.push(`${relativePath} imports raw ${imported} as ${local} from ${specifier}`);
        if (directCallPattern(local).test(source)) {
          violations.push(`${relativePath} calls raw ${local}()`);
        }
      }
      for (const { local, specifier } of namespaces) {
        const callMatch = namespaceCallPattern(local).exec(source);
        if (callMatch) {
          violations.push(`${relativePath} calls raw ${local}.${callMatch[1]}() from ${specifier}`);
        }
      }
    }

    expect(violations).toEqual([]);
  });

  it('keeps thread start calls behind sessionApi', () => {
    const violations = [];

    for (const filePath of collectProductFiles()) {
      const relativePath = rel(filePath);
      const source = fs.readFileSync(filePath, 'utf8');
      for (const { imported, specifier } of parseBackendApiSessionImports(source)) {
        violations.push(`${relativePath} imports ${imported} directly from ${specifier}`);
      }
    }

    expect(violations).toEqual([]);
  });

  it('does not keep temporary video RPC allowlist entries', () => {
    expect(Object.values(RPC_METHODS)).toContain('ui/video/getApiKey');
    expect(Object.values(RPC_METHODS)).toContain('ui/video/setApiKey');
  });
});
