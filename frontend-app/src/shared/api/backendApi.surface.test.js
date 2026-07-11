import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { parseStaticImports } from '../../test-utils/importAst.js';
import { RPC_METHODS } from './backendApi.js';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const rawBridgeNames = new Set(['callAPI', 'callBackend']);
const rawBridgeModules = ['/backendApi.js', '/wailsBridge.js'];
const sessionBackendApiNames = new Set(['forkThread', 'startThread', 'startTurn']);

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
  const defaultImports = [];
  const namespaces = [];
  for (const entry of parseStaticImports(source)) {
    if (!isRawBridgeModule(entry.specifier)) continue;
    if (entry.kind === 'default') {
      defaultImports.push({ local: entry.local, specifier: entry.specifier });
      continue;
    }
    if (entry.kind === 'namespace') {
      namespaces.push({ local: entry.local, specifier: entry.specifier });
      continue;
    }
    if (entry.kind === 'named' && rawBridgeNames.has(entry.imported)) {
      directImports.push({ imported: entry.imported, local: entry.local, specifier: entry.specifier });
    }
  }
  return { defaultImports, directImports, namespaces };
}

function parseBackendApiSessionImports(source) {
  const imports = [];
  for (const entry of parseStaticImports(source)) {
    if (entry.kind !== 'named' || !isRawBridgeModule(entry.specifier)) continue;
    if (sessionBackendApiNames.has(entry.imported)) {
      imports.push({ imported: entry.imported, specifier: entry.specifier });
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
  it('parses raw backend imports with the TypeScript AST', () => {
    const source = [
      "import rawDefault from '../../shared/api/backendApi.js';",
      "import * as bridge from '../../shared/api/wailsBridge.js';",
      'import {',
      '  callAPI as rawCall,',
      '  callBackend,',
      '  getDashboardPage,',
      "} from '../../shared/api/backendApi.js';",
      'const ignored = "import { callAPI } from \'../../shared/api/backendApi.js\'";',
      "// import { callBackend } from '../../shared/api/backendApi.js';",
    ].join('\n');

    expect(parseRawBridgeReferences(source)).toEqual({
      defaultImports: [{ local: 'rawDefault', specifier: '../../shared/api/backendApi.js' }],
      directImports: [
        { imported: 'callAPI', local: 'rawCall', specifier: '../../shared/api/backendApi.js' },
        { imported: 'callBackend', local: 'callBackend', specifier: '../../shared/api/backendApi.js' },
      ],
      namespaces: [{ local: 'bridge', specifier: '../../shared/api/wailsBridge.js' }],
    });
    expect(parseBackendApiSessionImports(source)).toEqual([]);
  });

  it('parses multiline session imports and ignores comments or strings', () => {
    const source = [
      'import {',
      '  forkThread as forkSession,',
      '  startThread as beginThread,',
      '  startTurn,',
      '  getThreadMessages,',
      "} from '../../shared/api/backendApi.js';",
      'const ignored = "import { startThread } from \'../../shared/api/backendApi.js\'";',
      "// import { startTurn } from '../../shared/api/backendApi.js';",
    ].join('\n');

    expect(parseBackendApiSessionImports(source)).toEqual([
      { imported: 'forkThread', specifier: '../../shared/api/backendApi.js' },
      { imported: 'startThread', specifier: '../../shared/api/backendApi.js' },
      { imported: 'startTurn', specifier: '../../shared/api/backendApi.js' },
    ]);
  });

  it('keeps all product code behind named backend facade methods', () => {
    const violations = [];

    for (const filePath of collectProductFiles()) {
      const relativePath = rel(filePath);
      const source = fs.readFileSync(filePath, 'utf8');
      const { defaultImports, directImports, namespaces } = parseRawBridgeReferences(source);

      for (const { local, specifier } of defaultImports) {
        violations.push(`${relativePath} imports raw default as ${local} from ${specifier}`);
      }
      for (const { imported, local, specifier } of directImports) {
        violations.push(`${relativePath} imports raw ${imported} as ${local} from ${specifier}`);
        if (directCallPattern(local).test(source)) {
          violations.push(`${relativePath} calls raw ${local}()`);
        }
      }
      for (const { local, specifier } of namespaces) {
        violations.push(`${relativePath} imports raw namespace ${local} from ${specifier}`);
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
