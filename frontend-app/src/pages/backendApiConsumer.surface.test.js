import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { parseStaticImports } from '../test-utils/importAst.js';
import {
  COMPUTED_VITEST_MODULE_MOCK,
  importSpecifiers,
  namedImportsFrom,
  NON_LITERAL_DYNAMIC_IMPORT,
  staticImportSpecifiers,
} from './importSurfaceGuard.test-helper.js';
import { pageSurfaceManifest } from './pageSurfaceManifest.js';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const rawNamedImportScanDirs = ['pages', 'features', 'entities'];
const pageSurfaceScanDirs = ['pages', 'features'];
const rawFacadeNames = new Set(['callAPI', 'callBackend']);
const promptFeatureSurface = 'features/prompts/PromptPageView.jsx';
const promptPageServicePath = 'pages/prompts/services/promptPageService.js';

function collectSourceFiles(dirs) {
  const files = [];
  for (const dir of dirs) {
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

function referencesBackendApi(specifier) {
  return specifier.includes('shared/api/backendApi');
}

function referencesModuleService(specifier) {
  return specifier.includes('services/modules/');
}

function isFeatureServiceConsumer(relativePath) {
  return Object.values(pageSurfaceManifest)
    .some((surface) => relativePath.startsWith(surface.servicePrefix));
}

function isGuardedSurface(relativePath) {
  return relativePath.startsWith('pages/') || relativePath === promptFeatureSurface;
}

function resolveImportSpecifier(relativePath, specifier) {
  if (!specifier.startsWith('.')) return specifier;
  return path.normalize(path.join(path.dirname(relativePath), specifier)).split(path.sep).join('/');
}

function importsPromptPageService(relativePath, source) {
  return staticImportSpecifiers(source)
    .some((specifier) => resolveImportSpecifier(relativePath, specifier) === promptPageServicePath);
}

function backendApiRawFacadeViolations(relativePath, source) {
  if (isFeatureServiceConsumer(relativePath)) return [];
  return namedImportsFrom(source, referencesBackendApi)
    .filter((imported) => rawFacadeNames.has(imported))
    .map((imported) => `${relativePath} imports raw ${imported}`);
}

function surfaceImportViolations(relativePath, source) {
  if (!isGuardedSurface(relativePath) || isFeatureServiceConsumer(relativePath)) return [];
  const violations = [];
  for (const specifier of importSpecifiers(source)) {
    if (specifier === NON_LITERAL_DYNAMIC_IMPORT) {
      violations.push(`${relativePath} uses non-literal dynamic import`);
      continue;
    }
    if (specifier === COMPUTED_VITEST_MODULE_MOCK) {
      violations.push(`${relativePath} uses computed Vitest module mock`);
      continue;
    }
    if (referencesBackendApi(specifier)) {
      violations.push(`${relativePath} imports ${specifier}`);
    }
    if (referencesModuleService(specifier)) {
      violations.push(`${relativePath} imports ${specifier}`);
    }
  }
  if (relativePath === promptFeatureSurface && !importsPromptPageService(relativePath, source)) {
    violations.push(`${relativePath} does not import promptPageService`);
  }
  return violations;
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

    for (const filePath of collectSourceFiles(rawNamedImportScanDirs)) {
      const relativePath = rel(filePath);
      const source = fs.readFileSync(filePath, 'utf8');
      violations.push(...backendApiRawFacadeViolations(relativePath, source));
    }

    expect(violations).toEqual([]);
  });

  it('keeps page and prompt feature surfaces behind manifest service modules', () => {
    const violations = [];

    for (const filePath of collectSourceFiles(pageSurfaceScanDirs)) {
      const relativePath = rel(filePath);
      const source = fs.readFileSync(filePath, 'utf8');
      violations.push(...surfaceImportViolations(relativePath, source));
    }

    expect(violations).toEqual([]);
  });

  it('detects recursive backend import bypasses in guarded surfaces', () => {
    expect(surfaceImportViolations('pages/files/FilesPage.jsx', `
      await import('../../shared/api/backendApi.js');
      await import('../../shared/api/' + 'backendApi.js');
      export { callAPI } from '../../shared/api/backendApi.js';
      export * from '../../shared/api/backendApi.js';
      const api = require('../../shared/api/backendApi.js');
      vi.mock('../../shared/api/backendApi.js', () => ({}));
      vi.doMock('../../shared/api/backendApi.js', () => ({}));
      vi.unstable_mockModule('../../shared/api/backendApi.js', () => ({}));
      vi['mock']('../../shared/api/backendApi.js', () => ({}));
      vi['doMock']('../../shared/api/backendApi.js', () => ({}));
      vi[mockName]('../../shared/api/backendApi.js', () => ({}));
      vi['doMock'].call(vi, '../../shared/api/backendApi.js', () => ({}));
      vi[mockName].call(vi, '../../shared/api/backendApi.js', () => ({}));
      vi['doMock'].apply(vi, ['../../shared/api/backendApi.js', () => ({})]);
      vi['doMock'].apply(vi, [backendApiPath, () => ({})]);
      vi[mockName].apply(vi, [backendApiPath, () => ({})]);
      Reflect.apply(vi.doMock, vi, ['../../shared/api/backendApi.js', () => ({})]);
      Reflect.apply(vi[mockName], vi, [backendApiPath, () => ({})]);
    `)).toEqual([
      'pages/files/FilesPage.jsx imports ../../shared/api/backendApi.js',
      'pages/files/FilesPage.jsx uses non-literal dynamic import',
      'pages/files/FilesPage.jsx imports ../../shared/api/backendApi.js',
      'pages/files/FilesPage.jsx imports ../../shared/api/backendApi.js',
      'pages/files/FilesPage.jsx imports ../../shared/api/backendApi.js',
      'pages/files/FilesPage.jsx imports ../../shared/api/backendApi.js',
      'pages/files/FilesPage.jsx imports ../../shared/api/backendApi.js',
      'pages/files/FilesPage.jsx imports ../../shared/api/backendApi.js',
      'pages/files/FilesPage.jsx imports ../../shared/api/backendApi.js',
      'pages/files/FilesPage.jsx imports ../../shared/api/backendApi.js',
      'pages/files/FilesPage.jsx uses computed Vitest module mock',
      'pages/files/FilesPage.jsx imports ../../shared/api/backendApi.js',
      'pages/files/FilesPage.jsx uses computed Vitest module mock',
      'pages/files/FilesPage.jsx imports ../../shared/api/backendApi.js',
      'pages/files/FilesPage.jsx uses computed Vitest module mock',
      'pages/files/FilesPage.jsx uses computed Vitest module mock',
      'pages/files/FilesPage.jsx imports ../../shared/api/backendApi.js',
      'pages/files/FilesPage.jsx uses computed Vitest module mock',
    ]);
  });

  it('does not accept promptPageService mentions without a real import', () => {
    expect(surfaceImportViolations(promptFeatureSurface, `
      // promptPageService appears in a comment but is not imported.
      const x = 'promptPageService';
      export function PromptPageView() {
        return x;
      }
    `)).toContain('features/prompts/PromptPageView.jsx does not import promptPageService');

    expect(surfaceImportViolations(promptFeatureSurface, `
      export { promptPageService } from '../../pages/prompts/services/promptPageService.js';
    `)).toContain('features/prompts/PromptPageView.jsx does not import promptPageService');

    expect(surfaceImportViolations(promptFeatureSurface, `
      import { promptPageService } from '../../pages/prompts/services/promptPageService.js';
      export function PromptPageView() {
        return promptPageService;
      }
    `)).toEqual([]);
  });

  it('allows ordinary non-Vitest apply calls', () => {
    expect(importSpecifiers('handler.apply(thisArg, argsArray);')).toEqual([]);
    expect(surfaceImportViolations('pages/chat/ChatPage.jsx', 'handler.apply(thisArg, argsArray);')).toEqual([]);
  });
});
