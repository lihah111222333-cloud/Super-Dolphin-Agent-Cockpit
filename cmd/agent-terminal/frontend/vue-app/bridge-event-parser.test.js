// @ts-nocheck
import { describe, expect, it } from 'vitest';

import {
  normalizeThreadID,
  toNormalizedEventString,
  getBridgeEventThreadId,
  getBridgeEventMethod,
  getBridgeEventType,
  getBridgeEventCommand,
  collectBridgeEventItemKinds,
  isContextCompactionItemKind,
  isCompactCommand,
} from './stores/bridge-event-parser.js';

describe('bridge-event-parser', () => {
  it('normalizes thread ids and event strings', () => {
    expect(normalizeThreadID('  thread-1  ')).toBe('thread-1');
    expect(normalizeThreadID(null)).toBe('');
    expect(toNormalizedEventString('  UI/STATE/CHANGED  ')).toBe('ui/state/changed');
  });

  it('resolves thread id from nested bridge payloads', () => {
    expect(getBridgeEventThreadId({ payload: { item: { threadId: 'thread-payload' } } })).toBe('thread-payload');
    expect(getBridgeEventThreadId({ params: { thread_id: 'thread-params' } })).toBe('thread-params');
    expect(getBridgeEventThreadId({ data: { agent_id: 'agent-1' } })).toBe('agent-1');
    expect(getBridgeEventThreadId({})).toBe('');
  });

  it('picks method type and command using the parser priority order', () => {
    const evt = {
      params: { method: 'ui/thread/changed', type: 'command', cmd: '/compact' },
      payload: { method: 'ignored', type: 'payload-type', command: 'ignored' },
    };

    expect(getBridgeEventMethod(evt)).toBe('ui/thread/changed');
    expect(getBridgeEventType(evt)).toBe('payload-type');
    expect(getBridgeEventCommand(evt)).toBe('/compact');
  });

  it('treats bridge-event top-level type as method fallback', () => {
    expect(getBridgeEventMethod({ type: 'thread/compacted', payload: { threadId: 'thread-live' } })).toBe('thread/compacted');
  });


  it('collects item kinds across payload layers and drops empty values', () => {
    const evt = {
      item: { type: 'command' },
      params: { item: { kind: 'tool' }, type: 'approval' },
      payload: { item: { type: '' }, type: 'file' },
    };

    expect(collectBridgeEventItemKinds(evt)).toEqual(['command', 'tool', 'approval', 'file']);
  });

  it('detects context compaction variants and compact commands', () => {
    expect(isContextCompactionItemKind('context_compaction')).toBe(true);
    expect(isContextCompactionItemKind('Context Compacted')).toBe(true);
    expect(isContextCompactionItemKind('tool')).toBe(false);
    expect(isCompactCommand(' /compact ')).toBe(true);
    expect(isCompactCommand('/other')).toBe(false);
  });
});
