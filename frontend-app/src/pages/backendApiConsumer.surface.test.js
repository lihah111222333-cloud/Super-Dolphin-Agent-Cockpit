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
  NON_LITERAL_REQUIRE,
  staticImportSpecifiers,
} from './importSurfaceGuard.test-helper.js';
import { pageSurfaceManifest } from './pageSurfaceManifest.js';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const rawNamedImportScanDirs = ['pages', 'features', 'entities'];
const pageSurfaceScanDirs = ['pages', 'features'];
const rawFacadeNames = new Set(['callAPI', 'callBackend']);
const featureModuleServices = new Map([
  ['files', 'fileService.js'],
  ['memory', 'memoryService.js'],
  ['observability', 'observabilityService.js'],
]);
const featureViewSurfaces = new Map([
  ['features/prompts/PromptPageView.jsx', pageSurfaceManifest.prompts],
]);

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

function featureForSurface(surface) {
  for (const [feature, candidate] of Object.entries(pageSurfaceManifest)) {
    if (candidate === surface) return feature;
  }
  return '';
}

function resolveImportSpecifier(relativePath, specifier) {
  if (!specifier.startsWith('.')) return specifier;
  return path.normalize(path.join(path.dirname(relativePath), specifier)).split(path.sep).join('/');
}

function surfaceForPath(relativePath) {
  if (featureViewSurfaces.has(relativePath)) return featureViewSurfaces.get(relativePath);
  return Object.values(pageSurfaceManifest)
    .find((surface) => (
      relativePath === surface.entry
      || relativePath === surface.serviceEntry
      || relativePath.startsWith(surface.servicePrefix)
      || relativePath.startsWith(surface.adapterPrefix)
      || surface.ownedStateFiles.includes(relativePath)
    ));
}

function serviceEntryPaths() {
  return new Set(Object.values(pageSurfaceManifest).map((surface) => surface.serviceEntry));
}

function isFeatureServiceEntry(relativePath) {
  return serviceEntryPaths().has(relativePath);
}

function moduleServiceViolation(relativePath, specifier, surface, serviceEntry) {
  if (!referencesModuleService(specifier)) return '';
  if (!serviceEntry) return `${relativePath} imports ${specifier}`;
  const feature = featureForSurface(surface);
  const allowedModule = featureModuleServices.get(feature);
  if (!allowedModule || !specifier.endsWith(`/services/modules/${allowedModule}`)) {
    return `${relativePath} imports non-owned module service ${specifier}`;
  }
  return '';
}

function isGuardedSurface(relativePath) {
  return relativePath.startsWith('pages/') || featureViewSurfaces.has(relativePath);
}

function importsOwnServiceEntry(relativePath, source, surface) {
  if (!surface) return false;
  return staticImportSpecifiers(source)
    .some((specifier) => resolveImportSpecifier(relativePath, specifier) === surface.serviceEntry);
}

function crossFeatureSurfaceViolations(relativePath, source) {
  const owner = surfaceForPath(relativePath);
  if (!owner) return [];
  const violations = [];
  for (const specifier of importSpecifiers(source)) {
    if (
      specifier === NON_LITERAL_DYNAMIC_IMPORT
      || specifier === NON_LITERAL_REQUIRE
      || specifier === COMPUTED_VITEST_MODULE_MOCK
    ) {
      continue;
    }
    const resolved = resolveImportSpecifier(relativePath, specifier);
    for (const [feature, surface] of Object.entries(pageSurfaceManifest)) {
      if (surface === owner) continue;
      if (resolved.startsWith(surface.servicePrefix) || resolved.startsWith(surface.adapterPrefix)) {
        violations.push(`${relativePath} imports ${feature} surface ${specifier}`);
      }
    }
  }
  return violations;
}

function backendApiRawFacadeViolations(relativePath, source) {
  if (isFeatureServiceEntry(relativePath)) return [];
  return namedImportsFrom(source, referencesBackendApi)
    .filter((imported) => rawFacadeNames.has(imported))
    .map((imported) => `${relativePath} imports raw ${imported}`);
}

