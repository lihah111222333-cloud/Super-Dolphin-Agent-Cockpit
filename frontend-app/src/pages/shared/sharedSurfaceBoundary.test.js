import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { parseStaticImports } from '../../test-utils/importAst.js';
import { importSpecifiers } from '../importSurfaceGuard.test-helper.js';
import { pageSurfaceManifest } from '../pageSurfaceManifest.js';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const appShellBackendApiAllowlist = new Set([
  'checkAppUpdate',
  'getSidebarState',
  'installLatestAppUpdate',
]);
const manifestServiceEntries = new Set(Object.values(pageSurfaceManifest).map((surface) => surface.serviceEntry));

function read(relPath) {
  return fs.readFileSync(path.join(sourceRoot, relPath), 'utf8');
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

function resolveImportSpecifier(relativePath, specifier) {
  if (!specifier.startsWith('.')) return specifier;
  return path.normalize(path.join(path.dirname(relativePath), specifier)).split(path.sep).join('/');
}

function isBackendApiModule(resolvedSpecifier) {
  return /shared\/api\/backendApi(?:\.js)?$/.test(resolvedSpecifier);
}

function appPageServiceImportViolations(source = read('App.jsx')) {
  return importSpecifiers(source)
    .map((specifier) => resolveImportSpecifier('App.jsx', specifier))
    .filter((resolved) => /^pages\/[^/]+\/services\/.+\.js$/.test(resolved))
    .filter((resolved) => !manifestServiceEntries.has(resolved))
    .map((resolved) => `App.jsx imports unmanifested page service ${resolved}`);
}

function appBackendApiImportViolations(source = read('App.jsx')) {
  const violations = [];
  const staticBackendSpecifiers = new Set();
  for (const entry of parseStaticImports(source)) {
    const resolved = resolveImportSpecifier('App.jsx', entry.specifier);
    if (!isBackendApiModule(resolved)) continue;
    staticBackendSpecifiers.add(resolved);
    if (entry.kind !== 'named') {
      violations.push(`App.jsx imports backendApi as ${entry.kind}`);
      continue;
    }
    if (!appShellBackendApiAllowlist.has(entry.imported)) {
      violations.push(`App.jsx imports app-shell backend API outside allowlist: ${entry.imported}`);
    }
  }
  for (const specifier of importSpecifiers(source)) {
    const resolved = resolveImportSpecifier('App.jsx', specifier);
    if (!isBackendApiModule(resolved)) continue;
    if (staticBackendSpecifiers.has(resolved)) {
      staticBackendSpecifiers.delete(resolved);
    } else {
      violations.push(`App.jsx imports backendApi outside static allowlist via ${specifier}`);
    }
  }
  return violations;
}

function sharedPageFiles() {
  const files = [];
  walk(path.join(sourceRoot, 'pages/shared'), files);
  return files;
}

function sharedPageBackendFacadeViolations() {
  const violations = [];
  for (const filePath of sharedPageFiles()) {
    const relativePath = rel(filePath);
    for (const specifier of importSpecifiers(read(relativePath))) {
      const resolved = resolveImportSpecifier(relativePath, specifier);
      if (isBackendApiModule(resolved)) violations.push(`${relativePath} imports ${specifier}`);
      if (resolved.includes('services/modules/')) violations.push(`${relativePath} imports ${specifier}`);
    }
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

  it('keeps App page-feature service imports listed by the page surface manifest', () => {
    expect(appPageServiceImportViolations()).toEqual([]);
    expect(appPageServiceImportViolations(`
      import { memoryPageService } from './pages/memory/services/memoryPageService.js';
      import { ghostPageService } from './pages/memory/services/ghostPageService.js';
    `)).toEqual([
      'App.jsx imports unmanifested page service pages/memory/services/ghostPageService.js',
    ]);
  });

  it('keeps App app-shell backend APIs on the small allowlist', () => {
    expect(appBackendApiImportViolations()).toEqual([]);
    expect(appBackendApiImportViolations(`
      import { checkAppUpdate, startThread } from './shared/api/backendApi.js';
      await import('./shared/api/backendApi.js');
    `)).toEqual([
      'App.jsx imports app-shell backend API outside allowlist: startThread',
      'App.jsx imports backendApi outside static allowlist via ./shared/api/backendApi.js',
    ]);
  });

  it('keeps shared page components away from backend facades and module services', () => {
    expect(sharedPageBackendFacadeViolations()).toEqual([]);
  });

  it('keeps prompt feature view behind the prompt page service', () => {
    const source = read('features/prompts/PromptPageView.jsx');
    expect(source).not.toContain('shared/api/backendApi.js');
    expect(source).toMatch(/promptPageService/);
  });
});
