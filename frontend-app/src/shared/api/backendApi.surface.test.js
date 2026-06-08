import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { RPC_METHODS } from './backendApi.js';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const productDirs = ['pages', 'features', 'entities'];
const rawBridgeNames = new Set(['callAPI', 'callBackend']);
const allowedRawBackendImports = new Map([
  ['pages/settings/SettingsPage.jsx', ['callBackend']],
]);
const allowedLiteralRpcCalls = new Map([
  ['pages/settings/SettingsPage.jsx', ['ui/video/getApiKey', 'ui/video/setApiKey']],
]);

const rpcSurfaceMatrixSeed = [
  {
    method: 'ui/video/getApiKey',
    risk: 'P0',
    status: 'known_gap',
    owner: 'task/frontend-rpc-video-api-facade-20260608',
  },
  {
    method: 'ui/video/setApiKey',
    risk: 'P0',
    status: 'known_gap',
    owner: 'task/frontend-rpc-video-api-facade-20260608',
  },
];

function collectProductFiles() {
  const files = [];
  for (const dir of productDirs) {
    walk(path.join(sourceRoot, dir), files);
  }
  return files;
}

function walk(dir, files) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      walk(fullPath, files);
      continue;
    }
    if (!/\.[jt]sx?$/.test(entry.name) || /\.test\.[jt]sx?$/.test(entry.name)) continue;
    files.push(fullPath);
  }
}

function rel(filePath) {
  return path.relative(sourceRoot, filePath).split(path.sep).join('/');
}

function parseNamedImports(source) {
  const imports = [];
  const importPattern = /import\s*\{([^}]+)\}\s*from\s*['"]([^'"]+)['"]/g;
  for (const match of source.matchAll(importPattern)) {
    const [, names, specifier] = match;
    if (!specifier.endsWith('/backendApi.js') && !specifier.endsWith('/wailsBridge.js')) continue;
    for (const part of names.split(',')) {
      const imported = part.trim().split(/\s+as\s+/)[0]?.trim();
      if (imported) imports.push({ imported, specifier });
    }
  }
  return imports;
}

function parseLiteralBridgeCalls(source) {
  const calls = [];
  const callPattern = /\b(callAPI|callBackend)\s*\(\s*(['"])([^'"]+)\2/g;
  for (const match of source.matchAll(callPattern)) {
    calls.push({ callee: match[1], method: match[3] });
  }
  return calls;
}

describe('backend API surface gate', () => {
  it('keeps product code behind named backend facade methods', () => {
    const violations = [];

    for (const filePath of collectProductFiles()) {
      const relativePath = rel(filePath);
      const source = fs.readFileSync(filePath, 'utf8');
      const allowedImports = new Set(allowedRawBackendImports.get(relativePath) || []);
      const allowedCalls = new Set(allowedLiteralRpcCalls.get(relativePath) || []);

      for (const { imported, specifier } of parseNamedImports(source)) {
        if (rawBridgeNames.has(imported) && !allowedImports.has(imported)) {
          violations.push(`${relativePath} imports ${imported} from ${specifier}`);
        }
      }

      for (const { callee, method } of parseLiteralBridgeCalls(source)) {
        if (!allowedCalls.has(method)) {
          violations.push(`${relativePath} calls ${callee}('${method}')`);
        }
      }
    }

    expect(violations).toEqual([]);
  });

  it('tracks known facade gaps in the RPC surface matrix seed', () => {
    const methodValues = new Set(Object.values(RPC_METHODS));
    const matrixMethods = new Set(rpcSurfaceMatrixSeed.map((row) => row.method));

    expect(matrixMethods).toEqual(new Set(['ui/video/getApiKey', 'ui/video/setApiKey']));
    for (const row of rpcSurfaceMatrixSeed) {
      expect(row.risk).toBe('P0');
      expect(row.status).toBe('known_gap');
      expect(row.owner).toBe('task/frontend-rpc-video-api-facade-20260608');
      expect(methodValues.has(row.method)).toBe(false);
    }
  });

  it('keeps temporary raw RPC exceptions tied to known matrix gaps', () => {
    const matrixMethods = new Set(rpcSurfaceMatrixSeed.map((row) => row.method));

    for (const [relativePath, importedNames] of allowedRawBackendImports.entries()) {
      expect(relativePath).toBe('pages/settings/SettingsPage.jsx');
      expect(importedNames).toEqual(['callBackend']);
    }
    for (const [relativePath, methods] of allowedLiteralRpcCalls.entries()) {
      expect(relativePath).toBe('pages/settings/SettingsPage.jsx');
      for (const method of methods) {
        expect(matrixMethods.has(method)).toBe(true);
      }
    }
  });
});
