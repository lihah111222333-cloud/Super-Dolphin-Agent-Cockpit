import { describe, expect, it } from 'vitest';
import {
  contractStoreGuardRatchetFailures,
  contractStoreGuardViolationsFromSources,
  contractStoreGuardViolationsInSource,
  summarizeContractStoreGuardViolations,
} from './frontend-contract-store-guard.mjs';

describe('frontend contract/store guard', () => {
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
      payload.items.sort();
    `;

    expect(contractStoreGuardViolationsInSource('src/pages/files/FilesPage.jsx', source)).toEqual([
      expect.objectContaining({ kind: 'store-hook-import' }),
      expect.objectContaining({ kind: 'mutable-browser-storage' }),
      expect.objectContaining({ kind: 'json-parse' }),
      expect.objectContaining({ kind: 'compat-field-fallback' }),
      expect.objectContaining({ kind: 'default-value-fallback' }),
      expect.objectContaining({ kind: 'date-parse-order' }),
      expect.objectContaining({ kind: 'date-parse-order' }),
      expect.objectContaining({ kind: 'date-parse-order' }),
      expect.objectContaining({ kind: 'sort-without-comparator' }),
    ]);
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
});
