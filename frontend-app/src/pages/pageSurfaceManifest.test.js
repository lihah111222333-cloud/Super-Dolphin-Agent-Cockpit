import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  COMPUTED_VITEST_MODULE_MOCK,
  importSpecifiers,
  NON_LITERAL_DYNAMIC_IMPORT,
} from './importSurfaceGuard.test-helper.js';
import { pageSurfaceManifest } from './pageSurfaceManifest.js';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

function read(relPath) {
  return fs.readFileSync(path.join(sourceRoot, relPath), 'utf8');
}

function resolveEntryImport(entry, specifier) {
  if (!specifier.startsWith('.')) return '';
  const resolved = path.normalize(path.join(path.dirname(entry), specifier));
  return resolved.split(path.sep).join('/');
}

describe('page surface manifest', () => {
  it('declares a feature service boundary for every page entry', () => {
    const missing = [];
    for (const [feature, surface] of Object.entries(pageSurfaceManifest)) {
      expect(surface.servicePrefix).toBe(`pages/${feature}/services/`);
      expect(surface.adapterPrefix).toBe(`pages/${feature}/adapters/`);
      const entrySource = read(surface.entry);
      const imports = importSpecifiers(entrySource)
        .map((specifier) => resolveEntryImport(surface.entry, specifier));
      if (!imports.some((resolved) => resolved.startsWith(surface.servicePrefix))) {
        missing.push(`${feature}:${surface.entry} does not import a service under ${surface.servicePrefix}`);
      }
    }
    expect(missing).toEqual([]);
  });

  it('prevents page entries from importing shared module services directly', () => {
    const violations = [];
    for (const surface of Object.values(pageSurfaceManifest)) {
      const entrySource = read(surface.entry);
      for (const specifier of importSpecifiers(entrySource)) {
        if (specifier.includes('/services/modules/')) {
          violations.push(`${surface.entry} imports ${specifier}`);
        }
      }
    }
    expect(violations).toEqual([]);
  });

  it('detects non-static backend imports before they can bypass the surface guard', () => {
    expect(importSpecifiers(`
      await import('../../shared/api/backendApi.js');
      export { callAPI } from '../../shared/api/backendApi.js';
      export * from '../../services/modules/fileService.js';
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
      handler.apply(thisArg, argsArray);
      await import('../../shared/api/' + 'backendApi.js');
    `)).toEqual([
      '../../shared/api/backendApi.js',
      '../../shared/api/backendApi.js',
      '../../services/modules/fileService.js',
      '../../shared/api/backendApi.js',
      '../../shared/api/backendApi.js',
      '../../shared/api/backendApi.js',
      '../../shared/api/backendApi.js',
      '../../shared/api/backendApi.js',
      '../../shared/api/backendApi.js',
      COMPUTED_VITEST_MODULE_MOCK,
      '../../shared/api/backendApi.js',
      COMPUTED_VITEST_MODULE_MOCK,
      '../../shared/api/backendApi.js',
      COMPUTED_VITEST_MODULE_MOCK,
      COMPUTED_VITEST_MODULE_MOCK,
      '../../shared/api/backendApi.js',
      COMPUTED_VITEST_MODULE_MOCK,
      NON_LITERAL_DYNAMIC_IMPORT,
    ]);
  });
});
