import { describe, expect, it } from 'vitest';
import { adaptObservabilityResult } from './observabilityAdapter.js';
import { parseObservabilityResultResponse } from '../shared/api/backendSchemas.js';

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

  it('rejects observability responses with non-array events at the schema boundary', () => {
    expect(() => parseObservabilityResultResponse({ events: null }))
      .toThrow('observability response events must be an array');
  });

  it('normalizes observability event aliases at the schema boundary', () => {
    const result = parseObservabilityResultResponse({
      source: 'tail',
      total_duration_ms: 12,
      events: [{
        trace_id: 'trace-1',
        span_id: 'span-1',
        duration_ms: 12,
        status: 'ok',
      }],
    });

    expect(result).toMatchObject({
      source: 'tail',
      totalDurationMs: 12,
    });
    expect(result.events[0]).toMatchObject({
      traceId: 'trace-1',
      spanId: 'span-1',
      durationMs: 12,
      status: 'ok',
    });
  });

  it('rejects malformed observability result bodies instead of fabricating degraded events', () => {
    expect(() => adaptObservabilityResult({
      source: 'memory',
      tail: 10,
    })).toThrow('observability response events must be an array');
  });

  it('rejects malformed events instead of fabricating parse failure events', () => {
    expect(() => adaptObservabilityResult({
      source: 'memory',
      events: [
        null,
        {
          ts: '2026-06-02T09:01:22.459Z',
          traceId: 'trace-ok',
          method: 'thread/start',
        },
      ],
    })).toThrow('observability response event[0] must be an object');
  });

  it('normalizes missing event status to unknown without defaulting to ok', () => {
    const result = adaptObservabilityResult({
      source: 'memory',
      events: [{
        ts: '2026-06-02T09:01:22.459Z',
        traceId: 'trace-ok',
        method: 'thread/start',
      }],
    });

    expect(result).toMatchObject({
      events: [
        expect.objectContaining({
          traceId: 'trace-ok',
          method: 'thread/start',
          status: 'unknown',
        }),
      ],
    });
  });
});
