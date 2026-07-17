import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { cwd } from 'node:process';
import { describe, expect, it } from 'vitest';
import { APP_SHELL_STORE_KEYS } from '../src/app/appShellModel.js';
import { useClientStore } from '../src/entities/client/model/useClientStore.js';
import {
  collectRouteStoreConsumerKeys,
  sourceFiles,
  storeKeysFromSource,
  validateAppShellStoreContract,
} from './app-shell-store-contract.mjs';

describe('AppShell store selector contract', () => {
  it('derives static member and destructured store keys from AST', () => {
    expect([...storeKeysFromSource(`
      const { activePage, toggleThreadPin } = store;
      store.openForkDraft();
      sourceStore.providerConfig;
      store?.sharedFilesRevision;
    `, 'fixture.jsx')].sort()).toEqual([
      'activePage',
      'openForkDraft',
      'providerConfig',
      'sharedFilesRevision',
      'toggleThreadPin',
    ]);
  });

  it('fails closed for regular and optional computed store access', () => {
    expect(() => storeKeysFromSource('store[key];', 'computed.js')).toThrow(/static identifier key/);
    expect(() => storeKeysFromSource('store?.[key];', 'optional-computed.js')).toThrow(/static identifier key/);
  });

  it('excludes __tests__, test/spec files, and test/spec support from production sources', () => {
    const root = mkdtempSync(join(tmpdir(), 'app-shell-contract-'));
    try {
      mkdirSync(join(root, '__tests__'));
      writeFileSync(join(root, 'Route.jsx'), 'store.activePage;');
      writeFileSync(join(root, '__tests__', 'routeSupport.js'), 'store.testDirectoryOnly;');
      writeFileSync(join(root, 'Route.test.jsx'), 'store.testFileOnly;');
      writeFileSync(join(root, 'Route.spec.js'), 'store.specFileOnly;');
      writeFileSync(join(root, 'routeTestSupport.js'), 'store.testSupportOnly;');
      writeFileSync(join(root, 'route-spec-support.jsx'), 'store.specSupportOnly;');
      expect(sourceFiles(root)).toEqual([join(root, 'Route.jsx')]);
    } finally {
      rmSync(root, { force: true, recursive: true });
    }
  });

  it('rejects missing, stale, and unknown selector contract fields', () => {
    expect(() => validateAppShellStoreContract({
      consumerKeys: ['activePage', 'toggleThreadPin', 'unknownField'],
      exemptions: {},
      producerKeys: ['activePage', 'toggleThreadPin'],
      selectorKeys: ['activePage', 'staleField'],
    })).toThrow(/unknown=\[unknownField\] missing=\[toggleThreadPin\] stale=\[staleField\]/);
  });

  it('rejects selector fields that are live producers but unused by production routes', () => {
    expect(() => validateAppShellStoreContract({
      consumerKeys: ['activePage'],
      exemptions: {},
      producerKeys: ['activePage', 'beginOpeningThread'],
      selectorKeys: ['activePage', 'beginOpeningThread'],
    })).toThrow(/unusedLiveProducer=\[beginOpeningThread\]/);
  });

  it('covers every AST-derived route store consumer with a live producer field', () => {
    const producerKeys = Object.keys(useClientStore.getState()).sort();
    const consumerKeys = collectRouteStoreConsumerKeys(cwd());
    const expectedSelectorKeys = consumerKeys.filter((key) => producerKeys.includes(key));
    expect(APP_SHELL_STORE_KEYS).toEqual(expectedSelectorKeys);
    expect(() => validateAppShellStoreContract({
      consumerKeys,
      producerKeys,
      selectorKeys: APP_SHELL_STORE_KEYS,
    })).not.toThrow();
  });

  it('fails first when a real route key is removed from the selector registry', () => {
    const producerKeys = Object.keys(useClientStore.getState()).sort();
    const consumerKeys = collectRouteStoreConsumerKeys(cwd());
    expect(() => validateAppShellStoreContract({
      consumerKeys,
      producerKeys,
      selectorKeys: APP_SHELL_STORE_KEYS.filter((key) => key !== 'toggleThreadPin'),
    })).toThrow(/missing=.*toggleThreadPin/);
  });
});
