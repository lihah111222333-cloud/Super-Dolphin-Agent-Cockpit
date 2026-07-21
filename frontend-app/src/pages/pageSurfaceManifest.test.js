import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from '@babel/parser';
import { describe, expect, it } from 'vitest';
import {
  COMPUTED_VITEST_MODULE_MOCK,
  importSpecifiers,
  NON_LITERAL_DYNAMIC_IMPORT,
  NON_LITERAL_REQUIRE,
  staticImportSpecifiers,
} from './importSurfaceGuard.test-helper.js';
import { pageSurfaceManifest } from './pageSurfaceManifest.js';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const ownershipModes = new Set(['dto-golden', 'service-boundary']);
const requiredFields = ['entry', 'servicePrefix', 'adapterPrefix', 'serviceEntry', 'ownershipMode', 'ownedStateFiles'];
const sharedDtoGoldenTest = 'pages/shared/featureDtoGolden.test.js';
const dtoGoldenFactories = new Map([
  ['files', { factory: 'createFilesPageService', methods: ['saveTextFile'] }],
  ['memory', { factory: 'createMemoryPageService', methods: ['upsertMemoryEntry', 'mergeMemoryEntries'] }],
  ['observability', { factory: 'createObservabilityPageService', methods: ['listObservabilityRecent'] }],
  ['prompts', { factory: 'createPromptPageService', methods: ['draftPromptIntent', 'writePrompt'] }],
]);

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

function entryReachesService(entry, serviceEntry, featureRoot, visited = new Set()) {
  if (visited.has(entry)) return false;
  visited.add(entry);
  const imports = staticImportSpecifiers(read(entry))
    .map((specifier) => resolveEntryImport(entry, specifier));
  if (imports.includes(serviceEntry)) return true;

  return imports
    .filter((specifier) => (
      specifier.startsWith(featureRoot)
      && /\.[jt]sx?$/.test(specifier)
      && exists(specifier)
    ))
    .some((specifier) => entryReachesService(
      specifier,
      serviceEntry,
      featureRoot,
      visited,
    ));
}

function parseModule(source) {
  return parse(source, {
    sourceType: 'module',
    createImportExpressions: true,
    plugins: ['jsx', 'typescript', 'dynamicImport', 'importAttributes'],
  });
}

function visit(node, visitor) {
  if (!node || typeof node !== 'object') return;
  visitor(node);
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) {
      for (const child of value) visit(child, visitor);
    } else if (value && typeof value === 'object' && typeof value.type === 'string') {
      visit(value, visitor);
    }
  }
}

function propertyName(property) {
  if (property?.type === 'Identifier') return property.name;
  if (property?.type === 'StringLiteral') return property.value;
  return '';
}

function objectPropertyValue(node, name) {
  if (node?.type !== 'ObjectExpression') return null;
  return node.properties
    .find((property) => property.type === 'ObjectProperty' && propertyName(property.key) === name)
    ?.value ?? null;
}

function stringArrayValues(node) {
  if (node?.type !== 'ArrayExpression') return [];
  return node.elements
    .filter((element) => element?.type === 'StringLiteral')
    .map((element) => element.value);
}

function dtoGoldenHarnessCases(source) {
  const cases = [];
  visit(parseModule(source), (node) => {
    if (
      node.type !== 'CallExpression'
      || node.callee?.type !== 'Identifier'
      || !['expectDtoGolden', 'expectSyncDtoError'].includes(node.callee.name)
    ) {
      return;
    }
    const options = node.arguments?.[0];
    const factory = objectPropertyValue(options, 'factory');
    const method = objectPropertyValue(options, 'method');
    cases.push({
      factory: factory?.type === 'Identifier' ? factory.name : '',
      method: method?.type === 'StringLiteral' ? method.value : '',
      methods: stringArrayValues(objectPropertyValue(options, 'methods')),
    });
  });
  return cases;
}

function harnessCoversFactoryMethod(cases, factory, method) {
  return cases.some((entry) => (
    entry.factory === factory
    && entry.method === method
    && entry.methods.includes(method)
  ));
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
      const coverage = dtoGoldenFactories.get(feature);
      if (!coverage) {
        violations.push(`${feature} dto-golden entry must declare a shared harness factory expectation`);
        continue;
      }
      const harnessImports = importSpecifiers(harnessSource)
        .map((specifier) => resolveEntryImport(surface.dtoGoldenTest, specifier));
      const harnessCases = dtoGoldenHarnessCases(harnessSource);
      const missingMethods = coverage.methods.filter(
        (method) => !harnessCoversFactoryMethod(harnessCases, coverage.factory, method),
      );
      if (!harnessImports.includes(surface.serviceEntry) || missingMethods.length > 0) {
        violations.push(`${feature} dtoGoldenTest does not cover ${surface.serviceEntry}`);
      }
    }
    expect(violations).toEqual([]);
  });

  it('declares a reachable feature service boundary for every page entry', () => {
    const missing = [];
    for (const [feature, surface] of Object.entries(pageSurfaceManifest)) {
      const featureRoot = `${path.posix.dirname(surface.entry)}/`;
      if (!entryReachesService(surface.entry, surface.serviceEntry, featureRoot)) {
        missing.push(`${feature}:${surface.entry} does not reach ${surface.serviceEntry}`);
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
        } else if (surface.dtoGoldenTest !== sharedDtoGoldenTest || !surface.dtoGoldenTest.endsWith('.test.js')) {
          violations.push(`${feature} dtoGoldenTest must be ${sharedDtoGoldenTest}`);
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
      handler.apply(thisArg, argsArray);
      await import('../../shared/api/' + 'backendApi.js');
    `)).toEqual([
      '../../shared/api/backendApi.js',
      '../../shared/api/backendApi.js',
      '../../services/modules/fileService.js',
      '../../shared/api/backendApi.js',
      NON_LITERAL_REQUIRE,
      NON_LITERAL_REQUIRE,
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