function surfaceImportViolations(relativePath, source) {
  if (!isGuardedSurface(relativePath)) return [];
  const violations = [];
  const surface = surfaceForPath(relativePath);
  const serviceEntry = isFeatureServiceEntry(relativePath);

  for (const specifier of importSpecifiers(source)) {
    if (specifier === NON_LITERAL_DYNAMIC_IMPORT) {
      violations.push(`${relativePath} uses non-literal dynamic import`);
      continue;
    }
    if (specifier === NON_LITERAL_REQUIRE) {
      violations.push(`${relativePath} uses non-literal require`);
      continue;
    }
    if (specifier === COMPUTED_VITEST_MODULE_MOCK) {
      violations.push(`${relativePath} uses computed Vitest module mock`);
      continue;
    }
    if (!serviceEntry && referencesBackendApi(specifier)) {
      violations.push(`${relativePath} imports ${specifier}`);
    }
    const moduleViolation = moduleServiceViolation(relativePath, specifier, surface, serviceEntry);
    if (moduleViolation) violations.push(moduleViolation);
  }

  violations.push(...crossFeatureSurfaceViolations(relativePath, source));

  if (featureViewSurfaces.has(relativePath) && !importsOwnServiceEntry(relativePath, source, surface)) {
    violations.push(`${relativePath} does not import ${surface.serviceEntry}`);
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

  it('allows backend facade and module service imports only from manifest service entries', () => {
    const violations = [];

    for (const filePath of collectSourceFiles(pageSurfaceScanDirs)) {
      const relativePath = rel(filePath);
      const source = fs.readFileSync(filePath, 'utf8');
      violations.push(...surfaceImportViolations(relativePath, source));
    }

    expect(violations).toEqual([]);
  });

  it('treats manifest serviceEntry as the only backend facade owner for a feature', () => {
    expect(surfaceImportViolations('pages/files/services/filesPageService.js', `
      import * as fileService from '../../../services/modules/fileService.js';
    `)).toEqual([]);

    expect(surfaceImportViolations('pages/files/services/fileServiceHelper.js', `
      import * as fileService from '../../../services/modules/fileService.js';
    `)).toEqual([
      'pages/files/services/fileServiceHelper.js imports ../../../services/modules/fileService.js',
    ]);
  });

  it('prevents feature pages from importing another feature service or adapter', () => {
    expect(surfaceImportViolations('pages/files/FilesPage.jsx', `
      import { upsertMemoryEntry } from '../memory/services/memoryPageService.js';
      import { workflowOrderedNodes } from '../workflows/adapters/workflowDisplayAdapter.js';
    `)).toEqual([
      'pages/files/FilesPage.jsx imports memory surface ../memory/services/memoryPageService.js',
      'pages/files/FilesPage.jsx imports workflows surface ../workflows/adapters/workflowDisplayAdapter.js',
    ]);
  });

  it('detects recursive backend import bypasses in guarded surfaces', () => {
    expect(surfaceImportViolations('pages/files/FilesPage.jsx', `
      await import('../../shared/api/backendApi.js');
      await import('../../shared/api/' + 'backendApi.js');
      export { callAPI } from '../../shared/api/backendApi.js';
      export * from '../../shared/api/backendApi.js';
      const api = require('../../shared/api/backendApi.js');
      const computedApi = require(backendApiPath);
      const joinedApi = require('../../shared/api/' + 'backendApi.js');
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
      'pages/files/FilesPage.jsx uses non-literal require',
      'pages/files/FilesPage.jsx uses non-literal require',
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

  it('does not accept feature service mentions without a real static import', () => {
    expect(surfaceImportViolations('features/prompts/PromptPageView.jsx', `
      // promptPageService appears in a comment but is not imported.
      const x = 'promptPageService';
      export function PromptPageView() {
        return x;
      }
    `)).toContain('features/prompts/PromptPageView.jsx does not import pages/prompts/services/promptPageService.js');

    expect(surfaceImportViolations('features/prompts/PromptPageView.jsx', `
      export { promptPageService } from '../../pages/prompts/services/promptPageService.js';
    `)).toContain('features/prompts/PromptPageView.jsx does not import pages/prompts/services/promptPageService.js');

    expect(surfaceImportViolations('features/prompts/PromptPageView.jsx', `
      import { promptPageService } from '../../pages/prompts/services/promptPageService.js';
      export function PromptPageView() {
        return promptPageService;
      }
    `)).toEqual([]);
  });

  it('detects cross-feature service and adapter imports', () => {
    expect(surfaceImportViolations('pages/files/FilesPage.jsx', `
      import { memoryPageService } from '../memory/services/memoryPageService.js';
      import { threadStatusBusy } from '../chat/adapters/threadStateAdapter.js';
    `)).toEqual([
      'pages/files/FilesPage.jsx imports memory surface ../memory/services/memoryPageService.js',
      'pages/files/FilesPage.jsx imports chat surface ../chat/adapters/threadStateAdapter.js',
    ]);
  });

  it('allows same-feature service helper files when they do not import backend facades', () => {
    expect(surfaceImportViolations('pages/files/services/fileDtoHelper.js', `
      import { normalizeFilePath } from '../adapters/filePathAdapter.js';
      export function normalizeSavePayload(payload) {
        return { ...payload, path: normalizeFilePath(payload.path) };
      }
    `)).toEqual([]);
  });

  it('rejects backend facades from same-feature service helper files', () => {
    expect(surfaceImportViolations('pages/files/services/fileDtoHelper.js', `
      import { saveTextFile } from '../../../shared/api/backendApi.js';
      import * as fileService from '../../../services/modules/fileService.js';
    `)).toEqual([
      'pages/files/services/fileDtoHelper.js imports ../../../shared/api/backendApi.js',
      'pages/files/services/fileDtoHelper.js imports ../../../services/modules/fileService.js',
    ]);
  });

  it('keeps manifest serviceEntry as the only feature surface that may import backend facades', () => {
    expect(surfaceImportViolations('pages/files/services/filesPageService.js', `
      import { saveTextFile } from '../../../shared/api/backendApi.js';
      import * as fileService from '../../../services/modules/fileService.js';
    `)).toEqual([]);

    expect(surfaceImportViolations('pages/files/services/filesPageService.js', `
      import * as memoryService from '../../../services/modules/memoryService.js';
    `)).toEqual([
      'pages/files/services/filesPageService.js imports non-owned module service ../../../services/modules/memoryService.js',
    ]);
  });

  it('allows ordinary non-Vitest apply calls', () => {
    expect(importSpecifiers('handler.apply(thisArg, argsArray);')).toEqual([]);
    expect(surfaceImportViolations('pages/chat/ChatPage.jsx', 'handler.apply(thisArg, argsArray);')).toEqual([]);
  });
});
