import { afterEach, describe, expect, it, vi } from 'vitest';
import { getObservabilityTrace, listObservabilityRecent } from './observabilityService.js';
import {
  getObservabilityTrace as getObservabilityTraceBackend,
  listObservabilityRecent as listObservabilityRecentBackend,
} from '../../shared/api/backendApi.js';

vi.mock('../../shared/api/backendApi.js', () => ({
  copyTextToClipboard: vi.fn(),
  getObservabilityTrace: vi.fn(),
  listObservabilityRecent: vi.fn(),
}));

describe('observabilityService', () => {
  afterEach(() => {
    vi.clearAllMocks();
  });

  it('keeps degradation fields from recent list normalization', async () => {
    listObservabilityRecentBackend.mockResolvedValue({
      source: 'memory',
      degraded: true,
      tailError: 'tail reader unavailable',
      tailTimedOut: true,
      tailFilesScanned: 3,
      events: [],
    });

    await expect(listObservabilityRecent({ includeTail: true })).resolves.toMatchObject({
      degraded: true,
      tailError: 'tail reader unavailable',
      tailTimedOut: true,
      tailFilesScanned: 3,
    });
  });

  it('keeps degradation fields from trace normalization', async () => {
    getObservabilityTraceBackend.mockResolvedValue({
      source: 'memory',
      degraded: true,
      tailError: 'tail reader unavailable',
      tailTimedOut: false,
      tailFilesScanned: 2,
      events: [],
    });

    await expect(getObservabilityTrace({ traceId: 'trace-1', includeTail: true })).resolves.toMatchObject({
      degraded: true,
      tailError: 'tail reader unavailable',
      tailTimedOut: false,
      tailFilesScanned: 2,
    });
  });
});
