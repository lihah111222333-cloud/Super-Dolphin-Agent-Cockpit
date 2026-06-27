import { describe, expect, it } from 'vitest';

import {
  EVENT_BRIDGE_CONTROL_WIRE_METHODS,
  EVENT_BRIDGE_CONTROL_WIRE_SUFFIXES,
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
    expect(isKnownEventWireMethod('rpc.failed')).toBe(true);
    expect(isKnownEventWireMethod('api.rpc.failed')).toBe(true);
    expect(isKnownEventWireMethod('task/node/statuschanged')).toBe(true);
    expect(isKnownEventWireMethod('TASK/NODE/STATUSCHANGED')).toBe(true);
    expect(isKnownEventWireMethod('thread.send/failed')).toBe(true);
    expect(isKnownEventWireMethod('unknown/domain/event')).toBe(false);
  });

  it('keeps compatibility and bridge control methods outside the raw provider allowlist', () => {
    expect(EVENT_COMPAT_WIRE_PREFIXES).toEqual(['workspace/run/']);
    expect(EVENT_BRIDGE_CONTROL_WIRE_METHODS).toEqual(['rpc.failed', 'api.rpc.failed', 'task/node/statuschanged']);
    expect(EVENT_BRIDGE_CONTROL_WIRE_SUFFIXES).toEqual(['/failed', '.failed']);
    expect(EVENT_RAW_WIRE_PREFIXES).not.toContain('workspace/run/');
    expect(EVENT_RAW_WIRE_METHODS).not.toContain('workspace/run/created');
    expect(EVENT_RAW_WIRE_METHODS).not.toContain('rpc.failed');
    expect(EVENT_RAW_WIRE_METHODS).not.toContain('api.rpc.failed');
    expect(EVENT_RAW_WIRE_METHODS).not.toContain('task/node/statuschanged');
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

  it('standardizes typed, raw, and compatibility event envelopes', () => {
    expect(asEventWireNotification({ type: 'thread/started', payload: { threadId: 'thread-1' } })).toEqual({
      method: 'thread/started',
      payload: { threadId: 'thread-1' },
    });
    expect(asEventWireNotification({ method: 'turn/plan/delta', params: { delta: 'step' } })).toEqual({
      method: 'turn/plan/delta',
      payload: { delta: 'step' },
    });
    expect(asEventWireNotification({ type: 'workspace/run/created', data: { runKey: 'run-1' } })).toEqual({
      method: 'workspace/run/created',
      payload: { runKey: 'run-1' },
    });
    expect(asEventWireNotification({ type: 'api.rpc.failed', payload: { method: 'thread/config/get' } })).toEqual({
      method: 'api.rpc.failed',
      payload: { method: 'thread/config/get' },
    });
    expect(asEventWireNotification({ type: 'task/node/statuschanged', payload: { dag_key: 'daily' } })).toEqual({
      method: 'task/node/statuschanged',
      payload: { dag_key: 'daily' },
    });
    expect(() => asEventWireNotification({ type: 'unknown/domain/event', payload: {} })).toThrow(
      'unknown event wire method: unknown/domain/event',
    );
  });
});
