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
const ownershipModes = new Set(['dto-golden', 'service-boundary']);

function read(relPath) {
  return fs.readFileSync(path.join(sourceRoot, relPath), 'utf8');
}

function exists(relPath) {
  return fs.existsSync(path.join(sourceRoot, relPath));
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
      expect(surface.serviceEntry).toEqual(expect.stringMatching(new RegExp(`^pages/${feature}/services/.+\\.js$`)));
      expect(ownershipModes.has(surface.ownershipMode)).toBe(true);
      expect(surface.ownedStateFiles).toEqual(expect.arrayContaining([surface.entry]));
      expect(surface.ownedStateFiles.length).toBeGreaterThan(0);
      expect(exists(surface.entry)).toBe(true);
      expect(exists(surface.serviceEntry)).toBe(true);
      for (const ownedStateFile of surface.ownedStateFiles) {
        expect(exists(ownedStateFile)).toBe(true);
      }
      const entrySource = read(surface.entry);
      const imports = importSpecifiers(entrySource)
        .map((specifier) => resolveEntryImport(surface.entry, specifier));
      if (!imports.includes(surface.serviceEntry)) {
        missing.push(`${feature}:${surface.entry} does not import ${surface.serviceEntry}`);
      }
    }
    expect(missing).toEqual([]);
  });

  it('keeps DTO golden ownership explicit and avoids service-boundary placeholders', () => {
    const violations = [];
    for (const [feature, surface] of Object.entries(pageSurfaceManifest)) {
      const hasDtoGoldenTest = Object.prototype.hasOwnProperty.call(surface, 'dtoGoldenTest');
      if (surface.ownershipMode === 'dto-golden') {
        if (!hasDtoGoldenTest || !surface.dtoGoldenTest) {
          violations.push(`${feature} is dto-golden without dtoGoldenTest`);
        } else if (!surface.dtoGoldenTest.startsWith(surface.servicePrefix) || !surface.dtoGoldenTest.endsWith('.test.js')) {
          violations.push(`${feature} dtoGoldenTest must live beside its feature service`);
        } else if (!exists(surface.dtoGoldenTest)) {
          violations.push(`${feature} dtoGoldenTest does not exist: ${surface.dtoGoldenTest}`);
        }
        continue;
      }
      if (hasDtoGoldenTest) {
        violations.push(`${feature} is service-boundary but declares placeholder dtoGoldenTest`);
      }
    }
    expect(violations).toEqual([]);
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
