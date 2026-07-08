import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { parseStaticImports } from '../../test-utils/importAst.js';
import {
  COMPUTED_VITEST_MODULE_MOCK,
  importSpecifiers,
  NON_LITERAL_DYNAMIC_IMPORT,
  NON_LITERAL_REQUIRE,
} from '../importSurfaceGuard.test-helper.js';
import { pageSurfaceManifest } from '../pageSurfaceManifest.js';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const appShellBackendApiAllowlist = new Set([
  'checkAppUpdate',
  'getSidebarState',
  'installLatestAppUpdate',
]);
const ownerlessFeatureSurfaceImportAllowlist = new Map([
  ['App.jsx', new Set([
    'pages/chat/adapters/threadStateAdapter.js',
    'pages/memory/services/memoryPageService.js',
  ])],
  ['pages/shared/pageShared.js', new Set([
    'pages/memory/services/memoryPageService.js',
  ])],
]);

function read(relPath) {
  return fs.readFileSync(path.join(sourceRoot, relPath), 'utf8');
}

function resolveImportSpecifier(relativePath, specifier) {
  if (!specifier.startsWith('.')) return specifier;
  return path.normalize(path.join(path.dirname(relativePath), specifier)).split(path.sep).join('/');
}

function collectSourceFiles(dir) {
  const files = [];
  walk(path.join(sourceRoot, dir), files);
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

function appShellBackendApiViolations() {
  return parseStaticImports(read('App.jsx'), 'App.jsx')
    .filter((entry) => entry.specifier.includes('shared/api/backendApi'))
    .filter((entry) => entry.kind !== 'named' || !appShellBackendApiAllowlist.has(entry.imported))
    .map((entry) => `App.jsx imports unowned app-shell backend API ${entry.imported}`);
}

function appShellFeatureServiceViolations() {
  const manifestServiceEntries = new Set(Object.values(pageSurfaceManifest).map((surface) => surface.serviceEntry));
  return parseStaticImports(read('App.jsx'), 'App.jsx')
    .map((entry) => resolveImportSpecifier('App.jsx', entry.specifier))
    .filter((specifier) => specifier.startsWith('pages/') && specifier.includes('/services/'))
    .filter((specifier) => !manifestServiceEntries.has(specifier))
    .map((specifier) => `App.jsx imports feature service outside manifest ${specifier}`);
}

function pageSharedModuleServiceViolations() {
  return importSpecifiers(read('pages/shared/pageShared.js'))
    .filter((specifier) => specifier.includes('services/modules/'))
    .map((specifier) => `pages/shared/pageShared.js imports ${specifier}`);
}

function sharedBackendFacadeViolations() {
  const violations = [];
  for (const filePath of collectSourceFiles('pages/shared')) {
    const relativePath = rel(filePath);
    for (const entry of parseStaticImports(read(relativePath), relativePath)) {
      if (entry.specifier.includes('shared/api/backendApi') || entry.specifier.includes('services/modules/')) {
        violations.push(`${relativePath} imports backend facade ${entry.specifier}`);
      }
    }
  }
  return violations;
}

function ownerlessSurfaceFiles() {
  return [
    path.join(sourceRoot, 'App.jsx'),
    ...collectSourceFiles('pages/shared'),
  ];
}

function isFeatureServiceOrAdapter(resolvedSpecifier) {
  return Object.values(pageSurfaceManifest).some((surface) => (
    resolvedSpecifier.startsWith(surface.servicePrefix)
    || resolvedSpecifier.startsWith(surface.adapterPrefix)
  ));
}

function ownerlessFeatureSurfaceImportViolations(relativePath, source) {
  const allowlist = ownerlessFeatureSurfaceImportAllowlist.get(relativePath) ?? new Set();
  const violations = [];
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
    const resolved = resolveImportSpecifier(relativePath, specifier);
    if (!isFeatureServiceOrAdapter(resolved) || allowlist.has(resolved)) continue;
    violations.push(`${relativePath} imports ownerless feature surface ${specifier}`);
  }
  return violations;
}

function actualOwnerlessFeatureSurfaceImportViolations() {
  const violations = [];
  for (const filePath of ownerlessSurfaceFiles()) {
    const relativePath = rel(filePath);
    violations.push(...ownerlessFeatureSurfaceImportViolations(relativePath, read(relativePath)));
  }
  return violations;
}

describe('shared page surface boundary', () => {
  it('keeps App memory badge and pageShared behind page-owned memory services', () => {
    const checked = ['App.jsx', 'pages/shared/pageShared.js'];
    const violations = [];
    for (const relPath of checked) {
      const source = read(relPath);
      if (source.includes('services/modules/memoryService.js')) violations.push(`${relPath} imports memoryService`);
    }
    expect(violations).toEqual([]);
    expect(read('App.jsx')).not.toMatch(/\bfetchMemoryDashboard\b/);
    expect(read('App.jsx')).toMatch(/memory(?:Page|Badge)Service/);
    expect(read('pages/shared/pageShared.js')).toMatch(/memory(?:Page|Badge)Service/);
  });

  it('keeps App shell backend API calls on a small explicit allowlist', () => {
    expect(appShellBackendApiViolations()).toEqual([]);
  });

  it('allows App.jsx to import only manifest-listed feature services', () => {
    expect(appShellFeatureServiceViolations()).toEqual([]);
  });

  it('keeps pageShared away from shared module services', () => {
    expect(pageSharedModuleServiceViolations()).toEqual([]);
  });

  it('keeps shared page components away from backend facades', () => {
    expect(sharedBackendFacadeViolations()).toEqual([]);
  });

  it('keeps App and shared page files away from ownerless feature services and adapters', () => {
    expect(actualOwnerlessFeatureSurfaceImportViolations()).toEqual([]);
  });

  it('blocks new ownerless feature service and adapter imports unless explicitly allowlisted', () => {
    expect(ownerlessFeatureSurfaceImportViolations('pages/shared/SharedWidget.jsx', `
      import { promptPageService } from '../prompts/services/promptPageService.js';
      import { threadStatusBusy } from '../chat/adapters/threadStateAdapter.js';
    `)).toEqual([
      'pages/shared/SharedWidget.jsx imports ownerless feature surface ../prompts/services/promptPageService.js',
      'pages/shared/SharedWidget.jsx imports ownerless feature surface ../chat/adapters/threadStateAdapter.js',
    ]);
  });

  it('keeps prompt feature view behind the prompt page service', () => {
    const source = read('features/prompts/PromptPageView.jsx');
    expect(source).not.toContain('shared/api/backendApi.js');
    expect(source).toMatch(/promptPageService/);
  });
});
