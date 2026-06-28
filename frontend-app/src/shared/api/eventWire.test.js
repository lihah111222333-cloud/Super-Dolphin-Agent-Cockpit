import { describe, expect, it } from 'vitest';

import {
  EVENT_COMPAT_WIRE_PREFIXES,
  EVENT_RAW_WIRE_METHODS,
  EVENT_RAW_WIRE_PREFIXES,
  EVENT_RAW_WIRE_SUFFIXES,
  EVENT_TYPED_WIRE_METHODS,
  EVENT_WIRE_METHODS,
} from './eventWireMethods.js';
import { asEventWireNotification, isKnownEventWireMethod } from './eventWire.js';

describe('event wire methods', () => {
  it('keeps the compatibility alias on the typed method list', () => {
    expect(EVENT_WIRE_METHODS).toBe(EVENT_TYPED_WIRE_METHODS);
    expect(EVENT_TYPED_WIRE_METHODS).toContain('ui/thread/changed');
    expect(EVENT_TYPED_WIRE_METHODS).toContain('cron/job/runStateChanged');
  });

  it('recognizes typed, raw, and compatibility source-event methods', () => {
    expect(isKnownEventWireMethod('thread/started')).toBe(true);
    expect(isKnownEventWireMethod('error')).toBe(true);
    expect(isKnownEventWireMethod('turn/plan/delta')).toBe(true);
    expect(isKnownEventWireMethod('item/custom/requestApproval')).toBe(true);
    expect(isKnownEventWireMethod('workspace/run/created')).toBe(true);
    expect(isKnownEventWireMethod('unknown/domain/event')).toBe(false);
  });

  it('keeps workspace run outside the raw provider allowlist', () => {
    expect(EVENT_COMPAT_WIRE_PREFIXES).toEqual(['workspace/run/']);
    expect(EVENT_RAW_WIRE_PREFIXES).not.toContain('workspace/run/');
    expect(EVENT_RAW_WIRE_METHODS).not.toContain('workspace/run/created');
    expect(EVENT_RAW_WIRE_SUFFIXES).toEqual(['/requestApproval']);
  });

  it('returns strict notification envelopes', () => {
    const payload = { threadId: 'thread-1' };
    expect(asEventWireNotification('thread/started', payload)).toEqual({
      method: 'thread/started',
      payload,
    });
    expect(() => asEventWireNotification('', payload)).toThrow('event wire method is required');
    expect(() => asEventWireNotification('unknown/domain/event', payload)).toThrow(
      'unknown event wire method: unknown/domain/event',
    );
  });
});
