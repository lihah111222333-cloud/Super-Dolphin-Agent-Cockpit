import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  collectContractStoreGuardViolations,
  contractStoreGuardRatchetFailures,
  contractStoreGuardViolationsFromSources,
  contractStoreGuardViolationsInSource,
  summarizeContractStoreGuardViolations,
} from './frontend-contract-store-guard.mjs';

describe('frontend contract/store guard', () => {
  it('excludes only the anchored test-helper suffix from production scans', () => {
    const root = mkdtempSync(join(tmpdir(), 'frontend-contract-store-guard-'));
    try {
      mkdirSync(join(root, 'src'));
      const source = 'const timestamp = Date.parse(value);';
      writeFileSync(join(root, 'src', 'fixture.test-helper.jsx'), source);
      writeFileSync(join(root, 'src', 'fixture-test-helper.jsx'), source);
      writeFileSync(join(root, 'src', 'test-helper.js'), source);
      writeFileSync(join(root, 'src', 'fixture.jsx'), source);

      const violations = collectContractStoreGuardViolations({ root, roots: ['src'] });
      expect([...new Set(violations.map(({ file }) => file))].sort()).toEqual([
        'src/fixture-test-helper.jsx',
        'src/fixture.jsx',
        'src/test-helper.js',
      ]);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it('detects store bypasses, fallback compatibility reads, and nondeterministic parse/order calls', () => {
    const source = `
      import { useClientStore } from '../entities/client/model/useClientStore.js';
      const saved = window.localStorage.getItem('ui-state');
      const payload = JSON.parse(saved);
      const id = row?.threadId || row?.thread_id;
      const items = payload.items || [];
      const at = Date.parse(payload.updatedAt);
      const now = Date.now();
      const stamp = new Date(payload.createdAt);
      const sneaky = new Function('text', 'return JSON.parse(text)');
      eval('Date.now()');
      payload.items.sort();
    `;

    expect(contractStoreGuardViolationsInSource('src/pages/files/FilesPage.jsx', source)).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'store-hook-import' }),
      expect.objectContaining({ kind: 'mutable-browser-storage' }),
      expect.objectContaining({ kind: 'json-parse' }),
      expect.objectContaining({ kind: 'compat-field-fallback' }),
      expect.objectContaining({ kind: 'default-value-fallback' }),
      expect.objectContaining({ kind: 'date-parse-order' }),
      expect.objectContaining({ kind: 'date-parse-order' }),
      expect.objectContaining({ kind: 'date-parse-order' }),
      expect.objectContaining({ kind: 'dynamic-code-execution' }),
      expect.objectContaining({ kind: 'dynamic-code-execution' }),
      expect.objectContaining({ kind: 'sort-without-comparator' }),
    ]));
  });

  it('detects globalThis parse/order bypasses', () => {
    const source = `
      const payload = globalThis.JSON.parse(saved);
      const payload2 = globalThis['JSON']['parse'](saved);
      const at = globalThis.Date.parse(payload.updatedAt);
      const at2 = globalThis['Date']['parse'](payload.updatedAt);
      const now = globalThis.Date.now();
      const now2 = globalThis.Date['now']();
      const stamp = new globalThis.Date(payload.createdAt);
      const stamp2 = new globalThis['Date'](payload.createdAt);
    `;

    expect(contractStoreGuardViolationsInSource('src/shared/api/contract.js', source)).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'json-parse', snippet: 'const payload = globalThis.JSON.parse(saved);' }),
      expect.objectContaining({ kind: 'json-parse', snippet: "const payload2 = globalThis['JSON']['parse'](saved);" }),
      expect.objectContaining({ kind: 'date-parse-order', snippet: 'const at = globalThis.Date.parse(payload.updatedAt);' }),
      expect.objectContaining({ kind: 'date-parse-order', snippet: "const at2 = globalThis['Date']['parse'](payload.updatedAt);" }),
      expect.objectContaining({ kind: 'date-parse-order', snippet: 'const now = globalThis.Date.now();' }),
      expect.objectContaining({ kind: 'date-parse-order', snippet: "const now2 = globalThis.Date['now']();" }),
      expect.objectContaining({ kind: 'date-parse-order', snippet: 'const stamp = new globalThis.Date(payload.createdAt);' }),
      expect.objectContaining({ kind: 'date-parse-order', snippet: "const stamp2 = new globalThis['Date'](payload.createdAt);" }),
    ]));
  });

  it('detects bound JSON.parse aliases before invocation', () => {
    const source = `
      const parseJsonValue = globalThis.JSON.parse.bind(globalThis.JSON);
      const { parse: parseJSONPayload } = JSON;
      const payload = parseJsonValue(saved);
      const payload2 = parseJSONPayload(saved);
    `;

    expect(contractStoreGuardViolationsInSource('src/shared/api/contract.js', source)).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'json-parse', snippet: 'const payload = parseJsonValue(saved);' }),
      expect.objectContaining({ kind: 'json-parse', snippet: 'const payload2 = parseJSONPayload(saved);' }),
    ]));
  });

  it('detects new Function wrappers that hide Date or JSON.parse', () => {
    const source = `
      const getNow = new Function('return Date.now()');
      const parsePayload = new Function('value', 'return JSON.parse(value)');
      globalThis.eval('Date.now()');
      globalThis.Function('return JSON.parse(value)');
    `;

    expect(contractStoreGuardViolationsInSource('src/shared/api/contract.js', source)).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'guard-bypass-wrapper', snippet: "const getNow = new Function('return Date.now()');" }),
      expect.objectContaining({ kind: 'guard-bypass-wrapper', snippet: "const parsePayload = new Function('value', 'return JSON.parse(value)');" }),
      expect.objectContaining({ kind: 'dynamic-code-execution', snippet: "globalThis.eval('Date.now()');" }),
      expect.objectContaining({ kind: 'dynamic-code-execution', snippet: "globalThis.Function('return JSON.parse(value)');" }),
    ]));
  });

  it('detects named wrapper helpers around Date and JSON parsing', () => {
    const source = `
      function parseJsonPayload(value) { return JSON.parse(value); }
      const currentTime = () => globalThis.Date.now();
      function dateFromInput(value) { return new globalThis.Date(value); }
    `;

    expect(contractStoreGuardViolationsInSource('src/shared/api/contract.js', source)).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'json-parse', snippet: 'function parseJsonPayload(value) { return JSON.parse(value); }' }),
      expect.objectContaining({ kind: 'guard-bypass-wrapper', snippet: 'function parseJsonPayload(value) { return JSON.parse(value); }' }),
      expect.objectContaining({ kind: 'date-parse-order', snippet: 'const currentTime = () => globalThis.Date.now();' }),
      expect.objectContaining({ kind: 'guard-bypass-wrapper', snippet: 'const currentTime = () => globalThis.Date.now();' }),
      expect.objectContaining({ kind: 'date-parse-order', snippet: 'function dateFromInput(value) { return new globalThis.Date(value); }' }),
      expect.objectContaining({ kind: 'guard-bypass-wrapper', snippet: 'function dateFromInput(value) { return new globalThis.Date(value); }' }),
    ]));
  });

  it('detects helper wrappers that return empty fallback values', () => {
    const source = `
      function emptyArray() { return []; }
      const emptyObject = () => ({});
      function fallbackText(value) { return String(value ?? ''); }
      function defaultArray(value) { return Array.isArray(value) ? value : []; }
    `;

    expect(contractStoreGuardViolationsInSource('src/shared/api/contract.js', source)).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'guard-bypass-wrapper', snippet: 'function emptyArray() { return []; }' }),
      expect.objectContaining({ kind: 'guard-bypass-wrapper', snippet: 'const emptyObject = () => ({});' }),
      expect.objectContaining({ kind: 'guard-bypass-wrapper', snippet: "function fallbackText(value) { return String(value ?? ''); }" }),
      expect.objectContaining({ kind: 'guard-bypass-wrapper', snippet: 'function defaultArray(value) { return Array.isArray(value) ? value : []; }' }),
    ]));
  });

  it('detects storage wrappers that return empty values when storage is missing', () => {
    const source = `
      function getSaved(storage) {
        if (!storage) return '';
        return storage.getItem('value');
      }
      function readSession(windowRef) {
        if (windowRef.sessionStorage == null) {
          return {};
        }
        return windowRef.sessionStorage;
      }
    `;

    expect(contractStoreGuardViolationsInSource('src/shared/api/storage.js', source)).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'guard-bypass-wrapper', snippet: "if (!storage) return '';" }),
      expect.objectContaining({ kind: 'mutable-browser-storage' }),
      expect.objectContaining({ kind: 'guard-bypass-wrapper', snippet: 'if (windowRef.sessionStorage == null) {' }),
      expect.objectContaining({ kind: 'mutable-browser-storage' }),
    ]));
  });

  it('does not flag ordinary business empty-state returns as guard bypass wrappers', () => {
    const source = `
      function selectVisibleItems(items) {
        if (!items.length) return [];
        return items.filter((item) => item.visible);
      }
      const projectRows = (projects) => {
        if (!projects.length) return [];
        return projects.map((project) => project.id);
      };
    `;

    expect(contractStoreGuardViolationsInSource('src/pages/projects/ProjectList.jsx', source)).toEqual([]);
  });

  it('allows only named strict helpers for JSON, clock, and app storage primitives', () => {
    const source = `
      function parseStrictDiagnosticPreviewJSON(value) {
        return JSON.parse(value);
      }
      function createFrontendTraceTimestamp(clock = Date) {
        return new clock().toISOString();
      }
      function initialAppLocale() {
        return window.localStorage.getItem('language');
      }
      function unsafeStorage() {
        return window.localStorage.getItem('other');
      }
    `;

    expect(contractStoreGuardViolationsInSource('src/shared/api/safeDiagnosticPreview.js', source)).toEqual([
      expect.objectContaining({
        kind: 'mutable-browser-storage',
        snippet: "return window.localStorage.getItem('language');",
      }),
      expect.objectContaining({
        kind: 'mutable-browser-storage',
        snippet: "return window.localStorage.getItem('other');",
      }),
    ]);
  });

  it('allows mutable browser storage only for exact file and helper name pairs', () => {
    const allowedSources = new Map([
      ['src/shared/api/browser/browserStorage.js', `
        function requiredAppStoragePort() {
          return window.localStorage.getItem('app');
        }
      `],
      ['src/shared/i18n/appI18n.js', `
        function initialAppLocale() {
          return window.localStorage.getItem('language');
        }
      `],
      ['src/shared/api/wails/wailsBridgeLogRuntime.js', `
        function isFrontendTraceDebugEnabled() {
          return window.localStorage.getItem('trace');
        }
      `],
      ['src/shared/api/wails/wailsBridgeTraceEvents.js', `
        function isFrontendTraceDebugEnabled() {
          return window.localStorage.getItem('trace');
        }
      `],
    ]);
    const allowedViolations = contractStoreGuardViolationsFromSources(allowedSources)
      .filter(({ kind }) => kind === 'mutable-browser-storage');

    expect(allowedViolations).toEqual([]);

    const reusedNames = `
      function initialAppLocale() {
        return window.localStorage.getItem('language');
      }
      function isFrontendTraceDebugEnabled() {
        return window.localStorage.getItem('trace');
      }
      function requiredAppStoragePort() {
        return window.localStorage.getItem('app');
      }
    `;
    const blockedViolations = contractStoreGuardViolationsInSource('src/pages/unsafeStorage.js', reusedNames)
      .filter(({ kind }) => kind === 'mutable-browser-storage');

    expect(blockedViolations).toEqual([
      expect.objectContaining({ snippet: "return window.localStorage.getItem('language');" }),
      expect.objectContaining({ snippet: "return window.localStorage.getItem('trace');" }),
      expect.objectContaining({ snippet: "return window.localStorage.getItem('app');" }),
    ]);
  });

  it('does not allow strict helper names from arbitrary files', () => {
    const source = `
      function parseStrictJsonValue(value) {
        return JSON.parse(value);
      }
      function systemClockMillis() {
        return Date.now();
      }
    `;

    expect(contractStoreGuardViolationsInSource('src/pages/unsafeHelper.js', source)).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'json-parse' }),
      expect.objectContaining({ kind: 'date-parse-order' }),
    ]));
  });

  it('keeps useClientStore centralized in the existing shell owners', () => {
    const source = "import { useClientStore } from './entities/client/model/useClientStore.js';";

    expect(contractStoreGuardViolationsInSource('src/App.jsx', source)).toEqual([]);
    expect(contractStoreGuardViolationsInSource('src/pages/settings/SettingsPage.jsx', source)).toEqual([]);
    expect(contractStoreGuardViolationsInSource('src/pages/chat/ChatPage.jsx', source)).toEqual([
      expect.objectContaining({ kind: 'store-hook-import' }),
    ]);
  });

  it('summarizes ratchet counts and fails only when a category grows past its limit', () => {
    const sources = new Map([
      ['src/pages/files/FilesPage.jsx', `
        const first = data.items || [];
        const second = data.items || [];
        const id = row?.id || row?.thread_id;
      `],
    ]);
    const violations = contractStoreGuardViolationsFromSources(sources);

    expect(summarizeContractStoreGuardViolations(violations)).toEqual(new Map([
      ['default-value-fallback', 2],
      ['compat-field-fallback', 1],
    ]));
    expect(contractStoreGuardRatchetFailures(violations, {
      'compat-field-fallback': 1,
      'default-value-fallback': 1,
    })).toEqual([
      { kind: 'default-value-fallback', count: 2, limit: 1 },
    ]);
  });

  it('detects both dot access and string element access to browser storage', () => {
    const source = `
      const a = window.localStorage.getItem('theme');
      const b = window['localStorage'].getItem('theme');
      const c = window["sessionStorage"].getItem('theme');
    `;
    expect(contractStoreGuardViolationsInSource('src/pages/unsafe.js', source)).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'mutable-browser-storage', snippet: "const a = window.localStorage.getItem('theme');" }),
      expect.objectContaining({ kind: 'mutable-browser-storage', snippet: "const b = window['localStorage'].getItem('theme');" }),
      expect.objectContaining({ kind: 'mutable-browser-storage', snippet: 'const c = window["sessionStorage"].getItem(\'theme\');' }),
    ]));
  });
});
