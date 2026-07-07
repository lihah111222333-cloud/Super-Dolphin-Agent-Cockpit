import { describe, expect, it } from 'vitest';
import { normalizeMemorySnapshot } from './memoryAdapter.js';

describe('memoryAdapter', () => {
  it('rejects malformed memory sections instead of normalizing them to empty entries', () => {
    expect(() => normalizeMemorySnapshot({
      overview: {},
      private: null,
      team: { entries: [] },
    })).toThrow(/memory private entries must be an array/);
  });
});
