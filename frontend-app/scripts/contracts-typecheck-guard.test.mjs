import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import {
  assertCompilerSucceeded,
  assertExactProductionFiles,
  assertRegisteredFiles,
  assertRegisteredTests,
  productionFilesFromListOutput,
  validateCriticalTypecheckConfig,
  validateCriticalTypecheckRegistry,
} from './critical-typecheck-guard.mjs';

const entrypoints = [
  'src/shared/contracts/turnContractValidators.js',
  'src/shared/ui/runUIAction.js',
  'src/shared/ui/actionFailureSink.js',
  'src/shared/ui/publicError.js',
  'src/shared/diagnostics/frontendHealthStore.js',
  'src/shared/api/browser/browserStorage.js',
];

function config(overrides = {}) {
  return {
    compilerOptions: {
      checkJs: true,
      strict: true,
      noImplicitAny: true,
      useUnknownInCatchVariables: true,
      skipLibCheck: false,
    },
    include: entrypoints,
    exclude: ['node_modules', 'dist', 'coverage'],
    ...overrides,
  };
}

function registry(overrides = {}) {
  const surfaces = {
    actionFeedback: ['src/pages/chat/runtime/RuntimeDiffView.jsx'],
    diagnostics: ['src/shared/diagnostics/safeLogFields.js'],
    promptHistory: ['src/features/prompt-history/model/promptHistoryController.js'],
    providerPreference: ['src/entities/client/model/helpers/providerPreferences.js'],
    rpcAdapter: [
      'src/shared/api/backend/backendApiFactoryThread.js',
      'src/shared/api/backend/backendApiCommon.js',
    ],
    storeBridge: ['src/entities/client/model/helpers/bridgeRevision.js'],
    terminalPublicError: ['src/shared/contracts/turnContractValidators.js'],
    uiAction: ['src/shared/ui/runUIAction.js'],
  };
  const registryEntrypoints = Object.values(surfaces).flat().sort();
  return {
    schemaVersion: 1,
    surfaces,
    entrypoints: registryEntrypoints,
    productionFiles: registryEntrypoints,
    testFiles: ['scripts/contracts-typecheck-guard.test.mjs'],
    ...overrides,
  };
}

