import { describe, expect, it } from 'vitest';
import { normalizeRegistry } from './ModelProvidersCardModel.js';

describe('ModelProvidersCard registry normalization', () => {
  it('rejects malformed provider registry payloads', () => {
    expect(() => normalizeRegistry(null)).toThrow(/model provider registry/);
  });

  it('normalizes absent optional budget and token pool objects', () => {
    const registry = normalizeRegistry({
      activeVendorId: 'codex',
      vendors: [{ id: 'codex' }],
    });

    expect(registry.vendors[0].budget).toEqual({ dailyUsd: '', monthlyUsd: '' });
    expect(registry.vendors[0].tokenPool).toEqual({ priority: '', fallbackVendorId: '' });
  });
});
