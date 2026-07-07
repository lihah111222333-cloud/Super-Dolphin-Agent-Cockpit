import { describe, expect, it } from 'vitest';
import { normalizeRegistry } from './ModelProvidersCardModel.js';

describe('ModelProvidersCard registry normalization', () => {
  it('rejects malformed provider registry payloads', () => {
    expect(() => normalizeRegistry(null)).toThrow(/model provider registry/);
  });
});