describe('critical typecheck guard', () => {
  it.each(['checkJs', 'strict', 'noImplicitAny', 'useUnknownInCatchVariables'])(
    'rejects disabled %s',
    (option) => {
      expect(() => validateCriticalTypecheckConfig(config({
        compilerOptions: { ...config().compilerOptions, [option]: false },
      }), entrypoints)).toThrow(`${option} must be true`);
    },
  );

  it('rejects skipLibCheck and missing explicit false', () => {
    expect(() => validateCriticalTypecheckConfig(config({
      compilerOptions: { ...config().compilerOptions, skipLibCheck: true },
    }), entrypoints)).toThrow('skipLibCheck must be false');
    const { skipLibCheck: _removed, ...compilerOptions } = config().compilerOptions;
    expect(() => validateCriticalTypecheckConfig(config({ compilerOptions }), entrypoints))
      .toThrow('skipLibCheck must be false');
  });

  it('rejects missing/stale includes, widened excludes, aliases, and zero production files', () => {
    expect(() => validateCriticalTypecheckConfig(config({ include: [entrypoints[0]] }), entrypoints))
      .toThrow('include exact diff failed');
    expect(() => validateCriticalTypecheckConfig(config({ include: [...entrypoints, 'src/stale.js'] }), entrypoints))
      .toThrow('include exact diff failed');
    expect(() => validateCriticalTypecheckConfig(config({ exclude: [...config().exclude, 'src/shared'] }), entrypoints))
      .toThrow('exclude exact diff failed');
    expect(() => validateCriticalTypecheckConfig(config({
      compilerOptions: { ...config().compilerOptions, paths: { '@critical/*': ['src/empty/*'] } },
    }), entrypoints)).toThrow('must not remap');
    expect(() => assertExactProductionFiles(entrypoints, [])).toThrow('zero production files');
  });

  it('rejects listFiles missing/stale drift from moved files and barrel or re-export closure changes', () => {
    expect(() => assertExactProductionFiles(entrypoints, [entrypoints[0]])).toThrow('missing=');
    expect(() => assertExactProductionFiles(entrypoints, [...entrypoints, 'src/shared/ui/barrel.js']))
      .toThrow('stale=');
  });

  it('normalizes only repo production JS/JSX from TypeScript listFiles output', () => {
    const root = '/repo/frontend-app';
    expect(productionFilesFromListOutput([
      '/repo/frontend-app/node_modules/typescript/lib/lib.es2022.d.ts',
      '/repo/frontend-app/src/shared/ui/runUIAction.js',
      '/repo/frontend-app/src/pages/chat/ChatPage.jsx',
      '/repo/frontend-app/scripts/guard.mjs',
    ].join('\n'), root)).toEqual([
      'src/pages/chat/ChatPage.jsx',
      'src/shared/ui/runUIAction.js',
    ]);
  });

  it('rejects compiler startup and non-zero exits instead of accepting handwritten PASS', () => {
    expect(() => assertCompilerSucceeded({ status: 2, stdout: 'PASS', stderr: 'type error' }, 'compiler'))
      .toThrow('exited 2');
    expect(() => assertCompilerSucceeded({ status: null, error: new Error('ENOENT') }, 'compiler'))
      .toThrow('failed to start');
    expect(() => validateCriticalTypecheckRegistry({ ...registry(), status: 'PASS' }))
      .toThrow('registry keys exact diff failed');
  });

  it('requires every named risk surface and exact registry entrypoints', () => {
    const missingSurface = registry();
    delete missingSurface.surfaces.storeBridge;
    expect(() => validateCriticalTypecheckRegistry(missingSurface)).toThrow('surfaces exact diff failed');

    expect(() => validateCriticalTypecheckRegistry({
      ...registry(),
      entrypoints: registry().entrypoints.slice(1),
    })).toThrow('surface entrypoints exact diff failed');

    expect(() => validateCriticalTypecheckRegistry({
      ...registry(),
      testFiles: ['scripts/handwritten-pass.test.mjs'],
    })).toThrow('testFiles exact diff failed');
  });

  it('rejects deleted source or guard tests and TypeScript suppression escape hatches', () => {
    const root = mkdtempSync(path.join(tmpdir(), 'critical-typecheck-'));
    const sourcePath = path.join(root, 'src', 'critical.js');
    const testPath = path.join(root, 'scripts', 'guard.test.mjs');
    mkdirSync(path.dirname(sourcePath), { recursive: true });
    mkdirSync(path.dirname(testPath), { recursive: true });
    writeFileSync(sourcePath, 'export const critical = 1;\n');
    writeFileSync(testPath, 'export const guardTest = true;\n');
    try {
      expect(() => assertRegisteredFiles(['src/critical.js'], root)).not.toThrow();
      expect(() => assertRegisteredTests(['scripts/guard.test.mjs'], root)).not.toThrow();

      rmSync(sourcePath);
      expect(() => assertRegisteredFiles(['src/critical.js'], root)).toThrow('path is missing');

      writeFileSync(sourcePath, '// @ts-nocheck\nexport const value = 1;\n');
      expect(() => assertRegisteredFiles(['src/critical.js'], root))
        .toThrow('forbidden TypeScript directive');
      writeFileSync(sourcePath, '/** @param {any} value */\nexport function critical(value) { return value; }\n');
      expect(() => assertRegisteredFiles(['src/critical.js'], root)).toThrow('explicit any type');

      rmSync(testPath);
      expect(() => assertRegisteredTests(['scripts/guard.test.mjs'], root)).toThrow('test path is missing');
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it('rejects zero guard tests and wires Vitest without passWithNoTests', () => {
    expect(() => validateCriticalTypecheckRegistry({
      ...registry(),
      testFiles: [],
    })).toThrow('testFiles must be a non-empty array');

    const packageJSON = JSON.parse(
      readFileSync(path.join(process.cwd(), 'package.json'), 'utf8'),
    );
    expect(packageJSON.scripts['typecheck:contracts'])
      .toBe('npm run typecheck:contracts:compiler && npm run typecheck:contracts:test');
    expect(packageJSON.scripts['typecheck:contracts:test'])
      .toContain('scripts/contracts-typecheck-guard.test.mjs');
    expect(packageJSON.scripts['typecheck:contracts:test'])
      .not.toContain('--passWithNoTests');
  });
});
