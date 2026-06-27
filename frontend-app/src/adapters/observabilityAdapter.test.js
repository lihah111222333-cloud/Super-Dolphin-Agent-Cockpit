import { describe, expect, it } from 'vitest';
import { adaptObservabilityResult } from './observabilityAdapter.js';

describe('observabilityAdapter', () => {
  it('preserves tail degradation diagnostics during normalization', () => {
    const result = adaptObservabilityResult({
      source: 'memory',
      degraded: true,
      tailError: 'tail reader unavailable',
      tailTimedOut: true,
      tailFilesScanned: 7,
      events: [],
    });

    expect(result).toMatchObject({
      source: 'memory',
      degraded: true,
      tailError: 'tail reader unavailable',
      tailTimedOut: true,
      tailFilesScanned: 7,
    });
  });
});
