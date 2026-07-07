import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { parseStaticImports } from '../test-utils/importAst.js';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const scannedDirs = ['pages', 'features', 'entities'];
const rawFacadeNames = new Set(['callAPI', 'callBackend']);
const migratedBackendApiConsumers = new Set([
  'pages/chat/ChatPage.jsx',
  'pages/files/FilesPage.jsx',
  'pages/memory/MemoryPage.jsx',
  'pages/observability/ObservabilityPage.jsx',
  'pages/settings/SettingsPage.jsx',
  'pages/shared/pageShared.js',
  'pages/workflows/WorkflowPage.jsx',
]);
const migratedBackendApiConsumerPrefixes = [
  'pages/chat/',
  'pages/settings/',
  'pages/skills/',
  'pages/workflows/',
];
const backendApiServiceConsumerPrefixes = [
  'pages/chat/services/',
  'pages/settings/services/',
  'pages/skills/services/',
  'pages/workflows/services/',
];

function collectSourceFiles() {
  const files = [];
  for (const dir of scannedDirs) {
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

function isBackendApiModule(specifier) {
  return /shared\/api\/backendApi(?:\.js)?$/.test(specifier);
}

function backendApiImports(source) {
  return parseStaticImports(source).filter((entry) => isBackendApiModule(entry.specifier));
}

function backendApiNamedImports(source) {
  return backendApiImports(source)
    .filter((entry) => entry.kind === 'named')
    .map((entry) => entry.imported);
}

function importsBackendApi(source) {
  return backendApiImports(source).length > 0;
}

function isMigratedBackendApiConsumer(relativePath) {
  return migratedBackendApiConsumers.has(relativePath)
    || migratedBackendApiConsumerPrefixes.some((prefix) => relativePath.startsWith(prefix));
}

function isBackendApiServiceConsumer(relativePath) {
  return backendApiServiceConsumerPrefixes.some((prefix) => relativePath.startsWith(prefix));
}

describe('backend API consumer guardrails', () => {
  it('parses backendApi imports with the TypeScript AST', () => {
    const source = [
      "import backendApiDefault from '../shared/api/backendApi.js';",
      "import * as backend from '../shared/api/backendApi.js';",
      'import {',
      '  callAPI as rawCall,',
      '  callBackend,',
      '  getDashboardPage,',
      "} from '../shared/api/backendApi.js';",
      'const ignored = "from \'../shared/api/backendApi.js\'";',
      "// import { callAPI } from '../shared/api/backendApi.js';",
    ].join('\n');

    expect(backendApiImports(source)).toEqual([
      { kind: 'default', imported: 'default', local: 'backendApiDefault', specifier: '../shared/api/backendApi.js' },
      { kind: 'namespace', imported: '*', local: 'backend', specifier: '../shared/api/backendApi.js' },
      { kind: 'named', imported: 'callAPI', local: 'rawCall', specifier: '../shared/api/backendApi.js' },
      { kind: 'named', imported: 'callBackend', local: 'callBackend', specifier: '../shared/api/backendApi.js' },
      { kind: 'named', imported: 'getDashboardPage', local: 'getDashboardPage', specifier: '../shared/api/backendApi.js' },
    ]);
    expect(backendApiNamedImports(source)).toEqual(['callAPI', 'callBackend', 'getDashboardPage']);
    expect(importsBackendApi(source)).toBe(true);
  });

  it('ignores commented and string backendApi references', () => {
    const source = [
      'const fixture = "import { callAPI } from \'../shared/api/backendApi.js\'";',
      "// import * as backend from '../shared/api/backendApi.js';",
      "/* import backendApiDefault from '../shared/api/backendApi.js'; */",
    ].join('\n');

    expect(backendApiImports(source)).toEqual([]);
    expect(backendApiNamedImports(source)).toEqual([]);
    expect(importsBackendApi(source)).toBe(false);
  });

  it('keeps page, feature, and entity consumers on named backend facade imports', () => {
    const violations = [];

    for (const filePath of collectSourceFiles()) {
      const relativePath = rel(filePath);
      const source = fs.readFileSync(filePath, 'utf8');
      for (const imported of backendApiNamedImports(source)) {
        if (rawFacadeNames.has(imported)) {
          violations.push(`${relativePath} imports raw ${imported}`);
        }
      }
    }

    expect(violations).toEqual([]);
  });

  it('keeps migrated page surfaces behind service modules', () => {
    const violations = [];

    for (const filePath of collectSourceFiles()) {
      const relativePath = rel(filePath);
      if (!isMigratedBackendApiConsumer(relativePath) || isBackendApiServiceConsumer(relativePath)) continue;
      const source = fs.readFileSync(filePath, 'utf8');
      if (importsBackendApi(source)) {
        violations.push(`${relativePath} imports shared/api/backendApi.js directly`);
      }
    }

    expect(violations).toEqual([]);
  });
});
