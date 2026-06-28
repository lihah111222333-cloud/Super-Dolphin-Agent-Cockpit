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

  it('marks missing events as degraded instead of normalizing them to an empty ok result', () => {
    const result = adaptObservabilityResult({
      source: 'memory',
    });

    expect(result).toMatchObject({
      degraded: true,
      parseError: expect.stringContaining('events must be an array'),
      events: [
        expect.objectContaining({
          method: 'observability.events.invalid',
          status: 'error',
        }),
      ],
    });
  });

  it('keeps malformed events visible as parse failures', () => {
    const result = adaptObservabilityResult({
      source: 'memory',
      events: [
        null,
        {
          ts: '2026-06-02T09:01:22.459Z',
          traceId: 'trace-ok',
          method: 'thread/start',
        },
      ],
    });

    expect(result).toMatchObject({
      degraded: true,
      parseError: expect.stringContaining('event[0] must be an object'),
      events: [
        expect.objectContaining({
          method: 'observability.event.parse_failed',
          status: 'error',
        }),
        expect.objectContaining({
          traceId: 'trace-ok',
          method: 'thread/start',
          status: 'unknown',
        }),
      ],
    });
  });
});
