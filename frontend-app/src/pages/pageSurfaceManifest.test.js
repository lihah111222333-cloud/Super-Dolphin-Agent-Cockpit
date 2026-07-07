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
const requiredFields = ['entry', 'servicePrefix', 'adapterPrefix', 'serviceEntry', 'ownershipMode', 'ownedStateFiles'];
const sharedDtoGoldenTest = 'pages/shared/featureDtoGolden.test.js';
const dtoGoldenFactories = new Map([
  ['files', 'createFilesPageService'],
  ['memory', 'createMemoryPageService'],
  ['observability', 'createObservabilityPageService'],
  ['prompts', 'createPromptPageService'],
]);

function read(relPath) {
  return fs.readFileSync(path.join(sourceRoot, relPath), 'utf8');
}

function resolveEntryImport(entry, specifier) {
  if (!specifier.startsWith('.')) return '';
  const resolved = path.normalize(path.join(path.dirname(entry), specifier));
  return resolved.split(path.sep).join('/');
}

function manifestContractViolations(manifest) {
  const violations = [];
  for (const [feature, surface] of Object.entries(manifest)) {
    for (const field of requiredFields) {
      if (!(field in surface)) violations.push(`${feature} is missing ${field}`);
    }
    if (surface.servicePrefix !== `pages/${feature}/services/`) {
      violations.push(`${feature} servicePrefix must be pages/${feature}/services/`);
    }
    if (surface.adapterPrefix !== `pages/${feature}/adapters/`) {
      violations.push(`${feature} adapterPrefix must be pages/${feature}/adapters/`);
    }
    if (surface.serviceEntry && !surface.serviceEntry.startsWith(surface.servicePrefix)) {
      violations.push(`${feature} serviceEntry must be under ${surface.servicePrefix}`);
    }
    if (!ownershipModes.has(surface.ownershipMode)) {
      violations.push(`${feature} ownershipMode must be dto-golden or service-boundary`);
    }
    if (!Array.isArray(surface.ownedStateFiles) || surface.ownedStateFiles.length === 0) {
      violations.push(`${feature} ownedStateFiles must name at least one file`);
    }
    if (surface.ownershipMode === 'dto-golden' && !surface.dtoGoldenTest) {
      violations.push(`${feature} dto-golden entry is missing dtoGoldenTest`);
    }
    if (surface.ownershipMode === 'service-boundary' && 'dtoGoldenTest' in surface) {
      violations.push(`${feature} service-boundary entry must not declare placeholder dtoGoldenTest`);
    }
  }
  return violations;
}

describe('page surface manifest', () => {
  it('declares feature ownership fields for every page entry', () => {
    expect(manifestContractViolations(pageSurfaceManifest)).toEqual([]);
  });

  it('fails closed when a feature omits required ownership metadata', () => {
    const missingServiceEntry = {
      files: {
        entry: 'pages/files/FilesPage.jsx',
        servicePrefix: 'pages/files/services/',
        adapterPrefix: 'pages/files/adapters/',
        ownershipMode: 'dto-golden',
        dtoGoldenTest: 'pages/files/services/filesPageService.test.js',
        ownedStateFiles: ['pages/files/FilesPage.jsx'],
      },
    };
    expect(manifestContractViolations(missingServiceEntry)).toContain('files is missing serviceEntry');

    const missingOwnershipMode = {
      files: {
        entry: 'pages/files/FilesPage.jsx',
        servicePrefix: 'pages/files/services/',
        adapterPrefix: 'pages/files/adapters/',
        serviceEntry: 'pages/files/services/filesPageService.js',
        ownedStateFiles: ['pages/files/FilesPage.jsx'],
      },
    };
    expect(manifestContractViolations(missingOwnershipMode)).toContain('files is missing ownershipMode');

    const missingOwnedStateFiles = {
      files: {
        entry: 'pages/files/FilesPage.jsx',
        servicePrefix: 'pages/files/services/',
        adapterPrefix: 'pages/files/adapters/',
        serviceEntry: 'pages/files/services/filesPageService.js',
        ownershipMode: 'dto-golden',
        dtoGoldenTest: 'pages/files/services/filesPageService.test.js',
      },
    };
    expect(manifestContractViolations(missingOwnedStateFiles)).toContain('files is missing ownedStateFiles');
  });

  it('requires real dto golden tests only for dto-golden ownership', () => {
    expect(manifestContractViolations({
      files: {
        entry: 'pages/files/FilesPage.jsx',
        servicePrefix: 'pages/files/services/',
        adapterPrefix: 'pages/files/adapters/',
        serviceEntry: 'pages/files/services/filesPageService.js',
        ownershipMode: 'dto-golden',
        ownedStateFiles: ['pages/files/FilesPage.jsx'],
      },
    })).toContain('files dto-golden entry is missing dtoGoldenTest');

    expect(manifestContractViolations({
      chat: {
        entry: 'pages/chat/ChatPage.jsx',
        servicePrefix: 'pages/chat/services/',
        adapterPrefix: 'pages/chat/adapters/',
        serviceEntry: 'pages/chat/services/chatCodeService.js',
        ownershipMode: 'service-boundary',
        dtoGoldenTest: 'pages/chat/services/chatCodeService.test.js',
        ownedStateFiles: ['pages/chat/ChatPage.jsx'],
      },
    })).toContain('chat service-boundary entry must not declare placeholder dtoGoldenTest');
  });

  it('keeps dto-golden entries on the shared service DTO golden harness', () => {
    const violations = [];
    for (const [feature, surface] of Object.entries(pageSurfaceManifest)) {
      if (surface.ownershipMode !== 'dto-golden') continue;
      if (surface.dtoGoldenTest !== sharedDtoGoldenTest) {
        violations.push(`${feature} dtoGoldenTest must be ${sharedDtoGoldenTest}`);
      }
      const harnessSource = read(surface.dtoGoldenTest);
      const serviceFactory = dtoGoldenFactories.get(feature);
      if (!serviceFactory) {
        violations.push(`${feature} dto-golden entry must declare a shared harness factory expectation`);
        continue;
      }
      const harnessImports = importSpecifiers(harnessSource)
        .map((specifier) => resolveEntryImport(surface.dtoGoldenTest, specifier));
      if (!harnessImports.includes(surface.serviceEntry) || !harnessSource.includes(serviceFactory)) {
        violations.push(`${feature} dtoGoldenTest does not cover ${surface.serviceEntry}`);
      }
    }
    expect(violations).toEqual([]);
  });

  it('declares a feature service boundary for every page entry', () => {
    const missing = [];
    for (const [feature, surface] of Object.entries(pageSurfaceManifest)) {
      const entrySource = read(surface.entry);
      const imports = importSpecifiers(entrySource)
        .map((specifier) => resolveEntryImport(surface.entry, specifier));
      if (!imports.includes(surface.serviceEntry)) {
        missing.push(`${feature}:${surface.entry} does not import ${surface.serviceEntry}`);
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
